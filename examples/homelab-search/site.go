package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
	"github.com/regiellis/hyperchi/modules/markdown"
)

// statusChannel is the SSE channel the index status panel subscribes to.
const statusChannel = "index-status"

// site is the running application: the HyperChi app, the library index, and
// the background work that keeps the index current.
type site struct {
	app     *hyperchi.HyperChi
	ix      *Index
	hub     *sse.SSEHub
	opts    options
	watcher *watcher

	// ctx bounds background work such as rescans and the watcher. prime sets it.
	ctx context.Context
	wg  sync.WaitGroup
}

// newSite builds the app, loads templates, opens the index, and registers
// routes. It starts nothing. Call prime and then launch, or start for both.
func newSite(cfg *config.Config, opts options) (*site, error) {
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		return nil, err
	}
	if app.DB == nil {
		return nil, errors.New("homelab-search keeps its index in SQLite: unset DB_DISABLED")
	}
	if err := app.LoadTemplatesFS(assets, "templates/*.tmpl"); err != nil {
		return nil, errors.Join(err, app.Close())
	}
	ix, err := OpenIndex(app.DB, opts.LibraryDir, opts.MaxFileBytes)
	if err != nil {
		return nil, errors.Join(err, app.Close())
	}
	s := &site{app: app, ix: ix, hub: app.Extensions().GetSSEHub(), opts: opts, ctx: context.Background()}
	s.watcher = newWatcher(ix)
	ix.onStatus = s.publishStatus

	app.Use(app.Middleware.Recovery(), app.Middleware.Logger(), app.Middleware.Security(), app.Middleware.HTMX())
	if opts.AuthUser != "" {
		// Router middleware, so it covers every route registered below,
		// including /events, /static/*, and the framework's script routes. It
		// must come before them: chi refuses middleware added after a route.
		app.BasicAuth(opts.AuthUser, opts.AuthPassword)
	}
	if err := s.routes(cfg.IsDevelopment()); err != nil {
		return nil, errors.Join(err, s.close())
	}
	return s, nil
}

func (s *site) routes(development bool) error {
	app := s.app
	app.SetGlobal("development", development)

	app.Get("/", s.search)
	app.Get("/doc/*", s.document)
	app.Get("/raw/*", s.raw)
	app.Post("/rescan", s.rescan)
	app.Get("/healthz", func(c *hyperchi.Context) error {
		return c.SetHeader("Cache-Control", "no-store").Text("ok\n")
	})

	// The SSE hub's stream handler speaks net/http, so it mounts through the
	// escape hatch. It lifts the server's write deadline for the stream itself.
	app.Handle(http.MethodGet, "/events", s.hub.HTTPHandler())
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return err
	}
	// Development sends no-cache; production caches for an hour.
	if err := app.StaticFS("/static/*", static, hyperchi.StaticOptions{Development: development}); err != nil {
		return err
	}
	// Serves the htmx 4 SSE extension at /hyperchi/js/hx-sse.js.
	app.Extensions().EnableExtensionAssets()
	return nil
}

// start primes the index and launches the watcher. The context bounds both.
// Cancel it before close.
func (s *site) start(ctx context.Context) error {
	if err := s.prime(ctx); err != nil {
		return err
	}
	s.launch(ctx)
	return nil
}

// prime records the watcher's baseline and then reconciles the index with
// the library, in that order, so a file changed between the two is indexed by
// the sync and seen again by the next watch pass rather than missed.
func (s *site) prime(ctx context.Context) error {
	s.ctx = ctx
	if err := s.watcher.baseline(); err != nil {
		log.Printf("homelab-search: watcher baseline: %v", err)
	}
	return s.ix.Sync(ctx)
}

func (s *site) launch(ctx context.Context) {
	s.wg.Go(func() {
		if err := s.watcher.run(ctx, s.opts.WatchInterval); err != nil {
			log.Printf("homelab-search: watcher: %v", err)
		}
	})
}

// close waits for background work (its context must be cancelled) and
// releases the index and the app.
func (s *site) close() error {
	s.wg.Wait()
	return errors.Join(s.ix.Close(), s.app.Close())
}

// page adds what the layout needs (filter values, facets, index status) to a
// handler's data. Every key the layout reads is set, so a query parameter of
// the same name never reaches a template.
func (s *site) page(c *hyperchi.Context, data hyperchi.H) (hyperchi.H, error) {
	types, tags, err := s.ix.Facets(c.Ctx())
	if err != nil {
		return nil, err
	}
	data["types"], data["tags"], data["index"] = types, tags, s.ix.Status()
	for _, key := range []string{"q", "type", "tag", "title"} {
		if _, ok := data[key]; !ok {
			data[key] = ""
		}
	}
	return data, nil
}

// search is the home page and the active-search endpoint. An htmx request
// gets the results fragment for #main, and a browser gets the whole page.
func (s *site) search(c *hyperchi.Context) error {
	q := boundQuery(c.Query("q"))
	typ, tag := c.Query("type"), c.Query("tag")
	if typ != "" && !knownType(typ) {
		return hyperchi.BadRequest("Unknown document type.")
	}
	if tag != "" && normalizeTag(tag) != tag {
		return hyperchi.BadRequest("Tags are lowercase letters, digits, dots, dashes, and underscores.")
	}
	results, err := s.ix.Search(c.Ctx(), SearchParams{Query: q, Type: typ, Tag: tag, Limit: 50})
	if err != nil {
		return err
	}
	title := ""
	if trimmed := strings.TrimSpace(q); trimmed != "" {
		title = trimmed
	}
	data, err := s.page(c, hyperchi.H{"q": strings.TrimSpace(q), "type": typ, "tag": tag, "title": title, "results": results})
	if err != nil {
		return err
	}
	return c.HX().PushURL(searchURL(strings.TrimSpace(q), typ, tag)).View("results", data)
}

func knownType(t string) bool {
	for _, known := range Types {
		if t == known {
			return true
		}
	}
	return false
}

// searchURL is the canonical, shareable URL of a search, without empty parameters.
func searchURL(q, typ, tag string) string {
	v := url.Values{}
	for key, value := range map[string]string{"q": q, "type": typ, "tag": tag} {
		if value != "" {
			v.Set(key, value)
		}
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

// libraryPath reads the document path from a /doc/* or /raw/* URL. It must be
// a clean, relative, slash-separated path with no hidden segments. Anything
// else is a 404, the same answer as a path that is not in the library.
func libraryPath(c *hyperchi.Context) (string, error) {
	rel := c.Param("*")
	if c.Request().URL.RawPath != "" {
		// chi routes on the escaped path when there is one, so unescape here.
		unescaped, err := url.PathUnescape(rel)
		if err != nil {
			return "", hyperchi.NotFound("No such document in the library.")
		}
		rel = unescaped
	}
	if !fs.ValidPath(rel) || rel == "." || strings.Contains(rel, `\`) || isHidden(rel) {
		return "", hyperchi.NotFound("No such document in the library.")
	}
	return rel, nil
}

// lookup returns the indexed document for a /doc/* or /raw/* request.
func (s *site) lookup(c *hyperchi.Context) (Document, error) {
	rel, err := libraryPath(c)
	if err != nil {
		return Document{}, err
	}
	doc, err := s.ix.Lookup(c.Ctx(), rel)
	if errors.Is(err, errNotIndexed) {
		return doc, hyperchi.NotFound("No such document in the library.")
	}
	return doc, err
}

// document shows one file: Markdown rendered with an outline, other text with
// line numbers, and anything else as a description with a download link.
func (s *site) document(c *hyperchi.Context) error {
	doc, err := s.lookup(c)
	if err != nil {
		return err
	}
	data := hyperchi.H{"doc": doc, "title": doc.Title}
	if doc.Searchable {
		src, err := s.ix.readLimited(doc.Path, s.ix.maxBytes)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return hyperchi.NotFound("That file was removed from the library.")
		case err != nil:
			return err
		case int64(len(src)) > s.ix.maxBytes || looksBinary(src):
			// Changed since it was indexed. The watcher will catch up.
			data["note"] = "This file changed and is waiting to be indexed again."
		case doc.Type == TypeMarkdown:
			parsed, err := parseMarkdown(s.ix.md, doc.Path, src)
			if err != nil {
				data["note"] = "The Markdown could not be rendered, so the source is shown."
				data["lines"] = numberLines(src)
				break
			}
			data["html"] = parsed.Doc.HTML
			data["outline"] = outline(parsed.Doc.Headings)
			data["note"] = parsed.FrontMatterNote
			// The page heading already shows the title; hide a first H1 that repeats it.
			if h := parsed.Doc.Headings; len(h) > 0 && h[0].Level == 1 && h[0].Text == doc.Title {
				data["hideFirstH1"] = true
			}
		default:
			data["lines"] = numberLines(src)
		}
	}
	data, err = s.page(c, data)
	if err != nil {
		return err
	}
	return c.View("doc", data)
}

// outline keeps the second to fourth heading levels: the title is already at
// the top of the page, and deeper levels crowd the sidebar.
func outline(headings []markdown.Heading) []markdown.Heading {
	var out []markdown.Heading
	for _, h := range headings {
		if h.Level >= 2 && h.Level <= 4 && h.ID != "" {
			out = append(out, h)
		}
	}
	return out
}

// line is one numbered line of a text file in the code view.
type line struct {
	N    int
	Text string
}

func numberLines(src []byte) []line {
	text := strings.TrimSuffix(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	parts := strings.Split(text, "\n")
	lines := make([]line, len(parts))
	for i, p := range parts {
		lines[i] = line{N: i + 1, Text: p}
	}
	return lines
}

// raw sends a file's bytes with its content type. Text opens in the browser,
// and anything else downloads. Responses are sandboxed by CSP and never sniffed,
// so a library file cannot run script on this origin.
func (s *site) raw(c *hyperchi.Context) error {
	doc, err := s.lookup(c)
	if err != nil {
		return err
	}
	tooLarge := hyperchi.Errorf(http.StatusRequestEntityTooLarge,
		"This file is larger than MAX_RAW_BYTES (%d bytes); open it from the library directory instead.", s.opts.MaxRawBytes)
	if doc.Size > s.opts.MaxRawBytes {
		return tooLarge
	}
	body, err := s.ix.readLimited(doc.Path, s.opts.MaxRawBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return hyperchi.NotFound("That file was removed from the library.")
	case err != nil:
		return err
	case int64(len(body)) > s.opts.MaxRawBytes:
		return tooLarge
	}
	contentType, inline := rawContentType(doc)
	disposition := "attachment"
	if inline && c.Query("download") == "" {
		disposition = "inline"
	}
	if header := mime.FormatMediaType(disposition, map[string]string{"filename": path.Base(doc.Path)}); header != "" {
		c.SetHeader("Content-Disposition", header)
	} else {
		c.SetHeader("Content-Disposition", disposition)
	}
	c.SetHeader("Content-Security-Policy", "sandbox").SetHeader("X-Content-Type-Options", "nosniff")
	return c.Blob(contentType, body)
}

// safeDownloadTypes are the media types a non-text file may be sent as. Any
// other type (HTML, SVG, XML, script) goes out as application/octet-stream.
var safeDownloadTypes = map[string]bool{
	"application/pdf": true, "application/zip": true, "application/gzip": true, "application/x-tar": true,
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"audio/mpeg": true, "audio/ogg": true, "video/mp4": true, "video/webm": true,
}

// rawContentType picks the response type for a file and whether it is shown
// inline. Searchable text is shown, and everything else is a download.
func rawContentType(doc Document) (contentType string, inline bool) {
	if doc.Searchable {
		switch strings.ToLower(path.Ext(doc.Path)) {
		case ".md", ".markdown", ".mdown":
			return "text/markdown; charset=utf-8", true
		case ".json":
			return "application/json; charset=utf-8", true
		case ".yaml", ".yml":
			return "application/yaml; charset=utf-8", true
		case ".toml":
			return "application/toml; charset=utf-8", true
		case ".csv":
			return "text/csv; charset=utf-8", true
		}
		return "text/plain; charset=utf-8", true
	}
	t := mime.TypeByExtension(strings.ToLower(path.Ext(doc.Path)))
	if base, _, err := mime.ParseMediaType(t); err == nil && safeDownloadTypes[base] {
		return base, false
	}
	return "application/octet-stream", false
}

// rescan runs a full scan and answers with the status panel it ended on.
// It runs inside the request so the response is never older than the SSE
// updates it races with. It takes htmx requests only: a cross-site form
// cannot send the HX-Request header, so other sites cannot trigger scans.
func (s *site) rescan(c *hyperchi.Context) error {
	if !c.IsHTMX() {
		return hyperchi.BadRequest("Start a rescan from the page.")
	}
	// The scan belongs to the server, not the request: a closed tab must not
	// leave the index half reconciled.
	if err := s.ix.Sync(s.ctx); err != nil {
		return err
	}
	return c.Fragment("index-status-body", hyperchi.H{"index": s.ix.Status()})
}

// publishStatus renders the status panel and pushes it to every open page.
func (s *site) publishStatus(st Status) {
	html, err := s.app.RenderToString("index-status-body", hyperchi.H{"index": st})
	if err != nil {
		log.Printf("homelab-search: render status: %v", err)
		return
	}
	s.hub.BroadcastSwap(statusChannel, string(html))
}

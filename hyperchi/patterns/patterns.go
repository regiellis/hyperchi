// Package patterns registers ready-made htmx endpoints: LiveFeed and
// ProgressBar push updates over SSE, MultiStepForm runs a wizard whose
// answers live in the user's session, InfiniteScroll and LiveSearch serve
// paged and searched fragments, Polling serves a fragment on each poll, and
// AutoSave stores form drafts.
//
// An app gets its PatternManager from app.Extensions().GetPatternManager().
// Each pattern renders a template registered with RegisterTemplate or loaded
// with the app's LoadTemplates. A pattern that refuses a request (bad input,
// a failing DataFunc, a template error) answers through the host's error
// pipeline when the host provides one, and with response.Error otherwise:
//
//	pm := app.Extensions().GetPatternManager()
//	pm.InfiniteScroll("/items", patterns.InfiniteScrollConfig{
//		DataFunc: loadItems,
//		Template: "items",
//	})
package patterns

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
	"github.com/regiellis/hyperchi/hyperchi/response"
)

// HTTPRouter registers the plain net/http handlers the patterns create. Each
// handler is registered for every method on its pattern. HyperChi passes an
// adapter over its router; a chi.Router also satisfies it.
type HTTPRouter interface {
	HandleFunc(pattern string, handler http.HandlerFunc)
}

// templateLookup is an optional capability of the host app: resolving a template
// by name from the set loaded via LoadTemplates. When the app implements it, the
// pattern manager falls back to app-loaded templates for any name not registered
// through RegisterTemplate, so patterns and application templates share one set.
type templateLookup interface {
	LookupTemplate(name string) *template.Template
}

// statusResponder is an optional capability of the host app: answering a
// refused request through the app's error pipeline, so pattern errors render
// like every other error. Without it the patterns answer with response.Error.
type statusResponder interface {
	RespondStatus(w http.ResponseWriter, r *http.Request, code int, err error)
}

// PatternManager registers pattern endpoints on an app and renders their
// templates. Register patterns and templates during setup: its maps and
// config are not synchronized.
type PatternManager struct {
	app       HTTPRouter
	sseHub    *sse.SSEHub
	templates map[string]*template.Template
	config    PatternConfig
}

// PatternConfig holds the defaults the patterns fall back to.
type PatternConfig struct {
	// DefaultPollInterval is the LiveFeed push interval when
	// LiveFeedConfig.PollInterval is not positive.
	DefaultPollInterval time.Duration `json:"default_poll_interval"`

	// InfiniteScrollPageSize is the page size when
	// InfiniteScrollConfig.PageSize is not positive.
	InfiniteScrollPageSize int `json:"infinite_scroll_page_size"`
}

// NewPatternManager returns a PatternManager that registers routes on app
// and pushes SSE updates through sseHub, with a 5-second poll interval and
// a page size of 20. sseHub may be nil when LiveFeed and ProgressBar are not
// used. Apps usually call app.Extensions().GetPatternManager() instead.
func NewPatternManager(app HTTPRouter, sseHub *sse.SSEHub) *PatternManager {
	return &PatternManager{
		app:       app,
		sseHub:    sseHub,
		templates: make(map[string]*template.Template),
		config: PatternConfig{
			DefaultPollInterval:    5 * time.Second,
			InfiniteScrollPageSize: 20,
		},
	}
}

// LiveFeedConfig configures LiveFeed. DataFunc is required.
type LiveFeedConfig struct {
	Channel      string              `json:"channel"`
	PollInterval time.Duration       `json:"poll_interval"`
	DataFunc     func() (any, error) `json:"-"`
	Template     string              `json:"template"`
	Target       string              `json:"target"`
}

// LiveFeed serves the SSE hub at pattern+"/stream" and the rendered
// Template at pattern, for clients that poll instead. It also starts a
// goroutine that, every PollInterval for the life of the process, renders
// DataFunc's result and broadcasts it on Channel as an unnamed SSE message,
// which the htmx 4 SSE extension swaps into the target. Errors from
// DataFunc or the template skip that tick.
//
//	pm.LiveFeed("/live-feed", patterns.LiveFeedConfig{
//		Channel: "dashboard-updates",
//		DataFunc: func() (any, error) {
//			return getDashboardData(), nil
//		},
//		Template: "dashboard-feed.html",
//	})
func (pm *PatternManager) LiveFeed(pattern string, config LiveFeedConfig) {
	pm.app.HandleFunc(pattern+"/stream", func(w http.ResponseWriter, r *http.Request) {
		pm.sseHub.HTTPHandler()(w, r)
	})

	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		data, err := config.DataFunc()
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Failed to load data", err)
			return
		}

		html, err := pm.renderTemplate(config.Template, data)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Template error", err)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html)) // client may have disconnected
	})

	go pm.liveFeedUpdater(config)
}

// liveFeedUpdater pushes a rendered update on each tick. It never returns.
func (pm *PatternManager) liveFeedUpdater(config LiveFeedConfig) {
	// time.NewTicker panics on a non-positive duration, and PollInterval is
	// optional, so fall back to the configured default.
	interval := config.PollInterval
	if interval <= 0 {
		interval = pm.config.DefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		data, err := config.DataFunc()
		if err != nil {
			continue
		}

		html, err := pm.renderTemplate(config.Template, data)
		if err != nil {
			continue
		}

		// An unnamed message, because the htmx 4 SSE extension swaps it into
		// the target. A named event would be dispatched, not swapped.
		pm.sseHub.BroadcastSwap(config.Channel, html)
	}
}

// ProgressBarConfig configures ProgressBar. ProgressFunc is required and
// returns a percentage.
type ProgressBarConfig struct {
	TaskID       string                           `json:"task_id"`
	Channel      string                           `json:"channel"`
	ProgressFunc func(taskID string) (int, error) `json:"-"`
	Template     string                           `json:"template"`
}

// ProgressBar serves Template at pattern with "progress" and "task_id" for
// TaskID. It also starts a goroutine that renders the same data every second
// and broadcasts it on Channel as an unnamed SSE message, stopping once
// progress reaches 100. If progress never reaches 100, the goroutine runs
// for the life of the process.
//
//	pm.ProgressBar("/upload-progress", patterns.ProgressBarConfig{
//		TaskID: "upload-123",
//		Channel: "upload-progress",
//		ProgressFunc: func(taskID string) (int, error) {
//			return getUploadProgress(taskID), nil
//		},
//	})
func (pm *PatternManager) ProgressBar(pattern string, config ProgressBarConfig) {
	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		progress, err := config.ProgressFunc(config.TaskID)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Failed to get progress", err)
			return
		}

		data := map[string]any{
			"progress": progress,
			"task_id":  config.TaskID,
		}

		html, err := pm.renderTemplate(config.Template, data)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Template error", err)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html)) // client may have disconnected
	})

	go pm.progressUpdater(config)
}

// progressUpdater pushes the rendered progress every second until it
// reaches 100.
func (pm *PatternManager) progressUpdater(config ProgressBarConfig) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		progress, err := config.ProgressFunc(config.TaskID)
		if err != nil {
			continue
		}

		data := map[string]any{
			"progress": progress,
			"task_id":  config.TaskID,
		}

		html, err := pm.renderTemplate(config.Template, data)
		if err != nil {
			continue
		}

		// Unnamed, so the htmx 4 SSE extension swaps it in.
		pm.sseHub.BroadcastSwap(config.Channel, html)

		if progress >= 100 {
			break
		}
	}
}

// InfiniteScrollConfig configures InfiniteScroll. DataFunc is required.
// HasMoreFunc is optional: without it, a page is assumed to have a
// successor when DataFunc returned a full slice.
type InfiniteScrollConfig struct {
	DataFunc    func(page int, pageSize int) (any, error) `json:"-"`
	Template    string                                    `json:"template"`
	PageSize    int                                       `json:"page_size"`
	HasMoreFunc func(page int) bool                       `json:"-"`
}

// InfiniteScroll serves page ?page=N (default 1) of DataFunc's results at
// pattern, rendering Template with "data", "page", and "has_more". PageSize
// falls back to PatternConfig.InfiniteScrollPageSize.
func (pm *PatternManager) InfiniteScroll(pattern string, config InfiniteScrollConfig) {
	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		pageParam := r.URL.Query().Get("page")
		page, err := strconv.Atoi(pageParam)
		if err != nil || page < 1 {
			page = 1
		}

		pageSize := config.PageSize
		if pageSize <= 0 {
			pageSize = pm.config.InfiniteScrollPageSize
		}

		data, err := config.DataFunc(page, pageSize)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Failed to load data", err)
			return
		}

		// Without HasMoreFunc, assume more pages remain when the returned slice
		// filled this one.
		hasMore := false
		if config.HasMoreFunc != nil {
			hasMore = config.HasMoreFunc(page)
		} else if v := reflect.ValueOf(data); v.Kind() == reflect.Slice {
			hasMore = v.Len() >= pageSize
		}

		templateData := map[string]any{
			"data":     data,
			"page":     page,
			"has_more": hasMore,
		}

		html, err := pm.renderTemplate(config.Template, templateData)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Template error", err)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html)) // client may have disconnected
	})
}

// LiveSearchConfig configures LiveSearch. SearchFunc is required.
// MaxResults defaults to 10.
type LiveSearchConfig struct {
	SearchFunc  func(query string, limit int) (any, error) `json:"-"`
	Template    string                                     `json:"template"`
	MinQueryLen int                                        `json:"min_query_length"`
	MaxResults  int                                        `json:"max_results"`
}

// LiveSearch serves search results for ?q= at pattern, rendering Template
// with "query" and "results". A query shorter than MinQueryLen bytes returns
// an empty body without calling SearchFunc. The server does not debounce;
// add a delay to the input's hx-trigger.
func (pm *PatternManager) LiveSearch(pattern string, config LiveSearchConfig) {
	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")

		if len(query) < config.MinQueryLen {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("")) // client may have disconnected
			return
		}

		maxResults := config.MaxResults
		if maxResults <= 0 {
			maxResults = 10
		}

		results, err := config.SearchFunc(query, maxResults)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Search failed", err)
			return
		}

		templateData := map[string]any{
			"query":   query,
			"results": results,
		}

		html, err := pm.renderTemplate(config.Template, templateData)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Template error", err)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html)) // client may have disconnected
	})
}

// PollingConfig configures Polling. DataFunc is required. The client sets
// the poll rate in the element's hx-trigger, for example "every 2s".
type PollingConfig struct {
	DataFunc    func() (any, error) `json:"-"`
	Template    string              `json:"template"`
	MaxDuration time.Duration       `json:"max_duration"`
}

// Polling serves DataFunc's result rendered with Template at pattern. When
// the request carries an X-Poll-Start header (RFC 3339) older than
// MaxDuration, the response also triggers stopPolling; the client must send
// that header and stop on the event.
func (pm *PatternManager) Polling(pattern string, config PollingConfig) {
	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		data, err := config.DataFunc()
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Failed to load data", err)
			return
		}

		html, err := pm.renderTemplate(config.Template, data)
		if err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Template error", err)
			return
		}

		startTime := r.Header.Get("X-Poll-Start")
		if startTime != "" {
			if start, err := time.Parse(time.RFC3339, startTime); err == nil {
				if time.Since(start) > config.MaxDuration {
					w.Header().Set("HX-Trigger", "stopPolling")
				}
			}
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html)) // client may have disconnected
	})
}

// AutoSaveConfig configures AutoSave. SaveFunc is required.
type AutoSaveConfig struct {
	SaveFunc func(data map[string]any) error `json:"-"`
	DraftKey string                          `json:"draft_key"`
}

// AutoSave accepts POSTs at pattern, passes the parsed form to SaveFunc, and
// answers "Draft saved" with an autoSaved trigger carrying a Unix timestamp
// and DraftKey. Other methods get 405. A repeated field is passed as a
// []string, a single one as a string.
func (pm *PatternManager) AutoSave(pattern string, config AutoSaveConfig) {
	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Without ParseForm, r.Form is nil and the draft would be saved empty.
		if err := r.ParseForm(); err != nil {
			pm.fail(w, r, http.StatusBadRequest, "Invalid form data", err)
			return
		}

		formData := make(map[string]any)
		for key, values := range r.Form {
			if len(values) == 1 {
				formData[key] = values[0]
			} else {
				formData[key] = values
			}
		}

		if err := config.SaveFunc(formData); err != nil {
			pm.fail(w, r, http.StatusInternalServerError, "Auto-save failed", err)
			return
		}

		// client may have disconnected
		_, _ = response.New(w).
			Status(http.StatusOK).
			Trigger(map[string]any{
				"autoSaved": map[string]any{
					"timestamp": time.Now().Unix(),
					"draft_key": config.DraftKey,
				},
			}).
			WriteString("Draft saved")
	})
}

// fail answers a refused request with code. With a host error pipeline the
// pipeline renders it and message and cause are logged; otherwise
// response.Error sends message, which is meant for the client, so cause never
// reaches it, and a 5xx cause is logged here.
func (pm *PatternManager) fail(w http.ResponseWriter, r *http.Request, code int, message string, cause error) {
	if host, ok := pm.app.(statusResponder); ok {
		host.RespondStatus(w, r, code, fmt.Errorf("patterns: %s: %w", message, cause))
		return
	}
	if code >= http.StatusInternalServerError {
		log.Printf("patterns: %s %s: %s: %v", r.Method, r.URL.Path, message, cause)
	}
	response.Error(w, code, message)
}

// renderTemplate renders templateName with data. It looks the name up in the
// RegisterTemplate set first, then in the host app's LoadTemplates set when
// the app supports lookup, so patterns can use templates the app already
// loaded.
func (pm *PatternManager) renderTemplate(templateName string, data any) (string, error) {
	if tmpl, exists := pm.templates[templateName]; exists {
		var buf strings.Builder
		if err := tmpl.Execute(&buf, data); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	if lookup, ok := pm.app.(templateLookup); ok {
		if appTmpl := lookup.LookupTemplate(templateName); appTmpl != nil {
			var buf strings.Builder
			if err := appTmpl.Execute(&buf, data); err != nil {
				return "", err
			}
			return buf.String(), nil
		}
	}

	return "", fmt.Errorf("template %s not found (register it with RegisterTemplate or load it with LoadTemplates)", templateName)
}

// RegisterTemplate makes tmpl available to the patterns under name. It wins
// over an app template with the same name. Call it during setup.
func (pm *PatternManager) RegisterTemplate(name string, tmpl *template.Template) {
	pm.templates[name] = tmpl
}

// GetConfig returns a copy of the pattern configuration.
func (pm *PatternManager) GetConfig() PatternConfig {
	return pm.config
}

// SetConfig replaces the pattern configuration. Call it during setup: the
// configuration is read by request handlers without locking.
func (pm *PatternManager) SetConfig(config PatternConfig) {
	pm.config = config
}

package main

import (
	"fmt"
	"io/fs"
	"mime"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Static files are embedded (see main.go) and served by app.StaticFS, which
// refuses directories and dotfiles, gives each file a content-hash ETag for
// conditional requests, and sends a miss through the app's error pipeline.
// Templates link them with the built-in asset function,
// {{asset "/static/css/site.css"}}, which adds a ?v= content version that
// StaticFS answers with a year-long immutable Cache-Control (any other request
// gets an hour). A path that names no file fails the render, so a typo shows
// up in tests and at pre-render time rather than as a broken page.

// extraTypes covers extensions some systems' MIME tables lack. mountStatic
// registers them, so StaticFS sends the same type on every host.
var extraTypes = map[string]string{
	".woff2": "font/woff2",
	".svg":   "image/svg+xml",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".png":   "image/png",
}

// mountStatic serves root in fsys at /static/*. Development sends no-cache and
// re-hashes changed files for the asset function.
func mountStatic(app *hyperchi.HyperChi, fsys fs.FS, root string, dev bool) error {
	for ext, ctype := range extraTypes {
		if err := mime.AddExtensionType(ext, ctype); err != nil {
			return fmt.Errorf("static: register %s: %w", ext, err)
		}
	}
	sub, err := fs.Sub(fsys, root)
	if err != nil {
		return err
	}
	return app.StaticFS("/static/*", sub, hyperchi.StaticOptions{Development: dev})
}

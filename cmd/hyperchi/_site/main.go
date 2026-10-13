package main

import (
	"errors"
	"log"
	"net/http"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	app, err := hyperchi.NewWithConfigE(config.Minimal())
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()
	if err := app.LoadTemplates("templates/*.tmpl"); err != nil {
		return err
	}

	// Every error status wears the branded page, rendered once at startup.
	statuses := map[int]hyperchi.Handler{}
	for _, code := range []int{400, 403, 404, 405, 413, 415, 429, 500} {
		h, err := app.PreRender("error", hyperchi.H{"code": code, "message": http.StatusText(code)}, hyperchi.PreRenderOptions{Status: code})
		if err != nil {
			return err
		}
		statuses[code] = h
	}
	// A status without a page here falls back to the framework's DefaultError.
	app.OnError(func(c *hyperchi.Context, err error) {
		code := http.StatusInternalServerError
		var he *hyperchi.HTTPError
		if errors.As(err, &he) {
			code = he.Code
		}
		page, ok := statuses[code]
		if !ok {
			c.App().DefaultError(c, err)
			return
		}
		if perr := page(c); perr != nil {
			log.Printf("error page %d: %v", code, perr)
		}
	})

	app.AutoMiddleware()
	if err := app.PreRenderedPage("/", "index", hyperchi.H{"title": "__APPNAME__"}, hyperchi.PreRenderOptions{Layout: "base"}); err != nil {
		return err
	}
	if err := app.StaticWithOptions("/static/*", "static", hyperchi.StaticOptions{Development: app.Config.IsDevelopment()}); err != nil {
		return err
	}
	app.Get("/healthz", func(c *hyperchi.Context) error {
		return c.SetHeader("Cache-Control", "no-store").Text("ok\n")
	})
	return app.Serve(app.Config.Server.Address())
}

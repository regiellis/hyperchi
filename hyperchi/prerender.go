package hyperchi

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html/template"
	"net/http"
	"time"
)

// PreRenderOptions describes a request-independent document. Layout is the
// layout's short name ("base"); an empty Layout renders the template alone.
type PreRenderOptions struct {
	Layout       string
	Status       int
	CacheControl string
	Headers      http.Header
}

// PreRender renders a page, with app globals and its optional layout, once
// before traffic is served, and returns a Handler that serves the immutable
// result with ETag validation, conditional requests, and HEAD support. A
// template error is returned now instead of on the first request. Error
// documents (Status 400-599) carry no validators and are never cached.
//
//	home, err := app.PreRender("index", hyperchi.H{"title": "Home"}, hyperchi.PreRenderOptions{Layout: "base"})
//	if err != nil {
//		return err
//	}
//	app.Get("/", home)
func (g *HyperChi) PreRender(name string, data any, options PreRenderOptions) (Handler, error) {
	page, err := g.renderFragment(nil, name, data)
	if err != nil {
		return nil, err
	}
	if options.Layout != "" && options.Layout != "none" {
		values := H{}
		switch v := data.(type) {
		case H:
			for key, value := range v {
				values[key] = value
			}
		case map[string]any:
			for key, value := range v {
				values[key] = value
			}
		case nil:
		default:
			values["Result"] = data
		}
		values["content"] = template.HTML(page)
		page, err = g.renderFragment(nil, "layout-"+options.Layout, values)
		if err != nil {
			return nil, fmt.Errorf("prerender layout: %w", err)
		}
	}
	status := options.Status
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK && (status < 400 || status > 599) {
		return nil, fmt.Errorf("prerender status must be 200 or an error status")
	}
	body := []byte(page)
	headers := options.Headers.Clone()
	cache := options.CacheControl
	if cache == "" {
		cache = "no-cache"
	}
	tag := fmt.Sprintf("W/\"%x\"", sha256.Sum256(body))
	return func(c *Context) error {
		for k, values := range headers {
			c.w.Header()[k] = append([]string(nil), values...)
		}
		if status >= 400 {
			c.SetHeader("Cache-Control", "no-store").SetHeader("X-Robots-Tag", "noindex")
			return c.Status(status).HTML(template.HTML(body))
		}
		c.SetHeader("Content-Type", "text/html; charset=utf-8").SetHeader("Cache-Control", cache).SetHeader("ETag", tag)
		http.ServeContent(c.w, c.r, "", time.Time{}, bytes.NewReader(body))
		return nil
	}, nil
}

// PreRenderedPage pre-renders a page and registers it for GET and HEAD, only
// after the document renders successfully.
func (g *HyperChi) PreRenderedPage(pattern, name string, data any, options PreRenderOptions) error {
	h, err := g.PreRender(name, data, options)
	if err != nil {
		return err
	}
	g.Get(pattern, h)
	g.Head(pattern, h)
	return nil
}

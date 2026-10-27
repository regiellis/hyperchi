package hyperchi

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/analytics"
)

//go:embed extensions/scripts/hyperchi-analytics.js
var analyticsJS []byte

// EnableAnalytics installs request hooks once on the root app, before routes.
// Framework script and developer endpoints are excluded. The optional browser
// runtime is served locally; {{analyticsScript}} includes it but collection
// stays off until application code calls HyperChiAnalytics.setEnabled(true).
func (g *HyperChi) EnableAnalytics(config analytics.Config) error {
	if g.routePrefix != "" || g.analyticsEnabled || len(g.router.Routes()) != 0 {
		return fmt.Errorf("enable analytics once on the root app before registering routes")
	}
	include := config.Include
	config.Include = func(r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/__hyperchi/") || strings.HasPrefix(r.URL.Path, "/hyperchi/js/") {
			return false
		}
		return include == nil || include(r)
	}
	collector, err := analytics.New(config)
	if err != nil {
		return err
	}
	g.router.Use(collector.Middleware)
	g.analyticsEnabled = true
	tag := fmt.Sprintf(`"%x"`, sha256.Sum256(analyticsJS))
	g.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/hyperchi/js/analytics.js" {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("ETag", tag)
			http.ServeContent(w, r, "analytics.js", time.Time{}, bytes.NewReader(analyticsJS))
		})
	})
	return nil
}

// AnalyticsScript returns the external analytics runtime script tag when
// enabled, or empty HTML. Load it before application analytics setup code.
func (g *HyperChi) AnalyticsScript() template.HTML {
	if !g.analyticsEnabled {
		return ""
	}
	return `<script defer src="/hyperchi/js/analytics.js"></script>`
}

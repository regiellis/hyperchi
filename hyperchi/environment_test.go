package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// ValidateConfig and config.Validate apply one environment rule: a value
// ValidateConfig accepts is one FromEnv turns into a valid configuration.
func TestValidateConfigSharesTheEnvironmentRule(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("LOG_LEVEL", "")
	app := noDBApp(t)
	for _, env := range []string{"development", "dev", "production", "prod", "test", "testing", "staging", "Production"} {
		for _, variable := range []string{"ENVIRONMENT", "GO_ENV"} {
			t.Setenv("ENVIRONMENT", "")
			t.Setenv("GO_ENV", "")
			t.Setenv(variable, env)

			envRejected := false
			for _, err := range app.ValidateConfig() {
				if strings.Contains(err.Error(), "environment") || strings.Contains(err.Error(), "ENVIRONMENT") || strings.Contains(err.Error(), "GO_ENV") {
					envRejected = true
				}
			}
			configRejected := config.FromEnv().Validate() != nil
			if envRejected != configRejected {
				t.Errorf("%s=%q: ValidateConfig rejected=%v, FromEnv().Validate rejected=%v", variable, env, envRejected, configRejected)
			}
		}
	}
}

// Production's log says the /_debug endpoints are off; they answer 404 once
// the app is in production, whether Debug ran before or after.
func TestProductionDisablesDebugEndpoints(t *testing.T) {
	get := func(app *HyperChi, path string) int {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code
	}
	paths := []string{"/_debug/status", "/_debug/metrics", "/_debug/cache", "/_debug/events"}

	dev := noDBApp(t)
	dev.Debug()
	for _, path := range paths {
		if code := get(dev, path); code != http.StatusOK {
			t.Errorf("development %s = %d, want 200", path, code)
		}
	}

	prod := noDBApp(t)
	prod.Production()
	prod.Debug()
	for _, path := range paths {
		if code := get(prod, path); code != http.StatusNotFound {
			t.Errorf("production %s = %d, want 404", path, code)
		}
	}

	// Switching to production after Debug also turns them off.
	dev.Configure().Env("production")
	for _, path := range paths {
		if code := get(dev, path); code != http.StatusNotFound {
			t.Errorf("%s after switching to production = %d, want 404", path, code)
		}
	}
}

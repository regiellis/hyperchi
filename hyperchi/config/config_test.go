package config

import "testing"

func TestEnvironmentPrecedence(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("GO_ENV", "")
	t.Setenv("PRODUCTION", "true")
	if !FromEnv().IsProduction() {
		t.Fatal("legacy PRODUCTION flag ignored")
	}
	t.Setenv("ENVIRONMENT", "development")
	if !FromEnv().IsDevelopment() {
		t.Fatal("explicit environment lost precedence")
	}
	cfg := Production()
	cfg.Security.TrustedProxies = []string{"not-a-cidr"}
	if cfg.Validate() == nil {
		t.Fatal("invalid proxy accepted")
	}
	var missing *Config
	if missing.Validate() == nil {
		t.Fatal("nil config accepted")
	}
	cfg = Minimal()
	if !cfg.Database.Disabled || cfg.Cache.Enabled || cfg.Assets.HotReload {
		t.Fatal("minimal preset starts optional services")
	}
}

// One environment rule: FromEnv maps the accepted aliases to the canonical
// name, and Validate accepts only the canonical name, which is what
// IsDevelopment and IsProduction compare.
func TestParseEnvironment(t *testing.T) {
	for env, want := range map[string]string{"development": "development", "dev": "development", "production": "production"} {
		got, err := ParseEnvironment(env)
		if err != nil || got != want {
			t.Errorf("ParseEnvironment(%q) = %q, %v; want %q", env, got, err, want)
		}
		t.Setenv("ENVIRONMENT", env)
		if cfg := FromEnv(); cfg.Server.Environment != want || cfg.Validate() != nil {
			t.Errorf("ENVIRONMENT=%q: FromEnv environment %q, Validate %v", env, cfg.Server.Environment, cfg.Validate())
		}
	}
	for _, env := range []string{"prod", "test", "staging", "Production", ""} {
		if _, err := ParseEnvironment(env); err == nil {
			t.Errorf("ParseEnvironment(%q) accepted", env)
		}
	}
	cfg := Default()
	cfg.Server.Environment = "dev"
	if cfg.Validate() == nil {
		t.Error(`Validate accepted "dev", which IsDevelopment does not recognize`)
	}
}

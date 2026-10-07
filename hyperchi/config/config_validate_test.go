package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

// Production validates without a secret key: an app that signs nothing must
// not be forced to configure a secret that protects nothing. Signing calls
// fail at runtime instead (hyperchi.ErrNoSecretKey).
func TestProductionValidatesWithoutAnApplicationSecret(t *testing.T) {
	if err := Production().Validate(); err != nil {
		t.Fatalf("production config rejected: %v", err)
	}
}

func TestSecretKeyMinimumLength(t *testing.T) {
	long := strings.Repeat("k", MinSecretKeyLength)
	cases := []struct {
		name     string
		current  string
		previous []string
		ok       bool
	}{
		{"unset", "", nil, true},
		{"32 bytes", long, nil, true},
		{"31 bytes", long[1:], nil, false},
		{"short previous", long, []string{"short"}, false},
		{"previous without current", "", []string{long}, false},
		{"rotation", long, []string{long + "old"}, true},
	}
	for _, tc := range cases {
		cfg := Production()
		cfg.Security.SecretKey = tc.current
		cfg.Security.SecretKeyPrevious = tc.previous
		err := cfg.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tc.name, err, tc.ok)
		}
		if err != nil && (strings.Contains(err.Error(), tc.current) && tc.current != "") {
			t.Errorf("%s: error message contains the key: %v", tc.name, err)
		}
	}
}

func TestSecretKeyFromEnv(t *testing.T) {
	current := strings.Repeat("c", MinSecretKeyLength)
	old1 := strings.Repeat("a", MinSecretKeyLength)
	old2 := strings.Repeat("b", MinSecretKeyLength)
	t.Setenv("SECRET_KEY", current)
	t.Setenv("SECRET_KEY_PREVIOUS", old1+", "+old2+",")
	cfg := FromEnv()
	if cfg.Security.SecretKey != current {
		t.Fatal("SECRET_KEY not read")
	}
	if !reflect.DeepEqual(cfg.Security.SecretKeyPrevious, []string{old1, old2}) {
		t.Fatalf("SECRET_KEY_PREVIOUS = %d keys, want 2", len(cfg.Security.SecretKeyPrevious))
	}
}

// The keys never leave the process through JSON or fmt formatting.
func TestSecretKeyIsNeverSerialized(t *testing.T) {
	cfg := Default()
	cfg.Security.SecretKey = "current-secret-" + strings.Repeat("x", MinSecretKeyLength)
	cfg.Security.SecretKeyPrevious = []string{"retired-secret-" + strings.Repeat("y", MinSecretKeyLength)}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{
		"json": string(body),
		"%v":   fmt.Sprintf("%v", cfg),
		"%+v":  fmt.Sprintf("%+v", cfg),
		"%#v":  fmt.Sprintf("%#v", cfg),
		"sec":  fmt.Sprintf("%v %+v %#v %s", cfg.Security, cfg.Security, cfg.Security, cfg.Security),
	}
	for name, out := range outputs {
		if strings.Contains(out, "current-secret") || strings.Contains(out, "retired-secret") {
			t.Errorf("%s output contains a secret key: %s", name, out)
		}
	}
	if !strings.Contains(outputs["%+v"], "[redacted]") {
		t.Errorf("%%+v does not show the key as redacted: %s", outputs["%+v"])
	}
}

// DSN is the SQLite path, and Validate rejects other database types: no other
// driver ships.
func TestDSNIsTheSQLitePath(t *testing.T) {
	cfg := Default()
	cfg.Database.Name = "/var/lib/app/app.db"
	if got := cfg.Database.DSN(); got != "/var/lib/app/app.db" {
		t.Fatalf("DSN = %q", got)
	}
	cfg.Database.Type = "postgres"
	if cfg.Validate() == nil {
		t.Fatal("postgres accepted although no driver ships")
	}
}

// Config carries no yaml tags: nothing loads YAML, so a tag would advertise a
// loader that does not exist.
func TestConfigHasNoYAMLTags(t *testing.T) {
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		for i := range typ.NumField() {
			field := typ.Field(i)
			if _, ok := field.Tag.Lookup("yaml"); ok {
				t.Errorf("%s.%s has a yaml tag but nothing loads YAML", typ.Name(), field.Name)
			}
			if field.Type.Kind() == reflect.Struct && field.Type.PkgPath() == typ.PkgPath() {
				walk(field.Type)
			}
		}
	}
	walk(reflect.TypeFor[Config]())
}

// FrameworkVersion reads the module version from build info, the one source
// the logging package and the health endpoints report.
func TestFrameworkVersionFromBuildInfo(t *testing.T) {
	cases := map[string]struct {
		info *debug.BuildInfo
		want string
	}{
		"dependency": {&debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "(devel)"},
			Deps: []*debug.Module{{Path: modulePath, Version: "v0.2.0"}},
		}, "v0.2.0"},
		"replaced with a version": {&debug.BuildInfo{
			Deps: []*debug.Module{{Path: modulePath, Version: "v0.1.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v0.1.1"}}},
		}, "v0.1.1"},
		"replaced with a local checkout": {&debug.BuildInfo{
			Deps: []*debug.Module{{Path: modulePath, Version: "v0.1.0", Replace: &debug.Module{Path: "../hyperchi"}}},
		}, develVersion},
		"framework itself": {&debug.BuildInfo{
			Main: debug.Module{Path: modulePath, Version: "v0.2.0"},
		}, "v0.2.0"},
		"unrelated binary": {&debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"},
		}, develVersion},
	}
	for name, tc := range cases {
		if got := frameworkVersionFrom(tc.info); got != tc.want {
			t.Errorf("%s: version = %q, want %q", name, got, tc.want)
		}
	}
	if FrameworkVersion() == "2.0.0" || FrameworkVersion() == "" {
		t.Fatalf("FrameworkVersion() = %q", FrameworkVersion())
	}
}

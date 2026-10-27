package helpers

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tomlValue returns the raw right-hand side of `key = ...` in the first
// section that defines key.
func tomlValue(t *testing.T, doc, key string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if v, ok := strings.CutPrefix(line, key+" = "); ok {
			return v
		}
	}
	t.Fatalf("key %q not found in:\n%s", key, doc)
	return ""
}

// tomlStrings decodes a TOML array of basic strings. For the ASCII content
// the Air config uses, TOML basic-string escapes coincide with Go's.
func tomlStrings(t *testing.T, raw string) []string {
	t.Helper()
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	var out []string
	for _, item := range strings.Split(raw, ", ") {
		s, err := strconv.Unquote(item)
		if err != nil {
			t.Fatalf("invalid TOML string %s: %v", item, err)
		}
		out = append(out, s)
	}
	return out
}

func TestAirConfigTOMLEscapesStrings(t *testing.T) {
	cfg := HyperChiAirConfig()
	cfg.Build.PreCmd = []string{`echo "pre"`}
	cfg.Build.Args = []string{"--port", "8080"}
	cfg.Build.FullBin = `APP_ENV=dev ./tmp/main`
	doc := cfg.ToTOML()

	// The default regexes contain backslashes, which must be escaped or
	// the generated file is not valid TOML.
	if got := tomlStrings(t, tomlValue(t, doc, "exclude_regex")); !slices.Equal(got, cfg.Build.ExcludeRegex) {
		t.Errorf("exclude_regex decodes to %q, want %q", got, cfg.Build.ExcludeRegex)
	}
	if got := tomlStrings(t, tomlValue(t, doc, "pre_cmd")); !slices.Equal(got, cfg.Build.PreCmd) {
		t.Errorf("pre_cmd = %q", got)
	}
	if got := tomlStrings(t, tomlValue(t, doc, "args")); !slices.Equal(got, cfg.Build.Args) {
		t.Errorf("args = %q", got)
	}
	if got := tomlStrings(t, tomlValue(t, doc, "include_ext")); !slices.Contains(got, "gohtml") || !slices.Contains(got, "env") {
		t.Errorf("include_ext = %q", got)
	}
	for key, want := range map[string]string{"root": ".", "cmd": cfg.Build.Cmd, "full_bin": cfg.Build.FullBin, "kill_delay": "0s"} {
		if got := tomlStrings(t, tomlValue(t, doc, key)); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	for _, section := range []string{"[build]", "[color]", "[log]", "[misc]"} {
		if !strings.Contains(doc, "\n"+section+"\n") {
			t.Errorf("missing section %s", section)
		}
	}

	if got := tomlString("tab\tnl\nbell\x07"); got != `"tab\tnl\nbell\u0007"` {
		t.Errorf("tomlString control characters = %s", got)
	}
}

func TestAirConfigDefaults(t *testing.T) {
	def := DefaultAirConfig()
	hc := HyperChiAirConfig()
	if def.Root != "." || def.Build.Bin != "tmp/main" || slices.Contains(def.Build.IncludeExt, "gohtml") {
		t.Error("DefaultAirConfig")
	}
	if !slices.Contains(hc.Build.ExcludeDir, "uploads") || !slices.Contains(hc.Watch.IncludeExt, "gotmpl") {
		t.Error("HyperChiAirConfig extends the defaults")
	}
	minimal := DefaultAirConfig()
	minimal.Build.ExcludeDir, minimal.Build.ExcludeFile, minimal.Build.ExcludeRegex, minimal.Build.IncludeExt = nil, nil, nil, nil
	doc := minimal.ToTOML()
	for _, key := range []string{"exclude_dir", "exclude_file", "exclude_regex", "include_ext", "pre_cmd", "args", "full_bin"} {
		if strings.Contains(doc, key+" = ") {
			t.Errorf("empty %s should be omitted", key)
		}
	}
}

func TestAirHelperFiles(t *testing.T) {
	root := t.TempDir()
	h := NewAirHelper(root).
		AddWatchExt("svelte").
		AddExcludeDir("coverage").
		SetBuildCmd("go build -o ./tmp/app ./cmd/app").
		SetBinPath("tmp/app").
		AddBuildArgs("-v").
		UpdateConfig(func(c *AirConfig) { c.Misc.CleanOnExit = true })

	if err := h.InitAir(); err != nil {
		t.Fatal(err)
	}
	if !IsDir(filepath.Join(root, "tmp")) {
		t.Error("InitAir creates tmp/")
	}
	doc := mustRead(t, filepath.Join(root, ".air.toml"))
	for _, want := range []string{`cmd = "go build -o ./tmp/app ./cmd/app"`, `bin = "tmp/app"`, `args = ["-v"]`, `"svelte"`, `"coverage"`, "clean_on_exit = true"} {
		if !strings.Contains(doc, want) {
			t.Errorf(".air.toml missing %s", want)
		}
	}
	if err := h.InitAir(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second InitAir = %v", err)
	}

	if err := h.CreateDevScript(); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "dev.sh")
	info, err := os.Stat(script)
	if err != nil || !strings.HasPrefix(mustRead(t, script), "#!/bin/bash") {
		t.Fatalf("dev.sh: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Error("dev.sh should be executable")
	}

	if NewAirHelper("").projectRoot != "." {
		t.Error("empty project root defaults to .")
	}
	if !strings.Contains(AirInstallHelp(), "go install") || AirInstallHelp() != h.GetInstallInstructions() {
		t.Error("install help")
	}

	out := filepath.Join(t.TempDir(), "custom.toml")
	if err := CreateHyperChiAirConfig(out); err != nil || !strings.Contains(mustRead(t, out), "[build]") {
		t.Errorf("CreateHyperChiAirConfig: %v", err)
	}
}

func TestAirConfigDefaultFilename(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := DefaultAirConfig().SaveAirConfig(""); err != nil || !FileExists(".air.toml") {
		t.Fatalf("SaveAirConfig default name: %v", err)
	}
	if err := InitAirConfig(); err == nil {
		t.Error("InitAirConfig over an existing .air.toml")
	}
}

func TestDeploymentHelperConfig(t *testing.T) {
	dh := NewDeploymentHelper("", "")
	if dh.projectRoot != "." || dh.appName != "hyperchi-app" {
		t.Error("defaults")
	}
	dh = NewDeploymentHelper(t.TempDir(), "shop")
	cfg := dh.DefaultDeploymentConfig()
	if cfg.AppName != "shop" || cfg.Port != "8080" || cfg.HealthCheckPath != "/health" || cfg.EnvVars == nil {
		t.Errorf("DefaultDeploymentConfig = %+v", cfg)
	}
	prod := dh.GetProductionConfig()
	if !prod.DisableProfiler || prod.SecuritySettings["csrf_enabled"] != "true" {
		t.Error("GetProductionConfig")
	}
	info := dh.GetBuildInfo()
	if info["app_name"] != "shop" || info["go_version"] != runtime.Version() || info["build_time"] == "" || info["deployment_id"] == "" {
		t.Errorf("GetBuildInfo = %v", info)
	}
}

func TestDeploymentFiles(t *testing.T) {
	root := t.TempDir()
	if err := CreateDeploymentFiles(root, "shop"); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"Dockerfile":         "FROM ",
		"docker-compose.yml": "shop",
		".env.production":    "APP_NAME=shop",
		"shop.service":       "[Service]",
		"health.go":          "package main",
	} {
		if content := mustRead(t, filepath.Join(root, file)); !strings.Contains(content, want) {
			t.Errorf("%s missing %q", file, want)
		}
	}

	other := t.TempDir()
	if err := NewDeploymentHelper(other, "svc").CreateDeploymentFiles(); err != nil {
		t.Fatal(err)
	}
	if !FileExists(filepath.Join(other, "svc.service")) || FileExists(filepath.Join(other, "health.go")) {
		t.Error("the method variant writes the core files only")
	}
}

func TestSwitchToProductionReadsTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	t.Chdir(t.TempDir()) // the working directory is not the project
	dh := NewDeploymentHelper(root, "shop")
	if err := dh.CreateProductionEnv(); err != nil {
		t.Fatal(err)
	}
	if err := dh.SwitchToProduction(); err != nil {
		t.Fatalf("SwitchToProduction should load %s: %v", filepath.Join(root, ".env.production"), err)
	}
	if dh.env.Get("APP_NAME") != "shop" {
		t.Error("production env not loaded")
	}

	missing := NewDeploymentHelper(t.TempDir(), "x")
	if err := missing.SwitchToProduction(); err == nil {
		t.Error("missing .env.production accepted")
	}
}

func TestSwitchEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := SwitchEnvironment("staging"); err == nil {
		t.Error("missing env file accepted")
	}
	if err := WriteFile(".env.staging", "STAGE=1\n"); err != nil {
		t.Fatal(err)
	}
	if err := SwitchEnvironment("staging"); err != nil || mustRead(t, ".env") != "STAGE=1\n" {
		t.Errorf("SwitchEnvironment: %v", err)
	}
}

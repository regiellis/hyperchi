package helpers

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDotEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessEnvironmentBeatsDotEnv(t *testing.T) {
	t.Setenv("HC_PREC_PORT", "9000")
	path := writeDotEnv(t, "HC_PREC_PORT=8080\nHC_PREC_ONLY_FILE=from-file\n")

	e := NewEnv()
	if err := e.LoadFile(path); err != nil {
		t.Fatal(err)
	}
	if got := e.Get("HC_PREC_PORT"); got != "9000" {
		t.Fatalf("Get = %q, want the process value 9000", got)
	}
	if got := e.All()["HC_PREC_PORT"]; got != "9000" {
		t.Fatalf("All = %q, want 9000", got)
	}
	if got := e.Get("HC_PREC_ONLY_FILE"); got != "from-file" || !e.Has("HC_PREC_ONLY_FILE") {
		t.Fatalf("a key only in .env = %q", got)
	}

	e.Set("HC_PREC_PORT", "7000")
	if got := e.Get("HC_PREC_PORT"); got != "7000" {
		t.Fatalf("a value set in code = %q, want 7000", got)
	}
}

func TestOverrideSystemEnvLetsDotEnvWin(t *testing.T) {
	t.Setenv("HC_PREC_MODE", "process")
	path := writeDotEnv(t, "HC_PREC_MODE=file\n")

	e := NewEnv()
	if err := e.LoadConfig(EnvConfig{Files: []string{path}, OverrideSystemEnv: true}); err != nil {
		t.Fatal(err)
	}
	if got := e.Get("HC_PREC_MODE"); got != "file" {
		t.Fatalf("Get = %q, want file", got)
	}
}

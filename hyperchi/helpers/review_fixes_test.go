package helpers

import (
	"encoding/json"
	"go/format"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A # after whitespace starts a comment in an unquoted .env value. Inside
// quotes, or with no whitespace before it, the # is part of the value.
func TestEnvInlineComments(t *testing.T) {
	e := loadEnvBody(t, strings.Join([]string{
		"HC_T_C_PLAIN=value # trailing note",
		"HC_T_C_TAB=value\t# tab before the hash",
		"HC_T_C_PASS=pa#ss",
		"HC_T_C_LEADING=#not-a-comment",
		"HC_T_C_ONLY= # nothing but a comment",
		`HC_T_C_DQ="quoted # kept" # dropped`,
		`HC_T_C_SQ='single # kept' # dropped`,
		`HC_T_C_DQ_ESC="say \"hi\" # kept"   # dropped`,
		`HC_T_C_DQ_WHOLE="has # inside"`,
		"HC_T_C_REF=${HC_T_C_PASS} # expands, then the comment goes",
		"HC_T_C_URL=http://h/x#frag",
	}, "\n"))

	tests := map[string]string{
		"HC_T_C_PLAIN":    "value",
		"HC_T_C_TAB":      "value",
		"HC_T_C_PASS":     "pa#ss",
		"HC_T_C_LEADING":  "#not-a-comment",
		"HC_T_C_ONLY":     "",
		"HC_T_C_DQ":       "quoted # kept",
		"HC_T_C_SQ":       "single # kept",
		"HC_T_C_DQ_ESC":   `say "hi" # kept`,
		"HC_T_C_DQ_WHOLE": "has # inside",
		"HC_T_C_REF":      "pa#ss",
		"HC_T_C_URL":      "http://h/x#frag",
	}
	for key, want := range tests {
		if got := e.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// Save quotes values with " #" so they survive the round trip.
	saved := NewEnv()
	saved.Set("HC_T_C_RT", "a # b")
	path := filepath.Join(t.TempDir(), ".env")
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded := NewEnv()
	if err := reloaded.LoadFile(path); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Dump()["HC_T_C_RT"]; got != "a # b" {
		t.Errorf("round trip = %q", got)
	}
}

// Env.Save and SwitchEnvironment write secrets: owner-only 0600, even over a
// file that already existed with a looser mode.
func TestEnvFilesAreWrittenOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not apply on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("OLD=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv()
	e.Set("SECRET", "s3cr3t")
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("Save mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}

	t.Chdir(dir)
	if err := os.WriteFile(".env.prod", []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SwitchEnvironment("prod"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(".env"); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("SwitchEnvironment mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if err := writeSecretFile(filepath.Join(dir, ".env", "under-a-file"), "x"); err == nil {
		t.Error("writeSecretFile under a regular file accepted")
	}
}

// SwitchEnvironment reports a read failure instead of copying "" over .env.
func TestSwitchEnvironmentReportsReadErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir(".env.dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(".env", "KEEP=1\n"); err != nil {
		t.Fatal(err)
	}
	if err := SwitchEnvironment("dir"); err == nil {
		t.Fatal("a directory named .env.dir was copied as an env file")
	}
	if got := mustRead(t, ".env"); got != "KEEP=1\n" {
		t.Errorf(".env overwritten with %q", got)
	}
}

// ReadFile and ReadFileLines report errors. ReadFileLines also handles
// lines longer than bufio.Scanner's 64 KiB default.
func TestReadFileHelpersReturnErrors(t *testing.T) {
	dir := t.TempDir()
	if content, err := ReadFile(filepath.Join(dir, "missing")); err == nil || content != "" {
		t.Errorf("ReadFile(missing) = %q, %v", content, err)
	}
	if _, err := ReadFile(dir); err == nil {
		t.Error("ReadFile of a directory returned no error")
	}
	if _, err := ReadFileLines(dir); err == nil {
		t.Error("ReadFileLines of a directory returned no error")
	}

	empty := filepath.Join(dir, "empty")
	if err := WriteFile(empty, ""); err != nil {
		t.Fatal(err)
	}
	if lines, err := ReadFileLines(empty); err != nil || lines == nil || len(lines) != 0 {
		t.Errorf("ReadFileLines(empty) = %#v, %v", lines, err)
	}

	long := strings.Repeat("x", 200*1024)
	path := filepath.Join(dir, "long")
	if err := WriteFile(path, "first\r\n"+long+"\nlast"); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadFileLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "first" || lines[1] != long || lines[2] != "last" {
		t.Errorf("ReadFileLines lost data: %d lines", len(lines))
	}
}

// GetIntOr and GetFloatOr accept every Go numeric type and json.Number, not
// only int and float64.
func TestJSONNumericGettersAcceptAllNumberTypes(t *testing.T) {
	j := JSON{
		"i8": int8(-8), "i16": int16(16), "i32": int32(-32), "i64": int64(64),
		"u": uint(7), "u8": uint8(8), "u16": uint16(16), "u32": uint32(32), "u64": uint64(64),
		"f32": float32(2.5), "num": json.Number("42"), "numf": json.Number("1.5"),
		"bigu": uint64(1 << 63), "nan": float32(0) / func() float32 { return 0 }(),
	}
	for key, want := range map[string]int{
		"i8": -8, "i16": 16, "i32": -32, "i64": 64, "u": 7, "u8": 8, "u16": 16,
		"u32": 32, "u64": 64, "f32": 2, "num": 42,
	} {
		if got := j.GetIntOr(key, -1); got != want {
			t.Errorf("GetIntOr(%s) = %d, want %d", key, got, want)
		}
	}
	for _, key := range []string{"bigu", "nan", "numf"} {
		if got := j.GetIntOr(key, -1); got != -1 {
			t.Errorf("GetIntOr(%s) = %d, want the default", key, got)
		}
	}
	for key, want := range map[string]float64{
		"i8": -8, "i16": 16, "i32": -32, "i64": 64, "u": 7, "u8": 8, "u16": 16,
		"u32": 32, "u64": 64, "f32": 2.5, "num": 42, "numf": 1.5,
	} {
		if got := j.GetFloatOr(key, -1); got != want {
			t.Errorf("GetFloatOr(%s) = %v, want %v", key, got, want)
		}
	}
	bad := JSON{"num": json.Number("x")}
	if bad.GetFloatOr("num", -1) != -1 || bad.GetIntOr("num", -1) != -1 {
		t.Error("an invalid json.Number returns the default")
	}
	if n, ok := intFromInt64(-5); !ok || n != -5 {
		t.Error("intFromInt64")
	}
}

// FetchJSONOr returns an empty object, never nil, when the body is not JSON.
func TestFetchJSONOrInvalidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("not json")); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	if j := FetchJSONOr(srv.URL); j == nil || len(j) != 0 {
		t.Errorf("FetchJSONOr(invalid body) = %#v, want an empty object", j)
	}
}

// Install help names Air's current module, github.com/air-verse/air. The
// old github.com/cosmtrek/air path does not install the current release.
func TestAirInstallHelpUsesCurrentModule(t *testing.T) {
	help := AirInstallHelp()
	if strings.Contains(help, "cosmtrek") || !strings.Contains(help, "go install github.com/air-verse/air@latest") {
		t.Errorf("install help:\n%s", help)
	}
	root := t.TempDir()
	if err := NewAirHelper(root).CreateDevScript(); err != nil {
		t.Fatal(err)
	}
	script := mustRead(t, filepath.Join(root, "dev.sh"))
	if strings.Contains(script, "cosmtrek") || !strings.Contains(script, "github.com/air-verse/air@latest") {
		t.Errorf("dev.sh:\n%s", script)
	}
}

// The generated Dockerfile builds with the go.mod's Go version and a static
// CGO_ENABLED=0 build. The build script cross-compiles without cgo.
func TestDockerfileUsesGoModVersionAndNoCgo(t *testing.T) {
	root := t.TempDir()
	dh := NewDeploymentHelper(root, "shop")
	if err := dh.CreateDockerfile(); err != nil {
		t.Fatal(err)
	}
	dockerfile := mustRead(t, filepath.Join(root, "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM golang:"+frameworkGoVersion+"-alpine AS builder") {
		t.Errorf("without a go.mod the build image is golang:%s:\n%s", frameworkGoVersion, dockerfile)
	}
	for _, banned := range []string{"golang:1.21", "CGO_ENABLED=1", "gcc", "musl-dev", "sqlite-dev", "installsuffix"} {
		if strings.Contains(dockerfile, banned) {
			t.Errorf("Dockerfile contains %q", banned)
		}
	}
	if !strings.Contains(dockerfile, "CGO_ENABLED=0") {
		t.Error("Dockerfile does not build statically")
	}

	if err := WriteFile(filepath.Join(root, "go.mod"), "module shop\n\ngo 1.99.1\n"); err != nil {
		t.Fatal(err)
	}
	if err := dh.CreateDockerfile(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustRead(t, filepath.Join(root, "Dockerfile")), "FROM golang:1.99-alpine") {
		t.Error("the project's go directive picks the build image")
	}

	if err := dh.CreateBuildScript(); err != nil {
		t.Fatal(err)
	}
	if script := mustRead(t, filepath.Join(root, "build.sh")); strings.Contains(script, "CGO_ENABLED=1") || !strings.Contains(script, "CGO_ENABLED=0") {
		t.Error("build.sh cross-compiles with cgo")
	}
}

func TestDockerGoVersionSelection(t *testing.T) {
	for _, tc := range []struct{ gomod, want string }{
		{"module x\n\ngo 1.26.8\n", "1.26"},
		{"module x\ngo 1.30\ntoolchain go1.30.1\n", "1.30"},
		{"module x\n\ngo 1.21\n", frameworkGoVersion}, // older than the framework
		{"module x\n", frameworkGoVersion},
		{"module x\ngo banana\n", frameworkGoVersion},
		{"module x\ngo 1.x\n", frameworkGoVersion},
	} {
		root := t.TempDir()
		if err := WriteFile(filepath.Join(root, "go.mod"), tc.gomod); err != nil {
			t.Fatal(err)
		}
		if got := NewDeploymentHelper(root, "x").dockerGoVersion(); got != tc.want {
			t.Errorf("go.mod %q gives %q, want %q", tc.gomod, got, tc.want)
		}
	}
	if goVersionAtLeast("bad", "1.2") || !goVersionAtLeast("1.2", "bad") {
		t.Error("goVersionAtLeast parse failures")
	}
}

// frameworkGoVersion must match the framework's own go.mod. When the go
// directive moves, update the constant.
func TestFrameworkGoVersionTracksGoMod(t *testing.T) {
	gomod := mustRead(t, filepath.Join("..", "..", "go.mod"))
	if got := goDirectiveMinor(gomod); got != frameworkGoVersion {
		t.Errorf("root go.mod is go %s but frameworkGoVersion is %s", got, frameworkGoVersion)
	}
}

// The generated health.go compiles on its own, is gofmt clean, and serves.
func TestGeneratedHealthCheckCompilesAndServes(t *testing.T) {
	formatted, err := format.Source([]byte(healthCheckSource))
	if err != nil {
		t.Fatalf("health.go does not parse: %v", err)
	}
	if string(formatted) != healthCheckSource {
		t.Error("health.go is not gofmt clean")
	}

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	if err := NewDeploymentHelper(root, "shop").CreateHealthCheck(); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module healthcheck\n\ngo 1.22\n",
		"main.go": `package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
)

type failingDB struct{}

func (failingDB) PingContext(context.Context) error { return errors.New("down") }

func check(h http.Handler, wantCode int, wantBody string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != wantCode || !strings.Contains(rec.Body.String(), wantBody) {
		fmt.Fprintf(os.Stderr, "got %d %q, want %d containing %q\n", rec.Code, rec.Body.String(), wantCode, wantBody)
		os.Exit(1)
	}
}

func main() {
	check(healthCheckHandler(nil, "test"), http.StatusOK, ` + "`" + `"status":"healthy"` + "`" + `)
	check(healthCheckHandler(failingDB{}, "test"), http.StatusServiceUnavailable, ` + "`" + `"database":"unhealthy"` + "`" + `)
	check(readinessHandler(nil), http.StatusOK, "Ready")
	check(readinessHandler(failingDB{}), http.StatusServiceUnavailable, "Database not ready")
	check(http.HandlerFunc(livenessHandler), http.StatusOK, "Alive")
}
`,
	}
	for name, content := range files {
		if err := WriteFile(filepath.Join(root, name), content); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"vet", "."}, {"run", "."}} {
		cmd := exec.Command(goTool, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s on generated health.go: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func TestProductionDeploymentFilesPinAndProtect(t *testing.T) {
	root := t.TempDir()
	dh := NewDeploymentHelper(root, "shop")
	if err := dh.CreateDockerfile(); err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dockerfile), ":latest") {
		t.Fatalf("Dockerfile uses an unpinned :latest image:\n%s", dockerfile)
	}
	if err := dh.CreateProductionEnv(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, ".env.production"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf(".env.production mode = %o, want 600", mode)
	}
}

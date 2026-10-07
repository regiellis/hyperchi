package helpers

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// loadEnvBody loads body as a .env file into a fresh Env. It fails the test
// instead of hanging if LoadFile deadlocks.
func loadEnvBody(t *testing.T, body string) *Env {
	t.Helper()
	path := writeDotEnv(t, body)
	e := NewEnv()
	done := make(chan error, 1)
	go func() { done <- e.LoadFile(path) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadFile did not return; variable expansion deadlocked")
	}
	return e
}

func TestEnvFileParsing(t *testing.T) {
	t.Setenv("HC_T_PROC", "from-process")
	e := loadEnvBody(t, strings.Join([]string{
		"# comment",
		"",
		"HC_T_PLAIN=value",
		"export HC_T_EXPORTED=yes",
		"  HC_T_SPACED  =  padded value  ",
		`HC_T_DQ="line1\nline2\ttab \"q\" back\\slash"`,
		`HC_T_SQ='literal ${HC_T_PLAIN} \n'`,
		`HC_T_DQ_SPACES="  keep   inner  "`,
		"HC_T_BRACE=${HC_T_PLAIN}-suffix",
		"HC_T_DOLLAR=$HC_T_PLAIN and more",
		"HC_T_DOLLAR_PATH=$HC_T_PLAIN/sub",
		"HC_T_FROM_PROC=${HC_T_PROC}",
		"HC_T_UNKNOWN=[${HC_T_NOPE}]",
		"HC_T_PRICE=costs $5",
		"HC_T_MID=pa$sword",
		`HC_T_ESC_DOLLAR="\$HC_T_PLAIN"`,
		"HC_T_EMPTY=",
		"HC_T_EQ=a=b=c",
		"HC_T_UNCLOSED=${HC_T_PLAIN",
		`HC_T_URL="http://h:${HC_T_PLAIN}/x"`,
		`HC_T_UNI=héllo wörld`,
	}, "\n"))

	tests := map[string]string{
		"HC_T_PLAIN":       "value",
		"HC_T_EXPORTED":    "yes",
		"HC_T_SPACED":      "padded value",
		"HC_T_DQ":          "line1\nline2\ttab \"q\" back\\slash",
		"HC_T_SQ":          `literal ${HC_T_PLAIN} \n`,
		"HC_T_DQ_SPACES":   "  keep   inner  ",
		"HC_T_BRACE":       "value-suffix",
		"HC_T_DOLLAR":      "value and more",
		"HC_T_DOLLAR_PATH": "value/sub",
		"HC_T_FROM_PROC":   "from-process",
		"HC_T_UNKNOWN":     "[]",
		"HC_T_PRICE":       "costs $5",
		"HC_T_MID":         "pa$sword",
		"HC_T_ESC_DOLLAR":  "$HC_T_PLAIN",
		"HC_T_EMPTY":       "",
		"HC_T_EQ":          "a=b=c",
		"HC_T_UNCLOSED":    "${HC_T_PLAIN",
		"HC_T_URL":         "http://h:value/x",
		"HC_T_UNI":         "héllo wörld",
	}
	for key, want := range tests {
		if got := e.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if !e.Has("HC_T_EMPTY") {
		t.Error("an empty value still counts as present")
	}
}

func TestEnvSelfReferenceTerminates(t *testing.T) {
	// A value whose expansion contains its own reference must not loop.
	e := loadEnvBody(t, "HC_T_LOOP='${HC_T_LOOP}'\nHC_T_NEXT=${HC_T_LOOP}!\n")
	if got := e.Get("HC_T_NEXT"); got != "${HC_T_LOOP}!" {
		t.Errorf("HC_T_NEXT = %q", got)
	}
}

func TestEnvParseErrors(t *testing.T) {
	for name, body := range map[string]string{
		"no equals": "JUST_A_WORD\n",
		"empty key": "=value\n",
	} {
		e := NewEnv()
		err := e.LoadFile(writeDotEnv(t, body))
		if err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := NewEnv().LoadFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Error("missing file accepted")
	}
}

func TestEnvLoadFileOnce(t *testing.T) {
	path := writeDotEnv(t, "HC_T_ONCE=1\n")
	e := NewEnv()
	if err := e.LoadFiles(path, path); err != nil {
		t.Fatal(err)
	}
	if got := e.LoadedFiles(); !reflect.DeepEqual(got, []string{path}) {
		t.Errorf("LoadedFiles = %v", got)
	}
	if err := os.WriteFile(path, []byte("HC_T_ONCE=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadFile(path); err != nil || e.Get("HC_T_ONCE") != "1" {
		t.Error("a loaded file is not re-read")
	}
	e.Clear()
	if len(e.LoadedFiles()) != 0 || e.Has("HC_T_ONCE") || len(e.Dump()) != 0 {
		t.Error("Clear")
	}
	if err := e.LoadFile(path); err != nil || e.Get("HC_T_ONCE") != "2" {
		t.Error("reload after Clear")
	}
	if err := e.LoadFiles(path, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("LoadFiles should stop at a missing file")
	}
}

func TestEnvTypedGetters(t *testing.T) {
	e := NewEnv()
	e.Set("HC_T_INT", "42")
	e.Set("HC_T_BADINT", "4x")
	e.Set("HC_T_FLOAT", "2.5")
	e.Set("HC_T_TRUE", "YES")
	e.Set("HC_T_FALSE", "off")
	e.Set("HC_T_LIST", " a, b ,c ")

	if v, err := e.GetInt("HC_T_INT"); v != 42 || err != nil {
		t.Error("GetInt")
	}
	if _, err := e.GetInt("HC_T_BADINT"); err == nil {
		t.Error("GetInt bad")
	}
	if _, err := e.GetInt("HC_T_MISSING"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Error("GetInt missing")
	}
	if e.GetIntOr("HC_T_BADINT", 7) != 7 || e.GetIntOr("HC_T_INT", 7) != 42 {
		t.Error("GetIntOr")
	}
	if v, err := e.GetFloat("HC_T_FLOAT"); v != 2.5 || err != nil {
		t.Error("GetFloat")
	}
	if _, err := e.GetFloat("HC_T_MISSING"); err == nil {
		t.Error("GetFloat missing")
	}
	if e.GetFloatOr("HC_T_BADINT", 1.5) != 1.5 || e.GetFloatOr("HC_T_FLOAT", 1) != 2.5 {
		t.Error("GetFloatOr")
	}
	if !e.GetBool("HC_T_TRUE") || e.GetBool("HC_T_FALSE") || e.GetBool("HC_T_MISSING") {
		t.Error("GetBool")
	}
	if !e.GetBoolOr("HC_T_MISSING", true) || e.GetBoolOr("HC_T_FALSE", true) {
		t.Error("GetBoolOr")
	}
	if got := e.GetSlice("HC_T_LIST"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("GetSlice = %q", got)
	}
	if got := e.GetSlice("HC_T_MISSING"); got == nil || len(got) != 0 {
		t.Error("GetSlice missing")
	}
	if e.GetOr("HC_T_MISSING", "d") != "d" || e.GetOr("HC_T_INT", "d") != "42" {
		t.Error("GetOr")
	}
}

func TestEnvSaveRoundTrip(t *testing.T) {
	values := map[string]string{
		"HC_T_S_PLAIN":     "simple",
		"HC_T_S_SPACES":    "  two  spaces  ",
		"HC_T_S_QUOTES":    `say "hi" and 'bye'`,
		"HC_T_S_BACKSLASH": `C:\new\table\`,
		"HC_T_S_DOLLAR":    "$HOME and ${PATH}",
		"HC_T_S_NEWLINE":   "line1\nline2\r\n\tend",
		"HC_T_S_EMPTY":     "",
		"HC_T_S_UNICODE":   "héllo wörld",
	}
	e := NewEnv()
	for k, v := range values {
		e.Set(k, v)
	}
	path := filepath.Join(t.TempDir(), "nested", ".env")
	if err := e.Save(path); err != nil {
		t.Fatal(err)
	}

	saved := mustRead(t, path)
	lines := strings.Split(strings.TrimSuffix(saved, "\n"), "\n")
	if len(lines) != len(values) || !slices.IsSorted(lines) {
		t.Errorf("Save should write one sorted line per key:\n%s", saved)
	}

	reloaded := NewEnv()
	if err := reloaded.LoadFile(path); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Dump(); !reflect.DeepEqual(got, values) {
		for k, v := range values {
			if got[k] != v {
				t.Errorf("%s round-tripped as %q, want %q", k, got[k], v)
			}
		}
	}
}

func TestEnvAllKeysAndValidate(t *testing.T) {
	t.Setenv("HC_T_SYS", "sys")
	e := loadEnvBody(t, "HC_T_SYS=file\nHC_T_FILEONLY=f\n")
	all := e.All()
	if all["HC_T_SYS"] != "sys" || all["HC_T_FILEONLY"] != "f" {
		t.Errorf("All: SYS=%q FILEONLY=%q", all["HC_T_SYS"], all["HC_T_FILEONLY"])
	}
	keys := e.Keys()
	if !slices.Contains(keys, "HC_T_SYS") || !slices.Contains(keys, "HC_T_FILEONLY") {
		t.Error("Keys")
	}
	if dump := e.Dump(); dump["HC_T_SYS"] != "file" || len(dump) != 2 {
		t.Errorf("Dump holds only .env values: %v", dump)
	}
	if err := e.Validate([]string{"HC_T_SYS", "HC_T_FILEONLY"}); err != nil {
		t.Error(err)
	}
	err := e.Validate([]string{"HC_T_SYS", "HC_T_ABSENT_1", "HC_T_ABSENT_2"})
	if err == nil || !strings.Contains(err.Error(), "HC_T_ABSENT_1") || !strings.Contains(err.Error(), "HC_T_ABSENT_2") {
		t.Errorf("Validate = %v", err)
	}
}

func TestEnvSetSystem(t *testing.T) {
	t.Setenv("HC_T_SETSYS", "before")
	e := NewEnv()
	if err := e.SetSystem("HC_T_SETSYS", "after"); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HC_T_SETSYS") != "after" || e.Get("HC_T_SETSYS") != "after" {
		t.Error("SetSystem should set both the manager and the process")
	}
}

func TestEnvLoadConfig(t *testing.T) {
	t.Setenv("HC_T_CFG_PROC", "p")
	path := writeDotEnv(t, "HC_T_CFG_FILE=f\n")

	e := NewEnv()
	err := e.LoadConfig(EnvConfig{
		Files:    []string{path},
		Defaults: map[string]string{"HC_T_CFG_DEFAULT": "d", "HC_T_CFG_FILE": "ignored", "HC_T_CFG_PROC": "ignored"},
		Required: []string{"HC_T_CFG_FILE", "HC_T_CFG_PROC", "HC_T_CFG_DEFAULT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.Get("HC_T_CFG_DEFAULT") != "d" || e.Get("HC_T_CFG_FILE") != "f" || e.Get("HC_T_CFG_PROC") != "p" {
		t.Error("defaults apply only to absent keys")
	}

	err = NewEnv().LoadConfig(EnvConfig{Files: []string{path}, Required: []string{"HC_T_CFG_MISSING"}})
	if err == nil || !strings.Contains(err.Error(), "HC_T_CFG_MISSING") {
		t.Errorf("missing required = %v", err)
	}
	if err := NewEnv().LoadConfig(EnvConfig{Files: []string{filepath.Join(t.TempDir(), "x")}}); err == nil {
		t.Error("missing config file accepted")
	}
}

func TestEnvLoadConfigOverrideWritesProcessEnv(t *testing.T) {
	t.Setenv("HC_T_OVR", "process")
	path := writeDotEnv(t, "HC_T_OVR=file\n")
	if err := NewEnv().LoadConfig(EnvConfig{Files: []string{path}, OverrideSystemEnv: true}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HC_T_OVR"); got != "file" {
		t.Errorf("process env = %q, want file", got)
	}
}

func TestEnvLoadDefaultFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("HC_T_DEF=base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("HC_T_DEF=local\nHC_T_LOCAL=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	e := NewEnv()
	if err := e.LoadConfig(EnvConfig{}); err != nil {
		t.Fatal(err)
	}
	if e.Get("HC_T_DEF") != "local" || e.Get("HC_T_LOCAL") != "1" {
		t.Error("later default files override earlier ones")
	}
	if got := e.LoadedFiles(); !reflect.DeepEqual(got, []string{".env", ".env.local"}) {
		t.Errorf("LoadedFiles = %v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, ".env.development"), []byte("broken line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewEnv().LoadDefault(); err == nil || !strings.Contains(err.Error(), ".env.development") {
		t.Errorf("LoadDefault parse error = %v", err)
	}
}

func TestPackageLevelEnv(t *testing.T) {
	prev := defaultEnv
	defaultEnv = NewEnv()
	t.Cleanup(func() { defaultEnv = prev })

	path := writeDotEnv(t, "HC_T_PKG_INT=5\nHC_T_PKG_BOOL=true\n")
	if err := LoadEnv(path); err != nil {
		t.Fatal(err)
	}
	if v, err := GetEnvInt("HC_T_PKG_INT"); v != 5 || err != nil {
		t.Error("GetEnvInt")
	}
	if GetEnvIntOr("HC_T_PKG_MISSING", 3) != 3 || !GetEnvBool("HC_T_PKG_BOOL") || !GetEnvBoolOr("HC_T_PKG_MISSING", true) {
		t.Error("typed package getters")
	}
	if GetEnv("HC_T_PKG_INT") != "5" || GetEnvOr("HC_T_PKG_MISSING", "d") != "d" {
		t.Error("GetEnv/GetEnvOr")
	}
	t.Setenv("HC_T_PKG_SET", "")
	if err := SetEnv("HC_T_PKG_SET", "v"); err != nil || os.Getenv("HC_T_PKG_SET") != "v" || !HasEnv("HC_T_PKG_SET") {
		t.Error("SetEnv/HasEnv")
	}
	if err := RequireEnv("HC_T_PKG_INT"); err != nil {
		t.Error(err)
	}
	if err := RequireEnv("HC_T_PKG_NOT_THERE"); err == nil {
		t.Error("RequireEnv missing")
	}

	t.Chdir(t.TempDir())
	if err := LoadEnv(); err != nil {
		t.Errorf("LoadEnv with no default files present: %v", err)
	}
}

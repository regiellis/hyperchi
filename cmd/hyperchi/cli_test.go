package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// helperEnv makes the test binary exit at once when a test runs it as a child
// process, so serveApp can be exercised against a real executable.
const helperEnv = "HYPERCHI_CLI_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "exit" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestValidateProjectName(t *testing.T) {
	valid := []string{"app", "my-app", "my_app", "app2", "a-1-b"}
	invalid := []string{"", "My App", "MyApp", "my app", "1app", "-app", "_app",
		"../evil", "a/b", "a.b", "app!", "ápp"}
	for _, name := range valid {
		if err := validateProjectName(name); err != nil {
			t.Errorf("validateProjectName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range invalid {
		if err := validateProjectName(name); err == nil {
			t.Errorf("validateProjectName(%q) = nil, want an error", name)
		}
	}
}

func TestCreateProjectRejectsInvalidNameBeforeWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := createProject("My App", "", "full"); err == nil {
		t.Fatal("createProject accepted a name with a space and uppercase letters")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected name still wrote %d entries", len(entries))
	}
}

func TestServePath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "app")
	cases := map[string]string{
		"app":     "." + string(filepath.Separator) + "app",
		"bin/app": "bin/app",
		abs:       abs,
	}
	for in, want := range cases {
		if got := servePath(in); got != want {
			t.Errorf("servePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServeAppRunsAnAbsoluteBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(exe) {
		t.Fatalf("test executable path %q is not absolute", exe)
	}
	t.Setenv(helperEnv, "exit")
	if err := serveApp(exe, 0); err != nil {
		t.Fatalf("serveApp(%q): %v", exe, err)
	}
}

// The CLI registers no command that only prints instructions for endpoints
// the framework does not serve (profiler start/stats/reset, deploy health).
func TestInstructionOnlyCommandsAreGone(t *testing.T) {
	paths := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		paths[c.CommandPath()] = true
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(newRootCmd())
	for _, gone := range []string{"hyperchi profiler", "hyperchi deploy health"} {
		if paths[gone] {
			t.Errorf("%q is still registered", gone)
		}
	}
	for _, kept := range []string{"hyperchi new", "hyperchi dev", "hyperchi deploy init"} {
		if !paths[kept] {
			t.Errorf("%q is missing", kept)
		}
	}
}

// The templates must request the framework version this CLI is built against,
// or a project generated without --framework-path pins a release whose API
// does not match the generated code.
func TestScaffoldsRequireTheCLIFrameworkVersion(t *testing.T) {
	requireRe := regexp.MustCompile(`github\.com/regiellis/hyperchi (v\S+)`)
	cliMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	want := requireRe.FindSubmatch(cliMod)
	if want == nil {
		t.Fatal("cmd/hyperchi/go.mod does not require the framework")
	}
	for _, tpl := range []string{"_scaffold/go.mod.tpl", "_site/go.mod.tpl"} {
		data, err := scaffoldFS.ReadFile(tpl)
		if err != nil {
			t.Fatal(err)
		}
		got := requireRe.FindSubmatch(data)
		if got == nil || string(got[1]) != string(want[1]) {
			t.Errorf("%s requires %q, CLI go.mod requires %s", tpl, got, want[1])
		}
	}
}

// The agent guide ships under names a public mirror keeps (no file named like
// the repo's own agent tooling), and hyperchi new writes the names agents read.
func TestCreateProjectWritesTheAgentGuide(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := createProject("guided", root, "full"); err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile(filepath.Join("guided", "AGENTS.md"))
	if err != nil || !strings.Contains(string(guide), "hyperchi.Context") {
		t.Fatalf("AGENTS.md = %d bytes, %v", len(guide), err)
	}
	bridge, err := os.ReadFile(filepath.Join("guided", "CLAUDE.md"))
	if err != nil || strings.TrimSpace(string(bridge)) != "@AGENTS.md" {
		t.Fatalf("CLAUDE.md = %q, %v", bridge, err)
	}
	for _, stale := range []string{"agent-guide.md", "assistant-bridge.md"} {
		if _, err := os.Stat(filepath.Join("guided", stale)); err == nil {
			t.Errorf("%s was written under its source name", stale)
		}
	}
	if err := fs.WalkDir(scaffoldFS, "_scaffold", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		lower := strings.ToLower(path)
		if strings.Contains(lower, "claude") || strings.Contains(lower, "agents.md") {
			t.Errorf("embedded scaffold path %s would be stripped by a public mirror", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

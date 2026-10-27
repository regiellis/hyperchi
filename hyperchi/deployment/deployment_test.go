package deployment_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/deployment"
)

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{
		"go.mod": "module fixture\n\ngo 1.26.0\n",
		"main.go": `package main
import("fmt";"os")
func main(){data,err:=os.ReadFile("public/message.txt");if err!=nil{panic(err)};fmt.Printf("%s:%s",os.Getenv("ENVIRONMENT"),data)}
`, "public/message.txt": "hello",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAdaptersBuildExecutableArtifacts(t *testing.T) {
	t.Parallel()
	root := project(t)
	for _, target := range []string{"docker", "systemd"} {
		t.Run(target, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "artifact")
			manifest, err := deployment.Build(t.Context(), deployment.Config{Project: root, Output: output, Name: "demo", Target: target, Arch: "amd64", Assets: []string{"public"}, Port: 9090, Log: os.Stderr})
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Version != 1 || manifest.Binary != "app" || manifest.Environment["PORT"] != "9090" || manifest.Environment["ENVIRONMENT"] != "production" || manifest.OS != "linux" {
				t.Fatalf("manifest: %+v", manifest)
			}
			for name, want := range manifest.Files {
				data, err := os.ReadFile(filepath.Join(output, name))
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256(data)
				if hex.EncodeToString(hash[:]) != want {
					t.Errorf("checksum %s", name)
				}
			}
			data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var decoded deployment.Manifest
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Files) != 2 {
				t.Fatalf("unexpected files: %v", decoded.Files)
			}
			if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
				cmd := exec.CommandContext(t.Context(), filepath.Join(output, "app"))
				cmd.Dir = output
				cmd.Env = append(os.Environ(), "ENVIRONMENT=production")
				got, err := cmd.CombinedOutput()
				if err != nil || string(got) != "production:hello" {
					t.Fatalf("execute: %s %v", got, err)
				}
			}
			adapter := "Dockerfile"
			if target == "systemd" {
				adapter = "demo.service"
			}
			content, err := os.ReadFile(filepath.Join(output, adapter))
			if err != nil {
				t.Fatal(err)
			}
			wants := []string{"USER 65532:65532", "PORT=9090", "ENTRYPOINT [\"/app/app\"]", "/app/public"}
			if target == "systemd" {
				wants = []string{"DynamicUser=yes", "PORT=9090", "WorkingDirectory=/opt/demo", "TimeoutStopSec=30s", "EnvironmentFile=-/etc/demo.env"}
			}
			for _, want := range wants {
				if !bytes.Contains(content, []byte(want)) {
					t.Errorf("adapter missing %q", want)
				}
			}
			if _, err := deployment.Build(t.Context(), deployment.Config{Project: root, Output: output, Name: "demo", Target: target}); err == nil {
				t.Fatal("existing artifact overwritten")
			}
		})
	}
}

func TestRejectInvalidInputsWithoutOutput(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		change func(*deployment.Config)
	}{
		{"name injection", func(c *deployment.Config) { c.Name = "demo\nExecStart=/evil" }},
		{"target", func(c *deployment.Config) { c.Target = "cloud" }},
		{"arch", func(c *deployment.Config) { c.Arch = "mips" }},
		{"port", func(c *deployment.Config) { c.Port = 80 }},
		{"asset escape", func(c *deployment.Config) { c.Assets = []string{"../private"} }},
		{"asset collision", func(c *deployment.Config) { c.Assets = []string{"app"} }},
		{"duplicate asset", func(c *deployment.Config) { c.Assets = []string{"public", "public"} }},
		{"nested output", func(c *deployment.Config) {
			c.Output = filepath.Join(c.Project, "public", "deploy")
			c.Assets = []string{"public"}
		}},
		{"image injection", func(c *deployment.Config) { c.RuntimeImage = "base\nRUN evil" }},
		{"package flag", func(c *deployment.Config) { c.Package = "-race" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := deployment.Config{Project: project(t), Output: filepath.Join(t.TempDir(), "out"), Name: "demo", Target: "docker"}
			tt.change(&cfg)
			if _, err := deployment.Build(t.Context(), cfg); err == nil {
				t.Fatal("accepted invalid config")
			}
			if _, err := os.Lstat(cfg.Output); !os.IsNotExist(err) {
				t.Fatalf("output exists: %v", err)
			}
		})
	}
}

func TestAssetExclusionsAndFailureCleanup(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"secret", "symlink", "build failure", "non-main", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			root := project(t)
			outParent := t.TempDir()
			ctx := t.Context()
			switch mode {
			case "secret":
				if err := os.WriteFile(filepath.Join(root, "public", ".env.production"), []byte("SECRET=private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("message.txt", filepath.Join(root, "public", "link")); err != nil {
					t.Fatal(err)
				}
			case "build failure":
				if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){broken()}"), 0644); err != nil {
					t.Fatal(err)
				}
			case "non-main":
				if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package fixture"), 0644); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := deployment.Build(ctx, deployment.Config{Project: root, Output: filepath.Join(outParent, "out"), Name: "demo", Target: "docker", Assets: []string{"public"}})
			if err == nil {
				t.Fatal("expected failure")
			}
			entries, err := os.ReadDir(outParent)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("left partial artifact: %v", entries)
			}
		})
	}
}

func TestOutputSymlinkCannotRecurseIntoAssets(t *testing.T) {
	t.Parallel()
	root := project(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Join(root, "public"), alias); err != nil {
		t.Fatal(err)
	}
	_, err := deployment.Build(t.Context(), deployment.Config{Project: root, Output: filepath.Join(alias, "new", "output"), Name: "demo", Target: "docker", Assets: []string{"public"}})
	if err == nil || !strings.Contains(err.Error(), "inside selected asset") {
		t.Fatalf("got %v", err)
	}
}

func TestCrossCompileARM64(t *testing.T) {
	t.Parallel()
	output := filepath.Join(t.TempDir(), "arm64")
	_, err := deployment.Build(t.Context(), deployment.Config{Project: project(t), Output: output, Name: "demo", Target: "docker", Arch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := elf.Open(filepath.Join(output, "app"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binary.Close(); err != nil {
			t.Errorf("close binary: %v", err)
		}
	})
	if binary.Machine != elf.EM_AARCH64 {
		t.Fatalf("machine: %v", binary.Machine)
	}
	for _, program := range binary.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("binary needs dynamic linker")
		}
	}
}

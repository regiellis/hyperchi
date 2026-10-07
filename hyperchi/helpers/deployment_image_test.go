package helpers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileKeepsSecretsOutAndCopiesOnlyExistingDirs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"templates", "dist"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dh := NewDeploymentHelper(root, "shop")
	if err := dh.CreateDockerfile(); err != nil {
		t.Fatal(err)
	}
	dockerfile := mustRead(t, filepath.Join(root, "Dockerfile"))
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.HasPrefix(line, "COPY") && strings.Contains(line, ".env") {
			t.Errorf("Dockerfile copies an env file into the image: %s", line)
		}
	}
	for _, want := range []string{"COPY --from=builder /app/templates ./templates/", "COPY --from=builder /app/dist ./dist/"} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	for _, missing := range []string{"/app/static", "/app/public"} {
		if strings.Contains(dockerfile, missing) {
			t.Errorf("Dockerfile copies %s, which does not exist (docker build would fail)", missing)
		}
	}

	ignore := mustRead(t, filepath.Join(root, ".dockerignore"))
	for _, want := range []string{".env", ".env.*", ".git", "*.db"} {
		if !strings.Contains(ignore, want+"\n") {
			t.Errorf(".dockerignore missing %q:\n%s", want, ignore)
		}
	}
}

func TestDockerignoreIsNotOverwritten(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".dockerignore")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewDeploymentHelper(root, "shop").CreateDockerfile(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "mine\n" {
		t.Fatalf(".dockerignore overwritten: %q", got)
	}
}

// Generated files set PORT, the variable the framework reads, not APP_PORT.
func TestDeploymentFilesSetThePortTheFrameworkReads(t *testing.T) {
	root := t.TempDir()
	dh := NewDeploymentHelper(root, "shop")
	if err := dh.CreateDockerCompose(); err != nil {
		t.Fatal(err)
	}
	if err := dh.CreateProductionEnv(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker-compose.yml", ".env.production"} {
		got := mustRead(t, filepath.Join(root, name))
		if strings.Contains(got, "APP_PORT") || !strings.Contains(got, "PORT=8080") {
			t.Errorf("%s does not set PORT:\n%s", name, got)
		}
	}
}

func TestDevScriptPointsAtTheRealAirInitCommand(t *testing.T) {
	root := t.TempDir()
	ah := NewAirHelper(root)
	if err := ah.CreateDevScript(); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "dev.sh"))
	if strings.Contains(got, "--init-air") || !strings.Contains(got, "hyperchi air init") {
		t.Fatalf("dev.sh points at a missing command:\n%s", got)
	}
}

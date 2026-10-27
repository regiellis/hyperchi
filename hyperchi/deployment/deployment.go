// Package deployment builds a portable Linux application artifact and generates
// Docker or systemd runtime configuration. It never publishes images or changes
// services. Applications must read HOST, PORT, and ENVIRONMENT at runtime.
package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config selects a build and adapter. Output must not exist. Package is a
// local main-package path (default "."). Assets are explicit top-level files
// or directories copied without symlinks or common secret/data files. Frontend
// compilation must run first. Log receives build diagnostics, never a manifest.
type Config struct {
	Project      string
	Output       string
	Name         string
	Target       string
	Package      string
	Arch         string
	Port         int
	Assets       []string
	RuntimeImage string
	Log          io.Writer
}

// Manifest describes artifact format version 1. Files maps runtime file paths
// to SHA-256 digests; generated adapter files are not part of that inventory.
// Environment contains non-secret defaults only. Runtime secrets stay external.
type Manifest struct {
	Version          int               `json:"version"`
	Name             string            `json:"name"`
	Target           string            `json:"target"`
	OS               string            `json:"os"`
	Arch             string            `json:"arch"`
	Binary           string            `json:"binary"`
	Package          string            `json:"package"`
	Assets           []string          `json:"assets"`
	Files            map[string]string `json:"files"`
	Environment      map[string]string `json:"environment"`
	WorkingDirectory string            `json:"working_directory"`
	RuntimeImage     string            `json:"runtime_image,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var assetPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var packagePattern = regexp.MustCompile(`^\./[a-zA-Z0-9_./-]+$`)
var imagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_:@-]*$`)

func normalize(cfg Config) (Config, error) {
	if cfg.Project == "" {
		cfg.Project = "."
	}
	if cfg.Output == "" {
		return cfg, fmt.Errorf("deployment: output directory is required")
	}
	if !namePattern.MatchString(cfg.Name) {
		return cfg, fmt.Errorf("deployment: name must be lowercase letters, digits, or hyphens, starting with a letter (max 48)")
	}
	if cfg.Target != "docker" && cfg.Target != "systemd" {
		return cfg, fmt.Errorf("deployment: target must be docker or systemd")
	}
	if cfg.Package == "" {
		cfg.Package = "."
	}
	if cfg.Package != "." && (!packagePattern.MatchString(cfg.Package) || strings.Contains(cfg.Package, "..")) {
		return cfg, fmt.Errorf("deployment: package must be a local path such as ./cmd/server")
	}
	if cfg.Arch == "" {
		cfg.Arch = runtime.GOARCH
	}
	if cfg.Arch != "amd64" && cfg.Arch != "arm64" {
		return cfg, fmt.Errorf("deployment: arch must be amd64 or arm64")
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.Port < 1024 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("deployment: port must be between 1024 and 65535")
	}
	if cfg.Target == "docker" {
		if cfg.RuntimeImage == "" {
			cfg.RuntimeImage = "gcr.io/distroless/static-debian13:nonroot"
		}
		if !imagePattern.MatchString(cfg.RuntimeImage) {
			return cfg, fmt.Errorf("deployment: invalid runtime image")
		}
	} else if cfg.RuntimeImage != "" {
		return cfg, fmt.Errorf("deployment: runtime image applies only to docker")
	}
	cfg.Assets = slices.Clone(cfg.Assets)
	slices.Sort(cfg.Assets)
	for i, asset := range cfg.Assets {
		if !assetPattern.MatchString(asset) || forbidden(asset) || slices.Contains([]string{"app", "Dockerfile", "manifest.json", cfg.Name + ".service"}, asset) || (i > 0 && cfg.Assets[i-1] == asset) {
			return cfg, fmt.Errorf("deployment: invalid, reserved, or duplicate top-level asset %q", asset)
		}
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	return cfg, nil
}

func forbidden(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".git" || lower == ".ssh" || lower == ".aws" || lower == ".npmrc" ||
		lower == ".netrc" || strings.HasPrefix(lower, ".env") ||
		strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".db-wal") || strings.HasSuffix(lower, ".db-shm") ||
		strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".sqlite3") ||
		strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".pem") || strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519")
}

// Build compiles with CGO_ENABLED=0 and GOOS=linux, stages runtime files and the
// chosen adapter, then renames the completed directory into Output. Failures
// remove the stage and leave existing output untouched. This does not run npm,
// Docker, systemctl, health probes, or a remote deployment. Build only trusted
// source projects, as with go build itself.
func Build(ctx context.Context, config Config) (_ *Manifest, err error) {
	cfg, err := normalize(config)
	if err != nil {
		return nil, err
	}
	project, err := filepath.Abs(cfg.Project)
	if err != nil {
		return nil, err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return nil, err
	}
	output, err := filepath.Abs(cfg.Output)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(output); err == nil {
		return nil, fmt.Errorf("deployment: output already exists: %s", output)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Resolve the nearest existing parent, including symlinks, before ensuring
	// an output stage cannot be copied recursively as part of a selected asset.
	parent, err := resolvedPath(filepath.Dir(output))
	if err != nil {
		return nil, err
	}
	output = filepath.Join(parent, filepath.Base(output))
	for _, asset := range cfg.Assets {
		assetPath := filepath.Join(project, asset)
		relative, err := filepath.Rel(assetPath, output)
		if err != nil {
			return nil, err
		}
		if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return nil, fmt.Errorf("deployment: output cannot be inside selected asset %q", asset)
		}
	}
	root, err := os.OpenRoot(project)
	if err != nil {
		return nil, err
	}
	// The root is closed and checked once the assets are staged, before the
	// rename. This close covers the failures that return earlier.
	rootOpen := true
	defer func() {
		if !rootOpen {
			return
		}
		if cerr := root.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()
	for _, asset := range cfg.Assets {
		if err := walkAsset(ctx, root, asset, nil); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(parent, 0755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(parent, ".hyperchi-deploy-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return // renamed into Output
		}
		if rerr := os.RemoveAll(stage); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()
	env := buildEnv(cfg.Arch)
	list := exec.CommandContext(ctx, "go", "list", "-f", "{{.Name}}", cfg.Package)
	list.Dir = project
	list.Env = env
	list.Stderr = cfg.Log
	list.WaitDelay = 5 * time.Second
	name, err := list.Output()
	if err != nil {
		return nil, fmt.Errorf("deployment: inspect main package: %w", err)
	}
	if strings.TrimSpace(string(name)) != "main" {
		return nil, fmt.Errorf("deployment: package must be one executable main package")
	}
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", filepath.Join(stage, "app"), cfg.Package)
	build.Dir = project
	build.Env = env
	build.Stdout = cfg.Log
	build.Stderr = cfg.Log
	build.WaitDelay = 5 * time.Second
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("deployment: build: %w", err)
	}
	if err := os.Chmod(filepath.Join(stage, "app"), 0755); err != nil {
		return nil, err
	}
	manifest := &Manifest{Version: 1, Name: cfg.Name, Target: cfg.Target, OS: "linux", Arch: cfg.Arch, Binary: "app",
		Package: cfg.Package, Assets: cfg.Assets, Files: map[string]string{}, RuntimeImage: cfg.RuntimeImage,
		Environment: map[string]string{"HOST": "0.0.0.0", "PORT": strconv.Itoa(cfg.Port), "ENVIRONMENT": "production", "DB_DISABLED": "true"}}
	for _, asset := range cfg.Assets {
		err := walkAsset(ctx, root, asset, func(path string, entry fs.DirEntry) (err error) {
			dest := filepath.Join(stage, filepath.FromSlash(path))
			if entry.IsDir() {
				if err := os.MkdirAll(dest, 0755); err != nil {
					return err
				}
				return os.Chmod(dest, 0755)
			}
			source, err := root.Open(path)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := source.Close(); cerr != nil && err == nil {
					err = cerr
				}
			}()
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, source)
			modeErr := out.Chmod(0644)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if modeErr != nil {
				return modeErr
			}
			return closeErr
		})
		if err != nil {
			return nil, err
		}
	}
	rootOpen = false
	if err := root.Close(); err != nil {
		return nil, err
	}
	if err := filepath.WalkDir(stage, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		manifest.Files[filepath.ToSlash(relative)] = sum
		return nil
	}); err != nil {
		return nil, err
	}
	if err := writeAdapter(stage, cfg, manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), append(data, '\n'), 0644); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(output); err == nil {
		return nil, fmt.Errorf("deployment: output appeared during build")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, output); err != nil {
		return nil, err
	}
	return manifest, nil
}

// hashFile returns the hex SHA-256 of the file at path.
func hashFile(path string) (sum string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			sum, err = "", cerr
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func walkAsset(ctx context.Context, root *os.Root, asset string, visit func(string, fs.DirEntry) error) error {
	info, err := root.Lstat(asset)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("deployment: symlink asset %q", asset)
	}
	return fs.WalkDir(root.FS(), asset, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if forbidden(d.Name()) {
			return fmt.Errorf("deployment: excluded secret/data asset %q", path)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("deployment: non-regular asset %q", path)
		}
		if visit != nil {
			return visit(path, d)
		}
		return nil
	})
}

func resolvedPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolvedPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

func buildEnv(arch string) []string {
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOAMD64", "GOARM64":
			continue
		}
		env = append(env, value)
	}
	return append(env, "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=")
}

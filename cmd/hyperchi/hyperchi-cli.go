// Command hyperchi creates, runs, and builds HyperChi applications.
//
// Commands:
//
//	hyperchi new <name>    create a project (--template full or site)
//	hyperchi dev           run the Go server and Vite together
//	hyperchi build         build the frontend assets and the Go binary
//	hyperchi serve         run the built binary
//	hyperchi env           load, switch, validate, and list .env files
//	hyperchi air           set up Air live reload
//	hyperchi deploy        write deployment files and build a Docker image
//	hyperchi helpers       print examples for the helpers package
//
// Quick start:
//
//	hyperchi new my-app
//	cd my-app
//	go mod tidy
//	task dev
//
// A full project is a Go server on chi and HyperChi, Vite and Tailwind for
// assets, htmx and HyperChi directives in the browser, and templates split
// into layouts, components, and pages. The site template is Go templates and
// static files only.
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	"github.com/spf13/cobra"
)

// bundledAssets ships the compiled HyperChi JS into every CLI binary.
// Source of truth lives at frontend/dist/ and frontend/assets/scripts/;
// run `task sync-cli-assets` from the framework repo to refresh these
// copies after changes.
//
//go:embed assets/hyperchi-directives.js assets/hyperchi-view-transitions.js
var bundledAssets embed.FS

// scaffoldFS holds the project template trees under _scaffold/ and _site/.
// The leading underscore keeps the go tool from compiling their main.go files
// as part of this package; the all: prefix makes embed include them anyway.
//
//go:embed all:_scaffold all:_site
var scaffoldFS embed.FS

// mustReadAsset returns an embedded runtime asset. It panics if the asset is
// missing, which means the CLI binary was built wrong.
func mustReadAsset(name string) string {
	data, err := bundledAssets.ReadFile("assets/" + name)
	if err != nil {
		panic(fmt.Sprintf("hyperchi CLI is missing embedded asset %q: %v", name, err))
	}
	return string(data)
}

// frameworkBuildPath is set at build time with -ldflags. `task cli` and
// `task scaffold` inject the checkout path, so scaffolded projects get a
// `replace github.com/regiellis/hyperchi => <path>` directive without
// --framework-path. A plain `go build` leaves it empty, and new projects
// resolve the framework from the module proxy.
var frameworkBuildPath = ""

// resolveFrameworkPath picks the local-checkout path to use, in priority order:
//  1. explicit --framework-path flag (caller-supplied, including empty)
//  2. HYPERCHI_FRAMEWORK_PATH env var
//  3. frameworkBuildPath baked in at build time
//  4. "" (no replace; falls through to public-proxy resolution)
//
// A candidate without a go.mod is skipped, so a stale path never becomes a
// replace directive.
func resolveFrameworkPath(explicit string) string {
	candidates := []string{explicit, os.Getenv("HYPERCHI_FRAMEWORK_PATH"), frameworkBuildPath}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c, "go.mod")); err == nil {
			return c
		}
	}
	return ""
}

// Styles for CLI status messages.
var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)
	infoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6"))
	titleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
)

// main runs the CLI. A failing command prints its error in the error style and
// exits with status 1.
func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, errorStyle.Render("Error: %v\n"), err)
		os.Exit(1)
	}
}

// newRootCmd assembles the command tree. Tests inspect it, so keep every
// registration here rather than in main.
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "hyperchi",
		Short: "HyperChi - Simple build tools for Go + HTMX + Tailwind",
		Long: titleStyle.Render("HyperChi Build Tools") + "\n\n" +
			"Simple build tools for Go web development with HTMX, Tailwind CSS, and HyperChi directives.\n" +
			"No generators, no magic - just the tools you need to build and serve your app.",
	}

	rootCmd.AddCommand(
		newCmd(),
		devCmd(),
		buildCmd(),
		serveCmd(),
		envCmd(),
		airCmd(),
		deployCmd(),
		helpersCmd(),
	)
	return rootCmd
}

// newCmd creates the `new` command. --template full (the default) writes the
// _scaffold tree: Go server, Vite and Tailwind, htmx and directives, templates,
// package.json, and a Taskfile. --template site writes the _site tree: Go
// templates and static files with no Node toolchain.
func newCmd() *cobra.Command {
	var frameworkPath string
	var templateName string

	cmd := &cobra.Command{
		Use:   "new [project-name]",
		Short: "Create a new HyperChi project structure",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := args[0]
			if err := validateProjectName(projectName); err != nil {
				return err
			}
			resolved := resolveFrameworkPath(frameworkPath)

			fmt.Printf("%s Creating project: %s\n",
				infoStyle.Render("INFO"), titleStyle.Render(projectName))

			return createProject(projectName, resolved, templateName)
		},
	}

	cmd.Flags().StringVar(&templateName, "template", "full", "Project template: full (Vite and HTMX) or site (Go templates and static files)")
	cmd.Flags().StringVar(&frameworkPath, "framework-path", "",
		"Local path to the HyperChi framework checkout. When set, the new "+
			"project's go.mod gets a `replace github.com/regiellis/hyperchi => <path>` "+
			"directive and `go mod tidy` runs immediately. Defaults are tried "+
			"in order: this flag → $HYPERCHI_FRAMEWORK_PATH env var → path baked "+
			"in at build time via -ldflags. End users on the published framework "+
			"leave this unset.")

	return cmd
}

// projectNameRe matches names safe to substitute into a Go module path and a
// package.json "name" field: a leading lowercase letter followed by lowercase
// letters, digits, hyphens, or underscores. This rejects spaces, uppercase,
// path separators, dots, and leading digits/punctuation that would otherwise
// produce an invalid go.mod module line or package.json.
var projectNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// validateProjectName rejects project names that would break the generated
// go.mod / package.json before any files are written.
func validateProjectName(name string) error {
	if !projectNameRe.MatchString(name) {
		return fmt.Errorf(
			"invalid project name %q: use lowercase letters, digits, hyphens, or "+
				"underscores, starting with a letter (kebab-case, e.g. \"my-app\")",
			name)
	}
	return nil
}

// scaffoldRenames maps scaffold source files to the names written into a new
// project. The agent guide is stored under neutral names because a public
// mirror of this repository strips every file named like the repository's own
// agent tooling (AGENTS.md, CLAUDE.md); the generated project still gets them.
var scaffoldRenames = map[string]string{
	"agent-guide.md":      "AGENTS.md",
	"assistant-bridge.md": "CLAUDE.md",
}

// createProject writes project name from the chosen template tree and, when
// frameworkPath is set, points its go.mod at that local checkout.
func createProject(name, frameworkPath, templateName string) error {
	if err := validateProjectName(name); err != nil {
		return err
	}
	root := "_scaffold"
	if templateName == "site" {
		root = "_site"
	} else if templateName != "full" {
		return fmt.Errorf("unknown template %q: choose full or site", templateName)
	}
	if err := os.Mkdir(name, 0755); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}
	// Walk the embedded scaffold tree and copy every file into the new project,
	// renaming `_scaffold/foo/bar` to `name/foo/bar`. Three transformations along
	// the way:
	//   1. files named `*.tpl` are written without the `.tpl` suffix (used for
	//      go.mod, which would otherwise create an embed module-boundary).
	//   2. the agent guide is written under the names agents read (see
	//      scaffoldRenames).
	//   3. the `__APPNAME__` placeholder is replaced with the project name in
	//      every file.
	err := fs.WalkDir(scaffoldFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(name, 0755)
		}

		outRel := strings.TrimSuffix(rel, ".tpl")
		if renamed, ok := scaffoldRenames[outRel]; ok {
			outRel = renamed
		}
		outPath := filepath.Join(name, outRel)

		if d.IsDir() {
			return os.MkdirAll(outPath, 0755)
		}

		data, err := scaffoldFS.ReadFile(path)
		if err != nil {
			return err
		}
		content := strings.ReplaceAll(string(data), "__APPNAME__", name)

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(outPath, []byte(content), 0644)
	})
	if err != nil {
		return fmt.Errorf("scaffold walk: %w", err)
	}

	// The runtime JS comes from cmd/hyperchi/assets, a separate embed that
	// `task sync-cli-assets` refreshes from the frontend build.
	if templateName == "full" {
		staticDir := filepath.Join(name, "static", "js")
		if err := os.MkdirAll(staticDir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(staticDir, "hyperchi-directives.js"),
			[]byte(mustReadAsset("hyperchi-directives.js")), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(staticDir, "hyperchi-view-transitions.js"),
			[]byte(mustReadAsset("hyperchi-view-transitions.js")), 0644); err != nil {
			return err
		}

	}
	// If a local framework path was given, inject a replace directive into the
	// scaffolded go.mod and resolve modules. Use it whenever the framework version
	// the templates require is not yet on the module proxy; without it, `go run .`
	// inside the new project cannot resolve the framework.
	if frameworkPath != "" {
		absPath, err := filepath.Abs(frameworkPath)
		if err != nil {
			return fmt.Errorf("resolve framework path: %w", err)
		}
		if _, err := os.Stat(filepath.Join(absPath, "go.mod")); err != nil {
			return fmt.Errorf("--framework-path %q does not look like a HyperChi checkout (no go.mod): %w", absPath, err)
		}
		fmt.Printf("%s Wiring local replace: %s\n", infoStyle.Render("INFO"), absPath)
		editCmd := exec.Command("go", "mod", "edit",
			"-replace=github.com/regiellis/hyperchi="+absPath)
		editCmd.Dir = name
		editCmd.Stdout, editCmd.Stderr = os.Stdout, os.Stderr
		if err := editCmd.Run(); err != nil {
			return fmt.Errorf("go mod edit failed: %w", err)
		}
		fmt.Printf("%s Resolving modules (go mod tidy)...\n", infoStyle.Render("INFO"))
		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Dir = name
		tidyCmd.Stdout, tidyCmd.Stderr = os.Stdout, os.Stderr
		if err := tidyCmd.Run(); err != nil {
			return fmt.Errorf("go mod tidy failed: %w", err)
		}
	}

	fmt.Printf("\n%s Project created successfully!\n", successStyle.Render("SUCCESS"))
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  cd %s\n", name)
	if frameworkPath == "" {
		fmt.Printf("  go mod tidy       # resolve dependencies (needs framework published, or run `hyperchi new --framework-path=…`)\n")
	}
	if templateName == "site" {
		fmt.Printf("  task dev          # Go server; visit http://localhost:8080\n")
		return nil
	}
	fmt.Printf("  task dev          # backend + Vite together; visit http://localhost:8080\n")
	fmt.Printf("  task dev:air      # ... with backend hot reload (needs `air` installed)\n")
	fmt.Printf("\nThe page that comes up is a self-documenting docs site. Read it to learn the framework.\n")

	return nil
}

// devCmd creates the `dev` command. By default it runs the Go server
// (`go run .` on --port, 8080 by default) and Vite (`npm run dev` on 3000), and
// stops both, with their children, on Ctrl+C or when either exits. --go-only
// and --assets-only run one of them.
//
// Vite serves assets only; it does not proxy. The browser loads pages from the
// Go server, which pulls JS and CSS from Vite through VITE_DEV_SERVER.
func devCmd() *cobra.Command {
	var port int
	var goOnly bool
	var assetsOnly bool

	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Start development servers (Go + Vite)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if goOnly {
				return startGoServer(port)
			}
			if assetsOnly {
				return startViteServer()
			}
			return startBothServers(port)
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 8080, "Go server port")
	cmd.Flags().BoolVar(&goOnly, "go-only", false, "Start only Go server")
	cmd.Flags().BoolVar(&assetsOnly, "assets-only", false, "Start only Vite server")

	return cmd
}

// viteDevPort is the port the scaffold's vite.config.js binds.
const viteDevPort = 3000

// shutdownGrace bounds how long a dev server gets to exit after SIGINT before
// its process group is killed.
const shutdownGrace = 5 * time.Second

// startBothServers runs Vite and the Go server until a signal arrives or one
// of them exits, then stops both.
func startBothServers(port int) error {
	viteURL := fmt.Sprintf("http://localhost:%d", viteDevPort)
	fmt.Printf("%s Starting development servers...\n", infoStyle.Render("INFO"))
	fmt.Printf("  Go server:   http://localhost:%d\n", port)
	fmt.Printf("  Vite server: %s\n", viteURL)

	// Each server runs in its own process group so shutdown reaches the
	// processes that hold the ports: Vite's node child under npm, and the
	// compiled binary under `go run`. Signaling only the parents would orphan them.
	viteCmd := exec.Command("npm", "run", "dev")
	viteCmd.Stdout = os.Stdout
	viteCmd.Stderr = os.Stderr
	vite, err := startGroup(viteCmd)
	if err != nil {
		return fmt.Errorf("failed to start Vite: %w", err)
	}

	// `go run .` so every package file compiles, not only main.go.
	goCmd := exec.Command("go", "run", ".")
	goCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		"VITE_DEV_SERVER="+viteURL,
	)
	goCmd.Stdout = os.Stdout
	goCmd.Stderr = os.Stderr
	goServer, err := startGroup(goCmd)
	if err != nil {
		vite.stop(shutdownGrace)
		return fmt.Errorf("failed to start Go server: %w", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	// Stop both on Ctrl+C, or when either server exits on its own (a compile
	// error, a port already in use) so the other is not left running.
	var exited error
	select {
	case <-signals:
	case <-goServer.done:
		exited = unexpectedExit("go server", goServer.err)
	case <-vite.done:
		exited = unexpectedExit("vite", vite.err)
	}

	fmt.Printf("\n%s Shutting down servers...\n", infoStyle.Render("INFO"))
	goServer.stop(shutdownGrace)
	vite.stop(shutdownGrace)

	return exited
}

// unexpectedExit reports a dev server that stopped before the user asked.
func unexpectedExit(name string, err error) error {
	if err != nil {
		return fmt.Errorf("%s exited: %w", name, err)
	}
	return fmt.Errorf("%s exited", name)
}

// processGroup is a started command that leads its own process group, so the
// whole subtree it spawns can be signaled at once.
type processGroup struct {
	cmd  *exec.Cmd
	done chan struct{} // closed once cmd.Wait returns
	err  error         // cmd.Wait result; read only after done is closed
}

// startGroup starts cmd as the leader of a new process group and reaps it in
// the background.
func startGroup(cmd *exec.Cmd) (*processGroup, error) {
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	g := &processGroup{cmd: cmd, done: make(chan struct{})}
	go func() {
		g.err = cmd.Wait()
		close(g.done)
	}()
	return g, nil
}

// stop sends SIGINT to the whole group, waits up to grace for the leader to
// exit, then kills whatever is left in the group. The final kill is
// unconditional: a member that ignores SIGINT (a shell's background job, a
// wedged child) would otherwise outlive a leader that exited cleanly.
func (g *processGroup) stop(grace time.Duration) {
	if err := interruptGroup(g.cmd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: interrupting %s: %v\n", g.cmd.Path, err)
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-g.done:
	case <-timer.C:
	}
	if err := killGroup(g.cmd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: killing %s: %v\n", g.cmd.Path, err)
	}
	<-g.done
}

func startGoServer(port int) error {
	fmt.Printf("%s Starting Go server on port %d...\n", infoStyle.Render("INFO"), port)

	cmd := exec.Command("go", "run", ".")
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func startViteServer() error {
	fmt.Printf("%s Starting Vite server...\n", infoStyle.Render("INFO"))

	cmd := exec.Command("npm", "run", "dev")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// buildCmd creates the `build` command: `npm run build` in the project root
// (Vite writes dist/), then `go build -ldflags="-s -w"` into --output, bin/app
// by default. `hyperchi serve` runs the result.
func buildCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build application for production",
		RunE: func(cmd *cobra.Command, args []string) error {
			return buildApp(output)
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "bin/app", "Output binary path")
	return cmd
}

// buildApp builds the frontend assets, then the Go binary at output.
func buildApp(output string) error {
	fmt.Printf("%s Building application...\n", infoStyle.Render("INFO"))

	// Build frontend assets. The layout is flat: package.json sits at the project root.
	fmt.Printf("%s Building frontend assets...\n", infoStyle.Render("INFO"))
	frontendCmd := exec.Command("npm", "run", "build")
	frontendCmd.Stdout = os.Stdout
	frontendCmd.Stderr = os.Stderr

	if err := frontendCmd.Run(); err != nil {
		return fmt.Errorf("frontend build failed: %w", err)
	}

	// Build from `.` so every package file compiles, not only main.go.
	fmt.Printf("%s Building Go binary...\n", infoStyle.Render("INFO"))
	goCmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", output, ".")
	goCmd.Stdout = os.Stdout
	goCmd.Stderr = os.Stderr

	if err := goCmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	fmt.Printf("%s Build completed successfully!\n", successStyle.Render("SUCCESS"))
	fmt.Printf("  Binary: %s\n", output)
	fmt.Printf("  Assets: dist/\n")

	return nil
}

// serveCmd creates the `serve` command, which runs a built binary with PORT set.
func serveCmd() *cobra.Command {
	var binary string
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the built application",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serveApp(binary, port)
		},
	}

	cmd.Flags().StringVarP(&binary, "binary", "b", "bin/app", "Binary to serve")
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "Server port")

	return cmd
}

func serveApp(binary string, port int) error {
	fmt.Printf("%s Starting production server on port %d...\n",
		infoStyle.Render("INFO"), port)

	cmd := exec.Command(servePath(binary))
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// servePath turns the --binary flag into a path exec runs directly. Only a bare
// filename gets a "./" prefix, so it resolves in the working directory instead
// of through $PATH; absolute paths and paths with a directory are used as given.
func servePath(binary string) string {
	if filepath.IsAbs(binary) || strings.ContainsRune(binary, filepath.Separator) ||
		strings.ContainsRune(binary, '/') {
		return binary
	}
	return "." + string(filepath.Separator) + binary
}

// envCmd creates the `env` command group:
//
//	load [files...]     load .env files (default: .env, .env.local, .env.development)
//	switch <env>        copy .env.<env> over .env
//	validate [vars...]  fail unless every named variable is set
//	list                print the loaded variables, masking any whose name
//	                    contains secret, password, or token
func envCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Environment variable management",
	}

	cmd.AddCommand(
		envLoadCmd(),
		envSwitchCmd(),
		envValidateCmd(),
		envListCmd(),
	)

	return cmd
}

func envLoadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "load [files...]",
		Short: "Load environment variables from .env files",
		RunE: func(cmd *cobra.Command, args []string) error {
			env := helpers.NewEnv()

			if len(args) == 0 {
				if err := env.LoadDefault(); err != nil {
					return fmt.Errorf("failed to load environment: %v", err)
				}
			} else {
				if err := env.LoadFiles(args...); err != nil {
					return fmt.Errorf("failed to load environment files: %v", err)
				}
			}

			fmt.Printf("%s Environment loaded from: %v\n",
				successStyle.Render("SUCCESS"), env.LoadedFiles())
			return nil
		},
	}
}

func envSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "switch [environment]",
		Short: "Switch to different environment (development, production, etc.)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			environment := args[0]

			if err := helpers.SwitchEnvironment(environment); err != nil {
				return fmt.Errorf("failed to switch environment: %v", err)
			}

			fmt.Printf("%s Switched to %s environment\n",
				successStyle.Render("SUCCESS"), environment)
			return nil
		},
	}
}

func envValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [required-vars...]",
		Short: "Validate that required environment variables are present",
		RunE: func(cmd *cobra.Command, args []string) error {
			env := helpers.NewEnv()
			if err := env.LoadDefault(); err != nil {
				return fmt.Errorf("failed to load environment: %v", err)
			}

			if err := env.Validate(args); err != nil {
				return fmt.Errorf("environment validation failed: %v", err)
			}

			fmt.Printf("%s All required environment variables are present\n",
				successStyle.Render("SUCCESS"))
			return nil
		},
	}
}

func envListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all environment variables",
		RunE: func(cmd *cobra.Command, args []string) error {
			env := helpers.NewEnv()
			if err := env.LoadDefault(); err != nil {
				return fmt.Errorf("failed to load environment: %v", err)
			}

			all := env.All()

			fmt.Printf("%s Environment variables (%d total):\n",
				infoStyle.Render("INFO"), len(all))

			for key, value := range all {
				if strings.Contains(strings.ToLower(key), "secret") ||
					strings.Contains(strings.ToLower(key), "password") ||
					strings.Contains(strings.ToLower(key), "token") {
					value = "***MASKED***"
				}
				fmt.Printf("  %s=%s\n", key, value)
			}
			return nil
		},
	}
}

// airCmd creates the `air` command group for Air, which rebuilds and restarts
// the Go server on source changes:
//
//	init     write .air.toml and dev.sh (loads .env, then runs air)
//	install  print install instructions
//	check    report whether air is installed
func airCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "air",
		Short: "Air live reload development tool integration",
	}

	cmd.AddCommand(
		airInitCmd(),
		airInstallCmd(),
		airCheckCmd(),
	)

	return cmd
}

func airInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize Air configuration for HyperChi",
		RunE: func(cmd *cobra.Command, args []string) error {
			air := helpers.NewAirHelper(".")

			if err := air.InitAir(); err != nil {
				return fmt.Errorf("failed to initialize Air: %v", err)
			}

			if err := air.CreateDevScript(); err != nil {
				return fmt.Errorf("failed to create dev script: %v", err)
			}

			fmt.Printf("%s Air configuration created!\n",
				successStyle.Render("SUCCESS"))
			fmt.Printf("  .air.toml - Air configuration\n")
			fmt.Printf("  dev.sh - Development script\n\n")
			fmt.Printf("Start development with: ./dev.sh\n")

			return nil
		},
	}
}

func airInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Show Air installation instructions",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s Air Installation Instructions:\n\n",
				infoStyle.Render("INFO"))
			fmt.Print(helpers.NewAirHelper(".").GetInstallInstructions())
			return nil
		},
	}
}

func airCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check if Air is installed and available",
		RunE: func(cmd *cobra.Command, args []string) error {
			if helpers.NewAirHelper(".").IsAirInstalled() {
				fmt.Printf("%s Air is installed and ready!\n",
					successStyle.Render("SUCCESS"))
			} else {
				fmt.Printf("%s Air is not installed\n",
					errorStyle.Render("ERROR"))
				fmt.Printf("Run 'hyperchi air install' for installation instructions\n")
			}

			return nil
		},
	}
}

// deployCmd creates the `deploy` command group:
//
//	init        write Dockerfile, docker-compose.yml, .env.production, build.sh,
//	            deploy.sh, nginx.conf, <app>.service, and health.go
//	docker      build the image hyperchi-app from the Dockerfile
//	production  copy .env.production over .env and warn when APP_NAME,
//	            ENVIRONMENT, or JWT_SECRET is missing
//
// Health endpoints come from the framework: call app.CreateHealthEndpoints() in
// main.go to serve /health, /health/ready, and /health/live.
func deployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deployment and production management",
	}

	cmd.AddCommand(
		deployInitCmd(),
		deployDockerCmd(),
		deployProdCmd(),
	)

	return cmd
}

func deployInitCmd() *cobra.Command {
	var appName string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize deployment files (Dockerfile, docker-compose, etc.)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if appName == "" {
				// Default the app name to the current directory's name.
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("determine app name (pass --name): %w", err)
				}
				appName = filepath.Base(cwd)
			}

			if err := helpers.CreateDeploymentFiles(".", appName); err != nil {
				return fmt.Errorf("failed to create deployment files: %v", err)
			}

			fmt.Printf("%s Deployment files created for %s!\n",
				successStyle.Render("SUCCESS"), appName)
			fmt.Printf("  Dockerfile - Optimized container build\n")
			fmt.Printf("  docker-compose.yml - Container orchestration\n")
			fmt.Printf("  .env.production - Production environment template\n")
			fmt.Printf("  build.sh - Production build script\n")
			fmt.Printf("  deploy.sh - Deployment script\n")
			fmt.Printf("  nginx.conf - Reverse proxy configuration\n")
			fmt.Printf("  %s.service - Systemd service file\n", appName)

			return nil
		},
	}

	cmd.Flags().StringVarP(&appName, "name", "n", "", "Application name")
	return cmd
}

func deployDockerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docker",
		Short: "Build and manage Docker containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s Building Docker image...\n",
				infoStyle.Render("INFO"))

			dockerCmd := exec.Command("docker", "build", "-t", "hyperchi-app", ".")
			dockerCmd.Stdout = os.Stdout
			dockerCmd.Stderr = os.Stderr

			if err := dockerCmd.Run(); err != nil {
				return fmt.Errorf("docker build failed: %v", err)
			}

			fmt.Printf("%s Docker image built successfully!\n",
				successStyle.Render("SUCCESS"))
			fmt.Printf("Run with: docker run -p 8080:8080 hyperchi-app\n")

			return nil
		},
	}
}

func deployProdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "production",
		Short: "Switch to production mode and validate configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := helpers.SwitchEnvironment("production"); err != nil {
				return fmt.Errorf("failed to switch to production: %v", err)
			}

			env := helpers.NewEnv()
			if err := env.LoadFile(".env.production"); err != nil {
				fmt.Printf("%s Could not load .env.production: %v\n",
					errorStyle.Render("WARNING"), err)
			}

			required := []string{"APP_NAME", "ENVIRONMENT", "JWT_SECRET"}
			if err := env.Validate(required); err != nil {
				fmt.Printf("%s Warning: %v\n",
					errorStyle.Render("WARNING"), err)
			}

			fmt.Printf("%s Switched to production mode\n",
				successStyle.Render("SUCCESS"))
			fmt.Printf("Make sure to set all required environment variables\n")

			return nil
		},
	}
}

// helpersCmd creates the `helpers` command group, which prints examples for the
// hyperchi/helpers package: `list` names the categories, and `error`, `string`,
// and `file` print code samples.
func helpersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "helpers",
		Short: "Developer experience helpers showcase and examples",
	}

	cmd.AddCommand(
		helpersListCmd(),
		helpersErrorCmd(),
		helpersStringCmd(),
		helpersFileCmd(),
	)

	return cmd
}

func helpersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all available helper categories",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s HyperChi Developer Experience Helpers:\n\n",
				titleStyle.Render("HELPERS"))

			helpers := []struct {
				name        string
				description string
				examples    []string
			}{
				{
					"Error Handling",
					"Python-style error handling patterns",
					[]string{"Must", "Try", "Catch", "Default"},
				},
				{
					"Logging",
					"Leveled logging with colors and caller info",
					[]string{"Debug", "Info", "Warn", "Error", "Fatal"},
				},
				{
					"Environment",
					"Automatic .env loading with type conversion",
					[]string{"GetEnv", "GetEnvInt", "GetEnvBool", "LoadEnv"},
				},
				{
					"File Operations",
					"Safe file I/O with fluent API",
					[]string{"ReadFile", "WriteFile", "FileExists"},
				},
				{
					"String Manipulation",
					"Fluent string operations and case conversion",
					[]string{"Trim", "Lower", "CamelCase", "Slugify"},
				},
				{
					"Collections",
					"Map, Filter, Reduce with Go generics",
					[]string{"Map", "Filter", "Reduce", "Range"},
				},
				{
					"JSON & HTTP",
					"Fluent JSON and HTTP client operations",
					[]string{"NewJSON", "HTTPGet", "HTTPPost"},
				},
				{
					"Profiler",
					"Built-in performance monitoring",
					[]string{"StartProfiler", "TimeOperation", "GetStats"},
				},
				{
					"Air Integration",
					"Live reload development setup",
					[]string{"InitAir", "CreateDevScript", "IsAirInstalled"},
				},
				{
					"Deployment",
					"Production deployment helpers",
					[]string{"CreateDockerfile", "SwitchToProduction"},
				},
			}

			for _, helper := range helpers {
				fmt.Printf("%s - %s\n",
					successStyle.Render(helper.name), helper.description)
				fmt.Printf("  Examples: %s\n\n", strings.Join(helper.examples, ", "))
			}

			fmt.Printf("Try: hyperchi helpers [category] for specific examples\n")
			return nil
		},
	}
}

func helpersErrorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "error",
		Short: "Show error handling examples",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s Error Handling Examples:\n\n",
				titleStyle.Render("ERROR HANDLING"))

			examples := []string{
				"// Must pattern - panic on error",
				"helpers.Must(err)",
				"value := helpers.MustValue(someFunc())",
				"",
				"// Try pattern - return error without panic",
				"err := helpers.Try(func() error {",
				"    return riskyOperation()",
				"})",
				"",
				"// Catch pattern - handle errors with callback",
				"helpers.Catch(func() error {",
				"    return mightFail()",
				"}, func(err error) {",
				"    app.Helpers.Logger.Error(\"Operation failed: %v\", err)",
				"})",
				"",
				"// Default values on error",
				"value, err := someFunc()",
				"value = helpers.Default(value, err, \"fallback\")",
			}

			for _, example := range examples {
				fmt.Println(example)
			}

			return nil
		},
	}
}

func helpersStringCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "string",
		Short: "Show string manipulation examples",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s String Manipulation Examples:\n\n",
				titleStyle.Render("STRING HELPERS"))

			examples := []string{
				"// Fluent string operations",
				"result := helpers.S(\"  Hello World  \")",
				"    .Trim()",
				"    .Lower()",
				"    .Replace(\"world\", \"hyperchi\")",
				"    .Capitalize()",
				"    .String()",
				"// Result: \"Hello hyperchi\"",
				"",
				"// Case conversions",
				"camel := helpers.CamelCase(\"hello_world\")      // \"helloWorld\"",
				"snake := helpers.SnakeCase(\"HelloWorld\")       // \"hello_world\"",
				"kebab := helpers.KebabCase(\"HelloWorld\")       // \"hello-world\"",
				"slug := helpers.Slugify(\"My Blog Post\")        // \"my-blog-post\"",
				"",
				"// String validation",
				"if helpers.S(email).IsNotEmpty() && helpers.S(email).Contains(\"@\") {",
				"    // Valid email format",
				"}",
			}

			for _, example := range examples {
				fmt.Println(example)
			}

			return nil
		},
	}
}

func helpersFileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "file",
		Short: "Show file operation examples",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s File Operation Examples:\n\n",
				titleStyle.Render("FILE HELPERS"))

			examples := []string{
				"// Simple file operations",
				"content, err := helpers.ReadFile(\"config.txt\")  // Returns the read error",
				"helpers.WriteFile(\"output.txt\", \"data\")   // Creates dirs if needed",
				"",
				"// File operations with defaults",
				"content := helpers.ReadFileOr(\"config.txt\", \"default config\")",
				"",
				"// Fluent file operations",
				"err := helpers.File(\"data.txt\")",
				"    .Write(\"Hello\")",
				"    .Append(\" World\")",
				"    .Copy(\"backup.txt\")",
				"    .Error()",
				"",
				"// File existence and properties",
				"if helpers.FileExists(\"important.txt\") {",
				"    size := helpers.FileSize(\"important.txt\")",
				"}",
			}

			for _, example := range examples {
				fmt.Println(example)
			}

			return nil
		},
	}
}

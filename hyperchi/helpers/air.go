package helpers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AirConfig is an Air live-reload configuration. ToTOML renders it as an
// .air.toml file.
type AirConfig struct {
	Root string `json:"root"`

	// TmpDir holds Air's build output.
	TmpDir string `json:"tmp_dir"`

	Build AirBuildConfig `json:"build"`

	// Watch mirrors the watch lists in Build. ToTOML does not write it,
	// because Air reads those settings from the [build] section.
	Watch AirWatchConfig `json:"watch"`

	Log AirLogConfig `json:"log"`

	Misc AirMiscConfig `json:"misc"`
}

// AirBuildConfig is the [build] section of an Air configuration.
type AirBuildConfig struct {
	// PreCmd lists commands Air runs before each build.
	PreCmd []string `json:"pre_cmd"`

	Cmd string `json:"cmd"`

	// Bin is the binary Air runs after a build.
	Bin string `json:"bin"`

	// FullBin, when set, is the full command Air runs instead of Bin.
	FullBin string `json:"full_bin"`

	// Args are passed to the binary.
	Args []string `json:"args"`

	// Log is the file Air writes build errors to.
	Log string `json:"log"`

	ExcludeDir []string `json:"exclude_dir"`

	ExcludeFile []string `json:"exclude_file"`

	ExcludeRegex []string `json:"exclude_regex"`

	// IncludeExt lists the file extensions, without dots, that trigger a
	// rebuild.
	IncludeExt []string `json:"include_ext"`

	// KillDelay is a duration string such as "0s" or "500ms".
	KillDelay string `json:"kill_delay"`

	// SendInterrupt sends SIGINT before killing the process.
	SendInterrupt bool `json:"send_interrupt"`

	StopOnRoot bool `json:"stop_on_root"`
}

// AirWatchConfig holds the watch lists kept in AirConfig.Watch.
type AirWatchConfig struct {
	ExcludeDir []string `json:"exclude_dir"`

	ExcludeFile []string `json:"exclude_file"`

	ExcludeRegex []string `json:"exclude_regex"`

	IncludeExt []string `json:"include_ext"`
}

// AirLogConfig is the [log] section of an Air configuration.
type AirLogConfig struct {
	// AddTime is written as Air's "time" key.
	AddTime bool `json:"add_time"`

	MainOnly bool `json:"main_only"`
}

// AirMiscConfig is the [misc] section of an Air configuration.
type AirMiscConfig struct {
	// CleanOnExit removes TmpDir when Air exits.
	CleanOnExit bool `json:"clean_on_exit"`
}

// DefaultAirConfig returns an Air configuration that builds ./tmp/main from
// the current directory and watches Go, template, HTML, JS, and CSS files.
func DefaultAirConfig() *AirConfig {
	return &AirConfig{
		Root:   ".",
		TmpDir: "tmp",
		Build: AirBuildConfig{
			PreCmd:  []string{},
			Cmd:     "go build -o ./tmp/main .",
			Bin:     "tmp/main",
			FullBin: "",
			Args:    []string{},
			Log:     "tmp/build-errors.log",
			ExcludeDir: []string{
				"assets", "tmp", "vendor", "testdata",
				"examples", "docs", "node_modules", ".git",
				"dist", "build", "public",
			},
			ExcludeFile: []string{
				"README.md", "Makefile", "*.md", "*.yml", "*.yaml",
				"*.json", "*.toml", "*.lock", "go.sum",
			},
			ExcludeRegex: []string{
				"_test\\.go", ".*_gen\\.go", "\\.#.*", ".*~",
			},
			IncludeExt: []string{
				"go", "tpl", "tmpl", "html", "js", "css",
			},
			KillDelay:     "0s",
			SendInterrupt: false,
			StopOnRoot:    false,
		},
		Watch: AirWatchConfig{
			ExcludeDir: []string{
				"assets", "tmp", "vendor", "testdata",
				"examples", "docs", "node_modules", ".git",
			},
			ExcludeFile: []string{},
			ExcludeRegex: []string{
				"_test\\.go", ".*_gen\\.go",
			},
			IncludeExt: []string{
				"go", "tpl", "tmpl", "html", "js", "css",
			},
		},
		Log: AirLogConfig{
			AddTime:  false,
			MainOnly: false,
		},
		Misc: AirMiscConfig{
			CleanOnExit: false,
		},
	}
}

// HyperChiAirConfig returns DefaultAirConfig extended for a HyperChi project:
// it also watches .md, .env, .gotmpl, and .gohtml files and skips the static,
// uploads, logs, storage, and cache directories.
func HyperChiAirConfig() *AirConfig {
	config := DefaultAirConfig()

	config.Build.IncludeExt = append(config.Build.IncludeExt, "md", "env")
	config.Watch.IncludeExt = append(config.Watch.IncludeExt, "md", "env")

	config.Build.IncludeExt = append(config.Build.IncludeExt, "gotmpl", "gohtml")
	config.Watch.IncludeExt = append(config.Watch.IncludeExt, "gotmpl", "gohtml")

	config.Build.ExcludeDir = append(config.Build.ExcludeDir,
		"static", "uploads", "logs", "storage", "cache")
	config.Watch.ExcludeDir = append(config.Watch.ExcludeDir,
		"static", "uploads", "logs", "storage", "cache")

	return config
}

// ToTOML renders the configuration as the contents of an .air.toml file,
// with the [build], [color], [log], and [misc] sections. Strings are quoted
// and escaped as TOML basic strings.
func (ac *AirConfig) ToTOML() string {
	var toml strings.Builder

	toml.WriteString("# Air configuration for HyperChi\n")
	toml.WriteString("# Generated automatically - modify as needed\n\n")

	fmt.Fprintf(&toml, "root = %s\n", tomlString(ac.Root))
	fmt.Fprintf(&toml, "tmp_dir = %s\n\n", tomlString(ac.TmpDir))

	toml.WriteString("[build]\n")
	if len(ac.Build.PreCmd) > 0 {
		toml.WriteString("pre_cmd = [")
		for i, cmd := range ac.Build.PreCmd {
			if i > 0 {
				toml.WriteString(", ")
			}
			toml.WriteString(tomlString(cmd))
		}
		toml.WriteString("]\n")
	}
	fmt.Fprintf(&toml, "cmd = %s\n", tomlString(ac.Build.Cmd))
	fmt.Fprintf(&toml, "bin = %s\n", tomlString(ac.Build.Bin))
	if ac.Build.FullBin != "" {
		fmt.Fprintf(&toml, "full_bin = %s\n", tomlString(ac.Build.FullBin))
	}
	if len(ac.Build.Args) > 0 {
		toml.WriteString("args = [")
		for i, arg := range ac.Build.Args {
			if i > 0 {
				toml.WriteString(", ")
			}
			toml.WriteString(tomlString(arg))
		}
		toml.WriteString("]\n")
	}
	fmt.Fprintf(&toml, "log = %s\n", tomlString(ac.Build.Log))

	if len(ac.Build.ExcludeDir) > 0 {
		toml.WriteString("exclude_dir = [")
		for i, dir := range ac.Build.ExcludeDir {
			if i > 0 {
				toml.WriteString(", ")
			}
			toml.WriteString(tomlString(dir))
		}
		toml.WriteString("]\n")
	}

	if len(ac.Build.ExcludeFile) > 0 {
		toml.WriteString("exclude_file = [")
		for i, file := range ac.Build.ExcludeFile {
			if i > 0 {
				toml.WriteString(", ")
			}
			toml.WriteString(tomlString(file))
		}
		toml.WriteString("]\n")
	}

	if len(ac.Build.ExcludeRegex) > 0 {
		toml.WriteString("exclude_regex = [")
		for i, regex := range ac.Build.ExcludeRegex {
			if i > 0 {
				toml.WriteString(", ")
			}
			toml.WriteString(tomlString(regex))
		}
		toml.WriteString("]\n")
	}

	if len(ac.Build.IncludeExt) > 0 {
		toml.WriteString("include_ext = [")
		for i, ext := range ac.Build.IncludeExt {
			if i > 0 {
				toml.WriteString(", ")
			}
			toml.WriteString(tomlString(ext))
		}
		toml.WriteString("]\n")
	}

	fmt.Fprintf(&toml, "kill_delay = %s\n", tomlString(ac.Build.KillDelay))
	fmt.Fprintf(&toml, "send_interrupt = %t\n", ac.Build.SendInterrupt)
	fmt.Fprintf(&toml, "stop_on_root = %t\n\n", ac.Build.StopOnRoot)

	toml.WriteString("[color]\n")
	toml.WriteString("main = \"magenta\"\n")
	toml.WriteString("watcher = \"cyan\"\n")
	toml.WriteString("build = \"yellow\"\n")
	toml.WriteString("runner = \"green\"\n\n")

	toml.WriteString("[log]\n")
	fmt.Fprintf(&toml, "time = %t\n", ac.Log.AddTime)
	fmt.Fprintf(&toml, "main_only = %t\n\n", ac.Log.MainOnly)

	toml.WriteString("[misc]\n")
	fmt.Fprintf(&toml, "clean_on_exit = %t\n", ac.Misc.CleanOnExit)

	return toml.String()
}

// tomlString quotes s as a TOML basic string. Backslashes must be escaped:
// the default exclude_regex entries contain "\." which TOML rejects as an
// invalid escape.
func tomlString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// SaveAirConfig writes ToTOML to filename, or to .air.toml when filename is
// empty.
func (ac *AirConfig) SaveAirConfig(filename string) error {
	if filename == "" {
		filename = ".air.toml"
	}

	content := ac.ToTOML()
	return WriteFile(filename, content)
}

// AirHelper sets up Air for the project in one directory. Its setters edit
// a HyperChiAirConfig and return the helper for chaining; InitAir writes it.
type AirHelper struct {
	projectRoot string
	config      *AirConfig
}

// NewAirHelper returns an AirHelper for projectRoot, or for the current
// directory when projectRoot is empty.
func NewAirHelper(projectRoot string) *AirHelper {
	if projectRoot == "" {
		projectRoot = "."
	}

	return &AirHelper{
		projectRoot: projectRoot,
		config:      HyperChiAirConfig(),
	}
}

// InitAir creates tmp/ and writes .air.toml in the project root. It returns
// an error without writing anything when .air.toml already exists.
func (ah *AirHelper) InitAir() error {
	configPath := filepath.Join(ah.projectRoot, ".air.toml")

	if FileExists(configPath) {
		return fmt.Errorf("air configuration already exists at %s", configPath)
	}

	tmpDir := filepath.Join(ah.projectRoot, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create tmp directory: %v", err)
	}

	if err := ah.config.SaveAirConfig(configPath); err != nil {
		return fmt.Errorf("failed to save Air configuration: %v", err)
	}

	return nil
}

// UpdateConfig calls modifier with the configuration so it can change any
// field.
func (ah *AirHelper) UpdateConfig(modifier func(*AirConfig)) *AirHelper {
	modifier(ah.config)
	return ah
}

// AddWatchExt adds file extensions, without dots, that trigger a rebuild.
func (ah *AirHelper) AddWatchExt(extensions ...string) *AirHelper {
	ah.config.Build.IncludeExt = append(ah.config.Build.IncludeExt, extensions...)
	ah.config.Watch.IncludeExt = append(ah.config.Watch.IncludeExt, extensions...)
	return ah
}

// AddExcludeDir adds directories Air does not watch.
func (ah *AirHelper) AddExcludeDir(directories ...string) *AirHelper {
	ah.config.Build.ExcludeDir = append(ah.config.Build.ExcludeDir, directories...)
	ah.config.Watch.ExcludeDir = append(ah.config.Watch.ExcludeDir, directories...)
	return ah
}

// SetBuildCmd sets the command Air runs to build the binary.
func (ah *AirHelper) SetBuildCmd(cmd string) *AirHelper {
	ah.config.Build.Cmd = cmd
	return ah
}

// SetBinPath sets the path of the binary Air runs after a build.
func (ah *AirHelper) SetBinPath(path string) *AirHelper {
	ah.config.Build.Bin = path
	return ah
}

// AddBuildArgs adds arguments Air passes to the binary when it runs it.
func (ah *AirHelper) AddBuildArgs(args ...string) *AirHelper {
	ah.config.Build.Args = append(ah.config.Build.Args, args...)
	return ah
}

// IsAirInstalled reports whether an air binary exists in /usr/local/bin,
// /usr/bin, $GOPATH/bin, or $HOME/go/bin. It does not search PATH.
func (ah *AirHelper) IsAirInstalled() bool {
	if _, err := os.Stat("/usr/local/bin/air"); err == nil {
		return true
	}
	if _, err := os.Stat("/usr/bin/air"); err == nil {
		return true
	}

	gopath := os.Getenv("GOPATH")
	if gopath != "" {
		airPath := filepath.Join(gopath, "bin", "air")
		if _, err := os.Stat(airPath); err == nil {
			return true
		}
	}

	home := os.Getenv("HOME")
	if home != "" {
		airPath := filepath.Join(home, "go", "bin", "air")
		if _, err := os.Stat(airPath); err == nil {
			return true
		}
	}

	return false
}

// airModule is Air's module path. The project moved from
// github.com/cosmtrek/air to the air-verse organization; checked against the
// upstream README on 2026-10-05 (go install needs Go 1.25 or newer).
const airModule = "github.com/air-verse/air"

// GetInstallInstructions returns text describing the ways to install Air.
func (ah *AirHelper) GetInstallInstructions() string {
	return `To install Air, run one of the following commands:

# Using go install (Go 1.25+)
go install ` + airModule + `@latest

# Using curl (Linux/macOS)
curl -sSfL https://raw.githubusercontent.com/air-verse/air/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Using homebrew (macOS)
brew install go-air

# Manual download
# Download from https://` + airModule + `/releases

After installation, you can start development with:
air
`
}

// CreateDevScript writes an executable dev.sh to the project root. The
// script checks that Air and .air.toml exist, exports the variables in .env
// if present, and runs air.
func (ah *AirHelper) CreateDevScript() error {
	scriptContent := `#!/bin/bash
# HyperChi development script with Air live reload

# Check if Air is installed
if ! command -v air &> /dev/null; then
    echo "Air is not installed. Please install it first:"
    echo ""
    echo "go install ` + airModule + `@latest"
    echo ""
    echo "Or visit: https://` + airModule + `"
    exit 1
fi

# Check if .air.toml exists
if [ ! -f .air.toml ]; then
    echo "Air configuration not found. Run 'hyperchi air init' first."
    exit 1
fi

# Load environment variables if .env exists
if [ -f .env ]; then
    echo "Loading environment variables from .env"
    export $(cat .env | grep -v '^#' | xargs)
fi

# Start Air with live reload
echo "Starting HyperChi development server with Air..."
echo "Visit http://localhost:8080 (or your configured port)"
echo "Press Ctrl+C to stop"
echo ""
air
`

	scriptPath := filepath.Join(ah.projectRoot, "dev.sh")
	if err := WriteFile(scriptPath, scriptContent); err != nil {
		return err
	}

	return os.Chmod(scriptPath, 0755)
}

// InitAirConfig runs AirHelper.InitAir in the current directory.
func InitAirConfig() error {
	helper := NewAirHelper(".")
	return helper.InitAir()
}

// CreateHyperChiAirConfig writes HyperChiAirConfig to filename, or to
// .air.toml when filename is empty. It overwrites an existing file.
func CreateHyperChiAirConfig(filename string) error {
	config := HyperChiAirConfig()
	return config.SaveAirConfig(filename)
}

// IsAirAvailable reports whether Air is installed (see
// AirHelper.IsAirInstalled).
func IsAirAvailable() bool {
	helper := NewAirHelper(".")
	return helper.IsAirInstalled()
}

// AirInstallHelp returns text describing the ways to install Air.
func AirInstallHelp() string {
	helper := NewAirHelper(".")
	return helper.GetInstallInstructions()
}

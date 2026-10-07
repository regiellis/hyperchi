// Package config holds a HyperChi app's configuration: Default for
// development, Production, FromEnv to read environment variables over the
// defaults, and Minimal for a site without a database. Run Validate on the
// result, then pass it to hyperchi.NewWithConfig.
//
// Security.SecretKey (SECRET_KEY) is the application secret that signs
// cookies and flash messages; Security.SecretKeyPrevious
// (SECRET_KEY_PREVIOUS) keeps retired keys verifying during a rotation. An app
// that signs nothing can leave both unset.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the whole configuration of a HyperChi app.
type Config struct {
	Server ServerConfig `json:"server"`

	Database DatabaseConfig `json:"database"`

	Assets AssetsConfig `json:"assets"`

	Cache CacheConfig `json:"cache"`

	Development DevelopmentConfig `json:"development"`

	Security SecurityConfig `json:"security"`
}

// ServerConfig sets the listen address, HTTP server limits, and environment.
// Environment is "development" or "production", and it decides development
// mode for the whole framework.
type ServerConfig struct {
	ReadHeaderTimeout time.Duration `json:"readHeaderTimeout"`
	MaxHeaderBytes    int           `json:"maxHeaderBytes"`
	ShutdownTimeout   time.Duration `json:"shutdownTimeout"`
	PortFallback      bool          `json:"portFallback"`
	Host              string        `json:"host"`
	Port              int           `json:"port"`
	ReadTimeout       time.Duration `json:"readTimeout"`
	WriteTimeout      time.Duration `json:"writeTimeout"`
	IdleTimeout       time.Duration `json:"idleTimeout"`
	Environment       string        `json:"environment"`
}

// DatabaseConfig holds database configuration. SQLite is the only supported
// driver: Type must be "sqlite" and Name is the database file path.
type DatabaseConfig struct {
	Disabled bool          `json:"disabled"`
	Timeout  time.Duration `json:"timeout"`
	Type     string        `json:"type"`
	Name     string        `json:"name"`
	MaxConns int           `json:"maxConns"`
	// LogQueries logs every statement with its arguments (DB_LOG_QUERIES).
	// Off by default in every environment: arguments can be documents or
	// personal data.
	LogQueries bool `json:"logQueries"`
}

// AssetsConfig sets the asset source and output directories and the build
// switches.
type AssetsConfig struct {
	SourceDir string `json:"sourceDir"`
	OutputDir string `json:"outputDir"`
	HotReload bool   `json:"hotReload"`
	Minify    bool   `json:"minify"`
}

// CacheConfig configures the app's in-process cache (app.Cache).
type CacheConfig struct {
	// Enabled off makes the cache store nothing, so every Get misses.
	Enabled bool `json:"enabled"`
	// TTL is the lifetime of an entry stored without its own; zero keeps
	// such entries until they are deleted or evicted.
	TTL time.Duration `json:"ttl"`
	// MaxEntries caps the entries held; storing one more evicts the least
	// recently used. Zero means no cap.
	MaxEntries int `json:"maxEntries"`
	// CleanupTime is how often a Set also removes every expired entry. Zero
	// removes them only when they are read or the cache is full.
	CleanupTime time.Duration `json:"cleanupTime"`
}

// DevelopmentConfig holds development switches. FromEnv turns them off in
// production.
type DevelopmentConfig struct {
	Debug        bool   `json:"debug"`
	HotReload    bool   `json:"hotReload"`
	LiveReload   bool   `json:"liveReload"`
	ProfilerPath string `json:"profilerPath"`
	LogLevel     string `json:"logLevel"`
}

// MinSecretKeyLength is the shortest secret key Validate accepts, in bytes.
// HMAC-SHA256 keys shorter than its 32-byte output weaken the signature.
const MinSecretKeyLength = 32

// SecurityConfig holds the security middleware settings. CSRF, RateLimit, and
// CORS choose what AutoMiddleware installs: CSRF protection (on by default),
// a per-client rate limit of 100 requests a minute (off by default), and the
// CORS headers for CORSOrigins (on by default). TrustedProxies are
// CIDR prefixes whose forwarding headers are believed when finding the client
// IP. With none, the TCP peer is the client. HSTS is the
// Strict-Transport-Security value sent outside development, or none if empty.
//
// SecretKey signs cookies and flash messages (SECRET_KEY). SecretKeyPrevious
// holds retired keys (SECRET_KEY_PREVIOUS, comma-separated): values they
// signed still verify, and new values are signed with SecretKey only. Neither
// is serialized, and String and GoString print them redacted.
type SecurityConfig struct {
	SecretKey         string   `json:"-"`
	SecretKeyPrevious []string `json:"-"`

	TrustedProxies []string `json:"trustedProxies"`
	HSTS           string   `json:"hsts"`
	CSRF           bool     `json:"csrf"`
	RateLimit      bool     `json:"rateLimit"`
	CORS           bool     `json:"cors"`
	CORSOrigins    string   `json:"corsOrigins"` // one origin or "*", sent verbatim as Access-Control-Allow-Origin
	CSPPolicy      string   `json:"cspPolicy"`   // Content-Security-Policy value, or the framework default if empty
}

// redactedSecurityConfig has SecurityConfig's fields and none of its
// methods, so formatting it does not call String again.
type redactedSecurityConfig SecurityConfig

func (s SecurityConfig) redacted() redactedSecurityConfig {
	r := redactedSecurityConfig(s)
	if r.SecretKey != "" {
		r.SecretKey = "[redacted]"
	}
	if r.SecretKeyPrevious != nil {
		r.SecretKeyPrevious = make([]string, len(s.SecretKeyPrevious))
		for i := range r.SecretKeyPrevious {
			r.SecretKeyPrevious[i] = "[redacted]"
		}
	}
	return r
}

// String formats the settings for %v and %+v with the secret keys replaced
// by "[redacted]", so logging a Config never prints a key.
func (s SecurityConfig) String() string {
	return fmt.Sprintf("%+v", s.redacted())
}

// GoString formats the settings for %#v with the secret keys redacted.
func (s SecurityConfig) GoString() string {
	return fmt.Sprintf("%#v", s.redacted())
}

// Default returns the development configuration: 0.0.0.0:8080, SQLite at
// hyperchi.db, hot reload and debug on, and HSTS max-age one year.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			ReadHeaderTimeout: 5 * time.Second,
			MaxHeaderBytes:    32 << 10,
			ShutdownTimeout:   10 * time.Second,
			Host:              "0.0.0.0",
			Port:              8080,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
			Environment:       "development",
		},
		Database: DatabaseConfig{
			Timeout:  5 * time.Second,
			Type:     "sqlite",
			Name:     "hyperchi.db",
			MaxConns: 10,
		},
		Assets: AssetsConfig{
			SourceDir: "assets",
			OutputDir: "static",
			HotReload: true,
			Minify:    false,
		},
		Cache: CacheConfig{
			Enabled:     true,
			TTL:         1 * time.Hour,
			MaxEntries:  10000,
			CleanupTime: 10 * time.Minute,
		},
		Development: DevelopmentConfig{
			Debug:        true,
			HotReload:    true,
			LiveReload:   true,
			ProfilerPath: "/debug",
			LogLevel:     "info",
		},
		Security: SecurityConfig{
			HSTS:      "max-age=31536000",
			CSRF:      true,
			RateLimit: false,
			CORS:      true,
		},
	}
}

// Production returns Default with the environment set to "production",
// minified assets, reloading and debug off, and log level "warn".
func Production() *Config {
	config := Default()
	config.Server.Environment = "production"
	applyProduction(config)
	return config
}

// ProductionFromEnv returns FromEnv with the environment forced to
// production, whatever ENVIRONMENT or GO_ENV say: the deployment's settings
// (SECRET_KEY, DB_PATH, PORT, ...) apply, and so do Production's switches.
func ProductionFromEnv() *Config {
	config := FromEnv()
	config.Server.Environment = "production"
	applyProduction(config)
	return config
}

// applyProduction turns off development features, keeping a LOG_LEVEL other
// than the default "info".
func applyProduction(config *Config) {
	config.Assets.HotReload = false
	config.Assets.Minify = true
	config.Development.Debug = false
	config.Development.HotReload = false
	config.Development.LiveReload = false
	if config.Development.LogLevel == "info" {
		config.Development.LogLevel = "warn"
	}
}

// FromEnv returns Default overridden by environment variables. The
// environment is ENVIRONMENT, else GO_ENV (see EnvironmentFromEnv and
// ParseEnvironment), else "production" when PRODUCTION=true, else
// "development". A
// production environment also applies Production's switches, keeping a
// LOG_LEVEL other than "info". An unparseable PORT is ignored. FromEnv does
// not validate. Call Validate on the result.
func FromEnv() *Config {
	config := Default()

	if host := os.Getenv("HOST"); host != "" {
		config.Server.Host = host
	}
	if port := os.Getenv("PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Server.Port = p
		}
	}
	if env := EnvironmentFromEnv(); env != "" {
		// An unknown name is kept as is, so Validate reports it.
		config.Server.Environment = env
		if canonical, err := ParseEnvironment(env); err == nil {
			config.Server.Environment = canonical
		}
	} else if os.Getenv("PRODUCTION") == "true" {
		config.Server.Environment = "production"
	}
	if os.Getenv("DB_DISABLED") == "true" {
		config.Database.Disabled = true
	}
	if proxies := os.Getenv("TRUSTED_PROXIES"); proxies != "" {
		config.Security.TrustedProxies = strings.Split(proxies, ",")
	}
	if hsts, ok := os.LookupEnv("HSTS"); ok {
		config.Security.HSTS = hsts
	}

	if logQueries := os.Getenv("DB_LOG_QUERIES"); logQueries != "" {
		config.Database.LogQueries = strings.EqualFold(logQueries, "true")
	}
	if dbType := os.Getenv("DB_TYPE"); dbType != "" {
		config.Database.Type = dbType
	}
	// DB_PATH is the name the scaffold, .env.example, and generated deployment
	// files set. DB_NAME is read only when DB_PATH is unset, so existing
	// deployments that set it keep working.
	if dbPath := os.Getenv("DB_PATH"); dbPath != "" {
		config.Database.Name = dbPath
	} else if dbName := os.Getenv("DB_NAME"); dbName != "" {
		config.Database.Name = dbName
	}

	if assetsDir := os.Getenv("ASSETS_DIR"); assetsDir != "" {
		config.Assets.SourceDir = assetsDir
	}
	if staticDir := os.Getenv("STATIC_DIR"); staticDir != "" {
		config.Assets.OutputDir = staticDir
	}
	if hotReload := os.Getenv("HOT_RELOAD"); hotReload != "" {
		config.Assets.HotReload = strings.ToLower(hotReload) == "true"
	}

	if debug := os.Getenv("DEBUG"); debug != "" {
		config.Development.Debug = strings.ToLower(debug) == "true"
	}
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		config.Development.LogLevel = logLevel
	}

	if corsOrigins := os.Getenv("CORS_ORIGINS"); corsOrigins != "" {
		config.Security.CORSOrigins = corsOrigins
	}
	if cspPolicy := os.Getenv("CSP_POLICY"); cspPolicy != "" {
		config.Security.CSPPolicy = cspPolicy
	}
	config.Security.SecretKey = os.Getenv("SECRET_KEY")
	for _, key := range strings.Split(os.Getenv("SECRET_KEY_PREVIOUS"), ",") {
		if key = strings.TrimSpace(key); key != "" {
			config.Security.SecretKeyPrevious = append(config.Security.SecretKeyPrevious, key)
		}
	}

	if config.Server.Environment == "production" {
		applyProduction(config)
	}

	return config
}

// Address returns Host and Port joined as host:port, bracketing an IPv6 host.
func (sc ServerConfig) Address() string {
	return net.JoinHostPort(sc.Host, strconv.Itoa(sc.Port))
}

// DSN returns the SQLite database path. SQLite is the only driver the
// framework ships, and Validate rejects any other Type.
func (dc DatabaseConfig) DSN() string {
	return dc.Name
}

// IsDevelopment reports whether Environment is exactly "development".
func (c *Config) IsDevelopment() bool {
	return c.Server.Environment == "development"
}

// IsProduction reports whether Environment is exactly "production".
func (c *Config) IsProduction() bool {
	return c.Server.Environment == "production"
}

// EnvironmentFromEnv returns the environment name the process sets:
// ENVIRONMENT, else GO_ENV, else "". FromEnv reads it this way.
func EnvironmentFromEnv() string {
	if env := os.Getenv("ENVIRONMENT"); env != "" {
		return env
	}
	return os.Getenv("GO_ENV")
}

// ParseEnvironment returns the canonical name for an environment variable
// value: "development" for "development" or "dev", "production" for
// "production". Any other value is an error. FromEnv, Config.Validate, and
// HyperChi.ValidateConfig all apply this rule.
func ParseEnvironment(env string) (string, error) {
	switch env {
	case "development", "dev":
		return "development", nil
	case "production":
		return "production", nil
	}
	return "", fmt.Errorf("invalid environment %q: want \"development\" (or \"dev\") or \"production\"", env)
}

// Validate returns the first problem found: an environment other than
// "development" or "production", a negative server limit, database timeout,
// or cache setting, a database type other than "sqlite" while the database is
// enabled, a trusted proxy that is not a CIDR prefix, a secret key shorter
// than MinSecretKeyLength, a port outside 1 to 65535, or an empty assets
// directory. A nil Config is an error. An unset secret key is valid: apps that
// sign nothing need none.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("configuration is nil")
	}
	// The canonical name only: IsDevelopment and IsProduction compare it
	// exactly, so an alias such as "dev" would be neither.
	if canonical, err := ParseEnvironment(c.Server.Environment); err != nil {
		return err
	} else if canonical != c.Server.Environment {
		return fmt.Errorf("invalid environment %q: use %q", c.Server.Environment, canonical)
	}
	if c.Server.ReadHeaderTimeout < 0 || c.Server.ReadTimeout < 0 || c.Server.WriteTimeout < 0 || c.Server.IdleTimeout < 0 || c.Server.ShutdownTimeout < 0 || c.Server.MaxHeaderBytes < 0 {
		return fmt.Errorf("server limits cannot be negative")
	}
	if c.Database.Timeout < 0 {
		return fmt.Errorf("database timeout cannot be negative")
	}
	if c.Cache.TTL < 0 || c.Cache.MaxEntries < 0 || c.Cache.CleanupTime < 0 {
		return fmt.Errorf("cache settings cannot be negative")
	}
	if !c.Database.Disabled && c.Database.Type != "sqlite" {
		return fmt.Errorf("unsupported database type %q: core supports sqlite", c.Database.Type)
	}
	for _, proxy := range c.Security.TrustedProxies {
		if _, err := netip.ParsePrefix(strings.TrimSpace(proxy)); err != nil {
			return fmt.Errorf("trusted proxy: %w", err)
		}
	}

	if c.Security.SecretKey != "" && len(c.Security.SecretKey) < MinSecretKeyLength {
		return fmt.Errorf("secret key (SECRET_KEY) is %d bytes: it must be at least %d (generate one with: openssl rand -base64 32)",
			len(c.Security.SecretKey), MinSecretKeyLength)
	}
	for i, key := range c.Security.SecretKeyPrevious {
		if len(key) < MinSecretKeyLength {
			return fmt.Errorf("previous secret key %d (SECRET_KEY_PREVIOUS) is %d bytes: it must be at least %d", i+1, len(key), MinSecretKeyLength)
		}
	}
	if c.Security.SecretKey == "" && len(c.Security.SecretKeyPrevious) > 0 {
		return fmt.Errorf("previous secret keys (SECRET_KEY_PREVIOUS) are set without a current key (SECRET_KEY)")
	}

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if !c.Database.Disabled && c.Database.Type == "" {
		return fmt.Errorf("database type is required")
	}

	if c.Assets.SourceDir == "" {
		return fmt.Errorf("assets source directory is required")
	}

	if c.Assets.OutputDir == "" {
		return fmt.Errorf("assets output directory is required")
	}

	return nil
}

// Minimal disables optional services for a templates-and-assets site.
// Environment and security settings are still loaded through FromEnv.
func Minimal() *Config {
	cfg := FromEnv()
	cfg.Database.Disabled = true
	cfg.Cache.Enabled = false
	cfg.Assets.HotReload = false
	cfg.Development.HotReload = false
	cfg.Development.LiveReload = false
	if cfg.Security.CSPPolicy == "" {
		cfg.Security.CSPPolicy = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'"
	}
	return cfg
}

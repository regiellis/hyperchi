package helpers

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DeploymentHelper writes deployment files (Dockerfile, compose file,
// scripts, server configs) for one project directory and app name.
type DeploymentHelper struct {
	projectRoot string
	appName     string
	env         *Env
}

// DeploymentConfig describes how an app is deployed. The Create methods do
// not read it; DefaultDeploymentConfig returns the values they assume.
type DeploymentConfig struct {
	AppName         string            `json:"app_name"`
	Port            string            `json:"port"`
	Environment     string            `json:"environment"`
	BuildTarget     string            `json:"build_target"`
	DockerRegistry  string            `json:"docker_registry"`
	HealthCheckPath string            `json:"health_check_path"`
	EnvVars         map[string]string `json:"env_vars"`
	BuildFlags      []string          `json:"build_flags"`
	Assets          []string          `json:"assets"`
}

// ProductionConfig is a set of recommended production settings returned by
// GetProductionConfig. Nothing in HyperChi applies it.
type ProductionConfig struct {
	DisableProfiler     bool              `json:"disable_profiler"`
	LogLevel            string            `json:"log_level"`
	DatabaseSettings    map[string]string `json:"database_settings"`
	SecuritySettings    map[string]string `json:"security_settings"`
	PerformanceSettings map[string]string `json:"performance_settings"`
}

// NewDeploymentHelper returns a DeploymentHelper for projectRoot (default
// ".") and appName (default "hyperchi-app").
func NewDeploymentHelper(projectRoot, appName string) *DeploymentHelper {
	if projectRoot == "" {
		projectRoot = "."
	}
	if appName == "" {
		appName = "hyperchi-app"
	}

	return &DeploymentHelper{
		projectRoot: projectRoot,
		appName:     appName,
		env:         NewEnv(),
	}
}

// DefaultDeploymentConfig returns a production config for linux/amd64 on
// port 8080 with a /health check.
func (dh *DeploymentHelper) DefaultDeploymentConfig() *DeploymentConfig {
	return &DeploymentConfig{
		AppName:         dh.appName,
		Port:            "8080",
		Environment:     "production",
		BuildTarget:     "linux/amd64",
		DockerRegistry:  "",
		HealthCheckPath: "/health",
		EnvVars:         make(map[string]string),
		BuildFlags:      []string{"-ldflags", "-s -w"},
		Assets:          []string{"templates", "static", "public"},
	}
}

// frameworkGoVersion is the major.minor Go release the framework's go.mod
// requires. CreateDockerfile uses it when the project's own go.mod asks for
// less or cannot be read. TestFrameworkGoVersionTracksGoMod keeps it in step
// with the root go.mod.
const frameworkGoVersion = "1.26"

// dockerGoVersion returns the major.minor Go version for the build image: the
// project go.mod's go directive, raised to frameworkGoVersion when lower,
// because a module cannot build with a toolchain older than a dependency's
// go line.
func (dh *DeploymentHelper) dockerGoVersion() string {
	content, err := os.ReadFile(filepath.Join(dh.projectRoot, "go.mod"))
	if err != nil {
		return frameworkGoVersion
	}
	version := goDirectiveMinor(string(content))
	if version == "" || !goVersionAtLeast(version, frameworkGoVersion) {
		return frameworkGoVersion
	}
	return version
}

// goDirectiveMinor returns the major.minor part of a go.mod go directive
// ("go 1.26.8" gives "1.26"), or "" when there is none.
func goDirectiveMinor(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "go" {
			continue
		}
		parts := strings.SplitN(fields[1], ".", 3)
		if len(parts) < 2 {
			return ""
		}
		major, errMajor := strconv.Atoi(parts[0])
		minor, errMinor := strconv.Atoi(parts[1])
		if errMajor != nil || errMinor != nil {
			return ""
		}
		return fmt.Sprintf("%d.%d", major, minor)
	}
	return ""
}

// goVersionAtLeast compares two major.minor versions produced by
// goDirectiveMinor.
func goVersionAtLeast(version, floor string) bool {
	var vMajor, vMinor, fMajor, fMinor int
	if _, err := fmt.Sscanf(version, "%d.%d", &vMajor, &vMinor); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(floor, "%d.%d", &fMajor, &fMinor); err != nil {
		return true
	}
	return vMajor > fMajor || (vMajor == fMajor && vMinor >= fMinor)
}

// CreateDockerfile writes a multi-stage Dockerfile, and a .dockerignore when
// the project has none. The build stage uses the Go version from the
// project's go.mod (see dockerGoVersion) and produces a static binary with
// CGO_ENABLED=0; HyperChi's SQLite driver is pure Go, so no C toolchain is
// needed. The image runs as a non-root user on port 8080 and copies only
// the asset directories that exist.
func (dh *DeploymentHelper) CreateDockerfile() error {
	dockerfile := `# Multi-stage Dockerfile for HyperChi Application
# Build stage
FROM golang:` + dh.dockerGoVersion() + `-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build a static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" \
    -o main .

# Production stage
FROM alpine:3.24

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN adduser -D -s /bin/sh hyperchi

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/main .

` + dh.assetCopyLines() + `
# Configuration comes from the environment at run time
# (docker run --env-file .env.production, or the compose file), never from a
# file baked into the image.

# Set ownership
RUN chown -R hyperchi:hyperchi /app

# Switch to non-root user
USER hyperchi

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
CMD ["./main"]
`

	dockerfilePath := filepath.Join(dh.projectRoot, "Dockerfile")
	if err := WriteFile(dockerfilePath, dockerfile); err != nil {
		return err
	}
	return dh.createDockerignore()
}

// assetDirs are the directories a HyperChi app reads at run time; the
// Dockerfile copies the ones the project has, since COPY of a missing
// directory fails the build.
var assetDirs = []string{"templates", "static", "public", "dist"}

// assetCopyLines returns a COPY line for each asset directory present in the
// project root.
func (dh *DeploymentHelper) assetCopyLines() string {
	var b strings.Builder
	b.WriteString("# Copy the asset directories this project has\n")
	for _, dir := range assetDirs {
		if IsDir(filepath.Join(dh.projectRoot, dir)) {
			b.WriteString("COPY --from=builder /app/" + dir + " ./" + dir + "/\n")
		}
	}
	return b.String()
}

// dockerignoreContent keeps secrets, history, and local data out of the build
// context, so COPY . . cannot place them in an image layer.
const dockerignoreContent = `.env
.env.*
.git
*.db
*.db-shm
*.db-wal
data/
logs/
node_modules/
tmp/
`

// createDockerignore writes .dockerignore unless the project already has one.
func (dh *DeploymentHelper) createDockerignore() error {
	path := filepath.Join(dh.projectRoot, ".dockerignore")
	if FileExists(path) {
		return nil
	}
	return WriteFile(path, dockerignoreContent)
}

// CreateDockerCompose writes docker-compose.yml with the app service on port
// 8080, a data volume, and an nginx service in front of it.
func (dh *DeploymentHelper) CreateDockerCompose() error {
	compose := fmt.Sprintf(`version: '3.8'

services:
  %s:
    build: .
    ports:
      - "8080:8080"
    environment:
      - ENVIRONMENT=production
      - PORT=8080
      - DB_PATH=/app/data/app.db
    volumes:
      - app_data:/app/data
      - ./logs:/app/logs
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
    deploy:
      resources:
        limits:
          memory: 512M
        reservations:
          memory: 256M

  # Optional: Add a reverse proxy
  nginx:
    image: nginx:alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./ssl:/etc/nginx/ssl:ro
    depends_on:
      - %s
    restart: unless-stopped

volumes:
  app_data:
    driver: local

networks:
  default:
    driver: bridge
`, dh.appName, dh.appName)

	composePath := filepath.Join(dh.projectRoot, "docker-compose.yml")
	return WriteFile(composePath, compose)
}

// CreateProductionEnv writes a .env.production template with mode 0600. Fill
// in its secrets before deploying.
func (dh *DeploymentHelper) CreateProductionEnv() error {
	prodEnv := `# Production Environment Configuration
# DO NOT commit this file to version control with real values!

# Application
APP_NAME=` + dh.appName + `
ENVIRONMENT=production
PORT=8080
APP_DEBUG=false

# Database
DB_PATH=/app/data/app.db
DB_DEBUG=false

# Security
JWT_SECRET=CHANGE_THIS_IN_PRODUCTION
SESSION_SECRET=CHANGE_THIS_IN_PRODUCTION
CORS_ORIGINS=https://yourdomain.com

# Logging
LOG_LEVEL=INFO
LOG_COLORED=false
LOG_SHOW_CALLER=false

# Profiler (disabled in production)
ENABLE_PROFILER=false
ENABLE_PROFILER_ENDPOINT=false

# Performance
CACHE_TTL=3600
MAX_CONNECTIONS=100
READ_TIMEOUT=30s
WRITE_TIMEOUT=30s
IDLE_TIMEOUT=120s

# Monitoring
HEALTH_CHECK_ENABLED=true
METRICS_ENABLED=true

# External Services
# DATABASE_URL=postgres://user:pass@host:5432/db
# REDIS_URL=redis://host:6379
# SMTP_URL=smtp://user:pass@host:587

# Rate Limiting
RATE_LIMIT_ENABLED=true
RATE_LIMIT_REQUESTS=100
RATE_LIMIT_WINDOW=1m

# SSL/TLS
TLS_ENABLED=false
TLS_CERT_FILE=/app/ssl/cert.pem
TLS_KEY_FILE=/app/ssl/key.pem

# File Upload
MAX_UPLOAD_SIZE=10MB
UPLOAD_DIR=/app/uploads

# Background Jobs
ENABLE_SCHEDULER=true
CLEANUP_INTERVAL=24h
`

	envPath := filepath.Join(dh.projectRoot, ".env.production")
	return writeSecretFile(envPath, prodEnv)
}

// CreateBuildScript writes an executable build.sh that cross-compiles
// release archives into dist/ and builds a Docker image.
func (dh *DeploymentHelper) CreateBuildScript() error {
	buildScript := `#!/bin/bash
# HyperChi Production Build Script

set -e

APP_NAME="` + dh.appName + `"
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "v1.0.0")}
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_VERSION=$(go version | awk '{print $3}')

echo "Building $APP_NAME $VERSION..."

# Clean previous builds
echo "Cleaning previous builds..."
rm -rf dist/
mkdir -p dist/

# Build for multiple platforms
platforms=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64" "windows/amd64")

for platform in "${platforms[@]}"; do
    IFS='/' read -r GOOS GOARCH <<< "$platform"
    output_name="$APP_NAME-$VERSION-$GOOS-$GOARCH"
    
    if [ "$GOOS" = "windows" ]; then
        output_name="$output_name.exe"
    fi
    
    echo "Building for $GOOS/$GOARCH..."
    
    CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags="-s -w -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME -X main.GoVersion=$GO_VERSION" \
        -o "dist/$output_name" \
        .
    
    # Create platform-specific archive
    if [ "$GOOS" = "windows" ]; then
        (cd dist && zip "$APP_NAME-$VERSION-$GOOS-$GOARCH.zip" "$output_name" ../README.md ../LICENSE 2>/dev/null || true)
    else
        (cd dist && tar -czf "$APP_NAME-$VERSION-$GOOS-$GOARCH.tar.gz" "$output_name" ../README.md ../LICENSE 2>/dev/null || true)
    fi
done

# Build Docker image
echo "Building Docker image..."
docker build -t "$APP_NAME:$VERSION" -t "$APP_NAME:latest" .

echo "Build complete!"
echo "Binaries available in dist/"
echo "Docker image: $APP_NAME:$VERSION"
`

	scriptPath := filepath.Join(dh.projectRoot, "build.sh")
	if err := WriteFile(scriptPath, buildScript); err != nil {
		return err
	}

	return os.Chmod(scriptPath, 0755)
}

// CreateDeployScript writes an executable deploy.sh.
func (dh *DeploymentHelper) CreateDeployScript() error {
	deployScript := `#!/bin/bash
# HyperChi Deployment Script

set -e

APP_NAME="` + dh.appName + `"
VERSION=${VERSION:-latest}
ENVIRONMENT=${ENVIRONMENT:-production}

echo "Deploying $APP_NAME:$VERSION to $ENVIRONMENT..."

# Load environment-specific configuration
if [ -f ".env.$ENVIRONMENT" ]; then
    echo "Loading environment from .env.$ENVIRONMENT"
    export $(cat .env.$ENVIRONMENT | grep -v '^#' | xargs)
fi

# Deployment strategy
STRATEGY=${DEPLOY_STRATEGY:-docker-compose}

case $STRATEGY in
    "docker-compose")
        echo "Deploying with Docker Compose..."
        
        # Pull latest images
        docker-compose pull
        
        # Stop existing containers
        docker-compose down
        
        # Start new containers
        docker-compose up -d
        
        # Wait for health check
        echo "Waiting for application to be healthy..."
        timeout 60s bash -c 'until docker-compose exec '$APP_NAME' wget --spider -q http://localhost:8080/health; do sleep 2; done'
        
        echo "Deployment successful!"
        ;;
        
    "kubernetes")
        echo "Deploying to Kubernetes..."
        kubectl apply -f k8s/
        kubectl rollout status deployment/$APP_NAME
        ;;
        
    "systemd")
        echo "Deploying with systemd..."
        sudo systemctl stop $APP_NAME || true
        sudo cp dist/$APP_NAME-*-linux-amd64 /usr/local/bin/$APP_NAME
        sudo chmod +x /usr/local/bin/$APP_NAME
        sudo systemctl start $APP_NAME
        sudo systemctl enable $APP_NAME
        ;;
        
    *)
        echo "Unknown deployment strategy: $STRATEGY"
        exit 1
        ;;
esac

echo "Deployment complete!"
`

	scriptPath := filepath.Join(dh.projectRoot, "deploy.sh")
	if err := WriteFile(scriptPath, deployScript); err != nil {
		return err
	}

	return os.Chmod(scriptPath, 0755)
}

// CreateNginxConfig writes nginx.conf, a reverse proxy in front of the app
// on port 8080.
func (dh *DeploymentHelper) CreateNginxConfig() error {
	nginxConf := `events {
    worker_connections 1024;
}

http {
    upstream ` + dh.appName + ` {
        server ` + dh.appName + `:8080;
    }

    # Rate limiting
    limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
    limit_req_zone $binary_remote_addr zone=login:10m rate=1r/s;

    server {
        listen 80;
        server_name localhost;

        # Security headers
        add_header X-Frame-Options DENY;
        add_header X-Content-Type-Options nosniff;
        add_header X-XSS-Protection "1; mode=block";
        add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

        # Gzip compression
        gzip on;
        gzip_types text/plain text/css application/json application/javascript text/xml application/xml text/javascript;

        # Static file serving
        location /static/ {
            alias /app/static/;
            expires 1y;
            add_header Cache-Control "public, immutable";
        }

        # API routes with rate limiting
        location /api/ {
            limit_req zone=api burst=20 nodelay;
            proxy_pass http://` + dh.appName + `;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
        }

        # Login endpoint with stricter rate limiting
        location /login {
            limit_req zone=login burst=5 nodelay;
            proxy_pass http://` + dh.appName + `;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
        }

        # Health check
        location /health {
            proxy_pass http://` + dh.appName + `;
            access_log off;
        }

        # All other requests
        location / {
            proxy_pass http://` + dh.appName + `;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            
            # WebSocket support
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";
        }
    }

    # HTTPS server (uncomment and configure for SSL)
    # server {
    #     listen 443 ssl http2;
    #     server_name yourdomain.com;
    #     
    #     ssl_certificate /etc/nginx/ssl/cert.pem;
    #     ssl_certificate_key /etc/nginx/ssl/key.pem;
    #     
    #     # SSL configuration
    #     ssl_protocols TLSv1.2 TLSv1.3;
    #     ssl_ciphers ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384;
    #     ssl_prefer_server_ciphers off;
    #     
    #     # Same location blocks as above
    # }
}
`

	nginxPath := filepath.Join(dh.projectRoot, "nginx.conf")
	return WriteFile(nginxPath, nginxConf)
}

// CreateSystemdService writes <appName>.service, a systemd unit that runs
// the app.
func (dh *DeploymentHelper) CreateSystemdService() error {
	service := `[Unit]
Description=` + dh.appName + ` HyperChi Application
After=network.target
Wants=network.target

[Service]
Type=simple
User=hyperchi
Group=hyperchi
WorkingDirectory=/opt/` + dh.appName + `
ExecStart=/usr/local/bin/` + dh.appName + `
ExecReload=/bin/kill -HUP $MAINPID
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=30
Restart=always
RestartSec=5

# Security
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/` + dh.appName + `/data
ReadWritePaths=/opt/` + dh.appName + `/logs

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096

# Environment
Environment=ENVIRONMENT=production
EnvironmentFile=-/opt/` + dh.appName + `/.env

[Install]
WantedBy=multi-user.target
`

	servicePath := filepath.Join(dh.projectRoot, dh.appName+".service")
	return WriteFile(servicePath, service)
}

// healthCheckSource is the health.go CreateHealthCheck writes. It is a
// self-contained part of package main using only the standard library: wire
// the handlers into the app, for example
//
//	app.Handle(http.MethodGet, "/health", healthCheckHandler(db, "production"))
//	app.Handle(http.MethodGet, "/ready", readinessHandler(db))
//	app.Handle(http.MethodGet, "/live", http.HandlerFunc(livenessHandler))
//
// where db is the app's *sql.DB, or an untyped nil when there is none.
const healthCheckSource = `package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"runtime"
	"time"
)

// healthPinger is the database check the health handlers run. *sql.DB
// satisfies it. Pass an untyped nil when the app has no database.
type healthPinger interface {
	PingContext(ctx context.Context) error
}

// healthVersion is reported by the health check. Set it at build time with
// -ldflags "-X main.healthVersion=v1.2.3".
var healthVersion = "dev"

var healthStartTime = time.Now()

// HealthStatus is the health check response.
type HealthStatus struct {
	Status      string            ` + "`json:\"status\"`" + `
	Timestamp   time.Time         ` + "`json:\"timestamp\"`" + `
	Version     string            ` + "`json:\"version\"`" + `
	Uptime      string            ` + "`json:\"uptime\"`" + `
	Environment string            ` + "`json:\"environment\"`" + `
	Memory      HealthMemoryStats ` + "`json:\"memory\"`" + `
	Checks      map[string]string ` + "`json:\"checks\"`" + `
}

// HealthMemoryStats reports process memory in the health check response.
type HealthMemoryStats struct {
	AllocMB    float64 ` + "`json:\"alloc_mb\"`" + `
	SysMB      float64 ` + "`json:\"sys_mb\"`" + `
	NumGC      uint32  ` + "`json:\"num_gc\"`" + `
	Goroutines int     ` + "`json:\"goroutines\"`" + `
}

// healthCheckHandler reports overall health as JSON: 200 when every check
// passes, 503 when the database does not answer.
func healthCheckHandler(db healthPinger, environment string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]string{"database": "not configured"}
		status, code := "healthy", http.StatusOK
		if db != nil {
			if err := db.PingContext(r.Context()); err != nil {
				// The error stays in the log: health endpoints are public.
				log.Printf("health: database ping failed: %v", err)
				checks["database"] = "unhealthy"
				status, code = "degraded", http.StatusServiceUnavailable
			} else {
				checks["database"] = "healthy"
			}
		}

		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		body := HealthStatus{
			Status:      status,
			Timestamp:   time.Now().UTC(),
			Version:     healthVersion,
			Uptime:      time.Since(healthStartTime).Round(time.Second).String(),
			Environment: environment,
			Memory: HealthMemoryStats{
				AllocMB:    float64(m.Alloc) / 1024 / 1024,
				SysMB:      float64(m.Sys) / 1024 / 1024,
				NumGC:      m.NumGC,
				Goroutines: runtime.NumGoroutine(),
			},
			Checks: checks,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			log.Printf("health: write response: %v", err)
		}
	})
}

// readinessHandler answers 200 once the database responds, 503 before.
func readinessHandler(db healthPinger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if db != nil {
			if err := db.PingContext(r.Context()); err != nil {
				log.Printf("ready: database ping failed: %v", err)
				writeHealthText(w, http.StatusServiceUnavailable, "Database not ready")
				return
			}
		}
		writeHealthText(w, http.StatusOK, "Ready")
	})
}

// livenessHandler answers 200 while the process can serve requests.
func livenessHandler(w http.ResponseWriter, r *http.Request) {
	writeHealthText(w, http.StatusOK, "Alive")
}

func writeHealthText(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	if _, err := w.Write([]byte(text)); err != nil {
		log.Printf("health: write response: %v", err)
	}
}
`

// CreateHealthCheck writes health.go, standard-library health, readiness, and
// liveness handlers for package main. See healthCheckSource for wiring.
func (dh *DeploymentHelper) CreateHealthCheck() error {
	return WriteFile(filepath.Join(dh.projectRoot, "health.go"), healthCheckSource)
}

// SwitchToProduction loads .env.production from the project root and
// returns an error when APP_NAME, ENVIRONMENT, JWT_SECRET, or SESSION_SECRET
// is missing. It does not change the process environment.
func (dh *DeploymentHelper) SwitchToProduction() error {
	// Load the file CreateProductionEnv writes, under the project root.
	if err := dh.env.LoadFile(filepath.Join(dh.projectRoot, ".env.production")); err != nil {
		return fmt.Errorf("failed to load production environment: %v", err)
	}

	required := []string{
		"APP_NAME", "ENVIRONMENT", "JWT_SECRET", "SESSION_SECRET",
	}

	return dh.env.Validate(required)
}

// GetProductionConfig returns recommended production settings as a
// ProductionConfig.
func (dh *DeploymentHelper) GetProductionConfig() *ProductionConfig {
	return &ProductionConfig{
		DisableProfiler: true,
		LogLevel:        "INFO",
		DatabaseSettings: map[string]string{
			"debug":           "false",
			"connection_pool": "true",
			"max_connections": "25",
		},
		SecuritySettings: map[string]string{
			"cors_enabled":   "true",
			"csrf_enabled":   "true",
			"secure_cookies": "true",
			"rate_limit":     "true",
		},
		PerformanceSettings: map[string]string{
			"cache_enabled":   "true",
			"gzip_enabled":    "true",
			"static_expires":  "1y",
			"max_upload_size": "10MB",
		},
	}
}

// GetBuildInfo returns the Go version, OS, architecture, current UTC time,
// app name, and a Unix-time deployment ID as strings.
func (dh *DeploymentHelper) GetBuildInfo() map[string]string {
	return map[string]string{
		"go_version":    runtime.Version(),
		"go_os":         runtime.GOOS,
		"go_arch":       runtime.GOARCH,
		"build_time":    time.Now().UTC().Format(time.RFC3339),
		"app_name":      dh.appName,
		"deployment_id": fmt.Sprintf("%d", time.Now().Unix()),
	}
}

// CreateDeploymentFiles writes the Dockerfile, docker-compose.yml, systemd
// unit, deploy.sh, and nginx.conf. The package-level CreateDeploymentFiles
// also writes the production .env, build.sh, and health.go.
func (dh *DeploymentHelper) CreateDeploymentFiles() error {
	var err error

	if err = dh.CreateDockerfile(); err != nil {
		return fmt.Errorf("failed to create Dockerfile: %w", err)
	}

	if err = dh.CreateDockerCompose(); err != nil {
		return fmt.Errorf("failed to create docker-compose.yml: %w", err)
	}

	if err = dh.CreateSystemdService(); err != nil {
		return fmt.Errorf("failed to create systemd service: %w", err)
	}

	if err = dh.CreateDeployScript(); err != nil {
		return fmt.Errorf("failed to create deploy script: %w", err)
	}

	if err = dh.CreateNginxConfig(); err != nil {
		return fmt.Errorf("failed to create nginx config: %w", err)
	}

	return nil
}

// CreateDeploymentFiles writes every deployment file for appName into
// projectRoot: Dockerfile, compose file, production .env, build and deploy
// scripts, nginx and systemd configs, and a health check.
func CreateDeploymentFiles(projectRoot, appName string) error {
	dh := NewDeploymentHelper(projectRoot, appName)

	files := []func() error{
		dh.CreateDockerfile,
		dh.CreateDockerCompose,
		dh.CreateProductionEnv,
		dh.CreateBuildScript,
		dh.CreateDeployScript,
		dh.CreateNginxConfig,
		dh.CreateSystemdService,
		dh.CreateHealthCheck,
	}

	for _, createFunc := range files {
		if err := createFunc(); err != nil {
			return err
		}
	}

	return nil
}

// SwitchEnvironment copies .env.<env> over .env in the working directory.
// The copy is written with mode 0600, like Env.Save, because it holds secrets.
func SwitchEnvironment(env string) error {
	envFile := fmt.Sprintf(".env.%s", env)
	if !FileExists(envFile) {
		return fmt.Errorf("environment file %s does not exist", envFile)
	}

	content, err := ReadFile(envFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", envFile, err)
	}
	return writeSecretFile(".env", content)
}

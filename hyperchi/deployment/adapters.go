package deployment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeAdapter(stage string, cfg Config, manifest *Manifest) error {
	if cfg.Target == "docker" {
		manifest.WorkingDirectory = "/app"
		var file strings.Builder
		fmt.Fprintf(&file, "# Built artifact: linux/%s. Build with this directory as context.\n", cfg.Arch)
		fmt.Fprintf(&file, "FROM --platform=linux/%s %s\nWORKDIR /app\n", cfg.Arch, cfg.RuntimeImage)
		for _, asset := range append([]string{"app"}, cfg.Assets...) {
			args, _ := json.Marshal([]string{asset, "/app/" + asset})
			fmt.Fprintf(&file, "COPY --chown=65532:65532 %s\n", args)
		}
		fmt.Fprintf(&file, "USER 65532:65532\nENV HOST=0.0.0.0 PORT=%d ENVIRONMENT=production DB_DISABLED=true\n", cfg.Port)
		fmt.Fprintf(&file, "EXPOSE %d\nSTOPSIGNAL SIGTERM\nENTRYPOINT [\"/app/app\"]\n", cfg.Port)
		return os.WriteFile(filepath.Join(stage, "Dockerfile"), []byte(file.String()), 0644)
	}
	manifest.WorkingDirectory = "/opt/" + cfg.Name
	manifest.Environment["DB_PATH"] = "/var/lib/" + cfg.Name + "/app.db"
	service := fmt.Sprintf(`[Unit]
Description=HyperChi %s
After=network.target

[Service]
Type=simple
DynamicUser=yes
WorkingDirectory=/opt/%s
ExecStart=/opt/%s/app
Environment=HOST=0.0.0.0 PORT=%d ENVIRONMENT=production DB_DISABLED=true
Environment=DB_PATH=/var/lib/%s/app.db
EnvironmentFile=-/etc/%s.env
StateDirectory=%s
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes

[Install]
WantedBy=multi-user.target
`, cfg.Name, cfg.Name, cfg.Name, cfg.Port, cfg.Name, cfg.Name, cfg.Name)
	return os.WriteFile(filepath.Join(stage, cfg.Name+".service"), []byte(service), 0644)
}

---
title: Deployment adapters
section: Production
order: 151
next: testing
description: Build a Linux binary and selected assets into a versioned artifact with a Docker or systemd adapter, ready for review and deployment.
---

## One artifact, two runtimes {#overview}

`hyperchi deploy build` produces a self-contained artifact directory for Docker or systemd. It compiles one local Go main package, copies explicitly selected runtime assets, writes a manifest, and generates the selected runtime configuration. It does not publish an image, contact a hosting account, or install or restart a service.

The new adapters are separate from the older `deploy init`, `deploy docker`, and `deploy production` helpers. Those commands remain available. Use `deploy build` when you want a binary and runtime files together in a fresh, reviewable output directory.

## Build an artifact {#example}

From the application module directory:

```sh
# Run your frontend build first, if the application needs it.
npm run build

hyperchi deploy build \
  --name my-site \
  --target docker \
  --arch amd64 \
  --assets templates,dist,public \
  --output .hyperchi/releases/v1
```

Only list assets your application actually reads. For an app with fully embedded templates and assets, omit `--assets`. Content directories, generated images, and other disk resources must be selected explicitly when they are not embedded. The command does not run npm or infer files from source code.

`--package ./cmd/server` selects another local main package. `--project /path/to/module` selects the source directory; `--output` is always relative to your current working directory. The package defaults to `.`, the target to `docker`, and the port to `8080`. Supported architectures are `amd64` and `arm64`; the host architecture is used when omitted. Builds always use `GOOS=linux`, `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, and stripped symbols. Inherited `GOFLAGS` and architecture-tuning variables are cleared so they cannot silently change those defaults. Normal Go module/workspace resolution and toolchain selection still apply.

An existing output path is refused. Builds use a temporary sibling directory and publish it only after all steps succeed. A failure or cancellation removes the temporary output. Use a new release directory for each build. Source files should remain unchanged while the command runs.

### Artifact contract

```text
.hyperchi/releases/v1/
  app                 Linux executable, mode 0755
  templates/          Selected runtime assets at their original relative paths
  dist/
  public/
  manifest.json       Version 1 artifact description
  Dockerfile          Docker adapter (or my-site.service for systemd)
```

The manifest records the artifact version, application name, adapter, OS/architecture, binary path, main package, selected assets, working directory, non-secret environment defaults, and a SHA-256 inventory of the binary and copied files. Adapter files are excluded from that inventory so operators can customize them. Checksums detect changes; they are not a signature or an authenticity guarantee. The runtime does not read the manifest automatically.

Asset selections must be top-level names such as `templates`, `dist`, or `settings.json`, not globs or nested paths. Directories are copied recursively; files become mode 0644 and directories 0755. Symlinks and special files are rejected. Common environment files, Git metadata, private-key files, and SQLite database files are rejected rather than silently omitted. This is a filename safeguard, not a secret-content scanner. Secrets, live data, uploaded files, and writable state belong outside the artifact.

## Docker adapter {#docker}

Build the image using the artifact directory as the entire build context:

```sh
docker build -t my-site:v1 .hyperchi/releases/v1

docker run --rm \
  --read-only --tmpfs /tmp \
  --stop-timeout 30 \
  --env-file /secure/my-site.env \
  -p 8080:8080 \
  my-site:v1
```

The generated Dockerfile copies only the compiled binary and selected assets. It runs `/app/app` as UID/GID `65532:65532`, with `/app` as its working directory. The image platform is fixed to the binary's architecture; creating an ARM64 artifact does not make it runnable on an AMD64 host without emulation. BuildKit may warn about the constant `FROM --platform`; here it intentionally matches the precompiled artifact.

The default base is `gcr.io/distroless/static-debian13:nonroot`. It has no shell, so the entrypoint uses exec form and there is no shell-based health check. See the [Distroless image documentation](https://github.com/GoogleContainerTools/distroless). For reproducible releases, pass a reviewed image digest using `--runtime-image registry/image@sha256:...`. A custom image must support the chosen architecture and UID and provide any runtime certificates or data your app needs.

The adapter sets `HOST=0.0.0.0`, the selected `PORT`, `ENVIRONMENT=production`, and `DB_DISABLED=true`. Override them through Docker's runtime environment. If SQLite is needed, set `DB_DISABLED=false`, set `DB_PATH` to a mounted writable volume, and arrange ownership for UID 65532. Supply `SECRET_KEY` and other secrets at runtime. The Dockerfile does not create volumes or generate secrets.

## systemd adapter {#systemd}

```sh
hyperchi deploy build \
  --name my-site \
  --target systemd \
  --arch amd64 \
  --assets templates,dist \
  --output .hyperchi/releases/v1-systemd
```

The generated `my-site.service` expects the complete artifact under `/opt/my-site` and uses that directory as its working directory. It runs `/opt/my-site/app` with `DynamicUser=yes`, a private temporary directory, a protected filesystem, and `StateDirectory=my-site`. The service manager provides writable persistent state under `/var/lib/my-site`.

Deploy the artifact to the matching Linux architecture, install the unit through your usual service-management process, and verify it with `systemd-analyze verify` before enabling it. The build command performs none of those installation steps. This adapter requires systemd features including dynamic users and state directories; it is not an init script for other service managers.

The optional `/etc/my-site.env` file overrides the generated environment defaults. Keep it outside the artifact with restricted permissions. Database access is disabled by default; to enable SQLite, set `DB_DISABLED=false`. The unit already supplies `DB_PATH=/var/lib/my-site/app.db`. Add application secrets and proxy configuration to the same external environment file or your service-management setup.

The service uses `Restart=on-failure`, a five-second restart delay, SIGTERM, and a 30-second stop timeout. Keep that timeout longer than your application's shutdown grace period. Review the generated unit when your app needs additional writable paths or other runtime privileges.

## Application requirements {#runtime}

Use `config.FromEnv()` and serve on `cfg.Server.Address()` so runtime configuration takes effect. The adapter cannot make an application honor environment variables it never reads. Keep the application in the foreground and use `app.Serve` or `ServeContext` to drain requests on shutdown.

Both adapters assume a static Go executable with no cgo dependency. Register health routes in application code and configure health/readiness probes in your hosting environment; the adapters do not inject source code or assume a health endpoint exists. TLS termination, domain names, trusted proxy ranges, database provisioning, backups, deployment rollout, and rollback remain explicit operational choices.

## Next steps {#next}

The [deploying guide](/docs/deploying) covers health checks, graceful shutdown, and reverse proxies. Use [testing](/docs/testing) to verify your application before building the release artifact.

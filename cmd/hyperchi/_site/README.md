# __APPNAME__

Run `task dev` and visit http://localhost:8080. Templates render at startup; restart
the process after editing them. Static files revalidate in development.

`task serve` builds and starts production with explicit HTTP limits and graceful
shutdown. Set `HOST`, `PORT`, and `ENVIRONMENT` through the process environment.
The site needs no Node tooling, database, or realtime extensions. Go 1.26.8 is
required; no C toolchain is needed (the framework's SQLite driver is pure Go).

Add application handlers to `app.Router()`. Use `hyperchi.ProtectForm` for bounded
URL-encoded forms. Set `cfg.Database.Disabled = false` and configure its path when
adding persistent data. Configure `cfg.Security.TrustedProxies` only for proxies
that actually reach this server. The default uses the transport peer.

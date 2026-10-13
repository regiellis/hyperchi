module github.com/regiellis/hyperchi/examples/homelab-search

go 1.26.0

require (
	github.com/regiellis/hyperchi v0.0.0
	github.com/regiellis/hyperchi/modules/markdown v0.0.0
)

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-chi/chi/v5 v5.3.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tdewolff/minify/v2 v2.24.19 // indirect
	github.com/tdewolff/parse/v2 v2.8.16 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	go.etcd.io/bbolt v1.5.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.60.1 // indirect
)

replace github.com/regiellis/hyperchi => ../..

replace github.com/regiellis/hyperchi/modules/markdown => ../../modules/markdown

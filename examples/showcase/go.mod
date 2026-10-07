module showcase

go 1.26.8

require (
	github.com/CloudyKit/jet/v6 v6.3.3
	github.com/a-h/templ v0.3.1070
	github.com/regiellis/hyperchi v0.1.0
)

require (
	github.com/CloudyKit/fastprinter v0.0.0-20200109182630-33d98a066a53 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-chi/chi/v5 v5.3.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tdewolff/minify/v2 v2.24.13 // indirect
	github.com/tdewolff/parse/v2 v2.8.12 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	go.etcd.io/bbolt v1.4.3 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.60.1 // indirect
)

replace github.com/regiellis/hyperchi => ../../

require (
	github.com/regiellis/hyperchi/modules/jet v0.0.0
	github.com/regiellis/hyperchi/modules/markdown v0.0.0
	github.com/regiellis/hyperchi/modules/templ v0.0.0
)

replace github.com/regiellis/hyperchi/modules/templ => ../../modules/templ

replace github.com/regiellis/hyperchi/modules/jet => ../../modules/jet

replace github.com/regiellis/hyperchi/modules/markdown => ../../modules/markdown

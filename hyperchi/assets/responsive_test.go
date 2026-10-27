package assets

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponsiveImageVariants(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	input := filepath.Join(root, "source.png")
	writePNG(t, input, 80, 40)
	p := NewWithConfig(&Config{OutputDir: filepath.Join(root, "out")})
	widths := []int{160, 40, 40, 20}
	result, err := p.ResponsiveImage(input, ImageOptions{Widths: widths, URLPrefix: "/static images,a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Variants) != 3 {
		t.Fatalf("variants: %+v", result)
	}
	for i, want := range []int{20, 40, 80} {
		v := result.Variants[i]
		cfg := decodeConfig(t, v.Path)
		if cfg.Width != want || cfg.Height != want/2 || v.Width != cfg.Width || v.Height != cfg.Height {
			t.Fatalf("wrong dimensions: %+v %+v", v, cfg)
		}
		if !strings.HasPrefix(v.URL, "/static%20images%2Ca/responsive/") {
			t.Fatalf("URL: %s", v.URL)
		}
	}
	data, err := os.ReadFile(result.Variants[2].Path)
	if err != nil {
		t.Fatal(err)
	}
	transparent, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, alpha := transparent.At(75, 15).RGBA(); alpha != 0 {
		t.Fatal("PNG lost transparency")
	}
	if widths[0] != 160 {
		t.Fatal("mutated caller widths")
	}
	tmpl := template.Must(template.New("image").Parse(`<img srcset="{{.SrcSet}}">`))
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.String(), "ZgotmplZ") || !strings.Contains(rendered.String(), "80w") {
		t.Fatalf("template: %s", &rendered)
	}
	again, err := p.ResponsiveImage(input, ImageOptions{Widths: widths, URLPrefix: "/static images,a"})
	if err != nil || again.SrcSet() != result.SrcSet() {
		t.Fatalf("not deterministic: %+v %v", again, err)
	}
	writePNG(t, input, 100, 50)
	changed, err := p.ResponsiveImage(input, ImageOptions{Widths: []int{100}, URLPrefix: "/static"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Variants[0].Path == result.Variants[2].Path {
		t.Fatal("changed image reused filename")
	}
}

func TestResponsiveImageConversion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	input := filepath.Join(root, "source.png")
	writePNG(t, input, 80, 40)
	o := NewOptimizer(&Config{OutputDir: filepath.Join(root, "out"), MaxWidth: 60})
	result, err := o.ResponsiveImage(input, ImageOptions{Widths: []int{80, 120}, Format: "jpeg", URLPrefix: "/static"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Variants) != 1 || result.Variants[0].Width != 60 {
		t.Fatalf("MaxWidth: %+v", result)
	}
	data, err := os.ReadFile(result.Variants[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(55, 15).RGBA()
	if r < 64000 || g < 64000 || b < 64000 {
		t.Fatalf("matte is not white: %d %d %d", r, g, b)
	}
	pngResult, err := o.ResponsiveImage(result.Variants[0].Path, ImageOptions{Widths: []int{30}, Format: "png", URLPrefix: "/static"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeConfig(t, pngResult.Variants[0].Path); cfg.Width != 30 {
		t.Fatal(cfg)
	}
	webp, err := base64.StdEncoding.DecodeString(transparentWebP)
	if err != nil {
		t.Fatal(err)
	}
	webpPath := filepath.Join(root, "source.webp")
	writeTestFile(t, webpPath, string(webp))
	converted, err := o.ResponsiveImage(webpPath, ImageOptions{Widths: []int{10}, Format: "png", URLPrefix: "/static"})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Variants[0].Width != 1 {
		t.Fatal("upscaled WebP")
	}
}

func TestResponsiveImageValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		opts ImageOptions
	}{
		{"empty widths", ImageOptions{URLPrefix: "/static"}},
		{"negative width", ImageOptions{Widths: []int{-1}, URLPrefix: "/static"}},
		{"unsupported format", ImageOptions{Widths: []int{10}, Format: "webp", URLPrefix: "/static"}},
		{"external prefix", ImageOptions{Widths: []int{10}, URLPrefix: "https://example.com"}},
		{"protocol relative", ImageOptions{Widths: []int{10}, URLPrefix: "//example.com"}},
		{"encoded authority", ImageOptions{Widths: []int{10}, URLPrefix: "/%2Fexample.com"}},
		{"parent path", ImageOptions{Widths: []int{10}, URLPrefix: "/static/../private"}},
		{"query", ImageOptions{Widths: []int{10}, URLPrefix: "/static?x=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			o := NewOptimizer(&Config{OutputDir: out})
			if _, err := o.ResponsiveImage("missing.png", tc.opts); err == nil || strings.Contains(err.Error(), "no such file") {
				t.Fatalf("validation error: %v", err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("created output for invalid options")
			}
		})
	}
}

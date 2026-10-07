package assets

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/webp"
)

// transparentWebP is a 1x1 lossless WebP whose only pixel is fully
// transparent. Nothing in Go can encode WebP, so the fixture is literal.
const transparentWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

// writePNG writes a w x h image whose left half is opaque red and right half
// fully transparent.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w/2; x++ {
			img.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, buf.String())
}

// writeJPEG writes a w x h gradient JPEG at quality 100.
func writeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 11), B: uint8(x ^ y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, buf.String())
}

func decodeConfig(t *testing.T, path string) image.Config {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return cfg
}

// Every format, re-encoded, minified, or copied, lands in OutputDir/<rel>,
// mirroring the source tree.
func TestOptimizerOutputLayoutIsUnified(t *testing.T) {
	root := t.TempDir()
	src, out := filepath.Join(root, "assets"), filepath.Join(root, "static")
	writeJPEG(t, filepath.Join(src, "images", "photo.jpg"), 8, 8)
	writePNG(t, filepath.Join(src, "images", "logo.png"), 8, 8)
	writeTestFile(t, filepath.Join(src, "icons", "x.svg"), "<svg></svg>")
	writeTestFile(t, filepath.Join(src, "images", "anim.gif"), "GIF89a")
	webpData, err := base64.StdEncoding.DecodeString(transparentWebP)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(src, "images", "dot.webp"), string(webpData))
	writeTestFile(t, filepath.Join(src, "notes.txt"), "not an image")

	o := NewOptimizer(&Config{SourceDir: src, OutputDir: out})
	if err := o.OptimizeImages(src); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"images/photo.jpg", "images/logo.png", "icons/x.svg", "images/anim.gif", "images/dot.webp"} {
		want := filepath.Join(out, filepath.FromSlash(rel))
		if got := o.GetOptimizedPath(filepath.Join(src, filepath.FromSlash(rel))); got != want {
			t.Errorf("GetOptimizedPath(%s) = %s, want %s", rel, got, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Errorf("%s not written: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "notes.txt")); !os.IsNotExist(err) {
		t.Error("OptimizeImages copied a non-image")
	}
	if _, err := os.Stat(filepath.Join(out, "images", "images")); !os.IsNotExist(err) {
		t.Error("output nested an extra images/ directory")
	}
}

func TestGetOptimizedPathStaysInOutputDir(t *testing.T) {
	o := NewOptimizer(&Config{SourceDir: "assets", OutputDir: "static"})
	for in, want := range map[string]string{
		"assets/a.png":            filepath.Join("static", "a.png"),
		"assets/img/b.png":        filepath.Join("static", "img", "b.png"),
		"elsewhere/c.png":         filepath.Join("static", "c.png"),
		"../../etc/d.png":         filepath.Join("static", "d.png"),
		"assets2/e.png":           filepath.Join("static", "e.png"),
		"/abs/outside/source.png": filepath.Join("static", "source.png"),
	} {
		if got := o.GetOptimizedPath(filepath.FromSlash(in)); got != want {
			t.Errorf("GetOptimizedPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// WebP is decoded with x/image/webp to validate it and copied unchanged, so
// it keeps its format and transparency.
func TestOptimizeWebPDecodesAndKeepsFile(t *testing.T) {
	root := t.TempDir()
	src, out := filepath.Join(root, "assets"), filepath.Join(root, "static")
	data, err := base64.StdEncoding.DecodeString(transparentWebP)
	if err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(src, "dot.webp")
	writeTestFile(t, in, string(data))

	o := NewOptimizer(&Config{SourceDir: src, OutputDir: out, MaxWidth: 1})
	if err := o.OptimizeImage(in); err != nil {
		t.Fatalf("OptimizeImage(webp): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "dot.webp"))
	if err != nil {
		t.Fatalf("WebP output missing: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("WebP output differs from the input")
	}
	if _, err := os.Stat(filepath.Join(out, "dot.jpg")); !os.IsNotExist(err) {
		t.Error("WebP was converted to JPEG")
	}
	img, err := webp.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("alpha = %d, want the transparent pixel kept", a)
	}

	bad := filepath.Join(src, "bad.webp")
	writeTestFile(t, bad, "RIFF not really")
	if err := o.OptimizeImage(bad); err == nil || !strings.Contains(err.Error(), "decode WebP") {
		t.Errorf("corrupt WebP: err = %v", err)
	}
}

func TestOptimizePNGResizesAndKeepsAlpha(t *testing.T) {
	root := t.TempDir()
	in := filepath.Join(root, "src", "wide.png")
	writePNG(t, in, 40, 20)
	out := filepath.Join(root, "out")
	o := NewOptimizer(&Config{SourceDir: filepath.Join(root, "src"), OutputDir: out, MaxWidth: 10})
	if err := o.OptimizeImage(in); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(filepath.Join(out, "wide.png"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	if cerr := f.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 10 || b.Dy() != 5 {
		t.Fatalf("resized to %v, want 10x5", b)
	}
	if _, _, _, a := img.At(9, 2).RGBA(); a != 0 {
		t.Errorf("right edge alpha = %d, want transparent", a)
	}
	if r, _, _, a := img.At(0, 2).RGBA(); r>>8 != 255 || a>>8 != 255 {
		t.Errorf("left edge = r %d a %d, want opaque red", r>>8, a>>8)
	}

	// An image no wider than MaxWidth keeps its size.
	narrow := filepath.Join(root, "src", "narrow.png")
	writePNG(t, narrow, 6, 3)
	if err := o.OptimizeImage(narrow); err != nil {
		t.Fatal(err)
	}
	if cfg := decodeConfig(t, filepath.Join(out, "narrow.png")); cfg.Width != 6 || cfg.Height != 3 {
		t.Errorf("narrow image = %dx%d, want 6x3", cfg.Width, cfg.Height)
	}
}

func TestOptimizeJPEGResizesAndUsesQuality(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	in := filepath.Join(src, "photo.jpeg")
	writeJPEG(t, in, 64, 32)

	sizes := map[int]int64{}
	for _, q := range []int{10, 95} {
		out := filepath.Join(root, "out", string(rune('a'+q%26)))
		o := NewOptimizer(&Config{SourceDir: src, OutputDir: out, MaxWidth: 16, JPEGQuality: q})
		if err := o.OptimizeImage(in); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(out, "photo.jpeg")
		if cfg := decodeConfig(t, path); cfg.Width != 16 || cfg.Height != 8 {
			t.Fatalf("quality %d: size %dx%d, want 16x8", q, cfg.Width, cfg.Height)
		}
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		sizes[q] = st.Size()
	}
	if sizes[10] >= sizes[95] {
		t.Errorf("quality 10 file (%d bytes) is not smaller than quality 95 (%d bytes)", sizes[10], sizes[95])
	}

	// The default quality applies when JPEGQuality is zero.
	o := NewOptimizer(&Config{SourceDir: src, OutputDir: filepath.Join(root, "default")})
	if err := o.OptimizeImage(in); err != nil {
		t.Fatal(err)
	}
	if cfg := decodeConfig(t, filepath.Join(root, "default", "photo.jpeg")); cfg.Width != 64 {
		t.Errorf("width without MaxWidth = %d, want 64", cfg.Width)
	}
}

func TestOptimizeRejectsCorruptImages(t *testing.T) {
	root := t.TempDir()
	o := NewOptimizer(&Config{SourceDir: root, OutputDir: filepath.Join(root, "out")})
	for _, name := range []string{"bad.jpg", "bad.png"} {
		path := filepath.Join(root, name)
		writeTestFile(t, path, "not an image")
		if err := o.OptimizeImage(path); err == nil {
			t.Errorf("%s: corrupt image accepted", name)
		}
	}
	if err := o.OptimizeImage(filepath.Join(root, "missing.png")); err == nil {
		t.Error("missing file accepted")
	}
	if err := o.OptimizeImages(filepath.Join(root, "absent")); err == nil {
		t.Error("missing directory accepted")
	}
}

func TestOptimizeSVG(t *testing.T) {
	root := t.TempDir()
	in := filepath.Join(root, "icon.svg")
	svg := "<svg>\n  <!-- note -->\n\t<path d=\"M0 0\" />\n</svg>\n"
	writeTestFile(t, in, svg)

	plain := NewOptimizer(&Config{SourceDir: root, OutputDir: filepath.Join(root, "plain")})
	if err := plain.OptimizeImage(in); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "plain", "icon.svg")); err != nil || string(got) != svg {
		t.Errorf("unminified SVG = %q, %v; want it unchanged", got, err)
	}

	min := NewOptimizer(&Config{SourceDir: root, OutputDir: filepath.Join(root, "min"), EnableMinify: true})
	if err := min.OptimizeImage(in); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "min", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `<svg><path d="M0 0"/></svg>` {
		t.Errorf("minified SVG = %q", got)
	}
	// Comments, including an unterminated one at the end, are dropped without
	// leaking their bytes into the output.
	for in, want := range map[string]string{
		"<svg>a<!-- unterminated":       "<svg>a",
		"<svg>a<!--x-->b<!---->c</svg>": "<svg>abc</svg>",
		"<svg><!-- a --> <b/></svg>":    "<svg><b/></svg>",
		"<svg>no comments</svg>":        "<svg>no comments</svg>",
		"<svg><!--":                     "<svg>",
	} {
		got, err := min.optimizeSVGContent(in)
		if err != nil || got != want {
			t.Errorf("optimizeSVGContent(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestGetImageInfo(t *testing.T) {
	root := t.TempDir()
	in := filepath.Join(root, "src", "a.PNG")
	writePNG(t, in, 2, 2)
	o := NewOptimizer(&Config{SourceDir: filepath.Join(root, "src"), OutputDir: filepath.Join(root, "out")})

	info := o.GetImageInfo(in)
	if info["format"] != ".png" || info["optimized"] != false || info["size"] == nil {
		t.Fatalf("before = %v", info)
	}
	if err := o.OptimizeImage(in); err != nil {
		t.Fatal(err)
	}
	if info := o.GetImageInfo(in); info["optimized"] != true {
		t.Errorf("after = %v", info)
	}
	if info := o.GetImageInfo(filepath.Join(root, "missing.jpg")); info["size"] != nil {
		t.Errorf("missing file = %v", info)
	}
}

func TestResizeToWidthMinimumHeight(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 1))
	if b := resizeToWidth(img, 10).Bounds(); b.Dx() != 10 || b.Dy() != 1 {
		t.Errorf("thin image resized to %v, want 10x1", b)
	}
}

// The SVG minifier only touches markup: text content such as "a = b" keeps
// its spacing.
func TestOptimizeSVGKeepsTextContent(t *testing.T) {
	o := NewOptimizer(&Config{EnableMinify: true})
	in := "<svg>\n  <text x=\"1\">a = b</text>\n  <desc>x = y, p =q</desc>\n</svg>"
	got, err := o.optimizeSVGContent(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, ">a = b</text>") || !strings.Contains(got, ">x = y, p =q</desc>") {
		t.Fatalf("SVG text content changed: %q", got)
	}
}

// The optimizer writes an optimized copy of each image to OutputDir, keeping
// the image's path relative to SourceDir (see GetOptimizedPath).
//
//	optimizer := NewOptimizer(&Config{SourceDir: "assets", OutputDir: "static", MaxWidth: 1920})
//	err := optimizer.OptimizeImage("assets/images/hero.jpg") // writes static/images/hero.jpg

package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/svg"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const svgMediaType = "image/svg+xml"

// svgMinifier minifies SVG, with CSS registered for embedded <style> blocks.
// A *minify.M is safe for concurrent use once configured.
var svgMinifier = func() *minify.M {
	m := minify.New()
	m.AddFunc(cssMediaType, css.Minify)
	m.AddFunc(svgMediaType, svg.Minify)
	return m
}()

// defaultJPEGQuality is used when Config.JPEGQuality is zero.
const defaultJPEGQuality = 85

// Optimizer writes optimized copies of images. It is safe for concurrent use
// as long as its Config is not changed meanwhile.
type Optimizer struct {
	config *Config
}

// NewOptimizer returns an Optimizer that reads its settings from config.
func NewOptimizer(config *Config) *Optimizer {
	return &Optimizer{
		config: config,
	}
}

// OptimizeImages optimizes every image under inputDir (by extension: .svg,
// .png, .jpg, .jpeg, .gif, .webp, .bmp, .tiff). The first failure stops the
// walk and is returned.
func (o *Optimizer) OptimizeImages(inputDir string) error {
	return filepath.WalkDir(inputDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !o.isImageFile(path) {
			return nil
		}
		return o.OptimizeImage(path)
	})
}

// OptimizeImage writes an optimized copy of one file to GetOptimizedPath:
//   - .jpg, .jpeg: decoded, downscaled to MaxWidth when wider, re-encoded at
//     JPEGQuality (85 when zero).
//   - .png: decoded, downscaled the same way, re-encoded with the best
//     compression level. Transparency is kept.
//   - .webp: decoded to check that it is a valid WebP file, then copied
//     unchanged. Neither the standard library nor golang.org/x/image has a WebP
//     encoder, so WebP files are not recompressed or resized.
//   - .svg: minified by github.com/tdewolff/minify when minification is on
//     (comments and markup whitespace removed, text content kept), otherwise
//     copied.
//   - anything else: copied unchanged.
func (o *Optimizer) OptimizeImage(inputPath string) error {
	ext := strings.ToLower(filepath.Ext(inputPath))

	switch ext {
	case ".svg":
		return o.optimizeSVG(inputPath)
	case ".jpg", ".jpeg":
		return o.optimizeJPEG(inputPath)
	case ".png":
		return o.optimizePNG(inputPath)
	case ".webp":
		return o.optimizeWebP(inputPath)
	default:
		return o.copyFile(inputPath)
	}
}

// optimizeSVG writes the SVG, minified when minification is on.
func (o *Optimizer) optimizeSVG(inputPath string) error {
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read SVG file %s: %w", inputPath, err)
	}
	optimized, err := o.optimizeSVGContent(string(content))
	if err != nil {
		return fmt.Errorf("failed to minify SVG file %s: %w", inputPath, err)
	}
	return o.writeFile(o.GetOptimizedPath(inputPath), []byte(optimized))
}

// optimizeSVGContent minifies SVG markup with github.com/tdewolff/minify, an
// XML parser-based minifier: it drops comments and markup whitespace and
// leaves text content alone. Embedded <style> blocks are minified as CSS.
func (o *Optimizer) optimizeSVGContent(content string) (string, error) {
	if !o.config.minify() {
		return content, nil
	}
	return svgMinifier.String(svgMediaType, content)
}

// optimizeJPEG decodes, downscales, and re-encodes a JPEG.
func (o *Optimizer) optimizeJPEG(inputPath string) error {
	src, err := decodeFile(inputPath, jpeg.Decode)
	if err != nil {
		return fmt.Errorf("failed to open JPEG %s: %w", inputPath, err)
	}
	quality := defaultJPEGQuality
	if o.config.JPEGQuality > 0 {
		quality = o.config.JPEGQuality
	}
	img := resizeToWidth(src, o.config.MaxWidth)
	return o.encodeFile(o.GetOptimizedPath(inputPath), func(w io.Writer) error {
		return jpeg.Encode(w, img, &jpeg.Options{Quality: quality})
	})
}

// optimizePNG decodes, downscales, and re-encodes a PNG.
func (o *Optimizer) optimizePNG(inputPath string) error {
	src, err := decodeFile(inputPath, png.Decode)
	if err != nil {
		return fmt.Errorf("failed to open PNG %s: %w", inputPath, err)
	}
	img := resizeToWidth(src, o.config.MaxWidth)
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	return o.encodeFile(o.GetOptimizedPath(inputPath), func(w io.Writer) error {
		return enc.Encode(w, img)
	})
}

// optimizeWebP checks that a WebP file decodes, then copies it unchanged.
func (o *Optimizer) optimizeWebP(inputPath string) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read WebP %s: %w", inputPath, err)
	}
	if _, err := webp.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("failed to decode WebP %s: %w", inputPath, err)
	}
	return o.writeFile(o.GetOptimizedPath(inputPath), data)
}

// decodeFile opens path and decodes it with decode.
func decodeFile(path string, decode func(io.Reader) (image.Image, error)) (img image.Image, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return decode(f)
}

// resizeToWidth scales img down to maxWidth, keeping its aspect ratio, with
// Catmull-Rom resampling. An image no wider than maxWidth, or a maxWidth of
// zero, is returned as is.
func resizeToWidth(img image.Image, maxWidth int) image.Image {
	b := img.Bounds()
	if maxWidth <= 0 || b.Dx() <= maxWidth {
		return img
	}
	height := (b.Dy()*maxWidth + b.Dx()/2) / b.Dx()
	if height < 1 {
		height = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, maxWidth, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// encodeFile creates path (and its directory) and writes it with encode,
// surfacing the close error, since a failed close can mean a truncated file.
func (o *Optimizer) encodeFile(path string, encode func(io.Writer) error) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", path, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close output file %s: %w", path, cerr)
		}
	}()
	if err := encode(out); err != nil {
		return fmt.Errorf("failed to encode %s: %w", path, err)
	}
	return nil
}

// copyFile copies a file unchanged to its optimized path.
func (o *Optimizer) copyFile(inputPath string) error {
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", inputPath, err)
	}
	return o.writeFile(o.GetOptimizedPath(inputPath), content)
}

// writeFile writes content to path, creating its directory.
func (o *Optimizer) writeFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}

// isImageFile reports whether path has one of the extensions OptimizeImages
// visits.
func (o *Optimizer) isImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff":
		return true
	default:
		return false
	}
}

// GetOptimizedPath returns where OptimizeImage writes originalPath: OutputDir
// joined with the path relative to SourceDir, so "assets/images/a.png" becomes
// "static/images/a.png". A file outside SourceDir lands directly in OutputDir
// under its base name, so no input path can write above OutputDir.
func (o *Optimizer) GetOptimizedPath(originalPath string) string {
	rel, err := filepath.Rel(o.config.SourceDir, originalPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = filepath.Base(originalPath)
	}
	return filepath.Join(o.config.OutputDir, rel)
}

// GetImageInfo returns the file's size and modification time, its extension
// as "format", and whether an optimized copy exists.
func (o *Optimizer) GetImageInfo(path string) map[string]any {
	info := make(map[string]any)

	if stat, err := os.Stat(path); err == nil {
		info["size"] = stat.Size()
		info["modified"] = stat.ModTime()
	}

	info["format"] = strings.ToLower(filepath.Ext(path))
	info["optimized"] = o.isOptimized(path)

	return info
}

// isOptimized reports whether the optimized copy of path exists.
func (o *Optimizer) isOptimized(path string) bool {
	_, err := os.Stat(o.GetOptimizedPath(path))
	return err == nil
}

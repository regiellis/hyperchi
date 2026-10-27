package assets

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ImageOptions describes a set of responsive images generated at build time.
type ImageOptions struct {
	// Widths must contain positive pixel widths. Larger widths are clamped to
	// the source width and Config.MaxWidth; duplicates are removed.
	Widths []int
	// Format is "jpeg" or "png". Empty preserves JPEG/PNG input format;
	// WebP input requires an explicit output format. JPEG uses a white matte.
	Format string
	// URLPrefix is the local absolute URL serving OutputDir, e.g. "/static".
	URLPrefix string
}

// ImageVariant describes one generated file and its public URL.
type ImageVariant struct {
	Path   string
	URL    string
	Width  int
	Height int
}

// ResponsiveImage contains ascending, distinct widths in a single format.
type ResponsiveImage struct {
	Variants []ImageVariant
}

// SrcSet returns a width-descriptor srcset suitable for an HTML template.
func (r ResponsiveImage) SrcSet() string {
	parts := make([]string, len(r.Variants))
	for i, v := range r.Variants {
		parts[i] = fmt.Sprintf("%s %dw", v.URL, v.Width)
	}
	return strings.Join(parts, ", ")
}

// ResponsiveImage generates JPEG/PNG variants from JPEG, PNG, or WebP input.
// It decodes once, preserves aspect ratio, never upscales, and names files by
// their encoded content. Inputs are limited to 32 MiB and 40 million pixels.
// Call during a build or startup, not for arbitrary request parameters.
// Files already written remain if a later variant fails. The source is kept.
func (o *Optimizer) ResponsiveImage(inputPath string, opts ImageOptions) (ResponsiveImage, error) {
	fail := func(err error) (ResponsiveImage, error) {
		return ResponsiveImage{}, fmt.Errorf("responsive image %q: %w", inputPath, err)
	}
	if o == nil || o.config == nil || o.config.OutputDir == "" {
		return fail(fmt.Errorf("OutputDir is required"))
	}
	prefix, err := url.Parse(opts.URLPrefix)
	if err != nil || prefix.Scheme != "" || prefix.Host != "" || prefix.RawQuery != "" || prefix.Fragment != "" ||
		!strings.HasPrefix(opts.URLPrefix, "/") || strings.HasPrefix(opts.URLPrefix, "//") || strings.ContainsAny(opts.URLPrefix, "?#\\") {
		return fail(fmt.Errorf("URLPrefix must be a local absolute URL path"))
	}
	if strings.HasPrefix(prefix.Path, "//") || strings.Contains(prefix.Path, "\\") || path.Clean(prefix.Path) != strings.TrimRight(prefix.Path, "/") && prefix.Path != "/" {
		return fail(fmt.Errorf("URLPrefix must be a clean local path"))
	}
	if len(opts.Widths) == 0 || len(opts.Widths) > 32 {
		return fail(fmt.Errorf("provide between 1 and 32 widths"))
	}
	for _, w := range opts.Widths {
		if w <= 0 {
			return fail(fmt.Errorf("widths must be positive"))
		}
	}
	format := strings.ToLower(opts.Format)
	if format == "jpg" {
		format = "jpeg"
	}
	if format != "" && format != "jpeg" && format != "png" {
		return fail(fmt.Errorf("unsupported output format %q", opts.Format))
	}
	quality := o.config.JPEGQuality
	if quality == 0 {
		quality = defaultJPEGQuality
	}
	if quality < 1 || quality > 100 || o.config.MaxWidth < 0 {
		return fail(fmt.Errorf("invalid JPEGQuality or MaxWidth"))
	}
	f, err := os.Open(inputPath)
	if err != nil {
		return fail(err)
	}
	const maxBytes = 32 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	closeErr := f.Close()
	if err != nil {
		return fail(err)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if len(data) > maxBytes {
		return fail(fmt.Errorf("input exceeds 32 MiB"))
	}
	cfg, inputFormat, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fail(err)
	}
	if inputFormat != "jpeg" && inputFormat != "png" && inputFormat != "webp" {
		return fail(fmt.Errorf("unsupported input format %q", inputFormat))
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > 40_000_000/int64(cfg.Height) {
		return fail(fmt.Errorf("input exceeds 40 million pixels"))
	}
	if format == "" {
		format = inputFormat
		if format == "webp" {
			return fail(fmt.Errorf("WebP input requires jpeg or png output"))
		}
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fail(err)
	}
	widths := slices.Clone(opts.Widths)
	for i, w := range widths {
		widths[i] = min(w, cfg.Width)
		if o.config.MaxWidth > 0 {
			widths[i] = min(widths[i], o.config.MaxWidth)
		}
	}
	slices.Sort(widths)
	widths = slices.Compact(widths)
	result := ResponsiveImage{Variants: make([]ImageVariant, 0, len(widths))}
	for _, width := range widths {
		img := resizeToWidth(src, width)
		var encoded bytes.Buffer
		if format == "jpeg" {
			matte := image.NewRGBA(img.Bounds())
			draw.Draw(matte, matte.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			draw.Draw(matte, matte.Bounds(), img, img.Bounds().Min, draw.Over)
			err = jpeg.Encode(&encoded, matte, &jpeg.Options{Quality: quality})
		} else {
			encoder := png.Encoder{CompressionLevel: png.BestCompression}
			err = encoder.Encode(&encoded, img)
		}
		if err != nil {
			return fail(err)
		}
		sum := sha256.Sum256(encoded.Bytes())
		name := fmt.Sprintf("%x.%s", sum, format)
		rel := "responsive/" + name
		output := filepath.Join(o.config.OutputDir, filepath.FromSlash(rel))
		if err := writeImageAtomic(output, encoded.Bytes()); err != nil {
			return fail(err)
		}
		u := &url.URL{Path: strings.TrimRight(prefix.Path, "/") + "/" + rel}
		publicURL := strings.ReplaceAll(u.String(), ",", "%2C")
		result.Variants = append(result.Variants, ImageVariant{Path: output, URL: publicURL, Width: width, Height: img.Bounds().Dy()})
	}
	return result, nil
}

// ResponsiveImage generates variants using the pipeline's configuration.
func (ap *AssetPipeline) ResponsiveImage(inputPath string, opts ImageOptions) (ResponsiveImage, error) {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	return ap.optimizer.ResponsiveImage(inputPath, opts)
}

func writeImageAtomic(path string, data []byte) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".image-*")
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		if rerr := os.Remove(f.Name()); rerr != nil && !os.IsNotExist(rerr) {
			err = errors.Join(err, rerr)
		}
	}()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(f.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

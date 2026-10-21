package tools

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxImageDimension = 1600
	maxImagePayload   = 3 << 20
	imageJPEGQuality  = 85
)

// prepareImage downscales an image before it becomes an attachment. It
// returns the original path when the image is already small enough, and a
// note describing what changed otherwise.
func prepareImage(env *Env, path, mime string) (string, string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if st.Size() <= maxImagePayload {
		if f, err := os.Open(path); err == nil {
			cfg, _, err := image.DecodeConfig(f)
			_ = f.Close()
			if err == nil && cfg.Width <= maxImageDimension && cfg.Height <= maxImageDimension {
				return path, "", nil
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Formats the standard library cannot decode (webp, bmp) stay as
		// they are; the provider may still accept them.
		return path, "", nil
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	nw, nh := fitDimensions(w, h, maxImageDimension)
	if nw == w && nh == h && len(data) <= maxImagePayload {
		return path, "", nil
	}
	scaled := boxResize(img, nw, nh)
	outMime := "image/jpeg"
	ext := ".jpg"
	if format == "png" || format == "gif" {
		outMime = "image/png"
		ext = ".png"
	}
	var buf bytes.Buffer
	switch outMime {
	case "image/png":
		if err := png.Encode(&buf, scaled); err != nil {
			return "", "", err
		}
	default:
		if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: imageJPEGQuality}); err != nil {
			return "", "", err
		}
	}
	dir := ""
	if env != nil {
		dir = env.SpillDir()
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "wombat-images")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, st.ModTime().UnixNano(), st.Size())))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + "-" + hex.EncodeToString(sum[:6]) + ext
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return "", "", err
	}
	note := fmt.Sprintf("downscaled from %dx%d to %dx%d", w, h, nw, nh)
	return dst, note, nil
}

// fitDimensions scales a box to fit max on its long side.
func fitDimensions(w, h, maxSide int) (int, int) {
	if w <= maxSide && h <= maxSide {
		return w, h
	}
	if w >= h {
		return maxSide, int(float64(h)*float64(maxSide)/float64(w) + 0.5)
	}
	return int(float64(w)*float64(maxSide)/float64(h) + 0.5), maxSide
}

// boxResize averages the source area behind each destination pixel. It is a
// small, dependency-free filter that looks far better than nearest-neighbor
// on screenshots and photos.
func boxResize(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0 := b.Min.Y + y*sh/h
		y1 := b.Min.Y + (y+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0 := b.Min.X + x*sw/w
			x1 := b.Min.X + (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.RGBAModel.Convert(src.At(sx, sy)).(color.RGBA)
					r += uint64(c.R)
					g += uint64(c.G)
					bl += uint64(c.B)
					a += uint64(c.A)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), uint8(a / n)})
		}
	}
	return dst
}

func mimeOf(path, fallback string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	}
	return fallback
}

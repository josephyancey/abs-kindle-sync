// Package cover gives books their cover in the Kindle library.
//
// The Kindle doesn't use the cover inside a book that is typed EBOK (which calibre gives every
// AZW3 it makes): it asks Amazon for the cover by the book's ASIN, and a sideloaded book's ASIN
// is made up, so the library shows "No image available". Like calibre, we write the thumbnail
// the library looks for ourselves, made from the book's own cover.
package cover

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/josephyancey/abs-kindle-sync/app/internal/azw3"
)

// Dir is where the library keeps cover thumbnails.
var Dir = "/mnt/us/system/thumbnails"

// Height of the thumbnails Amazon's own books get on a Paperwhite.
const height = 500

// placeholder is the size under which a thumbnail is the Kindle's "No image available" card.
const placeholder = 4096

// Ensure writes the library thumbnail for the book at path unless it already has a real one.
// known is the thumbnail path a previous call returned, which saves reading the book again.
// fallback supplies a cover for books that don't name one (nil result: none); it may be nil.
// It returns the thumbnail path and whether it wrote it.
func Ensure(path, known string, fallback func() ([]byte, error)) (string, bool, error) {
	if known != "" && real(known) {
		return known, false, nil
	}
	info, err := azw3.ReadInfoFile(path)
	if err != nil {
		return "", false, err
	}
	if info.ASIN == "" || info.CDEType == "" {
		return "", false, nil // the Kindle names thumbnails by ASIN; without one it makes its own
	}
	target := filepath.Join(Dir, fmt.Sprintf("thumbnail_%s_%s_portrait.jpg", info.ASIN, info.CDEType))
	if real(target) {
		return target, false, nil
	}
	source := info.Cover
	if len(source) == 0 && fallback != nil {
		if source, err = fallback(); err != nil {
			return "", false, err
		}
	}
	if len(source) == 0 {
		return "", false, nil
	}
	img, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return "", false, fmt.Errorf("cover image: %w", err)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, shrink(img, height), &jpeg.Options{Quality: 85}); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return "", false, err
	}
	tmp := target + ".part"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return "", false, err
	}
	return target, true, nil
}

// real reports whether a thumbnail exists and isn't the "No image available" card.
func real(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() >= placeholder
}

// shrink scales img to the given height (never up), in grayscale, averaging the source pixels
// under each output pixel.
func shrink(img image.Image, h int) *image.Gray {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sh <= h {
		h = sh
	}
	w := sw * h / sh
	if w < 1 {
		w = 1
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, (y+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sum, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					sum += uint32(gray(img, b.Min.X+sx, b.Min.Y+sy))
					n++
				}
			}
			out.SetGray(x, y, color.Gray{Y: uint8(sum / n)})
		}
	}
	return out
}

// gray is the brightness of one pixel. JPEG covers keep brightness as its own channel, so read it
// directly; going through img.At allocates for every pixel.
func gray(img image.Image, x, y int) uint8 {
	switch m := img.(type) {
	case *image.YCbCr:
		return m.Y[m.YOffset(x, y)]
	case *image.Gray:
		return m.Pix[m.PixOffset(x, y)]
	}
	return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
}

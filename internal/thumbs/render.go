package thumbs

import (
	"bufio"
	"bytes"
	"fmt"
	"image"

	"image/jpeg"
	"image/png"
	"os"

	// Decoders registered with image.Decode. webp/bmp come from x/image.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Render decodes the image at path and returns a small square-ish thumbnail.
// JPEG is used for opaque images, PNG when the source has transparency.
func Render(path string, px int) ([]byte, string, error) {
	if px <= 0 {
		px = Size
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	img, format, err := decode(reader)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrNotImage, err)
	}
	_ = format

	src := img.Bounds()
	w, h := fit(src, px)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// Scale into the destination; draw.CatmullRom is the quality/price sweet spot
	// at these sizes.
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, src, draw.Src, nil)

	var buf bytes.Buffer
	if hasAlpha(img) {
		if err := png.Encode(&buf, dst); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

// decode tries the stdlib decoders first (they are registered) and then the
// x/image ones, so webp/bmp work even if their registration differs by version.
func decode(r *bufio.Reader) (image.Image, string, error) {
	peek, err := r.Peek(12)
	if err != nil && len(peek) == 0 {
		return nil, "", err
	}
	if len(peek) >= 12 && string(peek[:4]) == "RIFF" && string(peek[8:12]) == "WEBP" {
		img, err := webp.Decode(r)
		return img, "webp", err
	}
	if len(peek) >= 2 && peek[0] == 'B' && peek[1] == 'M' {
		img, err := bmp.Decode(r)
		return img, "bmp", err
	}
	return image.Decode(r)
}

// hasAlpha reports whether the decoded image has any transparency.
func hasAlpha(img image.Image) bool {
	if op, ok := img.(interface{ Opaque() bool }); ok {
		return !op.Opaque()
	}
	return true
}

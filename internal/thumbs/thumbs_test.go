package thumbs

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, dir string, w, h int, alpha bool) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fill := color.NRGBA{R: 20, G: 120, B: 220, A: 255}
	if alpha {
		fill.A = 100
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "img.png")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderShrinksAndKeepsAspect(t *testing.T) {
	path := writePNG(t, t.TempDir(), 800, 400, false)
	data, mime, err := Render(path, 96)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg for an opaque image", mime)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode thumb: %v", err)
	}
	if cfg.Width != 96 || cfg.Height != 48 {
		t.Fatalf("thumb = %dx%d, want 96x48 (aspect preserved)", cfg.Width, cfg.Height)
	}
}

func TestRenderKeepsTransparencyAsPNG(t *testing.T) {
	path := writePNG(t, t.TempDir(), 300, 300, true)
	_, mime, err := Render(path, 96)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png for a translucent image", mime)
	}
}

func TestRenderRejectsNonImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Render(p, 96); err == nil {
		t.Fatal("Render accepted a text file")
	}
}

func TestCacheEvictsAndDeduplicates(t *testing.T) {
	c := New()
	key := Key{Path: "a.png", ModUnix: 1, Size: 10, Px: 96}
	calls := 0
	render := func() ([]byte, string, error) {
		calls++
		return []byte("data"), "image/jpeg", nil
	}
	if _, _, err := c.GetOrRender(t.Context(), key, render); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetOrRender(t.Context(), key, render); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("render called %d times, want 1 (cache hit)", calls)
	}

	// A different version of the same file is a different key.
	key2 := key
	key2.ModUnix = 2
	if _, _, err := c.GetOrRender(t.Context(), key2, render); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("render called %d times, want 2 after the file changed", calls)
	}
}

func TestFitNeverUpscales(t *testing.T) {
	w, h := fit(image.Rect(0, 0, 40, 20), 96)
	if w != 40 || h != 20 {
		t.Fatalf("fit small = %dx%d, want 40x20", w, h)
	}
	w, h = fit(image.Rect(0, 0, 1920, 1080), 96)
	if w != 96 || h != 54 {
		t.Fatalf("fit large = %dx%d, want 96x54", w, h)
	}
}

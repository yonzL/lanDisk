package thumbs

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestDiskCachePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	key := Key{Path: "a.png", ModUnix: 1, Size: 10, Px: 96}
	calls := 0
	render := func() ([]byte, string, error) {
		calls++
		return []byte("thumb-bytes"), "image/jpeg", nil
	}

	first, err := NewDisk(dir)
	if err != nil {
		t.Fatalf("new disk: %v", err)
	}
	if _, _, err := first.GetOrRender(t.Context(), key, render); err != nil {
		t.Fatalf("render: %v", err)
	}
	if calls != 1 {
		t.Fatalf("render called %d times, want 1", calls)
	}

	// A new instance (a service restart) reads the same file instead of decoding.
	second, err := NewDisk(dir)
	if err != nil {
		t.Fatalf("new disk: %v", err)
	}
	data, mime, err := second.GetOrRender(t.Context(), key, render)
	if err != nil {
		t.Fatalf("cached render: %v", err)
	}
	if calls != 1 {
		t.Fatalf("render called %d times after restart, want 1 (disk hit)", calls)
	}
	if string(data) != "thumb-bytes" || mime != "image/jpeg" {
		t.Fatalf("cached bytes = %q (%s)", data, mime)
	}

	// A different file version is a different key and renders again.
	key.ModUnix = 2
	if _, _, err := second.GetOrRender(t.Context(), key, render); err != nil {
		t.Fatalf("render v2: %v", err)
	}
	if calls != 2 {
		t.Fatalf("render called %d times, want 2 after the file changed", calls)
	}
}

func TestDiskCachePrunesExpiredEntries(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewDisk(dir)
	if err != nil {
		t.Fatalf("new disk: %v", err)
	}
	stale := filepath.Join(dir, "stale.jpg")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * DiskMaxAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	cache.prune()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("expired cache entry survived the prune")
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

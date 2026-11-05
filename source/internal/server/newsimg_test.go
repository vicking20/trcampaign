package server

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func pngOf(t *testing.T, w, h int, c color.Color, gray bool) []byte {
	t.Helper()
	var img image.Image
	if gray {
		g := image.NewGray(image.Rect(0, 0, w, h))
		for i := range g.Pix {
			g.Pix[i] = 0x80
		}
		img = g
	} else {
		r := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r.Set(x, y, c)
			}
		}
		img = r
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// assertGameJPEG: what setJPGTextureData can take -- a JPEG (FF D8), baseline
// (SOF0), 3 components.
func assertGameJPEG(t *testing.T, b []byte) image.Config {
	t.Helper()
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		t.Fatalf("not a JPEG: % x", b[:min(len(b), 8)])
	}
	i := bytes.Index(b, []byte{0xFF, 0xC0})
	if i < 0 || i+9 >= len(b) {
		t.Fatal("no baseline SOF0 marker")
	}
	if comps := b[i+9]; comps != 3 {
		t.Fatalf("JPEG has %d components, the game expects 3", comps)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNewsBannersAreAlwaysJPEG(t *testing.T) {
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	s := New(cfg, &fakeStore{}, log.New(&bytes.Buffer{}, "", 0))
	dir := s.newsImageDir()
	os.MkdirAll(dir, 0755)
	get := func(p string) (int, []byte, string) {
		rec := httptest.NewRecorder()
		if !s.serveNewsImage(rec, httptest.NewRequest("GET", p, nil)) {
			return 404, nil, ""
		}
		b, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, b, rec.Header().Get("Content-Type")
	}

	// nothing on disk: "/" still answers a valid built-in JPEG
	c, b, ct := get("/")
	if c != 200 || ct != "image/jpeg" {
		t.Fatal(c, ct)
	}
	assertGameJPEG(t, b)

	// the shipped default.png (and any PNG) goes out converted
	os.WriteFile(filepath.Join(dir, "default.png"), pngOf(t, 512, 256, color.RGBA{200, 10, 10, 255}, false), 0644)
	os.WriteFile(filepath.Join(dir, "gray.png"), pngOf(t, 64, 32, nil, true), 0644)
	os.WriteFile(filepath.Join(dir, "huge.png"), pngOf(t, 4000, 1000, color.RGBA{0, 0, 255, 128}, false), 0644)
	os.WriteFile(filepath.Join(dir, "broken.jpg"), []byte("not an image"), 0644)

	c, b, _ = get("/")
	if conf := assertGameJPEG(t, b); c != 200 || conf.Width != 512 || conf.Height != 256 {
		t.Fatal(c, conf)
	}
	if _, b, _ = get("/news/gray.png"); true {
		assertGameJPEG(t, b) // grayscale source still 3 components
	}
	_, b, _ = get("/news/huge.png")
	if conf := assertGameJPEG(t, b); conf.Width != 1024 || conf.Height != 256 {
		t.Fatal("not scaled:", conf)
	}
	// unreadable or missing file -> the default banner, never raw bytes
	for _, p := range []string{"/news/broken.jpg", "/news/missing.png"} {
		if c, b, _ := get(p); c != 200 {
			t.Fatal(p, c)
		} else if conf := assertGameJPEG(t, b); conf.Width != 512 {
			t.Fatal(p, conf)
		}
	}
	// not banners
	for _, bad := range []string{"/news/../news.json", "/news/x.txt", "/news/a.dds", "/news/a.swf"} {
		if c, _, _ := get(bad); c != 404 {
			t.Fatal(bad, c)
		}
	}
	// the name in any case, under a web base path, or saved with a doubled or
	// another extension
	os.WriteFile(filepath.Join(dir, "nfs.jpg"), pngOf(t, 300, 100, color.RGBA{0, 200, 0, 255}, false), 0644)
	os.WriteFile(filepath.Join(dir, "Promo.jpg.jpg"), pngOf(t, 200, 100, color.RGBA{0, 200, 0, 255}, false), 0644)
	os.WriteFile(filepath.Join(dir, "logo.png"), pngOf(t, 100, 100, color.RGBA{0, 200, 0, 255}, false), 0644)
	for p, want := range map[string]int{
		"/news/nfs.jpg": 300, "/News/NFS.jpg": 300, "/NFS12_A_01/Game/pc/news/nfs.jpg": 300,
		"/news/promo.jpg": 200, "/news/logo.jpg": 100,
	} {
		if c, b, _ := get(p); c != 200 {
			t.Fatal(p, c)
		} else if conf := assertGameJPEG(t, b); conf.Width != want {
			t.Fatalf("%s: width %d, want %d", p, conf.Width, want)
		}
	}
	// a WebP renamed to .jpg: the log says what it is
	os.WriteFile(filepath.Join(dir, "web.jpg"), append([]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), make([]byte, 16)...), 0644)
	var logs bytes.Buffer
	s.log = log.New(&logs, "", 0)
	get("/news/web.jpg")
	if !bytes.Contains(logs.Bytes(), []byte("WebP")) {
		t.Fatalf("no WebP hint: %s", logs.String())
	}
	// default unreadable and requested file unreadable -> 404 (no picture)
	os.WriteFile(filepath.Join(dir, "default.png"), []byte("junk"), 0644)
	if c, _, _ := get("/news/broken.jpg"); c != 404 {
		t.Fatal("expected 404 when nothing decodes, got", c)
	}
}

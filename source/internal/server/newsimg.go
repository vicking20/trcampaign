package server

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // decoders for banners dropped in data\news\
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// News banners.
//
// The news screen downloads "https://" + V8ServerName + NewsIcon ("/" when
// NewsIcon is empty; newsIconPath adds the leading "/") and feeds the bytes
// straight to libjpeg, whose error handler calls exit(1) on anything else:
// the game only takes JPEG. A
// 1-component JPEG would be read as RGB garbage; any size works.
//
// Every banner is therefore sent as a baseline 3-component JPEG: files in
// data\news\ (.jpg .jpeg .png .gif) are decoded and re-encoded. A file that
// cannot be decoded is never sent (404).
//
//	data\news\banner1.jpg   ->  NewsIcon "news/banner1.jpg" (case does not matter)
//	data\news\default.jpg   ->  "/" (items with NewsIcon ""); default.png also works

var newsImageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true}

// newsBannerMax bounds the re-encoded image (memory on the game side).
const newsBannerMaxW, newsBannerMaxH = 1024, 1024

func (s *Server) newsImageDir() string {
	return filepath.Join(filepath.Dir(s.cfg.LockerDir), "news")
}

// serveNewsImage answers ".../news/<file>" (any case, any prefix) and "/"
// (default banner); false = not a banner.
func (s *Server) serveNewsImage(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	defaults := []string{"default.jpg", "default.jpeg", "default.png", "default.gif"}
	var names []string
	want := ""
	switch i := strings.LastIndex(strings.ToLower(p), "/news/"); {
	case p == "/" || p == "":
		names = defaults
	case i >= 0:
		want = p[i+len("/news/"):]
		if want == "" || strings.ContainsAny(want, `\/:`) || strings.Contains(want, "..") {
			return false
		}
		if !newsImageExts[strings.ToLower(filepath.Ext(want))] {
			return false
		}
		names = append([]string{s.findNewsImage(want)}, defaults...)
	default:
		return false
	}
	for i, name := range names {
		if name == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.newsImageDir(), name))
		if err != nil {
			continue
		}
		b, err := bannerJPEG(raw)
		if err != nil {
			s.log.Printf("[WEB] news banner %s cannot be read: %v%s", name, err, imageKindHint(raw))
			continue
		}
		if i > 0 && want != "" {
			s.log.Printf("[WEB] news banner %q: no usable image in %s, sending %s", want, s.newsImageDir(), name)
		}
		writeJPEG(w, b)
		s.log.Printf("[WEB] news banner %s -> JPEG %d bytes", name, len(b))
		return true
	}
	if want == "" {
		// nothing in data\news\: a plain built-in banner (valid JPEG)
		b := builtinNewsBanner()
		writeJPEG(w, b)
		s.log.Printf("[WEB] news banner (built-in) -> JPEG %d bytes", len(b))
		return true
	}
	s.log.Printf("[WEB] news banner %q: no usable image in %s (404)", want, s.newsImageDir())
	return false
}

// findNewsImage returns the file in data\news\ for a NewsIcon name: the same
// name in any case, else the same name with another image extension or a
// doubled one ("nfs.jpg" -> "NFS.JPG", "nfs.png", "nfs.jpg.jpg").
func (s *Server) findNewsImage(want string) string {
	ents, err := os.ReadDir(s.newsImageDir())
	if err != nil {
		return want
	}
	stem := func(n string) string {
		n = strings.ToLower(n)
		for newsImageExts[filepath.Ext(n)] {
			n = strings.TrimSuffix(n, filepath.Ext(n))
		}
		return n
	}
	alt := ""
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !newsImageExts[strings.ToLower(filepath.Ext(n))] {
			continue
		}
		if strings.EqualFold(n, want) {
			return n
		}
		if alt == "" && stem(n) == stem(want) {
			alt = n
		}
	}
	if alt != "" {
		s.log.Printf("[WEB] news banner %q: using %s", want, alt)
		return alt
	}
	return want
}

// imageKindHint names formats Go cannot decode, so the log says why a file
// renamed to .jpg is refused.
func imageKindHint(b []byte) string {
	switch {
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return " (the file is WebP, not JPEG: save it again as JPEG or PNG)"
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		return " (the file is AVIF/HEIC, not JPEG: save it again as JPEG or PNG)"
	case len(b) >= 2 && string(b[0:2]) == "BM":
		return " (the file is BMP: save it again as JPEG or PNG)"
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xD8:
		return " (a JPEG variant Go cannot read: save it again as a normal JPEG or PNG)"
	}
	return ""
}

func writeJPEG(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", itoa(uint64(len(b))))
	_, _ = w.Write(b)
}

// bannerJPEG decodes any supported image and re-encodes it as a baseline
// RGB JPEG the game's libjpeg path accepts.
func bannerJPEG(raw []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return encodeBannerJPEG(src)
}

func encodeBannerJPEG(src image.Image) ([]byte, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, image.ErrFormat
	}
	// fit inside the maximum, keeping the aspect ratio
	dw, dh := w, h
	if dw > newsBannerMaxW {
		dh = dh * newsBannerMaxW / dw
		dw = newsBannerMaxW
	}
	if dh > newsBannerMaxH {
		dw = dw * newsBannerMaxH / dh
		dh = newsBannerMaxH
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	// Always an RGBA canvas: image/jpeg writes 1 component for *image.Gray,
	// which the game would misread. Transparent pixels go on black.
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if dw == w && dh == h {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	} else {
		for y := 0; y < dh; y++ {
			sy := b.Min.Y + y*h/dh
			for x := 0; x < dw; x++ {
				sx := b.Min.X + x*w/dw
				// color.RGBA is alpha-premultiplied: R,G,B are already "over black"
				c := color.RGBAModel.Convert(src.At(sx, sy)).(color.RGBA)
				dst.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	bannerOnce sync.Once
	bannerJPG  []byte
)

// builtinNewsBanner: a plain dark 512x256 JPEG.
func builtinNewsBanner() []byte {
	bannerOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, 512, 256))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 20, G: 24, B: 32, A: 255}), image.Point{}, draw.Src)
		bannerJPG, _ = encodeBannerJPEG(img)
	})
	return bannerJPG
}

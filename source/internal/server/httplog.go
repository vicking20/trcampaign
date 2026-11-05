package server

import (
	"bytes"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// HTTP request logging. Every request is logged; unknown routes answer 404
// JSON and are logged as TODO.

const httpLogBodyMax = 4096

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }
func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush keeps streaming responses (/nfsrun/stream) working through the logger.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func redactHeader(name, value string) string {
	switch strings.ToLower(name) {
	case "x-session-key", "x-nfsrun-session", "x-nfsrun-device", "authorization", "cookie":
		if len(value) > 6 {
			return value[:6] + "...<redacted>"
		}
		return "<redacted>"
	}
	return value
}

func (s *Server) logHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		names := make([]string, 0, len(r.Header))
		for k := range r.Header {
			names = append(names, k)
		}
		sort.Strings(names)
		var hdr strings.Builder
		for _, k := range names {
			hdr.WriteString(" | " + k + ": " + redactHeader(k, strings.Join(r.Header[k], ",")))
		}
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		took := time.Since(start)
		shown := body
		if len(shown) > httpLogBodyMax {
			shown = shown[:httpLogBodyMax]
		}
		s.log.Printf("[HTTP] %s %s %s host=%q from=%s -> %d (%d bytes, %dms)%s", r.Method, r.URL.RequestURI(), r.Proto, r.Host, r.RemoteAddr, rec.status, rec.bytes, took.Milliseconds(), hdr.String())
		// Launcher auth bodies may contain a one-time invite code and a live
		// session ticket. Log only the size, never the credential material.
		sensitiveBody := strings.HasPrefix(r.URL.Path, "/nfsrun/auth/")
		if len(body) > 0 && sensitiveBody {
			s.log.Printf("[HTTP] body (%d bytes): <redacted launcher auth body>", len(body))
		} else if len(body) > 0 {
			s.log.Printf("[HTTP] body (%d bytes): %s", len(body), printable(shown))
		}
	})
}

func (s *Server) httpNotFound(w http.ResponseWriter, r *http.Request) {
	// Web Autolog: the game builds "/%s/Game/%s" + route (0x0247C8D0), e.g.
	// /NFS12_A_01/Game/pc/Persona/1000001.
	if webRoute(r.URL.Path) != "" {
		s.handleWebHTTP(w, r)
		return
	}
	s.log.Printf("[HTTP] TODO unhandled route %s %s", r.Method, r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, "{\"error\":\"not_implemented\"}\n")
}

// printable: bytes for the log (NUL as " | ", other control bytes as '.').
func printable(b []byte) string {
	var sb strings.Builder
	for _, ch := range b {
		switch {
		case ch == 0:
			sb.WriteString(" | ")
		case ch == '\r' || ch == '\n' || ch == '\t':
			sb.WriteByte(' ')
		case ch < 0x20 || ch > 0x7e:
			sb.WriteByte('.')
		default:
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}

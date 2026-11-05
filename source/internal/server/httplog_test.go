package server

import (
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPLogging(t *testing.T) {
	var logs syncBuf
	s := New(testConfig(), &fakeStore{}, log.New(&logs, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.httpNotFound)
	h := s.logHTTP(mux)
	req := httptest.NewRequest("POST", "http://autolog.example/Other/getStories?x=1", strings.NewReader(`{"a":1}`))
	req.Header.Set("X-SESSION-KEY", "abcdefghijk")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	out := logs.String()
	if rr.Code != 404 || !strings.Contains(out, "POST /Other/getStories?x=1") || !strings.Contains(out, `host="autolog.example"`) ||
		!strings.Contains(out, `{"a":1}`) || strings.Contains(out, "abcdefghijk") || !strings.Contains(out, "TODO unhandled route") {
		t.Fatalf("log:\n%s", out)
	}
}

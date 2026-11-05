package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nfsrun-server/internal/database"
)

func TestPasswordLoginFlow(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	var logs syncBuf
	s := New(testConfig(), db, log.New(&logs, "", 0))
	r := s.Command("adduser Racer", false)
	var pw string
	for _, l := range r.Lines {
		if v, ok := strings.CutPrefix(l, "password : "); ok {
			pw = v
		}
	}
	if pw == "" {
		t.Fatalf("no password in %q", r.Lines)
	}
	post := func(name, pass string) (int, map[string]any) {
		body, _ := json.Marshal(map[string]string{"name": name, "password": pass})
		req := httptest.NewRequest(http.MethodPost, "/nfsrun/auth/password", strings.NewReader(string(body)))
		req.RemoteAddr = "203.0.113.9:5555"
		w := httptest.NewRecorder()
		s.handlePasswordLoginHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, out := post("racer", pw)
	if code != 200 || !isGameCode(out["code"].(string)) {
		t.Fatalf("login: %d %v", code, out)
	}
	state := &connState{}
	if per, err := s.loginWithGameCode(state, out["code"].(string), "test"); err != nil || per.Name != "Racer" {
		t.Fatalf("blaze login with the code: %v %v", per, err)
	}
	if _, err := s.loginWithGameCode(&connState{}, out["code"].(string), "test"); err == nil {
		t.Fatal("a code must work once")
	}
	for i := 0; i < pwFailMax; i++ {
		if c, _ := post("Racer", "wrong-pass-"+strings.Repeat("x", i)); c != 401 {
			t.Fatalf("wrong password %d -> %d", i, c)
		}
	}
	if c, _ := post("Racer", pw); c != 429 {
		t.Fatalf("after %d failures the address must be refused, got %d", pwFailMax, c)
	}
}

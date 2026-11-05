package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
)

func launcherSessionForTest(t *testing.T, s *Server, name string) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := authChallengeForTest(t, s, name, hex.EncodeToString(pub))
	w := authSessionForTest(t, s, c, priv, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("auth status=%d body=%s", w.Code, w.Body.String())
	}
	var id serverIdentityForTest
	_ = json.Unmarshal(w.Body.Bytes(), &id)
	return id.SessionKey
}

func gameCodeForTest(s *Server, session string) (string, int) {
	req := httptest.NewRequest(http.MethodPost, "/nfsrun/auth/gamecode", nil)
	req.RemoteAddr = "203.0.113.20:50000"
	if session != "" {
		req.Header.Set("X-NFSRun-Session", session)
	}
	w := httptest.NewRecorder()
	s.handleGameCodeHTTP(w, req)
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Code, w.Code
}

func blazeLoginForTest(s *Server, tag, value string) (*blaze.Packet, *connState) {
	var w blaze.Writer
	w.TagString(tag, value)
	w.TagVar("TYPE", 1)
	fs, _ := blaze.DecodeAll(w.Bytes())
	cs := &connState{peer: "203.0.113.20:51000"}
	cmd := uint16(0x0028)
	if tag == "AUTH" {
		cmd = 0x0098
	}
	return s.handleAuth(&blaze.Packet{Component: 1, Command: cmd}, fs, cs), cs
}

func TestGameCodeLoginIsSingleUse(t *testing.T) {
	cfg := testConfig()
	cfg.FriendsAll = false
	st := &v3AuthTestStore{}
	s := New(cfg, st, log.New(io.Discard, "", 0))
	ticket := launcherSessionForTest(t, s, "RacerOne")

	if _, status := gameCodeForTest(s, ""); status != http.StatusUnauthorized {
		t.Fatalf("game code without session: status %d", status)
	}
	if _, status := gameCodeForTest(s, strings.Repeat("0", 48)); status != http.StatusUnauthorized {
		t.Fatalf("game code with unknown session: status %d", status)
	}

	for _, tag := range []string{"TOKN", "AUTH"} {
		code, status := gameCodeForTest(s, ticket)
		if status != http.StatusOK || !isGameCode(code) {
			t.Fatalf("game code: status %d code %q", status, code)
		}
		resp, cs := blazeLoginForTest(s, tag, code)
		if resp.Error != 0 || !cs.Authenticated || cs.Persona.Name != "RacerOne" {
			t.Fatalf("%s login: err=%d persona=%+v", tag, resp.Error, cs.Persona)
		}
		if cs.Persona.SessionKey == "" || cs.Persona.SessionKey == ticket {
			t.Fatalf("%s login: Blaze session key %q must differ from the launcher ticket", tag, cs.Persona.SessionKey)
		}
		if strings.Contains(string(resp.Payload), ticket) {
			t.Fatalf("%s login reply contains the launcher ticket", tag)
		}
		// the same code a second time
		if resp, cs := blazeLoginForTest(s, tag, code); resp.Error == 0 || cs.Authenticated {
			t.Fatalf("%s: game code accepted twice", tag)
		}
	}
}

func TestGameCodeRegistryLimits(t *testing.T) {
	var g gameCodeRegistry
	var codes []string
	for i := 0; i < gameCodePerPersonaMax+3; i++ {
		c, err := g.issue("k", 7)
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, c)
	}
	if len(g.by) != gameCodePerPersonaMax {
		t.Fatalf("%d outstanding codes for one persona, want %d", len(g.by), gameCodePerPersonaMax)
	}
	if _, ok := g.consume(codes[len(codes)-1]); !ok {
		t.Fatal("newest code not accepted")
	}
	if isGameCode("nfsrun-g1.xyz") || isGameCode("nfsrun-v3."+strings.Repeat("a", 48)) {
		t.Fatal("bad code format accepted")
	}
}

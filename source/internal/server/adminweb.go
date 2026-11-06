package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed adminweb.html
var adminWebPage []byte

const adminSessionHours = 24 * 30 // logins last a month and survive a restart, so a long password is typed rarely

// adminWeb: the owner's web page (ADMIN_WEB_LISTEN). It runs the same commands as the console, behind
// one username and password from the config, so it can be opened from a phone on the local network.
type adminWeb struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	limit    pwLimiter
	path     string
	pwTag    string // a hash of the login: changing the password ends every saved session
}

func (a *adminWeb) newSession() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]time.Time{}
	}
	now := time.Now()
	for k, exp := range a.sessions {
		if now.After(exp) {
			delete(a.sessions, k)
		}
	}
	a.sessions[tok] = now.Add(adminSessionHours * time.Hour)
	a.saveLocked()
	return tok
}

// the sessions live in a file next to the other config (tokens only; they stop at their expiry or when the password changes)
func (a *adminWeb) saveLocked() {
	if a.path == "" {
		return
	}
	b, _ := json.Marshal(map[string]any{"pw": a.pwTag, "sessions": a.sessions})
	_ = os.MkdirAll(filepath.Dir(a.path), 0o755)
	_ = os.WriteFile(a.path, b, 0o600)
}

func (a *adminWeb) load(path, pwTag string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.path, a.pwTag = path, pwTag
	var f struct {
		Pw       string               `json:"pw"`
		Sessions map[string]time.Time `json:"sessions"`
	}
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &f) == nil && f.Pw == pwTag {
		a.sessions = f.Sessions
	}
}

func (a *adminWeb) valid(r *http.Request) bool {
	c, err := r.Cookie("nfsadmin")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[c.Value]
	return ok && time.Now().Before(exp)
}

func (a *adminWeb) end(r *http.Request) {
	if c, err := r.Cookie("nfsadmin"); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.saveLocked()
		a.mu.Unlock()
	}
}

func sameSecret(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// startAdminWeb serves the page until ctx ends. Off when ADMIN_WEB_LISTEN or the password is empty.
func (s *Server) startAdminWeb(ctx context.Context) {
	c := s.cfg
	if c.AdminWebListen == "" {
		return
	}
	if c.AdminWebUser == "" || c.AdminWebPassword == "" {
		s.log.Printf("[ADMINWEB] not started: set ADMIN_WEB_USER and ADMIN_WEB_PASSWORD")
		return
	}
	tag := sha256.Sum256([]byte(c.AdminWebUser + "\x00" + c.AdminWebPassword))
	s.adminWeb.load(filepath.Join(filepath.Dir(c.BansFile), "admin_sessions.json"), hex.EncodeToString(tag[:8]))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(adminWebPage)
	})
	mux.HandleFunc("/api/login", s.adminLogin)
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		s.adminWeb.end(r)
		http.SetCookie(w, &http.Cookie{Name: "nfsadmin", Value: "", Path: "/", MaxAge: -1})
		adminJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/state", s.adminAuth(func(w http.ResponseWriter, r *http.Request) {
		adminJSON(w, 200, s.adminState())
	}))
	mux.HandleFunc("/api/cmd", s.adminAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Line      string `json:"line"`
			Confirmed bool   `json:"confirmed"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			adminJSON(w, 400, map[string]any{"error": "bad request"})
			return
		}
		res := s.Command(req.Line, req.Confirmed)
		adminJSON(w, 200, map[string]any{"lines": res.Lines, "confirm": res.Confirm})
	}))
	srv := &http.Server{Addr: c.AdminWebListen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.log.Printf("[ADMINWEB] listening on %s (user %q)", c.AdminWebListen, c.AdminWebUser)
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			s.log.Printf("[ADMINWEB] %v", e)
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
}

// adminAuth wraps a handler: a valid session cookie, and for POST the custom header a cross-site form cannot send.
func (s *Server) adminAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.adminWeb.valid(r) {
			adminJSON(w, 401, map[string]any{"error": "login"})
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-Admin") != "1" {
			adminJSON(w, 403, map[string]any{"error": "header"})
			return
		}
		h(w, r)
	}
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ User, Password string }
	if r.Method != http.MethodPost || r.Header.Get("X-Admin") != "1" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req) != nil {
		adminJSON(w, 400, map[string]any{"error": "bad request"})
		return
	}
	key := "ip:" + hostOf(r.RemoteAddr)
	if s.adminWeb.limit.blocked(key) {
		adminJSON(w, 429, map[string]any{"error": "too many failed logins, try again later"})
		return
	}
	userOK := sameSecret(req.User, s.cfg.AdminWebUser)
	passOK := sameSecret(req.Password, s.cfg.AdminWebPassword)
	if !userOK || !passOK {
		s.adminWeb.limit.failed(key)
		s.log.Printf("[ADMINWEB] failed login from %s", hostOf(r.RemoteAddr))
		adminJSON(w, 401, map[string]any{"error": "wrong user or password"})
		return
	}
	s.adminWeb.limit.ok(key)
	s.log.Printf("[ADMINWEB] login from %s", hostOf(r.RemoteAddr))
	http.SetCookie(w, &http.Cookie{Name: "nfsadmin", Value: s.adminWeb.newSession(), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: adminSessionHours * 3600})
	adminJSON(w, 200, map[string]any{"ok": true})
}

func adminJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type adminPlayer struct {
	Name  string `json:"name"`
	Where string `json:"where"`
	Addr  string `json:"addr"`
}

type adminServer struct {
	N     int    `json:"n"`
	Port  int    `json:"port"`
	State string `json:"state"`
	Pid   int    `json:"pid"`
	Up    string `json:"up"`
	Why   string `json:"why"`
}

func (s *Server) adminState() map[string]any {
	players := []adminPlayer{}
	for _, st := range s.online.states() {
		if st.Persona.Name == "" || isDSPersonaName(st.Persona.Name) || isDedicatedServerAccount(st.Email) {
			continue
		}
		where := "menus"
		s.gm.mu.Lock()
		if g := st.game; g != nil && s.gm.games[g.id] == g {
			where = "game " + itoa(uint64(g.id)) + " (" + gameStateName(g.gstate) + ")"
		} else if st.mm != nil && !st.mm.done {
			where = "matchmaking"
		}
		s.gm.mu.Unlock()
		players = append(players, adminPlayer{st.Persona.Name, where, remoteIP(st.peer)})
	}
	sort.Slice(players, func(i, j int) bool { return strings.ToLower(players[i].Name) < strings.ToLower(players[j].Name) })
	servers := []adminServer{}
	s.ds.mu.Lock()
	for _, i := range s.ds.insts {
		up := ""
		if i.state == "running" && !i.started.IsZero() {
			up = time.Since(i.started).Round(time.Second).String()
		}
		servers = append(servers, adminServer{i.n, i.port, i.state, i.pid, up, i.why})
	}
	s.ds.mu.Unlock()
	type account struct {
		Name   string `json:"name"`
		Last   string `json:"last"`
		Online bool   `json:"online"`
	}
	accounts := []account{}
	if db, ok := s.db.(adminStore); ok {
		online := map[string]bool{}
		for _, st := range s.online.states() {
			online[strings.ToLower(st.Persona.Name)] = true
		}
		if list, err := db.ListPersonaInfo(500); err == nil {
			for _, p := range list {
				if !isDSPersonaName(p.Name) {
					accounts = append(accounts, account{p.Name, p.LastLogin, online[strings.ToLower(p.Name)]})
				}
			}
		}
	}
	bans := s.banned()
	if bans == nil {
		bans = []string{}
	}
	return map[string]any{
		"players":  players,
		"servers":  servers,
		"games":    s.cmdGames().Lines,
		"bans":     bans,
		"traffic":  s.raceSettings(),
		"accounts": accounts,
		"cars":     s.carList(),
		"known":    func() []knownCar { l, _ := s.knownCars(); return l }(),
		"news":     s.newsList(),
		"says":     saysPending(),
		"scoring":  s.scoringState(),
		"modes":    s.allModes(),
		"mode":     s.raceSettings().Mode,
	}
}

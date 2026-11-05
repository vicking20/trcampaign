// nfsrun-launcher is the player launcher for the NFS The Run private server.
//
// The game's Origin SDK needs a local Origin client; the launcher is one: a
// local LSX endpoint on 127.0.0.1:3216 (internal/lsx) and Origin-style
// environment variables for the game. It logs into the server with a local
// Ed25519 key, hands the game one-time codes and runs a copy of the game exe
// patched for the server (multi.go).
//
// LSX handshake/crypto: Go port of EAappEmulater (MIT), see internal/lsx.
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/lsx"
	"nfsrun-server/internal/originid"
	"nfsrun-server/internal/playlistpatch"
)

func clientKeyForInstance(base string, instance int) string {
	base = strings.ToLower(strings.TrimSpace(base))
	if instance <= 1 {
		return base
	}
	sum := sha256.Sum256([]byte(base + "/instance/" + strconv.Itoa(instance)))
	return hex.EncodeToString(sum[:])
}

func launcherKeyPair(seedHex string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	seedHex = strings.ToLower(strings.TrimSpace(seedHex))
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, nil, fmt.Errorf("invalid CLIENT_KEY seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := make([]byte, ed25519.PublicKeySize)
	copy(pub, priv[ed25519.SeedSize:])
	return ed25519.PublicKey(pub), priv, nil
}

func normalizeClaimForSignature(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	return strings.ReplaceAll(v, "-", "")
}

func launcherAuthMessage(challengeID, nonce, persona, publicKey, claimCode, currentSession string) []byte {
	return []byte(strings.Join([]string{
		"nfsrun-auth-v3",
		strings.ToLower(strings.TrimSpace(challengeID)),
		strings.ToLower(strings.TrimSpace(nonce)),
		strings.TrimSpace(persona),
		strings.ToLower(strings.TrimSpace(publicKey)),
		normalizeClaimForSignature(claimCode),
		strings.ToLower(strings.TrimSpace(currentSession)),
	}, "\n"))
}

func defaultPersona() string {
	h := os.Getenv("COMPUTERNAME")
	if h == "" {
		h, _ = os.Hostname()
	}
	if h == "" {
		h = "Player"
	}
	if n := strings.ToUpper(h) + ".0"; len(n) <= maxPersonaName {
		return n
	}
	return strings.ToUpper(h) // COMPUTERNAME.0 can exceed 16 characters
}

func stableID(persona string) uint64 { return originid.UserID(persona) }

func newSessionKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// setOriginClientPath registers the launcher as Origin (HKLM
// SOFTWARE\Origin\ClientPath, 32-bit view). Only the original game exe
// needs it: its Origin SDK checks that Origin is installed and starts
// ClientPath. The launcher's game copy skips both checks.
func setOriginClientPath(l *log.Logger, self string) {
	if runtime.GOOS != "windows" {
		return
	}
	if err := regSetString(true, `SOFTWARE\Origin`, "ClientPath", self); err != nil {
		l.Printf(`[REG] HKLM\SOFTWARE\Origin\ClientPath not written (%v): the original game exe needs it. Run the launcher once as administrator, or use NO_ORIGIN_START=1.`, err)
		return
	}
	l.Printf(`[REG] HKLM\SOFTWARE\Origin\ClientPath = %s`, self)
}

// lsxBinding: the LSX port this launcher serves.
type lsxBinding struct {
	ln           net.Listener
	instance     int
	addr         string
	foreignPorts []string // ports held by the EA app / Origin
}

// bindLSX binds the LSX port. If another launcher holds it, the next free
// port is used and the game runs from a copy patched for that port
// (push 0xC90 at 0x00838BA6).
func bindLSX(listen string, sdkStarted, lsxOnly bool) (lsxBinding, error) {
	b := lsxBinding{instance: 1, addr: listen}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		host, portS, _ := net.SplitHostPort(listen)
		base, _ := strconv.Atoi(portS)
		if !sdkStarted && !lsxOnly && base > 0 {
			// Only ports held by another launcher make this a second game copy (own
			// persona and key); a port held by the EA app does not.
			ours := 0
			if o := portOwner(base); o != "" && !isLauncherImage(o) {
				b.foreignPorts = append(b.foreignPorts, fmt.Sprintf("%d (%s)", base, o))
			} else {
				ours++
			}
			for p := base + 1; p <= base+15 && err != nil; p++ {
				addr := net.JoinHostPort(host, strconv.Itoa(p))
				if ln, err = net.Listen("tcp", addr); err == nil {
					b.instance, b.addr = ours+1, addr
				} else if o := portOwner(p); o != "" && !isLauncherImage(o) {
					b.foreignPorts = append(b.foreignPorts, fmt.Sprintf("%d (%s)", p, o))
				} else {
					ours++
				}
			}
		}
	}
	b.ln = ln
	return b, err
}

type launchOptions struct {
	cfg                     config
	cfgErr                  error
	dir, self               string
	lsx                     lsxBinding
	lsxOnly, sdkStarted     bool
	personaFlag, inviteFlag string
}

func main() {
	lsxOnly := flag.Bool("lsx-only", false, "only run the fake Origin (LSX); start the game yourself")
	personaFlag := flag.String("persona", "", "player name for this copy of the game (overrides PLAYER_NAME)")
	inviteFlag := flag.String("invite-code", "", "one-time invite code for this player name")
	consoleFlag := flag.Bool("console", false, "text console instead of the window")
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	// The Origin SDK starts ClientPath with its own arguments when it cannot find
	// Origin; unknown arguments mark such a start.
	sdkStarted := flag.CommandLine.Parse(os.Args[1:]) != nil || flag.NArg() > 0

	self, _ := os.Executable()
	self, _ = filepath.Abs(self)
	dir := filepath.Dir(self)

	cfg, cfgErr := loadConfig(dir)
	b, err := bindLSX(cfg.LSXListen, sdkStarted, *lsxOnly)
	if err != nil {
		if f, e := os.OpenFile(filepath.Join(dir, "nfsrun-launcher.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); e == nil {
			fmt.Fprintf(f, "%s [second instance] args=%q: %s busy (%v) -- exiting\n", time.Now().Format("2006/01/02 15:04:05.000000"), os.Args[1:], cfg.LSXListen, err)
			f.Close()
		}
		if !sdkStarted {
			setLanguage(cfg.Language)
			showError(T("port_busy", cfg.LSXListen))
		}
		return
	}
	setLanguage(cfg.Language)
	ui := newUI(*consoleFlag)
	ui.Run(func() {
		launch(ui, launchOptions{cfg: cfg, cfgErr: cfgErr, dir: dir, self: self, lsx: b, lsxOnly: *lsxOnly, sdkStarted: sdkStarted, personaFlag: *personaFlag, inviteFlag: *inviteFlag})
	})
	// closing the launcher closes the game (also enforced by the job object)
	killGames()
}

func launch(ui launcherUI, o launchOptions) {
	cfg, dir, b := o.cfg, o.dir, o.lsx
	instance, lsxAddr, ln := b.instance, b.addr, b.ln

	logName := "nfsrun-launcher.log"
	if instance > 1 {
		logName = fmt.Sprintf("nfsrun-launcher-%d.log", instance)
	} else if o.lsxOnly {
		logName = "nfsrun-launcher-lsx.log"
	}
	out := ui.LogWriter()
	if lf, err := os.OpenFile(filepath.Join(dir, logName), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644); err == nil {
		defer lf.Close()
		out = io.MultiWriter(out, lf)
	}
	l := log.New(out, "", log.LstdFlags|log.Lmicroseconds)
	l.Printf("NFS The Run launcher (%s/%s) starting in %s", runtime.GOOS, runtime.GOARCH, dir)
	ui.SetStatus(statusBusy, T("st_preparing"), "")
	local := T("local", lsxAddr)
	if instance > 1 {
		local += T("local_copy", instance)
	}
	ui.SetLocal(local)
	if len(b.foreignPorts) > 0 {
		l.Printf("[ORIGIN] LSX port %s is used by another program (the EA app or Origin serve Origin's LSX there); this launcher uses %s and starts a game copy that connects to it", strings.Join(b.foreignPorts, ", "), lsxAddr)
	}
	if o.cfgErr != nil {
		l.Printf("config: %v", o.cfgErr)
	}
	if b := builtIn(); b.ServerHTTP != "" {
		src := "built into this launcher"
		if cfg.ServerHTTP != b.ServerHTTP || cfg.ServerCertPin != b.Pin {
			src = iniName + " (overrides the built-in " + b.ServerHTTP + ")"
		}
		l.Printf("[ID] server %s from %s", cfg.ServerHTTP, src)
	}
	if err := ensureClientKey(dir, &cfg); err != nil {
		l.Printf("[ID] could not create/load CLIENT_KEY: %v", err)
		ui.Fatal(T("err_key"), err.Error())
		return
	}
	if err := setServerPin(cfg.ServerCertPin); err != nil {
		l.Printf("[ID] SERVER_CERT_PIN: %v", err)
		ui.Fatal(T("err_pin"), "SERVER_CERT_PIN: "+err.Error())
		return
	}
	if cfg.ServerHTTP != "" && !strings.HasPrefix(strings.ToLower(cfg.ServerHTTP), "https://") {
		l.Printf("[ID] WARNING: SERVER_HTTP is not https://: the login ticket travels unencrypted")
	} else if cfg.ServerHTTP != "" && serverPin != "" {
		l.Printf("[ID] server certificate pinned (%s...)", serverPin[:16])
	}
	persona := cfg.Persona
	if persona == "" {
		persona = defaultPersona()
	}
	if o.personaFlag != "" {
		persona = o.personaFlag
	} else if instance > 1 {
		// another player on the same PC; keep within 16 characters
		suffix := fmt.Sprintf("_%d", instance)
		base := []rune(persona)
		if len(base)+len(suffix) > maxPersonaName && len(base) > maxPersonaName-len(suffix) {
			base = base[:maxPersonaName-len(suffix)]
		}
		persona = string(base) + suffix
	}
	ui.SetPlayer(persona)
	if instance > 1 {
		l.Printf("[MULTI] game copy #%d on this PC: LSX %s, persona %q (use -persona Name to choose)", instance, lsxAddr, persona)
	}
	// The game cannot show names longer than 16 characters; the server refuses
	// them too.
	if n := len([]rune(persona)); n > maxPersonaName || n == 0 {
		l.Printf("PLAYER_NAME %q has %d characters: the game supports at most %d.", persona, n, maxPersonaName)
		l.Printf("Set a shorter name in %s (PLAYER_NAME=YourName) or start with -persona YourName.", iniName)
		ui.Fatal(T("err_name", persona, n, maxPersonaName), T("err_name_hint", iniName))
		return
	}
	deviceKey := clientKeyForInstance(cfg.ClientKey, instance)
	id := stableID(persona)
	session := &launcherSessionState{}
	// the window shows the server state, never its address
	if cfg.ServerHTTP == "" {
		ui.SetServer(serverNone)
	} else {
		ui.SetServer(serverConnecting)
		ui.SetStatus(statusBusy, T("st_connecting"), "")
		claimCode := cfg.InviteCode
		if strings.TrimSpace(o.inviteFlag) != "" {
			claimCode = strings.TrimSpace(o.inviteFlag)
		}
		// Prove the local Ed25519 key and get a session ticket; CLIENT_KEY never
		// leaves this PC.
		ident, err := fetchIdentity(cfg.ServerHTTP, persona, deviceKey, claimCode, "")
		if err != nil || ident.BlazeID == 0 || strings.TrimSpace(ident.SessionKey) == "" {
			if err == nil {
				err = fmt.Errorf("server returned an incomplete identity")
			}
			// never fall back to name-only login
			l.Printf("[ID] private-server login failed: %v", err)
			l.Printf("The game was not started. A new name needs INVITE_CODE from the server owner; a lost %s needs a new code (reset-key).", keyName)
			ui.SetServer(serverFailed)
			ui.Fatal(T("err_login"), publicError(err))
			return
		}
		id = ident.BlazeID
		session.set(ident)
		if ident.Name != "" {
			persona = ident.Name
			ui.SetPlayer(persona)
		}
		ui.SetServer(serverOnline)
		if strings.TrimSpace(claimCode) != "" && strings.TrimSpace(o.inviteFlag) == "" {
			if err := clearInviteCode(dir, &cfg); err != nil {
				l.Printf("[AUTH] warning: login succeeded but INVITE_CODE could not be cleared from %s: %v", iniName, err)
			} else {
				l.Printf("[AUTH] invite code used and cleared from %s", iniName)
			}
		}
		l.Printf("[AUTH] launcher session valid until %s", time.Unix(ident.ExpiresAt, 0).Format(time.RFC3339))
		go maintainLauncherSession(l, cfg.ServerHTTP, persona, deviceKey, session)
	}
	// never log the auth code or session key
	l.Printf("player=%q userId=%d online=%t (LSX_LISTEN=%s)", persona, id, session.keyOnly() != "", cfg.LSXListen)

	// game language: LANGUAGE, or the Windows language for auto
	wanted := cfg.Language
	if strings.EqualFold(wanted, "auto") || wanted == "" {
		wanted = systemLocale()
	}
	if code, ok := gameLocale(wanted); ok {
		cfg.Locale = code
	} else {
		l.Printf("[LOCALE] %s: the PC game has no text in this language, so the game stays in English (the launcher window still uses it)", wanted)
		cfg.Locale = code
	}
	ident := lsx.Identity{Persona: persona, UserID: id, PersonaID: id, Locale: cfg.Locale}
	if cfg.ServerHTTP == "" {
		ident.AuthCode = "nfsrun-offline"
	} else {
		// The game gets a one-time code per request, never the session ticket:
		// its own connections to the server are not encrypted.
		ident.AuthCodeFunc = func() string {
			code, err := fetchGameCode(cfg.ServerHTTP, session.keyOnly())
			if err != nil {
				l.Printf("[AUTH] could not get a game code: %v", err)
				return ""
			}
			l.Printf("[AUTH] one-time game code handed to the game")
			return code
		}
	}
	var friends *friendSource
	if cfg.ServerHTTP != "" {
		friends = newFriendSource(cfg.ServerHTTP, persona, session, l)
		friends.invitesMode, friends.ask = cfg.Invites, ui.AskInvite
		friends.onChange = func(list []lsx.Friend) {
			others := make([]lsx.Friend, 0, len(list))
			for _, f := range list {
				if f.UserID != id {
					others = append(others, f)
				}
			}
			ui.SetFriends(others)
			ui.SetServer(serverOnline)
		}
		friends.onDown = func() { ui.SetServer(serverFailed) }
		ident.Friends = friends.list
		ident.OnSetPresence = func(p, sess, rich string) {
			postJSON(l, cfg.ServerHTTP+"/nfsrun/presence", session, map[string]any{"persona": persona, "presence": p, "sessionId": sess, "richPresence": rich})
		}
		ident.OnSendInvite = func(to []uint64, sess string) {
			postJSON(l, cfg.ServerHTTP+"/nfsrun/invite", session, map[string]any{"persona": persona, "from": id, "to": to, "session": sess})
		}
		l.Printf("friends: live channel %s/nfsrun/stream (polling /nfsrun/players when it is down; presence and invites relayed)", cfg.ServerHTTP)
	}
	srv := lsx.NewServer(ident, l)
	if friends != nil {
		go friends.run(srv, id)
	}
	go srv.Serve(ln)
	defer ln.Close()
	l.Printf("[LSX] fake Origin listening on %s", lsxAddr)

	if o.lsxOnly || o.sdkStarted {
		if o.lsxOnly {
			setOriginClientPath(l, o.self)
			l.Printf("lsx-only mode: start the game now. Ctrl+C to stop.")
			ui.SetStatus(statusOK, T("st_lsx"), T("st_lsx_manual"))
		} else {
			l.Printf("started by the game's Origin SDK (args %q): serving LSX only", os.Args[1:])
			ui.SetStatus(statusOK, T("st_lsx"), T("st_lsx_sdk"))
		}
		ui.WaitClose()
		return
	}

	gameExe := cfg.GameExe
	if !filepath.IsAbs(gameExe) {
		gameExe = filepath.Join(dir, gameExe)
	}
	if _, err := os.Stat(gameExe); err != nil {
		l.Printf("game not found: %s", gameExe)
		l.Printf("Put the launcher in the game folder or set GAME_EXE in %s.", iniName)
		ui.Fatal(T("err_notfound"), T("err_notfound_hint", iniName))
		return
	}
	_, port, _ := net.SplitHostPort(lsxAddr)
	p, _ := strconv.Atoi(port)
	// The launcher runs a copy of the game exe patched for this server
	// (multi.go); NO_ORIGIN_START=0 runs the original on port 3216.
	exePort, err := lsxPortOf(gameExe)
	if err != nil {
		l.Printf("[GAME] %s: %v", filepath.Base(gameExe), err)
		ui.Fatal(T("err_prepare"), err.Error())
		return
	}
	usesCopy := exePort != p || cfg.NoOriginStart
	copyName := fmt.Sprintf("NFSTR_lsx%d.exe", p)
	var items []consentItem
	if usesCopy {
		items = append(items, consentItem{"copy", T("cs_copy", copyName)})
	} else if instance == 1 {
		items = append(items, consentItem{"origin", T("cs_origin")})
	}
	items = append(items, consentItem{"language", T("cs_lang")})
	if cfg.ExtraPlaylists {
		items = append(items, consentItem{"playlists", T("cs_playlists")})
	}
	if !ensureConsent(ui, dir, &cfg, items) {
		l.Printf("[GAME] the player did not allow the first-start steps; nothing was changed")
		ui.Fatal(T("consent_no"), "")
		return
	}
	// EXTRA_PLAYLISTS: same playlist patch as the race servers
	if r, err := playlistpatch.Ensure(filepath.Dir(gameExe), cfg.ExtraPlaylists); err != nil {
		l.Printf("[PLAYLISTS] %v (the extra playlists stay unavailable; a game folder under Program Files may need the launcher run as administrator once)", err)
	} else {
		l.Printf("[PLAYLISTS] %s", r.Note)
	}
	if usesCopy {
		cp, notes, err := multiCopy(gameExe, p, cfg.NoOriginStart, cfg.Domain)
		if err != nil {
			l.Printf("[GAME] cannot prepare the game copy: %v", err)
			ui.Fatal(T("err_prepare"), err.Error())
			return
		}
		l.Printf("[GAME] game copy %s: %s", filepath.Base(cp), strings.Join(notes, ", "))
		gameExe = cp
	} else if instance == 1 {
		setOriginClientPath(l, o.self)
	}
	// the game's language comes only from the registry (Locale)
	setGameLocale(l, cfg.Locale, copyReadsHKCU(gameExe))

	gameArgs, dropped := stripTunnelArgs(strings.Fields(cfg.GameArgs))
	if len(dropped) > 0 {
		l.Printf("[GAME] ignoring %q from GAME_ARGS: the race servers and every player must use the same ProtoTunnel setting, so it stays at the game's default", strings.Join(dropped, " "))
	}
	if cfg.SoloOnline {
		var added bool
		if gameArgs, added = withSoloOnline(gameArgs); added {
			l.Printf("[GAME] -NfsOnline.AllowSinglePlayerOnline true (SOLO_ONLINE=1): stay in a playlist when alone")
		}
	}
	ui.SetPlayable(true)
	ui.SetStatus(statusOK, T("st_ready"), "")
	for first := true; ; first = false {
		if !(first && cfg.AutoStart) && !ui.WaitPlay() {
			return
		}
		code := ident.AuthCode
		if ident.AuthCodeFunc != nil {
			code = ident.AuthCodeFunc()
		}
		cmd := exec.Command(gameExe, gameArgs...)
		cmd.Dir = filepath.Dir(gameExe)
		cmd.Env = append(os.Environ(),
			"EALsxPort="+port,
			"EALaunchEAID="+persona,
			"EAAuthCode="+code,
			"EALaunchUserAuthToken="+code,
			"EAGenericAuthToken="+code,
			"OriginSessionKey="+newSessionKey(),
			"EALaunchEnv=production",
			"EAGameLocale="+cfg.Locale,
			"EALaunchOfflineMode=false",
			"EAFreeTrialGame=false",
			"EAEntitlementSource=EA",
			"EAExternalSource=EA",
			"EALaunchOwner=EA",
		)
		l.Printf("starting %s", gameExe)
		start := time.Now()
		if err := cmd.Start(); err != nil {
			l.Printf("could not start the game: %v", err)
			if ui.Console() {
				ui.Fatal(T("err_start"), err.Error())
				return
			}
			ui.SetStatus(statusError, T("err_start"), err.Error())
			continue
		}
		if err := trackGame(cmd.Process.Pid); err != nil {
			l.Printf("[GAME] cannot tie the game to the launcher (it may stay open when the launcher closes): %v", err)
		}
		ui.SetGame(true)
		ui.SetStatus(statusOK, T("st_running"), T("st_running_hint"))
		err := cmd.Wait()
		played := time.Since(start).Round(time.Second)
		l.Printf("game exited after %s (%v)", played, err)
		ui.SetGame(false)
		time.Sleep(time.Second)
		l.Printf("[LSX] requests this session: %s", srv.Summary())
		if cfg.CloseWithGame || ui.Console() {
			return
		}
		ui.SetStatus(statusOK, T("st_ready"), T("st_exited", played.String()))
	}
}

// publicError: a login error for the window, without the server address.
func publicError(err error) string {
	var ue *url.Error
	switch {
	case strings.Contains(err.Error(), "SERVER_CERT_PIN"):
		return T("err_pin")
	case errors.As(err, &ue):
		return T("err_unreachable")
	case strings.Contains(err.Error(), "INVITE_CODE"), strings.Contains(err.Error(), "invite code"):
		return T("err_claim")
	}
	return err.Error()
}

// maxPersonaName: longest player name the game can show (server checks it too).
const maxPersonaName = 16

type launcherSessionState struct {
	mu         sync.RWMutex
	key        string
	expiresAt  int64
	renewAfter int64
}

func (s *launcherSessionState) set(id serverIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = strings.TrimSpace(id.SessionKey)
	s.expiresAt = id.ExpiresAt
	s.renewAfter = id.RenewAfter
}

func (s *launcherSessionState) current() (string, int64, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key, s.expiresAt, s.renewAfter
}

func (s *launcherSessionState) keyOnly() string {
	k, _, _ := s.current()
	return k
}

// friendSource provides the player's friends and presence, kept current by the
// server's live channel (GET /nfsrun/stream) with polling as fallback.
type friendSource struct {
	base    string
	persona string
	session *launcherSessionState
	log     *log.Logger
	mu      sync.Mutex
	at      time.Time
	cached  []lsx.Friend
	invites []serverInvite // delivered by the server, not yet pushed
	live    bool           // stream connected
	// onChange: every new snapshot (window); onDown: the server stopped answering
	onChange func([]lsx.Friend)
	onDown   func()
	// invites: INVITES mode and the accept prompt
	invitesMode string
	ask         func(from string) bool
	asking      sync.Mutex // one prompt at a time
}

type serverInvite struct {
	From    uint64 `json:"from"`
	Name    string `json:"name"`
	Session string `json:"session"`
}

type serverPlayers struct {
	Type    string `json:"type"`
	Players []struct {
		Name      string `json:"name"`
		UserID    uint64 `json:"userId"`
		Online    bool   `json:"online"`
		Presence  string `json:"presence"`
		SessionID string `json:"sessionId"`
	} `json:"players"`
	Invites []serverInvite `json:"invites"`
}

func newFriendSource(base, persona string, session *launcherSessionState, l *log.Logger) *friendSource {
	return &friendSource{base: base, persona: persona, session: session, log: l}
}

// postJSON: fire-and-forget POST to the private server (presence, invites).
func postJSON(l *log.Logger, u string, session *launcherSessionState, body any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", u, bytes.NewReader(b))
	req.Header.Set("User-Agent", "nfsrun-launcher")
	req.Header.Set("Content-Type", "application/json")
	if sessionKey := session.keyOnly(); sessionKey != "" {
		req.Header.Set("X-NFSRun-Session", sessionKey)
	}
	resp, err := serverClient(5 * time.Second).Do(req)
	if err != nil {
		l.Printf("[ORIGIN] %s: %v", u, err)
		return
	}
	resp.Body.Close()
	// Do not log the JSON body: authenticated requests may carry launcher session data.
	l.Printf("[ORIGIN] POST %s -> HTTP %d", u, resp.StatusCode)
}

// deliverInvite plays Origin's part: the player accepts (INVITES=ask) and the
// game gets MultiplayerInvite with the inviter's session (its Blaze id).
func (f *friendSource) deliverInvite(srv *lsx.Server, inv serverInvite) {
	who := inv.Name
	if who == "" {
		who = fmt.Sprint(inv.From)
	}
	switch f.invitesMode {
	case "off":
		f.log.Printf("[ORIGIN] invite from %q ignored (INVITES=off)", who)
		return
	case "ask":
		if f.ask != nil {
			f.asking.Lock()
			ok := f.ask(who)
			f.asking.Unlock()
			if !ok {
				f.log.Printf("[ORIGIN] invite from %q declined", who)
				return
			}
		}
	}
	session := strings.TrimSpace(inv.Session)
	if session == "" || session == "0" {
		session = fmt.Sprint(inv.From)
	}
	n := srv.PushInvite(inv.From, session)
	f.log.Printf("[ORIGIN] invite from %q (%d) accepted, session %q: MultiplayerInvite sent to %d game connection(s)", who, inv.From, session, n)
}

func (f *friendSource) takeInvites() []serverInvite {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.invites
	f.invites = nil
	return out
}

// store takes a server snapshot (stream line or /nfsrun/players body).
func (f *friendSource) store(body serverPlayers) []lsx.Friend {
	out := make([]lsx.Friend, 0, len(body.Players))
	for _, p := range body.Players {
		id := p.UserID
		if id == 0 {
			id = originid.UserID(p.Name)
		}
		out = append(out, lsx.Friend{Persona: p.Name, UserID: id, Online: p.Online, Presence: p.Presence, SessionID: p.SessionID})
	}
	f.mu.Lock()
	if len(out) != len(f.cached) || f.cached == nil {
		f.log.Printf("[FRIENDS] %d player(s) in the list", len(out))
	}
	f.cached, f.at = out, time.Now()
	f.invites = append(f.invites, body.Invites...)
	f.mu.Unlock()
	if f.onChange != nil {
		f.onChange(out)
	}
	return out
}

// poll: one GET /nfsrun/players?persona=X.
func (f *friendSource) poll() ([]lsx.Friend, error) {
	u := f.base + "/nfsrun/players?persona=" + url.QueryEscape(f.persona)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "nfsrun-launcher")
	if sessionKey := f.session.keyOnly(); sessionKey != "" {
		req.Header.Set("X-NFSRun-Session", sessionKey)
	}
	resp, err := serverClient(4 * time.Second).Do(req)
	if err != nil {
		if f.onDown != nil {
			f.onDown()
		}
		return nil, err
	}
	defer resp.Body.Close()
	var body serverPlayers
	if resp.StatusCode != 200 {
		if resp.StatusCode >= 500 && f.onDown != nil {
			f.onDown()
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return f.store(body), nil
}

// list answers QueryFriends: the live list, or a fresh poll when the stream
// is down and the cache is old.
func (f *friendSource) list() []lsx.Friend {
	f.mu.Lock()
	fresh := f.cached != nil && (f.live || time.Since(f.at) < 20*time.Second)
	cached := f.cached
	f.mu.Unlock()
	if fresh {
		return cached
	}
	out, err := f.poll()
	if err != nil {
		f.log.Printf("[FRIENDS] %s/nfsrun/players: %v", f.base, err)
		return cached
	}
	return out
}

// run keeps the live channel open (reconnecting) and turns every change into
// the LSX events this game's Origin SDK listens to:
//
//	friend list changed  -> <QueryFriendsResponse> event   (event type 0x20)
//	presence changed     -> <GetPresenceResponse> event    (event type 0x10)
//	invite               -> <MultiplayerInvite> event
//
// While the stream is down it polls every 5 s.
func (f *friendSource) run(srv *lsx.Server, self uint64) {
	type pstate struct {
		on               bool
		presence, sessid string
	}
	var prev map[uint64]pstate
	apply := func(list []lsx.Friend) {
		cur := map[uint64]pstate{}
		for _, p := range list {
			if p.UserID != self {
				cur[p.UserID] = pstate{p.Online, p.Presence, p.SessionID}
			}
		}
		if prev != nil {
			changedList := len(cur) != len(prev)
			for id := range cur {
				if _, ok := prev[id]; !ok {
					changedList = true
				}
			}
			if changedList {
				f.log.Printf("[FRIENDS] friend list changed (%d -> %d): QueryFriendsResponse event", len(prev), len(cur))
				srv.PushFriends()
			}
			presenceChanged := false
			for _, p := range list {
				was, ok := prev[p.UserID]
				now := cur[p.UserID]
				if p.UserID == self || !ok || was == now {
					continue
				}
				f.log.Printf("[FRIENDS] %s (%d) is now %s session=%s: GetPresenceResponse event", p.Persona, p.UserID, map[bool]string{true: "online", false: "offline"}[now.on], now.sessid)
				srv.PushPresence(p)
				presenceChanged = true
			}
			// The game re-reads its friends after a QueryFriendsResponse event only, so
			// send the list on presence changes too.
			if presenceChanged && !changedList {
				srv.PushFriends()
			}
		}
		prev = cur
		for _, inv := range f.takeInvites() {
			go f.deliverInvite(srv, inv)
		}
	}
	if list, err := f.poll(); err == nil {
		apply(list)
	}
	backoff := 2 * time.Second
	for {
		started := time.Now()
		err := f.stream(apply)
		f.mu.Lock()
		f.live = false
		f.mu.Unlock()
		if time.Since(started) > time.Minute {
			backoff = 2 * time.Second
		}
		f.log.Printf("[STREAM] live channel closed (%v); polling, reconnect in %s", err, backoff)
		deadline := time.Now().Add(backoff)
		for time.Now().Before(deadline) {
			if list, perr := f.poll(); perr == nil {
				apply(list)
			}
			time.Sleep(5 * time.Second)
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// stream reads GET /nfsrun/stream until it ends.
func (f *friendSource) stream(apply func([]lsx.Friend)) error {
	u := f.base + "/nfsrun/stream?persona=" + url.QueryEscape(f.persona)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "nfsrun-launcher")
	if sessionKey := f.session.keyOnly(); sessionKey != "" {
		req.Header.Set("X-NFSRun-Session", sessionKey)
	}
	resp, err := serverClient(0).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f.mu.Lock()
	f.live = true
	f.mu.Unlock()
	f.log.Printf("[STREAM] live channel to %s open", f.base)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	lines := make(chan []byte)
	errc := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for sc.Scan() {
			select {
			case lines <- append([]byte(nil), sc.Bytes()...):
			case <-done:
				return
			}
		}
		errc <- sc.Err()
	}()
	for {
		select {
		case line := <-lines:
			var body serverPlayers
			if json.Unmarshal(line, &body) != nil || body.Type != "players" {
				continue // ping
			}
			apply(f.store(body))
		case err := <-errc:
			if err == nil {
				err = io.EOF
			}
			return err
		case <-time.After(60 * time.Second): // no ping for 60 s: dead link
			return fmt.Errorf("no data for 60 s")
		}
	}
}

type serverIdentity struct {
	Name       string `json:"name"`
	BlazeID    uint64 `json:"blazeId"`
	SessionKey string `json:"sessionKey"`
	ExpiresAt  int64  `json:"expiresAt"`
	RenewAfter int64  `json:"renewAfter"`
}

type serverAuthChallenge struct {
	Version       string `json:"version"`
	ChallengeID   string `json:"challengeId"`
	Nonce         string `json:"nonce"`
	Persona       string `json:"persona"`
	AuthMode      string `json:"authMode"`
	ClaimRequired bool   `json:"claimRequired"`
	ExpiresIn     int    `json:"expiresIn"`
	SessionHours  int    `json:"sessionHours"`
}

func httpProblem(resp *http.Response) error {
	var problem struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&problem)
	if problem.Message != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, problem.Message)
	}
	if problem.Error != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, problem.Error)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// fetchIdentity authenticates the launcher: get a one-use challenge, sign it
// with the Ed25519 key, receive (or renew) a session ticket.
func fetchIdentity(base, persona, clientKey, claimCode, currentSession string) (serverIdentity, error) {
	pub, priv, err := launcherKeyPair(clientKey)
	if err != nil {
		return serverIdentity{}, err
	}
	pubHex := hex.EncodeToString(pub)
	client := serverClient(6 * time.Second)

	challengeBody, _ := json.Marshal(map[string]string{
		"persona":   persona,
		"publicKey": pubHex,
	})
	req, _ := http.NewRequest("POST", base+"/nfsrun/auth/challenge", bytes.NewReader(challengeBody))
	req.Header.Set("User-Agent", "nfsrun-launcher")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return serverIdentity{}, err
	}
	if resp.StatusCode != http.StatusOK {
		err = httpProblem(resp)
		resp.Body.Close()
		return serverIdentity{}, err
	}
	var ch serverAuthChallenge
	err = json.NewDecoder(resp.Body).Decode(&ch)
	resp.Body.Close()
	if err != nil {
		return serverIdentity{}, err
	}
	if ch.Version != "nfsrun-auth-v3" || ch.ChallengeID == "" || ch.Nonce == "" || ch.Persona == "" {
		return serverIdentity{}, fmt.Errorf("server returned an invalid auth challenge")
	}
	if ch.ClaimRequired && strings.TrimSpace(claimCode) == "" {
		return serverIdentity{}, fmt.Errorf("this name needs a one-time INVITE_CODE from the server owner (%q)", ch.Persona)
	}

	message := launcherAuthMessage(ch.ChallengeID, ch.Nonce, ch.Persona, pubHex, claimCode, currentSession)
	signature := hex.EncodeToString(ed25519.Sign(priv, message))
	authBody, _ := json.Marshal(map[string]string{
		"challengeId":    ch.ChallengeID,
		"persona":        ch.Persona,
		"publicKey":      pubHex,
		"signature":      signature,
		"claimCode":      claimCode,
		"currentSession": currentSession,
	})
	req, _ = http.NewRequest("POST", base+"/nfsrun/auth/session", bytes.NewReader(authBody))
	req.Header.Set("User-Agent", "nfsrun-launcher")
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		return serverIdentity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return serverIdentity{}, httpProblem(resp)
	}
	var body serverIdentity
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return serverIdentity{}, err
	}
	if body.Name == "" || body.BlazeID == 0 || strings.TrimSpace(body.SessionKey) == "" {
		return serverIdentity{}, fmt.Errorf("server returned an incomplete identity")
	}
	return body, nil
}

func maintainLauncherSession(l *log.Logger, base, persona, clientKey string, session *launcherSessionState) {
	for {
		key, expiresAt, renewAfter := session.current()
		if key == "" {
			return
		}
		now := time.Now().Unix()
		wait := int64(60)
		if renewAfter > now {
			wait = renewAfter - now
			if wait > 300 {
				wait = 300
			}
		}
		if wait < 5 {
			wait = 5
		}
		time.Sleep(time.Duration(wait) * time.Second)

		key, expiresAt, renewAfter = session.current()
		now = time.Now().Unix()
		if renewAfter > now {
			continue
		}
		ident, err := fetchIdentity(base, persona, clientKey, "", key)
		if err != nil {
			remaining := expiresAt - now
			l.Printf("[AUTH] session renewal failed (%ds remaining): %v", remaining, err)
			// Retry aggressively near expiry, but never busy-loop.
			if remaining > 120 {
				time.Sleep(30 * time.Second)
			} else {
				time.Sleep(10 * time.Second)
			}
			continue
		}
		old := key
		session.set(ident)
		if ident.SessionKey == old {
			l.Printf("[AUTH] launcher session renewed until %s", time.Unix(ident.ExpiresAt, 0).Format(time.RFC3339))
		} else {
			l.Printf("[AUTH] launcher session rotated; new ticket valid until %s", time.Unix(ident.ExpiresAt, 0).Format(time.RFC3339))
		}
	}
}

// stripTunnelArgs removes "-NfsOnline.EnableProtoTunnel <value>": players and
// race servers must agree on ProtoTunnel, so both keep the game's default.
func stripTunnelArgs(args []string) (kept, dropped []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.EqualFold(strings.TrimLeft(a, "-+"), "NfsOnline.EnableProtoTunnel") {
			dropped = append(dropped, a)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				dropped = append(dropped, args[i])
			}
			continue
		}
		kept = append(kept, a)
	}
	return kept, dropped
}

// withSoloOnline adds "-NfsOnline.AllowSinglePlayerOnline true" unless
// GAME_ARGS sets it; without it the client leaves a playlist when alone.
func withSoloOnline(args []string) ([]string, bool) {
	for _, a := range args {
		if strings.EqualFold(strings.TrimLeft(a, "-+"), "NfsOnline.AllowSinglePlayerOnline") {
			return args, false
		}
	}
	return append(args, "-NfsOnline.AllowSinglePlayerOnline", "true"), true
}

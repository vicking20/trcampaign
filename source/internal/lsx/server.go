package lsx

import (
	"bufio"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Identity is what the fake Origin reports for the local player.
type Identity struct {
	Persona   string // display name (also used by the Blaze server as persona name)
	UserID    uint64 // Nucleus user id (any stable number)
	PersonaID uint64 // Origin persona id (any stable number)
	AuthCode  string // returned by GetAuthCode/GetAuthToken; the Blaze server decodes it in originLogin
	// AuthCodeFunc, when set, overrides AuthCode, so a renewed session ticket is
	// returned on a later reconnect.
	AuthCodeFunc func() string
	Locale       string // e.g. en_US

	// Friends, when set, lists the other players (QueryFriends).
	Friends func() []Friend

	// relays to the private server: presence the game sets, invites it sends
	OnSetPresence func(presence, sessionID, rich string)
	OnSendInvite  func(to []uint64, sessionID string)
}

func (i Identity) authCode() string {
	if i.AuthCodeFunc != nil {
		if v := i.AuthCodeFunc(); v != "" {
			return v
		}
	}
	return i.AuthCode
}

// Friend is one entry of QueryFriendsResponse.
type Friend struct {
	Persona string
	UserID  uint64
	Online  bool
	// Origin presence (INGAME, JOINABLE, ...) and SessionId; empty = INGAME or
	// OFFLINE from Online.
	Presence  string
	SessionID string
}

func (f Friend) presence() string {
	if !f.Online {
		return "OFFLINE"
	}
	if f.Presence != "" && f.Presence != "OFFLINE" {
		return f.Presence
	}
	return "INGAME"
}

func (f Friend) session() string {
	if f.SessionID == "" || !f.Online {
		return "0"
	}
	return f.SessionID
}

// friendsXML builds QueryFriendsResponse (format as in EAappEmulater's captures).
func (s *Server) friendsXML() string {
	if s.ID.Friends == nil {
		return `<QueryFriendsResponse />`
	}
	var b strings.Builder
	b.WriteString("<QueryFriendsResponse>")
	n := 0
	for _, f := range s.ID.Friends() {
		if f.UserID == s.ID.UserID || strings.EqualFold(f.Persona, s.ID.Persona) {
			continue
		}
		fmt.Fprintf(&b, `<Friend RichPresence="" AvatarId="" UserId="%d" Group="" Title="NFS: The Run" TitleId="bbx.nfs2012.pc.na" GamePresence="" Persona="%s" PersonaId="%d" State="MUTUAL" MultiplayerId="71156" GroupId="" Presence="%s" SessionId="%s" />`,
			f.UserID, x(f.Persona), f.UserID, f.presence(), x(f.session()))
		n++
	}
	b.WriteString("</QueryFriendsResponse>")
	s.logf("[LSX] QueryFriends -> %d friend(s)", n)
	return b.String()
}

// friend looks a player up in the current friend list.
func (s *Server) friend(id uint64) (Friend, bool) {
	if s.ID.Friends == nil {
		return Friend{}, false
	}
	for _, f := range s.ID.Friends() {
		if f.UserID == id {
			return f, true
		}
	}
	return Friend{}, false
}

// presenceXML: QueryPresenceResponse with one Friend per friend.
func (s *Server) presenceXML() string {
	var b strings.Builder
	b.WriteString("<QueryPresenceResponse>")
	if s.ID.Friends != nil {
		for _, f := range s.ID.Friends() {
			if f.UserID == s.ID.UserID {
				continue
			}
			fmt.Fprintf(&b, `<Friend UserId="%d" Presence="%s" RichPresence="" GamePresence="" SessionId="%s" Title="NFS: The Run" TitleId="bbx.nfs2012.pc.na" />`, f.UserID, f.presence(), x(f.session()))
		}
	}
	b.WriteString("</QueryPresenceResponse>")
	return b.String()
}

// Server is a single-user LSX endpoint on 127.0.0.1.
type Server struct {
	ID  Identity
	Log *log.Logger

	// ChallengeKey is the key we challenge the SDK with (EAappEmulater uses a fixed one).
	ChallengeKey string

	mu       sync.Mutex
	sessions int
	// own presence as the game last set it (SetPresence)
	selfPresence, selfSession, selfRich string
	Requests                            map[string]int // request name -> count (for the summary / tests)
	conns                               map[*lsxConn]bool
}

// lsxConn is one game connection; events are written with its cipher.
type lsxConn struct {
	c    net.Conn
	wmu  sync.Mutex
	mode cipherMode
	key  []byte
	tag  string
}

func (lc *lsxConn) send(s *Server, msg string) error {
	lc.wmu.Lock()
	defer lc.wmu.Unlock()
	out := msg
	if lc.mode != modePlain && lc.key != nil {
		var err error
		if out, err = encryptHex(lc.key, []byte(msg)); err != nil {
			return err
		}
	}
	return writeMsg(lc.c, out)
}

// PushEvent sends an unsolicited LSX event (e.g. PresenceEvent, FriendsEvent)
// to every connected game. sender is the service of the facility in GetConfig
// (XMPP for PRESENCE_EVENT / FRIENDS_EVENT).
func (s *Server) PushEvent(sender, body string) int {
	msg := fmt.Sprintf(`<LSX><Event sender="%s">%s</Event></LSX>`, sender, body)
	s.mu.Lock()
	var list []*lsxConn
	for lc := range s.conns {
		list = append(list, lc)
	}
	s.mu.Unlock()
	n := 0
	for _, lc := range list {
		if lc.key == nil && lc.mode != modePlain {
			continue // handshake not finished
		}
		if err := lc.send(s, msg); err == nil {
			s.logf("%s -> [event] %s", lc.tag, msg)
			n++
		}
	}
	return n
}

const defaultChallengeKey = "cacf897a20b6d612ad0c05e011df52bb"

func NewServer(id Identity, l *log.Logger) *Server {
	if id.Locale == "" {
		id.Locale = "en_US"
	}
	return &Server{ID: id, Log: l, ChallengeKey: defaultChallengeKey, Requests: map[string]int{}, conns: map[*lsxConn]bool{}}
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(c)
	}
}

// ------------------------------------------------------------------ framing

func readMsg(r *bufio.Reader) (string, error) {
	b, err := r.ReadBytes(0)
	if err != nil {
		if len(b) > 0 && err == io.EOF {
			return string(b), nil
		}
		return "", err
	}
	return string(b[:len(b)-1]), nil
}

func writeMsg(w io.Writer, msg string) error {
	_, err := w.Write(append([]byte(msg), 0))
	return err
}

// ------------------------------------------------------------------ parsing

var (
	reAttr    = regexp.MustCompile(`([A-Za-z_][\w.-]*)\s*=\s*"([^"]*)"`)
	reRequest = regexp.MustCompile(`(?s)<Request\b([^>]*)>\s*<([A-Za-z_][\w]*)\b([^>]*?)/?>`)
	reContent = regexp.MustCompile(`(?s)<ContentId>([^<]*)</ContentId>`)
	reChalRsp = regexp.MustCompile(`(?s)<ChallengeResponse\b([^>]*)>`)
)

func attrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range reAttr.FindAllStringSubmatch(s, -1) {
		m[a[1]] = html.UnescapeString(a[2])
	}
	return m
}

// Request is one parsed LSX request.
type Request struct {
	ID     string
	Sender string
	Name   string            // first element inside <Request>, e.g. GetProfile
	Attrs  map[string]string // attributes of that element
	Raw    string
}

func parseRequest(xml string) (Request, bool) {
	m := reRequest.FindStringSubmatch(xml)
	if m == nil {
		return Request{Raw: xml}, false
	}
	ra := attrs(m[1])
	return Request{ID: ra["id"], Sender: ra["recipient"], Name: m[2], Attrs: attrs(m[3]), Raw: xml}, true
}

// ------------------------------------------------------------------ session

type cipherMode int

const (
	modePlain cipherMode = iota
	modeFixedKey
	modeSeeded
)

func (m cipherMode) String() string {
	return [...]string{"plaintext", "fixed-key (SDK version 2)", "seeded-key"}[m]
}

func (s *Server) logf(f string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(f, a...)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	s.mu.Lock()
	s.sessions++
	n := s.sessions
	s.mu.Unlock()
	tag := fmt.Sprintf("[LSX#%d]", n)
	s.logf("%s game connected from %s", tag, c.RemoteAddr())
	defer s.logf("%s closed", tag)

	r := bufio.NewReader(c)
	challenge := fmt.Sprintf(`<LSX><Event sender="EALS"><Challenge build="release" key="%s" version="10,5,30,15625" /></Event></LSX>`, s.ChallengeKey)
	if err := writeMsg(c, challenge); err != nil {
		s.logf("%s send challenge: %v", tag, err)
		return
	}
	s.logf("%s -> %s", tag, challenge)

	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	first, err := readMsg(r)
	if err != nil {
		s.logf("%s no challenge response: %v", tag, err)
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	s.logf("%s <- %s", tag, first)

	req, _ := parseRequest(first)
	cr := map[string]string{}
	if m := reChalRsp.FindStringSubmatch(first); m != nil {
		cr = attrs(m[1])
	}
	contentID := ""
	if m := reContent.FindStringSubmatch(first); m != nil {
		contentID = strings.TrimSpace(m[1])
	}
	version := cr["version"]
	s.logf("%s challenge: version=%q contentId=%q key=%q", tag, version, contentID, cr["key"])
	if cr["response"] != "" && !checkChallengeResponse(cr["response"], s.ChallengeKey) {
		s.logf("%s WARNING challenge response does not decrypt to our key (continuing anyway)", tag)
	}
	ours := makeChallengeResponse(cr["key"])
	accept := fmt.Sprintf(`<LSX><Response id="%s" sender="EALS"><ChallengeAccepted response="%s" /></Response></LSX>`, req.ID, ours)
	if err := writeMsg(c, accept); err != nil {
		return
	}
	s.logf("%s -> %s", tag, accept)

	mode := modeSeeded
	if version == "2" {
		mode = modeFixedKey
	}
	key := challengeKey
	if mode == modeSeeded {
		key = seededKey(seedFromResponse(ours))
	}
	lc := &lsxConn{c: c, mode: mode, key: key, tag: tag}
	s.mu.Lock()
	s.conns[lc] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, lc)
		s.mu.Unlock()
	}()

	for {
		raw, err := readMsg(r)
		if err != nil {
			if err != io.EOF {
				s.logf("%s read: %v", tag, err)
			}
			return
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}
		msgMode := mode
		plain := raw
		if strings.HasPrefix(strings.TrimSpace(raw), "<") {
			msgMode = modePlain
		} else if p, err := decryptHex(key, raw); err == nil {
			plain = p
		} else if p, err := decryptHex(challengeKey, raw); err == nil {
			// fall back if our guess of the session mode was wrong
			plain, mode, key, msgMode = p, modeFixedKey, challengeKey, modeFixedKey
		} else {
			s.logf("%s <- undecodable message (%d bytes): %.120q", tag, len(raw), raw)
			continue
		}
		s.logf("%s <- [%s] %s", tag, msgMode, plain)

		resp := s.Respond(plain)
		if resp == "" {
			continue
		}
		s.logf("%s -> %s", tag, resp)
		lc.wmu.Lock()
		lc.mode, lc.key = msgMode, key
		lc.wmu.Unlock()
		if err := lc.send(s, resp); err != nil {
			return
		}
	}
}

// ------------------------------------------------------------------ replies

func x(v string) string { return html.EscapeString(v) }

// Respond builds the reply XML for one decrypted request (templates follow
// EAappEmulater's LSX/BFH set, adapted to a private server).
func (s *Server) Respond(xml string) string {
	req, ok := parseRequest(xml)
	if !ok {
		s.logf("[LSX] not a request: %.200q", xml)
		return ""
	}
	s.mu.Lock()
	s.Requests[req.Name]++
	s.mu.Unlock()
	id := x(req.ID)
	wrap := func(sender, body string) string {
		return fmt.Sprintf(`<LSX><Response id="%s" sender="%s">%s</Response></LSX>`, id, sender, body)
	}
	switch req.Name {
	case "GetConfig":
		var b strings.Builder
		b.WriteString("<GetConfigResponse>")
		for _, sv := range [][2]string{
			{"EbisuSDK", "SDK"}, {"EbisuSDK", "PROFILE"}, {"XMPP", "PRESENCE"}, {"XMPP", "FRIENDS"},
			{"Commerce", "COMMERCE"}, {"EbisuSDK", "RECENTPLAYER"}, {"EbisuSDK", "IGO"}, {"EbisuSDK", "MISC"},
			{"EALS", "LOGIN"}, {"Utility", "UTILITY"}, {"XMPP", "XMPP"}, {"XMPP", "CHAT"},
			{"EbisuSDK", "IGO_EVENT"}, {"EALS", "EALS_EVENTS"}, {"EbisuSDK", "LOGIN_EVENT"}, {"XMPP", "INVITE_EVENT"},
			{"EbisuSDK", "PROFILE_EVENT"}, {"XMPP", "PRESENCE_EVENT"}, {"XMPP", "FRIENDS_EVENT"},
			{"Commerce", "COMMERCE_EVENT"}, {"XMPP", "CHAT_EVENT"}, {"EbisuSDK", "DOWNLOAD_EVENT"},
			{"EbisuSDK", "PERMISSION"}, {"EbisuSDK", "RESOURCES"}, {"EbisuSDK", "BLOCKED_USERS"},
			{"EbisuSDK", "BLOCKED_USER_EVENT"}, {"EbisuSDK", "GET_USERID"}, {"EbisuSDK", "ONLINE_STATUS_EVENT"},
			{"EbisuSDK", "ACHIEVEMENT"}, {"EbisuSDK", "ACHIEVEMENT_EVENT"}, {"EbisuSDK", "BROADCAST_EVENT"},
			{"EbisuSDK", "CONTENT"},
		} {
			fmt.Fprintf(&b, `<Service Name="%s" Facility="%s" />`, sv[0], sv[1])
		}
		b.WriteString("</GetConfigResponse>")
		return wrap("EbisuSDK", b.String())
	case "GetProfile":
		return wrap("EbisuSDK", fmt.Sprintf(`<GetProfileResponse UserId="%d" GeoCountry="US" UserIndex="0" IsSubscriber="false" PersonaId="%d" SubscriberLevel="0" Persona="%s" Country="US" CommerceCurrency="USD" IsUnderAge="false" AvatarId="" CommerceCountry="US" IsTrialSubscriber="false" />`,
			s.ID.UserID, s.ID.PersonaID, x(s.ID.Persona)))
	case "GetAuthCode":
		return wrap("Utility", fmt.Sprintf(`<AuthCode value="%s" />`, x(s.ID.authCode())))
	case "GetAuthToken":
		return wrap("Utility", fmt.Sprintf(`<AuthToken value="%s" />`, x(s.ID.authCode())))
	case "GetInternetConnectedState":
		return wrap("Utility", `<InternetConnectedState connected="1" />`)
	case "GetSetting":
		v := "false"
		switch strings.ToUpper(req.Attrs["SettingId"]) {
		case "ENVIRONMENT":
			v = "production"
		case "IS_IGO_AVAILABLE", "IS_IGO_ENABLED":
			v = "false"
		}
		return wrap("EbisuSDK", fmt.Sprintf(`<GetSettingResponse Setting="%s" />`, v))
	case "GetGameInfo":
		v := s.ID.Locale
		switch strings.ToUpper(req.Attrs["GameInfoId"]) {
		case "FREETRIAL":
			v = "false"
		case "UPTODATE":
			v = "true"
		case "INSTALLED_LANGUAGE", "LANGUAGES", "":
			v = s.ID.Locale
		}
		return wrap("EbisuSDK", fmt.Sprintf(`<GetGameInfoResponse GameInfo="%s" />`, x(v)))
	case "GetPresence":
		uid := s.ID.UserID
		s.mu.Lock()
		presence, session, rich := s.selfPresence, s.selfSession, s.selfRich
		s.mu.Unlock()
		if presence == "" {
			presence = "INGAME"
		}
		if session == "" {
			session = "0"
		}
		if v, err := strconv.ParseUint(req.Attrs["UserId"], 10, 64); err == nil && v != 0 && v != s.ID.UserID {
			uid, presence, session, rich = v, "OFFLINE", "0", ""
			if f, ok := s.friend(v); ok {
				presence, session = f.presence(), f.session()
			}
		}
		return wrap("XMPP", fmt.Sprintf(`<GetPresenceResponse Title="NFS: The Run" UserId="%d" Group="" MultiplayerId="71156" RichPresence="%s" Presence="%s" TitleId="bbx.nfs2012.pc.na" GamePresence="" GroupId="" SessionId="%s" />`, uid, x(rich), x(presence), x(session)))
	case "SetPresence":
		// <SetPresence UserId Presence="JOINABLE" RichPresence SessionId="N"/>
		p, sess, rich := strings.ToUpper(req.Attrs["Presence"]), req.Attrs["SessionId"], req.Attrs["RichPresence"]
		s.mu.Lock()
		changed := p != s.selfPresence || sess != s.selfSession || rich != s.selfRich
		s.selfPresence, s.selfSession, s.selfRich = p, sess, rich
		s.mu.Unlock()
		if changed && s.ID.OnSetPresence != nil {
			go s.ID.OnSetPresence(p, sess, rich)
		}
		return wrap("XMPP", `<ErrorSuccess Description="" Code="0" />`)
	case "SendInvite":
		// SendInvite: the friend's launcher turns it into a MultiplayerInvite event.
		to := inviteesOf(req, s.ID.UserID)
		s.mu.Lock()
		sess := s.selfSession
		s.mu.Unlock()
		s.logf("[LSX] SendInvite to %v (session %q) raw=%.300q", to, sess, req.Raw)
		if len(to) > 0 && s.ID.OnSendInvite != nil {
			go s.ID.OnSendInvite(to, sess)
		}
		return wrap("XMPP", `<ErrorSuccess Description="" Code="0" />`)
	case "GetUserProfileByEmailorEAID":
		// The group menu looks friends up by name before inviting them: response
		// {version, Return} with <User version EAID MAIL UserId/> children.
		kw := strings.TrimSpace(req.Attrs["KeyWord"])
		var b strings.Builder
		b.WriteString(`<GetUserProfileByEmailorEAIDResponse Return="Success">`)
		found := 0
		if s.ID.Friends != nil {
			for _, f := range s.ID.Friends() {
				if strings.EqualFold(f.Persona, kw) {
					fmt.Fprintf(&b, `<User EAID="%s" MAIL="" UserId="%d" />`, x(f.Persona), f.UserID)
					found++
				}
			}
		}
		if found == 0 && strings.EqualFold(kw, s.ID.Persona) {
			fmt.Fprintf(&b, `<User EAID="%s" MAIL="" UserId="%d" />`, x(s.ID.Persona), s.ID.UserID)
			found++
		}
		b.WriteString(`</GetUserProfileByEmailorEAIDResponse>`)
		s.logf("[LSX] GetUserProfileByEmailorEAID %q -> %d user(s)", kw, found)
		return wrap("EbisuSDK", b.String())
	case "GetBlockList":
		return wrap("EbisuSDK", `<GetBlockListResponse Return="Success" />`)
	case "QueryFriends":
		return wrap("XMPP", s.friendsXML())
	case "QueryPresence":
		return wrap("XMPP", s.presenceXML())
	case "QueryImage":
		return wrap("EbisuSDK", `<QueryImageResponse Result="0" />`)
	case "QueryAreFriends":
		return wrap("XMPP", `<QueryAreFriendsResponse />`)
	case "QueryRecentPlayers", "QueryRecent":
		return wrap("EbisuSDK", `<QueryRecentPlayersResponse />`)
	case "QueryEntitlements":
		return wrap("EbisuSDK", `<QueryEntitlementsResponse />`)
	case "QueryOffers":
		return wrap("EbisuSDK", `<QueryOffersResponse />`)
	case "GetWalletBalance":
		return wrap("EbisuSDK", `<GetWalletBalanceResponse Balance="0" Currency="USD" />`)
	case "CheckPermission":
		return wrap("EbisuSDK", `<CheckPermissionResponse Permission="GRANTED" />`)
	case "RequestLicense":
		return wrap("EbisuSDK", `<RequestLicenseResponse License="" />`)
	default:
		// SetPresence, SetDownloaderUtilization, Show*UI, ... : generic success.
		sender := req.Sender
		if sender == "" {
			sender = "EbisuSDK"
		}
		if !strings.HasPrefix(req.Name, "Set") {
			s.logf("[LSX] UNHANDLED request %q (answered with ErrorSuccess) attrs=%v", req.Name, req.Attrs)
		}
		return wrap(sender, `<ErrorSuccess Description="" Code="0" />`)
	}
}

var reInvitee = regexp.MustCompile(`(?:UserId|Invitee|Id)\s*=\s*"(\d+)"|>\s*(\d+)\s*<`)

// inviteesOf collects the user ids inside <SendInvite ...><Invitees>...</>
// (whatever element form the SDK uses), minus the sender itself.
func inviteesOf(req Request, self uint64) []uint64 {
	raw := req.Raw
	if i := strings.Index(raw, "<SendInvite"); i >= 0 {
		raw = raw[i:]
		if j := strings.Index(raw, ">"); j >= 0 && !strings.HasPrefix(raw[j-1:], "/>") {
			raw = raw[j:] // skip SendInvite's own UserId (the sender)
		} else {
			raw = ""
		}
	}
	seen := map[uint64]bool{}
	var out []uint64
	for _, m := range reInvitee.FindAllStringSubmatch(raw, -1) {
		v := m[1]
		if v == "" {
			v = m[2]
		}
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 || id == self || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// PushPresence tells the game that friend f changed presence. This SDK only
// handles GetPresenceResponse (0x10), QueryFriendsResponse (0x20) and
// MultiplayerInvite events.
func (s *Server) PushPresence(f Friend) int {
	return s.PushEvent("XMPP", fmt.Sprintf(`<GetPresenceResponse Title="NFS: The Run" UserId="%d" Group="" MultiplayerId="71156" RichPresence="" Presence="%s" TitleId="bbx.nfs2012.pc.na" GamePresence="" GroupId="" SessionId="%s" />`,
		f.UserID, x(f.presence()), x(f.session())))
}

// PushFriends sends the whole friend list as a QueryFriendsResponse event;
// the game rebuilds its list.
func (s *Server) PushFriends() int {
	return s.PushEvent("XMPP", s.friendsXML())
}

// PushInvite raises a MultiplayerInvite event (INVITE_EVENT facility, XMPP
// service): Origin's "the player accepted this invite". The SDK reads the
// attributes initial and from and the child element SessionInformation
// (0x01920D30); the game parses the session as the inviter's Blaze id, looks
// the inviter up and joins its playgroup or game.
func (s *Server) PushInvite(from uint64, session string) int {
	return s.PushEvent("XMPP", fmt.Sprintf(`<MultiplayerInvite version="2" initial="false" from="%d"><SessionInformation>%s</SessionInformation></MultiplayerInvite>`, from, x(session)))
}

// Summary returns "Name xN, ..." for the log.
func (s *Server) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var parts []string
	for k, v := range s.Requests {
		parts = append(parts, fmt.Sprintf("%s x%d", k, v))
	}
	return strings.Join(parts, ", ")
}

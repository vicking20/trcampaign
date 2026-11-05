package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/database"
)

// Web Autolog ("V8" web service).
//
// The game calls https://<V8ServerName>/... (client config key); the SSL
// gateway forwards it to this HTTP port. Requests carry X-SESSION-KEY. Replies
// are Frostbite reflection JSON (field names from the game's type tables;
// 64-bit ids as strings):
//
//	GET  /Persona/{id}                               PlayerProfile
//	GET  /Persona/{id}/getfriends                    FriendCollection {Friends:[Friend]}
//	GET  /Persona/{id}/getRecommendedFriends         FriendCollection (people to add)
//	GET  /Persona/{id}/{id2}/getCommonFriends        FriendCollection (shared friends)
//	GET  /Persona/{id}/getSkillLevelRule             SkillLevelRule
//	GET  /Autolog/{id}/{a}/{b}/{lang}/getNews        [BroadcastNews]
//	GET  /Autolog/{id}/{a}/{b}/{lang}/getAutologNewItems  AutologNewItems
//	GET  /Autolog/{id}/getRunLeaderboard, getTopRunLeaderboard  [EventStanding]
//	GET  /Autolog/{id}/{ev}/getRaceMetaDataBlobs     [RaceMetaDataBlob]
//	POST anything else (update*, set*, increment*, bulkSet, ...) -> 200 {};
//	     Persona bodies are kept per player (data/web/<id>.json).
//
// Every reply has Content-Length: the game's HTTP client cannot read chunked
// bodies.

type webStore struct {
	mu  sync.Mutex
	dir string
}

func (ws *webStore) path(id string) string { return filepath.Join(ws.dir, "persona_"+id+".json") }

func (ws *webStore) load(id string) map[string]any {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	m := map[string]any{}
	if b, err := os.ReadFile(ws.path(id)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (ws *webStore) merge(id string, add map[string]any) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	m := map[string]any{}
	if b, err := os.ReadFile(ws.path(id)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	for k, v := range add {
		m[k] = v
	}
	_ = os.MkdirAll(ws.dir, 0755)
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(ws.path(id), b, 0644)
}

// ignoreReco / ignoredRecos: recommended friends the player dismissed.
func (ws *webStore) ignoreReco(me, other string) {
	m := ws.load(me)
	list, _ := m["_ignoredFriendRecos"].([]any)
	for _, v := range list {
		if v == other {
			return
		}
	}
	ws.merge(me, map[string]any{"_ignoredFriendRecos": append(list, other)})
}

func (ws *webStore) ignoredRecos(me string) map[string]bool {
	out := map[string]bool{}
	list, _ := ws.load(me)["_ignoredFriendRecos"].([]any)
	for _, v := range list {
		if str, ok := v.(string); ok {
			out[str] = true
		}
	}
	return out
}

// webRoute strips the "/<product>/Game/<platform>" prefix; "" when the path
// is not a web Autolog route.
// webUnmarshal also accepts the game's malformed bodies that open a "{"
// before every field after the first ({"a":"..",{"b":".."}).
func webUnmarshal(body []byte, v any) error {
	err := json.Unmarshal(body, v)
	if err == nil {
		return nil
	}
	fixed := bytes.ReplaceAll(body, []byte(`,{"`), []byte(`,"`))
	if bytes.Equal(fixed, body) {
		return err
	}
	return json.Unmarshal(fixed, v)
}

func webRoute(path string) string {
	for _, k := range []string{"/Persona/", "/Autolog/"} {
		if i := strings.Index(path, k); i >= 0 {
			return path[i:]
		}
		if strings.HasSuffix(path, strings.TrimSuffix(k, "/")) {
			return k
		}
	}
	return ""
}

var (
	webPersonaRe = regexp.MustCompile(`^/Persona/(\d+)(?:/(\d+))?(?:/(\d+))?(?:/([A-Za-z]+))?/?$`)
	webAutologRe = regexp.MustCompile(`^/Autolog/(\d+)(?:/(-?\d+))?(?:/(-?\d+))?(?:/(-?\d+))?(?:/([A-Za-z]+))?/?$`)
)

// writeWebJSON answers with JSON and an explicit length.
func (s *Server) writeWebJSON(w http.ResponseWriter, r *http.Request, v any) {
	var b []byte
	switch t := v.(type) {
	case []byte:
		b = t
	case string:
		b = []byte(t)
	default:
		b, _ = json.Marshal(v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
	shown := b
	if len(shown) > 600 {
		shown = shown[:600]
	}
	s.log.Printf("[WEB] %s %s -> %s", r.Method, r.URL.Path, shown)
}

func (s *Server) webPersonaByID(id string) (database.PersonaRef, uint64, bool) {
	want, _ := strconv.ParseUint(id, 10, 64)
	refs, err := s.db.ListPersonas(0, 1000)
	if err != nil {
		return database.PersonaRef{}, 0, false
	}
	for _, ref := range refs {
		wire := personaFromRef(ref).BlazeID
		if wire == want || ref.ID == want {
			return ref, wire, true
		}
	}
	return database.PersonaRef{}, 0, false
}

// webStats: what the server knows about a player's speedwall activity.
type webStats struct{ posts, firsts uint64 }

func (s *Server) webStatsFor(personaID uint64) webStats {
	var st webStats
	for _, board := range []string{"race_single", "run_single"} {
		entries, err := s.db.LoadBoard(board)
		if err != nil {
			continue
		}
		best := map[uint64]uint64{}
		for _, e := range entries {
			if b, ok := best[e.EventID]; !ok || e.Score < b {
				best[e.EventID] = e.Score
			}
		}
		for _, e := range entries {
			if e.PersonaID == personaID {
				st.posts++
				if e.Score == best[e.EventID] {
					st.firsts++
				}
			}
		}
	}
	return st
}

func (s *Server) webProfile(id string) map[string]any {
	ref, wire, ok := s.webPersonaByID(id)
	p := map[string]any{
		"PlayerProfileId": id, "RivalId": "0", "Persona": "",
		"NumberGamesPlayed": 0, "LoginCount": 0, "playerXP": 0, "playerLevel": 0,
		"friendPrivacy": 0, "recommendsPlayedCount": 0,
		"IconGuid": "", "MottoGuid": "", "BackgroundGuid": "", "currentLocation": "",
		"fastestRunTime": 0, "runStagesOwned": 0, "currentRunProgress": 0,
		"currentCompletedStageIdx": 0, "lastCompletedChallenge": "", "currentRunTime": 0, "avgSpeed": 0.0,
		"currentDifficulty": 0, "favoriteCar": "", "favoriteCarHash": "", "avgMultiplayerPos": 0.0,
		"onlineWins": 0, "onlinePodiums": 0,
		"recommendsBeatenCount": 0, "speedwallFirstPlaceCount": 0, "speedwallPostsCount": 0,
		"autologBronzeMedalCount": 0, "autologSilverMedalCount": 0,
		"autologGoldMedalCount": 0, "autologPlatinumMedalCount": 0,
	}
	if ok {
		p["PlayerProfileId"] = strconv.FormatUint(wire, 10)
		p["Persona"] = ref.Name
		st := s.webStatsFor(ref.ID)
		p["speedwallPostsCount"], p["speedwallFirstPlaceCount"] = st.posts, st.firsts
		p["runStagesOwned"] = st.firsts
		id = strconv.FormatUint(wire, 10)
	}
	// What the game itself posted (updatePersona, updateBadge, ...).
	stored := s.web.load(id)
	for k, v := range stored {
		if _, known := p[k]; known {
			p[k] = v
		}
	}
	for from, to := range map[string]string{"badgeIcon": "IconGuid", "badgeMotto": "MottoGuid", "badgeBackground": "BackgroundGuid"} {
		if v, ok := stored[from]; ok {
			p[to] = v
		}
	}
	return p
}

func (s *Server) webFriendOf(ref database.PersonaRef) map[string]any {
	prof := s.webProfile(strconv.FormatUint(personaFromRef(ref).BlazeID, 10))
	f := map[string]any{
		"Id": prof["PlayerProfileId"], "Name": ref.Name, "Status": friendStatusOffline,
		"playerLevelRequirement": 0, "externalId": prof["PlayerProfileId"], "country": "US",
	}
	if s.online.names()[strings.ToLower(ref.Name)] {
		f["Status"] = friendStatusPlaying // FriendStatus enum: 3 = Playing
		// in an open group: 4 = Joinable
		if _, joinable, ok := s.pgStatus(personaFromRef(ref).BlazeID); ok && joinable {
			f["Status"] = friendStatusJoinable
		}
	}
	for _, k := range []string{"IconGuid", "MottoGuid", "BackgroundGuid", "playerXP", "playerLevel",
		"recommendsPlayedCount", "currentLocation", "fastestRunTime", "runStagesOwned",
		"currentRunProgress", "currentCompletedStageIdx", "currentRunTime", "avgSpeed",
		"currentDifficulty", "favoriteCarHash", "avgMultiplayerPos", "onlineWins", "onlinePodiums",
		"recommendsBeatenCount", "speedwallFirstPlaceCount",
		"speedwallPostsCount", "autologBronzeMedalCount", "autologSilverMedalCount",
		"autologGoldMedalCount", "autologPlatinumMedalCount"} {
		f[k] = prof[k]
	}
	return f
}

// webBulk answers bulkSet: one Friend per known name, also carrying the
// PlayerProfile keys (the reflection reader skips unknown keys).
func (s *Server) webBulk(names []string) []map[string]any {
	out := []map[string]any{}
	if len(names) == 0 {
		return out
	}
	refs, _ := s.db.ListPersonas(0, 1000)
	for _, n := range names {
		for _, ref := range refs {
			if !strings.EqualFold(ref.Name, n) {
				continue
			}
			f := s.webFriendOf(ref)
			f["PlayerProfileId"], f["Persona"], f["RivalId"] = f["Id"], ref.Name, "0"
			out = append(out, f)
			break
		}
	}
	return out
}

func (s *Server) webFriends(exceptID string) map[string]any {
	// the friends of exceptID
	var refs []database.PersonaRef
	if ref, _, ok := s.webPersonaByID(exceptID); ok {
		refs, _ = s.friendRefs(ref.ID)
	} else {
		refs, _ = s.db.ListPersonas(0, 500)
	}
	list := []map[string]any{}
	for _, ref := range refs {
		if isDSPersonaName(ref.Name) {
			continue
		}
		f := s.webFriendOf(ref)
		if f["Id"] == exceptID {
			continue
		}
		list = append(list, f)
	}
	return map[string]any{"Friends": list}
}

// friendIDSet: persona ids of someone's friends.
func (s *Server) friendIDSet(personaID uint64) map[uint64]bool {
	set := map[uint64]bool{}
	refs, _ := s.friendRefs(personaID)
	for _, r := range refs {
		set[r.ID] = true
	}
	return set
}

// webRecommendedFriends: friends of my friends first, then players I raced
// with, never myself, a friend, a race server or someone I ignored.
func (s *Server) webRecommendedFriends(meID string) map[string]any {
	list := []map[string]any{}
	me, _, ok := s.webPersonaByID(meID)
	if !ok {
		return map[string]any{"Friends": list}
	}
	mine := s.friendIDSet(me.ID)
	ignored := s.web.ignoredRecos(meID)
	all, _ := s.db.ListPersonas(0, 1000)
	byID := map[uint64]database.PersonaRef{}
	for _, r := range all {
		byID[r.ID] = r
	}
	var cand []uint64
	seen := map[uint64]bool{}
	add := func(id uint64) {
		r, ok := byID[id]
		if !ok || seen[id] || id == me.ID || mine[id] || isDSPersonaName(r.Name) || s.launchers.isShadowed(r.Name, r.ID) {
			return
		}
		wire := personaFromRef(r).BlazeID
		if ignored[strconv.FormatUint(wire, 10)] {
			return
		}
		seen[id] = true
		cand = append(cand, id)
	}
	for fid := range mine {
		refs, _ := s.db.ListFriends(fid) // the friends table only (not FRIENDS_ALL)
		for _, r := range refs {
			add(r.ID)
		}
	}
	for _, m := range s.recent.list(me.ID) {
		for _, r := range all {
			if strings.EqualFold(r.Name, m.Name) {
				add(r.ID)
			}
		}
	}
	for _, id := range cand {
		if len(list) >= 10 {
			break
		}
		list = append(list, s.webFriendOf(byID[id]))
	}
	return map[string]any{"Friends": list}
}

// webCommonFriends: the friends both players have.
func (s *Server) webCommonFriends(aID, bID string) map[string]any {
	list := []map[string]any{}
	a, _, okA := s.webPersonaByID(aID)
	b, _, okB := s.webPersonaByID(bID)
	if !okA || !okB {
		return map[string]any{"Friends": list}
	}
	theirs := s.friendIDSet(b.ID)
	refs, _ := s.friendRefs(a.ID)
	for _, r := range refs {
		if theirs[r.ID] && r.ID != b.ID {
			list = append(list, s.webFriendOf(r))
		}
	}
	return map[string]any{"Friends": list}
}

// ---------------------------------------------------------------- news

// broadcastNews mirrors the game's reflection type BroadcastNews (13 members,
// same order). Strings except NewsId; Territory, Language, Viewed (0 No, 1 Yes),
// Category and DateNumber are 32-bit ints. The news screen shows Subject as
// header, ShortMessage/Message as text, Viewed=0 as the "new" dot and Category
// as icon (0 shift2DLC, 1 The_RUN, 2 New_DLC, 3 autolog, 4 ea,
// 5 The_RUN_exclusive); NewsIcon is appended to the web base URL.
type broadcastNews struct {
	NewsID        int    `json:"NewsId"`
	PublishedDate string `json:"PublishedDate"`
	Author        string `json:"Author"`
	Subject       string `json:"Subject"`
	ShortMessage  string `json:"ShortMessage"`
	Message       string `json:"Message"`
	NewsIcon      string `json:"NewsIcon"`
	MetaContent   string `json:"MetaContent"`
	Territory     int    `json:"Territory"`
	Language      int    `json:"Language"`
	Viewed        int    `json:"Viewed"`
	Category      int    `json:"Category"`
	DateNumber    int    `json:"DateNumber"`
}

// newsIconPath: the banner URL is "https://" + V8ServerName + NewsIcon with no
// separator (0x007CAB99), so an icon must start with "/" ("news/a.jpg" would
// become https://host:18443news/a.jpg). Empty stays empty (= "/", default).
func newsIconPath(icon string) string {
	icon = strings.TrimSpace(strings.ReplaceAll(icon, `\`, "/"))
	if i := strings.Index(icon, "://"); i >= 0 {
		// a full URL: only its path can be served from here
		if j := strings.Index(icon[i+3:], "/"); j >= 0 {
			icon = icon[i+3+j:]
		} else {
			icon = ""
		}
	}
	if icon != "" && !strings.HasPrefix(icon, "/") {
		icon = "/" + icon
	}
	return icon
}

// newsLanguages: the game's language numbers, the {lang} of getNews.
var newsLanguages = []struct {
	N             int
	Name, Culture string
}{
	{0, "English", "en-US"}, {1, "French", "fr-FR"}, {2, "German", "de-DE"}, {3, "Spanish", "es-ES"},
	{4, "Italian", "it-IT"}, {5, "Japanese", "jp-JP"}, {6, "Russian", "ru-RU"}, {7, "Polish", "pl-PL"},
	{8, "Dutch", "nl-NL"}, {9, "Portuguese", "pt-PT"}, {10, "Chinese", "zh-CN"}, {11, "Korean", "en-US"},
	{12, "Czech", "cs-CS"}, {13, "Spanish (Mexico)", "es-MX"},
}

// defaultNewsText: the example item in every language (Subject, ShortMessage,
// Message), indexed by the game's language number.
var defaultNewsText = map[int][3]string{
	0:  {"Autolog is back!", "Welcome to the NFS The Run private server.", "This is an example news item served by the private server."},
	1:  {"Autolog est de retour !", "Bienvenue sur le serveur privé de NFS The Run.", "Ceci est un exemple d'actualité envoyé par le serveur privé."},
	2:  {"Autolog ist zurück!", "Willkommen auf dem privaten Server von NFS The Run.", "Dies ist eine Beispielnachricht des privaten Servers."},
	3:  {"¡Autolog ha vuelto!", "Bienvenido al servidor privado de NFS The Run.", "Esta es una noticia de ejemplo enviada por el servidor privado."},
	4:  {"Autolog è tornato!", "Benvenuto nel server privato di NFS The Run.", "Questa è una notizia di esempio inviata dal server privato."},
	5:  {"Autologが復活！", "NFS The Run プライベートサーバーへようこそ。", "これはプライベートサーバーが配信するサンプルニュースです。"},
	6:  {"Autolog вернулся!", "Добро пожаловать на частный сервер NFS The Run.", "Это пример новости, отправленной частным сервером."},
	7:  {"Autolog powrócił!", "Witaj na prywatnym serwerze NFS The Run.", "To przykładowa wiadomość wysłana przez prywatny serwer."},
	8:  {"Autolog is terug!", "Welkom op de privéserver van NFS The Run.", "Dit is een voorbeeldbericht van de privéserver."},
	9:  {"O Autolog está de volta!", "Bem-vindo ao servidor privado de NFS The Run.", "Esta é uma notícia de exemplo enviada pelo servidor privado."},
	10: {"Autolog 回來了！", "歡迎來到 NFS The Run 私人伺服器。", "這是私人伺服器發送的範例新聞。"},
	11: {"Autolog가 돌아왔습니다!", "NFS The Run 비공개 서버에 오신 것을 환영합니다.", "비공개 서버에서 보낸 예시 뉴스입니다."},
	12: {"Autolog je zpět!", "Vítejte na soukromém serveru NFS The Run.", "Toto je ukázková zpráva odeslaná soukromým serverem."},
	13: {"¡Autolog está de vuelta!", "Bienvenido al servidor privado de NFS The Run.", "Esta es una noticia de ejemplo enviada por el servidor privado."},
}

func defaultNews() []broadcastNews {
	now := time.Now().UTC()
	var out []broadcastNews
	for _, l := range newsLanguages {
		t := defaultNewsText[l.N]
		out = append(out, broadcastNews{
			NewsID:        1,
			PublishedDate: now.Format("01/02/2006"),
			Author:        "NFS The Run Private Server",
			Subject:       t[0],
			ShortMessage:  t[1],
			Message:       t[2],
			Language:      l.N,
			Category:      3,
			DateNumber:    int(now.Unix()),
		})
	}
	return out
}

// newsForLanguage: news.json may hold one entry per NewsId and language
// ("Language": the game's number, 0 = English). Each NewsId is sent once: the
// requested language, else English, else the first entry.
func newsForLanguage(all []broadcastNews, lang int) []broadcastNews {
	var order []int
	pick := map[int]broadcastNews{}
	rank := map[int]int{}
	for _, n := range all {
		r := 1
		switch n.Language {
		case lang:
			r = 3
		case 0:
			r = 2
		}
		if _, seen := pick[n.NewsID]; !seen {
			order = append(order, n.NewsID)
		} else if r <= rank[n.NewsID] {
			continue
		}
		pick[n.NewsID], rank[n.NewsID] = n, r
	}
	out := make([]broadcastNews, 0, len(order))
	for _, id := range order {
		n := pick[id]
		n.Language = lang
		out = append(out, n)
	}
	return out
}

func (s *Server) loadNews() []broadcastNews {
	path := filepath.Join(filepath.Dir(s.cfg.LockerDir), "news.json")
	b, err := os.ReadFile(path)
	if err != nil {
		news := defaultNews()
		out, _ := json.MarshalIndent(news, "", "  ")
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		_ = os.WriteFile(path, out, 0644)
		s.log.Printf("[WEB] created %s with an example news item in %d languages", path, len(news))
		return news
	}
	news, err := parseNewsJSON(b)
	if err != nil {
		// the news screen expects a list: never answer null
		s.log.Printf("[WEB] %s is not valid (%v): sending no news until it is fixed", path, err)
		return []broadcastNews{}
	}
	return news
}

// parseNewsJSON is lenient: a UTF-8 BOM is skipped, numbers may be strings
// and missing keys get defaults.
func parseNewsJSON(b []byte) ([]broadcastNews, error) {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	var raw []map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	str := func(m map[string]any, k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case json.Number:
			return v.String()
		case nil:
			return ""
		default:
			return fmt.Sprint(v)
		}
	}
	num := func(m map[string]any, k string) int {
		switch v := m[k].(type) {
		case json.Number:
			n, _ := v.Int64()
			if n == 0 {
				f, _ := v.Float64()
				n = int64(f)
			}
			return int(n)
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		case bool:
			if v {
				return 1
			}
		}
		return 0
	}
	out := make([]broadcastNews, 0, len(raw))
	for i, m := range raw {
		n := broadcastNews{
			NewsID: num(m, "NewsId"), PublishedDate: str(m, "PublishedDate"), Author: str(m, "Author"),
			Subject: str(m, "Subject"), ShortMessage: str(m, "ShortMessage"), Message: str(m, "Message"),
			NewsIcon: newsIconPath(str(m, "NewsIcon")), MetaContent: str(m, "MetaContent"), Territory: num(m, "Territory"),
			Language: num(m, "Language"), Viewed: num(m, "Viewed"), Category: num(m, "Category"), DateNumber: num(m, "DateNumber"),
		}
		if n.NewsID == 0 {
			n.NewsID = i + 1
		}
		if n.Viewed != 0 { // NewsViewed: 0 No, 1 Yes
			n.Viewed = 1
		}
		if n.Category < 0 || n.Category > 5 {
			n.Category = 3 // icons 0-5 only
		}
		out = append(out, n)
	}
	return out, nil
}

// ---------------------------------------------------------------- handler

func (s *Server) handleWebHTTP(w http.ResponseWriter, r *http.Request) {
	path := webRoute(r.URL.Path)
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var m map[string]any
		id := ""
		if p := webPersonaRe.FindStringSubmatch(path); p != nil {
			id = p[1]
		}
		// Only Persona routes change the stored profile.
		if strings.HasPrefix(path, "/Persona") && webUnmarshal(body, &m) == nil && len(m) > 0 {
			if id == "" {
				if v, ok := m["PlayerProfileId"].(string); ok {
					id = v
				} else if v, ok := m["BlazeId"].(string); ok {
					id = v
				}
			}
			if id != "" {
				// empty strings from updatePersona (Persona:"") must not wipe values
				for k, v := range m {
					if str, ok := v.(string); ok && str == "" {
						delete(m, k)
					}
				}
				s.web.merge(id, m)
			}
		}
		switch {
		case strings.HasSuffix(path, "/setRaceMetaDataBlobList") || strings.HasSuffix(path, "/setRaceMetaDataBlob"):
			n := s.web.saveBlobs(parseBlobs(body))
			s.log.Printf("[WEB] split times stored for %d event(s)", n)
			s.writeWebJSON(w, r, "{}")
		case strings.HasSuffix(path, "/getPlaylistUserInfo"):
			// body: other players' Blaze ids; the reply is one PlayerProfile per id
			var ids []string
			_ = json.Unmarshal(body, &ids)
			list := []map[string]any{}
			for _, id := range ids {
				if _, _, ok := s.webPersonaByID(id); ok {
					list = append(list, s.webProfile(id))
				}
			}
			s.writeWebJSON(w, r, list)
		case strings.HasSuffix(path, "/ignoreFriendReco"):
			// /Persona/{me}/{other}/ignoreFriendReco: stop recommending other
			if p := webPersonaRe.FindStringSubmatch(path); p != nil && p[2] != "" {
				s.web.ignoreReco(p[1], p[2])
			}
			s.writeWebJSON(w, r, "{}")
		case strings.HasSuffix(path, "/setNewsViewed"):
			if m := webAutologRe.FindStringSubmatch(path); m != nil {
				n, _ := strconv.Atoi(m[2])
				s.markNewsViewed(m[1], n)
			}
			s.writeWebJSON(w, r, "{}")
		case strings.HasSuffix(path, "/bulkSet") || strings.HasSuffix(path, "/recentPlayerBulkSet"):
			// body: the friends' persona names. The reply must be a FriendCollection
			// {"Friends":[...]}: the reflection reader fills fields by name, and the
			// Autolog screens wait for it.
			var names []string
			_ = json.Unmarshal(body, &names)
			s.writeWebJSON(w, r, map[string]any{"Friends": s.webBulk(names)})
		case path == "/Persona/" || strings.HasSuffix(path, "/updatePersona"):
			// create / update the PlayerProfile: answer with the stored profile
			if id != "" {
				s.writeWebJSON(w, r, s.webProfile(id))
				return
			}
			s.writeWebJSON(w, r, "{}")
		default:
			s.writeWebJSON(w, r, "{}")
		}
		return
	}
	switch {
	case strings.HasSuffix(path, "/getPlaylistMultiplier"):
		// reflection type XPMultiplier (loader 0x0089B6C0)
		s.writeWebJSON(w, r, map[string]any{"Multiplier": 1.0})
		return
	case strings.HasSuffix(path, "/getPlaylistIndex"):
		// reflection type WeeklyPlaylistIndex (loader 0x0089B660)
		s.writeWebJSON(w, r, map[string]any{"PlaylistIndex": 0})
		return
	}
	if m := webAutologRe.FindStringSubmatch(path); m != nil {
		switch m[5] {
		case "getNews":
			lang, _ := strconv.Atoi(m[4])
			news := newsForLanguage(s.loadNews(), lang)
			seen := s.newsViewed(m[1])
			for i := range news {
				if seen[news[i].NewsID] {
					news[i].Viewed = 1
				}
			}
			s.writeWebJSON(w, r, news)
		case "getAutologNewItems":
			unread := 0
			seen := s.newsViewed(m[1])
			lang, _ := strconv.Atoi(m[4])
			for _, n := range newsForLanguage(s.loadNews(), lang) {
				if !seen[n.NewsID] && n.Viewed == 0 {
					unread++
				}
			}
			s.writeWebJSON(w, r, map[string]any{"NewRecommends": 0, "NewSnapPhotos": 0, "NewDreamPhotos": 0, "NewNews": unread})
		case "getRunLeaderboard":
			s.writeWebJSON(w, r, s.webRunStandings(m[1], false))
		case "getTopRunLeaderboard":
			s.writeWebJSON(w, r, s.webRunStandings(m[1], true))
		case "getRaceMetaDataBlobs":
			// one RaceMetaDataBlob object (callback 0x008A7970 -> loader 0x0089B780)
			ev, _ := strconv.ParseInt(m[2], 10, 64)
			s.writeWebJSON(w, r, map[string]any{"BlazeId": m[1], "EventId": ev, "SplitTimesMS": s.web.blob(m[1], m[2])})
		default:
			s.writeWebJSON(w, r, "{}")
		}
		return
	}
	if m := webPersonaRe.FindStringSubmatch(path); m != nil {
		switch m[4] {
		case "":
			s.writeWebJSON(w, r, s.webProfile(m[1]))
		// Three questions with the same reply type (FriendCollection).
		case "getfriends":
			// the player's friends
			s.writeWebJSON(w, r, s.webFriends(m[1]))
		case "getRecommendedFriends":
			// people to add (the menu offers invite / ignore); never a friend
			s.writeWebJSON(w, r, s.webRecommendedFriends(m[1]))
		case "getCommonFriends":
			// /Persona/{me}/{other}/getCommonFriends: friends both players share
			s.writeWebJSON(w, r, s.webCommonFriends(m[1], m[2]))
		case "getSkillLevelRule":
			s.writeWebJSON(w, r, map[string]any{"BlazeId": m[1], "SkillLevel": 1})
		default:
			s.writeWebJSON(w, r, "{}")
		}
		return
	}
	s.writeWebJSON(w, r, "{}")
}

// withWebAutolog adds V8ServerName (the web Autolog host) to NFS12_THERUN when
// the .cfg does not set it; without it the game waits for the dead EA host.
func (s *Server) withWebAutolog(conf map[string]string) map[string]string {
	if v := strings.TrimSpace(conf["V8ServerName"]); v != "" {
		if !strings.Contains(strings.ToLower(v), "example.") {
			return conf
		}
		// the commented example line from the .cfg was enabled
		s.log.Printf("[WEB] client_config V8ServerName=%q is the example value; ignored, sending the server's own host", v)
	}
	host := strings.TrimSpace(s.cfg.WebAutologHost)
	if strings.EqualFold(host, "off") {
		return conf
	}
	if host == "" {
		host = s.cfg.PublicHost
		if host == "" || host == "127.0.0.1" {
			return conf
		}
		host += ":18443"
	}
	out := map[string]string{}
	for k, v := range conf {
		out[k] = v
	}
	out["V8ServerName"] = host
	return out
}

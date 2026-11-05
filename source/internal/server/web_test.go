package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nfsrun-server/internal/database"
)

func newWebTest(t *testing.T) (*Server, *httptest.Server, *syncBuf) {
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	cfg.ClientGuard = true
	fs := &fakeStore{
		per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"},
		others: []database.PersonaRef{
			{ID: 1, BlazeID: 1000001, Name: "Player1"},
			{ID: 2, BlazeID: 1000002, Name: "Rival"},
			{ID: 3, BlazeID: 1000003, Name: "NFS-DS-01"},
		},
		entries: []database.BoardEntry{
			{Board: "race_single", EventID: 7, PersonaID: 1, Score: 1000},
			{Board: "race_single", EventID: 7, PersonaID: 2, Score: 1200},
		},
	}
	s := New(cfg, fs, log.New(&logs, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/Persona/", s.handleWebHTTP)
	mux.HandleFunc("/Autolog/", s.handleWebHTTP)
	srv := httptest.NewServer(s.httpGuard(s.logHTTP(mux)))
	t.Cleanup(srv.Close)
	return s, srv, &logs
}

func webGet(t *testing.T, srv *httptest.Server, path string, out any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-SESSION-KEY", "sk")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || r.ContentLength != int64(len(b)) || len(r.TransferEncoding) != 0 {
		t.Fatalf("%s: status %d len %d/%d te=%v", path, r.StatusCode, r.ContentLength, len(b), r.TransferEncoding)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v %s", path, err, b)
		}
	}
	return r
}

func TestWebNewsDefault(t *testing.T) {
	s, srv, _ := newWebTest(t)
	var news []map[string]any
	webGet(t, srv, "/Autolog/1000001/0/0/2/getNews", &news)
	// The default item exists in every language; 2 = German
	if len(news) != 1 || news[0]["Subject"] != "Autolog ist zurück!" || news[0]["Category"].(float64) != 3 ||
		news[0]["Language"].(float64) != 2 || news[0]["NewsId"].(float64) != 1 {
		t.Fatalf("news %v", news)
	}
	for _, k := range []string{"NewsId", "PublishedDate", "Author", "Subject", "ShortMessage", "Message", "NewsIcon", "MetaContent", "Territory", "Language", "Viewed", "Category", "DateNumber"} {
		if _, ok := news[0][k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
	// the example file was written and edits are served
	path := filepath.Join(filepath.Dir(s.cfg.LockerDir), "news.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`[{"NewsId":5,"Subject":"Edited","Category":1},{"NewsId":6,"Subject":"Two"}]`), 0644)
	webGet(t, srv, "/Autolog/1000001/0/0/0/getNews", &news)
	if len(news) != 2 || news[0]["Subject"] != "Edited" {
		t.Fatalf("edited news %v", news)
	}
	var items map[string]any
	webGet(t, srv, "/Autolog/1000001/0/0/0/getAutologNewItems", &items)
	if items["NewNews"].(float64) != 2 {
		t.Fatalf("items %v", items)
	}
}

func TestWebProfileFriendsAndBadge(t *testing.T) {
	_, srv, logs := newWebTest(t)
	var p map[string]any
	webGet(t, srv, "/Persona/1000001", &p)
	if p["PlayerProfileId"] != "1000001" || p["Persona"] != "Player1" || p["speedwallPostsCount"].(float64) != 1 || p["speedwallFirstPlaceCount"].(float64) != 1 {
		t.Fatalf("profile %v", p)
	}
	// updateBadge is stored and reflected
	req, _ := http.NewRequest("POST", srv.URL+"/Persona/1000001/updateBadge", strings.NewReader(`{"badgeIcon":"ICON-1","badgeMotto":"M","badgeBackground":"B"}`))
	req.Header.Set("X-SESSION-KEY", "sk")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("post %v %v", r, err)
	}
	webGet(t, srv, "/Persona/1000001", &p)
	if p["IconGuid"] != "ICON-1" || p["MottoGuid"] != "M" || p["BackgroundGuid"] != "B" {
		t.Fatalf("badge %v", p)
	}
	var fc struct{ Friends []map[string]any }
	webGet(t, srv, "/Persona/1000001/getfriends", &fc)
	if len(fc.Friends) != 1 || fc.Friends[0]["Name"] != "Rival" || fc.Friends[0]["Id"] != "1000002" {
		t.Fatalf("friends %v", fc)
	}
	if !strings.Contains(logs.String(), "[WEB] GET /Persona/1000001/getfriends") {
		t.Fatal(logs.String())
	}
}

func TestWebGuardNeedsSessionKey(t *testing.T) {
	_, srv, _ := newWebTest(t)
	r, _ := http.Get(srv.URL + "/Persona/1000001")
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("browser without key: %d", r.StatusCode)
	}
}

func TestClientConfigInjectsV8ServerName(t *testing.T) {
	s := New(testConfig(), &fakeStore{}, log.New(io.Discard, "", 0))
	s.cfg.PublicHost = "redirector.example.com"
	if got := s.withWebAutolog(nil)["V8ServerName"]; got != "redirector.example.com:18443" {
		t.Fatalf("default %q", got)
	}
	if got := s.withWebAutolog(map[string]string{"V8ServerName": "x:1"})["V8ServerName"]; got != "x:1" {
		t.Fatalf("cfg file must win: %q", got)
	}
	if got := s.withWebAutolog(map[string]string{"V8ServerName": "redirector.example.com:18443"})["V8ServerName"]; got != "redirector.example.com:18443" {
		t.Fatalf("placeholder: %q", got)
	}
	s.cfg.PublicHost = "redirector.private.test"
	if got := s.withWebAutolog(map[string]string{"V8ServerName": "redirector.example.com:18443"})["V8ServerName"]; got != "redirector.private.test:18443" {
		t.Fatalf("placeholder must be replaced: %q", got)
	}
	s.cfg.WebAutologHost = "off"
	if _, ok := s.withWebAutolog(map[string]string{"a": "b"})["V8ServerName"]; ok {
		t.Fatal("off")
	}
	s.cfg.WebAutologHost = "web.example.com:443"
	if got := s.withWebAutolog(nil)["V8ServerName"]; got != "web.example.com:443" {
		t.Fatalf("explicit %q", got)
	}
}

// Every route arrives under "/NFS12_A_01/Game/pc".
func TestWebPrefixedRoutes(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Ze1"},
		others: []database.PersonaRef{{ID: 1, BlazeID: 1000001, Name: "Ze1"}, {ID: 2, BlazeID: 1000002, Name: "Ze2"}}}
	s := New(cfg, fs, log.New(&logs, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.httpNotFound)
	srv := httptest.NewServer(s.httpGuard(s.logHTTP(mux)))
	defer srv.Close()
	base := "/NFS12_A_01/Game/pc"
	var p map[string]any
	webGet(t, srv, base+"/Persona/1000001", &p)
	if p["Persona"] != "Ze1" {
		t.Fatalf("profile %v", p)
	}
	var fc struct{ Friends []map[string]any }
	webGet(t, srv, base+"/Persona/1000001/getfriends", &fc)
	if len(fc.Friends) != 1 {
		t.Fatalf("friends %v", fc)
	}
	var xp map[string]float64
	webGet(t, srv, base+"/Autolog/getPlaylistMultiplier", &xp)
	if xp["Multiplier"] != 1 {
		t.Fatal(xp)
	}
	var wi map[string]float64
	webGet(t, srv, base+"/Autolog/getPlaylistIndex", &wi)
	if _, ok := wi["PlaylistIndex"]; !ok {
		t.Fatal(wi)
	}
	// updatePersona with Persona:"" keeps the name, stores the rest
	req, _ := http.NewRequest("POST", srv.URL+base+"/Persona/updatePersona", strings.NewReader(`{"PlayerProfileId":"1000001","Persona":"","playerXP":18435,"playerLevel":6}`))
	req.Header.Set("X-SESSION-KEY", "sk")
	r, err := http.DefaultClient.Do(req)
	if err != nil || r.StatusCode != 200 {
		t.Fatal(r, err)
	}
	webGet(t, srv, base+"/Persona/1000001", &p)
	if p["Persona"] != "Ze1" || p["playerXP"].(float64) != 18435 || p["playerLevel"].(float64) != 6 {
		t.Fatalf("after update %v", p)
	}
	var news []map[string]any
	webGet(t, srv, base+"/Autolog/1000001/0/0/0/getNews", &news)
	if len(news) != 1 || strings.Contains(logs.String(), "TODO unhandled") {
		t.Fatalf("news %v\n%s", news, logs.String())
	}
}

func TestWebBulkSetReturnsFriends(t *testing.T) {
	_, srv, _ := newWebTest(t)
	req, _ := http.NewRequest("POST", srv.URL+"/Persona/1000002/bulkSet", strings.NewReader(`["Player1","nobody"]`))
	req.Header.Set("X-SESSION-KEY", "sk")
	r, err := http.DefaultClient.Do(req)
	if err != nil || r.StatusCode != 200 {
		t.Fatal(r, err)
	}
	var fc struct{ Friends []map[string]any }
	if err := json.NewDecoder(r.Body).Decode(&fc); err != nil || len(fc.Friends) != 1 || fc.Friends[0]["Id"] != "1000001" || fc.Friends[0]["Name"] != "Player1" {
		t.Fatalf("%v %v", fc, err)
	}
}

func TestWebSplitTimesRunBoardAndNewsViewed(t *testing.T) {
	_, srv, _ := newWebTest(t)
	post := func(path, body string) {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-SESSION-KEY", "sk")
		if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
			t.Fatal(path, r, err)
		}
	}
	// exact (non-JSON) shape the game posts
	post("/Autolog/setRaceMetaDataBlobList", `{"RaceMetaDataBlobs" : [{"BlazeId" : {1000001}, "EventId" : {-1721928781}, "SplitTimesMS" : ["23","36","76"] }, {"BlazeId" : {1000001}, "EventId" : {314130284}, "SplitTimesMS" : [] }]}`)
	var blob map[string]any
	webGet(t, srv, "/Autolog/1000001/-1721928781/getRaceMetaDataBlobs", &blob)
	if blob["BlazeId"] != "1000001" || blob["EventId"].(float64) != -1721928781 || len(blob["SplitTimesMS"].([]any)) != 3 {
		t.Fatalf("blob %v", blob)
	}
	webGet(t, srv, "/Autolog/1000001/314130284/getRaceMetaDataBlobs", &blob)
	if len(blob["SplitTimesMS"].([]any)) != 0 {
		t.Fatalf("empty blob %v", blob)
	}
	var items map[string]float64
	webGet(t, srv, "/Autolog/1000001/0/0/0/getAutologNewItems", &items)
	if items["NewNews"] != 1 {
		t.Fatal(items)
	}
	post("/Autolog/1000001/1/setNewsViewed", "")
	webGet(t, srv, "/Autolog/1000001/0/0/0/getAutologNewItems", &items)
	var news []map[string]any
	webGet(t, srv, "/Autolog/1000001/0/0/0/getNews", &news)
	if items["NewNews"] != 0 || news[0]["Viewed"].(float64) != 1 {
		t.Fatalf("viewed %v %v", items, news)
	}
	var top []map[string]any
	webGet(t, srv, "/Autolog/1000001/getTopRunLeaderboard", &top)
	if top == nil {
		t.Fatal("nil")
	}
}

func TestWebPlaylistUserInfo(t *testing.T) {
	_, srv, _ := newWebTest(t)
	req, _ := http.NewRequest("POST", srv.URL+"/Persona/getPlaylistUserInfo", strings.NewReader(`["1000001","1000002","9"]`))
	req.Header.Set("X-SESSION-KEY", "sk")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if json.NewDecoder(r.Body).Decode(&list) != nil || len(list) != 2 || list[1]["Persona"] != "Rival" {
		t.Fatalf("%v", list)
	}
}

// Getfriends, getRecommendedFriends and getCommonFriends answer
// three different questions (PS3 ClientFriendsService / WebService*Request).
func TestWebThreeFriendRoutesDiffer(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	cfg.FriendsAll = false
	fs := &fakeStore{
		per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Me"},
		others: []database.PersonaRef{
			{ID: 1, BlazeID: 1000001, Name: "Me"},
			{ID: 2, BlazeID: 1000002, Name: "Friend"},
			{ID: 3, BlazeID: 1000003, Name: "FriendOfFriend"},
			{ID: 4, BlazeID: 1000004, Name: "Stranger"},
			{ID: 5, BlazeID: 1000005, Name: "Shared"},
		},
		friends: map[uint64][]uint64{1: {2, 5}, 2: {1, 3, 5}, 3: {2}, 5: {1, 2}},
	}
	s := New(cfg, fs, log.New(&logs, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/Persona/", s.handleWebHTTP)
	srv := httptest.NewServer(s.logHTTP(mux))
	t.Cleanup(srv.Close)
	names := func(path string) []string {
		var fc struct{ Friends []map[string]any }
		webGet(t, srv, path, &fc)
		var out []string
		for _, f := range fc.Friends {
			out = append(out, f["Name"].(string))
		}
		return out
	}
	if got := strings.Join(names("/Persona/1000001/getfriends"), ","); got != "Friend,Shared" {
		t.Fatal("getfriends:", got)
	}
	if got := strings.Join(names("/Persona/1000001/getRecommendedFriends"), ","); got != "FriendOfFriend" {
		t.Fatal("getRecommendedFriends must hold only non-friends:", got)
	}
	if got := strings.Join(names("/Persona/1000001/1000002/getCommonFriends"), ","); got != "Shared" {
		t.Fatal("getCommonFriends:", got)
	}
	// ignoreFriendReco removes the recommendation
	req, _ := http.NewRequest("POST", srv.URL+"/Persona/1000001/1000003/ignoreFriendReco", strings.NewReader(""))
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatal(err)
	}
	if got := names("/Persona/1000001/getRecommendedFriends"); len(got) != 0 {
		t.Fatal("ignored reco still listed:", got)
	}
}

func TestWebProfileOnlyFromPersonaRoutes(t *testing.T) {
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	s := New(cfg, &fakeStore{}, log.New(io.Discard, "", 0))
	post := func(path, body string) {
		rec := httptest.NewRecorder()
		s.handleWebHTTP(rec, httptest.NewRequest("POST", "/NFS12_A_01/Game/pc"+path, strings.NewReader(body)))
	}
	// the game's broken-JSON badge body
	post("/Persona/1000003/updateBadge", `{"badgeIcon":"EBA6C05D-B968-CA88-AD1F-D8B6A46AD1FA",{"badgeMotto":"00000000-0000-0000-0000-000000000000",{"badgeBackground":"525F7988-5BF4-1835-0D54-F2B88DE71DCF"}`)
	post("/Autolog/updateAutologChallengeStat", `{"BlazeId":"1000003","EventId":-1176584950,"BestMedal":4}`)
	m := s.web.load("1000003")
	if m["badgeBackground"] != "525F7988-5BF4-1835-0D54-F2B88DE71DCF" {
		t.Fatal("badge not stored:", m)
	}
	if _, bad := m["EventId"]; bad {
		t.Fatal("Autolog challenge stat merged into the profile:", m)
	}
	if p := s.webProfile("1000003"); p["BackgroundGuid"] != "525F7988-5BF4-1835-0D54-F2B88DE71DCF" {
		t.Fatal(p["BackgroundGuid"])
	}
}

func TestNewsForLanguage(t *testing.T) {
	all := []broadcastNews{
		{NewsID: 1, Subject: "en", Language: 0}, {NewsID: 1, Subject: "pt", Language: 9},
		{NewsID: 2, Subject: "fr only", Language: 1},
		{NewsID: 3, Subject: "en3"},
	}
	got := newsForLanguage(all, 9)
	if len(got) != 3 || got[0].Subject != "pt" || got[1].Subject != "fr only" || got[2].Subject != "en3" || got[2].Language != 9 {
		t.Fatalf("%+v", got)
	}
	if got := newsForLanguage(all, 5); got[0].Subject != "en" {
		t.Fatalf("fallback %+v", got)
	}
	if len(defaultNews()) != len(newsLanguages) {
		t.Fatal("default news must cover every language")
	}
}

func TestNewsIconPath(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "news/nfs.jpg": "/news/nfs.jpg", "/news/nfs.jpg": "/news/nfs.jpg",
		`news\nfs.jpg`: "/news/nfs.jpg", " News/A.jpg ": "/News/A.jpg",
		"https://web.example.org:18443/news/a.jpg": "/news/a.jpg", "https://web.example.org": "",
	} {
		if got := newsIconPath(in); got != want {
			t.Errorf("newsIconPath(%q) = %q, want %q", in, got, want)
		}
	}
}

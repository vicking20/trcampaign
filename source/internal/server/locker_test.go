package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Shape of the live createContent.
func liveCreateContent(t *testing.T) []blaze.Field {
	var w blaze.Writer
	writeStructListHeader(&w, "ATTR", 3)
	for _, kv := range [][2]string{{"carName", "ID_CARNAME_SHORT_MAZDA_MX5"}, {"location", "ID_STAGE_LOCATION_YOSEMITEAPPROACH,ID_STAGE_STATE_CA"}, {"userLiked", "1"}} {
		w.TagString("NAME", kv[0])
		w.TagString("VALU", kv[1])
		w.GroupEnd()
	}
	w.TagString("CCAT", "SNAPSHOTS")
	w.TagVar("COID", 0)
	w.TagString("DESC", "")
	w.TagVar("EDAT", 0)
	w.TagString("NAME", "NFSTheRun")
	w.TagVar("PERM", 0)
	w.Tag("SUBL", blaze.TDFList)
	w.WriteByte(byte(blaze.TDFString))
	w.Var(1)
	w.Var(7)
	w.WriteString("client")
	w.WriteByte(0)
	return decodeOK(t, w.Bytes())
}

func TestLockerSnapshotFlow(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = t.TempDir()
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"}}
	s := New(cfg, fs, log.New(&logs, "", 0))
	st := &connState{Persona: fs.per}

	// 1. createContent -> INFO with CID, PENDING, upload URLs
	resp := s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerCreateContent}, liveCreateContent(t), st)
	info := usGroup(t, decodeOK(t, resp.Payload), "INFO")
	assertSorted(t, info, "ContentInfo")
	if fieldUintAuth(info, "CID") != 1 || fieldUintAuth(info, "STTS") != 1 || blaze.FindString(info, "CCAT") != "SNAPSHOTS" || fieldUintAuth(info, "EID") != 1000001 {
		t.Fatalf("info=%v", info)
	}
	var subUpload string
	for _, f := range info {
		if f.Tag == "SUBL" {
			m := f.Value.(map[string]any)
			items := m["items"].([][2]any)
			if len(items) != 1 || items[0][0].(string) != "client" {
				t.Fatalf("SUBL %v", items)
			}
			subUpload = blaze.FindString(items[0][1].([]blaze.Field), "UURL")
		}
	}
	if !strings.Contains(subUpload, "/locker/upload/1/0123456789abcdef0123456789abcdef/client") {
		t.Fatalf("upload url %q", subUpload)
	}

	// 2. HTTP PUT upload (wrong token refused), then GET
	mux := http.NewServeMux()
	mux.HandleFunc("/locker/", s.handleLockerHTTP)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	path := subUpload[strings.Index(subUpload, "/locker/"):]
	bad, _ := http.NewRequest("PUT", srv.URL+strings.Replace(path, "0123456789abcdef", "ffffffffffffffff", 1), strings.NewReader("x"))
	if r, _ := http.DefaultClient.Do(bad); r.StatusCode != http.StatusForbidden {
		t.Fatalf("bad token status %d", r.StatusCode)
	}
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 20000)...)
	req, _ := http.NewRequest("PUT", srv.URL+path, bytes.NewReader(jpeg))
	req.Header.Set("Content-Type", "image/jpeg")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("upload %v %v", r, err)
	}

	// 3. confirmUpload -> ACTIVE with size
	var cw blaze.Writer
	cw.GroupStart("INFO")
	cw.TagVar("CID", 1)
	cw.GroupEnd()
	cw.TagVar("STST", 0)
	resp = s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerConfirmUpload}, decodeOK(t, cw.Bytes()), st)
	info = usGroup(t, decodeOK(t, resp.Payload), "INFO")
	if fieldUintAuth(info, "STTS") != 0 || fieldUintAuth(info, "SIZE") != uint64(len(jpeg)) || blaze.FindString(info, "UURL") != "" {
		t.Fatalf("confirmed info=%v", info)
	}

	// 4. ListContent -> LKRS with the snapshot
	var lw blaze.Writer
	lw.TagString("CCAT", "SNAPSHOTS")
	lw.TagVar("EID", 1000001)
	lw.TagVar("MXRC", 4294967295)
	resp = s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerListContent}, decodeOK(t, lw.Bytes()), st)
	lf := decodeOK(t, resp.Payload)
	lk := fieldStructList(lf, "LKRS")
	if len(lk) != 1 || fieldUintAuth(lk[0], "CID") != 1 || fieldUintAuth(lf, "TCNT") != 1 {
		t.Fatalf("list=%v", lf)
	}
	// 4b. listContentForUsers (EID [1000001]) -> map id -> ContentInfos
	var uw blaze.Writer
	uw.TagString("CCAT", "SNAPSHOTS")
	uw.Tag("EID", blaze.TDFList)
	uw.WriteByte(byte(blaze.TDFVarInt))
	uw.Var(2)
	uw.Var(1000009) // unknown player: empty entry
	uw.Var(1000001)
	uw.TagVar("MXRC", 4294967295)
	uw.TagVar("REFF", 1)
	resp = s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerListContentForUsers}, decodeOK(t, uw.Bytes()), st)
	uf := decodeOK(t, resp.Payload)
	if fieldUintAuth(uf, "TCNT") != 1 {
		t.Fatalf("for users=%v", uf)
	}
	for _, f := range uf {
		if f.Tag != "LKRS" {
			continue
		}
		items := f.Value.(map[string]any)["items"].([][2]any)
		if len(items) != 2 || items[0][0].(uint64) != 1000001 || items[1][0].(uint64) != 1000009 {
			t.Fatalf("map %v", items)
		}
		own := fieldStructList(items[0][1].([]blaze.Field), "LKRS")
		other := fieldStructList(items[1][1].([]blaze.Field), "LKRS")
		if len(own) != 1 || fieldUintAuth(own[0], "CID") != 1 || len(other) != 0 {
			t.Fatalf("entries %v / %v", own, other)
		}
	}
	get := blaze.FindString(lk[0], "GURL")
	gr, err := http.Get(srv.URL + get[strings.Index(get, "/locker/"):] + "x"[:0])
	if err != nil {
		t.Fatal(err)
	}
	// main sub was not uploaded, client sub was
	if gr.StatusCode != 404 {
		t.Fatalf("main get status %d", gr.StatusCode)
	}
	cr, _ := http.Get(srv.URL + "/locker/get/1/client")
	body, _ := io.ReadAll(cr.Body)
	if cr.StatusCode != 200 || !bytes.Equal(body, jpeg) || cr.Header.Get("Content-Type") != "image/jpeg" ||
		cr.ContentLength != int64(len(jpeg)) || len(cr.TransferEncoding) != 0 {
		t.Fatalf("client get %d %q", cr.StatusCode, cr.Header.Get("Content-Type"))
	}
}

func TestMiscEndpoints(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, AccountID: 9, BlazeID: 1000001, Name: "Player1", SessionKey: "sk"}}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	st := &connState{Persona: fs.per, Email: "eatest.Player1@ea.com"}

	// updateNetworkInfo -> stored + ExtendedDataUpdate notification with ADDR union
	var w blaze.Writer
	w.UnionStart("ADDR", 2)
	w.GroupStart("VALU")
	w.GroupStart("EXIP")
	w.TagVar("IP", 0)
	w.TagVar("PORT", 0)
	w.GroupEnd()
	w.GroupStart("INIP")
	w.TagVar("IP", 3232238188)
	w.TagVar("PORT", 3659)
	w.GroupEnd()
	w.GroupEnd()
	w.GroupStart("NQOS")
	w.TagVar("DBPS", 0)
	w.TagVar("NATT", 4)
	w.TagVar("UBPS", 0)
	w.GroupEnd()
	resp, ok := s.handleMisc(&blaze.Packet{Component: ComponentUserSessions, Command: USUpdateNetworkInfo}, decodeOK(t, w.Bytes()), st)
	if !ok || resp.Error != 0 || st.Net.IntIP != 3232238188 || st.Net.IntPort != 3659 || st.Net.NATT != 4 || len(st.Pending) != 1 {
		t.Fatalf("net=%+v pending=%d", st.Net, len(st.Pending))
	}
	nf := decodeOK(t, st.Pending[0].Payload)
	data := usGroup(t, nf, "DATA")
	assertSorted(t, data, "ExtendedData")
	if fieldUintAuth(nf, "USID") != 1000001 || st.Pending[0].Command != NotifyUserSessionExtendedDataUpdate {
		t.Fatalf("notify %v", nf)
	}

	// resumeSession
	var rw blaze.Writer
	rw.TagString("SKEY", "sk")
	st2 := &connState{}
	resp, _ = s.handleMisc(&blaze.Packet{Component: ComponentUserSessions, Command: USResumeSession}, decodeOK(t, rw.Bytes()), st2)
	if resp.Error != 0 || !st2.Authenticated || st2.Persona.Name != "Player1" || len(st2.Pending) != 2 {
		t.Fatalf("resume %+v", st2)
	}
	var bad blaze.Writer
	bad.TagString("SKEY", "nope")
	if resp, _ = s.handleMisc(&blaze.Packet{Component: ComponentUserSessions, Command: USResumeSession}, decodeOK(t, bad.Bytes()), &connState{}); resp.Error == 0 {
		t.Fatal("bad key must fail")
	}

	// telemetry, account, messages, setUsersToList
	for _, c := range []struct {
		comp, cmd uint16
		tag       string
	}{{0x0009, 0x0005, "ADRS"}, {0x0001, 0x001E, "MAIL"}, {0x000F, 0x0002, "MCNT"}, {ComponentAssociationLists, 0x0004, "LMID"}} {
		resp, ok := s.handleMisc(&blaze.Packet{Component: c.comp, Command: c.cmd}, nil, st)
		fs := decodeOK(t, resp.Payload)
		assertSorted(t, fs, c.tag)
		found := false
		for _, f := range fs {
			if f.Tag == c.tag {
				found = true
			}
		}
		if !ok || !found {
			t.Fatalf("%x/%x missing %s: %v", c.comp, c.cmd, c.tag, fs)
		}
	}
}

// The client appends "&pers=<BlazeId>" to the get URL.
func TestLockerURLsAcceptClientSuffix(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = t.TempDir()
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"}}
	s := New(cfg, fs, log.New(&logs, "", 0))
	st := &connState{Persona: fs.per}
	resp := s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerCreateContent}, liveCreateContent(t), st)
	info := usGroup(t, decodeOK(t, resp.Payload), "INFO")
	if g := blaze.FindString(info, "GURL"); !strings.HasSuffix(g, "/locker/get/1/main?v=1") {
		t.Fatalf("GURL %q", g)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/locker/", s.handleLockerHTTP)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// upload with the new URL form + appended parameter
	req, _ := http.NewRequest("PUT", srv.URL+"/locker/upload/1/0123456789abcdef0123456789abcdef/client?v=1&pers=1000001", strings.NewReader("JPEGDATA"))
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("upload %v %v", r, err)
	}
	// both URL shapes the client produces
	for _, u := range []string{"/locker/get/1/client?v=1&pers=1000001", "/locker/get/1/client&pers=1000001"} {
		r, err := http.Get(srv.URL + u)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("%s -> %v %v", u, r, err)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "JPEGDATA" {
			t.Fatalf("%s body %q", u, b)
		}
	}
	r, _ := http.Get(srv.URL + "/locker/get/1/main&pers=1000001")
	if r.StatusCode != 404 || !strings.Contains(logs.String(), "nothing uploaded for this part yet") {
		t.Fatalf("missing part: %d log=%s", r.StatusCode, logs.String())
	}
}

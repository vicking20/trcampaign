package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
)

func pgCreateWriter(name string) *blaze.Writer {
	var w blaze.Writer
	w.TagVar("JOIN", 0)
	w.GroupStart("PGRP")
	w.TagVar("ENBV", 1)
	w.UnionStart("HNET", 2)
	writeIPPairValue(&w, netInfo{Set: true, ExtIP: 767154415, ExtPort: 3659, IntIP: 3232238187, IntPort: 3659})
	w.TagVar("HSID", 0)
	w.TagVar("JOIN", 0)
	w.TagVar("MLIM", 4)
	w.TagString("NAME", name)
	w.TagVar("NTOP", 0)
	w.TagVar("OWNR", 0)
	w.TagVar("PGID", 0)
	w.TagVar("PRES", 2)
	w.TagString("UKEY", "")
	w.TagVar("UPRS", 0)
	w.TagString("UUID", "")
	w.TagVar("VOIP", 2)
	w.TagBlob("XNNC", nil)
	w.TagBlob("XSES", nil)
	w.GroupEnd()
	return &w
}

func TestPlaygroupCreateJoinLeave(t *testing.T) {
	var logs syncBuf
	s := New(testConfig(), &multiStore{}, log.New(&logs, "", 0))
	a := connectAndLogin(t, s, "Player1")
	b := connectAndLogin(t, s, "Rival")

	r := a.call(ComponentPlaygroups, PGCreate, pgCreateWriter("Player1"))
	if r.Error != 0 {
		t.Fatalf("create error %x", r.Error)
	}
	info := fieldGroupOf(decodeOK(t, r.Payload), "INFO")
	pgid := fieldUintAuth(info, "PGID")
	if pgid != 1000001 || fieldUintAuth(info, "OWNR") != 1000001 || fieldUintAuth(info, "MLIM") != 4 || blaze.FindString(info, "UKEY") == "" {
		t.Fatalf("create info %v", info)
	}
	nj := decodeOK(t, a.waitNotify(ComponentPlaygroups, PGNotifyJoin).Payload)
	if fieldUintAuth(nj, "USER") != 1000001 || len(fieldStructList(nj, "MLST")) != 1 {
		t.Fatalf("NotifyJoinPlaygroup %v", nj)
	}
	if _, joinable, ok := s.pgStatus(1000001); !ok || !joinable {
		t.Fatal("leader should be joinable")
	}

	// join by the Origin session id (= PGID = creator's id) given as USER.ID
	var jw blaze.Writer
	jw.TagVar("PGID", 0)
	jw.UnionStart("PNET", 2)
	writeIPPairValue(&jw, netInfo{Set: true, IntIP: 0xC0A80A02, IntPort: 3659})
	jw.TagString("UKEY", "")
	jw.GroupStart("USER")
	jw.TagVar("ID", 1000001)
	jw.GroupEnd()
	if r := b.call(ComponentPlaygroups, PGJoin, &jw); r.Error != 0 {
		t.Fatalf("join error %x", r.Error)
	}
	nb := decodeOK(t, b.waitNotify(ComponentPlaygroups, PGNotifyJoin).Payload)
	if m := fieldStructList(nb, "MLST"); len(m) != 2 {
		t.Fatalf("joiner sees %d members", len(m))
	}
	nm := decodeOK(t, a.waitNotify(ComponentPlaygroups, PGNotifyMemberJoined).Payload)
	if fieldUintAuth(nm, "PGID") != pgid || fieldUintAuth(fieldGroupOf(fieldGroupOf(nm, "MEMB"), "USER"), "ID") != 1000002 {
		t.Fatalf("member joined %v", nm)
	}

	// attributes reach the other member
	var aw blaze.Writer
	writeKVMap(&aw, "ATTR", []kv{{"GID", "77"}})
	aw.TagVar("PGID", pgid)
	a.call(ComponentPlaygroups, PGSetAttributes, &aw)
	if f := decodeOK(t, b.waitNotify(ComponentPlaygroups, PGNotifyAttributesSet).Payload); fieldUintAuth(f, "PGID") != pgid {
		t.Fatalf("attrs %v", f)
	}

	// the leader leaves: Rival is told and becomes leader
	a.call(ComponentPlaygroups, PGLeave, nil)
	if f := decodeOK(t, b.waitNotify(ComponentPlaygroups, PGNotifyMemberRemoved).Payload); fieldUintAuth(f, "MLST") != 1000001 {
		t.Fatalf("removed %v", f)
	}
	if f := decodeOK(t, b.waitNotify(ComponentPlaygroups, PGNotifyLeaderChange).Payload); fieldUintAuth(f, "LID") != 1000002 {
		t.Fatalf("leader %v", f)
	}
	b.call(ComponentPlaygroups, PGLeave, nil)
	b.waitNotify(ComponentPlaygroups, PGNotifyDestroy)
	if _, _, ok := s.pgStatus(1000002); ok {
		t.Fatal("group should be gone")
	}
	if !strings.Contains(logs.String(), "createPlaygroup") {
		t.Fatal(logs.String())
	}
}

func TestPlaygroupJoinUnknownFails(t *testing.T) {
	s := New(testConfig(), &multiStore{}, log.New(&bytes.Buffer{}, "", 0))
	b := connectAndLogin(t, s, "Rival")
	var jw blaze.Writer
	jw.TagVar("PGID", 12345)
	if r := b.call(ComponentPlaygroups, PGJoin, &jw); r.Error == 0 {
		t.Fatal("join of a missing group must fail")
	}
}

func TestOriginInviteRelay(t *testing.T) {
	s := New(testConfig(), &multiStore{}, log.New(&bytes.Buffer{}, "", 0))
	post := func(h http.HandlerFunc, body string) int {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		return rec.Code
	}
	if c := post(s.handlePresenceHTTP, `{"persona":"Player1","presence":"JOINABLE","sessionId":"1000001"}`); c != 200 {
		t.Fatal(c)
	}
	if c := post(s.handleInviteHTTP, `{"persona":"Player1","from":1000001,"to":[1000002],"session":""}`); c != 200 {
		t.Fatal(c)
	}
	inv := s.invites.take(1000002)
	if len(inv) != 1 || inv[0].Session != "1000001" || inv[0].From != 1000001 {
		t.Fatalf("invites %+v", inv)
	}
	b, _ := json.Marshal(inv)
	if !strings.Contains(string(b), `"session":"1000001"`) {
		t.Fatal(string(b))
	}
	if p, sess := s.playerPresence("Player1", 1000001, true); p != "JOINABLE" || sess != "1000001" {
		t.Fatalf("presence %s %s", p, sess)
	}
	if p, _ := s.playerPresence("Player1", 1000001, false); p != "OFFLINE" {
		t.Fatal(p)
	}
}

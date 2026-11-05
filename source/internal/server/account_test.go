package server

import (
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

func TestUpdateAccountOptins(t *testing.T) {
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	s := New(cfg, &fakeStore{}, log.New(io.Discard, "", 0))
	st := &connState{Persona: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "P"}}
	update := func(opt1, opt3 []byte) {
		// raw Heat2 ints so that -1 (0x41) is encoded as the client does
		var w blaze.Writer
		w.TagString("MAIL", "")
		w.Tag("OPT1", blaze.TDFVarInt)
		w.Write(opt1)
		w.Tag("OPT3", blaze.TDFVarInt)
		w.Write(opt3)
		w.TagString("PASS", "")
		w.TagVar("UID", 1)
		fs, err := blaze.DecodeAll(w.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		resp := s.handleUpdateAccount(&blaze.Packet{Component: 1, Command: 0x14}, fs, st)
		out, _ := blaze.DecodeAll(resp.Payload)
		if len(out) != 1 || out[0].Tag != "PCTK" {
			t.Fatal(out)
		}
	}
	account := func() (uint64, uint64) {
		fs, _ := blaze.DecodeAll(buildAccountInfoOpt(s, st))
		return fieldUint(fs, "GOPT"), fieldUint(fs, "TPOT")
	}
	if g, p := account(); g != 0 || p != 0 {
		t.Fatal(g, p)
	}
	update([]byte{0x01}, []byte{0x01}) // OPT1 1, OPT3 1
	if g, p := account(); g != 1 || p != 1 {
		t.Fatal(g, p)
	}
	update([]byte{0x41}, []byte{0x00}) // -1 = unchanged, 0 = off
	if g, p := account(); g != 1 || p != 0 {
		t.Fatal(g, p)
	}
}

func buildAccountInfoOpt(s *Server, st *connState) []byte {
	g, p := s.accountOptins(st)
	return buildAccountInfo(st, time.Now(), g, p)
}

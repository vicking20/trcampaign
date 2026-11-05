package server

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// writeSample fills every member of typ with a plausible value (depth-limited),
// so every handler sees a request shaped like the game's own.
func writeSample(sc *blaze.Schema, w *blaze.Writer, typ string, depth int) {
	for _, m := range sc.Members(typ) {
		writeSampleMember(sc, w, m.Tag, m.Spec, depth)
	}
}

func writeSampleMember(sc *blaze.Schema, w *blaze.Writer, tag, spec string, depth int) {
	switch {
	case strings.Contains(spec, "?"):
		return
	case spec == "int":
		w.TagVar(tag, 1)
	case spec == "string":
		w.TagString(tag, "x")
	case spec == "blob":
		w.TagBlob(tag, []byte{1})
	case spec == "float":
		w.TagFloat(tag, 1)
	case spec == "objtype":
		w.Tag(tag, blaze.TDFPair)
		w.Var(1)
		w.Var(1)
	case spec == "objid":
		w.TagTriple(tag, 1, 1, 1)
	case strings.HasPrefix(spec, "struct:"):
		if depth > 3 {
			return
		}
		w.GroupStart(tag)
		writeSample(sc, w, strings.TrimPrefix(spec, "struct:"), depth+1)
		w.GroupEnd()
	case strings.HasPrefix(spec, "union:"):
		w.UnionStart(tag, 0x7f)
	case strings.HasPrefix(spec, "list:"):
		elem := strings.TrimPrefix(spec, "list:")
		switch {
		case elem == "int":
			writeVarList(w, tag, []uint64{1})
		case elem == "string":
			w.Tag(tag, blaze.TDFList)
			w.WriteByte(byte(blaze.TDFString))
			w.Var(1)
			w.Var(2)
			w.WriteString("x")
			w.WriteByte(0)
		case strings.HasPrefix(elem, "struct:") && depth <= 3:
			writeStructListHeader(w, tag, 1)
			writeSample(sc, w, strings.TrimPrefix(elem, "struct:"), depth+1)
			w.GroupEnd()
		default:
			w.EmptyList(tag, blaze.TDFVarInt)
		}
	case strings.HasPrefix(spec, "map:"):
		w.EmptyMap(tag, blaze.TDFString, blaze.TDFString)
	}
}

// TestEveryCommandWithSampleRequests: like TestEveryCommandAgainstSchema, but
// each request carries every member of its type. Panics fail; replies the game
// cannot decode fail.
func TestEveryCommandWithSampleRequests(t *testing.T) {
	sc := blaze.GameSchema()
	cfg := testConfig()
	cfg.LogTDF = false
	st := &fakeStore{per: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "PlayerOne", SessionKey: "sk", Email: "p1@example"},
		others: []database.PersonaRef{{ID: 2, AccountID: 2, BlazeID: 1000002, Name: "PlayerTwo"}}}
	var logs syncBuf
	s := New(cfg, st, log.New(&logs, "", 0))
	keys := make([]string, 0, len(sc.Commands))
	for k := range sc.Commands {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		c, _ := strconv.ParseUint(k[:4], 16, 16)
		cmd, _ := strconv.ParseUint(k[5:], 16, 16)
		rq := sc.RequestType(uint16(c), uint16(cmd))
		if c == 0x0005 || rq == "" {
			continue
		}
		var w blaze.Writer
		writeSample(sc, &w, rq, 0)
		fields, err := blaze.DecodeAll(w.Bytes())
		if err != nil {
			t.Fatalf("%s: sample request does not decode: %v", k, err)
		}
		a, b := net.Pipe()
		go io.Copy(io.Discard, b)
		state := &connState{kind: "BLAZE", conn: a, wmu: &sync.Mutex{}, Persona: st.per, Authenticated: true, Email: st.per.Email, identified: true, UserAnnounced: true}
		var resp *blaze.Packet
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s %s: panic %v", k, sc.Commands[k].Name, r)
				}
			}()
			resp = s.dispatch(&blaze.Packet{Component: uint16(c), Command: uint16(cmd), ID: 1, Payload: w.Bytes()}, fields, false, state)
		}()
		name := sc.Commands[k].Component + "." + sc.Commands[k].Name
		check := func(what, typ string, payload []byte) {
			for _, is := range sc.Validate(typ, payload) {
				lines = append(lines, fmt.Sprintf("%s %s %s", name, what, is))
				if is.Fatal {
					t.Errorf("%s %s: %s", name, what, is)
				}
			}
		}
		if resp != nil {
			typ, _ := sc.ResponseType(resp.Component, resp.Command)
			if resp.QType == blaze.TypeError {
				typ = sc.ErrorType(resp.Component, resp.Command)
			}
			check("REPLY", typ, resp.Payload)
		}
		for _, n := range state.Pending {
			check("NOTIFY "+blaze.NotificationName(n.Component, n.Command), sc.NotificationType(n.Component, n.Command), n.Payload)
		}
		a.Close()
		b.Close()
	}
	if os.Getenv("SCHEMA_REPORT") != "" {
		sort.Strings(lines)
		for _, l := range lines {
			fmt.Println("[FUZZ]", l)
		}
	}
}

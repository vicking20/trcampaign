package server

import (
	"encoding/json"
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

// TestEveryCommandAgainstSchema sends every RPC of the game's catalog with an
// empty request to the dispatcher (logged-in player) and checks the replies
// and queued notifications against the game's schema. A panic is a failure;
// schema issues are reported with SCHEMA_REPORT=1.
func TestEveryCommandAgainstSchema(t *testing.T) {
	sc := blaze.GameSchema()
	cfg := testConfig()
	cfg.LogTDF = false
	st := &fakeStore{per: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "PlayerOne", SessionKey: "sk", Email: "p1@example"},
		others: []database.PersonaRef{{ID: 2, BlazeID: 1000002, Name: "PlayerTwo"}}}
	var logs syncBuf
	s := New(cfg, st, log.New(&logs, "", 0))
	keys := make([]string, 0, len(sc.Commands))
	for k := range sc.Commands {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var mu sync.Mutex
	var lines []string
	status := map[string]string{}
	unimpl := map[string]bool{}
	unimplementedHook = func(c, cmd uint16) { unimpl[fmt.Sprintf("%04x:%04x", c, cmd)] = true }
	defer func() { unimplementedHook = nil }()
	for _, k := range keys {
		c, _ := strconv.ParseUint(k[:4], 16, 16)
		cmd, _ := strconv.ParseUint(k[5:], 16, 16)
		if c == 0x0005 {
			continue // redirector has its own listener
		}
		a, b := net.Pipe()
		go io.Copy(io.Discard, b)
		state := &connState{kind: "BLAZE", conn: a, wmu: &sync.Mutex{}, Persona: st.per, Authenticated: true, Email: st.per.Email, identified: true, UserAnnounced: true}
		p := &blaze.Packet{Component: uint16(c), Command: uint16(cmd), ID: 1}
		var resp *blaze.Packet
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s %s: panic %v", k, sc.Commands[k].Name, r)
				}
			}()
			resp = s.dispatch(p, nil, false, state)
		}()
		name := sc.Commands[k].Component + "." + sc.Commands[k].Name
		switch {
		case resp == nil:
			status[k] = "none"
		case unimpl[k]:
			status[k] = "unimplemented"
		case resp.QType == blaze.TypeError:
			status[k] = "error"
		default:
			status[k] = "implemented"
			if t, _ := sc.ResponseType(resp.Component, resp.Command); t != "" && len(resp.Payload) == 0 {
				status[k] = "empty"
			}
		}
		if resp != nil {
			typ, _ := sc.ResponseType(resp.Component, resp.Command)
			if resp.QType == blaze.TypeError {
				typ = sc.ErrorType(resp.Component, resp.Command)
			}
			for _, is := range sc.Validate(typ, resp.Payload) {
				mu.Lock()
				lines = append(lines, fmt.Sprintf("%s REPLY %s", name, is))
				mu.Unlock()
			}
			if typ != "" && len(resp.Payload) == 0 && resp.QType != blaze.TypeError {
				lines = append(lines, fmt.Sprintf("%s REPLY empty %s (client uses all defaults)", name, strings.TrimPrefix(typ, "Blaze::")))
			}
		}
		for _, n := range state.Pending {
			typ := sc.NotificationType(n.Component, n.Command)
			for _, is := range sc.Validate(typ, n.Payload) {
				lines = append(lines, fmt.Sprintf("%s NOTIFY %s %s", name, blaze.NotificationName(n.Component, n.Command), is))
			}
		}
		a.Close()
		b.Close()
	}
	if out := os.Getenv("SCHEMA_STATUS_OUT"); out != "" {
		b, _ := json.MarshalIndent(status, "", " ")
		_ = os.WriteFile(out, b, 0644)
	}
	if os.Getenv("SCHEMA_REPORT") != "" {
		sort.Strings(lines)
		for _, l := range lines {
			fmt.Println("[ALLCMD]", l)
		}
		fmt.Println(logs.String())
	}
}

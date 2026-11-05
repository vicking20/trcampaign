package server

import (
	"log"
	"sort"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
)

// clientLookup mimics the client's attribute lookup (0x01A58FA0): binary
// search in the decoded order with the case-insensitive compare.
func clientLookup(list []kv, key string) (string, bool) {
	i := sort.Search(len(list), func(i int) bool { return !lessFold(list[i].K, key) })
	if i < len(list) && strings.EqualFold(list[i].K, key) {
		return list[i].V, true
	}
	return "", false
}

func TestAttributeMapOrderIsCaseInsensitive(t *testing.T) {
	attrs := []kv{{"skillLevel", "1"}, {"OkToConnect", "true"}, {"playListId", "C516C96D"}, {"RouteIndex", "0"},
		{"WaitingForMorePlayersTimer", "0"}, {"SessionIndex", "1"}}
	var w blaze.Writer
	writeKVMap(&w, "ATTR", attrs)
	got := mapKV(decodeOK(t, w.Bytes()), "ATTR")
	var keys []string
	for _, a := range got {
		keys = append(keys, a.K)
	}
	if strings.Join(keys, ",") != "OkToConnect,playListId,RouteIndex,SessionIndex,skillLevel,WaitingForMorePlayersTimer" {
		t.Fatalf("order %v", keys)
	}
	for _, a := range attrs {
		if v, ok := clientLookup(got, a.K); !ok || v != a.V {
			t.Fatalf("client lookup of %s failed in %v", a.K, keys)
		}
	}
}

func setupReasonOf(t *testing.T, fs []blaze.Field) (byte, []blaze.Field) {
	t.Helper()
	for _, f := range fs {
		if f.Tag == "REAS" {
			m := f.Value.(map[string]any)
			return m["key"].(byte), m["field"].(blaze.Field).Value.([]blaze.Field)
		}
	}
	t.Fatal("REAS missing")
	return 0, nil
}

func objList(fs []blaze.Field, tag string) []objectID {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		var out []objectID
		v := f.Value
		if m, ok := v.(map[string]any); ok {
			v = m["items"]
		}
		switch v := v.(type) {
		case []any:
			for _, e := range v {
				if o, ok := e.([3]uint64); ok {
					out = append(out, objectID(o))
				}
			}
		case [][3]uint64:
			for _, o := range v {
				out = append(out, objectID(o))
			}
		}
		return out
	}
	return nil
}

func TestPlaygroupMatchmakingAndJoinByPlayer(t *testing.T) {
	var logs syncBuf
	s := New(testConfig(), &multiStore{}, log.New(&logs, "", 0))
	ds := connectDedicatedServer(t, s)
	gid := fieldUintAuth(decodeOK(t, ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter()).Payload), "GID")
	finalizeGame(t, ds, gid)

	leader := connectAndLogin(t, s, "Player1")
	member := connectAndLogin(t, s, "Rival")
	pgid := fieldUintAuth(fieldGroupOf(decodeOK(t, leader.call(ComponentPlaygroups, PGCreate, pgCreateWriter("Player1")).Payload), "INFO"), "PGID")
	var jw blaze.Writer
	jw.TagVar("PGID", pgid)
	if r := member.call(ComponentPlaygroups, PGJoin, &jw); r.Error != 0 {
		t.Fatalf("join group %x", r.Error)
	}

	// the leader matchmakes for the group
	mm := mmRequestWriter(10000, 2, 0xC0A80A02)
	mm.TagTriple("BTPL", uint64(ComponentPlaygroups), 1, pgid)
	leader.call(ComponentGameManager, GMStartMatchmaking, mm)
	ds.waitNotify(ComponentGameManager, GMNotifyGameReset)
	preGame(t, ds, gid)

	sl := decodeOK(t, leader.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	if k, ctx := setupReasonOf(t, sl); k != reasonMatchmaking || fieldUintAuth(ctx, "RSLT") != mmResultCreatedGame {
		t.Fatalf("leader setup reason %d %v", k, ctx)
	}
	sm := decodeOK(t, member.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	k, ctx := setupReasonOf(t, sm)
	if k != reasonIndirectMM || fieldTriple(ctx, "GRID") != playgroupObject(uint32(pgid)) || fieldUintAuth(ctx, "USID") != 1000002 {
		t.Fatalf("member setup reason %d %v", k, ctx)
	}
	pros := fieldStructList(sm, "PROS")
	if len(pros) != 2 || fieldTriple(pros[1], "UGID") != playgroupObject(uint32(pgid)) {
		t.Fatalf("roster %v", pros)
	}

	// an invite lookup shows the leader's game and playgroup
	other := connectAndLogin(t, s, "Third")
	var lu blaze.Writer
	lu.TagVar("ID", 1000002)
	ud := decodeOK(t, other.call(ComponentUserSessions, USLookupUser, &lu).Payload)
	objs := objList(usGroup(t, ud, "EDAT"), "ULST")
	want := map[objectID]bool{gameObject(uint32(gid)): true, playgroupObject(uint32(pgid)): true}
	if len(objs) != 2 || !want[objs[0]] || !want[objs[1]] {
		t.Fatalf("ULST %v", objs)
	}

	// friends list "join": joinGame GID 0 + USER name
	var jg blaze.Writer
	jg.TagVar("GID", 0)
	jg.TagVar("JMET", 4)
	jg.GroupStart("USER")
	jg.TagString("NAME", "Player1")
	jg.GroupEnd()
	r := other.call(ComponentGameManager, GMJoinGame, &jg)
	if r.Error != 0 || fieldUintAuth(decodeOK(t, r.Payload), "GID") != gid {
		t.Fatalf("join by player %x %v", r.Error, r.Payload)
	}
	so := decodeOK(t, other.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	if k, _ := setupReasonOf(t, so); k != reasonDataless || len(fieldStructList(so, "PROS")) != 3 {
		t.Fatalf("join setup %d", k)
	}

	// a player that is not online
	var jx blaze.Writer
	jx.GroupStart("USER")
	jx.TagString("NAME", "Nobody")
	jx.GroupEnd()
	late := connectAndLogin(t, s, "Late")
	if r := late.call(ComponentGameManager, GMJoinGame, &jx); r.Error != gmErrPlayerNotFound {
		t.Fatalf("join unknown player: error %x", r.Error)
	}
	if strings.Contains(logs.String(), "[SCHEMA]") {
		t.Fatalf("schema problems:\n%s", logs.String())
	}
}

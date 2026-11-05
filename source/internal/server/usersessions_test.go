package server

import (
	"reflect"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

func usTestPersona() database.Persona {
	return database.Persona{ID: 7, AccountID: 42, BlazeID: 1000042, Name: "EXAMPLE-CLIENT1.0", SessionKey: "k"}
}

func usTags(fs []blaze.Field) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Tag)
	}
	return out
}

func usGroup(t *testing.T, fs []blaze.Field, tag string) []blaze.Field {
	t.Helper()
	for _, f := range fs {
		if f.Tag == tag {
			if g, ok := f.Value.([]blaze.Field); ok {
				return g
			}
		}
	}
	t.Fatalf("group %s missing", tag)
	return nil
}

func usVar(t *testing.T, fs []blaze.Field, tag string) uint64 {
	t.Helper()
	for _, f := range fs {
		if f.Tag == tag {
			return f.Value.(uint64)
		}
	}
	t.Fatalf("var %s missing", tag)
	return 0
}

// Heat2 emits members in ascending encoded-tag order; assert we do the same at every level.
func assertSorted(t *testing.T, fs []blaze.Field, where string) {
	t.Helper()
	key := func(s string) uint32 {
		b := encodeTagForTest(s)
		return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	}
	for i := 1; i < len(fs); i++ {
		if key(fs[i-1].Tag) >= key(fs[i].Tag) {
			t.Fatalf("%s: %s before %s is out of order", where, fs[i-1].Tag, fs[i].Tag)
		}
	}
	for _, f := range fs {
		if g, ok := f.Value.([]blaze.Field); ok {
			assertSorted(t, g, where+"."+f.Tag)
		}
	}
}

func TestNotifyUserAddedSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildNotifyUserAdded(usTestPersona()))
	if err != nil {
		t.Fatal(err)
	}
	assertSorted(t, fs, "NotifyUserAdded")
	if got := usTags(fs); !reflect.DeepEqual(got, []string{"DATA", "USER"}) {
		t.Fatalf("top=%v", got)
	}
	user := usGroup(t, fs, "USER")
	if got, want := usTags(user), []string{"AID", "ALOC", "EXBB", "EXID", "ID", "NAME"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("USER=%v want %v", got, want)
	}
	if usVar(t, user, "ID") != 1000042 || usVar(t, user, "AID") != 42 {
		t.Fatal("identity mismatch")
	}
	data := usGroup(t, fs, "DATA")
	if got, want := usTags(data), []string{"ADDR", "BPS", "CTY", "DMAP", "HWFG", "PSLM", "QDAT", "UATT"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DATA=%v want %v", got, want)
	}
}

func TestUserStatusSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildUserStatus(usTestPersona(), userStatusOnline))
	if err != nil {
		t.Fatal(err)
	}
	assertSorted(t, fs, "UserStatus")
	if usVar(t, fs, "FLGS") != 3 || usVar(t, fs, "ID") != 1000042 {
		t.Fatal("bad UserStatus")
	}
}

func TestPostAuthSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildPostAuthResponse(usTestPersona(), "127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	assertSorted(t, fs, "PostAuth")
	if got := usTags(fs); !reflect.DeepEqual(got, []string{"PSS", "TELE", "TICK", "UROP"}) {
		t.Fatalf("top=%v", got)
	}
	if usVar(t, usGroup(t, fs, "UROP"), "UID") != 1000042 {
		t.Fatal("UROP.UID")
	}
}

func TestPreAuthSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildPreAuthResponse("NFSRunEmu"))
	if err != nil {
		t.Fatal(err)
	}
	assertSorted(t, fs, "PreAuth")
	for _, f := range fs {
		if f.Tag == "CIDS" {
			m := f.Value.(map[string]any)
			items := m["items"].([]any)
			found := false
			for _, it := range items {
				if it.(uint64) == 0x7802 {
					found = true
				}
			}
			if !found {
				t.Fatal("CIDS missing UserSessions")
			}
			return
		}
	}
	t.Fatal("CIDS missing")
}

func TestPingSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildPingResponse(time.Unix(1700000000, 0)))
	if err != nil || usVar(t, fs, "STIM") != 1700000000 {
		t.Fatal("ping")
	}
}

func TestLoginNotificationsFraming(t *testing.T) {
	ps := loginNotifications(usTestPersona())
	if len(ps) != 2 || ps[0].Command != 2 || ps[1].Command != 5 {
		t.Fatal("order")
	}
	for _, p := range ps {
		b := p.Bytes()
		if p.Component != 0x7802 || b[8] != 0x20 || b[9] != 0x00 || p.ID != 0 {
			t.Fatalf("bad header % X", b[:12])
		}
	}
}

func encodeTagForTest(s string) [3]byte { return blaze.EncodeTag(s) }

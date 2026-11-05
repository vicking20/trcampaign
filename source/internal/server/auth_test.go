package server

import (
	"reflect"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

func testPersona() database.Persona {
	return database.Persona{ID: 7, AccountID: 42, BlazeID: 1000042, Name: "EXAMPLE-CLIENT1.0", SessionKey: "test-session-key"}
}

func fieldTags(fs []blaze.Field) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Tag)
	}
	return out
}

func fieldGroup(t *testing.T, fs []blaze.Field, tag string) []blaze.Field {
	t.Helper()
	for _, f := range fs {
		if f.Tag == tag {
			g, ok := f.Value.([]blaze.Field)
			if !ok {
				t.Fatalf("%s is not a group: %#v", tag, f.Value)
			}
			return g
		}
	}
	t.Fatalf("group %s not found", tag)
	return nil
}

func fieldVar(t *testing.T, fs []blaze.Field, tag string) uint64 {
	t.Helper()
	for _, f := range fs {
		if f.Tag == tag {
			v, ok := f.Value.(uint64)
			if !ok {
				t.Fatalf("%s is not varint: %#v", tag, f.Value)
			}
			return v
		}
	}
	t.Fatalf("field %s not found", tag)
	return 0
}

func TestNormalizePersonaName(t *testing.T) {
	cases := map[string]string{
		"eatest.EXAMPLE-CLIENT1.0@ea.com": "EXAMPLE-CLIENT1.0",
		"Player@ea.com":                   "Player",
		"":                                "LocalPlayer",
	}
	for in, want := range cases {
		if got := normalizePersonaName(in); got != want {
			t.Fatalf("normalizePersonaName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestFullLoginResponseSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildFullLoginResponse(testPersona(), "eatest.EXAMPLE-CLIENT1.0@ea.com"))
	if err != nil {
		t.Fatal(err)
	}
	wantTop := []string{"AGUP", "LDHT", "NTOS", "PCTK", "PRIV", "SESS", "SPAM", "THST", "TSUI", "TURI"}
	if got := fieldTags(fs); !reflect.DeepEqual(got, wantTop) {
		t.Fatalf("top tags=%v want %v", got, wantTop)
	}
	sess := fieldGroup(t, fs, "SESS")
	wantSess := []string{"BUID", "FRST", "KEY", "LLOG", "MAIL", "PDTL", "UID"}
	if got := fieldTags(sess); !reflect.DeepEqual(got, wantSess) {
		t.Fatalf("SESS tags=%v want %v", got, wantSess)
	}
	pdtl := fieldGroup(t, sess, "PDTL")
	wantPDTL := []string{"DSNM", "LAST", "PID", "STAS"}
	if got := fieldTags(pdtl); !reflect.DeepEqual(got, wantPDTL) {
		t.Fatalf("PDTL tags=%v want %v", got, wantPDTL)
	}
	if got := fieldVar(t, sess, "BUID"); got != 1000042 {
		t.Fatalf("BUID=%d want 1000042", got)
	}
	if got := fieldVar(t, sess, "UID"); got != 42 {
		t.Fatalf("UID=%d want 42", got)
	}
	if got := fieldVar(t, pdtl, "PID"); got != 7 {
		t.Fatalf("PID=%d want 7", got)
	}
}

func TestCreatePersonaResponseSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildCreatePersonaResponse(testPersona()))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fieldTags(fs), []string{"DSNM", "LAST", "PID", "STAS"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top tags=%v want %v", got, want)
	}
	if got := fieldVar(t, fs, "PID"); got != 7 {
		t.Fatalf("PID=%d want 7", got)
	}
}

func TestGetPersonaResponseSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildGetPersonaResponse(testPersona()))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fieldTags(fs), []string{"PINF", "UID"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top tags=%v want %v", got, want)
	}
	pinf := fieldGroup(t, fs, "PINF")
	want := []string{"DSNM", "DTCR", "LADT", "NSNM", "PID", "STAS"}
	if got := fieldTags(pinf); !reflect.DeepEqual(got, want) {
		t.Fatalf("PINF tags=%v want %v", got, want)
	}
}

func TestLoginPersonaResponseSchema(t *testing.T) {
	fs, err := blaze.DecodeAll(buildLoginPersonaResponse(testPersona(), "eatest.EXAMPLE-CLIENT1.0@ea.com"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BUID", "FRST", "KEY", "LLOG", "MAIL", "PDTL", "UID"}
	if got := fieldTags(fs); !reflect.DeepEqual(got, want) {
		t.Fatalf("tags=%v want %v", got, want)
	}
	if got := fieldVar(t, fs, "BUID"); got != 1000042 {
		t.Fatalf("BUID=%d want 1000042", got)
	}
	if got := fieldVar(t, fs, "UID"); got != 42 {
		t.Fatalf("UID=%d want 42", got)
	}
	pdtl := fieldGroup(t, fs, "PDTL")
	if got := fieldVar(t, pdtl, "PID"); got != 7 {
		t.Fatalf("PID=%d want 7", got)
	}
}

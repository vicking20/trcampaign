package blaze

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestTag(t *testing.T) {
	x := encodeTag("VALU")
	if x != [3]byte{218, 27, 53} {
		t.Fatalf("VALU=%v", x)
	}
	y := encodeTag("TEST")
	if y != [3]byte{210, 92, 244} {
		t.Fatalf("TEST=%v", y)
	}
}
func TestDecodeTag(t *testing.T) {
	if got := decodeTag([3]byte{218, 27, 53}); got != "VALU" {
		t.Fatalf("decode VALU=%q", got)
	}
	if got := decodeTag([3]byte{210, 92, 244}); got != "TEST" {
		t.Fatalf("decode TEST=%q", got)
	}
}

func TestVar(t *testing.T) {
	vals := []uint64{0, 1, 63, 64, 127, 128, 9999, 1 << 32}
	for _, v := range vals {
		var w Writer
		w.Var(v)
		r := NewReader(w.Bytes())
		got, e := r.varint()
		if e != nil || got != v {
			t.Fatalf("%d -> %x -> %d %v", v, w.Bytes(), got, e)
		}
	}
}

func TestFormatFieldsSafeRedactsCredentials(t *testing.T) {
	fields := []Field{
		{Tag: "MAIL", Type: TDFString, Value: "player@example.test"},
		{Tag: "PASS", Type: TDFString, Value: "secret-password"},
		{Tag: "TOKN", Type: TDFString, Value: "secret-token"},
		{Tag: "KEY", Type: TDFString, Value: "secret-session"},
		{Tag: "PCTK", Type: TDFString, Value: "secret-pctk"},
	}
	got := FormatFieldsSafe(fields, "")
	if strings.Contains(got, "secret-password") || strings.Contains(got, "secret-token") || strings.Contains(got, "secret-session") || strings.Contains(got, "secret-pctk") {
		t.Fatalf("credential leaked in safe formatter: %q", got)
	}
	if !strings.Contains(got, "MAIL") || !strings.Contains(got, "player@example.test") {
		t.Fatalf("non-secret fields missing from safe formatter: %q", got)
	}
}

// Real GameReporting.submitOfflineGameReport payload from a game log.
// It ends with a single 0x00 terminator that must not be read as a field.
const liveOfflineReportHex = "9AECE80000C32DB40700CB0CB4039E1B650701AFCBDAE1039E1B6503CA5CC00500030181897A874B7000009B2CF40001B29CF40041C22CF40001CA18E500B3CEEB9513D29B6500889210DA5A2900AEFDC3E70ADE9B8000010000009F2A6400009F4E70010C726163655F73696E676C650000"

func TestDecodeTrailingZeroTerminator(t *testing.T) {
	b, _ := hex.DecodeString(liveOfflineReportHex)
	fs, err := DecodeAll(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var tags []string
	for _, f := range fs {
		tags = append(tags, f.Tag)
	}
	if strings.Join(tags, ",") != "FNSH,PRVT,RPRT,GRID,GTYP" {
		t.Fatalf("tags=%v", tags)
	}
	if FindString(fs, "GTYP") != "race_single" {
		t.Fatal("GTYP")
	}
}

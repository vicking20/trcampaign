package blaze

import "testing"

func TestTagRoundTrip(t *testing.T) {
	for _, s := range []string{"CINF", "GID", "OPT1", "CLD2", "VALU", "ID", "A", "XBOX", "COP5"} {
		if g := decodeTag(encodeTag(s)); g != s {
			t.Errorf("%s -> %s", s, g)
		}
	}
}

package database

import "testing"

func TestEsc(t *testing.T) {
	cases := map[string]string{
		"Player":            "Player",
		"O'Brien":           "O''Brien",
		`a\`:                `a\`,
		`x\'; DROP --`:      `x\''; DROP --`,
		"nul\x00ctl\x01":    "nulctl",
		"EXAMPLE-CLIENT1.0": "EXAMPLE-CLIENT1.0",
	}
	for in, want := range cases {
		if got := esc(in); got != want {
			t.Fatalf("esc(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParsePersonaRowEmptyEmail(t *testing.T) {
	for _, row := range []string{"1\t1\t1000001\tLocalPlayer\t", "1\t1\t1000001\tLocalPlayer"} {
		p, err := parsePersonaRow(row)
		if err != nil || p.Name != "LocalPlayer" || p.Email != "" {
			t.Fatalf("%q -> %+v %v", row, p, err)
		}
	}
}

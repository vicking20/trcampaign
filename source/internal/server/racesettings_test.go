package server

import (
	"strings"
	"testing"
)

func TestSetIniKeys(t *testing.T) {
	in := "[Server]\r\nHost=1.2.3.4\r\n\r\n[Patch]\r\nTrafficDensity=0.5\r\nMaxPlayers=8\r\n\r\n[Origin]\r\nEnable=1\r\n"
	out := setIniKeys(in, "Patch", map[string]string{"TrafficDensity": "1.5", "TrafficCarLimit": "40"})
	for _, want := range []string{"TrafficDensity=1.5", "TrafficCarLimit=40", "MaxPlayers=8", "Host=1.2.3.4", "[Origin]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "0.5") || strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Fatalf("old value kept or line endings mixed: %q", out)
	}
	off := setIniKeys(out, "Patch", map[string]string{"TrafficDensity": "", "TrafficCarLimit": ""})
	if strings.Contains(off, "Traffic") || !strings.Contains(off, "MaxPlayers=8") {
		t.Fatalf("removal failed: %q", off)
	}
	add := setIniKeys("[Server]\nHost=x\n", "Patch", map[string]string{"TrafficDensity": "1"})
	if !strings.Contains(add, "[Patch]\nTrafficDensity=1") {
		t.Fatalf("section not added: %q", add)
	}
}

package main

import (
	"strings"
	"testing"
)

func TestWithSoloOnline(t *testing.T) {
	got, added := withSoloOnline([]string{"-Window.Fullscreen", "false"})
	if !added || strings.Join(got, " ") != "-Window.Fullscreen false -NfsOnline.AllowSinglePlayerOnline true" {
		t.Fatal(got)
	}
	got, added = withSoloOnline([]string{"-NfsOnline.AllowSinglePlayerOnline", "false"})
	if added || len(got) != 2 {
		t.Fatal("must keep the player's own value", got)
	}
}

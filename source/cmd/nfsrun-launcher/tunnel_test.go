package main

import (
	"reflect"
	"testing"
)

func TestStripTunnelArgs(t *testing.T) {
	kept, dropped := stripTunnelArgs([]string{"-windowed", "-NfsOnline.EnableProtoTunnel", "false", "-x", "1"})
	if !reflect.DeepEqual(kept, []string{"-windowed", "-x", "1"}) || len(dropped) != 2 {
		t.Fatalf("kept %v dropped %v", kept, dropped)
	}
	kept, dropped = stripTunnelArgs([]string{"-nfsonline.enableprototunnel", "-a"})
	if !reflect.DeepEqual(kept, []string{"-a"}) || len(dropped) != 1 {
		t.Fatalf("kept %v dropped %v", kept, dropped)
	}
}

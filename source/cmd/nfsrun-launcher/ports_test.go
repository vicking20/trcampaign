package main

import "testing"

func TestIsLauncherImage(t *testing.T) {
	if !isLauncherImage("nfsrun-launcher.exe") || isLauncherImage("EADesktop.exe") {
		t.Fatal("isLauncherImage")
	}
}

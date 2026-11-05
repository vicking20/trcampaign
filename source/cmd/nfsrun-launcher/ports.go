package main

import "strings"

// isLauncherImage: one of ours (nfsrun-launcher.exe, renamed copies too).
func isLauncherImage(name string) bool {
	return strings.Contains(strings.ToLower(name), "nfsrun")
}

package main

import (
	"strings"

	"nfsrun-server/internal/launcherpreset"
)

// The server built into this launcher by its owner (players configure
// nothing). Set either by
//
//	nfsrun-server --stamp-launcher <out.exe>   (rewrites presetBlock in a copy)
//	go build -ldflags "-X main.bakedServerHTTP=https://host:8443 -X main.bakedServerPin=<hex> -X main.bakedDomain=example.org"
//
// SERVER_HTTP / SERVER_CERT_PIN in nfsrun-launcher.ini override them.
var bakedServerHTTP, bakedServerPin, bakedDomain string

// presetBlock: fixed-size block that --stamp-launcher rewrites in place.
var presetBlock = launcherpreset.Block

// builtIn: the preset of this exe; a stamped block wins over -ldflags values.
func builtIn() launcherpreset.Preset {
	if p := launcherpreset.Parse(presetBlock); p.ServerHTTP != "" {
		return p
	}
	return launcherpreset.Preset{
		ServerHTTP: strings.TrimRight(strings.TrimSpace(bakedServerHTTP), "/"),
		Pin:        strings.TrimSpace(bakedServerPin),
		Domain:     strings.TrimSpace(bakedDomain),
	}
}

package server

// Every packet we send is checked against the game's own TDF schema
// (internal/blaze/schema.json, extracted from the game binaries). A wire-type
// mismatch is a decode error in the client; an out-of-order or unknown tag is
// silently dropped by it. Logged once per message/issue as [SCHEMA].

import (
	"fmt"
	"strings"
	"sync"

	"nfsrun-server/internal/blaze"
)

type schemaSeen struct {
	mu   sync.Mutex
	seen map[string]bool
}

var schemaLog schemaSeen

// schemaIssueHook lets tests fail on any schema issue (nil in production).
var schemaIssueHook func(what string, issues []blaze.Issue)

// schemaCheckedHook records every message checked (tests: coverage report).
var schemaCheckedHook func(what string)

func (s *Server) checkSchema(state *connState, p *blaze.Packet, notify bool) {
	sc := blaze.GameSchema()
	if sc == nil || (!s.cfg.SchemaCheck && schemaIssueHook == nil) {
		return
	}
	var typ, what string
	switch {
	case notify:
		typ = sc.NotificationType(p.Component, p.Command)
		what = "NOTIFY " + blaze.NotificationName(p.Component, p.Command)
	case p.QType == blaze.TypeError:
		typ = sc.ErrorType(p.Component, p.Command)
		what = "ERROR " + blaze.CommandName(p.Component, p.Command)
	default:
		t, known := sc.ResponseType(p.Component, p.Command)
		typ = t
		what = "REPLY " + blaze.CommandName(p.Component, p.Command)
		if known && t == "" && len(p.Payload) > 0 {
			s.reportSchema(what, []blaze.Issue{{Path: what, Detail: fmt.Sprintf("the client expects no payload (RpcJob<void>), %d bytes sent are ignored", len(p.Payload))}})
			return
		}
	}
	if schemaCheckedHook != nil {
		schemaCheckedHook(what + " -> " + typ)
	}
	if typ == "" || len(p.Payload) == 0 {
		return
	}
	if issues := sc.Validate(typ, p.Payload); len(issues) > 0 {
		s.reportSchema(what+" ("+strings.TrimPrefix(typ, "Blaze::")+")", issues)
	}
}

func (s *Server) reportSchema(what string, issues []blaze.Issue) {
	if schemaIssueHook != nil {
		schemaIssueHook(what, issues)
	}
	schemaLog.mu.Lock()
	defer schemaLog.mu.Unlock()
	if schemaLog.seen == nil {
		schemaLog.seen = map[string]bool{}
	}
	for _, is := range issues {
		k := what + "|" + is.String()
		if schemaLog.seen[k] {
			continue
		}
		schemaLog.seen[k] = true
		s.log.Printf("[SCHEMA] %s: %s", what, is)
	}
}

// unimplemented logs an RPC the server does not handle, with what the client
// expects in return (from schema.json).
// unimplementedHook records unimplemented RPCs (tests: catalog status).
var unimplementedHook func(component, command uint16)

func (s *Server) unimplemented(p *blaze.Packet, where string) {
	if unimplementedHook != nil {
		unimplementedHook(p.Component, p.Command)
	}
	sc := blaze.GameSchema()
	name := blaze.CommandName(p.Component, p.Command)
	if sc == nil {
		s.log.Printf("[BLAZE] UNIMPLEMENTED %s (%s)", name, where)
		return
	}
	typ, known := sc.ResponseType(p.Component, p.Command)
	switch {
	case !known:
		s.log.Printf("[BLAZE] UNIMPLEMENTED %s: not an RPC of this game's catalog", name)
	case typ == "":
		s.log.Printf("[BLAZE] UNIMPLEMENTED %s: client expects no payload (void) -> empty success", name)
	default:
		var tags []string
		for _, m := range sc.Members(typ) {
			tags = append(tags, m.Tag)
		}
		s.log.Printf("[BLAZE] UNIMPLEMENTED %s: client expects %s{%s}; reply has none of them (client defaults)", name, strings.TrimPrefix(typ, "Blaze::"), strings.Join(tags, " "))
	}
}

// unimplementedReply: an empty success.
func (s *Server) unimplementedReply(p *blaze.Packet) *blaze.Packet {
	return s.response(p, nil)
}

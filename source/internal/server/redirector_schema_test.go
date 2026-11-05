package server

import (
	"log"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

func TestRedirectorReplyMatchesSchema(t *testing.T) {
	var logs syncBuf
	s := New(testConfig(), &fakeStore{per: database.Persona{ID: 1}}, log.New(&logs, "", 0))
	resp := s.dispatch(&blaze.Packet{Component: 5, Command: 1, ID: 1}, nil, true, &connState{kind: "REDIRECTOR"})
	if issues := blaze.GameSchema().Validate("Blaze::Redirector::ServerInstanceInfo", resp.Payload); len(issues) > 0 {
		t.Fatalf("%v", issues)
	}
}

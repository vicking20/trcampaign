// Package originid derives the Origin ("Nucleus") user id of a persona.
//
// The launcher's fake Origin reports it (GetProfile UserId, QueryFriends) and the
// Blaze server reports the same number as the user's external id (EXID / XREF),
// so the game can match its Origin friends with Blaze users.
package originid

import (
	"hash/fnv"
	"strings"
)

// UserID is stable per persona name (case-insensitive).
func UserID(persona string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(persona))))
	return 1_000_000_000 + h.Sum64()%1_000_000_000
}

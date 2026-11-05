package server

import (
	"sort"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Autolog Recommends (getFriendsPlayRecommendations). The screen has four
// tabs: Medals and Progression are built by the client; Beat You comes from
// FriendsPlayResponse.STOR (Story) and Popular from .WISD
// (WisdomOfCrowdsStory).
//
// Story: BLID, POST (the friend; stories posted by the player are skipped),
// TIME ("YYYY-MM-DD HH:MM:SS GMT"), GEN, SUBJ (text key: friend_try_this,
// friend_first_attempt, friend_first_attempt_took_first,
// friend_improved_position_above, friend_pulling_ahead, friend_took_position,
// friend_took_top_spot), EVNT, EVTM (ms), EVBY, STAG, SVWD. The client drops
// locked events and events the player leads, and keeps at most 10.
// WisdomOfCrowdsStory: EVNT, EVTM best time, BLID best player, TIME, FRDS.

const recommendsLimit = 10

type storyOut struct {
	friend, poster uint64
	event          uint64
	timeMs, by     uint64
	subject        string
	when           int64
}

type wisdomOut struct {
	best    uint64
	event   uint64
	timeMs  uint64
	friends uint64
	when    int64
}

func storyTime(unix int64) string {
	if unix <= 0 {
		unix = time.Now().Unix()
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04:05") + " GMT"
}

// buildRecommends computes Beat You stories and Popular entries for player me
// (wire Blaze id) from one board's bests.
func buildRecommends(entries []database.BoardEntry, me uint64) ([]storyOut, []wisdomOut) {
	byEvent := map[uint64][]database.BoardEntry{}
	var events []uint64
	for _, e := range entries {
		if _, ok := byEvent[e.EventID]; !ok {
			events = append(events, e.EventID)
		}
		byEvent[e.EventID] = append(byEvent[e.EventID], e)
	}
	wire := func(e database.BoardEntry) uint64 {
		return database.Persona{ID: e.PersonaID, BlazeID: e.BlazeID}.BlazeID
	}
	var stories []storyOut
	var popular []wisdomOut
	for _, ev := range events {
		rows := byEvent[ev]
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Score < rows[j].Score })
		var mine *database.BoardEntry
		var others []database.BoardEntry
		for i := range rows {
			if wire(rows[i]) == me {
				mine = &rows[i]
			} else {
				others = append(others, rows[i])
			}
		}
		if len(others) == 0 {
			continue
		}
		best := others[0]
		if mine == nil && len(others) >= 2 {
			continue // never raced, several friends did: Popular (below)
		}
		if mine == nil || best.Score < mine.Score {
			st := storyOut{friend: wire(best), poster: wire(best), event: ev, timeMs: best.Score, when: best.UpdatedUnix}
			switch {
			case mine == nil:
				st.subject = "friend_try_this"
			case best.Score <= rows[0].Score:
				st.subject = "friend_took_top_spot"
				st.by = mine.Score - best.Score
			default:
				st.subject = "friend_improved_position_above"
				st.by = mine.Score - best.Score
			}
			stories = append(stories, st)
		}
	}
	// Popular: events several friends raced that are not a Beat You story
	// (the client shows "never played it" or "compare, not top").
	inStory := map[uint64]bool{}
	for _, s := range stories {
		inStory[s.event] = true
	}
	for _, ev := range events {
		rows := byEvent[ev]
		var others []database.BoardEntry
		mineTop := false
		for i, r := range rows {
			if wire(r) == me {
				mineTop = i == 0
			} else {
				others = append(others, r)
			}
		}
		if inStory[ev] || mineTop || len(others) < 2 {
			continue
		}
		popular = append(popular, wisdomOut{best: wire(others[0]), event: ev, timeMs: others[0].Score, friends: uint64(len(others)), when: others[0].UpdatedUnix})
	}
	sort.SliceStable(stories, func(i, j int) bool { return stories[i].when > stories[j].when })
	sort.SliceStable(popular, func(i, j int) bool { return popular[i].friends > popular[j].friends })
	if len(stories) > recommendsLimit {
		stories = stories[:recommendsLimit]
	}
	if len(popular) > recommendsLimit {
		popular = popular[:recommendsLimit]
	}
	return stories, popular
}

// FriendsPlayResponse { STOR list<Story>, WISD list<WisdomOfCrowdsStory> }.
func buildFriendsPlayStories(gen string, stories []storyOut, popular []wisdomOut) []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "STOR", len(stories))
	for _, s := range stories {
		w.TagVar("BLID", s.friend)
		w.TagVar("EVBY", s.by)
		w.TagVar("EVNT", s.event)
		w.TagVar("EVTM", s.timeMs)
		w.TagString("GEN", gen)
		w.TagVar("POST", s.poster)
		w.TagVar("PRIO", 0)
		w.TagVar("STAG", 0)
		w.TagString("SUBJ", s.subject)
		w.TagVar("SVWD", 0)
		w.TagString("TIME", storyTime(s.when))
		w.GroupEnd()
	}
	writeStructListHeader(&w, "WISD", len(popular))
	for _, p := range popular {
		w.TagVar("BLID", p.best)
		w.TagVar("BNTY", 0)
		w.TagVar("EVNT", p.event)
		w.TagVar("EVTM", p.timeMs)
		w.TagVar("FRDS", p.friends)
		w.TagString("GEN", gen)
		w.TagString("TIME", storyTime(p.when))
		w.GroupEnd()
	}
	return w.Bytes()
}

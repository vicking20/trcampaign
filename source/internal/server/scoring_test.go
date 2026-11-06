package server

import "testing"

func fresh(mode string) *Standings {
	return &Standings{Mode: mode, Scheme: "field", Players: map[string]*ScorePlayer{}}
}

func TestCannonballFieldScheme(t *testing.T) {
	st := fresh("cannonball")
	cannonball(st, []RaceFinish{{"A", 100}, {"B", 110}})
	if st.Players["A"].Points != 2 || st.Players["B"].Points != 1 || st.Players["A"].Wins != 1 {
		t.Fatalf("%+v %+v", st.Players["A"], st.Players["B"])
	}
}

func TestEliminationRoundEnds(t *testing.T) {
	st := fresh("elimination")
	f := knockout(false)
	ev := f(st, []RaceFinish{{"A", 100}, {"B", 110}, {"C", 120}})
	if !st.Players["C"].Eliminated || st.Players["A"].Eliminated || len(ev) != 1 {
		t.Fatalf("%v", ev)
	}
	// B finishes, A missing from the results = did not finish, so A is out and B wins the round
	ev = f(st, []RaceFinish{{"B", 105}})
	if !st.Players["A"].Eliminated || st.Champions["B"] != 1 {
		t.Fatalf("%v %+v", ev, st.Champions)
	}
}

func TestEliminationAloneNobodyOut(t *testing.T) {
	st := fresh("elimination")
	ev := knockout(false)(st, []RaceFinish{{"A", 100}})
	if len(ev) != 0 || st.Players["A"].Eliminated {
		t.Fatalf("%v", ev)
	}
}

func TestEveryModeRuns(t *testing.T) {
	for name, m := range scoreModes {
		st := fresh(name)
		for i := 0; i < 4; i++ {
			m.apply(st, []RaceFinish{{"A", 100 + float64(i)}, {"B", 101}, {"C", 130}, {"D", 0}})
		}
		if len(m.rank(st)) == 0 {
			t.Errorf("%s: empty ranking", name)
		}
	}
}

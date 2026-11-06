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

package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A RaceFinish is one player's result from a race server: seconds, 0 = did not finish.
type RaceFinish struct {
	Name string  `json:"name"`
	Time float64 `json:"time"`
}

type raceResultsIn struct {
	Instance string       `json:"instance"`
	Port     string       `json:"port"`
	Results  []RaceFinish `json:"results"`
}

// isLocalPeer: the race servers run on this machine, so only a peer that is one of this machine's own addresses may
// post results.
func isLocalPeer(remote string) bool {
	ip := net.ParseIP(hostOf(remote))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// handleResultsHTTP receives a finished race from the plugin inside a race server (plugin/nfstr_online.c, results_thread).
func (s *Server) handleResultsHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !isLocalPeer(r.RemoteAddr) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	b, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var in raceResultsIn
	if err := json.Unmarshal(b, &in); err != nil || len(in.Results) == 0 {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	sort.SliceStable(in.Results, func(i, j int) bool { return in.Results[i].Time < in.Results[j].Time })
	s.log.Printf("[RESULTS] %s: %s", in.Instance, string(b))
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(s.cfg.LockerDir), "results.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		line, _ := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "instance": in.Instance, "results": in.Results})
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}
	s.scoreRace(in.Results)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var newsMu sync.Mutex

func (s *Server) newsPath() string { return filepath.Join(filepath.Dir(s.cfg.LockerDir), "news.json") }

func (s *Server) readNewsRaw() ([]map[string]any, error) {
	if _, err := os.Stat(s.newsPath()); err != nil {
		s.loadNews() // creates the example file
	}
	b, err := os.ReadFile(s.newsPath())
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("news.json is not valid: %v", err)
	}
	return raw, nil
}

func (s *Server) writeNewsRaw(raw []map[string]any) error {
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.newsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.newsPath())
}

func numOf(v any) int {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

type newsRow struct {
	ID      int    `json:"id"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
}

// newsList: one row per NewsId (the English text when there is one).
func (s *Server) newsList() []newsRow {
	newsMu.Lock()
	defer newsMu.Unlock()
	raw, err := s.readNewsRaw()
	if err != nil {
		return []newsRow{}
	}
	rows := map[int]newsRow{}
	for _, m := range raw {
		id := numOf(m["NewsId"])
		subj, _ := m["Subject"].(string)
		date, _ := m["PublishedDate"].(string)
		if cur, ok := rows[id]; !ok || numOf(m["Language"]) == 0 {
			_ = cur
			rows[id] = newsRow{id, subj, date}
		}
	}
	out := []newsRow{}
	for _, r := range rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// announce adds a news item in every language the file already has (the text is the same in all of them).
func (s *Server) announce(subject, text string) (int, error) {
	newsMu.Lock()
	defer newsMu.Unlock()
	raw, err := s.readNewsRaw()
	if err != nil {
		return 0, err
	}
	next, langs := 0, map[int]bool{}
	for _, m := range raw {
		if id := numOf(m["NewsId"]); id > next {
			next = id
		}
		langs[numOf(m["Language"])] = true
	}
	if len(langs) == 0 {
		langs[0] = true
	}
	next++
	short := text
	if r := []rune(short); len(r) > 90 {
		short = string(r[:90]) + "…"
	}
	now := time.Now().UTC()
	var ls []int
	for l := range langs {
		ls = append(ls, l)
	}
	sort.Ints(ls)
	for _, l := range ls {
		raw = append(raw, map[string]any{
			"NewsId": next, "PublishedDate": now.Format("01/02/2006"), "Author": "NFS The Run Private Server",
			"Subject": subject, "ShortMessage": short, "Message": text, "NewsIcon": "", "MetaContent": "",
			"Territory": 0, "Language": l, "Viewed": 0, "Category": 3, "DateNumber": now.Unix(),
		})
	}
	return next, s.writeNewsRaw(raw)
}

func (s *Server) deleteNews(id int) (int, error) {
	newsMu.Lock()
	defer newsMu.Unlock()
	raw, err := s.readNewsRaw()
	if err != nil {
		return 0, err
	}
	var keep []map[string]any
	for _, m := range raw {
		if numOf(m["NewsId"]) != id {
			keep = append(keep, m)
		}
	}
	if len(keep) == len(raw) {
		return 0, nil
	}
	return len(raw) - len(keep), s.writeNewsRaw(keep)
}

func (s *Server) cmdNews(cmd string, args []string) CommandResult {
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	switch cmd {
	case "announce":
		if len(args) < 2 || strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" {
			return say("usage: announce \"<title>\" \"<text>\"")
		}
		id, err := s.announce(strings.TrimSpace(args[0]), strings.TrimSpace(strings.Join(args[1:], " ")))
		if err != nil {
			return say("not posted: %v", err)
		}
		return say("news item %d posted: players see it in the news feed the next time the game loads it", id)
	case "news":
		if len(args) == 2 && strings.EqualFold(args[0], "delete") {
			id, err := strconv.Atoi(args[1])
			if err != nil {
				return say("usage: news delete <id>")
			}
			n, err := s.deleteNews(id)
			if err != nil {
				return say("not deleted: %v", err)
			}
			if n == 0 {
				return say("there is no news item %d", id)
			}
			return say("news item %d deleted", id)
		}
		rows := s.newsList()
		if len(rows) == 0 {
			return say("no news items")
		}
		out := []string{"news items (newest first):"}
		for _, r := range rows {
			out = append(out, fmt.Sprintf("  %d  %s  %s", r.ID, r.Date, r.Subject))
		}
		return CommandResult{Lines: out}
	}
	return say("usage: announce \"<title>\" \"<text>\"  |  news  |  news delete <id>")
}

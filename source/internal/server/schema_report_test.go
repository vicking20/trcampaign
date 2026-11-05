package server

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"

	"nfsrun-server/internal/blaze"
)

var (
	schemaTestMu     sync.Mutex
	schemaTestIssues = map[string]bool{}
	schemaTestFatal  []string
)

func TestMain(m *testing.M) {
	schemaIssueHook = func(what string, issues []blaze.Issue) {
		schemaTestMu.Lock()
		defer schemaTestMu.Unlock()
		for _, is := range issues {
			schemaTestIssues[what+": "+is.String()] = true
			if is.Fatal {
				schemaTestFatal = append(schemaTestFatal, what+": "+is.String())
			}
		}
	}
	checked := map[string]int{}
	schemaCheckedHook = func(what string) {
		schemaTestMu.Lock()
		checked[what]++
		schemaTestMu.Unlock()
	}
	code := m.Run()
	if os.Getenv("SCHEMA_REPORT") != "" {
		var ks []string
		for k := range checked {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Println("[CHECKED]", k, checked[k])
		}
	}
	if os.Getenv("SCHEMA_REPORT") != "" {
		var ks []string
		for k := range schemaTestIssues {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Println("[SCHEMA]", k)
		}
	}
	if len(schemaTestFatal) > 0 {
		// a packet the game cannot decode is a test failure
		fmt.Println("FAIL: packets the game cannot decode (schema.json):")
		for _, f := range schemaTestFatal {
			fmt.Println("  ", f)
		}
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

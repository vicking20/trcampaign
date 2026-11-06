package main

import (
	"path/filepath"
	"sort"
)

// globOld lists rotated copies of path (path.YYYYMMDD-HHMMSS), oldest first.
func globOld(path string) ([]string, error) {
	m, err := filepath.Glob(path + ".2*")
	sort.Strings(m)
	return m, err
}

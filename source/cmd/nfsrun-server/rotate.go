package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// rotatingFile keeps logs\server.log below a size limit: long runs
// with LOG_TDF=true wrote hundreds of MB. When the file passes max bytes it is
// renamed to server-<time>.log and a new one starts; only the newest keep
// rotated files are kept.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
	max  int64
	keep int
}

func openRotating(path string, maxBytes int64, keep int) (*rotatingFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	st, _ := f.Stat()
	r := &rotatingFile{path: path, f: f, max: maxBytes, keep: keep}
	if st != nil {
		r.size = st.Size()
	}
	return r, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.max > 0 && r.size+int64(len(p)) > r.max {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() {
	_ = r.f.Close()
	dst := fmt.Sprintf("%s.%s", r.path, time.Now().Format("20060102-150405"))
	_ = os.Rename(r.path, dst)
	if old, _ := globOld(r.path); len(old) > r.keep {
		for _, o := range old[:len(old)-r.keep] {
			_ = os.Remove(o)
		}
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err == nil {
		r.f, r.size = f, 0
	}
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

package playlistpatch

import (
	"bytes"
	"encoding/binary"
	"errors"
)

type run struct {
	size int
	src  int32
	off  int // base/common offset (src 1 / -1)
	data []byte
}

func readRuns(delta []byte) ([]run, []byte, error) {
	if len(delta) < 16 {
		return nil, nil, errors.New("delta bundle too short")
	}
	rt := int(be32(delta, 0))
	if rt%16 != 0 || 16+rt > len(delta) {
		return nil, nil, errors.New("bad delta run table")
	}
	pd := 16 + rt
	var runs []run
	for o := 16; o < 16+rt; o += 16 {
		r := run{size: int(be32(delta, o)), src: int32(be32(delta, o+4)), off: int(binary.BigEndian.Uint64(delta[o+8:]))}
		if r.src == 0 {
			if pd+r.size > len(delta) {
				return nil, nil, errors.New("delta data out of range")
			}
			r.data = delta[pd : pd+r.size]
			pd += r.size
		}
		runs = append(runs, r)
	}
	if pd != len(delta) {
		return nil, nil, errors.New("delta data size mismatch")
	}
	return runs, delta[:16], nil
}

func writeRuns(head []byte, runs []run) []byte {
	var t, data bytes.Buffer
	for _, r := range runs {
		var b [16]byte
		binary.BigEndian.PutUint32(b[0:], uint32(r.size))
		binary.BigEndian.PutUint32(b[4:], uint32(r.src))
		if r.src != 0 {
			binary.BigEndian.PutUint64(b[8:], uint64(r.off))
		}
		t.Write(b[:])
		if r.src == 0 {
			data.Write(r.data)
		}
	}
	out := make([]byte, 0, 16+t.Len()+data.Len())
	out = append(out, head...)
	binary.BigEndian.PutUint32(out[0:], uint32(t.Len()))
	out = append(out, t.Bytes()...)
	return append(out, data.Bytes()...)
}

// patchDelta rewrites an Update\Patch delta so that the bundle it builds is
// want (the patched full bundle) instead of old (what it built before). The
// two may differ only inside the bundle header (same length) and in one
// region [ps, oldEnd) of old that becomes [ps, ps+len(repl)) in want. Every
// other run stays as EA made it; the run holding the region is split and the
// new bytes become delta data (source 0).
func patchDelta(delta []byte, old, want []byte, hdrLen, ps, oldEnd int, repl []byte) ([]byte, error) {
	runs, head, err := readRuns(delta)
	if err != nil {
		return nil, err
	}
	var out []run
	pos := 0
	done := false
	for _, r := range runs {
		rs, re := pos, pos+r.size
		pos = re
		// header bytes live in delta data: take them from want
		if rs < hdrLen {
			if r.src != 0 {
				return nil, errors.New("bundle header not in delta data")
			}
			d := append([]byte(nil), r.data...)
			end := re
			if end > hdrLen {
				end = hdrLen
			}
			copy(d[:end-rs], want[rs:end])
			r.data = d
		}
		if done || re <= ps || rs >= oldEnd {
			out = append(out, r)
			continue
		}
		if rs > ps || re < oldEnd {
			return nil, errors.New("the PlaylistSet spans several delta runs")
		}
		pre, post := ps-rs, re-oldEnd
		if pre > 0 {
			p := run{size: pre, src: r.src, off: r.off}
			if r.src == 0 {
				p.data = r.data[:pre]
			}
			out = append(out, p)
		}
		out = append(out, run{size: len(repl), src: 0, data: repl})
		if post > 0 {
			p := run{size: post, src: r.src, off: r.off + (oldEnd - rs)}
			if r.src == 0 {
				p.data = r.data[oldEnd-rs:]
			}
			out = append(out, p)
		}
		done = true
	}
	if !done {
		return nil, errors.New("PlaylistSet not found in the delta runs")
	}
	if pos != len(old) {
		return nil, errors.New("delta runs do not cover the bundle")
	}
	return writeRuns(head, out), nil
}

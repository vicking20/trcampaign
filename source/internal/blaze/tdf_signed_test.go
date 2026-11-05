package blaze

import "testing"

// Heat2 ints: bit 6 of the first byte is the sign.
func TestSignedVarInt(t *testing.T) {
	for _, c := range []struct {
		raw  []byte
		want int64
	}{
		{[]byte{0x01}, 1},
		{[]byte{0x41}, -1},
		{[]byte{0x3F}, 63},
		{[]byte{0x80, 0x01}, 64},
		{[]byte{0xC0, 0x01}, -64},
		{[]byte{0x7F}, -63},
	} {
		var w Writer
		w.Tag("VALU", TDFVarInt)
		w.Write(c.raw)
		fs, err := DecodeAll(w.Bytes())
		if err != nil || len(fs) != 1 {
			t.Fatal(c.raw, err)
		}
		if got := int64(fs[0].Value.(uint64)); got != c.want {
			t.Fatalf("% x -> %d, want %d", c.raw, got, c.want)
		}
	}
}

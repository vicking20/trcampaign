package blaze

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

type TDFType byte

const (
	TDFVarInt TDFType = iota
	TDFString
	TDFBlob
	TDFGroup
	TDFList
	TDFMap
	TDFUnion
	TDFVarIntList
	TDFPair
	TDFTriple
	TDFFloat
)

type Writer struct{ bytes.Buffer }

func encodeTag(tag string) [3]byte {
	t := []byte(strings.ToUpper(tag))
	if len(t) > 4 {
		t = t[:4]
	}
	var o [3]byte
	if len(t) > 0 {
		o[0] |= (t[0] & 0x40) << 1
		o[0] |= (t[0] & 0x10) << 2
		o[0] |= (t[0] & 0x0f) << 2
	}
	if len(t) > 1 {
		o[0] |= (t[1] & 0x40) >> 5
		o[0] |= (t[1] & 0x10) >> 4
		o[1] |= (t[1] & 0x0f) << 4
	}
	if len(t) > 2 {
		o[1] |= (t[2] & 0x40) >> 3
		o[1] |= (t[2] & 0x10) >> 2
		o[1] |= (t[2] & 0x0c) >> 2
		o[2] |= (t[2] & 0x03) << 6
	}
	if len(t) > 3 {
		o[2] |= (t[3] & 0x40) >> 1
		o[2] |= t[3] & 0x1f
	}
	return o
}

func decodeTag(b [3]byte) string {
	// Heat tag: four 6-bit characters (c-0x20) packed into 24 bits; a zero
	// character ends a shorter tag.
	vals := []byte{b[0] >> 2, (b[0]&0x03)<<4 | b[1]>>4, (b[1]&0x0f)<<2 | b[2]>>6, b[2] & 0x3f}
	out := make([]byte, 0, 4)
	for _, v := range vals {
		if v == 0 {
			break
		}
		out = append(out, v+0x20)
	}
	return string(out)
}

func (w *Writer) Tag(tag string, typ TDFType) {
	t := encodeTag(tag)
	w.Write(t[:])
	w.WriteByte(byte(typ))
}
func (w *Writer) Var(v uint64) {
	if v < 64 {
		w.WriteByte(byte(v))
		return
	}
	w.WriteByte(byte(v&63) | 0x80)
	v >>= 6
	for v >= 128 {
		w.WriteByte(byte(v&127) | 0x80)
		v >>= 7
	}
	w.WriteByte(byte(v))
}
func (w *Writer) TagVar(tag string, v uint64) { w.Tag(tag, TDFVarInt); w.Var(v) }
func (w *Writer) TagBool(tag string, v bool) {
	if v {
		w.TagVar(tag, 1)
	} else {
		w.TagVar(tag, 0)
	}
}
func (w *Writer) TagString(tag, s string) {
	w.Tag(tag, TDFString)
	w.Var(uint64(len(s) + 1))
	w.WriteString(s)
	w.WriteByte(0)
}
func (w *Writer) TagBlob(tag string, b []byte) {
	w.Tag(tag, TDFBlob)
	w.Var(uint64(len(b)))
	w.Write(b)
}
func (w *Writer) GroupStart(tag string)           { w.Tag(tag, TDFGroup) }
func (w *Writer) GroupEnd()                       { w.WriteByte(0) }
func (w *Writer) UnionStart(tag string, key byte) { w.Tag(tag, TDFUnion); w.WriteByte(key) }
func (w *Writer) EmptyList(tag string, item TDFType) {
	w.Tag(tag, TDFList)
	w.WriteByte(byte(item))
	w.Var(0)
}
func (w *Writer) EmptyMap(tag string, key, value TDFType) {
	w.Tag(tag, TDFMap)
	w.WriteByte(byte(key))
	w.WriteByte(byte(value))
	w.Var(0)
}
func (w *Writer) TagTriple(tag string, a, b, c uint64) {
	w.Tag(tag, TDFTriple)
	w.Var(a)
	w.Var(b)
	w.Var(c)
}
func (w *Writer) TagFloat(tag string, v float32) {
	w.Tag(tag, TDFFloat)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], math.Float32bits(v))
	w.Write(b[:])
}

type Field struct {
	Tag   string
	Type  TDFType
	Value any
}
type Reader struct {
	b []byte
	p int
}

func NewReader(b []byte) *Reader { return &Reader{b: b} }
func (r *Reader) rem() int       { return len(r.b) - r.p }
func (r *Reader) byte() (byte, error) {
	if r.rem() < 1 {
		return 0, fmt.Errorf("eof")
	}
	v := r.b[r.p]
	r.p++
	return v, nil
}
func (r *Reader) varint() (uint64, error) {
	f, e := r.byte()
	if e != nil {
		return 0, e
	}
	// Heat2 integers: bit 7 = more bytes, bit 6 of the first byte = negative,
	// magnitude in the rest. Negatives are returned as two's complement.
	neg := f&0x40 != 0
	v := uint64(f & 63)
	sign := func(v uint64) uint64 {
		if neg {
			return uint64(-int64(v))
		}
		return v
	}
	if f&0x80 == 0 {
		return sign(v), nil
	}
	shift := uint(6)
	for i := 0; i < 9; i++ {
		b, e := r.byte()
		if e != nil {
			return 0, e
		}
		v |= uint64(b&127) << shift
		if b&0x80 == 0 {
			return sign(v), nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("varint overflow")
}
func (r *Reader) header() (string, TDFType, error) {
	if r.rem() < 4 {
		return "", 0, fmt.Errorf("eof")
	}
	var t [3]byte
	copy(t[:], r.b[r.p:r.p+3])
	r.p += 3
	ty, e := r.byte()
	return decodeTag(t), TDFType(ty), e
}
func (r *Reader) value(ty TDFType) (any, error) {
	switch ty {
	case TDFVarInt:
		return r.varint()
	case TDFString:
		n, e := r.varint()
		if e != nil {
			return nil, e
		}
		if n > uint64(r.rem()) {
			return nil, fmt.Errorf("bad string len %d", n)
		}
		x := r.b[r.p : r.p+int(n)]
		r.p += int(n)
		if len(x) > 0 && x[len(x)-1] == 0 {
			x = x[:len(x)-1]
		}
		return string(x), nil
	case TDFBlob:
		n, e := r.varint()
		if e != nil {
			return nil, e
		}
		if n > uint64(r.rem()) {
			return nil, fmt.Errorf("bad blob len")
		}
		x := append([]byte(nil), r.b[r.p:r.p+int(n)]...)
		r.p += int(n)
		return x, nil
	case TDFGroup:
		fields := []Field{}
		for {
			if r.rem() < 1 {
				return fields, fmt.Errorf("unterminated group")
			}
			if r.b[r.p] == 0 {
				r.p++
				break
			}
			f, e := r.Field()
			if e != nil {
				return fields, e
			}
			fields = append(fields, f)
		}
		return fields, nil
	case TDFUnion:
		k, e := r.byte()
		if e != nil {
			return nil, e
		}
		if k == 0x7f {
			return map[string]any{"key": k}, nil
		}
		f, e := r.Field()
		if e != nil {
			return nil, e
		}
		return map[string]any{"key": k, "field": f}, nil
	case TDFList:
		it, e := r.byte()
		if e != nil {
			return nil, e
		}
		n, e := r.varint()
		if e != nil {
			return nil, e
		}
		arr := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			t := TDFType(it)
			// Lists of unions (NetworkAddress) use element type 3 (STRUCT): member byte
			// + the member's struct body (no VALU header) + terminator.
			if t == TDFGroup && r.unionElementAhead() {
				v, e := r.listUnionElement()
				if e != nil {
					return arr, e
				}
				arr = append(arr, v)
				continue
			}
			v, e := r.value(t)
			if e != nil {
				return arr, e
			}
			arr = append(arr, v)
		}
		return map[string]any{"type": TDFType(it), "items": arr}, nil
	case TDFMap:
		kt, e := r.byte()
		if e != nil {
			return nil, e
		}
		vt, e := r.byte()
		if e != nil {
			return nil, e
		}
		n, e := r.varint()
		if e != nil {
			return nil, e
		}
		arr := make([][2]any, 0, n)
		for i := uint64(0); i < n; i++ {
			k, e := r.value(TDFType(kt))
			if e != nil {
				return arr, e
			}
			v, e := r.value(TDFType(vt))
			if e != nil {
				return arr, e
			}
			arr = append(arr, [2]any{k, v})
		}
		return map[string]any{"keyType": TDFType(kt), "valueType": TDFType(vt), "items": arr}, nil
	case TDFVarIntList:
		n, e := r.varint()
		if e != nil {
			return nil, e
		}
		a := make([]uint64, 0, n)
		for i := uint64(0); i < n; i++ {
			v, e := r.varint()
			if e != nil {
				return a, e
			}
			a = append(a, v)
		}
		return a, nil
	case TDFPair:
		a, e := r.varint()
		if e != nil {
			return nil, e
		}
		b, e := r.varint()
		return [2]uint64{a, b}, e
	case TDFTriple:
		a, e := r.varint()
		if e != nil {
			return nil, e
		}
		b, e := r.varint()
		if e != nil {
			return nil, e
		}
		c, e := r.varint()
		return [3]uint64{a, b, c}, e
	case TDFFloat:
		if r.rem() < 4 {
			return nil, fmt.Errorf("short float")
		}
		v := math.Float32frombits(binary.BigEndian.Uint32(r.b[r.p : r.p+4]))
		r.p += 4
		return v, nil
	default:
		return nil, fmt.Errorf("unsupported tdf type %d at %d", ty, r.p)
	}
}
func (r *Reader) Field() (Field, error) {
	tag, ty, e := r.header()
	if e != nil {
		return Field{}, e
	}
	v, e := r.value(ty)
	return Field{Tag: tag, Type: ty, Value: v}, e
}
func DecodeAll(b []byte) ([]Field, error) {
	r := NewReader(b)
	out := []Field{}
	for r.rem() > 0 {
		// Blaze 3 clients may end the top-level struct with 0x00 bytes; trailing zero
		// padding is not a field.
		if r.b[r.p] == 0 && allZero(r.b[r.p:]) {
			break
		}
		f, e := r.Field()
		if e != nil {
			return out, e
		}
		out = append(out, f)
	}
	return out, nil
}
func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func FindString(fields []Field, tags ...string) string {
	want := map[string]bool{}
	for _, t := range tags {
		want[strings.ToUpper(t)] = true
	}
	var walk func([]Field) string
	walk = func(fs []Field) string {
		for _, f := range fs {
			if want[f.Tag] {
				if s, ok := f.Value.(string); ok && s != "" {
					return s
				}
			}
			if g, ok := f.Value.([]Field); ok {
				if s := walk(g); s != "" {
					return s
				}
			}
		}
		return ""
	}
	return walk(fields)
}
func FormatFields(fields []Field, indent string) string {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%s%s type=%d ", indent, f.Tag, f.Type)
		switch v := f.Value.(type) {
		case []Field:
			fmt.Fprintf(&b, "{\n%s%s}\n", FormatFields(v, indent+"  "), indent)
		case uint64:
			fmt.Fprintln(&b, fmtInt(v))
		default:
			fmt.Fprintf(&b, "%v\n", v)
		}
	}
	return b.String()
}

// fmtInt prints negative Heat2 ints (two's complement) as "-1".
func fmtInt(v uint64) string {
	if int64(v) < 0 {
		return fmt.Sprint(int64(v))
	}
	return fmt.Sprint(v)
}

// FormatFieldsSafe formats fields for logs, hiding credentials and tokens.
func FormatFieldsSafe(fields []Field, indent string) string {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%s%s type=%d ", indent, f.Tag, f.Type)
		switch v := f.Value.(type) {
		case []Field:
			fmt.Fprintf(&b, "{\n%s%s}\n", FormatFieldsSafe(v, indent+"  "), indent)
		default:
			if f.Tag == "PASS" || f.Tag == "TOKN" || f.Tag == "AUTH" || f.Tag == "KEY" || f.Tag == "PCTK" {
				if str, ok := v.(string); ok && str == "" {
					fmt.Fprintln(&b, "<empty>")
				} else {
					fmt.Fprintln(&b, "<redacted>")
				}
			} else if n, ok := v.(uint64); ok {
				fmt.Fprintln(&b, fmtInt(n))
			} else {
				fmt.Fprintf(&b, "%v\n", v)
			}
		}
	}
	return b.String()
}

// unionElementAhead: the next list element is a union (member byte 1..4,
// 0x7f = none, or 0 followed by a field tag) rather than a plain struct, whose
// first byte is a tag byte (>= 0x40) or 0 (empty struct).
func (r *Reader) unionElementAhead() bool {
	if r.rem() < 1 {
		return false
	}
	k := r.b[r.p]
	switch {
	case k == 0x7f || (k >= 1 && k <= 4):
		return true
	case k == 0:
		return r.rem() >= 2 && r.b[r.p+1] >= 0x80
	}
	return false
}

// listUnionElement reads member byte + struct body, returned like a union
// value ({"key", "field": VALU group}).
func (r *Reader) listUnionElement() (any, error) {
	k, e := r.byte()
	if e != nil {
		return nil, e
	}
	if k == 0x7f {
		return map[string]any{"key": k}, nil
	}
	body, e := r.value(TDFGroup)
	if e != nil {
		return nil, e
	}
	return map[string]any{"key": k, "field": Field{Tag: "VALU", Type: TDFGroup, Value: body}}, nil
}

package blaze

// Blaze schema of Need for Speed The Run, generated from the game binaries
// (tools/blaze_schema): TDF member tables, RPC and notification ids, and the
// request/response/error type of every call.
//
// The client's Heat2 decoder reads members in tag order: same tag and wire
// type is decoded; same tag with another wire type is an error that loses the
// rest of the TDF; a lower tag is skipped; a higher tag leaves the member at
// its default. Replies must therefore use ascending tags and the right wire
// types.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed schema.json
var schemaJSON []byte

type SchemaMember struct {
	Tag     string `json:"tag"`
	Name    string `json:"name"`
	Spec    string `json:"spec"` // int|string|blob|float|objtype|objid|variable|struct:T|union:T|list:E|map:K|V
	Default any    `json:"default,omitempty"`
}

type SchemaRPC struct {
	Component string `json:"component"`
	Name      string `json:"name"`
	Request   any    `json:"request"`
	Response  any    `json:"response"`
	Error     any    `json:"error"`
}

type SchemaNotification struct {
	Component  string `json:"component"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Confidence string `json:"confidence"`
}

type Schema struct {
	Errors        map[string]string             `json:"errors"` // name -> "0x<code><component>"
	Types         map[string][]SchemaMember     `json:"types"`
	Unions        map[string]map[string]string  `json:"unions"`
	Commands      map[string]SchemaRPC          `json:"commands"`
	Notifications map[string]SchemaNotification `json:"notifications"`
}

var (
	schemaOnce sync.Once
	schema     *Schema
)

// GameSchema returns the parsed schema (nil only if schema.json is broken,
// which the tests catch).
func GameSchema() *Schema {
	schemaOnce.Do(func() {
		var s Schema
		if err := json.Unmarshal(schemaJSON, &s); err == nil {
			schema = &s
		}
	})
	return schema
}

func key(component, command uint16) string { return fmt.Sprintf("%04x:%04x", component, command) }

func asType(v any) string {
	if s, ok := v.(string); ok && s != "void" {
		return s
	}
	return ""
}

// ResponseType is the TDF the client decodes a reply of component.command
// into ("" = no payload expected, or unknown).
func (s *Schema) ResponseType(component, command uint16) (typ string, known bool) {
	r, ok := s.Commands[key(component, command)]
	if !ok {
		return "", false
	}
	return asType(r.Response), r.Response != nil
}

// ErrorType is the TDF of an error reply ("" = none).
func (s *Schema) ErrorType(component, command uint16) string {
	if r, ok := s.Commands[key(component, command)]; ok {
		return asType(r.Error)
	}
	return ""
}

// RequestType is the TDF the client sends ("" = unknown).
func (s *Schema) RequestType(component, command uint16) string {
	if r, ok := s.Commands[key(component, command)]; ok {
		return asType(r.Request)
	}
	return ""
}

// NotificationType is the TDF the client decodes a notification into.
func (s *Schema) NotificationType(component, command uint16) string {
	if n, ok := s.Notifications[key(component, command)]; ok {
		return n.Type
	}
	return ""
}

// Issue is one difference between what we send and what the client decodes.
type Issue struct {
	Path   string
	Fatal  bool // the client's decoder fails (wrong wire type, bad framing)
	Detail string
}

func (i Issue) String() string {
	lvl := "note"
	if i.Fatal {
		lvl = "ERROR"
	}
	return fmt.Sprintf("%s %s: %s", lvl, i.Path, i.Detail)
}

var wireOf = map[string]TDFType{"int": TDFVarInt, "string": TDFString, "blob": TDFBlob, "float": TDFFloat, "objtype": TDFPair, "objid": TDFTriple, "variable": TDFVarIntList}

func wireFor(spec string) TDFType {
	switch {
	case strings.HasPrefix(spec, "struct:"):
		return TDFGroup
	case strings.HasPrefix(spec, "union:"):
		return TDFUnion
	case strings.HasPrefix(spec, "list:"):
		return TDFList
	case strings.HasPrefix(spec, "map:"):
		return TDFMap
	}
	if w, ok := wireOf[spec]; ok {
		return w
	}
	return 0xff
}

var wireName = []string{"int", "string", "blob", "struct", "list", "map", "union", "variable", "objtype", "objid", "float"}

func wn(t TDFType) string {
	if int(t) < len(wireName) {
		return wireName[t]
	}
	return fmt.Sprintf("type%d", t)
}

// tagValue orders tags like the client (compressed 24-bit value).
func tagValue(tag string) uint32 {
	t := encodeTag(tag)
	return uint32(t[0])<<16 | uint32(t[1])<<8 | uint32(t[2])
}

// Validate checks a top-level payload (no struct terminator) against typ.
func (s *Schema) Validate(typ string, payload []byte) []Issue {
	if s == nil || typ == "" {
		return nil
	}
	v := &validator{s: s, r: NewReader(payload)}
	v.structBody(typ, typ, true)
	if v.r.rem() > 0 && !allZero(v.r.b[v.r.p:]) && len(v.issues) == 0 {
		v.add(typ, true, fmt.Sprintf("%d trailing bytes", v.r.rem()))
	}
	return v.issues
}

type validator struct {
	s      *Schema
	r      *Reader
	issues []Issue
}

func (v *validator) add(path string, fatal bool, d string) {
	if len(v.issues) < 64 {
		v.issues = append(v.issues, Issue{Path: path, Fatal: fatal, Detail: d})
	}
}

// structBody reads members until the 0 terminator (or the end for the top level).
func (v *validator) structBody(typ, path string, top bool) bool {
	members, ok := v.s.Types[typ]
	if !ok && typ != "" {
		v.add(path, false, "type "+typ+" not in schema")
	}
	byTag := map[string]SchemaMember{}
	for _, m := range members {
		byTag[m.Tag] = m
	}
	var last uint32
	for {
		if v.r.rem() == 0 {
			if !top {
				v.add(path, true, "struct not terminated")
				return false
			}
			return true
		}
		if v.r.b[v.r.p] == 0 {
			if top && allZero(v.r.b[v.r.p:]) {
				return true
			}
			v.r.p++
			return true
		}
		tag, wt, err := v.r.header()
		if err != nil {
			v.add(path, true, err.Error())
			return false
		}
		p := path + "." + tag
		tv := tagValue(tag)
		m, known := byTag[tag]
		if tv <= last && last != 0 {
			v.add(p, false, "out of tag order: the client skips it")
		}
		if tv > last {
			last = tv
		}
		if !known {
			if ok {
				v.add(p, false, fmt.Sprintf("unknown member (%s) for %s: ignored by the client", wn(wt), shortType(typ)))
			}
			if !v.skip(wt, p) {
				return false
			}
			continue
		}
		if want := wireFor(m.Spec); want != wt {
			v.add(p, true, fmt.Sprintf("%s (%s) sent as %s, client expects %s: decode error", m.Name, shortType(typ), wn(wt), wn(want)))
			if !v.skip(wt, p) {
				return false
			}
			continue
		}
		if !v.value(m.Spec, wt, p) {
			return false
		}
	}
}

func shortType(t string) string { return strings.TrimPrefix(t, "Blaze::") }

// value validates one value of a known spec (its wire type already matched).
func (v *validator) value(spec string, wt TDFType, path string) bool {
	if strings.Contains(spec, "?") {
		return v.skip(wt, path) // element type not recovered from the binary
	}
	switch {
	case strings.HasPrefix(spec, "struct:"):
		return v.structBody(strings.TrimPrefix(spec, "struct:"), path, false)
	case strings.HasPrefix(spec, "union:"):
		return v.union(strings.TrimPrefix(spec, "union:"), path)
	case strings.HasPrefix(spec, "list:"):
		elem := strings.TrimPrefix(spec, "list:")
		et, err := v.r.byte()
		if err != nil {
			v.add(path, true, err.Error())
			return false
		}
		n, err := v.r.varint()
		if err != nil {
			v.add(path, true, err.Error())
			return false
		}
		want := wireFor(elem)
		isUnion := strings.HasPrefix(elem, "union:")
		if isUnion {
			want = TDFGroup // list elements of a union travel as STRUCT
		}
		if TDFType(et) != want && n > 0 {
			v.add(path, true, fmt.Sprintf("list of %s sent with element type %s, client expects %s", elem, wn(TDFType(et)), wn(want)))
			for i := uint64(0); i < n; i++ {
				if !v.skip(TDFType(et), path) {
					return false
				}
			}
			return true
		}
		for i := uint64(0); i < n; i++ {
			ep := fmt.Sprintf("%s[%d]", path, i)
			if isUnion {
				if !v.unionElement(strings.TrimPrefix(elem, "union:"), ep) {
					return false
				}
				continue
			}
			if !v.value(elem, want, ep) {
				return false
			}
		}
		return true
	case strings.HasPrefix(spec, "map:"):
		kv := strings.SplitN(strings.TrimPrefix(spec, "map:"), "|", 2)
		kt, err := v.r.byte()
		if err != nil {
			return false
		}
		vt, err := v.r.byte()
		if err != nil {
			return false
		}
		n, err := v.r.varint()
		if err != nil {
			return false
		}
		wk, wv := wireFor(kv[0]), wireFor(kv[len(kv)-1])
		bad := n > 0 && (TDFType(kt) != wk || TDFType(vt) != wv)
		if bad {
			v.add(path, true, fmt.Sprintf("map<%s,%s> sent as map<%s,%s>", wn(wk), wn(wv), wn(TDFType(kt)), wn(TDFType(vt))))
		}
		for i := uint64(0); i < n; i++ {
			ep := fmt.Sprintf("%s{%d}", path, i)
			if bad {
				if !v.skip(TDFType(kt), ep) || !v.skip(TDFType(vt), ep) {
					return false
				}
				continue
			}
			if !v.value(kv[0], wk, ep) || !v.value(kv[len(kv)-1], wv, ep) {
				return false
			}
		}
		return true
	}
	return v.skip(wt, path)
}

func (v *validator) union(typ, path string) bool {
	k, err := v.r.byte()
	if err != nil {
		v.add(path, true, err.Error())
		return false
	}
	if k == 0x7f {
		return true
	}
	vs := v.s.Unions[typ]
	spec, ok := vs[fmt.Sprint(k)]
	tag, wt, err := v.r.header()
	if err != nil {
		v.add(path, true, err.Error())
		return false
	}
	if tag != "VALU" {
		v.add(path+"."+tag, true, "union value must be tagged VALU")
	}
	if !ok {
		v.add(path, true, fmt.Sprintf("%s has no member %d", shortType(typ), k))
		return v.skip(wt, path)
	}
	if want := wireFor(spec); want != wt {
		v.add(path+".VALU", true, fmt.Sprintf("member %d sent as %s, client expects %s", k, wn(wt), wn(want)))
		return v.skip(wt, path)
	}
	return v.value(spec, wt, path+".VALU")
}

// unionElement: member byte + the member's struct body (list elements).
func (v *validator) unionElement(typ, path string) bool {
	k, err := v.r.byte()
	if err != nil {
		v.add(path, true, err.Error())
		return false
	}
	if k == 0x7f {
		return true // unset: no body
	}
	spec, ok := v.s.Unions[typ][fmt.Sprint(k)]
	if !ok || !strings.HasPrefix(spec, "struct:") {
		v.add(path, true, fmt.Sprintf("%s has no struct member %d", shortType(typ), k))
		return v.structBody("", path, false)
	}
	return v.structBody(strings.TrimPrefix(spec, "struct:"), path, false)
}

// skip consumes one value of wire type t without checking it.
func (v *validator) skip(t TDFType, path string) bool {
	if t == TDFGroup {
		return v.structBody("", path, false)
	}
	if t == TDFVarIntList {
		// VariableTdf: tdf id + struct body (not used by this game's replies)
		if _, err := v.r.varint(); err != nil {
			return false
		}
		return v.structBody("", path, false)
	}
	if _, err := v.r.value(t); err != nil {
		v.add(path, true, err.Error())
		return false
	}
	return true
}

// Members lists a type's members in tag order (for docs/tests).
func (s *Schema) Members(typ string) []SchemaMember {
	m := append([]SchemaMember(nil), s.Types[typ]...)
	sort.Slice(m, func(i, j int) bool { return tagValue(m[i].Tag) < tagValue(m[j].Tag) })
	return m
}

var (
	errNamesOnce sync.Once
	errNames     map[uint32]string
)

// ErrorName names a reply's error code like the game does (getErrorName):
// codes are (code << 16) | component, system errors (0x4000+) have component 0.
func (s *Schema) ErrorName(component, code uint16) string {
	if s == nil || code == 0 {
		return ""
	}
	errNamesOnce.Do(func() {
		errNames = map[uint32]string{}
		for n, v := range s.Errors {
			var x uint32
			if _, err := fmt.Sscanf(v, "0x%x", &x); err == nil {
				errNames[x] = n
			}
		}
	})
	if n, ok := errNames[uint32(code)<<16|uint32(component)]; ok {
		return n
	}
	if n, ok := errNames[uint32(code)<<16]; ok {
		return n
	}
	return ""
}

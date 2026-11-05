package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Locker (component 0x0014): photo-mode snapshots (category SNAPSHOTS).
//
//	createContent (1)  -> ContentInfo with CID, STTS PENDING and upload/get
//	                      URLs per SUBL name
//	client uploads with HTTP PUT/POST to UURL (this server's HTTP port)
//	confirmUpload (3)  -> STTS ACTIVE
//	listContent (9), listContentForUsers (16), getContentInfo (8),
//	updateContentInfo (4), deleteContent (5)
//	updateRating (12)  -> void. The gallery's "like" sends RATE 1
//	                      (0x00825730); the gallery shows RCNT as the like
//	                      count and UCNT as views (0x009719C0).
//	incrementUseCount (13) -> void, one view of a photo
//
// Files: <LOCKER_DIR>/<cid>/<sub>.bin (+ .type with the uploaded Content-Type).

const (
	ComponentLocker           uint16 = 0x0014
	LockerCreateContent       uint16 = 1
	LockerConfirmUpload       uint16 = 3
	LockerUpdateContentInfo   uint16 = 4
	LockerDeleteContent       uint16 = 5
	LockerGetContentInfo      uint16 = 8
	LockerListContent         uint16 = 9
	LockerUpdateRating        uint16 = 0x0C
	LockerIncrementUseCount   uint16 = 0x0D
	LockerListContentForUsers uint16 = 0x10
	lockerStatusActive        uint64 = 0
	lockerStatusPending       uint64 = 1
	lockerPermPrivate         uint64 = 1
	lockerMaxPerUser                 = 200
	lockerMaxUploadBytes             = 16 << 20
	lockerMainSub                    = "main"
)

type lockerAttr struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func lockerAttrs(fs []blaze.Field) []lockerAttr {
	var out []lockerAttr
	for _, a := range fieldStructList(fs, "ATTR") {
		out = append(out, lockerAttr{Name: blaze.FindString(a, "NAME"), Value: blaze.FindString(a, "VALU")})
	}
	return out
}

var subNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

func cleanSubNames(names []string) []string {
	var out []string
	for _, n := range names {
		if subNameRe.MatchString(n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) lockerBaseURL() string {
	if s.cfg.LockerBaseURL != "" {
		return strings.TrimRight(s.cfg.LockerBaseURL, "/")
	}
	_, port, err := net.SplitHostPort(s.cfg.HTTPListen)
	if err != nil || port == "" {
		port = "18080"
	}
	return "http://" + s.cfg.PublicHost + ":" + port
}

func (s *Server) lockerURLs(c database.LockerContent, sub string) (upload, get string) {
	base := s.lockerBaseURL()
	// The client appends "&pers=<BlazeId>" to these URLs, so they already carry a
	// query.
	return fmt.Sprintf("%s/locker/upload/%d/%s/%s?v=1", base, c.ID, c.UploadToken, sub),
		fmt.Sprintf("%s/locker/get/%d/%s?v=1", base, c.ID, sub)
}

// lockerRatingStore: ratings and view counts (optional for test stores).
type lockerRatingStore interface {
	RateLockerContent(contentID, personaID uint64, rating int64) error
	IncrementLockerUseCount(contentID uint64) error
	LockerMyRatings(personaID uint64, ids []uint64) (map[uint64]int64, error)
}

// myRatings: the caller's ratings of the listed items (URAT).
func (s *Server) myRatings(me database.Persona, list []database.LockerContent) map[uint64]int64 {
	rs, ok := s.db.(lockerRatingStore)
	if !ok || len(list) == 0 {
		return nil
	}
	ids := make([]uint64, len(list))
	for i, c := range list {
		ids[i] = c.ID
	}
	m, err := rs.LockerMyRatings(me.ID, ids)
	if err != nil {
		s.log.Printf("[LOCKER] ratings: %v", err)
	}
	return m
}

func (s *Server) writeContentInfo(w *blaze.Writer, tag string, c database.LockerContent, wireOwner uint64, myRating int64) {
	var attrs []lockerAttr
	_ = json.Unmarshal([]byte(c.AttributesJSON), &attrs)
	subs := []string{}
	if c.SubNames != "" {
		subs = strings.Split(c.SubNames, ",")
	}
	up, get := s.lockerURLs(c, lockerMainSub)

	w.GroupStart(tag)
	writeStructListHeader(w, "ATTR", len(attrs))
	for _, a := range attrs {
		w.TagString("NAME", a.Name)
		w.TagString("VALU", a.Value)
		w.GroupEnd()
	}
	w.TagTriple("BOID", uint64(ComponentLocker), 1, c.ID)
	w.TagString("CCAT", c.Category)
	w.TagVar("CDAT", uint64(c.Created))
	w.TagVar("CID", c.ID)
	w.TagVar("COID", 0)
	w.TagString("DESC", c.Description)
	w.TagString("DFMT", "")
	w.TagVar("EDAT", 0)
	w.TagVar("EID", wireOwner)
	w.TagString("ENAM", c.OwnerName)
	w.TagTriple("GPID", 0, 0, 0)
	w.TagString("GURL", get)
	w.TagString("NAME", c.Name)
	w.TagVar("PERM", c.Permission)
	// RATE: average rating, RCNT: number of ratings ("likes"), URAT: the
	// caller's own rating.
	rate := int64(0)
	if c.RatingCount > 0 {
		rate = c.RatingSum / int64(c.RatingCount)
	}
	w.TagVar("RATE", uint64(rate))
	w.TagVar("RCNT", c.RatingCount)
	w.TagVar("SIZE", c.Size)
	w.TagVar("STTS", c.Status)
	// SUBL map<string, SubContentInfo{GURL, STTS, UURL, XID}>
	w.Tag("SUBL", blaze.TDFMap)
	w.WriteByte(byte(blaze.TDFString))
	w.WriteByte(byte(blaze.TDFGroup))
	w.Var(uint64(len(subs)))
	for _, sub := range subs { // stored sorted
		su, sg := s.lockerURLs(c, sub)
		w.Var(uint64(len(sub) + 1))
		w.WriteString(sub)
		w.WriteByte(0)
		w.TagString("GURL", sg)
		w.TagVar("STTS", c.Status)
		w.TagString("UURL", su)
		w.TagString("XID", "")
		w.GroupEnd()
	}
	writeStructListHeader(w, "TAGS", 0)
	w.TagVar("UCNT", c.UseCount)
	w.TagVar("UDAT", uint64(c.Updated))
	w.TagVar("URAT", uint64(myRating))
	if c.Status == lockerStatusPending {
		w.TagString("UURL", up)
	} else {
		w.TagString("UURL", "")
	}
	w.TagString("XID", "")
	w.GroupEnd()
}

func (s *Server) wireOwner(c database.LockerContent) uint64 {
	return database.Persona{ID: c.PersonaID, BlazeID: c.BlazeID}.BlazeID
}

func (s *Server) contentInfoResponse(c database.LockerContent, me database.Persona) []byte {
	var w blaze.Writer
	s.writeContentInfo(&w, "INFO", c, s.wireOwner(c), s.myRatings(me, []database.LockerContent{c})[c.ID])
	return w.Bytes()
}

func contentIDFromInfo(fs []blaze.Field) uint64 {
	for _, f := range fs {
		if f.Tag == "INFO" {
			if g, ok := f.Value.([]blaze.Field); ok {
				return fieldUintAuth(g, "CID")
			}
		}
	}
	return fieldUintAuth(fs, "CID")
}

func (s *Server) handleLocker(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	name := blaze.CommandName(p.Component, p.Command)
	me := state.Persona
	switch p.Command {
	case LockerCreateContent:
		if me.ID == 0 {
			return s.errorResp(p, 1)
		}
		attrs, _ := json.Marshal(lockerAttrs(fields))
		subs := cleanSubNames(stringList(fields, "SUBL"))
		sortStrings(subs)
		c, err := s.db.CreateLockerContent(me.ID, blaze.FindString(fields, "CCAT"), blaze.FindString(fields, "NAME"),
			blaze.FindString(fields, "DESC"), string(attrs), strings.Join(subs, ","), fieldUintAuth(fields, "PERM"))
		if err != nil {
			s.log.Printf("[LOCKER] %s failed: %v", name, err)
			return s.errorResp(p, 1)
		}
		up, _ := s.lockerURLs(c, lockerMainSub)
		s.log.Printf("[LOCKER] %s: %s #%d by %q subs=%v upload=%s", name, c.Category, c.ID, me.Name, subs, up)
		return s.response(p, s.contentInfoResponse(c, me))

	case LockerConfirmUpload:
		cid := contentIDFromInfo(fields)
		c, ok, err := s.db.GetLockerContent(cid)
		if err != nil || !ok || c.PersonaID != me.ID {
			s.log.Printf("[LOCKER] %s: content %d not found/owned (%v)", name, cid, err)
			return s.errorResp(p, 1)
		}
		status := lockerStatusActive
		if fieldUintAuth(fields, "STST") != 0 {
			status = lockerStatusPending
		}
		size := s.lockerStoredSize(c)
		if err := s.db.SetLockerStatus(cid, status, size); err != nil {
			s.log.Printf("[LOCKER] %s: %v", name, err)
		}
		c.Status, c.Size = status, size
		s.log.Printf("[LOCKER] %s: #%d status=%d stored=%d bytes", name, cid, status, size)
		return s.response(p, s.contentInfoResponse(c, me))

	case LockerGetContentInfo:
		cid := fieldUintAuth(fields, "CID")
		c, ok, err := s.db.GetLockerContent(cid)
		if err != nil || !ok {
			return s.errorResp(p, 1)
		}
		return s.response(p, s.contentInfoResponse(c, me))

	case LockerUpdateContentInfo:
		cid := fieldUintAuth(fields, "CID")
		if cid == 0 {
			return s.errorResp(p, 1)
		}
		attrs, _ := json.Marshal(lockerAttrs(fields))
		if err := s.db.UpdateLockerContent(cid, me.ID, blaze.FindString(fields, "NAME"), blaze.FindString(fields, "DESC"), string(attrs)); err != nil {
			s.log.Printf("[LOCKER] %s: %v", name, err)
		}
		c, ok, err := s.db.GetLockerContent(cid)
		if err != nil || !ok {
			return s.errorResp(p, 1)
		}
		return s.response(p, s.contentInfoResponse(c, me))

	case LockerDeleteContent:
		ids := idList(fields, "CIDS")
		if err := s.db.DeleteLockerContents(me.ID, ids); err != nil {
			s.log.Printf("[LOCKER] %s: %v", name, err)
		}
		for _, id := range ids {
			_ = os.RemoveAll(filepath.Join(s.cfg.LockerDir, strconv.FormatUint(id, 10)))
		}
		s.log.Printf("[LOCKER] %s: %v", name, ids)
		return s.response(p, nil)

	case LockerListContent:
		cat := blaze.FindString(fields, "CCAT")
		owner := fieldUintAuth(fields, "EID")
		ownerID := uint64(0)
		if owner != 0 {
			ownerID = s.personaIDForWire(owner, me)
		}
		limit := int(fieldUintAuth(fields, "MXRC"))
		if limit <= 0 || limit > lockerMaxPerUser {
			limit = lockerMaxPerUser
		}
		list, total, err := s.db.ListLockerContents(cat, ownerID, ownerID != 0 && ownerID == me.ID, int(fieldUintAuth(fields, "OFRC")), limit)
		if err != nil {
			s.log.Printf("[LOCKER] %s: %v", name, err)
		}
		var w blaze.Writer
		writeStructListHeader(&w, "LKRS", len(list))
		mine := s.myRatings(me, list)
		for _, c := range list {
			// list elements are ContentInfo structs: members then GroupEnd
			var inner blaze.Writer
			s.writeContentInfo(&inner, "INFO", c, s.wireOwner(c), mine[c.ID])
			b := inner.Bytes()
			w.Write(b[4:]) // drop the "INFO" struct header: element = struct body + end
		}
		w.TagVar("MSIZ", lockerMaxPerUser)
		w.TagVar("TCNT", total)
		s.log.Printf("[LOCKER] %s: %s owner=%d -> %d of %d", name, cat, owner, len(list), total)
		return s.response(p, w.Bytes())

	case LockerListContentForUsers:
		// ListContentForUsersResponse{LKRS map<int64, ContentInfos{LKRS}>, MSIZ,
		// TCNT}: the given players' items, one entry per id. Private items only for
		// the caller's own id.
		cat := blaze.FindString(fields, "CCAT")
		limit := int(fieldUintAuth(fields, "MXRC"))
		if limit <= 0 || limit > lockerMaxPerUser {
			limit = lockerMaxPerUser
		}
		ids := idList(fields, "EID")
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		type entry struct {
			wire uint64
			list []database.LockerContent
		}
		var entries []entry
		var total uint64
		seen := map[uint64]bool{}
		for _, wire := range ids {
			if seen[wire] {
				continue
			}
			seen[wire] = true
			var list []database.LockerContent
			ownerID := uint64(0)
			if wire == me.BlazeID {
				ownerID = me.ID
			} else if ref, _, ok := s.webPersonaByID(strconv.FormatUint(wire, 10)); ok {
				ownerID = ref.ID
			}
			if ownerID != 0 {
				l, n, err := s.db.ListLockerContents(cat, ownerID, ownerID == me.ID, int(fieldUintAuth(fields, "OFRC")), limit)
				if err != nil {
					s.log.Printf("[LOCKER] %s: %v", name, err)
				}
				list, total = l, total+n
			}
			entries = append(entries, entry{wire, list})
		}
		var w blaze.Writer
		w.Tag("LKRS", blaze.TDFMap)
		w.WriteByte(byte(blaze.TDFVarInt))
		w.WriteByte(byte(blaze.TDFGroup))
		w.Var(uint64(len(entries)))
		counts := []string{}
		for _, e := range entries {
			w.Var(e.wire)
			writeStructListHeader(&w, "LKRS", len(e.list))
			mine := s.myRatings(me, e.list)
			for _, c := range e.list {
				var inner blaze.Writer
				s.writeContentInfo(&inner, "INFO", c, s.wireOwner(c), mine[c.ID])
				w.Write(inner.Bytes()[4:])
			}
			w.GroupEnd() // ContentInfos
			counts = append(counts, fmt.Sprintf("%d:%d", e.wire, len(e.list)))
		}
		w.TagVar("MSIZ", lockerMaxPerUser)
		w.TagVar("TCNT", total)
		s.log.Printf("[LOCKER] %s: %s for %v -> %s", name, cat, ids, strings.Join(counts, " "))
		return s.response(p, w.Bytes())

	case LockerUpdateRating, LockerIncrementUseCount:
		// UpdateRatingRequest{CCAT, CID, RATE} / IncrementUseCountRequest{CCAT,
		// CID}; both replies are empty (the client's job has no response type).
		cid := fieldUintAuth(fields, "CID")
		rs, ok := s.db.(lockerRatingStore)
		if !ok || cid == 0 || me.ID == 0 {
			return s.response(p, nil)
		}
		if _, found, err := s.db.GetLockerContent(cid); err != nil || !found {
			s.log.Printf("[LOCKER] %s: content %d not found (%v)", name, cid, err)
			return s.response(p, nil)
		}
		if p.Command == LockerUpdateRating {
			rate := int64(fieldUintAuth(fields, "RATE"))
			if err := rs.RateLockerContent(cid, me.ID, rate); err != nil {
				s.log.Printf("[LOCKER] %s: %v", name, err)
			} else {
				s.log.Printf("[LOCKER] %s: %q rated #%d with %d", name, me.Name, cid, rate)
			}
		} else if err := rs.IncrementLockerUseCount(cid); err != nil {
			s.log.Printf("[LOCKER] %s: %v", name, err)
		}
		return s.response(p, nil)

	default:
		s.unimplemented(p, "locker.go")
		return s.unimplementedReply(p)
	}
}

// personaIDForWire maps a wire BlazeId back to personas.id for the caller
// (other players: BlazeIds are 1000000+account).
func (s *Server) personaIDForWire(wire uint64, me database.Persona) uint64 {
	if wire == me.BlazeID {
		return me.ID
	}
	if wire < 1000000 {
		return wire
	}
	return 0 // unknown other player in split mode: list everyone's public items
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (s *Server) lockerFile(id uint64, sub string) string {
	return filepath.Join(s.cfg.LockerDir, strconv.FormatUint(id, 10), sub+".bin")
}

func (s *Server) lockerStoredSize(c database.LockerContent) uint64 {
	var total uint64
	subs := append([]string{lockerMainSub}, strings.Split(c.SubNames, ",")...)
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if st, err := os.Stat(s.lockerFile(c.ID, sub)); err == nil {
			total += uint64(st.Size())
		}
	}
	return total
}

// the optional "&..." tail accepts URLs without "?", where the client
// appended "&pers=<id>" to the path
var lockerPath = regexp.MustCompile(`^/locker/(upload|get)/(\d+)/(?:([0-9a-f]{32})/)?([A-Za-z0-9_.-]{1,32})(?:&.*)?$`)

// handleLockerHTTP serves PUT/POST uploads and GET downloads.
func (s *Server) handleLockerHTTP(w http.ResponseWriter, r *http.Request) {
	m := lockerPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	reason := func(code int, why string) {
		s.log.Printf("[LOCKER] HTTP %s %s -> %d (%s)", r.Method, r.URL.RequestURI(), code, why)
		w.WriteHeader(code)
	}
	id, _ := strconv.ParseUint(m[2], 10, 64)
	c, ok, err := s.db.GetLockerContent(id)
	if err != nil || !ok {
		reason(http.StatusNotFound, "no such content")
		return
	}
	sub := m[4]
	file := s.lockerFile(id, sub)
	switch m[1] {
	case "upload":
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if m[3] != c.UploadToken {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, lockerMaxUploadBytes+1))
		if err != nil || len(data) > lockerMaxUploadBytes {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		if err := os.WriteFile(file, data, 0644); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ct := r.Header.Get("Content-Type")
		_ = os.WriteFile(file+".type", []byte(ct), 0644)
		s.log.Printf("[LOCKER] upload #%d/%s: %d bytes (%s)", id, sub, len(data), ct)
		w.WriteHeader(http.StatusOK)
	case "get":
		data, err := os.ReadFile(file)
		if err != nil {
			reason(http.StatusNotFound, "nothing uploaded for this part yet")
			return
		}
		ct, _ := os.ReadFile(file + ".type")
		if len(ct) == 0 {
			ct = []byte(http.DetectContentType(data))
		}
		w.Header().Set("Content-Type", string(ct))
		w.Header().Set("Last-Modified", time.Unix(c.Updated, 0).UTC().Format(http.TimeFormat))
		// explicit length: the game's HTTP client cannot read chunked bodies
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}
}

// Package detect finds the key-based shadow mock a request belongs to (DESIGN.md §7, FR-7.4):
// designated headers, query parameters and cookies first (exact lookups), then Aho-Corasick
// scans of the metadata and the body. The first stage with a hit wins.
package detect

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	ahocorasick "github.com/petar-dambovaliev/aho-corasick"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Stage is a detection stage, as recorded in the journal.
type Stage string

// Stages, in the order they run.
const (
	StageHeader   Stage = "Header"
	StageQuery    Stage = "Query"
	StageCookie   Stage = "Cookie"
	StageScanMeta Stage = "ScanMeta"
	StageScanBody Stage = "ScanBody"
)

// Policy decides what happens when a scan finds keys or tokens of several sessions (FR-7.10).
type Policy string

// Ambiguity policies.
const (
	PolicyFirst  Policy = "first"
	PolicyReject Policy = "reject"
	PolicyGlobal Policy = "global"
)

// ErrAmbiguous is returned under PolicyReject when a scan finds several sessions.
var ErrAmbiguous = errors.New("the request carries keys or tokens of several sessions")

// Config controls detection (DESIGN.md §11, sessions.detect).
type Config struct {
	Headers      []string
	QueryParams  []string
	Cookies      []string
	ScanMetadata bool
	ScanBody     bool
	MaxScanBytes int
	ContentTypes []string
	Decompress   bool
	Ambiguity    Policy
}

// DefaultConfig is the documented default.
func DefaultConfig() Config {
	return Config{
		Headers:      []string{"X-Shadow-Mock-Key"},
		QueryParams:  []string{"shadow_mock_key"},
		Cookies:      []string{"shadow_mock_key"},
		ScanMetadata: true,
		ScanBody:     true,
		MaxScanBytes: 1 << 20,
		ContentTypes: []string{"text/*", "application/json", "application/*+json", "application/xml", "application/x-www-form-urlencoded"},
		Decompress:   true,
		Ambiguity:    PolicyFirst,
	}
}

// Hit is a detected session.
type Hit struct {
	SessionID     string
	Stage         Stage
	Offset        int
	ScanTruncated bool
	// Ambiguous lists every session a scan found when there was more than one.
	Ambiguous []string
}

// group is the set of key-based sessions that can be detected together: one tenant, and one
// URL-based parent ("" for sessions not nested in one).
type group struct {
	tenant tenant.ID
	parent string
}

type groupIndex struct {
	exact  map[string]string // key or token → session id
	ac     ahocorasick.AhoCorasick
	owners []string // pattern index → session id
}

// Index is the detector. Lookups read an immutable snapshot; Rebuild swaps in a new one.
//
// With a source set, session changes only mark the index stale (Invalidate) and the next
// lookup rebuilds it once, so creating many sessions costs one rebuild, and no lookup ever
// sees a stale index.
type Index struct {
	cfg    Config
	groups atomic.Pointer[map[group]*groupIndex]

	source func() []session.Session
	// changes counts Invalidate calls and built the count the current snapshot reflects, so a
	// lookup that arrives during a rebuild waits for it instead of reading the stale snapshot.
	changes atomic.Uint64
	built   atomic.Uint64
	mu      sync.Mutex // serialises rebuilds
}

// New returns an empty index.
func New(cfg Config) *Index {
	ix := &Index{cfg: cfg}
	empty := map[group]*groupIndex{}
	ix.groups.Store(&empty)
	return ix
}

// SetSource sets where Invalidate gets the active sessions from.
func (ix *Index) SetSource(source func() []session.Session) {
	ix.source = source
	ix.changes.Add(1)
}

// Invalidate marks the index stale; the next Detect rebuilds it from the source.
func (ix *Index) Invalidate() { ix.changes.Add(1) }

// refresh rebuilds a stale index. The change count is read before the sessions are, so a
// change made meanwhile leaves the index stale for the next lookup. Lookups that find the
// index stale queue on the lock, so none of them reads the old snapshot.
func (ix *Index) refresh() {
	if ix.source == nil || ix.built.Load() == ix.changes.Load() {
		return
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if n := ix.changes.Load(); ix.built.Load() != n {
		ix.Rebuild(ix.source())
		ix.built.Store(n)
	}
}

// Rebuild replaces the index with one built from the active key-based sessions. It is
// copy-on-write, so in-flight requests keep using the previous index (NFR-2.4).
func (ix *Index) Rebuild(sessions []session.Session) {
	type entry struct{ pattern, owner string }
	byGroup := map[group][]entry{}
	for i := range sessions {
		s := &sessions[i]
		if s.Kind != session.KeyBased {
			continue
		}
		g := group{tenant: s.Tenant, parent: s.ParentID}
		byGroup[g] = append(byGroup[g], entry{s.Key, s.ID})
		for _, tok := range s.Tokens {
			byGroup[g] = append(byGroup[g], entry{tok, s.ID})
		}
	}
	next := make(map[group]*groupIndex, len(byGroup))
	for g, entries := range byGroup {
		sort.Slice(entries, func(i, j int) bool { return entries[i].pattern < entries[j].pattern })
		gi := &groupIndex{exact: make(map[string]string, len(entries)), owners: make([]string, 0, len(entries))}
		patterns := make([]string, 0, len(entries))
		for _, e := range entries {
			gi.exact[e.pattern] = e.owner
			patterns = append(patterns, e.pattern)
			gi.owners = append(gi.owners, e.owner)
		}
		b := ahocorasick.NewAhoCorasickBuilder(ahocorasick.Opts{MatchKind: ahocorasick.LeftMostLongestMatch, DFA: true})
		gi.ac = b.Build(patterns)
		next[g] = gi
	}
	ix.groups.Store(&next)
}

// Detect finds the session for a request. parentID is the URL-based shadow mock the request
// came through ("" for plain traffic), so only sessions nested in it are considered.
func (ix *Index) Detect(t tenant.ID, parentID string, r *match.Request) (Hit, bool, error) {
	ix.refresh()
	gi := (*ix.groups.Load())[group{tenant: t, parent: parentID}]
	if gi == nil {
		return Hit{}, false, nil // fast negative: no active sessions to find (DESIGN.md §7.2)
	}
	if h, ok := ix.exactStages(gi, r); ok {
		return h, true, nil
	}
	if ix.cfg.ScanMetadata {
		if h, ok, err := ix.scan(gi, StageScanMeta, metadata(r), false); ok || err != nil {
			return h, ok, err
		}
	}
	if ix.cfg.ScanBody && ix.scannable(r) {
		body, truncated := ix.bodyToScan(r)
		h, found, err := ix.scan(gi, StageScanBody, body, truncated)
		if !found && err == nil && truncated {
			h.ScanTruncated = true // recorded in the journal even though nothing was found
		}
		return h, found, err
	}
	return Hit{}, false, nil
}

func (ix *Index) exactStages(gi *groupIndex, r *match.Request) (Hit, bool) {
	for _, h := range ix.cfg.Headers {
		for _, v := range r.Header.Values(h) {
			if id, ok := gi.exact[strings.TrimSpace(v)]; ok {
				return Hit{SessionID: id, Stage: StageHeader}, true
			}
		}
	}
	for _, q := range ix.cfg.QueryParams {
		for _, v := range r.Query[q] {
			if id, ok := gi.exact[v]; ok {
				return Hit{SessionID: id, Stage: StageQuery}, true
			}
		}
	}
	for _, c := range ix.cfg.Cookies {
		for _, v := range r.Cookies[c] {
			if id, ok := gi.exact[v]; ok {
				return Hit{SessionID: id, Stage: StageCookie}, true
			}
		}
	}
	return Hit{}, false
}

// scan runs the automaton over haystack and applies the ambiguity policy.
func (ix *Index) scan(gi *groupIndex, stage Stage, haystack []byte, truncated bool) (Hit, bool, error) {
	if len(haystack) == 0 {
		return Hit{}, false, nil
	}
	matches := gi.ac.FindAll(string(haystack))
	if len(matches) == 0 {
		return Hit{}, false, nil
	}
	first := matches[0]
	var owners []string
	for i := range matches {
		owner := gi.owners[matches[i].Pattern()]
		if !contains(owners, owner) {
			owners = append(owners, owner)
		}
		if matches[i].Start() < first.Start() {
			first = matches[i]
		}
	}
	h := Hit{SessionID: gi.owners[first.Pattern()], Stage: stage, Offset: first.Start(), ScanTruncated: truncated}
	if len(owners) == 1 {
		return h, true, nil
	}
	h.Ambiguous = owners
	switch ix.cfg.Ambiguity {
	case PolicyReject:
		return h, false, ErrAmbiguous
	case PolicyGlobal:
		return h, false, nil
	default:
		return h, true, nil
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// metadata joins every header value, the raw query string and the path with NUL separators,
// so a key cannot be matched across two fields.
func metadata(r *match.Request) []byte {
	var b bytes.Buffer
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		names = append(names, name)
	}
	sort.Strings(names) // a stable order keeps the "first" ambiguity policy deterministic
	for _, name := range names {
		if name == "Content-Length" {
			continue
		}
		for _, v := range r.Header[name] {
			b.WriteString(v)
			b.WriteByte(0)
		}
	}
	b.WriteString(r.RawQuery)
	b.WriteByte(0)
	b.WriteString(r.Path)
	return b.Bytes()
}

func (ix *Index) scannable(r *match.Request) bool {
	if len(r.Body) == 0 {
		return false
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	for _, allowed := range ix.cfg.ContentTypes {
		if mediaMatches(allowed, mt) {
			return true
		}
	}
	return false
}

// mediaMatches matches "text/*" and "application/*+json" style patterns.
func mediaMatches(pattern, mt string) bool {
	if pattern == mt {
		return true
	}
	pType, pSub, ok1 := strings.Cut(pattern, "/")
	mType, mSub, ok2 := strings.Cut(mt, "/")
	if !ok1 || !ok2 || pType != mType {
		return false
	}
	if pSub == "*" {
		return true
	}
	if suffix, ok := strings.CutPrefix(pSub, "*"); ok {
		return strings.HasSuffix(mSub, suffix)
	}
	return false
}

// bodyToScan returns at most MaxScanBytes of the body, gunzipped if configured.
func (ix *Index) bodyToScan(r *match.Request) ([]byte, bool) {
	limit := ix.cfg.MaxScanBytes
	body := r.Body
	if ix.cfg.Decompress && strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		if zr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			if plain, err := io.ReadAll(io.LimitReader(zr, int64(limit)+1)); err == nil {
				body = plain
			}
		}
	}
	if limit > 0 && len(body) > limit {
		return body[:limit], true
	}
	return body, false
}

// StatusFor returns the HTTP status for a detection error.
func StatusFor(err error) int {
	if errors.Is(err, ErrAmbiguous) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

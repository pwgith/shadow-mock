package detect

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

const (
	keyA = "smk_AAAAAAAAAAAAAAAAAAAAAAAAAA"
	keyB = "smk_BBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func sessions() []session.Session {
	return []session.Session{
		{ID: "ses_A", Kind: session.KeyBased, Tenant: tenant.Local, Key: keyA, Tokens: []string{"CUST-99812"}},
		{ID: "ses_B", Kind: session.KeyBased, Tenant: tenant.Local, Key: keyB, Tokens: []string{"order-7731"}},
		{ID: "ses_N", Kind: session.KeyBased, Tenant: tenant.Local, Key: "smk_NESTEDNESTEDNESTEDNESTED", ParentID: "urlmock"},
		{ID: "urlmock", Kind: session.URLBased, Tenant: tenant.Local},
		{ID: "ses_O", Kind: session.KeyBased, Tenant: "other", Key: "smk_OTHEROTHEROTHEROTHEROTHER"},
	}
}

func index(cfg Config) *Index {
	ix := New(cfg)
	ix.Rebuild(sessions())
	return ix
}

func req(t *testing.T, method, target, body string, header map[string]string) *match.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	for k, v := range header {
		r.Header.Set(k, v)
	}
	return match.NewRequest(r, []byte(body), r.URL.EscapedPath())
}

func TestDetectStages(t *testing.T) {
	t.Parallel()
	ix := index(DefaultConfig())
	jsonCT := map[string]string{"Content-Type": "application/json"}
	tests := []struct {
		name   string
		r      *match.Request
		parent string
		want   string
		stage  Stage
	}{
		{"designated header", req(t, "GET", "/a", "", map[string]string{"X-Shadow-Mock-Key": keyA}), "", "ses_A", StageHeader},
		{"header token", req(t, "GET", "/a", "", map[string]string{"X-Shadow-Mock-Key": "order-7731"}), "", "ses_B", StageHeader},
		{"query", req(t, "GET", "/a?shadow_mock_key="+keyB, "", nil), "", "ses_B", StageQuery},
		{"cookie", req(t, "GET", "/a", "", map[string]string{"Cookie": "shadow_mock_key=" + keyA}), "", "ses_A", StageCookie},
		{"metadata scan", req(t, "GET", "/a", "", map[string]string{"traceparent": "x-" + keyB + "-y"}), "", "ses_B", StageScanMeta},
		{"path scan", req(t, "GET", "/customers/CUST-99812", "", nil), "", "ses_A", StageScanMeta},
		{"body scan", req(t, "POST", "/a", `{"customer":"CUST-99812"}`, jsonCT), "", "ses_A", StageScanBody},
		{"body content type not scanned", req(t, "POST", "/a", `CUST-99812`, map[string]string{"Content-Type": "image/png"}), "", "", ""},
		{"no hit", req(t, "GET", "/a", "", nil), "", "", ""},
		{"nested session only through its parent", req(t, "GET", "/a", "", map[string]string{"X-Shadow-Mock-Key": "smk_NESTEDNESTEDNESTEDNESTED"}), "urlmock", "ses_N", StageHeader},
		{"nested session not found outside its parent", req(t, "GET", "/a", "", map[string]string{"X-Shadow-Mock-Key": "smk_NESTEDNESTEDNESTEDNESTED"}), "", "", ""},
		{"other tenant's key is ignored", req(t, "GET", "/a", "", map[string]string{"X-Shadow-Mock-Key": "smk_OTHEROTHEROTHEROTHEROTHER"}), "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, found, err := ix.Detect(tenant.Local, tt.parent, tt.r)
			if err != nil {
				t.Fatalf("Detect error: %v", err)
			}
			if found != (tt.want != "") || h.SessionID != tt.want || (found && h.Stage != tt.stage) {
				t.Errorf("Detect = %+v, %v, want %q at %s", h, found, tt.want, tt.stage)
			}
		})
	}
}

func TestDetectAmbiguity(t *testing.T) {
	t.Parallel()
	body := `{"a":"order-7731","b":"CUST-99812"}`
	ct := map[string]string{"Content-Type": "application/json"}
	tests := []struct {
		policy  Policy
		found   bool
		want    string
		wantErr bool
	}{
		{PolicyFirst, true, "ses_B", false},
		{PolicyGlobal, false, "ses_B", false},
		{PolicyReject, false, "ses_B", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.policy), func(t *testing.T) {
			t.Parallel()
			cfg := DefaultConfig()
			cfg.Ambiguity = tt.policy
			h, found, err := index(cfg).Detect(tenant.Local, "", req(t, "POST", "/a", body, ct))
			if found != tt.found || (err != nil) != tt.wantErr || h.SessionID != tt.want || len(h.Ambiguous) != 2 {
				t.Errorf("Detect = %+v, %v, %v", h, found, err)
			}
			if tt.wantErr && (!errors.Is(err, ErrAmbiguous) || StatusFor(err) != http.StatusConflict) {
				t.Errorf("error %v should be ErrAmbiguous with status 409", err)
			}
		})
	}
	if StatusFor(errors.New("x")) != http.StatusInternalServerError {
		t.Error("StatusFor(other) should be 500")
	}
}

func TestDetectBodyHandling(t *testing.T) {
	t.Parallel()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(`{"c":"CUST-99812"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	ix := index(DefaultConfig())
	h, found, err := ix.Detect(tenant.Local, "", req(t, "POST", "/a", gz.String(),
		map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"}))
	if err != nil || !found || h.Stage != StageScanBody {
		t.Errorf("gzip body: %+v, %v, %v", h, found, err)
	}

	cfg := DefaultConfig()
	cfg.MaxScanBytes = 8
	h, found = detected(t, index(cfg), req(t, "POST", "/a", `{"padding":"CUST-99812"}`, map[string]string{"Content-Type": "text/plain"}))
	if found || h.Stage != "" {
		t.Errorf("token beyond maxScanBytes was found: %+v", h)
	}
	if _, truncated := index(cfg).bodyToScan(req(t, "POST", "/a", "0123456789", nil)); !truncated {
		t.Error("bodyToScan should report truncation")
	}

	cfg = DefaultConfig()
	cfg.ScanBody, cfg.ScanMetadata = false, false
	if _, found := detected(t, index(cfg), req(t, "POST", "/CUST-99812", `CUST-99812`, map[string]string{"Content-Type": "text/plain"})); found {
		t.Error("scans disabled but a session was found")
	}
	if _, found := detected(t, index(DefaultConfig()), req(t, "POST", "/a", `x`, map[string]string{"Content-Type": ";;"})); found {
		t.Error("an invalid content type should not be scanned")
	}
}

func TestMediaMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, mt string
		want        bool
	}{
		{"text/*", "text/plain", true},
		{"application/*+json", "application/vnd.api+json", true},
		{"application/*+json", "application/xml", false},
		{"application/json", "application/json", true},
		{"application/json", "text/json", false},
		{"bad", "text/plain", false},
		{"application/xml", "application/json", false},
	}
	for _, tt := range tests {
		if got := mediaMatches(tt.pattern, tt.mt); got != tt.want {
			t.Errorf("mediaMatches(%q, %q) = %v, want %v", tt.pattern, tt.mt, got, tt.want)
		}
	}
}

func TestRebuildDropsRemovedSessions(t *testing.T) {
	t.Parallel()
	ix := index(DefaultConfig())
	ix.Rebuild(nil)
	if _, found := detected(t, ix, req(t, "GET", "/a", "", map[string]string{"X-Shadow-Mock-Key": keyA})); found {
		t.Error("removed session still detected")
	}
}

// detected runs Detect for plain traffic and fails the test on an error.
func detected(t *testing.T, ix *Index, r *match.Request) (Hit, bool) {
	t.Helper()
	h, found, err := ix.Detect(tenant.Local, "", r)
	if err != nil {
		t.Fatal(err)
	}
	return h, found
}

func TestLazyRebuild(t *testing.T) {
	t.Parallel()
	var active []session.Session
	rebuilds := 0
	ix := New(DefaultConfig())
	ix.SetSource(func() []session.Session { rebuilds++; return active })

	for i := range 50 {
		active = append(active, session.Session{
			ID: "ses_" + string(rune('A'+i%26)) + string(rune('a'+i/26)), Kind: session.KeyBased, Tenant: tenant.Local,
			Key: "smk_KEY" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + "XXXXXXXXXXXXXXXXXX",
		})
		ix.Invalidate() // creating many sessions must not rebuild each time
	}
	if rebuilds != 0 {
		t.Fatalf("rebuilt %d times before any lookup, want 0", rebuilds)
	}
	hdr := map[string]string{"X-Shadow-Mock-Key": active[7].Key}
	if h, found := detected(t, ix, req(t, "GET", "/a", "", hdr)); !found || h.SessionID != active[7].ID {
		t.Errorf("Detect = %+v, %v, want %s", h, found, active[7].ID)
	}
	detected(t, ix, req(t, "GET", "/a", "", hdr))
	if rebuilds != 1 {
		t.Errorf("rebuilt %d times for a batch of changes, want 1", rebuilds)
	}

	active = active[:7] // deleting a session is visible to the very next lookup
	ix.Invalidate()
	if _, found := detected(t, ix, req(t, "GET", "/a", "", hdr)); found {
		t.Error("a deleted session was still detected")
	}
}

func TestObserveReportsEachRebuild(t *testing.T) {
	t.Parallel()
	var sizes []int
	var took []time.Duration
	clock := time.Unix(0, 0)
	ix := New(DefaultConfig())
	ix.Observe(func() time.Time { clock = clock.Add(4 * time.Millisecond); return clock }, func(patterns int, d time.Duration) {
		sizes, took = append(sizes, patterns), append(took, d)
	})
	active := sessions()
	want := 0
	for i := range active {
		if active[i].Kind == session.KeyBased {
			want += 1 + len(active[i].Tokens) // the key and each token
		}
	}
	ix.Rebuild(active)
	ix.Rebuild(nil)
	if len(sizes) != 2 || sizes[0] != want || sizes[1] != 0 {
		t.Errorf("reported sizes %v, want [%d 0]", sizes, want)
	}
	if took[0] != 4*time.Millisecond {
		t.Errorf("reported duration %v, want the 4ms the clock advanced", took[0])
	}
}

func TestRebuildWithoutAnObserverIsFine(t *testing.T) {
	t.Parallel()
	New(DefaultConfig()).Rebuild(sessions())
}

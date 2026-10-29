package record_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/record"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func exchange(t *testing.T, method, target, reqBody string, status int, respHeader http.Header, respBody string, reqHeaders ...string) record.Exchange {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "http://mock"+target, strings.NewReader(reqBody))
	for i := 0; i+1 < len(reqHeaders); i += 2 {
		r.Header.Set(reqHeaders[i], reqHeaders[i+1])
	}
	return record.Exchange{Request: match.NewRequest(r, []byte(reqBody), r.URL.EscapedPath()), Status: status, Header: respHeader, Body: []byte(respBody)}
}

func jsonHeader() http.Header {
	return http.Header{"Content-Type": {"application/json"}, "Content-Length": {"9"}, "Date": {"x"}, "X-Keep": {"1"}}
}

func TestBuildBodiesAndHeaders(t *testing.T) {
	t.Parallel()
	ms, _, err := record.Build(&record.Spec{}, []record.Exchange{
		exchange(t, http.MethodGet, "/json?a=1", "", 200, jsonHeader(), `{"id":2}`),
		exchange(t, http.MethodGet, "/text", "", 200, http.Header{"Content-Type": {"text/plain"}}, "hello"),
		exchange(t, http.MethodGet, "/bin", "", 200, http.Header{"Content-Type": {"image/png"}}, "\x89PNG\x00"),
		exchange(t, http.MethodGet, "/empty", "", 204, nil, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 4 {
		t.Fatalf("got %d mappings, want 4", len(ms))
	}
	want := []string{
		`"body":"{\"id\":2}"`,
		`"body":"hello"`,
		`"base64Body":"iVBORwA="`,
		`"status":204`,
	}
	for i, m := range ms {
		b, _ := m.MarshalJSON()
		if !strings.Contains(string(b), want[i]) {
			t.Errorf("mapping %d = %s, want it to contain %s", i, b, want[i])
		}
		if m.ID == "" {
			t.Errorf("mapping %d has no id", i)
		}
	}
	first, _ := ms[0].MarshalJSON()
	for _, s := range []string{`"urlPath":"/json"`, `"queryParameters":{"a":{"hasExactly":[{"equalTo":"1"}]}}`, `"name":"json"`, `"method":"GET"`, `"X-Keep":"1"`} {
		if !strings.Contains(string(first), s) {
			t.Errorf("first mapping %s lacks %s", first, s)
		}
	}
	for _, s := range []string{"Content-Length"} {
		if strings.Contains(string(first), s) {
			t.Errorf("first mapping records the transfer header %s: %s", s, first)
		}
	}
}

func TestBuildCapturesHeadersAndBodies(t *testing.T) {
	t.Parallel()
	spec := &record.Spec{
		CaptureHeaders:     map[string]record.CaptureHeader{"Accept": {CaseInsensitive: true}, "X-Absent": {}},
		RequestBodyPattern: &record.BodyPattern{Matcher: "auto", IgnoreArrayOrder: true},
	}
	ms, _, err := record.Build(spec, []record.Exchange{
		exchange(t, http.MethodPost, "/j", `{"a":[1,2]}`, 200, nil, "", "Accept", "text/plain", "Content-Type", "application/json"),
		exchange(t, http.MethodPost, "/x", `<a/>`, 200, nil, "", "Content-Type", "text/xml"),
		exchange(t, http.MethodPost, "/t", `raw`, 200, nil, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(ms))
	for _, m := range ms {
		b, _ := m.MarshalJSON()
		got = append(got, string(b))
	}
	for i, want := range []string{
		`"bodyPatterns":[{"equalToJson":"{\"a\":[1,2]}","ignoreArrayOrder":true,"ignoreExtraElements":false}]`,
		`"bodyPatterns":[{"equalToXml":"`,
		`"bodyPatterns":[{"caseInsensitive":false,"equalTo":"raw"}]`,
	} {
		if !strings.Contains(got[i], want) {
			t.Errorf("mapping %d = %s, want it to contain %s", i, got[i], want)
		}
	}
	if !strings.Contains(got[0], `"Accept":{"caseInsensitive":true,"equalTo":"text/plain"}`) || strings.Contains(got[0], "X-Absent") {
		t.Errorf("captured headers wrong: %s", got[0])
	}
	// Without a requestBodyPattern the body is matched as WireMock does: by its content type, and JSON
	// ignoring array order and extra elements.
	plain, _, err := record.Build(&record.Spec{}, []record.Exchange{
		exchange(t, http.MethodPost, "/p", "data", 200, nil, ""),
		exchange(t, http.MethodPost, "/q", "{}", 200, nil, "", "Content-Type", "application/json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := plain[1].MarshalJSON(); !strings.Contains(string(b), `{"equalToJson":"{}","ignoreArrayOrder":true,"ignoreExtraElements":true}`) {
		t.Errorf("mapping = %s, want the default JSON body pattern", b)
	}
}

func TestBuildRepeats(t *testing.T) {
	t.Parallel()
	repeats := []record.Exchange{
		exchange(t, http.MethodGet, "/jobs/1", "", 202, nil, "PENDING"),
		exchange(t, http.MethodGet, "/other", "", 200, nil, "o"),
		exchange(t, http.MethodGet, "/jobs/1", "", 200, nil, "COMPLETE"),
	}
	once, _, err := record.Build(&record.Spec{}, repeats)
	if err != nil {
		t.Fatal(err)
	}
	if len(once) != 2 || once[0].HasCallResponses() || once[0].Response.Status != 200 {
		t.Errorf("without repeats: %d mappings, first = %+v; want 2 with the last response kept", len(once), once[0].Response)
	}
	many, _, err := record.Build(&record.Spec{RepeatsAsCallResponses: true}, repeats)
	if err != nil {
		t.Fatal(err)
	}
	if len(many) != 2 || !many[0].HasCallResponses() {
		t.Fatalf("with repeats: %d mappings", len(many))
	}
	if got := []int{many[0].ResponseFor(1).StatusOrDefault(), many[0].ResponseFor(2).StatusOrDefault(), many[0].ResponseFor(9).StatusOrDefault()}; !cmp.Equal(got, []int{202, 200, 200}) {
		t.Errorf("responses for calls 1, 2 and 9 = %v, want [202 200 200]", got)
	}
	if many[1].HasCallResponses() {
		t.Error("a request seen once got call responses")
	}
}

func TestRecorderLifecycle(t *testing.T) {
	t.Parallel()
	r := record.NewRecorder()
	global := store.Global(tenant.Local)
	sess := store.Scope{Tenant: tenant.Local, SessionID: "ses_1"}
	if got := r.Status(global); got != record.StatusNever {
		t.Errorf("initial status = %s", got)
	}
	if err := r.Start(global, record.Spec{}, nil); err == nil {
		t.Error("a recording without a target started")
	}
	spec := record.Spec{TargetBaseURL: "http://up"}
	if err := r.Start(global, spec, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(global, spec, nil); err == nil {
		t.Error("a second recording started in the same scope")
	}
	if got := r.Status(global); got != record.StatusRecording {
		t.Errorf("status = %s", got)
	}
	if r.Find([]store.Scope{sess}) != nil {
		t.Error("a session found the global recording")
	}
	if err := r.Start(sess, spec, nil); err != nil {
		t.Fatal(err)
	}
	// The most specific scope wins.
	if a := r.Find([]store.Scope{sess, global}); a == nil || a != r.Find([]store.Scope{sess}) {
		t.Error("Find did not prefer the session's recording")
	}
	a := r.Stop(global)
	if a == nil || r.Status(global) != record.StatusStopped || r.Stop(global) != nil {
		t.Errorf("stop: %v, status %s", a, r.Status(global))
	}
	r.Forget(sess)
	if r.Status(sess) != record.StatusNever || r.Find([]store.Scope{sess}) != nil {
		t.Error("Forget left the session's recording behind")
	}
}

func TestActiveFilterAndExchanges(t *testing.T) {
	t.Parallel()
	r := record.NewRecorder()
	sc := store.Global(tenant.Local)
	var p model.RequestPattern
	if err := p.UnmarshalJSON([]byte(`{"urlPathPattern":"/claims/.*"}`)); err != nil {
		t.Fatal(err)
	}
	m, err := match.Compile(&p, match.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(sc, record.Spec{TargetBaseURL: "http://up"}, m); err != nil {
		t.Fatal(err)
	}
	a := r.Find([]store.Scope{sc})
	in, out := exchange(t, http.MethodGet, "/claims/1", "", 200, nil, ""), exchange(t, http.MethodGet, "/other", "", 200, nil, "")
	if !a.Accepts(in.Request) || a.Accepts(out.Request) {
		t.Error("the filter did not separate /claims/1 from /other")
	}
	if err := a.Add(in); err != nil {
		t.Fatal(err)
	}
	if got := a.Exchanges(); len(got) != 1 {
		t.Errorf("Exchanges = %d, want 1", len(got))
	}
	if !(&record.Spec{}).PersistOrDefault() {
		t.Error("persist should default to true")
	}
	off := false
	if (&record.Spec{Persist: &off}).PersistOrDefault() {
		t.Error("persist=false was ignored")
	}
}

func TestALiveRecordingHandsOverEachNewRequestOnce(t *testing.T) {
	t.Parallel()
	r := record.NewRecorder()
	sc := store.Global(tenant.Local)
	var got []model.StubMapping
	failNext := false
	err := r.StartLive(sc, record.Spec{TargetBaseURL: "http://up", CaptureHeaders: map[string]record.CaptureHeader{"X-Key": {}}}, nil, func(m model.StubMapping, _ []record.File) error {
		if failNext {
			return http.ErrAbortHandler
		}
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a := r.Find([]store.Scope{sc})
	add := func(path, key string) error {
		return a.Add(exchange(t, http.MethodGet, path, "", 200, jsonHeader(), `{"a":1}`, "X-Key", key))
	}
	for _, c := range [][2]string{{"/a", "k1"}, {"/a", "k1"}, {"/a", "k2"}, {"/b", "k1"}} {
		if err := add(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 {
		t.Fatalf("handed over %d mappings, want 3: one for each request that differs by path or by a captured header", len(got))
	}
	failNext = true
	if err := add("/c", "k1"); err == nil {
		t.Error("an error from the callback must come back")
	}
	if n := len(a.Exchanges()); n != 5 {
		t.Errorf("Exchanges = %d, want all 5 kept even when writing one failed", n)
	}
}

func TestBuildKeepsLargeBodiesInFiles(t *testing.T) {
	t.Parallel()
	spec := &record.Spec{ExtractBodyCriteria: &record.ExtractBody{TextSizeThreshold: "1 kb", BinarySizeThreshold: "0 kb"}}
	ms, files, err := record.Build(spec, []record.Exchange{
		exchange(t, http.MethodGet, "/big/doc", "", 200, http.Header{"Content-Type": {"application/json"}}, strings.Repeat("x", 2048)),
		exchange(t, http.MethodGet, "/small", "", 200, http.Header{"Content-Type": {"text/plain"}}, "hi"),
		exchange(t, http.MethodGet, "/bin", "", 200, http.Header{"Content-Type": {"image/png"}}, "\x89PNG\x00"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !strings.HasPrefix(files[0].Name, "big_doc-"+ms[0].ID) || !strings.HasSuffix(files[0].Name, ".json") || len(files[0].Body) != 2048 {
		t.Fatalf("files = %+v", files)
	}
	if ms[0].Response.BodyFileName != files[0].Name || ms[1].Response.BodyFileName != "" || ms[2].Response.BodyFileName != files[1].Name {
		t.Errorf("bodyFileName = %q, %q, %q", ms[0].Response.BodyFileName, ms[1].Response.BodyFileName, ms[2].Response.BodyFileName)
	}
}

func TestSpecValidateRefusesWhatWireMockRefuses(t *testing.T) {
	t.Parallel()
	for size, ok := range map[string]bool{"100 kb": true, "1KB": true, "2 Mb": true, "": true, "100 b": false, "100 bytes": false, "kb": false} {
		spec := record.Spec{ExtractBodyCriteria: &record.ExtractBody{TextSizeThreshold: size}}
		if err := spec.Validate(); (err == nil) != ok {
			t.Errorf("size %q: error %v, want ok=%v", size, err, ok)
		}
	}
}

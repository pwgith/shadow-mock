package journal_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func entry(id string, org tenant.ID, sessions ...string) *journal.Entry {
	e := &journal.Entry{ID: id, Tenant: org, Sessions: sessions}
	if len(sessions) > 0 {
		e.SessionID = sessions[0]
	}
	return e
}

func ids(es []*journal.Entry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEntryInScope(t *testing.T) {
	t.Parallel()
	e := entry("1", "acme", "ses_key", "urlmock")
	global := store.Global("acme")
	tests := []struct {
		name  string
		scope store.Scope
		want  bool
	}{
		{"the org's global journal holds everything", global, true},
		{"the most specific session", store.Scope{Tenant: "acme", SessionID: "ses_key"}, true},
		{"a session further down the stack", store.Scope{Tenant: "acme", SessionID: "urlmock"}, true},
		{"another session", store.Scope{Tenant: "acme", SessionID: "ses_other"}, false},
		{"another org's global journal", store.Global("globex"), false},
		{"another org's session of the same id", store.Scope{Tenant: "globex", SessionID: "ses_key"}, false},
	}
	for _, tt := range tests {
		if got := e.InScope(tt.scope); got != tt.want {
			t.Errorf("%s: InScope = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEntryMatched(t *testing.T) {
	t.Parallel()
	if (&journal.Entry{}).Matched() {
		t.Error("an entry without a stub is not matched")
	}
	m := model.StubMapping{}
	if !(&journal.Entry{Stub: &m}).Matched() {
		t.Error("an entry with a stub is matched")
	}
}

func TestMemoryKeepsTheMostRecentEntries(t *testing.T) {
	t.Parallel()
	j := journal.NewMemory(3)
	if !j.Enabled() {
		t.Fatal("a journal with room is enabled")
	}
	for i := 1; i <= 5; i++ {
		j.Append(entry(strconv.Itoa(i), "acme"))
	}
	got := ids(j.Query(store.Global("acme"), nil, 0))
	if want := []string{"5", "4", "3"}; !equal(got, want) { // most recent first, oldest dropped
		t.Errorf("Query = %v, want %v", got, want)
	}
}

func TestMemoryDisabled(t *testing.T) {
	t.Parallel()
	j := journal.NewMemory(0)
	j.Append(entry("1", "acme"))
	if j.Enabled() || len(j.Query(store.Global("acme"), nil, 0)) != 0 || j.Count(store.Global("acme"), nil) != 0 {
		t.Error("a journal of size 0 records nothing")
	}
}

func TestMemoryQuery(t *testing.T) {
	t.Parallel()
	j := journal.NewMemory(100)
	j.Append(entry("a", "acme", "s1"))
	j.Append(entry("b", "acme"))
	j.Append(entry("c", "acme", "s1", "u1"))
	j.Append(entry("d", "globex", "s1"))
	acme, s1 := store.Global("acme"), store.Scope{Tenant: "acme", SessionID: "s1"}

	if got := ids(j.Query(acme, nil, 0)); !equal(got, []string{"c", "b", "a"}) {
		t.Errorf("global = %v", got)
	}
	if got := ids(j.Query(s1, nil, 0)); !equal(got, []string{"c", "a"}) {
		t.Errorf("session s1 = %v", got)
	}
	if got := ids(j.Query(acme, nil, 2)); !equal(got, []string{"c", "b"}) {
		t.Errorf("limit 2 = %v", got)
	}
	only := func(e *journal.Entry) bool { return e.ID == "b" }
	if got := ids(j.Query(acme, only, 0)); !equal(got, []string{"b"}) {
		t.Errorf("filtered = %v", got)
	}
	if j.Count(s1, nil) != 2 || j.Count(acme, only) != 1 {
		t.Errorf("Count = %d, %d", j.Count(s1, nil), j.Count(acme, only))
	}
}

func TestMemoryGetAndRemove(t *testing.T) {
	t.Parallel()
	j := journal.NewMemory(100)
	j.Append(entry("a", "acme", "s1"))
	j.Append(entry("b", "acme", "s2"))
	j.Append(entry("c", "globex", "s1"))
	s1 := store.Scope{Tenant: "acme", SessionID: "s1"}

	if e, ok := j.Get(s1, "a"); !ok || e.ID != "a" {
		t.Errorf("Get(a) = %v, %v", e, ok)
	}
	if _, ok := j.Get(s1, "b"); ok {
		t.Error("Get must not return another session's entry")
	}
	if _, ok := j.Get(s1, "missing"); ok {
		t.Error("Get of an unknown id should fail")
	}

	removed := j.Remove(s1, nil)
	if len(removed) != 1 || removed[0].ID != "a" {
		t.Errorf("Remove(s1) = %v, want only a", ids(removed))
	}
	if got := ids(j.Query(store.Global("acme"), nil, 0)); !equal(got, []string{"b"}) {
		t.Errorf("after removing s1: %v", got)
	}
	// Removing with a predicate takes only the entries that satisfy it.
	if got := j.Remove(store.Global("acme"), func(e *journal.Entry) bool { return e.ID == "zzz" }); len(got) != 0 {
		t.Errorf("Remove with a predicate that matches nothing removed %v", ids(got))
	}
	if got := ids(j.Remove(store.Global("acme"), nil)); !equal(got, []string{"b"}) {
		t.Errorf("Remove(global) = %v", got)
	}
	if _, ok := j.Get(store.Global("globex"), "c"); !ok {
		t.Error("another org's entries must survive")
	}
}

func TestMemoryCutsLongBodies(t *testing.T) {
	t.Parallel()
	j := journal.NewMemory(10)
	j.LimitBodies(8)
	big := []byte("0123456789abcdef")
	e := entry("1", "acme")
	e.Request = &match.Request{Method: "POST", Path: "/in", Body: big}
	e.Response = journal.Response{Status: 200, Body: big}
	j.Append(e)

	got := j.Query(store.Global("acme"), nil, 0)[0]
	if string(got.Request.Body) != "01234567" || string(got.Response.Body) != "01234567" {
		t.Errorf("kept bodies %q and %q, want the first 8 bytes of each", got.Request.Body, got.Response.Body)
	}
	if got.Request.Method != http.MethodPost || got.Request.Path != "/in" {
		t.Errorf("the rest of the request changed: %+v", got.Request)
	}
	if len(e.Request.Body) != len(big) || len(e.Response.Body) != len(big) {
		t.Error("the caller's entry must not be changed")
	}

	short := entry("2", "acme")
	short.Request = &match.Request{Body: []byte("tiny")}
	j.Append(short)
	if string(j.Query(store.Global("acme"), nil, 0)[0].Request.Body) != "tiny" {
		t.Error("a body under the limit is kept whole")
	}
}

func TestMemoryWithoutALimitKeepsBodiesWhole(t *testing.T) {
	t.Parallel()
	j := journal.NewMemory(10)
	e := entry("1", "acme")
	e.Request = &match.Request{Body: []byte("0123456789")}
	j.Append(e)
	if len(j.Query(store.Global("acme"), nil, 0)[0].Request.Body) != 10 {
		t.Error("no limit set, so nothing is cut")
	}
}

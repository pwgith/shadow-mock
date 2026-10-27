package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func mapping(t *testing.T, s string) model.StubMapping {
	t.Helper()
	m, err := model.DecodeStubMapping([]byte(s))
	if err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return m
}

func mask(t *testing.T, s string) model.Mask {
	t.Helper()
	var m model.Mask
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func layer(t *testing.T, sc store.Scope, kind session.Kind, ms []model.StubMapping, masks []model.Mask) *Layer {
	t.Helper()
	snap, errs := BuildSnapshot(sc, kind, ms, masks, match.Options{})
	if len(errs) > 0 {
		t.Fatalf("BuildSnapshot: %v", errs)
	}
	l := &Layer{Scope: sc, Kind: kind}
	l.snap.Store(snap)
	return l
}

func get(t *testing.T, path string) *match.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), "GET", path, http.NoBody)
	return match.NewRequest(r, nil, r.URL.EscapedPath())
}

var (
	globalScope  = store.Global(tenant.Local)
	sessionScope = store.Scope{Tenant: tenant.Local, SessionID: "ses_1"}
)

func served(r Result) string {
	if r.Stub == nil {
		return ""
	}
	return r.Stub.Mapping.Name
}

func TestResolvePriorityWithinLayer(t *testing.T) {
	t.Parallel()
	g := layer(t, globalScope, session.Global, []model.StubMapping{
		mapping(t, `{"id":"g1","name":"low-priority","priority":9,"request":{"urlPath":"/a"},"response":{}}`),
		mapping(t, `{"id":"g2","name":"older","request":{"urlPathPattern":"/a.*"},"response":{}}`),
		mapping(t, `{"id":"g3","name":"newer","request":{"urlPath":"/a"},"response":{}}`),
		mapping(t, `{"id":"g4","name":"high-priority","priority":1,"request":{"urlPathPattern":"/b.*"},"response":{}}`),
		mapping(t, `{"id":"g5","name":"post-only","priority":1,"request":{"method":"POST","urlPath":"/a"},"response":{}}`),
	}, nil)
	tests := map[string]string{"/a": "newer", "/b1": "high-priority", "/zzz": ""}
	for path, want := range tests {
		if got := served(Resolve(get(t, path), Stack{g})); got != want {
			t.Errorf("Resolve(%s) served %q, want %q", path, got, want)
		}
	}
}

func TestResolveLayerBeatsPriority(t *testing.T) {
	t.Parallel()
	g := layer(t, globalScope, session.Global, []model.StubMapping{
		mapping(t, `{"id":"g1","name":"global","priority":1,"request":{"urlPath":"/a"},"response":{}}`),
		mapping(t, `{"id":"g2","name":"catch-all","priority":10,"request":{},"response":{}}`),
	}, nil)
	s := layer(t, sessionScope, session.KeyBased, []model.StubMapping{
		mapping(t, `{"id":"s1","name":"session","priority":10,"request":{"urlPath":"/a"},"response":{}}`),
	}, nil)
	if got := served(Resolve(get(t, "/a"), Stack{s, g})); got != "session" {
		t.Errorf("served %q, want the session stub despite its priority (FR-7.5)", got)
	}
	if got := served(Resolve(get(t, "/other"), Stack{s, g})); got != "catch-all" {
		t.Errorf("unmatched session request served %q, want fall-through to global", got)
	}
	if res := Resolve(get(t, "/a"), Stack{s, g}); res.Layer != s {
		t.Error("Result.Layer should be the session layer")
	}
}

func TestResolveOverrideAndMasks(t *testing.T) {
	t.Parallel()
	global := []model.StubMapping{
		mapping(t, `{"id":"cccccccc-0000-0000-0000-000000000001","name":"rates","priority":1,"request":{"urlPath":"/rates"},"response":{}}`),
		mapping(t, `{"id":"g2","name":"catch-all","priority":10,"request":{},"response":{}}`),
	}
	g := layer(t, globalScope, session.Global, global, nil)
	tests := []struct {
		name     string
		mappings []model.StubMapping
		masks    []model.Mask
		want     string
	}{
		{"no override", nil, nil, "rates"},
		{"override by id hides even when not matching", []model.StubMapping{
			mapping(t, `{"id":"cccccccc-0000-0000-0000-000000000001","name":"override","request":{"urlPath":"/rates","headers":{"X":{"equalTo":"y"}}},"response":{}}`),
		}, nil, "catch-all"},
		{"mask by id", nil, []model.Mask{mask(t, `{"id":"cccccccc-0000-0000-0000-000000000001"}`)}, "catch-all"},
		{"mask by pattern hides every parent stub", nil, []model.Mask{mask(t, `{"request":{"urlPathPattern":"/rates.*"}}`)}, ""},
		{"mask by pattern not matching", nil, []model.Mask{mask(t, `{"request":{"urlPath":"/other"}}`)}, "rates"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := layer(t, sessionScope, session.KeyBased, tt.mappings, tt.masks)
			if got := served(Resolve(get(t, "/rates"), Stack{s, g})); got != tt.want {
				t.Errorf("served %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSnapshotSkipsInvalid(t *testing.T) {
	t.Parallel()
	ms := []model.StubMapping{
		mapping(t, `{"id":"ok","request":{"urlPath":"/a"},"response":{}}`),
		mapping(t, `{"id":"bad","request":{"urlPattern":"("},"response":{}}`),
	}
	snap, errs := BuildSnapshot(globalScope, session.Global, ms, []model.Mask{mask(t, `{"request":{"urlPattern":"("}}`)}, match.Options{})
	if len(errs) != 2 {
		t.Errorf("BuildSnapshot errors = %v, want 2", errs)
	}
	if len(snap.Stubs()) != 1 {
		t.Errorf("Stubs() = %d, want 1", len(snap.Stubs()))
	}
	if _, ok := snap.Stub("ok"); !ok {
		t.Error("Stub(ok) missing")
	}
}

func TestNearMisses(t *testing.T) {
	t.Parallel()
	g := layer(t, globalScope, session.Global, []model.StubMapping{
		mapping(t, `{"id":"g1","name":"close","request":{"method":"GET","urlPath":"/payments/1"},"response":{}}`),
		mapping(t, `{"id":"g2","name":"far","request":{"method":"POST","urlPath":"/x"},"response":{}}`),
		mapping(t, `{"id":"g3","name":"hidden","request":{"urlPath":"/payments/2"},"response":{}}`),
	}, nil)
	s := layer(t, sessionScope, session.KeyBased, nil, []model.Mask{mask(t, `{"id":"g3"}`)})
	nm := NearMisses(get(t, "/payments/2"), Stack{s, g}, 5)
	if len(nm) != 2 || nm[0].Stub.Mapping.Name != "close" || nm[1].Stub.Mapping.Name != "far" {
		names := make([]string, 0, len(nm))
		for _, n := range nm {
			names = append(names, n.Stub.Mapping.Name)
		}
		t.Fatalf("NearMisses = %v, want [close far]", names)
	}
	if nm[0].Distance >= nm[1].Distance {
		t.Errorf("distances %v, %v not ascending", nm[0].Distance, nm[1].Distance)
	}
	if got := NearMisses(get(t, "/payments/2"), Stack{s, g}, 1); len(got) != 1 {
		t.Errorf("limit 1 returned %d", len(got))
	}
}

func TestManager(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := memory.NewMappings()
	reg := session.NewRegistry(clock.NewFake(time.Unix(0, 0)), session.DefaultLimits())
	m := NewManager(repo, reg, match.Options{}, slog.New(slog.DiscardHandler))
	if err := repo.Put(ctx, globalScope, mapping(t, `{"id":"g","name":"global","request":{"urlPath":"/a"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.Rebuild(ctx, globalScope); err != nil {
		t.Fatal(err)
	}
	sess, err := reg.Create(ctx, tenant.Local, session.CreateRequest{Kind: session.KeyBased})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, sess.Scope(), mapping(t, `{"id":"s","name":"session","request":{"urlPath":"/a"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Layer(sess.Scope()); ok {
		t.Error("session layer exists before Rebuild")
	}
	if err := m.Rebuild(ctx, sess.Scope()); err != nil {
		t.Fatal(err)
	}
	st := m.Stack(tenant.Local, sess.ID, "", "ses_unknown")
	if len(st) != 2 || st.RequestScope() != sess.Scope() {
		t.Fatalf("Stack = %d layers, request scope %v", len(st), st.RequestScope())
	}
	if got := served(Resolve(get(t, "/a"), st)); got != "session" {
		t.Errorf("served %q, want session", got)
	}
	if (Stack{}).RequestScope() != (store.Scope{}) {
		t.Error("empty stack request scope")
	}
	// A session that no longer exists is dropped on rebuild.
	if _, err := reg.Remove(ctx, tenant.Local, sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Rebuild(ctx, sess.Scope()); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Layer(sess.Scope()); ok {
		t.Error("layer of a removed session survived Rebuild")
	}
	if m.Options().Now != nil {
		t.Error("Options() should return what the manager was built with")
	}
}

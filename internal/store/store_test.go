package store_test

import (
	"errors"
	"testing"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func mapping(t *testing.T, id string) model.StubMapping {
	t.Helper()
	m, err := model.DecodeStubMapping([]byte(`{"id":"` + id + `","request":{"urlPath":"/` + id + `"},"response":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestScope(t *testing.T) {
	t.Parallel()
	g := store.Global("acme")
	if !g.IsGlobal() || g.String() != "acme/global" {
		t.Errorf("global scope: %v %q", g.IsGlobal(), g.String())
	}
	s := store.Scope{Tenant: "acme", SessionID: "ses_1"}
	if s.IsGlobal() || s.String() != "acme/ses_1" {
		t.Errorf("session scope: %v %q", s.IsGlobal(), s.String())
	}
}

func TestSplitMappingsRoutesByScope(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	global, sessions := memory.NewMappings(), memory.NewMappings()
	r := store.SplitMappings{Global: global, Sessions: sessions}
	g, s := store.Global(tenant.Local), store.Scope{Tenant: tenant.Local, SessionID: "ses_1"}

	if err := r.Put(ctx, g, mapping(t, "g1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, s, mapping(t, "s1")); err != nil {
		t.Fatal(err)
	}
	if ms, _ := global.List(ctx, g); len(ms) != 1 || ms[0].ID != "g1" {
		t.Errorf("the global repository holds %v", ms)
	}
	if ms, _ := sessions.List(ctx, s); len(ms) != 1 || ms[0].ID != "s1" {
		t.Errorf("the session repository holds %v", ms)
	}
	if m, err := r.Get(ctx, s, "s1"); err != nil || m.ID != "s1" {
		t.Errorf("Get(s1) = %v, %v", m, err)
	}
	if _, err := r.Get(ctx, g, "s1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of a session mapping in the global scope = %v, want ErrNotFound", err)
	}
	if ms, err := r.List(ctx, g); err != nil || len(ms) != 1 {
		t.Errorf("List(global) = %v, %v", ms, err)
	}
	if err := r.Delete(ctx, s, "s1"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := r.DeleteAll(ctx, g); err != nil {
		t.Errorf("DeleteAll: %v", err)
	}
	if ms, _ := r.List(ctx, g); len(ms) != 0 {
		t.Errorf("after DeleteAll: %v", ms)
	}
}

func TestSplitMappingsResetAndSaveFallBack(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r := store.SplitMappings{Global: memory.NewMappings(), Sessions: memory.NewMappings()}
	g := store.Global(tenant.Local)
	if err := r.Put(ctx, g, mapping(t, "g1")); err != nil {
		t.Fatal(err)
	}
	// The memory repository can neither reset nor save, so Reset empties the scope and Save does nothing.
	if err := r.Save(ctx, g); err != nil {
		t.Errorf("Save = %v", err)
	}
	if err := r.Reset(ctx, g); err != nil {
		t.Errorf("Reset = %v", err)
	}
	if ms, _ := r.List(ctx, g); len(ms) != 0 {
		t.Errorf("after Reset: %v", ms)
	}
}

func TestSplitFilesRoutesByScope(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	global, sessions := memory.NewFiles(), memory.NewFiles()
	r := store.SplitFiles{Global: global, Sessions: sessions}
	g, s := store.Global(tenant.Local), store.Scope{Tenant: tenant.Local, SessionID: "ses_1"}

	if err := r.Put(ctx, g, "a.txt", []byte("global")); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, s, "a.txt", []byte("session")); err != nil {
		t.Fatal(err)
	}
	for scope, want := range map[store.Scope]string{g: "global", s: "session"} {
		if b, err := r.Read(ctx, scope, "a.txt"); err != nil || string(b) != want {
			t.Errorf("Read(%v) = %q, %v, want %q", scope, b, err, want)
		}
	}
	if names, _ := r.List(ctx, s); len(names) != 1 || names[0] != "a.txt" {
		t.Errorf("List = %v", names)
	}
	if err := r.Delete(ctx, s, "a.txt"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if _, err := r.Read(ctx, s, "a.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Read after Delete = %v, want ErrNotFound", err)
	}
	if err := r.DeleteAll(ctx, g); err != nil {
		t.Errorf("DeleteAll: %v", err)
	}
	if _, err := r.Read(ctx, g, "a.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Read after DeleteAll = %v, want ErrNotFound", err)
	}
}

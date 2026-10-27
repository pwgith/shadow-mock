package memory_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

var scope = store.Global(tenant.Local)

func mapping(t *testing.T, id, body string) model.StubMapping {
	t.Helper()
	m, err := model.DecodeStubMapping([]byte(`{"id":"` + id + `","request":{"urlPath":"/x"},"response":{"body":"` + body + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func order(ms []model.StubMapping) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestMappings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r := memory.NewMappings()

	if ms, err := r.List(ctx, scope); err != nil || len(ms) != 0 {
		t.Fatalf("List of an empty scope = %v, %v", ms, err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := r.Put(ctx, scope, mapping(t, id, "1")); err != nil {
			t.Fatal(err)
		}
	}
	// Replacing keeps the mapping's place in the insertion order.
	if err := r.Put(ctx, scope, mapping(t, "a", "2")); err != nil {
		t.Fatal(err)
	}
	ms, _ := r.List(ctx, scope)
	if diff := cmp.Diff([]string{"a", "b", "c"}, order(ms)); diff != "" {
		t.Errorf("order (-want +got):\n%s", diff)
	}
	if m, err := r.Get(ctx, scope, "a"); err != nil || *m.Response.Body != "2" {
		t.Errorf("Get(a) = %v, %v, want the replacement", m, err)
	}

	if err := r.Delete(ctx, scope, "b"); err != nil {
		t.Fatal(err)
	}
	ms, _ = r.List(ctx, scope)
	if diff := cmp.Diff([]string{"a", "c"}, order(ms)); diff != "" {
		t.Errorf("after Delete (-want +got):\n%s", diff)
	}
	if err := r.Delete(ctx, scope, "b"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete of a deleted mapping = %v, want ErrNotFound", err)
	}
	if err := r.Delete(ctx, store.Scope{Tenant: "other"}, "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete in an empty scope = %v, want ErrNotFound", err)
	}
	if _, err := r.Get(ctx, scope, "zzz"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of an unknown id = %v", err)
	}
	if _, err := r.Get(ctx, store.Scope{Tenant: "other"}, "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get in another scope = %v", err)
	}
	if err := r.DeleteAll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if ms, _ := r.List(ctx, scope); len(ms) != 0 {
		t.Errorf("after DeleteAll: %v", order(ms))
	}
}

func TestMappingScopesAreIsolated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r := memory.NewMappings()
	a, b := store.Scope{Tenant: "acme", SessionID: "s1"}, store.Scope{Tenant: "acme", SessionID: "s2"}
	if err := r.Put(ctx, a, mapping(t, "x", "a")); err != nil {
		t.Fatal(err)
	}
	if ms, _ := r.List(ctx, b); len(ms) != 0 {
		t.Errorf("scope b sees %v", order(ms))
	}
}

func TestFiles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r := memory.NewFiles()
	if err := r.Put(ctx, scope, "/b.txt", []byte("bee")); err != nil { // a leading slash is the same file
		t.Fatal(err)
	}
	if err := r.Put(ctx, scope, "a/a.txt", []byte("aye")); err != nil {
		t.Fatal(err)
	}
	if names, _ := r.List(ctx, scope); !cmp.Equal(names, []string{"a/a.txt", "b.txt"}) {
		t.Errorf("List = %v, want sorted names", names)
	}
	b, err := r.Read(ctx, scope, "b.txt")
	if err != nil || string(b) != "bee" {
		t.Fatalf("Read = %q, %v", b, err)
	}
	// Callers cannot change stored data through the slices they pass or get back.
	b[0] = 'X'
	if again, _ := r.Read(ctx, scope, "b.txt"); string(again) != "bee" {
		t.Errorf("the stored file changed to %q", again)
	}
	if err := r.Delete(ctx, scope, "b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, scope, "b.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
	if _, err := r.Read(ctx, scope, "b.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Read after Delete = %v", err)
	}
	if err := r.DeleteAll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if names, _ := r.List(ctx, scope); len(names) != 0 {
		t.Errorf("after DeleteAll: %v", names)
	}
}

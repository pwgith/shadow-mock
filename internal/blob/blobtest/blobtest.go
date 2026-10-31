// Package blobtest is the suite every blob.Store driver must pass. A new driver calls Run from its own tests.
package blobtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/pwgith/shadow-mock/internal/blob"
)

// Run runs the suite against stores made by newStore, which must give an empty store each time it is called.
func Run(t *testing.T, newStore func(t *testing.T) blob.Store) {
	t.Helper()
	for name, fn := range map[string]func(*testing.T, blob.Store){
		"round trip":                     roundTrip,
		"put replaces":                   putReplaces,
		"empty object":                   emptyObject,
		"large object":                   largeObject,
		"range reads":                    rangeReads,
		"missing key":                    missingKey,
		"stat":                           stat,
		"delete is repeatable":           deleteRepeats,
		"list by prefix":                 listByPrefix,
		"keys are checked":               keysAreChecked,
		"concurrent puts of one key":     concurrentPuts,
		"keys do not leak between orgs":  orgsDoNotLeak,
		"a cancelled context is refused": cancelled,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fn(t, newStore(t))
		})
	}
}

func roundTrip(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	if err := s.Put(ctx, "files/acme/abc", []byte("hello"), blob.Meta{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	got, err := blob.ReadAll(ctx, s, "files/acme/abc", nil)
	if err != nil || string(got) != "hello" {
		t.Errorf("got %q, %v", got, err)
	}
}

func putReplaces(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	check(t, s.Put(ctx, "k/a", []byte("one"), blob.Meta{}))
	if err := s.Put(ctx, "k/a", []byte("two!"), blob.Meta{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, s, "k/a"); string(got) != "two!" {
		t.Errorf("got %q", got)
	}
}

func emptyObject(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	if err := s.Put(ctx, "k/empty", nil, blob.Meta{}); err != nil {
		t.Fatal(err)
	}
	got, err := blob.ReadAll(ctx, s, "k/empty", nil)
	if err != nil || len(got) != 0 {
		t.Errorf("got %q, %v", got, err)
	}
	if info, err := s.Stat(ctx, "k/empty"); err != nil || info.Size != 0 {
		t.Errorf("stat = %+v, %v", info, err)
	}
}

func largeObject(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	data := make([]byte, 3<<20+17)
	_, _ = rand.Read(data)
	if err := s.Put(ctx, "k/large", data, blob.Meta{}); err != nil {
		t.Fatal(err)
	}
	got, err := blob.ReadAll(ctx, s, "k/large", nil)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("a 3 MiB object did not come back whole: %d bytes, %v", len(got), err)
	}
}

func rangeReads(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	check(t, s.Put(ctx, "k/r", []byte("0123456789"), blob.Meta{}))
	tests := []struct {
		name string
		rng  blob.Range
		want string
	}{
		{"from the start", blob.Range{Off: 0, Len: 4}, "0123"},
		{"in the middle", blob.Range{Off: 3, Len: 3}, "345"},
		{"to the end", blob.Range{Off: 7}, "789"},
		{"past the end is cut", blob.Range{Off: 8, Len: 100}, "89"},
	}
	for _, tt := range tests {
		got, err := blob.ReadAll(ctx, s, "k/r", &tt.rng)
		if err != nil || string(got) != tt.want {
			t.Errorf("%s: got %q, %v, want %q", tt.name, got, err, tt.want)
		}
	}
}

func missingKey(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Get(ctx, "k/none", nil); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("get: %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(ctx, "k/none"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("stat: %v, want ErrNotFound", err)
	}
}

func stat(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	check(t, s.Put(ctx, "k/s", []byte("12345"), blob.Meta{ContentType: "application/json"}))
	info, err := s.Stat(ctx, "k/s")
	if err != nil || info.Size != 5 {
		t.Fatalf("stat = %+v, %v", info, err)
	}
	if info.ContentType != "" && info.ContentType != "application/json" {
		t.Errorf("content type = %q", info.ContentType)
	}
}

func deleteRepeats(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	check(t, s.Put(ctx, "k/d", []byte("x"), blob.Meta{}))
	for range 2 {
		if err := s.Delete(ctx, "k/d"); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	if _, err := s.Stat(ctx, "k/d"); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.Delete(ctx, "k/never-existed"); err != nil {
		t.Errorf("deleting a key that holds nothing: %v", err)
	}
}

func listByPrefix(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	for _, k := range []string{"files/acme/b", "files/acme/a", "files/acmeco/a", "journal/acme/x"} {
		check(t, s.Put(ctx, k, []byte("."), blob.Meta{}))
	}
	got, err := s.List(ctx, "files/acme/")
	if err != nil || !slices.Equal(got, []string{"files/acme/a", "files/acme/b"}) {
		t.Errorf("list files/acme/ = %v, %v", got, err)
	}
	all, err := s.List(ctx, "")
	if err != nil || len(all) != 4 {
		t.Errorf("list of everything = %v", all)
	}
	none, err := s.List(ctx, "nothing/here/")
	if err != nil || len(none) != 0 {
		t.Errorf("list of nothing = %v, %v", none, err)
	}
}

func keysAreChecked(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	for _, k := range []string{"", "/abs", "a//b", "../x", "a/../b", "a/.hidden", "a b", "a/b/", "x\x00y"} {
		if err := s.Put(ctx, k, []byte("x"), blob.Meta{}); !errors.Is(err, blob.ErrBadKey) {
			t.Errorf("put %q: %v, want ErrBadKey", k, err)
		}
		if _, err := s.Get(ctx, k, nil); !errors.Is(err, blob.ErrBadKey) {
			t.Errorf("get %q: %v, want ErrBadKey", k, err)
		}
	}
}

func concurrentPuts(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			if err := s.Put(ctx, "k/race", []byte(fmt.Sprintf("writer-%02d", i)), blob.Meta{}); err != nil {
				t.Errorf("put: %v", err)
			}
		})
	}
	wg.Wait()
	got, err := blob.ReadAll(ctx, s, "k/race", nil)
	if err != nil || len(got) != len("writer-00") || !bytes.HasPrefix(got, []byte("writer-")) {
		t.Errorf("the object must be one writer's whole body, got %q, %v", got, err)
	}
}

func orgsDoNotLeak(t *testing.T, s blob.Store) {
	t.Helper()
	ctx := t.Context()
	check(t, s.Put(ctx, "files/acme/h", []byte("acme's"), blob.Meta{}))
	if _, err := s.Get(ctx, "files/globex/h", nil); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("another org's key: %v", err)
	}
}

func cancelled(t *testing.T, s blob.Store) {
	t.Helper()
	if err := s.Put(t.Context(), "k/c", []byte("x"), blob.Meta{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// A store that talks to a network must honour the context; a local one may finish the work. Either is fine,
	// but a cancelled call must never report success with wrong data.
	if got, err := blob.ReadAll(ctx, s, "k/c", nil); err == nil && string(got) != "x" {
		t.Errorf("got %q", got)
	}
	if err := s.Put(ctx, "k/c", []byte("y"), blob.Meta{}); err != nil {
		if got := read(t, s, "k/c"); string(got) != "x" {
			t.Errorf("a refused put must leave the object alone, got %q", got)
		}
	}
}

// check fails the test if err is not nil.
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// read returns the whole object.
func read(t *testing.T, s blob.Store, key string) []byte {
	t.Helper()
	b, err := blob.ReadAll(t.Context(), s, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

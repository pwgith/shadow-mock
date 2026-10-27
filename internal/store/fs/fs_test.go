package fs_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/fs"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

var scope = store.Global(tenant.Local)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mapping(t *testing.T, json string) model.StubMapping {
	t.Helper()
	m, err := model.DecodeStubMapping([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ids(ms []model.StubMapping) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

// newMappings opens a mappings directory and returns it with the log it writes to.
func newMappings(t *testing.T, dir string) (*fs.Mappings, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	r, err := fs.NewMappings(dir, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, &logs
}

func TestLoadReadsEveryMappingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "one.json"), `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/one"},"response":{}}`)
	write(t, filepath.Join(dir, "nested", "deep", "many.json"), `{"mappings":[
		{"id":"22222222-2222-4222-8222-222222222222","request":{"urlPath":"/two"},"response":{}},
		{"request":{"urlPath":"/three"},"response":{}}]}`)
	write(t, filepath.Join(dir, "notes.txt"), "not a mapping")
	r, logs := newMappings(t, dir)
	if err := r.Load(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	ms, _ := r.List(t.Context(), scope)
	if len(ms) != 3 {
		t.Fatalf("loaded %d mappings, want 3: %v", len(ms), ids(ms))
	}
	// A mapping without an id gets a stable one, so a session can override it by id.
	third := ms[2].ID
	if err := r.Load(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.List(t.Context(), scope); again[2].ID != third || third == "" {
		t.Errorf("generated id changed from %q to %q across reloads", third, again[2].ID)
	}
	if logs.Len() != 0 {
		t.Errorf("unexpected log output: %s", logs)
	}
}

func TestLoadSkipsAndReportsBadFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ok.json"), `{"request":{"urlPath":"/ok"},"response":{}}`)
	write(t, filepath.Join(dir, "broken.json"), `{"request": {"urlPath": "/broken"`)
	write(t, filepath.Join(dir, "scenario.json"), `{"scenarioName":"flow","request":{"urlPath":"/s"},"response":{}}`)
	r, logs := newMappings(t, dir)
	if err := r.Load(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if ms, _ := r.List(t.Context(), scope); len(ms) != 1 {
		t.Errorf("loaded %d mappings, want only the valid one", len(ms))
	}
	for _, want := range []string{"mappings/broken.json", "mappings/scenario.json", "scenarios_not_supported", "level=ERROR"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, logs)
		}
	}
}

func TestPersistentMappingsAreWrittenImmediately(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, _ := newMappings(t, dir)
	ctx := t.Context()

	if err := r.Put(ctx, scope, mapping(t, `{"id":"aaaaaaaa-0000-4000-8000-000000000001","request":{"urlPath":"/mem"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "aaaaaaaa-0000-4000-8000-000000000001.json")) {
		t.Error("a mapping that is not persistent must stay in memory")
	}
	if err := r.Put(ctx, scope, mapping(t, `{"id":"bbbbbbbb-0000-4000-8000-000000000001","persistent":true,"request":{"urlPath":"/disk"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "bbbbbbbb-0000-4000-8000-000000000001.json")) {
		t.Error("a persistent mapping must be written at once")
	}
}

func TestSaveWritesAPICreatedMappings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "from-file.json"), `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/f"},"response":{}}`)
	r, _ := newMappings(t, dir)
	ctx := t.Context()
	if err := r.Load(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, scope, mapping(t, `{"id":"cccccccc-0000-4000-8000-000000000001","request":{"urlPath":"/api"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(ctx, scope); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "cccccccc-0000-4000-8000-000000000001.json"))
	if err != nil || !strings.Contains(string(saved), "/api") {
		t.Errorf("saved file = %q, %v", saved, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("the directory holds %d entries, want the original and the saved mapping", len(entries))
	}
}

func TestResetDropsAPIMappingsAndReloadsFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.json"), `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/a"},"response":{}}`)
	r, _ := newMappings(t, dir)
	ctx := t.Context()
	if err := r.Load(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, scope, mapping(t, `{"id":"dddddddd-0000-4000-8000-000000000001","request":{"urlPath":"/api"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteAll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if ms, _ := r.List(ctx, scope); len(ms) != 0 {
		t.Fatalf("DeleteAll left %v", ids(ms))
	}
	if err := r.Reset(ctx, scope); err != nil {
		t.Fatal(err)
	}
	ms, _ := r.List(ctx, scope)
	if diff := cmp.Diff([]string{"11111111-1111-4111-8111-111111111111"}, ids(ms)); diff != "" {
		t.Errorf("after Reset (-want +got):\n%s", diff)
	}
}

func TestDeleteRemovesTheFileOfASingleMappingOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "single.json"), `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/s"},"response":{}}`)
	write(t, filepath.Join(dir, "many.json"), `{"mappings":[
		{"id":"22222222-2222-4222-8222-222222222222","request":{"urlPath":"/m1"},"response":{}},
		{"id":"33333333-3333-4333-8333-333333333333","request":{"urlPath":"/m2"},"response":{}}]}`)
	r, _ := newMappings(t, dir)
	ctx := t.Context()
	if err := r.Load(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, scope, "11111111-1111-4111-8111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "single.json")) {
		t.Error("deleting a mapping that has a file of its own removes the file")
	}
	if err := r.Delete(ctx, scope, "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "many.json")) {
		t.Error("a file shared by several mappings must be kept")
	}
	if err := r.Delete(ctx, scope, "99999999-9999-4999-8999-999999999999"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete of an unknown id = %v, want ErrNotFound", err)
	}
}

func TestReplacingAFileMappingRewritesItsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "claims", "get.json"), `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/old"},"response":{}}`)
	r, _ := newMappings(t, dir)
	ctx := t.Context()
	if err := r.Load(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, scope, mapping(t, `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/new"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "claims", "get.json"))
	if err != nil || !strings.Contains(string(b), "/new") {
		t.Errorf("the mapping's own file = %q, %v; want it rewritten in place", b, err)
	}
}

func TestWatchReloadsAfterAChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, _ := newMappings(t, dir)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	changes, err := r.Watch(ctx, dir, scope)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "new.json"), `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/new"},"response":{}}`)
	select {
	case ev := <-changes:
		if ev.Kind != "mapping" || ev.Scope != scope {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no change event after a file was written")
	}
	if ms, _ := r.List(ctx, scope); len(ms) != 1 {
		t.Errorf("the new file was not loaded: %v", ids(ms))
	}
	cancel()
	for range changes { // the channel closes once the watcher stops
	}
}

func TestFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := fs.NewFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := t.Context()
	if err := f.Put(ctx, scope, "/sub/dir/a.json", []byte(`{"a":1}`)); err != nil { // creates folders; a leading slash is ignored
		t.Fatal(err)
	}
	if err := f.Put(ctx, scope, "b.txt", []byte("bee")); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Read(ctx, scope, "sub/dir/a.json"); err != nil || string(got) != `{"a":1}` {
		t.Errorf("Read = %q, %v", got, err)
	}
	names, _ := f.List(ctx, scope)
	if diff := cmp.Diff([]string{"b.txt", "sub/dir/a.json"}, names); diff != "" {
		t.Errorf("List (-want +got):\n%s", diff)
	}
	if _, err := f.Read(ctx, scope, "missing.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Read of a missing file = %v, want ErrNotFound", err)
	}
	if err := f.Delete(ctx, scope, "missing.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete of a missing file = %v, want ErrNotFound", err)
	}
	if err := f.Delete(ctx, scope, "b.txt"); err != nil {
		t.Errorf("Delete = %v", err)
	}
	// The global files directory is never emptied by a session being deleted.
	if err := f.DeleteAll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if names, _ := f.List(ctx, scope); len(names) != 1 {
		t.Errorf("DeleteAll must leave the global files alone, got %v", names)
	}
}

func TestFilesCannotEscapeTheirDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "__files")
	secret := filepath.Join(root, "secret.txt")
	write(t, secret, "do not serve")
	f, err := fs.NewFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := t.Context()
	for _, name := range []string{"../secret.txt", "a/../../secret.txt", "sub/../../secret.txt"} {
		if b, err := f.Read(ctx, scope, name); err == nil || strings.Contains(string(b), "do not serve") {
			t.Errorf("Read(%q) = %q, %v; want it refused", name, b, err)
		}
		if err := f.Put(ctx, scope, name, []byte("overwritten")); err == nil {
			if b, _ := os.ReadFile(secret); string(b) != "do not serve" {
				t.Errorf("Put(%q) wrote outside the directory", name)
			}
		}
		if err := f.Delete(ctx, scope, name); err == nil && !exists(secret) {
			t.Errorf("Delete(%q) removed a file outside the directory", name)
		}
	}
}

func TestLoadMappingSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "single.json"), `{"mappings":[{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/a"},"response":{}}]}`)
	write(t, filepath.Join(dir, "folder", "one.json"), `{"id":"22222222-2222-4222-8222-222222222222","request":{"urlPath":"/b"},"response":{}}`)
	write(t, filepath.Join(dir, "folder", "sub", "two.json"), `{"id":"33333333-3333-4333-8333-333333333333","request":{"urlPath":"/c"},"response":{}}`)
	write(t, filepath.Join(dir, "bad", "x.json"), `{`)
	write(t, filepath.Join(dir, "empty", "readme.txt"), "no mappings here")

	if ms, err := fs.LoadMappingSet(dir, "single"); err != nil || len(ms) != 1 {
		t.Errorf("a set in one file = %v, %v", ids(ms), err)
	}
	ms, err := fs.LoadMappingSet(dir, "folder")
	if err != nil || len(ms) != 2 {
		t.Errorf("a set in a folder = %v, %v", ids(ms), err)
	}
	for _, name := range []string{"missing", "empty", "../outside", ".", "", "/abs"} {
		if _, err := fs.LoadMappingSet(dir, name); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("LoadMappingSet(%q) = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := fs.LoadMappingSet(dir, "bad"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("a set with an invalid file = %v, want a parse error", err)
	}
	if _, err := fs.LoadMappingSet(filepath.Join(dir, "nope"), "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a missing directory = %v, want ErrNotFound", err)
	}
}

func TestSessionStoreRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := t.Context()
	open := func() *fs.SessionStore {
		s, err := fs.NewSessionStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		return s
	}
	created := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	sess := session.Session{
		ID: "ses_AAAAAAAAAAAAAAAAAAAAAAAAAA", Kind: session.KeyBased, Tenant: tenant.Local, Key: "smk_KEY", Tokens: []string{"CUST-1"},
		Name: "n", CreatedBy: "local", CreatedAt: created, ExpiresAt: created.Add(time.Hour), TTL: time.Hour,
		Masks: []model.Mask{{ID: "m1"}}, Metadata: map[string]any{"a": "b"},
	}
	first := open()
	if got, err := first.LoadSessions(ctx); err != nil || got != nil {
		t.Fatalf("an empty store loads %v, %v", got, err)
	}
	if err := first.SaveSessions([]session.Session{sess}); err != nil {
		t.Fatal(err)
	}
	sc := sess.Scope()
	if err := first.Put(ctx, sc, mapping(t, `{"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/p"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := first.Put(ctx, sc, mapping(t, `{"id":"22222222-2222-4222-8222-222222222222","request":{"urlPath":"/q"},"response":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := first.Delete(ctx, sc, "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}

	// A new store over the same directory, as after a restart.
	second := open()
	got, err := second.LoadSessions(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("LoadSessions = %v, %v", got, err)
	}
	if diff := cmp.Diff(sess, got[0], cmp.AllowUnexported(session.Session{})); diff != "" {
		t.Errorf("session (-want +got):\n%s", diff)
	}
	ms, _ := second.List(ctx, sc)
	if diff := cmp.Diff([]string{"11111111-1111-4111-8111-111111111111"}, ids(ms)); diff != "" {
		t.Errorf("mappings (-want +got):\n%s", diff)
	}
	if m, err := second.Get(ctx, sc, "11111111-1111-4111-8111-111111111111"); err != nil || m.ID == "" {
		t.Errorf("Get = %v, %v", m, err)
	}

	if err := second.DeleteAll(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, sess.ID)) {
		t.Error("deleting every mapping removes the session's folder")
	}
	if ms, _ := second.List(ctx, sc); len(ms) != 0 {
		t.Errorf("after DeleteAll: %v", ids(ms))
	}
}

func TestSessionStoreRejectsCorruptFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions.json"), `[{"id":"ses_X","kind":"key-based","tenant":"local"}]`)
	write(t, filepath.Join(dir, "ses_X", "mappings.json"), `not json`)
	s, err := fs.NewSessionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.LoadSessions(t.Context()); err == nil {
		t.Error("a corrupt mappings file should fail the load")
	}
	write(t, filepath.Join(dir, "sessions.json"), `{`)
	if _, err := s.LoadSessions(t.Context()); err == nil {
		t.Error("a corrupt sessions file should fail the load")
	}
}

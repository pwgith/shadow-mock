package postgres_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/blobfiles"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/postgres"
	"github.com/pwgith/shadow-mock/internal/store/postgres/pgtest"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// raw runs SQL the way the management app would: straight at the tables.
func raw(t *testing.T, db *pgtest.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// newOrg inserts an org with a handle of its own and removes it, and everything of it, when the test ends.
func newOrg(t *testing.T, db *pgtest.DB, status string) (id tenant.ID, handle string) {
	t.Helper()
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	handle = "t" + hex.EncodeToString(b)
	id = tenant.ID("org_" + handle)
	raw(t, db, "INSERT INTO engine.orgs (id, handle, status) VALUES ($1, $2, $3)", string(id), handle, status)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		for _, table := range []string{"files", "mappings", "api_keys", "org_aliases", "audit_log"} {
			_ = db.Exec(ctx, "DELETE FROM engine."+table+" WHERE org_id = $1", string(id))
		}
		_ = db.Exec(ctx, "DELETE FROM engine.orgs WHERE id = $1", string(id))
	})
	return id, handle
}

func TestMigrateIsRepeatable(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	for range 2 {
		if err := db.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow(t.Context(), "SELECT max(version) FROM engine.schema_migrations").Scan(&n); err != nil || n != postgres.SchemaVersion() {
		t.Errorf("applied version = %d, %v, want %d", n, err, postgres.SchemaVersion())
	}
}

func TestOrgsAreReadWithTheirAliasesLimitsAndStatus(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	active, handle := newOrg(t, db, "active")
	raw(t, db, `UPDATE engine.orgs SET allowlist = '{10.0.0.0/8,192.168.1.0/24}', allowlist_admin = true,
		requests_per_second = 25, burst = 50, monthly_request_cap = 1000, grace = 100 WHERE id = $1`, string(active))
	raw(t, db, "INSERT INTO engine.org_aliases (alias, org_id) VALUES ($1, $2)", handle+"-old", string(active))
	suspended, sHandle := newOrg(t, db, "suspended")
	deleted, dHandle := newOrg(t, db, "deleted")
	raw(t, db, "INSERT INTO engine.org_aliases (alias, org_id) VALUES ($1, $2)", dHandle+"-old", string(deleted))

	var skipped []string
	orgs := db.Orgs(func(h string, _ error) { skipped = append(skipped, h) })
	if _, ok := orgs.Lookup(handle); ok {
		t.Error("the directory must be empty until Refresh")
	}
	if err := orgs.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	o, ok := orgs.Lookup(handle)
	if !ok || o.ID != active || o.Status != tenant.StatusActive || !o.AllowlistAdmin {
		t.Fatalf("org = %+v, %v", o, ok)
	}
	if len(o.Allowlist) != 2 || o.Allowlist[0].String() != "10.0.0.0/8" {
		t.Errorf("allowlist = %v", o.Allowlist)
	}
	if o.Limits != (tenant.Limits{RequestsPerSecond: 25, Burst: 50, MonthlyRequestCap: 1000, Grace: 100}) {
		t.Errorf("limits = %+v", o.Limits)
	}
	if byAlias, found := orgs.Lookup(handle + "-old"); !found || byAlias.ID != active {
		t.Errorf("alias lookup = %+v, %v", byAlias, found)
	}
	if s, found := orgs.Lookup(sHandle); !found || s.ID != suspended || s.Status != tenant.StatusSuspended {
		t.Errorf("suspended org = %+v, %v", s, found)
	}
	d, ok := orgs.Lookup(dHandle)
	if !ok || d.Status != tenant.StatusDeleted {
		t.Fatalf("deleted org = %+v, %v: a deleted org stays as a tombstone so its handle stays taken", d, ok)
	}
	if da, ok := orgs.Lookup(dHandle + "-old"); !ok || da.Status != tenant.StatusDeleted {
		t.Errorf("a deleted org's alias must resolve to the tombstone: %+v, %v", da, ok)
	}
	if byID, ok := orgs.ByID(active); !ok || byID.Handle != handle {
		t.Errorf("ByID = %+v, %v", byID, ok)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skips: %v", skipped)
	}
}

func TestAnOrgWithAReservedHandleIsSkippedNotFatal(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	good, goodHandle := newOrg(t, db, "active")
	bad, _ := newOrg(t, db, "active")
	raw(t, db, "UPDATE engine.orgs SET handle = 'admin' WHERE id = $1", string(bad))
	var skipped []string
	orgs := db.Orgs(func(h string, _ error) { skipped = append(skipped, h) })
	if err := orgs.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := orgs.Lookup(goodHandle); !ok {
		t.Errorf("org %s should load beside the bad one", good)
	}
	if _, ok := orgs.Lookup("admin"); ok {
		t.Error("a reserved handle must not resolve")
	}
	if !slices.Contains(skipped, "admin") {
		t.Errorf("the bad org should be reported, got %v", skipped)
	}
}

func hashOf(s string) [sha256.Size]byte { return sha256.Sum256([]byte(s)) }

func TestKeysAreFoundByHash(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	org, _ := newOrg(t, db, "active")
	live, revoked, expired := hashOf("live-"+string(org)), hashOf("revoked-"+string(org)), hashOf("expired-"+string(org))
	raw(t, db, "INSERT INTO engine.api_keys (hash, org_id, label, scopes) VALUES ($1, $2, 'ci', '{sessions,read}')", live[:], string(org))
	raw(t, db, "INSERT INTO engine.api_keys (hash, org_id, label, scopes, revoked_at) VALUES ($1, $2, 'old', '{read}', now())", revoked[:], string(org))
	raw(t, db, "INSERT INTO engine.api_keys (hash, org_id, label, scopes, expires_at) VALUES ($1, $2, 'temp', '{read}', now() - interval '1 hour')", expired[:], string(org))

	var failures []error
	keys := db.Keys(func(err error) { failures = append(failures, err) })
	k, ok := keys.Lookup(t.Context(), live)
	if !ok || k.Org != org || k.Label != "ci" || !slices.Equal(k.Scopes, []string{"sessions", "read"}) || !k.Expires.IsZero() {
		t.Fatalf("live key = %+v, %v", k, ok)
	}
	if _, ok = keys.Lookup(t.Context(), revoked); ok {
		t.Error("a revoked key must not be found")
	}
	if e, found := keys.Lookup(t.Context(), expired); !found || e.Expires.IsZero() {
		t.Errorf("an expired key is returned with its expiry for the caller to refuse: %+v, %v", e, found)
	}
	if _, ok = keys.Lookup(t.Context(), hashOf("unknown")); ok {
		t.Error("an unknown key must not be found")
	}
	if len(failures) != 0 {
		t.Errorf("failures: %v", failures)
	}

	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	keys.Touch(t.Context(), live, "203.0.113.9", at)
	keys.Touch(t.Context(), live, "198.51.100.1", at.Add(10*time.Second)) // inside the minute: not written
	use, found, err := keys.LastUse(t.Context(), live)
	if err != nil || !found || !use.At.Equal(at) || use.IP != "203.0.113.9" {
		t.Errorf("last use = %+v, %v, %v", use, found, err)
	}
	keys.Touch(t.Context(), live, "198.51.100.1", at.Add(2*time.Minute))
	if use, _, _ = keys.LastUse(t.Context(), live); use.IP != "198.51.100.1" {
		t.Errorf("after a minute the use is written again: %+v", use)
	}
}

func TestAuditTrail(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	org, _ := newOrg(t, db, "active")
	other, _ := newOrg(t, db, "active")
	var failures []error
	trail := db.Audit(func(err error) { failures = append(failures, err) })
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	for i, who := range []string{"dana", "sam", "dana"} {
		trail.Record(t.Context(), audit.Entry{
			At: at.Add(time.Duration(i) * time.Minute), Org: org, Issuer: "management-app", Subject: who,
			TokenID: fmt.Sprintf("jti-%d", i), Method: "POST", Route: "POST /__admin/sessions/key-based", Status: 201,
		})
	}
	trail.Record(t.Context(), audit.Entry{At: at, Org: other, Subject: "dana", Key: "ci", Method: "DELETE", Route: "DELETE /__admin/sessions/{id}", Status: 200})

	got := trail.List(t.Context(), org)
	if len(got) != 3 || got[0].TokenID != "jti-2" || got[2].TokenID != "jti-0" || !got[0].At.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("list = %+v: want 3 entries, newest first", got)
	}
	if got[0].Issuer != "management-app" || got[0].Status != 201 || got[0].Org != org {
		t.Errorf("entry = %+v", got[0])
	}
	if n := trail.Anonymise(t.Context(), org, "dana"); n != 2 {
		t.Errorf("anonymised %d, want 2", n)
	}
	for _, e := range trail.List(t.Context(), org) {
		if e.Subject == "dana" {
			t.Errorf("entry still names dana: %+v", e)
		}
	}
	if o := trail.List(t.Context(), other); len(o) != 1 || o[0].Subject != "dana" || o[0].Key != "ci" {
		t.Errorf("another org's trail was changed: %+v", o)
	}
	if len(failures) != 0 {
		t.Errorf("failures: %v", failures)
	}
}

func stub(t *testing.T, n int, url string) model.StubMapping {
	t.Helper()
	var m model.StubMapping
	body := fmt.Sprintf(`{"id":"00000000-0000-0000-0000-%012d","request":{"method":"GET","url":%q},"response":{"status":200,"body":"n%d"}}`, n, url, n)
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMappingsKeepTheirOrder(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	org, _ := newOrg(t, db, "active")
	ctx := t.Context()
	repo := db.Mappings()
	scope := store.Global(org)
	for i, url := range []string{"/a", "/b", "/c"} {
		if err := repo.Put(ctx, scope, stub(t, i+1, url)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Put(ctx, scope, stub(t, 1, "/a-changed")); err != nil { // replaced in place
		t.Fatal(err)
	}
	list, err := repo.List(ctx, scope)
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if list[0].ID != stub(t, 1, "").ID || list[1].ID != stub(t, 2, "").ID || list[2].ID != stub(t, 3, "").ID {
		t.Errorf("order changed: %s %s %s", list[0].ID, list[1].ID, list[2].ID)
	}
	got, err := repo.Get(ctx, scope, stub(t, 1, "").ID)
	if err != nil || got.Request.URL == nil || *got.Request.URL != "/a-changed" {
		t.Errorf("get = %+v, %v", got, err)
	}
	if _, err = repo.Get(ctx, scope, stub(t, 9, "").ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get of a missing mapping: %v", err)
	}
	if err = repo.Delete(ctx, scope, stub(t, 2, "").ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, scope, stub(t, 2, "").ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if err = repo.Put(ctx, store.Scope{Tenant: org, SessionID: "ses_1"}, stub(t, 4, "/d")); err == nil {
		t.Error("a session's mappings must not be stored here")
	}
	if err = repo.DeleteAll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if list, _ = repo.List(ctx, scope); len(list) != 0 {
		t.Errorf("after DeleteAll: %d", len(list))
	}
}

func TestMappingsOfAnotherOrgAreNotVisible(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	a, _ := newOrg(t, db, "active")
	b, _ := newOrg(t, db, "active")
	repo := db.Mappings()
	_ = repo.Put(t.Context(), store.Global(a), stub(t, 1, "/a"))
	if list, _ := repo.List(t.Context(), store.Global(b)); len(list) != 0 {
		t.Errorf("org b sees org a's mappings: %v", list)
	}
	if _, err := repo.Get(t.Context(), store.Global(b), stub(t, 1, "").ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get across orgs: %v", err)
	}
}

func TestFilesThroughObjectStorage(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	org, _ := newOrg(t, db, "active")
	other, _ := newOrg(t, db, "active")
	ctx := t.Context()
	blobs := blob.NewMemory()
	repo := blobfiles.New(db.FileIndex(), blobs, 0)
	scope := store.Global(org)
	if err := repo.Put(ctx, scope, "data/a.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Put(ctx, scope, "data/b.json", []byte(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}
	// A repository with no cache must read through the index and the object store.
	cold := blobfiles.New(db.FileIndex(), blobs, -1)
	if got, err := cold.Read(ctx, scope, "data/a.json"); err != nil || string(got) != `{"a":1}` {
		t.Errorf("read = %q, %v", got, err)
	}
	if names, err := cold.List(ctx, scope); err != nil || !slices.Equal(names, []string{"data/a.json", "data/b.json"}) {
		t.Errorf("list = %v, %v", names, err)
	}
	if _, err := cold.Read(ctx, store.Global(other), "data/a.json"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another org read the file: %v", err)
	}
	if err := cold.Put(ctx, scope, "data/a.json", []byte(`{"a":"changed"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := cold.Read(ctx, scope, "data/a.json"); string(got) != `{"a":"changed"}` {
		t.Errorf("after replace: %q", got)
	}
	if err := cold.Delete(ctx, scope, "data/b.json"); err != nil {
		t.Fatal(err)
	}
	if err := cold.Delete(ctx, scope, "data/b.json"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if err := cold.Purge(ctx, org); err != nil {
		t.Fatal(err)
	}
	if names, _ := cold.List(ctx, scope); len(names) != 0 {
		t.Errorf("after purge: %v", names)
	}
}

func TestChangesAreNotified(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	org, _ := newOrg(t, db, "active")
	ch, err := db.Watch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Mappings().Put(t.Context(), store.Global(org), stub(t, 1, "/a")); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("the watch ended")
			}
			if ev.Kind == "mapping" && ev.Scope.Tenant == org {
				return
			}
		case <-deadline:
			t.Fatal("no notice arrived for the mapping")
		}
	}
}

package app_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/auth/authtest"
	"github.com/pwgith/shadow-mock/internal/blob/s3"
	"github.com/pwgith/shadow-mock/internal/blobfiles"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/postgres/pgtest"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// eventually polls until fn is true or the time runs out. The engine learns of a database change through a NOTIFY
// and a reload, which takes a moment.
func eventually(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestManagedModeOnPostgresRedisAndObjectStorage runs the engine the way the hosted service does: orgs, keys, the
// global layer and the file names in Postgres, file bodies in an S3 bucket, counters and token ids in Redis. It
// needs a real Postgres (SHADOWMOCK_TEST_POSTGRES_DSN); the object store and Redis are in-process servers that
// speak the real protocols.
func TestManagedModeOnPostgresRedisAndObjectStorage(t *testing.T) {
	t.Parallel()
	db := pgtest.Connect(t)
	ctx := t.Context()

	backend := s3mem.New()
	if err := backend.CreateBucket("mocks"); err != nil {
		t.Fatal(err)
	}
	// The engine here runs on a fake clock, and the fake rejects requests more than 15 minutes from real time, as a
	// real store does. The engine runs on the real clock in production.
	objects := httptest.NewServer(gofakes3.New(backend, gofakes3.WithTimeSkewLimit(0)).Server())
	t.Cleanup(objects.Close)
	mr := miniredis.RunT(t)

	// The org, a key, the global layer and two files, as the management app would have written them.
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	handle := "t" + hex.EncodeToString(b)
	org := tenant.ID("org_" + handle)
	exec := func(sql string, args ...any) {
		t.Helper()
		if execErr := db.Exec(ctx, sql, args...); execErr != nil {
			t.Fatalf("%s: %v", sql, execErr)
		}
	}
	exec("INSERT INTO engine.orgs (id, handle) VALUES ($1, $2)", string(org), handle)
	t.Cleanup(func() {
		bg := context.WithoutCancel(ctx)
		for _, table := range []string{"files", "mappings", "api_keys", "org_aliases", "audit_log"} {
			_ = db.Exec(bg, "DELETE FROM engine."+table+" WHERE org_id = $1", string(org))
		}
		_ = db.Exec(bg, "DELETE FROM engine.orgs WHERE id = $1", string(org))
	})
	key := authtest.RandomKey()
	hash := auth.HashKey(key)
	exec("INSERT INTO engine.api_keys (hash, org_id, label, scopes) VALUES ($1, $2, 'ci-tests', '{sessions,read}')", hash[:], string(org))
	mapping := func(n int, url, file string) string {
		return fmt.Sprintf(`{"id":"00000000-0000-0000-0000-%012d","request":{"method":"GET","url":%q},"response":{"status":200,"bodyFileName":%q}}`, n, url, file)
	}
	exec("INSERT INTO engine.mappings (org_id, id, body) VALUES ($1, $2, $3)", string(org), "00000000-0000-0000-0000-000000000001", mapping(1, "/hello", "greeting.txt"))
	exec("INSERT INTO engine.mappings (org_id, id, body) VALUES ($1, $2, $3)", string(org), "00000000-0000-0000-0000-000000000002", mapping(2, "/late", "late.txt"))
	seed, err := s3.New(s3.Config{Endpoint: objects.URL, Bucket: "mocks", AccessKey: "k", SecretKey: "s", PathStyle: true}, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	files := blobfiles.New(db.FileIndex(), seed, -1)
	for name, body := range map[string]string{"greeting.txt": "hello from object storage", "late.txt": "never read until the store is down"} {
		if err = files.Put(ctx, store.Global(org), name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	clk := clock.NewFake(epoch)
	issuer := authtest.NewIssuer("management-app", "https://shadowmock.example", "mocks-test", clk)
	m := newManagedWith(t, clk, issuer, func(c *config.Config) {
		c.Storage.Global.Driver = "postgres"
		c.Storage.Postgres = config.Postgres{DSNEnv: pgtest.DSNVar, DSN: db.DSN, Migrate: true}
		c.Storage.Counters.Driver = "redis"
		c.Storage.Redis = config.Redis{Addr: mr.Addr(), Prefix: "sm:"}
		c.Storage.Blobs.Driver = "s3"
		c.Storage.Blobs.S3 = config.S3{Endpoint: objects.URL, Bucket: "mocks", AccessKeyEnv: "K", SecretKeyEnv: "S", AccessKey: "k", SecretKey: "s", PathStyle: true}
	})
	base := "/" + handle

	// The global layer comes from the database, and the body from object storage.
	if code, body, _ := m.do("GET", base+"/hello", "", ""); code != 200 || body != "hello from object storage" {
		t.Fatalf("GET /hello = %d %q", code, body)
	}
	// A Tool app key from the database works within its scopes, and nowhere else.
	if code, body, _ := m.do("GET", base+"/__admin/sessions", key, ""); code != 200 {
		t.Fatalf("list sessions with a key: %d %s", code, body)
	}
	if code, body, _ := m.do("POST", base+"/__admin/sessions/key-based", key, "{}"); code != 201 {
		t.Fatalf("create a session with a key: %d %s", code, body)
	}
	if code, _, _ := m.do("GET", base+"/__admin/sessions", authtest.RandomKey(), ""); code != 401 {
		t.Errorf("an unknown key: %d, want 401", code)
	}
	var audited int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM engine.audit_log WHERE org_id = $1 AND key_label = 'ci-tests' AND status = 201", string(org)).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audit rows for the key's call = %d, %v, want 1", audited, err)
	}
	// An admin token from the trusted issuer works for the org.
	// The token's org_id is the org's id, not its handle.
	if code, body, _ := m.do("GET", base+"/__admin/sessions", issuer.TokenFor("dana", string(org), "admin", nil), ""); code != 200 {
		t.Errorf("an admin token: %d %s", code, body)
	}

	// A mapping added to the database is served without a restart.
	exec("INSERT INTO engine.mappings (org_id, id, body) VALUES ($1, $2, $3)", string(org), "00000000-0000-0000-0000-000000000003",
		`{"id":"00000000-0000-0000-0000-000000000003","request":{"method":"GET","url":"/fresh"},"response":{"status":200,"body":"fresh"}}`)
	eventually(t, "a new mapping to be served", func() bool {
		code, body, _ := m.do("GET", base+"/fresh", "", "")
		return code == 200 && body == "fresh"
	})

	// The object store goes down: a cached file is still served, one that was never read is a 503 that says why.
	objects.Close()
	if code, _, _ := m.do("GET", base+"/hello", "", ""); code != 200 {
		t.Errorf("a cached file while the object store is down: %d, want 200", code)
	}
	code, body, hdr := m.do("GET", base+"/late", "", "")
	if code != 503 || errorOf(body) != "object_store_unavailable" || hdr.Get("X-Shadow-Mock-Reason") != "object_store_unavailable" {
		t.Errorf("an uncached file while the object store is down: %d %s %v", code, body, hdr)
	}

	// The org is suspended in the database, and the engine stops serving it without a restart.
	exec("UPDATE engine.orgs SET status = 'suspended' WHERE id = $1", string(org))
	eventually(t, "the suspension to take effect", func() bool {
		code, body, _ := m.do("GET", base+"/hello", "", "")
		return code == 403 && errorOf(body) == "org_suspended"
	})
	exec("UPDATE engine.orgs SET status = 'active' WHERE id = $1", string(org))
	eventually(t, "serving to resume", func() bool {
		code, _, _ := m.do("GET", base+"/hello", "", "")
		return code == 200
	})
}

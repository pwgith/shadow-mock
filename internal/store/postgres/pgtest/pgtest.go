// Package pgtest connects tests to a real Postgres. The database tests need one. CI provides it as a service
// container and a developer can point SHADOWMOCK_TEST_POSTGRES_DSN at any scratch database. Without the variable,
// the tests use PGlite, an in-memory Postgres run by Node from test/pglite (run `npm ci` there once). Without
// either, the test is skipped. The tests create and remove only their own orgs.
//
// PGlite is one Postgres session behind a connection multiplexer, so it differs from a server in two ways that
// matter here. The tests that use it run one at a time, so that their statements do not interleave, and it does
// not deliver LISTEN/NOTIFY, which DB.SupportsNotify reports.
package pgtest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pwgith/shadow-mock/internal/store/postgres"
)

// DSNVar names the environment variable that holds the DSN of the database to test against.
const DSNVar = "SHADOWMOCK_TEST_POSTGRES_DSN"

// DB is the database under test, and a pool of the test's own for the SQL that stands in for the management app,
// which writes the tables the engine reads.
type DB struct {
	*postgres.DB
	// DSN is the connection string, for configuring an engine that uses the same database.
	DSN  string
	pool *pgxpool.Pool
	// pglite is true when the database is PGlite and not a server.
	pglite bool
}

// SupportsNotify reports whether the database delivers LISTEN/NOTIFY. PGlite's socket server does not.
func (d *DB) SupportsNotify() bool { return !d.pglite }

// run serialises the tests that use PGlite.
var run sync.Mutex

// Connect returns a migrated database, or skips the test.
func Connect(t *testing.T) *DB {
	t.Helper()
	dsn, pglite := os.Getenv(DSNVar), false
	if dsn == "" {
		var reason string
		if dsn, reason = startPGlite(); dsn == "" {
			t.Skipf("set %s to a Postgres DSN, or run `npm ci` in test/pglite, to run the database tests: %s", DSNVar, reason)
		}
		pglite = true
		run.Lock()
		t.Cleanup(run.Unlock) // registered first, so it runs after the test's own cleanups
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db := postgres.New(pool)
	if err = db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return &DB{DB: db, DSN: dsn, pool: pool, pglite: pglite}
}

// Exec runs SQL directly against the tables.
func (d *DB) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.pool.Exec(ctx, sql, args...)
	return err
}

// QueryRow runs a query that returns one row.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

var (
	pgliteOnce   sync.Once
	pgliteDSN    string
	pgliteReason string
	// pgliteStdin is held open for the life of the test binary: Node ends when it closes.
	pgliteStdin io.WriteCloser
)

// startPGlite starts PGlite once per test binary. It returns an empty DSN and a reason when it cannot.
func startPGlite() (dsn, reason string) {
	pgliteOnce.Do(func() {
		var err error
		if pgliteDSN, err = launchPGlite(); err != nil {
			pgliteReason = err.Error()
		}
	})
	return pgliteDSN, pgliteReason
}

func launchPGlite() (string, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return "", errors.New("node is not installed")
	}
	dir, err := pgliteDir()
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		return "", errors.New("test/pglite has no node_modules")
	}
	cmd := exec.Command(node, "server.mjs") //nolint:gosec,noctx // a fixed script in this repository, ended by closing its stdin
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	if pgliteStdin, err = cmd.StdinPipe(); err != nil {
		return "", fmt.Errorf("start PGlite: %w", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("start PGlite: %w", err)
	}
	if err = cmd.Start(); err != nil {
		return "", fmt.Errorf("start PGlite: %w", err)
	}
	go func() { _ = cmd.Wait() }() //nolint:errcheck // the process ends with the test binary

	type result struct {
		port int
		err  error
	}
	got := make(chan result, 1)
	go func() {
		line, readErr := bufio.NewReader(out).ReadBytes('\n')
		var msg struct {
			Port int `json:"port"`
		}
		if readErr == nil {
			readErr = json.Unmarshal(line, &msg)
		}
		got <- result{msg.Port, readErr}
	}()
	select {
	case r := <-got:
		if r.err != nil || r.port == 0 {
			return "", fmt.Errorf("PGlite did not say where it listens: %v", r.err)
		}
		// One session serves every connection, so a named prepared statement made on one is seen by all: the simple
		// protocol makes none. A small pool keeps within the multiplexer's limit.
		return fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable&default_query_exec_mode=simple_protocol&pool_max_conns=4", r.port), nil
	case <-time.After(90 * time.Second):
		_ = pgliteStdin.Close()
		return "", errors.New("PGlite did not start in time")
	}
}

// pgliteDir finds test/pglite by looking for go.mod from the working directory up.
func pgliteDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("find the repository: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return filepath.Join(dir, "test", "pglite"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("the repository root was not found")
		}
		dir = parent
	}
}

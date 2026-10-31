// Package pgtest connects tests to a real Postgres. The database tests need one: CI provides it as a service
// container, and a developer can point SHADOWMOCK_TEST_POSTGRES_DSN at any scratch database. Without the variable
// the test is skipped. The tests create and remove only their own orgs.
package pgtest

import (
	"context"
	"os"
	"testing"

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
}

// Connect returns a migrated database, or skips the test.
func Connect(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv(DSNVar)
	if dsn == "" {
		t.Skipf("set %s to a Postgres DSN to run the database tests", DSNVar)
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
	return &DB{DB: db, DSN: dsn, pool: pool}
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

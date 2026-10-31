// Package postgres is the engine's database: the orgs, keys, global mappings and file names that the management
// app writes, and the audit log the engine writes (SAAS.md §6, FR-11.1, FR-11.8). Each store here mirrors one the
// engine already has in memory, behind the same interface, so the engine runs on either.
//
// Only the global layer is stored. Shadow mocks are runtime state and live in memory (and in Redis when there are
// several instances).
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the connection pool the stores share.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects and checks the connection. It does not migrate; call Migrate for that.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return &DB{pool: pool}, nil
}

// New wraps a pool that is already made.
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// Ping checks that the database answers, for readiness (FR-11.14).
func (d *DB) Ping(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	return nil
}

// Close closes the pool.
func (d *DB) Close() { d.pool.Close() }

//go:embed migrations/*.sql
var migrationFiles embed.FS

// SchemaVersion is the version of the schema this build expects: the number of its last migration. The version
// endpoint reports it so that the management app can check it against the one it was built for (FR-11.11e).
func SchemaVersion() int {
	ms, _ := migrations() //nolint:errcheck // the files are embedded and checked by a test
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

type migration struct {
	version int
	name    string
	sql     string
}

func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, convErr := strconv.Atoi(num)
		if !ok || convErr != nil {
			return nil, fmt.Errorf("migration %q: the name must start with a number and an underscore", e.Name())
		}
		b, readErr := migrationFiles.ReadFile("migrations/" + e.Name())
		if readErr != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), readErr)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// advisoryLock is held while migrating, so that instances starting together do not run the same migration twice.
const advisoryLock = 727274

// Migrate brings the schema up to date. Each migration runs once, in a transaction of its own.
func (d *DB) Migrate(ctx context.Context) error {
	ms, err := migrations()
	if err != nil {
		return err
	}
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres migrate: %w", err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLock); err != nil {
		return fmt.Errorf("postgres migrate: lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLock) //nolint:errcheck // the lock is released when the connection closes
	}()
	if _, err = conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS engine;
		CREATE TABLE IF NOT EXISTS engine.schema_migrations (version integer PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("postgres migrate: %w", err)
	}
	for _, m := range ms {
		var done bool
		if err = conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM engine.schema_migrations WHERE version = $1)", m.version).Scan(&done); err != nil {
			return fmt.Errorf("postgres migrate: %w", err)
		}
		if done {
			continue
		}
		if migErr := applyMigration(ctx, conn, m); migErr != nil {
			return migErr
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres migrate %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // rolling back after a commit is a no-op
	if _, err = tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("postgres migrate %s: %w", m.name, err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO engine.schema_migrations (version, name) VALUES ($1, $2)", m.version, m.name); err != nil {
		return fmt.Errorf("postgres migrate %s: %w", m.name, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres migrate %s: %w", m.name, err)
	}
	return nil
}

// notFound reports whether err is "no rows".
func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

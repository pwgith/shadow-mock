package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/pwgith/shadow-mock/internal/blobfiles"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Files is the index of an org's file names in the database: blobfiles.Index.
type Files struct {
	db *DB
}

var _ blobfiles.Index = (*Files)(nil)

// FileIndex returns the index of file names.
func (d *DB) FileIndex() *Files { return &Files{db: d} }

// Lookup implements blobfiles.Index.
func (f *Files) Lookup(ctx context.Context, org tenant.ID, name string) (blobfiles.Entry, error) {
	var (
		e   blobfiles.Entry
		sum []byte
	)
	err := f.db.pool.QueryRow(ctx, "SELECT sha256, size, content_type FROM engine.files WHERE org_id = $1 AND name = $2", string(org), name).
		Scan(&sum, &e.Size, &e.ContentType)
	switch {
	case notFound(err):
		return blobfiles.Entry{}, store.ErrNotFound
	case err != nil:
		return blobfiles.Entry{}, fmt.Errorf("postgres files: %w", err)
	case len(sum) != sha256.Size:
		return blobfiles.Entry{}, fmt.Errorf("postgres files: %s has a hash of %d bytes", name, len(sum))
	}
	copy(e.SHA256[:], sum)
	return e, nil
}

// Put implements blobfiles.Index.
func (f *Files) Put(ctx context.Context, org tenant.ID, name string, e blobfiles.Entry) error {
	_, err := f.db.pool.Exec(ctx, `INSERT INTO engine.files (org_id, name, sha256, size, content_type) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id, name) DO UPDATE SET sha256 = EXCLUDED.sha256, size = EXCLUDED.size, content_type = EXCLUDED.content_type`,
		string(org), name, e.SHA256[:], e.Size, e.ContentType)
	if err != nil {
		return fmt.Errorf("postgres files: %w", err)
	}
	return nil
}

// Delete implements blobfiles.Index.
func (f *Files) Delete(ctx context.Context, org tenant.ID, name string) error {
	tag, err := f.db.pool.Exec(ctx, "DELETE FROM engine.files WHERE org_id = $1 AND name = $2", string(org), name)
	switch {
	case err != nil:
		return fmt.Errorf("postgres files: %w", err)
	case tag.RowsAffected() == 0:
		return store.ErrNotFound
	}
	return nil
}

// DeleteAll implements blobfiles.Index.
func (f *Files) DeleteAll(ctx context.Context, org tenant.ID) error {
	if _, err := f.db.pool.Exec(ctx, "DELETE FROM engine.files WHERE org_id = $1", string(org)); err != nil {
		return fmt.Errorf("postgres files: %w", err)
	}
	return nil
}

// List implements blobfiles.Index.
func (f *Files) List(ctx context.Context, org tenant.ID) ([]string, error) {
	rows, err := f.db.pool.Query(ctx, "SELECT name FROM engine.files WHERE org_id = $1 ORDER BY name", string(org))
	if err != nil {
		return nil, fmt.Errorf("postgres files: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("postgres files: %w", err)
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres files: %w", err)
	}
	return out, nil
}

package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// Mappings is the global layer of mappings in the database. It is the MappingRepository of the global scope; a
// shadow mock's mappings are runtime state and are kept elsewhere (store.SplitMappings routes between them).
type Mappings struct {
	db *DB
}

var _ store.MappingRepository = (*Mappings)(nil)

// Mappings returns the repository.
func (d *DB) Mappings() *Mappings { return &Mappings{db: d} }

func globalOnly(s store.Scope) error {
	if !s.IsGlobal() {
		return fmt.Errorf("postgres mappings: only the global layer is stored, not %s", s)
	}
	return nil
}

// List implements store.MappingRepository, in the order the mappings were first put.
func (m *Mappings) List(ctx context.Context, s store.Scope) ([]model.StubMapping, error) {
	if err := globalOnly(s); err != nil {
		return nil, err
	}
	rows, err := m.db.pool.Query(ctx, "SELECT body FROM engine.mappings WHERE org_id = $1 ORDER BY position", string(s.Tenant))
	if err != nil {
		return nil, fmt.Errorf("postgres mappings: %w", err)
	}
	defer rows.Close()
	var out []model.StubMapping
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("postgres mappings: %w", err)
		}
		var sm model.StubMapping
		if err = json.Unmarshal(body, &sm); err != nil {
			return nil, fmt.Errorf("postgres mappings: a stored mapping is not valid: %w", err)
		}
		out = append(out, sm)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres mappings: %w", err)
	}
	return out, nil
}

// Get implements store.MappingRepository.
func (m *Mappings) Get(ctx context.Context, s store.Scope, id string) (model.StubMapping, error) {
	if err := globalOnly(s); err != nil {
		return model.StubMapping{}, err
	}
	var body []byte
	err := m.db.pool.QueryRow(ctx, "SELECT body FROM engine.mappings WHERE org_id = $1 AND id = $2", string(s.Tenant), id).Scan(&body)
	switch {
	case notFound(err):
		return model.StubMapping{}, fmt.Errorf("mapping %s in %s: %w", id, s, store.ErrNotFound)
	case err != nil:
		return model.StubMapping{}, fmt.Errorf("postgres mappings: %w", err)
	}
	var sm model.StubMapping
	if err = json.Unmarshal(body, &sm); err != nil {
		return model.StubMapping{}, fmt.Errorf("postgres mappings: a stored mapping is not valid: %w", err)
	}
	return sm, nil
}

// Put implements store.MappingRepository. A mapping that is put again keeps its place in the order.
func (m *Mappings) Put(ctx context.Context, s store.Scope, sm model.StubMapping) error {
	if err := globalOnly(s); err != nil {
		return err
	}
	body, err := json.Marshal(sm)
	if err != nil {
		return fmt.Errorf("postgres mappings: %w", err)
	}
	_, err = m.db.pool.Exec(ctx, `INSERT INTO engine.mappings (org_id, id, body) VALUES ($1, $2, $3)
		ON CONFLICT (org_id, id) DO UPDATE SET body = EXCLUDED.body`, string(s.Tenant), sm.ID, body)
	if err != nil {
		return fmt.Errorf("postgres mappings: %w", err)
	}
	return nil
}

// Delete implements store.MappingRepository.
func (m *Mappings) Delete(ctx context.Context, s store.Scope, id string) error {
	if err := globalOnly(s); err != nil {
		return err
	}
	tag, err := m.db.pool.Exec(ctx, "DELETE FROM engine.mappings WHERE org_id = $1 AND id = $2", string(s.Tenant), id)
	switch {
	case err != nil:
		return fmt.Errorf("postgres mappings: %w", err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("mapping %s in %s: %w", id, s, store.ErrNotFound)
	}
	return nil
}

// DeleteAll implements store.MappingRepository.
func (m *Mappings) DeleteAll(ctx context.Context, s store.Scope) error {
	if err := globalOnly(s); err != nil {
		return err
	}
	if _, err := m.db.pool.Exec(ctx, "DELETE FROM engine.mappings WHERE org_id = $1", string(s.Tenant)); err != nil {
		return fmt.Errorf("postgres mappings: %w", err)
	}
	return nil
}

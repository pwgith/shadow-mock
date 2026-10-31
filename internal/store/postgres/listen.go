package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Channel is the NOTIFY channel the triggers publish on.
const Channel = "sm_changes"

var _ store.Watcher = (*DB)(nil)

// Watch listens for the change notices the triggers send and returns them until ctx ends or the connection fails.
// It holds one connection of its own, outside the pool, because a connection that listens cannot be shared. When
// it fails the channel is closed and the caller starts it again; notices sent in the gap are caught by the
// caller's poll, which is why a notice is only a hint (FR-11.8).
func (d *DB) Watch(ctx context.Context) (<-chan store.ChangeEvent, error) {
	pooled, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres listen: %w", err)
	}
	conn := pooled.Hijack()
	if _, err = conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("postgres listen: %w", err)
	}
	out := make(chan store.ChangeEvent, 64)
	go func() {
		defer close(out)
		defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
		for {
			n, waitErr := conn.WaitForNotification(ctx)
			if waitErr != nil {
				return
			}
			var payload struct {
				Kind   string `json:"kind"`
				Tenant string `json:"tenant"`
			}
			if json.Unmarshal([]byte(n.Payload), &payload) != nil || payload.Kind == "" {
				continue
			}
			ev := store.ChangeEvent{Kind: payload.Kind, Scope: store.Global(tenant.ID(payload.Tenant))}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

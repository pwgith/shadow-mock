package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Replay remembers token ids in Redis for as long as the tokens live, so a token is used once across all
// instances (FR-11.2). It fails closed: when Redis cannot be reached the token counts as used, because accepting
// it could let a captured token be replayed during an outage.
type Replay struct {
	rdb     *goredis.Client
	prefix  string
	clk     clock.Clock
	onError func(error)
}

// replayTimeout bounds the one round trip on the authentication path.
const replayTimeout = 2 * time.Second

// Seen reports whether the id was used before, and remembers it until `until`.
func (r *Replay) Seen(ctx context.Context, issuer, id string, until time.Time) bool {
	ttl := max(until.Sub(r.clk.Now()), time.Second)
	ctx, cancel := context.WithTimeout(ctx, replayTimeout)
	defer cancel()
	fresh, err := r.rdb.SetNX(ctx, r.prefix+"jti:"+issuer+":"+id, 1, ttl).Result()
	if err != nil {
		if r.onError != nil {
			r.onError(fmt.Errorf("redis replay: %w", err))
		}
		return true
	}
	return !fresh
}

// Bus carries change notices between instances. An instance publishes a notice after it writes, and the others
// reload what the notice names (FR-11.8). A notice is a hint: an instance that misses one catches up on its next
// poll, so Redis pub/sub's lack of delivery guarantees does not matter.
type Bus struct {
	rdb     *goredis.Client
	channel string
}

var _ store.Watcher = (*Bus)(nil)

type notice struct {
	Kind    string `json:"kind"`
	Tenant  string `json:"tenant"`
	Session string `json:"session,omitempty"`
}

// Publish tells the other instances about a change.
func (b *Bus) Publish(ctx context.Context, ev store.ChangeEvent) error {
	payload, err := json.Marshal(notice{Kind: ev.Kind, Tenant: string(ev.Scope.Tenant), Session: ev.Scope.SessionID})
	if err != nil {
		return fmt.Errorf("redis bus: %w", err)
	}
	if err = b.rdb.Publish(ctx, b.channel, payload).Err(); err != nil {
		return fmt.Errorf("redis bus: %w", err)
	}
	return nil
}

// Watch subscribes and returns the notices until ctx ends. It returns once the subscription is in place, so a
// notice published after it returns is not missed.
func (b *Bus) Watch(ctx context.Context) (<-chan store.ChangeEvent, error) {
	sub := b.rdb.Subscribe(ctx, b.channel)
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, fmt.Errorf("redis bus: %w", err)
	}
	out := make(chan store.ChangeEvent, 64)
	go func() {
		defer close(out)
		defer sub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-sub.Channel():
				if !ok {
					return
				}
				var n notice
				if json.Unmarshal([]byte(m.Payload), &n) != nil || n.Kind == "" {
					continue
				}
				ev := store.ChangeEvent{Kind: n.Kind, Scope: store.Scope{Tenant: tenant.ID(n.Tenant), SessionID: n.Session}}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

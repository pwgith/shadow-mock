package redis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// Counters keeps call counters in Redis. There is one hash for each request scope, named by the org and the
// session ("~" for the global session), and two fields in it for each stub: the count, "c|org|session|stub", and
// the time of the last call, "t|org|session|stub". A call is one HINCRBY and one HSET in a transaction.
type Counters struct {
	rdb    *goredis.Client
	prefix string
}

var _ counter.Counters = (*Counters)(nil)

const global = "~"

func (c *Counters) hash(s store.Scope) (string, error) {
	sess := s.SessionID
	if sess == "" {
		sess = global
	}
	if unsafe(string(s.Tenant)) || unsafe(sess) || s.Tenant == "" {
		return "", fmt.Errorf("redis counters: scope %s has a character that is not allowed in a key", s)
	}
	return c.prefix + "cnt:" + string(s.Tenant) + ":" + sess, nil
}

func field(kind string, k counter.Key) (string, error) {
	if unsafe(string(k.StubScope.Tenant)) || unsafe(k.StubScope.SessionID) || unsafe(k.StubID) {
		return "", fmt.Errorf("redis counters: stub %s/%s has a character that is not allowed in a field", k.StubScope, k.StubID)
	}
	return kind + "|" + string(k.StubScope.Tenant) + "|" + k.StubScope.SessionID + "|" + k.StubID, nil
}

// parse reads a field name back: its kind, the stub's scope and its id.
func parse(f string) (kind string, scope store.Scope, stubID string, ok bool) {
	parts := strings.SplitN(f, "|", 4)
	if len(parts) != 4 {
		return "", store.Scope{}, "", false
	}
	return parts[0], store.Scope{Tenant: tenant.ID(parts[1]), SessionID: parts[2]}, parts[3], true
}

func wrap(err error) error { return fmt.Errorf("redis counters: %w", err) }

// Next implements counter.Counters.
func (c *Counters) Next(ctx context.Context, k counter.Key, now time.Time) (int64, error) {
	h, err := c.hash(k.RequestScope)
	if err != nil {
		return 0, err
	}
	cf, err := field("c", k)
	if err != nil {
		return 0, err
	}
	tf, _ := field("t", k) //nolint:errcheck // cf passed the same check
	pipe := c.rdb.TxPipeline()
	inc := pipe.HIncrBy(ctx, h, cf, 1)
	pipe.HSet(ctx, h, tf, now.UnixNano())
	if _, err = pipe.Exec(ctx); err != nil {
		return 0, wrap(err)
	}
	return inc.Val(), nil
}

// Peek implements counter.Counters.
func (c *Counters) Peek(ctx context.Context, k counter.Key) (int64, error) {
	h, err := c.hash(k.RequestScope)
	if err != nil {
		return 0, err
	}
	cf, err := field("c", k)
	if err != nil {
		return 0, err
	}
	n, err := c.rdb.HGet(ctx, h, cf).Int64()
	switch {
	case errors.Is(err, goredis.Nil):
		return 1, nil
	case err != nil:
		return 0, wrap(err)
	}
	return n + 1, nil
}

// List implements counter.Counters.
func (c *Counters) List(ctx context.Context, s store.Scope) ([]counter.Entry, error) {
	h, err := c.hash(s)
	if err != nil {
		return nil, err
	}
	all, err := c.rdb.HGetAll(ctx, h).Result()
	if err != nil {
		return nil, wrap(err)
	}
	var out []counter.Entry
	for f, v := range all {
		kind, stubScope, id, ok := parse(f)
		if !ok || kind != "c" {
			continue
		}
		n, convErr := strconv.ParseInt(v, 10, 64)
		if convErr != nil {
			continue
		}
		e := counter.Entry{Key: counter.Key{RequestScope: s, StubScope: stubScope, StubID: id}, Count: n}
		if last, ok := all["t"+f[1:]]; ok {
			if ns, convErr := strconv.ParseInt(last, 10, 64); convErr == nil && ns != 0 {
				e.LastCalledAt = time.Unix(0, ns).UTC()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.StubID != out[j].Key.StubID {
			return out[i].Key.StubID < out[j].Key.StubID
		}
		return out[i].Key.StubScope.SessionID > out[j].Key.StubScope.SessionID
	})
	return out, nil
}

// Set implements counter.Counters.
func (c *Counters) Set(ctx context.Context, k counter.Key, n int64) error {
	h, err := c.hash(k.RequestScope)
	if err != nil {
		return err
	}
	cf, err := field("c", k)
	if err != nil {
		return err
	}
	if err = c.rdb.HSet(ctx, h, cf, n).Err(); err != nil {
		return wrap(err)
	}
	return nil
}

// Reset implements counter.Counters.
func (c *Counters) Reset(ctx context.Context, s store.Scope, stubID string) error {
	h, err := c.hash(s)
	if err != nil {
		return err
	}
	if stubID == "" {
		if err = c.rdb.Del(ctx, h).Err(); err != nil {
			return wrap(err)
		}
		return nil
	}
	return c.dropFields(ctx, h, func(_ store.Scope, id string) bool { return id == stubID })
}

// ForgetStub implements counter.Counters.
func (c *Counters) ForgetStub(ctx context.Context, stubScope store.Scope, stubID string) error {
	return c.eachHashOf(ctx, stubScope.Tenant, func(h string) error {
		return c.dropFields(ctx, h, func(sc store.Scope, id string) bool { return sc == stubScope && id == stubID })
	})
}

// ForgetScope implements counter.Counters.
func (c *Counters) ForgetScope(ctx context.Context, s store.Scope) error {
	own, err := c.hash(s)
	if err != nil {
		return err
	}
	if err = c.rdb.Del(ctx, own).Err(); err != nil {
		return wrap(err)
	}
	return c.eachHashOf(ctx, s.Tenant, func(h string) error {
		return c.dropFields(ctx, h, func(sc store.Scope, _ string) bool { return sc == s })
	})
}

// dropFields deletes the fields of hash h whose stub scope and id satisfy match.
func (c *Counters) dropFields(ctx context.Context, h string, match func(store.Scope, string) bool) error {
	keys, err := c.rdb.HKeys(ctx, h).Result()
	if err != nil {
		return wrap(err)
	}
	var drop []string
	for _, f := range keys {
		if _, sc, id, ok := parse(f); ok && match(sc, id) {
			drop = append(drop, f)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	if err = c.rdb.HDel(ctx, h, drop...).Err(); err != nil {
		return wrap(err)
	}
	return nil
}

// eachHashOf calls fn for every request-scope hash of an org.
func (c *Counters) eachHashOf(ctx context.Context, org tenant.ID, fn func(hash string) error) error {
	if unsafe(string(org)) || org == "" {
		return fmt.Errorf("redis counters: org %q has a character that is not allowed in a key", org)
	}
	iter := c.rdb.Scan(ctx, 0, c.prefix+"cnt:"+string(org)+":*", 100).Iterator()
	for iter.Next(ctx) {
		if err := fn(iter.Val()); err != nil {
			return err
		}
	}
	if err := iter.Err(); err != nil {
		return wrap(err)
	}
	return nil
}

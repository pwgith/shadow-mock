// Package redis holds the engine's shared runtime state in Redis, so that many instances behave as one: call
// counters, the single-use cache for token ids, and the channel on which instances tell each other about changes
// (DESIGN.md §5.1, FR-11.1, FR-11.8). Nothing here is on the request path of a mock request except the call
// counter, and that is one round trip.
package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/pwgith/shadow-mock/internal/clock"
)

// Config is how to reach Redis.
type Config struct {
	Addr     string
	Username string
	Password string
	// TLS turns on TLS with the system's roots.
	TLS bool
	DB  int
	// Prefix starts every key, so that environments can share a server. Empty means "sm:".
	Prefix string
	// DialTimeout bounds connecting; operations are bounded by their contexts. Zero means 3 seconds.
	DialTimeout time.Duration
}

// DefaultPrefix starts every key unless Config.Prefix says otherwise.
const DefaultPrefix = "sm:"

// Client is the connection and the key prefix. Its parts share both.
type Client struct {
	rdb    *goredis.Client
	prefix string
}

// Open connects lazily: it checks the configuration and returns, and the first command dials.
func Open(cfg Config) (*Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis: addr is needed")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 3 * time.Second
	}
	opt := &goredis.Options{
		Addr: cfg.Addr, Username: cfg.Username, Password: cfg.Password, DB: cfg.DB, DialTimeout: cfg.DialTimeout,
	}
	if cfg.TLS {
		host, _, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			return nil, fmt.Errorf("redis: addr %q: %w", cfg.Addr, err)
		}
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return New(goredis.NewClient(opt), cfg.Prefix), nil
}

// New wraps a client that is already made, for tests and for callers with their own options.
func New(rdb *goredis.Client, prefix string) *Client {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	return &Client{rdb: rdb, prefix: prefix}
}

// Ping checks that Redis answers, for readiness (FR-11.14).
func (c *Client) Ping(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	if err := c.rdb.Close(); err != nil {
		return fmt.Errorf("close redis: %w", err)
	}
	return nil
}

// Counters returns the call counters.
func (c *Client) Counters() *Counters { return &Counters{rdb: c.rdb, prefix: c.prefix} }

// Replay returns the cache that makes a token usable once. onError is told when Redis fails, since Seen has no
// way to return an error.
func (c *Client) Replay(clk clock.Clock, onError func(error)) *Replay {
	return &Replay{rdb: c.rdb, prefix: c.prefix, clk: clk, onError: onError}
}

// Bus returns the change channel.
func (c *Client) Bus() *Bus { return &Bus{rdb: c.rdb, channel: c.prefix + "changes"} }

// unsafe reports whether s holds a character that would break a key, a field or a scan pattern. The ids the engine
// makes never do; this is the check that says so.
func unsafe(s string) bool { return strings.ContainsAny(s, "|*?[]\\\x00\n") }

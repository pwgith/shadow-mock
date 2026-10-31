package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/blob/s3"
	"github.com/pwgith/shadow-mock/internal/blobfiles"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/postgres"
	redisstore "github.com/pwgith/shadow-mock/internal/store/redis"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// startupTimeout bounds opening the shared stores, so a database that is not there stops the engine from starting
// instead of hanging it.
const startupTimeout = 30 * time.Second

// retryAfterLoss is how long the change relay waits before it listens again.
const retryAfterLoss = 5 * time.Second

// shared holds the engine's connections to the shared stores of managed mode (FR-11.1): the database, Redis and
// the object store. Each is nil when the configuration does not use it, and the engine then keeps that state in
// memory, as a single instance does.
type shared struct {
	pg    *postgres.DB
	orgs  *postgres.Orgs
	redis *redisstore.Client
	blobs blob.Store
}

// openShared connects to the stores the configuration names and registers their closers.
func (a *App) openShared(cfg config.Config, o Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	if cfg.Storage.Redis.Addr != "" {
		c, err := redisstore.Open(redisstore.Config{
			Addr: cfg.Storage.Redis.Addr, Username: cfg.Storage.Redis.Username, Password: cfg.Storage.Redis.Password,
			TLS: cfg.Storage.Redis.TLS, DB: cfg.Storage.Redis.DB, Prefix: cfg.Storage.Redis.Prefix,
		})
		if err != nil {
			return err
		}
		a.closers = append(a.closers, c.Close)
		if err = c.Ping(ctx); err != nil {
			return fmt.Errorf("storage.redis: %w", err)
		}
		a.sh.redis = c
	}
	if cfg.Storage.Global.Driver != "postgres" {
		return nil
	}
	db, err := postgres.Open(ctx, cfg.Storage.Postgres.DSN)
	if err != nil {
		return fmt.Errorf("storage.postgres: %w", err)
	}
	a.closers = append(a.closers, func() error { db.Close(); return nil })
	if cfg.Storage.Postgres.Migrate {
		if migErr := db.Migrate(ctx); migErr != nil {
			return migErr
		}
	}
	a.sh.pg = db
	a.sh.orgs = db.Orgs(func(handle string, err error) {
		o.Logger.Warn("an org was left out of the directory", "handle", handle, "error", err.Error())
	})
	a.sh.blobs, err = openBlobs(cfg, o.Clock)
	return err
}

// openBlobs opens the object store the configuration names.
func openBlobs(cfg config.Config, clk clock.Clock) (blob.Store, error) {
	b := cfg.Storage.Blobs
	switch b.Driver {
	case "memory":
		return blob.NewMemory(), nil
	case "s3":
		return s3.New(s3.Config{
			Endpoint: b.S3.Endpoint, Region: b.S3.Region, Bucket: b.S3.Bucket,
			AccessKey: b.S3.AccessKey, SecretKey: b.S3.SecretKey, SessionToken: b.S3.SessionToken,
			PathStyle: b.S3.PathStyle, Timeout: time.Duration(b.S3.Timeout),
		}, clk)
	default:
		dir := b.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cfg.Root, dir)
		}
		return blob.NewDir(dir)
	}
}

// counters is where call counters live: in Redis, shared by all instances, or in memory.
func (a *App) counters() counter.Counters {
	if a.sh.redis != nil && a.cfg.Storage.Counters.Driver == "redis" {
		return a.sh.redis.Counters()
	}
	return counter.NewMemory()
}

// keyStore is where Tool app keys are read from.
func (a *App) keyStore(o Options) auth.KeyStore {
	if a.sh.pg != nil {
		return a.sh.pg.Keys(func(err error) { o.Logger.Error("tool app keys", "error", err.Error()) })
	}
	a.keys = auth.NewMemoryKeys()
	return a.keys
}

// replayCache is where used token ids are remembered.
func (a *App) replayCache(o Options) auth.Replay {
	if a.sh.redis != nil {
		return a.sh.redis.Replay(o.Clock, func(err error) { o.Logger.Error("token replay cache", "error", err.Error()) })
	}
	return auth.NewMemoryReplay(o.Clock)
}

// auditStore is the audit trail as the admin API's store, or nil when there is none: a nil *audit.Trail in the
// interface would not compare equal to nil.
func (a *App) auditStore() audit.Store {
	return a.audit
}

// managedAudit picks where the audit trail is kept.
func (a *App) managedAudit(o Options) audit.Store {
	if a.sh.pg != nil {
		return a.sh.pg.Audit(func(err error) { o.Logger.Error("audit trail", "error", err.Error()) })
	}
	a.trail = audit.New()
	return a.trail
}

// globalFiles is the file repository of the global layer when it is in the database: names in Postgres, bodies in
// object storage, with a cache.
func (a *App) globalFiles() store.FileRepository {
	return blobfiles.New(a.sh.pg.FileIndex(), a.sh.blobs, 0)
}

// readiness reports the shared stores for /__ready. A store that is down is reported and the engine keeps
// serving from memory with the last configuration it read (FR-11.14).
func (a *App) readiness(ctx context.Context) map[string]string {
	checks := map[string]string{}
	if a.sh.pg != nil {
		checks["postgres"] = upOrDown(a.sh.pg.Ping(ctx))
	}
	if a.sh.redis != nil {
		checks["redis"] = upOrDown(a.sh.redis.Ping(ctx))
	}
	return checks
}

func upOrDown(err error) string {
	if err != nil {
		return "down"
	}
	return "up"
}

// startShared loads the orgs from the database and starts the relay that keeps them, and the global layer, up to
// date. It returns the orgs to load and the stream of changes for the service.
func (a *App) startShared(ctx context.Context) ([]tenant.ID, <-chan store.ChangeEvent, error) {
	if err := a.sh.orgs.Refresh(ctx); err != nil {
		return nil, nil, fmt.Errorf("load orgs: %w", err)
	}
	out := make(chan store.ChangeEvent, 64)
	go a.relay(ctx, out)
	poll := time.Duration(a.cfg.Storage.Postgres.PollInterval)
	if poll > 0 {
		go clock.Every(ctx, poll, func() {
			if err := a.sh.orgs.Refresh(ctx); err != nil {
				a.log.WarnContext(ctx, "refresh orgs", "error", err.Error())
			}
		})
	}
	return a.sh.orgs.IDs(), out, nil
}

// relay passes the database's change notices to the service until ctx ends. An org or key notice also refreshes the
// directory. When the connection is lost it listens again and then reloads every org, because notices sent in the
// gap are gone (a notice is only a hint).
func (a *App) relay(ctx context.Context, out chan<- store.ChangeEvent) {
	defer close(out)
	for ctx.Err() == nil {
		ch, err := a.sh.pg.Watch(ctx)
		if err != nil {
			a.log.WarnContext(ctx, "listen for changes", "error", err.Error())
			clock.Real{}.Sleep(ctx, retryAfterLoss)
			continue
		}
		// Once the listener is in place, reload everything: it closes the gap between loading at start-up and
		// listening, and the gap after a lost connection.
		a.resync(ctx, out)
		for ev := range ch {
			if ev.Kind == "org" {
				if err = a.sh.orgs.Refresh(ctx); err != nil {
					a.log.WarnContext(ctx, "refresh orgs", "error", err.Error())
				}
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() == nil {
			a.log.WarnContext(ctx, "the change listener was lost; listening again")
			clock.Real{}.Sleep(ctx, retryAfterLoss)
		}
	}
}

// resync refreshes the directory and has every org's global layer rebuilt.
func (a *App) resync(ctx context.Context, out chan<- store.ChangeEvent) {
	if err := a.sh.orgs.Refresh(ctx); err != nil {
		a.log.WarnContext(ctx, "refresh orgs", "error", err.Error())
		return
	}
	for _, id := range a.sh.orgs.IDs() {
		select {
		case out <- store.ChangeEvent{Kind: "org", Scope: store.Global(id)}:
		case <-ctx.Done():
			return
		}
	}
}

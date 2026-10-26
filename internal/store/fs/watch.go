package fs

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/pwgith/shadow-mock/internal/store"
)

// debounce groups bursts of file events (editors write several times per save).
const debounce = 200 * time.Millisecond

// Watch reloads the mappings of scope s whenever a file under dir changes (FR-1.5), and sends
// an event after each reload. It stops when ctx is done.
func (r *Mappings) Watch(ctx context.Context, dir string, s store.Scope) (<-chan store.ChangeEvent, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("start file watcher: %w", err)
	}
	if err := addTree(w, dir); err != nil {
		return nil, errors.Join(err, w.Close())
	}
	out := make(chan store.ChangeEvent, 1) // one pending reload is enough; later ones coalesce
	go r.watchLoop(ctx, w, s, out)
	return out, nil
}

func (r *Mappings) watchLoop(ctx context.Context, w *fsnotify.Watcher, s store.Scope, out chan<- store.ChangeEvent) {
	defer close(out)
	defer w.Close()
	// Debouncing is wall-clock scheduling, not a reading of the current time.
	timer := time.NewTimer(time.Hour) //nolint:forbidigo // debounce timer for file events
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Create) {
				_ = w.Add(ev.Name) //nolint:errcheck // a new plain file cannot be watched; directories can
			}
			timer.Reset(debounce)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			r.logger.WarnContext(ctx, "mapping watcher error", "error", err.Error())
		case <-timer.C:
			if err := r.Load(ctx, s); err != nil {
				r.logger.ErrorContext(ctx, "reload mappings", "error", err.Error())
				continue
			}
			select {
			case out <- store.ChangeEvent{Kind: "mapping", Scope: s}:
			default:
			}
		}
	}
}

func addTree(w *fsnotify.Watcher, dir string) error {
	err := filepath.WalkDir(dir, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return w.Add(p)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}
	return nil
}

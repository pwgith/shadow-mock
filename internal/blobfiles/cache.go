package blobfiles

import (
	"container/list"
	"crypto/sha256"
	"slices"
	"sync"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

type cacheKey struct {
	org tenant.ID
	sum [sha256.Size]byte
}

type cacheItem struct {
	key  cacheKey
	data []byte
}

// cache keeps recently read bodies, up to a number of bytes, dropping the least recently used first. A body is
// content-addressed, so a cached copy is never out of date.
type cache struct {
	mu     sync.Mutex
	limit  int64
	used   int64
	order  *list.List // front is the most recently used
	byKey  map[cacheKey]*list.Element
	paused bool
}

func newCache(limit int64) *cache {
	return &cache{limit: limit, order: list.New(), byKey: map[cacheKey]*list.Element{}, paused: limit < 0}
}

func (c *cache) get(org tenant.ID, sum [sha256.Size]byte) ([]byte, bool) {
	if c.paused {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[cacheKey{org, sum}]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	it, ok := el.Value.(*cacheItem)
	if !ok {
		return nil, false
	}
	return slices.Clone(it.data), true
}

func (c *cache) put(org tenant.ID, sum [sha256.Size]byte, data []byte) {
	if c.paused || int64(len(data)) > c.limit {
		return
	}
	k := cacheKey{org, sum}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[k]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.byKey[k] = c.order.PushFront(&cacheItem{key: k, data: slices.Clone(data)})
	c.used += int64(len(data))
	for c.used > c.limit {
		last := c.order.Back()
		if it, ok := c.order.Remove(last).(*cacheItem); ok {
			delete(c.byKey, it.key)
			c.used -= int64(len(it.data))
		}
	}
}

func (c *cache) forget(org tenant.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, el := range c.byKey {
		if k.org == org {
			if it, ok := el.Value.(*cacheItem); ok {
				c.used -= int64(len(it.data))
			}
			c.order.Remove(el)
			delete(c.byKey, k)
		}
	}
}

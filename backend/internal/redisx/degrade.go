// Package redisx degrade.go — in-memory LRU fallback used when
// GracefulDegrade is active (Redis unreachable).
//
// This is intentionally simple: a map with a fixed capacity, evicting the
// oldest entry (by insertion order via a doubly-linked list) when full.
// It is NOT a replacement for Redis at scale — it is a demo-quality guard
// that keeps the API alive during a Redis outage.
package redisx

import (
	"container/list"
	"sync"
	"time"
)

// DegradedMode mirrors GracefulDegrade as a plain bool for readability in
// handler code that already imports this package.
// Use GracefulDegrade.Load() == 1 for atomic checks in hot paths.
var DegradedMode bool

// entry is one record in the in-memory cache.
type entry struct {
	key       string
	value     string
	expiresAt time.Time
}

// InMemoryCache is a bounded in-memory LRU cache used as a Redis fallback.
// The zero value is NOT usable; use NewInMemoryCache.
type InMemoryCache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	items    map[string]*list.Element
}

// NewInMemoryCache constructs an InMemoryCache with the given capacity.
// A capacity ≤ 0 defaults to 512 entries.
func NewInMemoryCache(capacity int) *InMemoryCache {
	if capacity <= 0 {
		capacity = 512
	}
	return &InMemoryCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

// Set inserts or updates key with the given value and TTL.
// A zero TTL means the entry never expires.
func (c *InMemoryCache) Set(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*entry).value = value
		el.Value.(*entry).expiresAt = exp
		return
	}

	// Evict LRU entry if at capacity.
	if c.ll.Len() >= c.capacity {
		c.evictOldest()
	}

	e := &entry{key: key, value: value, expiresAt: exp}
	el := c.ll.PushFront(e)
	c.items[key] = el
}

// Get returns the value for key and whether it was found and is not expired.
func (c *InMemoryCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	e := el.Value.(*entry)
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		c.removeElement(el)
		return "", false
	}
	c.ll.MoveToFront(el)
	return e.value, true
}

// Delete removes key from the cache. No-op if the key does not exist.
func (c *InMemoryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}
}

// Len returns the current number of entries (including possibly expired ones
// that have not yet been lazily evicted).
func (c *InMemoryCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *InMemoryCache) evictOldest() {
	el := c.ll.Back()
	if el != nil {
		c.removeElement(el)
	}
}

func (c *InMemoryCache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	e := el.Value.(*entry)
	delete(c.items, e.key)
}

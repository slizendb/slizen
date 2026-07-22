package cache

import (
	"container/list"
	"sync"
	"time"
)

const entryOverheadBytes int64 = 128

// Clock keeps cache tests deterministic.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// EntrySnapshot is an immutable view of a cached value.
type EntrySnapshot struct {
	Key           string        `json:"key,omitempty"`
	Value         []byte        `json:"-"`
	InsertedAt    time.Time     `json:"inserted_at"`
	ExpiresAt     time.Time     `json:"expires_at"`
	LastAccess    time.Time     `json:"last_access"`
	EstimatedSize int64         `json:"estimated_size"`
	Age           time.Duration `json:"age"`
	TTL           time.Duration `json:"ttl"`
}

// Stats contains aggregate cache information. Values are approximate where
// size accounting is involved.
type Stats struct {
	Entries   int    `json:"entries"`
	Bytes     int64  `json:"bytes"`
	MaxBytes  int64  `json:"max_bytes"`
	Evictions uint64 `json:"evictions"`
}

type entry struct {
	key           string
	value         []byte
	insertedAt    time.Time
	expiresAt     time.Time
	lastAccess    time.Time
	estimatedSize int64
}

// Cache is a race-safe bounded in-memory LRU cache.
type Cache struct {
	mu         sync.Mutex
	clock      Clock
	maxBytes   int64
	maxEntries int
	items      map[string]*list.Element
	lru        *list.List
	bytes      int64
	evictions  uint64
	closed     bool
}

// New creates a cache. maxEntries <= 0 means the entry count is bounded only by
// maxBytes.
func New(maxBytes int64, maxEntries int, clock Clock) *Cache {
	if clock == nil {
		clock = realClock{}
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &Cache{
		clock:      clock,
		maxBytes:   maxBytes,
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element),
		lru:        list.New(),
	}
}

// EstimateSize returns Slizen's documented cache accounting approximation.
func EstimateSize(key string, value []byte) int64 {
	return int64(len(key)+len(value)) + entryOverheadBytes
}

// Put stores a copy of value until ttl expires. It returns false when the entry
// cannot fit or the cache is closed.
func (c *Cache) Put(key string, value []byte, ttl time.Duration) bool {
	if ttl <= 0 || key == "" {
		return false
	}

	now := c.clock.Now()
	copied := append([]byte(nil), value...)
	size := EstimateSize(key, copied)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.maxBytes <= 0 || size > c.maxBytes {
		c.removeLocked(key)
		return false
	}

	if existing, ok := c.items[key]; ok {
		ent := existing.Value.(*entry)
		c.bytes -= ent.estimatedSize
		ent.value = copied
		ent.insertedAt = now
		ent.expiresAt = now.Add(ttl)
		ent.lastAccess = now
		ent.estimatedSize = size
		c.bytes += size
		c.lru.MoveToFront(existing)
	} else {
		ent := &entry{
			key:           key,
			value:         copied,
			insertedAt:    now,
			expiresAt:     now.Add(ttl),
			lastAccess:    now,
			estimatedSize: size,
		}
		c.items[key] = c.lru.PushFront(ent)
		c.bytes += size
	}

	c.enforceLimitsLocked()
	return true
}

// Get returns a copy of a fresh cached value.
func (c *Cache) Get(key string) (EntrySnapshot, bool) {
	return c.get(key, 0, false)
}

// GetStale returns a cached value that is either fresh or inside the configured
// stale grace period. Callers must only use this when stale reads are enabled.
func (c *Cache) GetStale(key string, grace time.Duration) (EntrySnapshot, bool) {
	return c.get(key, grace, true)
}

func (c *Cache) get(key string, grace time.Duration, allowStale bool) (EntrySnapshot, bool) {
	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return EntrySnapshot{}, false
	}
	ent := el.Value.(*entry)
	expiryLimit := ent.expiresAt
	if allowStale && grace > 0 {
		expiryLimit = expiryLimit.Add(grace)
	}
	if !now.Before(expiryLimit) {
		// GetStale(key, 0) is the fresh-read path while stale fallback is
		// enabled. Keep the expired value for a later upstream-error read, but
		// remove it once a real grace-period lookup proves it unusable.
		if !allowStale || grace != 0 {
			c.removeElementLocked(el)
		}
		return EntrySnapshot{}, false
	}

	ent.lastAccess = now
	c.lru.MoveToFront(el)
	return ent.snapshot(now), true
}

// Delete removes one cached key.
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeLocked(key)
}

// Purge removes every cached entry.
func (c *Cache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.bytes = 0
}

// Close marks the cache closed and releases cached data.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	c.items = make(map[string]*list.Element)
	c.lru.Init()
	c.bytes = 0
}

// Stats reports aggregate retained cache state. Expired values may remain
// retained, within the configured memory bounds, so stale fallback can use
// them without an unrelated metrics read destroying the data.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return Stats{
		Entries:   len(c.items),
		Bytes:     c.bytes,
		MaxBytes:  c.maxBytes,
		Evictions: c.evictions,
	}
}

// Inspect returns metadata for a key without exposing its value.
func (c *Cache) Inspect(key string) (EntrySnapshot, bool) {
	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return EntrySnapshot{}, false
	}
	ent := el.Value.(*entry)
	if !now.Before(ent.expiresAt) {
		return EntrySnapshot{}, false
	}
	return ent.snapshot(now), true
}

func (c *Cache) enforceLimitsLocked() {
	for c.bytes > c.maxBytes || (c.maxEntries > 0 && len(c.items) > c.maxEntries) {
		oldest := c.lru.Back()
		if oldest == nil {
			return
		}
		c.removeElementLocked(oldest)
		c.evictions++
	}
}

func (c *Cache) removeLocked(key string) bool {
	el, ok := c.items[key]
	if !ok {
		return false
	}
	c.removeElementLocked(el)
	return true
}

func (c *Cache) removeElementLocked(el *list.Element) {
	ent := el.Value.(*entry)
	delete(c.items, ent.key)
	c.bytes -= ent.estimatedSize
	c.lru.Remove(el)
}

func (e *entry) snapshot(now time.Time) EntrySnapshot {
	ttl := e.expiresAt.Sub(now)
	if ttl < 0 {
		ttl = 0
	}
	return EntrySnapshot{
		Key:           e.key,
		Value:         append([]byte(nil), e.value...),
		InsertedAt:    e.insertedAt,
		ExpiresAt:     e.expiresAt,
		LastAccess:    e.lastAccess,
		EstimatedSize: e.estimatedSize,
		Age:           now.Sub(e.insertedAt),
		TTL:           ttl,
	}
}

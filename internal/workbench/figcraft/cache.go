package figcraft

import (
	"container/list"
	"encoding/json"
	"sync"
)

// cacheMaxBytes caps the in-memory cache (REQ-FV-008).
const cacheMaxBytes = 128 << 20

// cache is an LRU of request results counted by bytes (REQ-FV-008). PNG renders
// count their bytes; the others count their marshalled JSON. It is safe for
// concurrent use; build one with newCache.
type cache struct {
	mu    sync.Mutex
	max   int64
	total int64
	ll    *list.List
	items map[string]*list.Element
}

// cacheEntry is one cached value and the bytes it counts for.
type cacheEntry struct {
	key   string
	size  int64
	value any
}

// newCache builds an empty cache with the REQ-FV-008 capacity.
func newCache() *cache {
	return &cache{max: cacheMaxBytes, ll: list.New(), items: make(map[string]*list.Element)}
}

// get returns the value of key and moves it to the front. A missing key is not
// a hit.
func (c *cache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*cacheEntry).value, true
}

// put stores value under key counting size bytes, and evicts the least recently
// used entries until the total fits (REQ-FV-008).
func (c *cache) put(key string, value any, size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.total -= el.Value.(*cacheEntry).size
		c.ll.Remove(el)
		delete(c.items, key)
	}
	c.items[key] = c.ll.PushFront(&cacheEntry{key: key, size: size, value: value})
	c.total += size
	for c.total > c.max {
		back := c.ll.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*cacheEntry)
		c.ll.Remove(back)
		delete(c.items, entry.key)
		c.total -= entry.size
	}
}

// measure is how many bytes a cached value counts for: a render's PNG bytes,
// else the size of its marshalled JSON.
func measure(value any) int64 {
	if r, ok := value.(Render); ok {
		return int64(len(r.PNG))
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return int64(len(data))
}

// staleStore keeps the last good value per absolute path and request, whatever
// the fingerprint, so a failing run can fall back to it (REQ-FV-007). It is safe
// for concurrent use.
type staleStore struct {
	mu sync.Mutex
	m  map[string]any
}

// newStaleStore builds an empty store.
func newStaleStore() *staleStore {
	return &staleStore{m: make(map[string]any)}
}

// get returns the last good value of key.
func (s *staleStore) get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

// put stores value as the last good value of key.
func (s *staleStore) put(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
}

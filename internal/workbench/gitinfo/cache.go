package gitinfo

import (
	"sync"
	"time"
)

// lsFilesTTL is how long the ignored list and the tracked file list of a
// worktree are reused (REQ-WG-007, REQ-WG-008).
const lsFilesTTL = 10 * time.Second

// worktreesTTL is how long a worktree list of a repository is reused
// (REQ-WG-010).
const worktreesTTL = 30 * time.Second

// listCache caches a list of strings per key with a time to live. Its zero
// value is usable: an unset ttl falls back to lsFilesTTL. It is safe for
// concurrent use.
type listCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]listEntry
}

// listEntry is a cached list and the time it was stored.
type listEntry struct {
	values []string
	at     time.Time
}

// duration is the cache window: the set ttl, else lsFilesTTL.
func (c *listCache) duration() time.Duration {
	if c.ttl > 0 {
		return c.ttl
	}
	return lsFilesTTL
}

// get returns the cached values of key while they are younger than the window.
func (c *listCache) get(key string, now time.Time) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if now.Sub(entry.at) >= c.duration() {
		delete(c.entries, key)
		return nil, false
	}
	return entry.values, true
}

// put caches values for key at now.
func (c *listCache) put(key string, values []string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]listEntry)
	}
	c.entries[key] = listEntry{values: values, at: now}
}

// worktreesCache caches a worktree list per repository, base and root with a
// time to live. Its zero value is usable: an unset ttl falls back to
// worktreesTTL. It is safe for concurrent use.
type worktreesCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]worktreesEntry
}

// worktreesEntry is a cached list, the repository id it belongs to (so it can
// be invalidated per repository) and the time it was stored.
type worktreesEntry struct {
	repo string
	list WorktreeList
	at   time.Time
}

// duration is the cache window: the set ttl, else worktreesTTL.
func (c *worktreesCache) duration() time.Duration {
	if c.ttl > 0 {
		return c.ttl
	}
	return worktreesTTL
}

// get returns the cached list of key while it is younger than the window.
func (c *worktreesCache) get(key string, now time.Time) (WorktreeList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return WorktreeList{}, false
	}
	if now.Sub(entry.at) >= c.duration() {
		delete(c.entries, key)
		return WorktreeList{}, false
	}
	return entry.list, true
}

// put caches list for key, tagged with repo, at now.
func (c *worktreesCache) put(key, repo string, list WorktreeList, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]worktreesEntry)
	}
	c.entries[key] = worktreesEntry{repo: repo, list: list, at: now}
}

// invalidate drops every cached list that belongs to repo. An unknown repo
// drops nothing and is not an error.
func (c *worktreesCache) invalidate(repo string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if entry.repo == repo {
			delete(c.entries, key)
		}
	}
}

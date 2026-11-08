package router

import (
	"sync"
	"time"
)

// cooldowns keeps, in memory, the targets that are cooling down
// (REQ-RTR-006).
type cooldowns struct {
	mu    sync.Mutex
	until map[Target]time.Time
}

func (c *cooldowns) set(t Target, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.until[t]; !ok || until.After(prev) {
		c.until[t] = until
	}
}

func (c *cooldowns) clear(t Target) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.until, t)
}

// filter drops the targets cooling down at now. When every target is
// cooling down, it returns the full chain.
func (c *cooldowns) filter(chain []Target, now time.Time) []Target {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.until) == 0 {
		return chain
	}
	active := make([]Target, 0, len(chain))
	for _, t := range chain {
		if until, ok := c.until[t]; ok && now.Before(until) {
			continue
		}
		active = append(active, t)
	}
	if len(active) == 0 {
		return chain
	}
	return active
}

// active returns a copy of the cooldowns that have not ended at now and
// drops the ended ones.
func (c *cooldowns) active(now time.Time) map[Target]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[Target]time.Time{}
	for t, until := range c.until {
		if now.Before(until) {
			out[t] = until
		} else {
			delete(c.until, t)
		}
	}
	return out
}

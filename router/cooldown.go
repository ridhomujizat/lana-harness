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

// Key cooldowns of API key pools (REQ-PAF-005).
const (
	// keyCooldownRateLimit is a rate-limited key's cooldown when neither the
	// router cooldown nor the provider's Retry-After gives one.
	keyCooldownRateLimit = time.Minute
	// keyCooldownAuth is the cooldown of a key the provider refused.
	keyCooldownAuth = 5 * time.Minute
	// keyCooldownQuota is the cooldown of a key whose balance, credit, or
	// quota is used up.
	keyCooldownQuota = time.Hour
)

// keyCooldowns keeps, in memory, the API keys that are cooling down, keyed
// by keyID(provider, secret name) (REQ-PAF-005).
type keyCooldowns struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// keyID is the key of one API key of a provider in keyCooldowns.
func keyID(provider, env string) string { return provider + "\x00" + env }

func (c *keyCooldowns) set(provider, env string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := keyID(provider, env)
	if prev, ok := c.until[id]; !ok || until.After(prev) {
		c.until[id] = until
	}
}

func (c *keyCooldowns) clear(provider, env string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.until, keyID(provider, env))
}

// cooling reports whether the key is cooling down at now, and until when.
func (c *keyCooldowns) cooling(provider, env string, now time.Time) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.until[keyID(provider, env)]
	if !ok || !now.Before(until) {
		return time.Time{}, false
	}
	return until, true
}

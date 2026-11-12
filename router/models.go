package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/models"
)

const (
	// modelListTTL is how long a provider's model list is cached.
	modelListTTL = 24 * time.Hour
	// modelListErrorTTL is how long a failed model list fetch is remembered.
	modelListErrorTTL = 5 * time.Minute
	// modelRefreshTimeout bounds one background model-list refresh kicked by
	// CachedModel (REQ-MDL-010).
	modelRefreshTimeout = 20 * time.Second
	// providerThinking is the marker models.Model.Thinking carries for a
	// provider-listed model that reports thinking. Only its emptiness is
	// consulted (a thinking model reserves a thinking output budget), not its
	// value, which the catalog uses for static entries (REQ-MDL-011).
	providerThinking = "provider"
)

// ModelInfo is one model of a provider (API-RTR-009).
type ModelInfo struct {
	ID            string `json:"id"`
	Address       string `json:"address"`
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
	MaxOutput     int    `json:"max_output"`
	Vision        bool   `json:"vision"`
	Thinking      bool   `json:"thinking"`
	// Price is nil when unknown.
	Price *Price `json:"price"`
	// Source is "catalog", "provider", or "both".
	Source string `json:"source"`
}

type modelCacheEntry struct {
	entry  *providerEntry
	at     time.Time
	models []ModelInfo
	err    error
}

type modelCache struct {
	mu      sync.Mutex
	entries map[string]modelCacheEntry
	// refreshing holds the providers with a CachedModel background refresh in
	// flight, so at most one runs per provider (REQ-MDL-010).
	refreshing map[string]bool
}

// cachedPrice returns the price the provider's own model list gave for
// model, or nil.
func (c *modelCache) cachedPrice(entry *providerEntry, model string) *Price {
	if entry == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, ok := c.entries[entry.name]
	if !ok || cached.entry != entry {
		return nil
	}
	for _, m := range cached.models {
		if m.ID == model && m.Price != nil {
			p := *m.Price
			return &p
		}
	}
	return nil
}

// Models lists the models of a provider: the catalog models of its kind
// merged with the models its models endpoint returns, cached for 24 hours
// (REQ-RTR-013). refresh fetches the list again. When the fetch fails, the
// catalog models are returned with the error.
func (r *Router) Models(ctx context.Context, provider string, refresh bool) ([]ModelInfo, error) {
	if r.snap.Load().isClaude(provider) {
		return r.claudeCodeModels(provider), nil
	}
	entry, ok := r.snap.Load().providers[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAddress, provider)
	}
	listed, fetchErr := r.listModels(ctx, entry, refresh)
	var catalog []models.Model
	if entry.kind == "openai_compat" {
		catalog = models.ForProviderKind(entry.cfg.BaseURL)
	} else {
		catalog = models.ForKind(entry.kind)
	}
	out := make([]ModelInfo, 0, len(catalog)+len(listed))
	index := map[string]int{}
	for _, m := range catalog {
		info := fromCatalog(m)
		info.Address = provider + "/" + m.ID
		info.Source = "catalog"
		index[m.ID] = len(out)
		out = append(out, info)
	}
	for _, m := range listed {
		if m.ID == "" {
			continue
		}
		if i, ok := index[m.ID]; ok {
			merged := out[i]
			merged.Source = "both"
			if m.Price != nil {
				merged.Price = m.Price
			}
			if merged.ContextWindow == 0 {
				merged.ContextWindow = m.ContextWindow
			}
			if merged.MaxOutput == 0 {
				merged.MaxOutput = m.MaxOutput
			}
			if merged.Name == "" {
				merged.Name = m.Name
			}
			merged.Vision = merged.Vision || m.Vision
			merged.Thinking = merged.Thinking || m.Thinking
			out[i] = merged
			continue
		}
		info := m
		info.Address = provider + "/" + m.ID
		info.Source = "provider"
		if known, ok := models.Lookup(m.ID); ok {
			fill := fromCatalog(known)
			if info.Name == "" {
				info.Name = fill.Name
			}
			if info.ContextWindow == 0 {
				info.ContextWindow = fill.ContextWindow
			}
			if info.MaxOutput == 0 {
				info.MaxOutput = fill.MaxOutput
			}
			if info.Price == nil {
				info.Price = fill.Price
			}
			info.Vision = info.Vision || fill.Vision
			info.Thinking = info.Thinking || fill.Thinking
		}
		if info.Name == "" {
			info.Name = m.ID
		}
		index[m.ID] = len(out)
		out = append(out, info)
	}
	return out, fetchErr
}

// ModelsFetchedAt returns when the model list of provider was last fetched
// from its models endpoint (successfully or not); ok is false when it never
// was, or the provider's config changed since.
func (r *Router) ModelsFetchedAt(provider string) (time.Time, bool) {
	entry, ok := r.snap.Load().providers[provider]
	if !ok {
		return time.Time{}, false
	}
	r.models.mu.Lock()
	defer r.models.mu.Unlock()
	cached, ok := r.models.entries[provider]
	if !ok || cached.entry != entry {
		return time.Time{}, false
	}
	return cached.at, true
}

// CachedModel returns the model facts the provider's own model list gave for
// an address, without any network I/O (REQ-MDL-010). It is the seam
// internal/models registers as the catalog fallback, so a model the static
// catalog does not know still gets its real context window and max output.
//
// address is a "provider/model" address or a bare model id; the provider is
// the part before the FIRST "/" and the model id may itself contain "/"
// ("commandcode/deepseek/deepseek-v4.1-flash" is provider "commandcode").
// The provider's cached list is consulted first. When the address has no
// provider prefix, or the provider is not configured, the id is matched
// against every cached provider list (first hit). When a configured
// provider's list is missing or stale, a background refresh is kicked so the
// next lookup hits; CachedModel itself never blocks.
func (r *Router) CachedModel(address string) (models.Model, bool) {
	provider, id := splitModelAddress(address)
	snap := r.snap.Load()
	if entry, ok := snap.providers[provider]; ok && provider != "" {
		m, found := r.cachedModel(entry, id)
		r.refreshModelsAsync(entry)
		if found {
			return m, true
		}
		// Not in that provider's list. The prefix may be part of the model id
		// of another provider ("deepseek/deepseek-v4.1-flash" on commandcode
		// while a provider named "deepseek" exists), so scan the rest.
	}
	// No provider prefix, the provider is not configured, or it does not list
	// the model: match the id, then the whole address, against every cached
	// provider list, first hit.
	for _, name := range sortedKeys(snap.providers) {
		if m, ok := r.cachedModel(snap.providers[name], id); ok {
			return m, true
		}
		if id != address {
			if m, ok := r.cachedModel(snap.providers[name], address); ok {
				return m, true
			}
		}
	}
	return models.Model{}, false
}

// splitModelAddress splits a model address into its provider and model id.
// The provider is the part before the first "/"; a bare id has no provider.
func splitModelAddress(address string) (provider, id string) {
	i := strings.Index(address, "/")
	if i <= 0 || i == len(address)-1 {
		return "", address
	}
	return address[:i], address[i+1:]
}

// cachedModel returns the cached model-list entry of id within a provider's
// list. It reads the cache only, never the network; a missing entry returns
// false.
func (r *Router) cachedModel(entry *providerEntry, id string) (models.Model, bool) {
	if entry == nil || id == "" {
		return models.Model{}, false
	}
	c := &r.models
	c.mu.Lock()
	cached, ok := c.entries[entry.name]
	c.mu.Unlock()
	if !ok || cached.entry != entry {
		return models.Model{}, false
	}
	for _, m := range cached.models {
		if m.ID == id {
			return cachedModelInfo(m), true
		}
	}
	return models.Model{}, false
}

// cachedModelInfo converts a provider-listed model to a catalog entry
// (REQ-MDL-010): the provider's own context window, max output, name, vision,
// and thinking facts. Price is left to cachedPrice on the call path.
func cachedModelInfo(m ModelInfo) models.Model {
	out := models.Model{
		ID:            m.ID,
		Name:          m.Name,
		ContextWindow: m.ContextWindow,
		MaxOutput:     m.MaxOutput,
		Vision:        m.Vision,
	}
	if out.Name == "" {
		out.Name = m.ID
	}
	if m.Thinking {
		out.Thinking = providerThinking
	}
	return out
}

// refreshModelsAsync refreshes a provider's cached model list in the
// background when it is missing or stale, so the next CachedModel hits
// (REQ-MDL-010). At most one refresh runs per provider and it is bounded by
// modelRefreshTimeout; the caller never blocks and a fresh list is left
// alone.
func (r *Router) refreshModelsAsync(entry *providerEntry) {
	if entry == nil {
		return
	}
	now := r.now()
	c := &r.models
	c.mu.Lock()
	if c.refreshing == nil {
		c.refreshing = map[string]bool{}
	}
	if c.refreshing[entry.name] {
		c.mu.Unlock()
		return
	}
	if cached, ok := c.entries[entry.name]; ok && cached.entry == entry {
		ttl := modelListTTL
		if cached.err != nil {
			ttl = modelListErrorTTL
		}
		if now.Sub(cached.at) < ttl {
			c.mu.Unlock()
			return
		}
	}
	c.refreshing[entry.name] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.refreshing, entry.name)
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), modelRefreshTimeout)
		defer cancel()
		_, _ = r.listModels(ctx, entry, true)
	}()
}

// WarmModels fetches the model lists of the configured providers in the
// background, so the catalog fallback (CachedModel) can resolve model facts
// without ever blocking (REQ-MDL-010). Claude Code accounts are skipped:
// they are not in providers and their model list comes from the runtime. It
// returns at once; a nil ctx means the background context.
func (r *Router) WarmModels(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	snap := r.snap.Load()
	for _, name := range sortedKeys(snap.providers) {
		entry := snap.providers[name]
		if entry == nil {
			continue
		}
		go func(entry *providerEntry) {
			cctx, cancel := context.WithTimeout(ctx, modelRefreshTimeout)
			defer cancel()
			_, _ = r.listModels(cctx, entry, false)
		}(entry)
	}
}

func (r *Router) listModels(ctx context.Context, entry *providerEntry, refresh bool) ([]ModelInfo, error) {
	now := r.now()
	c := &r.models
	c.mu.Lock()
	cached, ok := c.entries[entry.name]
	c.mu.Unlock()
	if ok && cached.entry == entry && !refresh {
		ttl := modelListTTL
		if cached.err != nil {
			ttl = modelListErrorTTL
		}
		if now.Sub(cached.at) < ttl {
			return cached.models, cached.err
		}
	}
	listed, err := entry.adapter.ListModels(ctx)
	if errors.Is(err, ErrNotSupported) {
		listed, err = nil, nil
	}
	if err != nil {
		listed = nil
	}
	c.mu.Lock()
	c.entries[entry.name] = modelCacheEntry{entry: entry, at: now, models: listed, err: err}
	c.mu.Unlock()
	return listed, err
}

func fromCatalog(m models.Model) ModelInfo {
	info := ModelInfo{
		ID:            m.ID,
		Name:          m.Name,
		ContextWindow: m.ContextWindow,
		MaxOutput:     m.MaxOutput,
		Vision:        m.Vision,
		Thinking:      m.Thinking != "",
	}
	if m.Price != nil {
		p := *m.Price
		info.Price = &p
	}
	return info
}

// Package router routes LLM calls to providers: provider kinds,
// "provider/model" addressing, combos, retry and fallback, cooldown, call
// records, cost, limit snapshots, and model lists (ADR 0019). It imports
// only core, internal packages, and the standard library, so other Go
// services can embed it.
package router

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// DefaultCooldown is the cooldown used when Config.Cooldown is zero.
const DefaultCooldown = 5 * time.Minute

// Config is the router configuration (API-RTR-005).
type Config struct {
	Providers map[string]core.ProviderConfig
	Combos    map[string]Combo
	// Cooldown is how long a target that failed with rate_limit or auth is
	// skipped: 0 means DefaultCooldown, negative turns cooldown off.
	Cooldown time.Duration
	// BackgroundModel is the address or combo background passes use when an
	// agent's chain has only Claude Code targets (API-CCT-003).
	BackgroundModel string
	// ClaudeCodeModels are Claude Code model IDs the owner added by hand;
	// Models("claude-code") lists them with source "custom".
	ClaudeCodeModels []string
}

// Combo is a named, ordered list of model addresses (REQ-RTR-004).
type Combo struct {
	Description string
	Models      []string
}

// Target is one provider and model.
type Target = core.LLMTarget

// CallMeta describes who makes a call; it is copied into call records.
type CallMeta = core.LLMCallMeta

// Attempt is one failed attempt reported to the caller.
type Attempt = core.LLMAttempt

// Option configures New.
type Option func(*options)

type options struct {
	secrets    core.Secrets
	recorder   Recorder
	limitStore LimitStore
	httpClient *http.Client
	clock      func() time.Time
	sleep      func(context.Context, time.Duration) error
	jitter     func() float64
	providers  map[string]core.Provider
}

// WithSecrets sets the secret source of API keys.
func WithSecrets(s core.Secrets) Option { return func(o *options) { o.secrets = s } }

// WithRecorder sets the call recorder.
func WithRecorder(rec Recorder) Option { return func(o *options) { o.recorder = rec } }

// WithLimitStore sets the store that keeps limit snapshots across restarts.
func WithLimitStore(s LimitStore) Option { return func(o *options) { o.limitStore = s } }

// WithHTTPClient sets the HTTP client passed to adapters in Deps.
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.httpClient = c } }

// WithClock sets the clock (tests).
func WithClock(now func() time.Time) Option { return func(o *options) { o.clock = now } }

// WithSleep sets the backoff wait (tests).
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(o *options) { o.sleep = sleep }
}

// WithJitter sets the backoff jitter factor, normally random in [0.8, 1.2]
// (tests).
func WithJitter(jitter func() float64) Option { return func(o *options) { o.jitter = jitter } }

// WithProviders adds providers that are not built from a kind, for tests and
// embedders. A provider here wins over a Providers entry of the same name and
// survives Apply. A plain core.Provider is wrapped with WrapProvider.
func WithProviders(providers map[string]core.Provider) Option {
	return func(o *options) {
		if o.providers == nil {
			o.providers = map[string]core.Provider{}
		}
		for name, p := range providers {
			o.providers[name] = p
		}
	}
}

// Router routes LLM calls. It is safe for concurrent use.
type Router struct {
	opts    options
	applyMu sync.Mutex
	snap    atomic.Pointer[snapshot]
	// recorder is the call recorder: WithRecorder, or AttachStores later.
	recorder atomic.Pointer[recorderBox]
	cool     cooldowns
	limits   limitState
	models   modelCache
	// claudeModels is the model list Claude Code reported (nil: none yet).
	claudeModels atomic.Pointer[[]core.RuntimeModel]
}

type providerEntry struct {
	name string
	kind string
	cfg  core.ProviderConfig
	// raw is the provider as given (a custom provider before wrapping).
	raw     core.Provider
	adapter Adapter
	custom  bool
}

type snapshot struct {
	cfg       Config
	providers map[string]*providerEntry
	// claude are the Claude Code accounts of kind "claude-code" (REQ-CCT-010).
	// They have no adapter: their targets run in the Claude Code runtime.
	claude map[string]core.ProviderConfig
	combos map[string]Combo
	// cooldown is the normalized cooldown; 0 means off.
	cooldown time.Duration
}

// New builds a router from cfg.
func New(cfg Config, opts ...Option) (*Router, error) {
	r := &Router{}
	for _, opt := range opts {
		opt(&r.opts)
	}
	if r.opts.clock == nil {
		r.opts.clock = time.Now
	}
	if r.opts.jitter == nil {
		r.opts.jitter = randomJitter
	}
	r.cool.until = map[Target]time.Time{}
	r.recorder.Store(&recorderBox{rec: r.opts.recorder})
	r.limits.init(r.opts.limitStore)
	r.models.entries = map[string]modelCacheEntry{}
	next, err := r.build(cfg, nil)
	if err != nil {
		return nil, err
	}
	r.snap.Store(next)
	return r, nil
}

// Apply swaps in a new configuration atomically. Adapters are rebuilt only
// for providers whose config changed. Calls already running keep the
// snapshot they started with (REQ-RTR-008). On error the old configuration
// stays.
func (r *Router) Apply(cfg Config) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	next, err := r.build(cfg, r.snap.Load())
	if err != nil {
		return err
	}
	r.snap.Store(next)
	return nil
}

type recorderBox struct{ rec Recorder }

// AttachStores sets the call recorder and the limit store after New, for a
// host whose store opens after the router (lana boots the llm module before
// the session store). A nil argument keeps the current one. Snapshots the
// limit store holds are loaded under the ones already reported.
func (r *Router) AttachStores(rec Recorder, limits LimitStore) {
	if rec != nil {
		r.recorder.Store(&recorderBox{rec: rec})
	}
	if limits != nil {
		r.limits.attach(limits)
	}
}

// BackgroundModel returns [router] background_model (API-CCT-003). It
// implements llmcall.BackgroundModeler.
func (r *Router) BackgroundModel() string {
	return r.snap.Load().cfg.BackgroundModel
}

// Config returns the configuration currently applied.
func (r *Router) Config() Config {
	return r.snap.Load().cfg
}

// Close stops background work and flushes pending limit snapshots.
func (r *Router) Close() error {
	r.limits.close()
	return nil
}

func (r *Router) build(cfg Config, old *snapshot) (*snapshot, error) {
	next := &snapshot{
		cfg:       cloneConfig(cfg),
		providers: map[string]*providerEntry{},
		claude:    map[string]core.ProviderConfig{},
		combos:    map[string]Combo{},
	}
	// Work on the copy so a caller that changes its maps later cannot
	// change the snapshot or the change detection of the next Apply.
	cfg = next.cfg
	if _, ok := cfg.Providers[ClaudeCode]; ok {
		return nil, fmt.Errorf("providers.%s: claude-code is built in", ClaudeCode)
	}
	for name, p := range r.opts.providers {
		kind := "custom"
		if pc, ok := cfg.Providers[name]; ok && pc.Kind != "" {
			kind = pc.Kind
		}
		if old != nil {
			if prev, ok := old.providers[name]; ok && prev.custom && reflect.DeepEqual(prev.cfg, cfg.Providers[name]) {
				next.providers[name] = prev
				continue
			}
		}
		next.providers[name] = &providerEntry{
			name: name, kind: kind, cfg: cfg.Providers[name],
			raw: p, adapter: WrapProvider(p), custom: true,
		}
	}
	for _, name := range sortedKeys(cfg.Providers) {
		if _, custom := next.providers[name]; custom {
			continue
		}
		pc := cfg.Providers[name]
		if pc.Kind == core.ClaudeCodeKind {
			// REQ-CCT-010: a Claude Code account, run by the agent in the
			// Claude Code runtime.
			next.claude[name] = pc
			continue
		}
		if old != nil {
			if prev, ok := old.providers[name]; ok && !prev.custom && reflect.DeepEqual(prev.cfg, pc) {
				next.providers[name] = prev
				continue
			}
		}
		factory, ok := kindFactory(pc.Kind)
		if !ok {
			return nil, fmt.Errorf("provider '%s' kind '%s' is not supported in this build", name, pc.Kind)
		}
		adapter, err := factory(name, pc, r.deps(name, pc.Kind))
		if err != nil {
			return nil, err
		}
		if adapter == nil {
			return nil, fmt.Errorf("provider '%s' kind '%s' returned no adapter", name, pc.Kind)
		}
		next.providers[name] = &providerEntry{name: name, kind: pc.Kind, cfg: pc, raw: adapter, adapter: adapter}
	}
	for _, name := range sortedKeys(cfg.Combos) {
		combo := cfg.Combos[name]
		if err := validateCombo(name, combo, next, cfg.Combos); err != nil {
			return nil, err
		}
		next.combos[name] = Combo{Description: combo.Description, Models: append([]string(nil), combo.Models...)}
	}
	switch {
	case cfg.Cooldown == 0:
		next.cooldown = DefaultCooldown
	case cfg.Cooldown > 0:
		next.cooldown = cfg.Cooldown
	}
	return next, nil
}

func validateCombo(name string, combo Combo, snap *snapshot, combos map[string]Combo) error {
	providers := snap.providers
	path := "combos." + name
	switch {
	case name == "":
		return fmt.Errorf("combos: a combo name is empty")
	case strings.ContainsAny(name, "/:"):
		return fmt.Errorf("%s: a combo name must not contain '/' or ':'", path)
	case providers[name] != nil || snap.isClaude(name):
		return fmt.Errorf("%s: a combo name must not equal a provider name", path)
	case len(combo.Models) == 0:
		return fmt.Errorf("%s.models: a combo needs at least one model", path)
	}
	seen := map[string]bool{}
	for i, address := range combo.Models {
		item := fmt.Sprintf("%s.models[%d]", path, i)
		if _, isCombo := combos[address]; isCombo {
			return fmt.Errorf("%s: a combo must not list another combo (%q)", item, address)
		}
		target, ok := ParseAddress(address)
		if !ok || strings.IndexAny(address, ":/") != strings.Index(address, "/") {
			return fmt.Errorf("%s: %q is not a provider/model address", item, address)
		}
		if snap.isClaude(target.Provider) {
			if err := claudeCodeTarget(target); err != nil {
				return fmt.Errorf("%s: %w", item, err)
			}
		} else if providers[target.Provider] == nil {
			return fmt.Errorf("%s: unknown provider %q", item, target.Provider)
		}
		if seen[address] {
			return fmt.Errorf("%s: duplicate model %q", item, address)
		}
		seen[address] = true
	}
	return nil
}

func (r *Router) deps(name, kind string) Deps {
	return Deps{
		Secrets: r.opts.secrets,
		HTTP:    r.opts.httpClient,
		Limits: func(u LimitUpdate) {
			if u.Provider == "" {
				u.Provider = name
			}
			if u.Kind == "" {
				u.Kind = kind
			}
			r.UpdateLimits(u)
		},
	}
}

// Provider returns the provider registered under name (core.LLMRegistry).
// Calls made on it directly bypass retry, fallback, and call records.
func (r *Router) Provider(name string) (core.Provider, bool) {
	entry, ok := r.snap.Load().providers[name]
	if !ok {
		return nil, false
	}
	return entry.raw, true
}

// Names returns the provider names, sorted (core.LLMRegistry).
func (r *Router) Names() []string {
	return sortedKeys(r.snap.Load().providers)
}

// Disabled reports whether a configured provider (or Claude Code account) is
// switched off with disabled = true. An unknown provider is not disabled.
func (r *Router) Disabled(provider string) bool {
	snap := r.snap.Load()
	if pc, ok := snap.claude[provider]; ok {
		return pc.Disabled
	}
	entry, ok := snap.providers[provider]
	return ok && entry.cfg.Disabled
}

// Kind returns the kind of a provider ("custom" for one added with
// WithProviders and not in the config). A Claude Code account, the built-in
// one included, has kind "claude-code".
func (r *Router) Kind(provider string) (string, bool) {
	snap := r.snap.Load()
	if snap.isClaude(provider) {
		return core.ClaudeCodeKind, true
	}
	entry, ok := snap.providers[provider]
	if !ok {
		return "", false
	}
	return entry.kind, true
}

// Cooling returns the targets that are cooling down and until when.
func (r *Router) Cooling() map[Target]time.Time {
	return r.cool.active(r.now())
}

func (r *Router) now() time.Time { return r.opts.clock() }

func (r *Router) sleep(ctx context.Context, d time.Duration) error {
	if r.opts.sleep != nil {
		return r.opts.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// randomJitter returns a factor in [0.8, 1.2].
func randomJitter() float64 {
	return 0.8 + rand.Float64()*0.4 //nolint:gosec // backoff jitter is not security sensitive
}

func cloneConfig(cfg Config) Config {
	out := Config{Cooldown: cfg.Cooldown, BackgroundModel: cfg.BackgroundModel, ClaudeCodeModels: append([]string(nil), cfg.ClaudeCodeModels...)}
	if cfg.Providers != nil {
		out.Providers = make(map[string]core.ProviderConfig, len(cfg.Providers))
		for name, pc := range cfg.Providers {
			if pc.Headers != nil {
				headers := make(map[string]string, len(pc.Headers))
				for k, v := range pc.Headers {
					headers[k] = v
				}
				pc.Headers = headers
			}
			out.Providers[name] = pc
		}
	}
	if cfg.Combos != nil {
		out.Combos = make(map[string]Combo, len(cfg.Combos))
		for name, combo := range cfg.Combos {
			out.Combos[name] = Combo{Description: combo.Description, Models: append([]string(nil), combo.Models...)}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var (
	_ core.LLMRegistry = (*Router)(nil)
	_ core.LLMRouter   = (*Router)(nil)
)

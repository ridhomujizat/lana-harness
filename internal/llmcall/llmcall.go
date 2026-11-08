// Package llmcall builds the LLM chain of a lana agent and calls it through
// the router (spec 37, REQ-RTR-003, REQ-RTR-007). The agent packages use it
// so that turns, compaction, memory capture, knowledge passes, and embeddings
// resolve models and record calls the same way. It depends on core only: the
// router reaches it as core.LLMRouter.
package llmcall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// DefaultContextWindow is the context window of a chain whose models are not
// in the catalog.
const DefaultContextWindow = 128000

// Call sources of the router's call records (REQ-RTR-009).
const (
	SourceTurn          = "turn"
	SourceCompaction    = "compaction"
	SourceMemoryCapture = "memory_capture"
	SourceKnowledge     = "knowledge"
	SourceEmbedding     = "embedding"
)

// ErrUnknownModel is wrapped when an agent's model names neither a combo nor
// a model address with a known provider (AC-RTR-005).
var ErrUnknownModel = errors.New("unknown provider or combo")

// ErrNoBackgroundModel is returned by BackgroundChain when an agent's chain
// has no target outside Claude Code and [router] background_model is empty
// (REQ-CCT-007). The pass is skipped.
var ErrNoBackgroundModel = errors.New("no background model: the chain has only claude-code targets and [router] background_model is empty")

// ClaudeCode is the built-in provider of Claude Code targets (ADR 0020).
const ClaudeCode = core.ClaudeCodeProvider

// ClaudeCodeAliases are the model aliases of Claude Code targets, in picker
// order (API-CCT-004).
var ClaudeCodeAliases = []string{"opus", "sonnet", "haiku"}

// RuntimeProvider is the provider name of a call record for a turn of an
// external runtime: "claude-code" (API-CCT-008; spec 38 records used
// "claude-agent").
func RuntimeProvider(runtime string) string {
	return runtime
}

// IsClaudeCode reports whether a target runs in Claude Code (ADR 0020).
func IsClaudeCode(t core.LLMTarget) bool {
	return t.Provider == ClaudeCode
}

// IsClaudeCodeAddress reports whether value is a "claude-code/<model>"
// address.
func IsClaudeCodeAddress(value string) bool {
	t, ok := ParseTarget(value)
	return ok && IsClaudeCode(t) && value[len(ClaudeCode)] == '/'
}

// ValidClaudeCodeModel reports whether model may follow "claude-code/": a
// non-empty value that does not start with "-", so it is never read as a
// flag of the claude binary (REQ-CCT-001).
func ValidClaudeCodeModel(model string) bool {
	return model != "" && !strings.HasPrefix(model, "-") && strings.TrimSpace(model) == model
}

// WithoutClaudeCode returns chain without its Claude Code targets: the part
// lana's own loop and the background passes can call.
func WithoutClaudeCode(chain []core.LLMTarget) []core.LLMTarget {
	out := make([]core.LLMTarget, 0, len(chain))
	for _, t := range chain {
		if !IsClaudeCode(t) {
			out = append(out, t)
		}
	}
	return out
}

// HasClaudeCode reports whether chain has a Claude Code target.
func HasClaudeCode(chain []core.LLMTarget) bool {
	for _, t := range chain {
		if IsClaudeCode(t) {
			return true
		}
	}
	return false
}

// NormalizeAgent maps the deprecated engine key (API-CCT-002): runtime
// "claude-code" with model "M" becomes model "claude-code/M" ("default" when
// M is empty) with no provider and no runtime. Other agents are returned as
// they are.
func NormalizeAgent(cfg core.AgentConfig) core.AgentConfig {
	if cfg.Runtime != ClaudeCode {
		return cfg
	}
	model := cfg.Model
	if model == "" {
		model = "default"
	}
	if !IsClaudeCodeAddress(model) {
		model = ClaudeCode + "/" + model
	}
	cfg.Model = model
	cfg.Provider = ""
	cfg.Runtime = ""
	return cfg
}

// NormalizeAgents applies NormalizeAgent to every agent of a config map and
// returns a new map; nil stays nil.
func NormalizeAgents(agents map[string]core.AgentConfig) map[string]core.AgentConfig {
	if agents == nil {
		return nil
	}
	out := make(map[string]core.AgentConfig, len(agents))
	for name, cfg := range agents {
		out[name] = NormalizeAgent(cfg)
	}
	return out
}

// ParseTarget splits "provider/model" or the legacy "provider:model" at the
// first "/" or ":", whichever comes first. Both parts must be non-empty.
func ParseTarget(value string) (core.LLMTarget, bool) {
	i := strings.IndexAny(value, "/:")
	if i <= 0 || i == len(value)-1 {
		return core.LLMTarget{}, false
	}
	return core.LLMTarget{Provider: value[:i], Model: value[i+1:]}, true
}

// Address formats a target as "provider/model".
func Address(t core.LLMTarget) string {
	return t.Provider + "/" + t.Model
}

// Select applies a session's model/selection to an agent's provider and
// model. A selection with a model replaces both as a unit: provider "" with
// a combo or address in model is the router form; a provider with a model is
// the legacy provider:model form that old sessions stored.
func Select(provider, model string, sel core.ModelSelection) (string, string) {
	if sel.Model != "" {
		return sel.Provider, sel.Model
	}
	if sel.Provider != "" {
		provider = sel.Provider
	}
	return provider, model
}

// Requested is the value the router records as requested: the combo or
// address of a router-form model, or "provider/model" of the legacy form.
func Requested(provider, model string) string {
	if provider != "" {
		return provider + "/" + model
	}
	return model
}

// Display is the model of an agent as lana shows it and as a session model
// switch accepts it: "provider:model" for the legacy form, else the combo
// name or address as written.
func Display(provider, model string) string {
	switch {
	case provider != "" && model != "":
		return provider + ":" + model
	case provider == "":
		return model
	default:
		return ""
	}
}

// Chain builds the chain of a model setting (REQ-RTR-003): with provider set,
// [provider·model] followed by fallback; else the router resolves model (a
// combo is its list, an address one target) followed by fallback. When llm
// is not a router, a router-form model must be an address.
func Chain(llm core.LLMRegistry, provider, model string, fallback []string) ([]core.LLMTarget, error) {
	if model == "" {
		return nil, errors.New("no model set")
	}
	var chain []core.LLMTarget
	switch {
	case provider != "":
		chain = []core.LLMTarget{{Provider: provider, Model: model}}
	default:
		if r, ok := llm.(core.LLMRouter); ok {
			resolved, err := r.Resolve(model)
			if err != nil {
				return nil, fmt.Errorf("%w: %q", ErrUnknownModel, model)
			}
			chain = append(chain, resolved...)
		} else {
			target, ok := ParseTarget(model)
			if !ok || llm == nil {
				return nil, fmt.Errorf("%w: %q", ErrUnknownModel, model)
			}
			chain = []core.LLMTarget{target}
		}
	}
	for _, item := range fallback {
		target, ok := ParseTarget(item)
		if !ok {
			return nil, fmt.Errorf("invalid fallback: %s", item)
		}
		chain = append(chain, target)
	}
	return chain, nil
}

// AgentChain is the chain of an agent's configured model with its fallback,
// and the requested value for call records. A legacy runtime agent is read
// as its claude-code address (API-CCT-002). The chain may hold Claude Code
// targets; background passes use AgentBackgroundChain instead.
func AgentChain(llm core.LLMRegistry, cfg core.AgentConfig) ([]core.LLMTarget, string, error) {
	cfg = NormalizeAgent(cfg)
	chain, err := Chain(llm, cfg.Provider, cfg.Model, cfg.Fallback)
	if err != nil {
		return nil, "", err
	}
	return chain, Requested(cfg.Provider, cfg.Model), nil
}

// BackgroundModeler is implemented by a router that knows [router]
// background_model (API-CCT-003).
type BackgroundModeler interface {
	BackgroundModel() string
}

// BackgroundChain returns the chain a background pass (compaction, memory
// capture, knowledge passes) uses for an agent whose turn chain is chain
// (REQ-CCT-007): chain without its Claude Code targets, else [router]
// background_model resolved through llm, else ErrNoBackgroundModel. requested
// is kept when targets of chain remain, else it is the background model.
func BackgroundChain(llm core.LLMRegistry, chain []core.LLMTarget, requested string) ([]core.LLMTarget, string, error) {
	if !HasClaudeCode(chain) {
		return chain, requested, nil
	}
	if rest := WithoutClaudeCode(chain); len(rest) > 0 {
		if IsClaudeCodeAddress(requested) {
			requested = Address(rest[0])
		}
		return rest, requested, nil
	}
	background := ""
	if bm, ok := llm.(BackgroundModeler); ok {
		background = strings.TrimSpace(bm.BackgroundModel())
	}
	if background == "" {
		return nil, "", ErrNoBackgroundModel
	}
	resolved, err := Chain(llm, "", background, nil)
	if err != nil {
		return nil, "", fmt.Errorf("[router] background_model: %w", err)
	}
	resolved = WithoutClaudeCode(resolved)
	if len(resolved) == 0 {
		return nil, "", ErrNoBackgroundModel
	}
	return resolved, background, nil
}

// AgentBackgroundChain is BackgroundChain over the agent's configured chain.
func AgentBackgroundChain(llm core.LLMRegistry, cfg core.AgentConfig) ([]core.LLMTarget, string, error) {
	chain, requested, err := AgentChain(llm, cfg)
	if err != nil {
		return nil, "", err
	}
	return BackgroundChain(llm, chain, requested)
}

// backgroundWarnEvery is how often a skipped background pass is logged per
// agent (REQ-CCT-007).
const backgroundWarnEvery = time.Hour

var (
	backgroundWarnMu sync.Mutex
	backgroundWarned = map[string]time.Time{}
)

// WarnNoBackground logs that a background pass of agent was skipped because
// it has no model outside Claude Code, at most once per agent per hour
// (REQ-CCT-007). It reports whether it logged.
func WarnNoBackground(agent, pass string) bool {
	now := time.Now()
	backgroundWarnMu.Lock()
	last, ok := backgroundWarned[agent]
	if ok && now.Sub(last) < backgroundWarnEvery {
		backgroundWarnMu.Unlock()
		return false
	}
	backgroundWarned[agent] = now
	backgroundWarnMu.Unlock()
	slog.Warn("background pass skipped: the agent uses only Claude Code and [router] background_model is empty",
		"agent", agent, "pass", pass)
	return true
}

// Stream calls chain. When llm is a core.LLMRouter it goes through
// StreamChain, so retry, fallback, cooldown, and call records apply;
// otherwise the first target is called directly. It returns the target that
// answered.
func Stream(ctx context.Context, llm core.LLMRegistry, chain []core.LLMTarget, req core.ChatRequest, meta core.LLMCallMeta, onAttempt func(core.LLMAttempt)) (core.ChatStream, core.LLMTarget, error) {
	if len(chain) == 0 {
		return nil, core.LLMTarget{}, &core.ProviderError{Class: "client", Message: "provider target is empty"}
	}
	if llm == nil {
		return nil, core.LLMTarget{}, &core.ProviderError{Class: "client", Message: "no llm service"}
	}
	if r, ok := llm.(core.LLMRouter); ok {
		return r.StreamChain(ctx, chain, req, meta, onAttempt)
	}
	target := chain[0]
	provider, ok := llm.Provider(target.Provider)
	if !ok {
		return nil, core.LLMTarget{}, &core.ProviderError{Class: "client", Message: "unknown provider: " + target.Provider}
	}
	req.Model = target.Model
	stream, err := provider.Stream(ctx, req)
	if err != nil {
		return nil, core.LLMTarget{}, err
	}
	return stream, target, nil
}

// Lookup returns the catalog entry of the chain's first target.
func Lookup(chain []core.LLMTarget) (models.Model, bool) {
	if len(chain) == 0 {
		return models.Model{}, false
	}
	return models.Lookup(Address(chain[0]))
}

// ContextWindow is the context window of a chain: the agent's
// context_window when set, else the smallest catalog window across the
// chain, else DefaultContextWindow. The smallest window keeps a fallback
// from overflowing on a history the primary could hold.
func ContextWindow(agentWindow int, chain []core.LLMTarget) int {
	if agentWindow > 0 {
		return agentWindow
	}
	window := 0
	for _, target := range chain {
		known, ok := models.Lookup(Address(target))
		if !ok || known.ContextWindow <= 0 {
			continue
		}
		if window == 0 || known.ContextWindow < window {
			window = known.ContextWindow
		}
	}
	if window == 0 {
		return DefaultContextWindow
	}
	return window
}

// Embedder returns the embedder of provider. When llm is a router, the calls
// go through LLMRouter.Embed with source "embedding" so they are retried and
// recorded. ok is false when the provider is unknown or does not embed.
func Embedder(llm core.LLMRegistry, provider string) (core.Embedder, bool) {
	if llm == nil || provider == "" {
		return nil, false
	}
	p, ok := llm.Provider(provider)
	if !ok {
		return nil, false
	}
	direct, ok := p.(core.Embedder)
	if !ok {
		return nil, false
	}
	if r, ok := llm.(core.LLMRouter); ok {
		return routedEmbedder{router: r, provider: provider}, true
	}
	return direct, true
}

type routedEmbedder struct {
	router   core.LLMRouter
	provider string
}

func (e routedEmbedder) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	return e.router.Embed(ctx, e.provider+"/"+model, inputs, core.LLMCallMeta{Source: SourceEmbedding})
}

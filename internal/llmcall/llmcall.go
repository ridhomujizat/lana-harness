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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

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
	// SourceAvatar is a generated agent avatar (spec 43, REQ-AVT-002).
	SourceAvatar = "avatar"
)

// ErrUnknownModel is wrapped when an agent's model names neither a combo nor
// a model address with a known provider (AC-RTR-005).
var ErrUnknownModel = errors.New("unknown provider or combo")

// ErrNoBackgroundModel is returned by BackgroundChain when an agent's chain
// is empty and [router] background_model is empty (spec 78 REQ-RPR-009). The
// pass is skipped.
var ErrNoBackgroundModel = errors.New("no background model: the chain is empty and [router] background_model is empty")

// ClaudeCode is the built-in provider of Claude Code targets (ADR 0020).
const ClaudeCode = core.ClaudeCodeProvider

// Antigravity is the built-in provider of Antigravity targets (ADR 0022). The
// account runs agy with the daemon's own HOME and login.
const Antigravity = core.AntigravityKind

// ClaudeCodeAliases are the model aliases of Claude Code targets, in picker
// order (API-CCT-004).
var ClaudeCodeAliases = []string{"opus", "sonnet", "haiku"}

// RuntimeProvider is the provider name of a call record for a turn of an
// external runtime: "claude-code" (API-CCT-008; spec 38 records used
// "claude-agent").
func RuntimeProvider(runtime string) string {
	return runtime
}

// ClaudeCodeAccounts is implemented by an LLM registry (the router) that
// knows the Claude Code accounts (REQ-CCT-010): the built-in "claude-code"
// and every [providers.<name>] table of kind "claude-code".
type ClaudeCodeAccounts interface {
	// ClaudeCodeAccount returns the config of a Claude Code account. The
	// built-in account has an empty config with Kind set.
	ClaudeCodeAccount(name string) (core.ProviderConfig, bool)
}

// ClaudeCodeAccount returns the config of the Claude Code account name
// through llm. Without a registry that knows accounts, only the built-in
// "claude-code" is one.
func ClaudeCodeAccount(llm core.LLMRegistry, name string) (core.ProviderConfig, bool) {
	if a, ok := llm.(ClaudeCodeAccounts); ok {
		return a.ClaudeCodeAccount(name)
	}
	if name == ClaudeCode {
		return core.ProviderConfig{Kind: core.ClaudeCodeKind}, true
	}
	return core.ProviderConfig{}, false
}

// AntigravityAccounts is implemented by an LLM registry (the router) that
// knows the Antigravity accounts (REQ-AGY-001): the built-in "antigravity"
// and every [providers.<name>] table of kind "antigravity".
type AntigravityAccounts interface {
	// AntigravityAccount returns the config of an Antigravity account. The
	// built-in account has an empty config with Kind set.
	AntigravityAccount(name string) (core.ProviderConfig, bool)
}

// AntigravityAccount returns the config of the Antigravity account name
// through llm. Without a registry that knows accounts, only the built-in
// "antigravity" is one.
func AntigravityAccount(llm core.LLMRegistry, name string) (core.ProviderConfig, bool) {
	if a, ok := llm.(AntigravityAccounts); ok {
		return a.AntigravityAccount(name)
	}
	if name == Antigravity {
		return core.ProviderConfig{Kind: core.AntigravityKind}, true
	}
	return core.ProviderConfig{}, false
}

// IsClaudeCodeProvider reports whether name is a Claude Code account in a
// config's providers: the built-in "claude-code" or a table of kind
// "claude-code".
func IsClaudeCodeProvider(providers map[string]core.ProviderConfig, name string) bool {
	if name == ClaudeCode {
		return true
	}
	cfg, ok := providers[name]
	return ok && cfg.Kind == core.ClaudeCodeKind
}

// IsAntigravityProvider reports whether name is an Antigravity account in a
// config's providers: the built-in "antigravity" or a table of kind
// "antigravity".
func IsAntigravityProvider(providers map[string]core.ProviderConfig, name string) bool {
	if name == Antigravity {
		return true
	}
	cfg, ok := providers[name]
	return ok && cfg.Kind == core.AntigravityKind
}

// IsClaudeCode reports whether a target runs in Claude Code (ADR 0020): its
// provider is a Claude Code account of llm (REQ-CCT-010).
func IsClaudeCode(llm core.LLMRegistry, t core.LLMTarget) bool {
	_, ok := ClaudeCodeAccount(llm, t.Provider)
	return ok
}

// IsClaudeCodeAddress reports whether value is a "<account>/<model>"
// address of a Claude Code account of llm.
func IsClaudeCodeAddress(llm core.LLMRegistry, value string) bool {
	t, ok := ParseTarget(value)
	return ok && value[len(t.Provider)] == '/' && IsClaudeCode(llm, t)
}

// IsClaudeCodeAddressIn is IsClaudeCodeAddress over a config's providers.
func IsClaudeCodeAddressIn(providers map[string]core.ProviderConfig, value string) bool {
	t, ok := ParseTarget(value)
	return ok && value[len(t.Provider)] == '/' && IsClaudeCodeProvider(providers, t.Provider)
}

// ValidClaudeCodeModel reports whether model may follow "claude-code/": a
// non-empty value that does not start with "-", so it is never read as a
// flag of the claude binary (REQ-CCT-001).
func ValidClaudeCodeModel(model string) bool {
	return model != "" && !strings.HasPrefix(model, "-") && strings.TrimSpace(model) == model
}

// WithoutClaudeCode returns chain without its Claude Code targets: the part
// lana's own loop and the background passes can call.
func WithoutClaudeCode(llm core.LLMRegistry, chain []core.LLMTarget) []core.LLMTarget {
	out := make([]core.LLMTarget, 0, len(chain))
	for _, t := range chain {
		if !IsClaudeCode(llm, t) {
			out = append(out, t)
		}
	}
	return out
}

// HasClaudeCode reports whether chain has a Claude Code target.
func HasClaudeCode(llm core.LLMRegistry, chain []core.LLMTarget) bool {
	for _, t := range chain {
		if IsClaudeCode(llm, t) {
			return true
		}
	}
	return false
}

// ExternalRuntime returns the runtime of a provider when it is an external
// runtime (ADR 0020, ADR 0022): "claude-code" for the built-in claude-code or
// a provider of kind "claude-code", "antigravity" for the built-in
// antigravity or a provider of kind "antigravity". ok is false otherwise.
func ExternalRuntime(llm core.LLMRegistry, provider string) (string, bool) {
	if _, ok := ClaudeCodeAccount(llm, provider); ok {
		return ClaudeCode, true
	}
	if _, ok := AntigravityAccount(llm, provider); ok {
		return Antigravity, true
	}
	return "", false
}

// ExternalRuntimeIn is ExternalRuntime over a config's providers.
func ExternalRuntimeIn(providers map[string]core.ProviderConfig, name string) (string, bool) {
	switch {
	case IsClaudeCodeProvider(providers, name):
		return ClaudeCode, true
	case IsAntigravityProvider(providers, name):
		return Antigravity, true
	}
	return "", false
}

// IsExternal reports whether a target runs in an external runtime: its
// provider is a Claude Code or Antigravity account of llm (REQ-AGY-001).
func IsExternal(llm core.LLMRegistry, t core.LLMTarget) bool {
	_, ok := ExternalRuntime(llm, t.Provider)
	return ok
}

// IsExternalAddress reports whether value is a "<account>/<model>" address of
// a Claude Code or Antigravity account of llm.
func IsExternalAddress(llm core.LLMRegistry, value string) bool {
	t, ok := ParseTarget(value)
	return ok && value[len(t.Provider)] == '/' && IsExternal(llm, t)
}

// IsExternalAddressIn is IsExternalAddress over a config's providers.
func IsExternalAddressIn(providers map[string]core.ProviderConfig, value string) bool {
	t, ok := ParseTarget(value)
	if !ok || value[len(t.Provider)] != '/' {
		return false
	}
	_, ok = ExternalRuntimeIn(providers, t.Provider)
	return ok
}

// WithoutExternal returns chain without its external runtime targets: the part
// lana's own loop and the background passes can call.
func WithoutExternal(llm core.LLMRegistry, chain []core.LLMTarget) []core.LLMTarget {
	out := make([]core.LLMTarget, 0, len(chain))
	for _, t := range chain {
		if !IsExternal(llm, t) {
			out = append(out, t)
		}
	}
	return out
}

// HasExternal reports whether chain has a Claude Code or Antigravity target.
func HasExternal(llm core.LLMRegistry, chain []core.LLMTarget) bool {
	for _, t := range chain {
		if IsExternal(llm, t) {
			return true
		}
	}
	return false
}

// ValidAntigravityModel reports whether model may follow an Antigravity
// account: a non-empty value that does not start with "-" and has no
// whitespace anywhere (REQ-AGY-001).
func ValidAntigravityModel(model string) bool {
	return model != "" && !strings.HasPrefix(model, "-") && strings.IndexFunc(model, unicode.IsSpace) < 0
}

// ClaudeConfigDir returns the CLAUDE_CONFIG_DIR of the Claude Code account
// name (REQ-CCT-010): "" for the built-in account (the daemon's environment
// stays as it is), else config_dir with a leading ~ or $LANA_HOME expanded,
// or $LANA_HOME/claude/<name> when config_dir is empty. A relative result is
// taken from home. An empty home is $LANA_HOME, else ~/.lana (as
// config.HomeDir).
func ClaudeConfigDir(home, name string, cfg core.ProviderConfig) string {
	if name == ClaudeCode || name == "" {
		return ""
	}
	if home == "" {
		home = defaultHome()
	}
	dir := strings.TrimSpace(cfg.ConfigDir)
	if dir == "" {
		return filepath.Clean(filepath.Join(home, "claude", name))
	}
	return expandAccountDir(home, dir)
}

// expandAccountDir expands a leading $LANA_HOME (home) or ~ of an account
// folder and takes a relative result from home.
func expandAccountDir(home, dir string) string {
	for _, prefix := range []string{"$LANA_HOME", "${LANA_HOME}"} {
		if rest, ok := strings.CutPrefix(dir, prefix); ok && (rest == "" || rest[0] == '/') {
			dir = home + rest
			break
		}
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if userHome, err := os.UserHomeDir(); err == nil {
			dir = userHome + dir[1:]
		}
	}
	if !filepath.IsAbs(dir) && home != "" {
		dir = filepath.Join(home, dir)
	}
	return filepath.Clean(dir)
}

// AntigravityHomeDir returns the HOME of the Antigravity account name for a
// turn (REQ-AGY-001): "" for the built-in account (the daemon's own HOME),
// else home with a leading ~ or $LANA_HOME expanded, or
// $LANA_HOME/antigravity/<name> when home is empty. A relative result is taken
// from lanaHome. An empty lanaHome is $LANA_HOME, else ~/.lana.
func AntigravityHomeDir(lanaHome, name string, cfg core.ProviderConfig) string {
	if name == Antigravity || name == "" {
		return ""
	}
	if lanaHome == "" {
		lanaHome = defaultHome()
	}
	dir := strings.TrimSpace(cfg.Home)
	if dir == "" {
		return filepath.Clean(filepath.Join(lanaHome, "antigravity", name))
	}
	return expandAccountDir(lanaHome, dir)
}

// PoolAccounts is implemented by an LLM registry (the router) that knows the
// subscription pools of the external runtimes (spec 75, REQ-PAF-001).
type PoolAccounts interface {
	// PoolAccounts returns the accounts of runtime ("claude-code" or
	// "antigravity") a pooled address may use: enabled, pool not false,
	// sorted by priority then name, the built-in account included unless it
	// is left out.
	PoolAccounts(runtime string) []string
}

// Pool returns the targets a turn on t tries in order (REQ-PAF-001). For a
// pooled address (the built-in claude-code or antigravity provider) on a
// registry that knows pools, it is one target per pooled account of that
// runtime with t's model. A pinned account address, a target outside the
// external runtimes, an empty pool, or a registry without pools returns
// [t].
func Pool(llm core.LLMRegistry, t core.LLMTarget) []core.LLMTarget {
	if t.Provider != ClaudeCode && t.Provider != Antigravity {
		return []core.LLMTarget{t}
	}
	p, ok := llm.(PoolAccounts)
	if !ok {
		return []core.LLMTarget{t}
	}
	accounts := p.PoolAccounts(t.Provider)
	if len(accounts) == 0 {
		return []core.LLMTarget{t}
	}
	out := make([]core.LLMTarget, 0, len(accounts))
	seen := make(map[string]bool, len(accounts))
	for _, name := range accounts {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, core.LLMTarget{Provider: name, Model: t.Model})
	}
	if len(out) == 0 {
		return []core.LLMTarget{t}
	}
	return out
}

// AntigravityHome returns the HOME of the Antigravity account name
// (REQ-AGY-001): "" for the built-in account (it runs with the daemon's own
// HOME), else $LANA_HOME/antigravity/<name> (as config.HomeDir, else
// ~/.lana).
func AntigravityHome(name string) string {
	if name == Antigravity || name == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(defaultHome(), "antigravity", name))
}

// defaultHome is LANA_HOME, else ~/.lana.
func defaultHome() string {
	if v := strings.TrimSpace(os.Getenv("LANA_HOME")); v != "" {
		return v
	}
	if userHome, err := os.UserHomeDir(); err == nil {
		return filepath.Join(userHome, ".lana")
	}
	return ""
}

// ClaudeLoginCommand is the shell command that signs in a Claude Code account
// from a terminal (API-CCT-010, REQ-CCT-012), for a daemon on another machine:
// `CLAUDE_CONFIG_DIR='<dir>' CLAUDE_SECURESTORAGE_CONFIG_DIR='<dir>' claude
// auth login`, or `claude auth login` for the built-in account (dir ""). Both
// variables point at the folder, as in every claude lana runs for the
// account, so the login lands where lana looks for it.
func ClaudeLoginCommand(dir string) string {
	if dir == "" {
		return "claude auth login"
	}
	quoted := "'" + strings.ReplaceAll(dir, "'", `'\''`) + "'"
	return "CLAUDE_CONFIG_DIR=" + quoted + " CLAUDE_SECURESTORAGE_CONFIG_DIR=" + quoted + " claude auth login"
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
	if t, ok := ParseTarget(model); !ok || t.Provider != ClaudeCode || model[len(ClaudeCode)] != '/' {
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
// as its claude-code address (API-CCT-002). The chain may hold external
// runtime targets (Claude Code or Antigravity); background passes use
// AgentBackgroundChain instead.
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

// BackgroundChain returns the chain a background pass (titles, compaction,
// memory capture, knowledge passes, avatars) uses for an agent whose turn
// chain is chain (spec 78 REQ-RPR-009, superseding REQ-CCT-007 and
// REQ-AGY-008's "never in an external runtime"): chain as it is, Claude Code
// and Antigravity targets included, which the router runs through their
// CLIs; else, when chain is empty, [router] background_model resolved
// through llm; else ErrNoBackgroundModel. requested is kept when chain has
// targets, else it is the background model.
func BackgroundChain(llm core.LLMRegistry, chain []core.LLMTarget, requested string) ([]core.LLMTarget, string, error) {
	if len(chain) > 0 {
		return chain, requested, nil
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
	if len(resolved) == 0 {
		return nil, "", ErrNoBackgroundModel
	}
	return resolved, background, nil
}

// ToolBackgroundChain is the chain of a background pass that sends tool
// definitions or tool blocks (compaction, memory capture): the CLI runtimes
// serve text only (spec 78 REQ-RPR-003), so chain without its Claude Code and
// Antigravity targets; else [router] background_model without them; else
// ErrNoBackgroundModel. requested follows the first target kept.
func ToolBackgroundChain(llm core.LLMRegistry, chain []core.LLMTarget, requested string) ([]core.LLMTarget, string, error) {
	if !HasExternal(llm, chain) {
		return BackgroundChain(llm, chain, requested)
	}
	if rest := WithoutExternal(llm, chain); len(rest) > 0 {
		if IsExternalAddress(llm, requested) {
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
	resolved = WithoutExternal(llm, resolved)
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
// agent.
const backgroundWarnEvery = time.Hour

var (
	backgroundWarnMu sync.Mutex
	backgroundWarned = map[string]time.Time{}
)

// WarnNoBackground logs that a background pass of agent was skipped because
// it has no model at all (ErrNoBackgroundModel), at most once per agent per
// hour. It reports whether it logged.
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
	slog.Warn("background pass skipped: the agent uses only an external runtime and [router] background_model is empty",
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

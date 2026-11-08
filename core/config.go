package core

// DefaultWebAddr is the web view address used when [web] addr is absent.
const DefaultWebAddr = "127.0.0.1:18300"

// Config is the effective config. Secret values have type Secret.
type Config struct {
	Version   int                       `toml:"version"`
	Providers map[string]ProviderConfig `toml:"providers"`
	Agents    map[string]AgentConfig    `toml:"agents"`
	Teams     map[string]TeamConfig     `toml:"teams"`
	Routes    []RouteConfig             `toml:"routes"`
	MCP       map[string]MCPConfig      `toml:"mcp"`
	Channels  map[string]map[string]any `toml:"channels"`
	Modules   []ModuleRow               `toml:"modules"`
	Web       WebConfig                 `toml:"web"`
	Knowledge KnowledgeConfig           `toml:"knowledge"`
	Update    UpdateConfig              `toml:"update"`
	Combos    map[string]ComboConfig    `toml:"combos"`
	Router    RouterConfig              `toml:"router"`
}

// ComboConfig is one [combos.<name>] table: a named, ordered list of model
// addresses tried in order (API-RTR-002).
type ComboConfig struct {
	Description string   `toml:"description"`
	Models      []string `toml:"models"`
}

// RouterConfig is the [router] table (API-RTR-004).
type RouterConfig struct {
	// Proxy serves the OpenAI-compatible proxy when the web server runs.
	Proxy bool `toml:"proxy"`
	// Cooldown is a duration string; "" means 5m and "0s" turns cooldown off.
	Cooldown string `toml:"cooldown"`
	// BackgroundModel is the address or combo background passes use when an
	// agent's chain has no target outside Claude Code (API-CCT-003). "" means
	// none.
	BackgroundModel string `toml:"background_model"`
	// ClaudeCodeModels are model IDs the owner adds to the Claude Code model
	// list by hand, each usable as "claude-code/<id>".
	ClaudeCodeModels []string `toml:"claude_code_models"`
}

// KnowledgeConfig is the [knowledge] table (API-KNW-001, API-KNU-001).
// AutoRecall nil means on; a non-nil false turns per-turn knowledge recall off.
type KnowledgeConfig struct {
	Path              string `toml:"path"`
	AutoRecall        *bool  `toml:"auto_recall"`
	EmbeddingProvider string `toml:"embedding_provider"`
	EmbeddingModel    string `toml:"embedding_model"`
	// Agent names the agent that runs knowledge upkeep. "" picks the first
	// agent in name order with a knowledge tool (API-KNU-001).
	Agent string `toml:"agent"`
	// Journal turns the journal pass on or off. Nil means on (API-KNU-001).
	Journal *bool `toml:"journal"`
	// ConsolidateAt is the local HH:MM time of the nightly consolidation run.
	// Nil means "03:00"; "" turns nightly runs off (API-KNU-001).
	ConsolidateAt *string `toml:"consolidate_at"`
}

// UpdateConfig is the [update] table (API-UPD-001). config.Load fills the
// defaults: channel "stable" and check true.
type UpdateConfig struct {
	// Channel is "stable" or "beta".
	Channel string `toml:"channel"`
	// Check turns the periodic release check on.
	Check bool `toml:"check"`
}

// WebConfig is the [web] table.
type WebConfig struct {
	Enabled     bool   `toml:"enabled"`
	Addr        string `toml:"addr"`
	OpenBrowser bool   `toml:"open_browser"`
}

type ProviderConfig struct {
	Kind              string            `toml:"kind"`
	BaseURL           string            `toml:"base_url"`
	APIKeyEnv         string            `toml:"api_key_env"`
	Headers           map[string]string `toml:"headers"`
	Timeout           string            `toml:"timeout"`
	StreamIdleTimeout string            `toml:"stream_idle_timeout"`
	// ThinkingFormat is "deepseek" or "none"; "" means the format is inferred
	// from the base URL host (REQ-MDL-002).
	ThinkingFormat string `toml:"thinking_format"`
	// Disabled keeps the entry but takes it out of routing: its targets fail
	// with not_found so a chain moves on, and it is left out of model lists
	// and boot key checks (API-RTR-001 revision 2).
	Disabled bool `toml:"disabled"`
}

type AgentConfig struct {
	Provider      string   `toml:"provider"`
	Model         string   `toml:"model"`
	Fallback      []string `toml:"fallback"`
	PromptFile    string   `toml:"prompt_file"`
	Workspace     string   `toml:"workspace"`
	Tools         []string `toml:"tools"`
	DisabledTools []string `toml:"disabled_tools"`
	Subagents     []string `toml:"subagents"`
	MCP           []string `toml:"mcp"`
	Skills        []string `toml:"skills"`
	Permission    string   `toml:"permission"`
	MaxSteps      int      `toml:"max_steps"`
	ContextWindow int      `toml:"context_window"`
	// ReasoningEffort is "off", "low", "high", or "max"; "" uses the model's
	// catalog default (REQ-MDL-001).
	ReasoningEffort string `toml:"reasoning_effort"`
	// CompactionHeadroom, CompactionThreshold, and CompactionRetain tune the
	// context pressure policy (REQ-LTC-011). Zero means the default is used.
	CompactionHeadroom  int     `toml:"compaction_headroom"`
	CompactionThreshold float64 `toml:"compaction_threshold"`
	CompactionRetain    float64 `toml:"compaction_retain"`
	// MemoryCapture turns the background capture pass on or off. Nil means on
	// (REQ-MEM-010).
	MemoryCapture *bool `toml:"memory_capture"`
	// WorkspaceOnly keeps the file tools and the permission engine inside the
	// session workspace. False (the default) lets them reach any path.
	WorkspaceOnly bool `toml:"workspace_only"`
	// Vision turns image input on or off for the agent. Nil means the catalog
	// flag decides (API-ATT-005).
	Vision      *bool  `toml:"vision"`
	Description string `toml:"description"`
	// Title is the agent role shown on its profile, at most 60 characters
	// (REQ-ROOM-009, API-ROOM-006).
	Title string `toml:"title"`
	// Color is the avatar color, one of brand, success, warning, error, blue,
	// gray; "" derives a stable color from the name (REQ-ROOM-009, API-ROOM-006).
	Color string     `toml:"color"`
	Rules AgentRules `toml:"rules"`
	// Runtime is the deprecated engine key (API-CCT-002): "claude-code" with
	// model "M" means model "claude-code/M". config.Load rewrites it that way,
	// so the effective config leaves it empty.
	Runtime string `toml:"runtime"`
	// Auth is the runtime's credential mode: "subscription" (the default when
	// empty) or "api_key" (REQ-CCR-003).
	Auth string `toml:"auth"`
	// APIKeyEnv names the secret that holds the API key when Auth is
	// "api_key".
	APIKeyEnv string `toml:"api_key_env"`
	// Binary is the runtime executable. Empty looks it up on PATH.
	Binary string `toml:"binary"`
	// AllowScheduled lets calendar items and workflow nodes run the agent's
	// Claude Code targets (REQ-CCT-008). The default is false.
	AllowScheduled bool `toml:"allow_scheduled"`
}

type AgentRules struct {
	Allow []string `toml:"allow"`
	Deny  []string `toml:"deny"`
}

// TeamConfig is the [teams.<name>] table. Lead and members name agents.
type TeamConfig struct {
	Description string   `toml:"description"`
	Lead        string   `toml:"lead"`
	Members     []string `toml:"members"`
}

type RouteConfig struct {
	Channel string         `toml:"channel"`
	Agent   string         `toml:"agent"`
	Match   map[string]any `toml:"match"`
}

type MCPConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	URL     string            `toml:"url"`
	Env     map[string]Secret `toml:"env"`
	Headers map[string]Secret `toml:"headers"`
}

// ModuleRow is the effective [[modules]] row.
type ModuleRow struct {
	ID       string         `toml:"id"`
	Module   string         `toml:"module"`
	Disabled bool           `toml:"disabled"`
	Config   map[string]any `toml:"config"`
}

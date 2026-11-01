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
	// Vision turns image input on or off for the agent. Nil means the catalog
	// flag decides (API-ATT-005).
	Vision      *bool      `toml:"vision"`
	Description string     `toml:"description"`
	Rules       AgentRules `toml:"rules"`
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

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
	Enabled bool   `toml:"enabled"`
	Addr    string `toml:"addr"`
}

type ProviderConfig struct {
	Kind      string            `toml:"kind"`
	BaseURL   string            `toml:"base_url"`
	APIKeyEnv string            `toml:"api_key_env"`
	Headers   map[string]string `toml:"headers"`
	Timeout   string            `toml:"timeout"`
}

type AgentConfig struct {
	Provider      string     `toml:"provider"`
	Model         string     `toml:"model"`
	Fallback      []string   `toml:"fallback"`
	PromptFile    string     `toml:"prompt_file"`
	Workspace     string     `toml:"workspace"`
	Tools         []string   `toml:"tools"`
	DisabledTools []string   `toml:"disabled_tools"`
	Subagents     []string   `toml:"subagents"`
	MCP           []string   `toml:"mcp"`
	Skills        []string   `toml:"skills"`
	Permission    string     `toml:"permission"`
	MaxSteps      int        `toml:"max_steps"`
	ContextWindow int        `toml:"context_window"`
	Description   string     `toml:"description"`
	Rules         AgentRules `toml:"rules"`
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

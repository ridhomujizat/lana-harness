package core

// Config is the effective config. Secret values have type Secret.
type Config struct {
	Version   int                       `toml:"version"`
	Providers map[string]ProviderConfig `toml:"providers"`
	Agents    map[string]AgentConfig    `toml:"agents"`
	Routes    []RouteConfig             `toml:"routes"`
	MCP       map[string]MCPConfig      `toml:"mcp"`
	Channels  map[string]map[string]any `toml:"channels"`
	Modules   []ModuleRow               `toml:"modules"`
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
	Subagents     []string   `toml:"subagents"`
	MCP           []string   `toml:"mcp"`
	Skills        []string   `toml:"skills"`
	Permission    string     `toml:"permission"`
	MaxSteps      int        `toml:"max_steps"`
	ContextWindow int        `toml:"context_window"`
	Rules         AgentRules `toml:"rules"`
}

type AgentRules struct {
	Allow []string `toml:"allow"`
	Deny  []string `toml:"deny"`
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

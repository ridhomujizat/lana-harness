package config

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

var topKeys = []string{"agents", "channels", "combos", "knowledge", "mcp", "modules", "providers", "router", "routes", "teams", "update", "version", "web"}
var providerKeys = []string{"allow_scheduled", "api_key_env", "auth", "base_url", "binary", "config_dir", "disabled", "headers", "kind", "label", "stream_idle_timeout", "thinking_format", "timeout"}

// claudeAccountKeys are the keys of a [providers.<name>] table of kind
// "claude-code", a Claude Code account (API-CCT-010).
var claudeAccountKeys = []string{"allow_scheduled", "api_key_env", "auth", "binary", "config_dir", "disabled", "kind", "label"}

// builtinAccountRejectedKeys are the account keys the [providers.claude-code]
// table of the built-in account must not set (spec 39 revision 5,
// API-CCT-027): it always uses the owner's own Claude Code folder and has no
// label.
var builtinAccountRejectedKeys = []string{"config_dir", "label"}
var knowledgeKeys = []string{"agent", "auto_recall", "consolidate_at", "embedding_model", "embedding_provider", "journal", "path"}
var agentKeys = []string{"allow_scheduled", "api_key_env", "auth", "binary", "color", "compaction_headroom", "compaction_retain", "compaction_threshold", "context_window", "description", "disabled_tools", "display_name", "fallback", "max_steps", "mcp", "memory_capture", "model", "permission", "prompt_file", "provider", "reasoning_effort", "rules", "runtime", "skill_write", "skills", "subagents", "title", "tools", "vision", "workspace", "workspace_only"}

// claudeCodeRuntime is the runtime key of a Claude Agent (ADR 0018,
// API-CCR-001).
const claudeCodeRuntime = "claude-code"

// runtimeForbiddenKeys are the agent keys a legacy runtime = "claude-code"
// agent may not set: its model is the Claude Code address (API-CCT-002).
var runtimeForbiddenKeys = []string{"provider"}

// runtimeAuthModes are the accepted agents.<name>.auth values.
var runtimeAuthModes = []string{core.RuntimeAuthAPIKey, core.RuntimeAuthSubscription}

// runtimeEfforts are the reasoning_effort values Claude Code accepts, passed
// to --effort (API-CCR-001). An agent whose model is a claude-code address
// may use them and the native values.
var runtimeEfforts = []string{"high", "low", "max", "medium", "xhigh"}

// nativeEfforts are the reasoning_effort values of a lana agent (REQ-MDL-001).
var nativeEfforts = []string{"off", "low", "high", "max"}

// runtimeNames are the accepted agents.<name>.runtime values: claude-code and
// any runtime registered with core.RegisterRuntime.
func runtimeNames() []string {
	names := []string{claudeCodeRuntime}
	for _, name := range core.RuntimeNames() {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

var teamKeys = []string{"description", "discuss", "extra_rounds", "lead", "learn", "members", "room_memory", "rounds", "share_summary", "telegram_all"}
var routeKeys = []string{"agent", "channel", "match"}
var mcpKeys = []string{"args", "command", "env", "headers", "url"}
var moduleKeys = []string{"after", "before", "config", "disabled", "id", "module"}
var webKeys = []string{"addr", "enabled", "open_browser"}

// providerKinds are the accepted providers.<name>.kind values (API-RTR-001).
var providerKinds = []string{"anthropic", core.ClaudeCodeKind, "deepseek", "openai", "openai_compat", "openrouter"}

// embeddingKinds are the provider kinds that can serve knowledge embeddings
// (REQ-RTR-007).
var embeddingKinds = []string{"deepseek", "openai", "openai_compat", "openrouter"}

// comboKeys are the keys of [combos.<name>] (API-RTR-002).
var comboKeys = []string{"description", "models"}

// routerKeys are the keys of [router] (API-RTR-004).
var routerKeys = []string{"background_model", "claude_code_models", "cooldown", "proxy"}

// maxClaudeCodeModels is the longest accepted [router] claude_code_models.
const maxClaudeCodeModels = 50

// maxComboModels is the longest accepted combos.<name>.models list.
const maxComboModels = 10

// maxRouterCooldown is the longest accepted [router] cooldown.
const maxRouterCooldown = time.Hour

// comboNameRE is the accepted combo name (API-RTR-002).
var comboNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// updateKeys are the keys of [update] (API-UPD-001).
var updateKeys = []string{"channel", "check"}

// maxAgentDescription is the longest accepted agents.<name>.description, in
// characters (runes).
const maxAgentDescription = 120

// maxAgentTitle is the longest accepted agents.<name>.title, in characters
// (runes) (REQ-ROOM-009, API-ROOM-006).
const maxAgentTitle = 60

// maxAgentDisplayName is the longest accepted agents.<name>.display_name, in
// characters (runes).
const maxAgentDisplayName = 40

// maxTeamDescription is the longest accepted teams.<name>.description, in
// characters (runes).
const maxTeamDescription = 120

// maxTeamMembers is the largest accepted teams.<name>.members list.
const maxTeamMembers = 8

func validateLayer(ld layerData, file string, modules []string) []Error {
	var errs []Error
	m := ld.value
	at := func(key string) pos { return ld.positions[key] }
	add := func(key, msg string) {
		p := at(key)
		errs = append(errs, Error{File: file, Line: p.Line, Col: p.Col, Key: key, Msg: msg})
	}
	for _, key := range sortedKeys(m) {
		if !contains(topKeys, key) {
			add(key, unknownKeyMsg(key, topKeys))
		}
	}
	if v, ok := m["version"]; ok {
		n, ok := asInt(v)
		if !ok {
			add("version", typeMsg("integer", v))
		} else if n > maxSupportedVersion {
			add("version", fmt.Sprintf("config version %d is higher than the supported version (%d)", n, maxSupportedVersion))
		}
	}
	if raw, ok := m["providers"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("providers", typeMsg("table", raw))
		} else {
			for _, name := range sortedKeys(table) {
				path := "providers." + name
				val, ok := asMap(table[name])
				if !ok {
					add(path, typeMsg("table", table[name]))
					continue
				}
				validateProvider(val, path, add)
			}
		}
	}
	if raw, ok := m["agents"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("agents", typeMsg("table", raw))
		} else {
			for _, name := range sortedKeys(table) {
				path := "agents." + name
				val, ok := asMap(table[name])
				if !ok {
					add(path, typeMsg("table", table[name]))
					continue
				}
				validateAgent(val, path, add)
			}
		}
	}
	if raw, ok := m["teams"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("teams", typeMsg("table", raw))
		} else {
			for _, name := range sortedKeys(table) {
				path := "teams." + name
				if !agenthome.ValidName(name) {
					add(path, "name must match [a-z][a-z0-9_-]{0,31}")
				}
				val, ok := asMap(table[name])
				if !ok {
					add(path, typeMsg("table", table[name]))
					continue
				}
				validateTeam(val, path, add)
			}
		}
	}
	if raw, ok := m["routes"]; ok {
		list, ok := asSlice(raw)
		if !ok {
			add("routes", typeMsg("array", raw))
		} else {
			for i, item := range list {
				path := fmt.Sprintf("routes.%d", i)
				val, ok := asMap(item)
				if !ok {
					add(path, typeMsg("table", item))
					continue
				}
				validateRoute(val, path, add)
			}
		}
	}
	if raw, ok := m["mcp"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("mcp", typeMsg("table", raw))
		} else {
			for _, name := range sortedKeys(table) {
				path := "mcp." + name
				val, ok := asMap(table[name])
				if !ok {
					add(path, typeMsg("table", table[name]))
					continue
				}
				validateMCP(val, path, add)
			}
		}
	}
	if raw, ok := m["channels"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("channels", typeMsg("table", raw))
		} else {
			for _, name := range sortedKeys(table) {
				if _, ok := asMap(table[name]); !ok {
					add("channels."+name, typeMsg("table", table[name]))
				}
			}
		}
	}
	if raw, ok := m["modules"]; ok {
		list, ok := asSlice(raw)
		if !ok {
			add("modules", typeMsg("array", raw))
		} else {
			seen := map[string]int{}
			for i, item := range list {
				path := "modules." + strconv.Itoa(i)
				val, ok := asMap(item)
				if !ok {
					add(path, typeMsg("table", item))
					continue
				}
				if id, ok := val["id"].(string); ok && id != "" {
					if first, dup := seen[id]; dup {
						add(path+".id", fmt.Sprintf("id '%s' is already used on line %d", id, first))
					} else {
						p := at(path)
						seen[id] = p.Line
					}
				}
				validateModuleRow(val, path, modules, add)
			}
		}
	}
	if raw, ok := m["web"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("web", typeMsg("table", raw))
		} else {
			validateWeb(table, "web", add)
		}
	}
	if raw, ok := m["knowledge"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("knowledge", typeMsg("table", raw))
		} else {
			validateKnowledge(table, "knowledge", add)
		}
	}
	if raw, ok := m["update"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("update", typeMsg("table", raw))
		} else {
			validateUpdate(table, "update", add)
		}
	}
	if raw, ok := m["combos"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("combos", typeMsg("table", raw))
		} else {
			for _, name := range sortedKeys(table) {
				path := "combos." + name
				if !comboNameRE.MatchString(name) {
					add(path, "name must match [a-z0-9][a-z0-9_-]{0,63}")
				}
				val, ok := asMap(table[name])
				if !ok {
					add(path, typeMsg("table", table[name]))
					continue
				}
				validateCombo(val, path, add)
			}
		}
	}
	if raw, ok := m["router"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add("router", typeMsg("table", raw))
		} else {
			validateRouter(table, "router", add)
		}
	}
	return errs
}

// validateCombo checks the keys and types of one [combos.<name>] table
// (API-RTR-002). References to providers and combos are checked in
// validateEffective.
func validateCombo(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(comboKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, comboKeys))
		}
	}
	if val, ok := v["description"]; ok {
		if _, ok := val.(string); !ok {
			add(path+".description", typeMsg("string", val))
		}
	}
	if val, ok := v["models"]; ok {
		if _, ok := asStringSlice(val); !ok {
			add(path+".models", typeMsg("array string", val))
		}
	}
}

// validateRouter checks the [router] table (API-RTR-004): proxy is a
// boolean, cooldown a duration from 0s to 1h.
func validateRouter(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(routerKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, routerKeys))
		}
	}
	if val, ok := v["proxy"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".proxy", typeMsg("boolean", val))
		}
	}
	if val, ok := v["background_model"]; ok {
		if _, ok := val.(string); !ok {
			add(path+".background_model", typeMsg("string", val))
		}
	}
	if val, ok := v["claude_code_models"]; ok {
		list, ok := asStringSlice(val)
		if !ok {
			add(path+".claude_code_models", typeMsg("array string", val))
		} else {
			if len(list) > maxClaudeCodeModels {
				add(path+".claude_code_models", fmt.Sprintf("must list at most %d models", maxClaudeCodeModels))
			}
			seen := map[string]bool{}
			for i, id := range list {
				itemPath := fmt.Sprintf("%s.claude_code_models[%d]", path, i)
				switch {
				case id == "" || strings.TrimSpace(id) != id || strings.ContainsAny(id, " \t\r\n"):
					add(itemPath, fmt.Sprintf("invalid model ID %q", id))
				case strings.HasPrefix(id, "-"):
					add(itemPath, "a claude-code model must not start with '-'")
				case seen[id]:
					add(itemPath, fmt.Sprintf("model %q is listed twice", id))
				}
				seen[id] = true
			}
		}
	}
	if val, ok := v["cooldown"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".cooldown", typeMsg("string", val))
			return
		}
		d, err := time.ParseDuration(s)
		switch {
		case err != nil:
			add(path+".cooldown", "invalid duration: "+s)
		case d < 0:
			add(path+".cooldown", "must not be negative, got "+s)
		case d > maxRouterCooldown:
			add(path+".cooldown", "must be at most 1h, got "+s)
		}
	}
}

func validateProvider(v map[string]any, path string, add func(string, string)) {
	kindValue, _ := v["kind"].(string)
	claude := kindValue == core.ClaudeCodeKind
	for _, key := range sortedKeys(v) {
		switch {
		case !contains(providerKeys, key):
			add(path+"."+key, unknownKeyMsg(key, providerKeys))
		case claude && !contains(claudeAccountKeys, key):
			add(path+"."+key, "not allowed for kind claude-code")
		case !claude && kindValue != "" && contains(claudeAccountKeys, key) && !contains([]string{"api_key_env", "disabled", "kind"}, key):
			add(path+"."+key, "allowed only for kind claude-code")
		}
	}
	if claude {
		validateClaudeAccount(v, path, add)
	}
	kind, ok := v["kind"]
	if !ok {
		add(path+".kind", "is required")
	} else if s, ok := kind.(string); !ok {
		add(path+".kind", typeMsg("string", kind))
	} else if !contains(providerKinds, s) {
		add(path+".kind", enumMsg(s, providerKinds))
	}
	for _, key := range []string{"base_url", "api_key_env"} {
		if val, ok := v[key]; ok {
			if _, ok := val.(string); !ok {
				add(path+"."+key, typeMsg("string", val))
			}
		}
	}
	if val, ok := v["headers"]; ok {
		table, ok := asMap(val)
		if !ok {
			add(path+".headers", typeMsg("table", val))
		} else {
			for _, key := range sortedKeys(table) {
				if _, ok := table[key].(string); !ok {
					add(path+".headers."+key, typeMsg("string", table[key]))
				}
			}
		}
	}
	if val, ok := v["disabled"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".disabled", typeMsg("boolean", val))
		}
	}
	if val, ok := v["thinking_format"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".thinking_format", typeMsg("string", val))
		} else if s != "" && !contains([]string{"deepseek", "none"}, s) {
			add(path+".thinking_format", enumMsg(s, []string{"deepseek", "none"}))
		}
	}
	for _, key := range []string{"timeout", "stream_idle_timeout"} {
		if val, ok := v[key]; ok {
			s, ok := val.(string)
			if !ok {
				add(path+"."+key, typeMsg("string", val))
			} else if d, err := time.ParseDuration(s); err != nil {
				add(path+"."+key, "invalid duration: "+s)
			} else if d <= 0 {
				add(path+"."+key, "must be greater than 0, got "+s)
			}
		}
	}
}

// validateClaudeAccount checks the Claude Code keys of a provider of kind
// "claude-code" (API-CCT-010).
func validateClaudeAccount(v map[string]any, path string, add func(string, string)) {
	for _, key := range []string{"auth", "binary", "config_dir"} {
		if val, ok := v[key]; ok {
			if _, ok := val.(string); !ok {
				add(path+"."+key, typeMsg("string", val))
			}
		}
	}
	if val, ok := v["allow_scheduled"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".allow_scheduled", typeMsg("boolean", val))
		}
	}
	auth, _ := v["auth"].(string)
	if auth != "" && !contains(runtimeAuthModes, auth) {
		add(path+".auth", enumMsg(auth, runtimeAuthModes))
	}
	if keyEnv, _ := v["api_key_env"].(string); auth == core.RuntimeAuthAPIKey && keyEnv == "" {
		add(path+".api_key_env", "required when auth is api_key")
	}
	if dir, ok := v["config_dir"].(string); ok && dir != "" {
		switch {
		case strings.ContainsAny(dir, "\x00\r\n"):
			add(path+".config_dir", "must be one line")
		case !strings.HasPrefix(dir, "/") && dir != "~" && !strings.HasPrefix(dir, "~/") &&
			!strings.HasPrefix(dir, "$LANA_HOME") && !strings.HasPrefix(dir, "${LANA_HOME}"):
			add(path+".config_dir", "must be an absolute path, or start with ~/ or $LANA_HOME")
		}
	}
	if bin, ok := v["binary"].(string); ok && strings.HasPrefix(bin, "-") {
		add(path+".binary", "must not start with '-'")
	}
	// REQ-CCT-012: the display label.
	if val, ok := v["label"]; ok {
		label, ok := val.(string)
		switch {
		case !ok:
			add(path+".label", typeMsg("string", val))
		case strings.ContainsAny(label, "\x00\r\n"):
			add(path+".label", "must be one line")
		case utf8.RuneCountInString(label) > MaxClaudeAccountLabel:
			add(path+".label", fmt.Sprintf("must be at most %d characters (got %d)", MaxClaudeAccountLabel, utf8.RuneCountInString(label)))
		}
	}
}

// MaxClaudeAccountLabel is the longest label of a Claude Code account
// (REQ-CCT-012).
const MaxClaudeAccountLabel = 60

func validateAgent(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(agentKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, agentKeys))
		}
	}
	for _, key := range []string{"provider", "model", "prompt_file", "workspace", "permission", "runtime", "auth", "api_key_env", "binary"} {
		if val, ok := v[key]; ok {
			if _, ok := val.(string); !ok {
				add(path+"."+key, typeMsg("string", val))
			}
		}
	}
	if val, ok := v["allow_scheduled"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".allow_scheduled", typeMsg("boolean", val))
		}
	}
	for _, key := range []string{"fallback", "tools", "subagents", "mcp", "skills"} {
		if val, ok := v[key]; ok {
			if _, ok := asStringSlice(val); !ok {
				add(path+"."+key, typeMsg("array string", val))
			}
		}
	}
	if val, ok := v["description"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".description", typeMsg("string", val))
		} else {
			if strings.ContainsAny(s, "\n\r") {
				add(path+".description", "must be one line")
			}
			if n := utf8.RuneCountInString(s); n > maxAgentDescription {
				add(path+".description", fmt.Sprintf("must be at most %d characters (got %d)", maxAgentDescription, n))
			}
		}
	}
	if val, ok := v["title"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".title", typeMsg("string", val))
		} else {
			if strings.ContainsAny(s, "\n\r") {
				add(path+".title", "must be one line")
			}
			if n := utf8.RuneCountInString(s); n > maxAgentTitle {
				add(path+".title", fmt.Sprintf("must be at most %d characters (got %d)", maxAgentTitle, n))
			}
		}
	}
	if val, ok := v["display_name"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".display_name", typeMsg("string", val))
		} else {
			if strings.ContainsAny(s, "\n\r") {
				add(path+".display_name", "must be one line")
			}
			if n := utf8.RuneCountInString(s); n > maxAgentDisplayName {
				add(path+".display_name", fmt.Sprintf("must be at most %d characters (got %d)", maxAgentDisplayName, n))
			}
		}
	}
	if val, ok := v["color"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".color", typeMsg("string", val))
		} else if !core.ValidAgentColor(s) {
			// A palette token or a #RRGGBB hex color (API-ROOM-006, spec 43).
			add(path+".color", fmt.Sprintf("%q is not valid: %s", s, core.AgentColorMessage))
		}
	}
	if val, ok := v["disabled_tools"]; ok {
		list, ok := asStringSlice(val)
		if !ok {
			add(path+".disabled_tools", typeMsg("array string", val))
		} else {
			for _, item := range list {
				if !validToolRef(item) {
					add(path+".disabled_tools", fmt.Sprintf("invalid tool '%s': want <tool> or <mcp_server>/<tool>", item))
				}
			}
		}
	}
	for _, key := range []string{"max_steps", "context_window"} {
		if val, ok := v[key]; ok {
			n, isInt := asInt(val)
			if !isInt {
				add(path+"."+key, typeMsg("integer", val))
				continue
			}
			// REQ-LTC-040: max_steps is opt-in; 0 means no limit and there is
			// no upper bound.
			if key == "max_steps" && n < 0 {
				add(path+"."+key, "must be 0 (no limit) or greater")
			}
			if key == "context_window" && n <= 0 {
				add(path+"."+key, "must be greater than 0")
			}
		}
	}
	if val, ok := v["compaction_headroom"]; ok {
		n, isInt := asInt(val)
		if !isInt {
			add(path+".compaction_headroom", typeMsg("integer", val))
		} else if n <= 0 {
			add(path+".compaction_headroom", "must be greater than 0")
		}
	}
	for _, key := range []string{"compaction_threshold", "compaction_retain"} {
		val, ok := v[key]
		if !ok {
			continue
		}
		f, isNum := asFloat(val)
		if !isNum {
			add(path+"."+key, typeMsg("float", val))
		} else if f <= 0 || f >= 1 {
			add(path+"."+key, "must be between 0 and 1")
		}
	}
	if val, ok := v["memory_capture"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".memory_capture", typeMsg("boolean", val))
		}
	}
	if val, ok := v["workspace_only"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".workspace_only", typeMsg("boolean", val))
		}
	}
	if val, ok := v["vision"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".vision", typeMsg("boolean", val))
		}
	}
	if val, ok := v["permission"]; ok {
		if s, ok := val.(string); ok && !contains([]string{"ask", "never", "trust"}, s) {
			add(path+".permission", enumMsg(s, []string{"ask", "never", "trust"}))
		}
	}
	if val, ok := v["reasoning_effort"]; ok {
		s, ok := val.(string)
		// A Claude Code agent's effort values differ; validateEffective
		// checks an address model against the merged model, runtime, and
		// providers (API-CCR-001, REQ-CCT-010).
		runtime, _ := v["runtime"].(string)
		model, _ := v["model"].(string)
		if !ok {
			add(path+".reasoning_effort", typeMsg("string", val))
		} else if runtime == "" && !isAddress(model) && s != "" && !contains(nativeEfforts, s) {
			add(path+".reasoning_effort", enumMsg(s, nativeEfforts))
		}
	}
	// API-DKN-002: skill_write is "approve" (the default when empty) or
	// "direct" (REQ-DKN-050).
	if val, ok := v["skill_write"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".skill_write", typeMsg("string", val))
		} else if s != "" && s != "approve" && s != "direct" {
			add(path+".skill_write", `want "approve" or "direct"`)
		}
	}
	if raw, ok := v["rules"]; ok {
		table, ok := asMap(raw)
		if !ok {
			add(path+".rules", typeMsg("table", raw))
		} else {
			for _, key := range sortedKeys(table) {
				if key != "allow" && key != "deny" {
					add(path+".rules."+key, unknownKeyMsg(key, []string{"allow", "deny"}))
					continue
				}
				list, ok := asStringSlice(table[key])
				if !ok {
					add(path+".rules."+key, typeMsg("array string", table[key]))
					continue
				}
				for i, item := range list {
					if _, err := pattern.ParsePattern(item); err != nil {
						add(fmt.Sprintf("%s.rules.%s.%d", path, key, i), fmt.Sprintf("invalid pattern '%s': %v", item, err))
					}
				}
			}
		}
	}
}

// validateTeam checks the keys of one [teams.<name>] table. References to
// agents and teams are checked in validateEffective, where the merged config is
// known.
func validateTeam(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(teamKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, teamKeys))
		}
	}
	lead, hasLead := v["lead"]
	leadName, leadIsString := lead.(string)
	switch {
	case !hasLead:
		add(path+".lead", "is required")
	case !leadIsString:
		add(path+".lead", typeMsg("string", lead))
	}
	members, hasMembers := v["members"]
	list, membersAreStrings := asStringSlice(members)
	switch {
	case !hasMembers:
		add(path+".members", "is required")
	case !membersAreStrings:
		add(path+".members", typeMsg("array string", members))
	default:
		if len(list) == 0 {
			add(path+".members", "must list at least one agent")
		}
		if len(list) > maxTeamMembers {
			add(path+".members", fmt.Sprintf("must list at most %d agents", maxTeamMembers))
		}
		seen := map[string]bool{}
		for i, member := range list {
			itemPath := fmt.Sprintf("%s.members.%d", path, i)
			if leadIsString && member == leadName {
				add(itemPath, "is the lead; the lead is not listed in members")
			}
			if seen[member] {
				add(itemPath, fmt.Sprintf("agent '%s' is listed twice", member))
				continue
			}
			seen[member] = true
		}
	}
	if raw, ok := v["description"]; ok {
		s, ok := raw.(string)
		if !ok {
			add(path+".description", typeMsg("string", raw))
		} else if n := utf8.RuneCountInString(s); n > maxTeamDescription {
			add(path+".description", fmt.Sprintf("must be at most %d characters", maxTeamDescription))
		}
	}
	// Spec 47 REQ-RDS-001: discussion settings.
	enum := func(key string, allowed ...string) {
		raw, ok := v[key]
		if !ok {
			return
		}
		s, ok := raw.(string)
		if !ok {
			add(path+"."+key, typeMsg("string", raw))
			return
		}
		if !contains(allowed, s) {
			add(path+"."+key, fmt.Sprintf("must be one of %s", strings.Join(allowed, ", ")))
		}
	}
	enum("discuss", "auto", "ask")
	enum("extra_rounds", "once", "ask", "never")
	if raw, ok := v["rounds"]; ok {
		n, ok := asInt(raw)
		if !ok {
			add(path+".rounds", typeMsg("integer", raw))
		} else if n < 1 || n > 5 {
			add(path+".rounds", "must be between 1 and 5")
		}
	}
	for _, key := range []string{"room_memory", "share_summary", "learn", "telegram_all"} {
		if raw, ok := v[key]; ok {
			if _, ok := raw.(bool); !ok {
				add(path+"."+key, typeMsg("boolean", raw))
			}
		}
	}
}

// validToolRef reports whether s is a tool reference of the form <tool> or
// <mcp_server>/<tool>. The names are not checked against the tool registry:
// an unknown name is kept and has no effect (Q-024).
func validToolRef(s string) bool {
	if s == "" || strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return false
	}
	parts := strings.Split(s, "/")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}

func validateRoute(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(routeKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, routeKeys))
		}
	}
	for _, key := range []string{"channel", "agent"} {
		val, ok := v[key]
		if !ok {
			add(path+"."+key, "is required")
		} else if _, ok := val.(string); !ok {
			add(path+"."+key, typeMsg("string", val))
		}
	}
	if val, ok := v["match"]; ok {
		if _, ok := asMap(val); !ok {
			add(path+".match", typeMsg("table", val))
		}
	}
}

func validateMCP(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(mcpKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, mcpKeys))
		}
	}
	command, hasCommand := v["command"]
	url, hasURL := v["url"]
	if hasCommand {
		if _, ok := command.(string); !ok {
			add(path+".command", typeMsg("string", command))
		}
	}
	if hasURL {
		if _, ok := url.(string); !ok {
			add(path+".url", typeMsg("string", url))
		}
	}
	if hasCommand && hasURL {
		add(path+".url", "command and url cannot both be set")
	}
	if !hasCommand && !hasURL {
		add(path, "command or url is required")
	}
	if val, ok := v["args"]; ok {
		if _, ok := asStringSlice(val); !ok {
			add(path+".args", typeMsg("array string", val))
		}
	}
	for _, key := range []string{"env", "headers"} {
		if val, ok := v[key]; ok {
			table, ok := asMap(val)
			if !ok {
				add(path+"."+key, typeMsg("table", val))
				continue
			}
			for _, name := range sortedKeys(table) {
				if _, ok := table[name].(string); !ok {
					add(path+"."+key+"."+name, typeMsg("string", table[name]))
				}
			}
		}
	}
}

func validateModuleRow(v map[string]any, path string, modules []string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(moduleKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, moduleKeys))
		}
	}
	if idRaw, ok := v["id"]; !ok {
		add(path+".id", "is required")
	} else if id, ok := idRaw.(string); !ok {
		add(path+".id", typeMsg("string", idRaw))
	} else if id == "" {
		add(path+".id", "is required")
	}
	module, hasModule := v["module"]
	if hasModule {
		name, ok := module.(string)
		if !ok {
			add(path+".module", typeMsg("string", module))
		} else if name != "" && !contains(modules, name) {
			add(path+".module", fmt.Sprintf("unknown module '%s' (available: %s)", name, strings.Join(modules, ", ")))
		}
	}
	if val, ok := v["disabled"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".disabled", typeMsg("boolean", val))
		}
	}
	_, hasAfter := v["after"]
	_, hasBefore := v["before"]
	for _, key := range []string{"after", "before"} {
		if val, ok := v[key]; ok {
			if _, ok := val.(string); !ok {
				add(path+"."+key, typeMsg("string", val))
			}
		}
	}
	if hasAfter && hasBefore {
		add(path+".before", "after and before cannot both be set")
	}
	if val, ok := v["config"]; ok {
		if _, ok := asMap(val); !ok {
			add(path+".config", typeMsg("table", val))
		}
	}
}

func validateWeb(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(webKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, webKeys))
		}
	}
	if val, ok := v["enabled"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".enabled", typeMsg("boolean", val))
		}
	}
	if val, ok := v["open_browser"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".open_browser", typeMsg("boolean", val))
		}
	}
	if val, ok := v["addr"]; ok {
		if _, ok := val.(string); !ok {
			add(path+".addr", typeMsg("string", val))
		}
	}
}

// validateUpdate checks the [update] table (API-UPD-001): channel is "stable"
// or "beta", check is a boolean.
func validateUpdate(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(updateKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, updateKeys))
		}
	}
	if val, ok := v["channel"]; ok {
		if s, ok := val.(string); !ok || (s != "stable" && s != "beta") {
			add(path+".channel", `must be "stable" or "beta"`)
		}
	}
	if val, ok := v["check"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".check", typeMsg("boolean", val))
		}
	}
}

// validateKnowledge checks one [knowledge] table's keys and types
// (API-KNW-001, API-KNU-001). The embedding_provider and agent references are
// checked in validateEffective, where the merged config is known.
func validateKnowledge(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(knowledgeKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, knowledgeKeys))
		}
	}
	for _, key := range []string{"path", "embedding_provider", "embedding_model", "agent"} {
		if val, ok := v[key]; ok {
			if _, ok := val.(string); !ok {
				add(path+"."+key, typeMsg("string", val))
			}
		}
	}
	if val, ok := v["auto_recall"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".auto_recall", typeMsg("boolean", val))
		}
	}
	// API-KNU-001: journal turns the journal pass on or off.
	if val, ok := v["journal"]; ok {
		if _, ok := val.(bool); !ok {
			add(path+".journal", typeMsg("boolean", val))
		}
	}
	// API-KNU-001: consolidate_at is a 24-hour HH:MM local time, or "" to turn
	// nightly runs off.
	if val, ok := v["consolidate_at"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".consolidate_at", typeMsg("string", val))
		} else if s != "" {
			if _, err := time.Parse("15:04", s); err != nil {
				add(path+".consolidate_at", `want HH:MM or ""`)
			}
		}
	}
	provider, _ := v["embedding_provider"].(string)
	if provider != "" {
		raw, exists := v["embedding_model"]
		model, isString := raw.(string)
		if !exists || (isString && model == "") {
			add(path+".embedding_model", "required when embedding_provider is set")
		}
	}
}

func validateEffective(m map[string]any, positions map[string]pos, add func(string, string)) {
	agents, _ := asMap(m["agents"])
	teams, _ := asMap(m["teams"])
	providers, _ := asMap(m["providers"])
	mcps, _ := asMap(m["mcp"])
	combos, _ := asMap(m["combos"])
	comboNames := sortedKeys(combos)
	// The built-in account's optional table (API-CCT-027) is no configured
	// provider: claude-code addresses are checked as Claude Code accounts.
	providerNames := withoutString(sortedKeys(providers), core.ClaudeCodeProvider)
	agentNames := sortedKeys(agents)
	teamNames := sortedKeys(teams)
	mcpNames := sortedKeys(mcps)
	if k, ok := asMap(m["knowledge"]); ok {
		if name, ok := k["embedding_provider"].(string); ok && name != "" {
			if raw, ok := providers[name]; ok {
				pm, _ := asMap(raw)
				kind, _ := pm["kind"].(string)
				if kind != "" && !contains(embeddingKinds, kind) {
					add("knowledge.embedding_provider", fmt.Sprintf("provider %q must have kind openai, deepseek, openrouter, or openai_compat", name))
				}
			} else {
				add("knowledge.embedding_provider", fmt.Sprintf("unknown provider %q", name))
			}
		}
		// API-KNU-001: knowledge.agent must name a configured agent.
		if name, ok := k["agent"].(string); ok && name != "" {
			// REQ-CCT-007: a Claude Code agent may run the passes; they use its
			// background chain.
			if !contains(agentNames, name) {
				add("knowledge.agent", fmt.Sprintf("unknown agent %q", name))
			}
		}
	}
	// API-CCT-001: claude-code is a built-in provider. Spec 39 revision 5
	// (API-CCT-027): an optional [providers.claude-code] table of kind
	// "claude-code" holds the built-in account's settings only; it never
	// moves the account off the owner's own Claude Code login.
	if raw, ok := providers[core.ClaudeCodeProvider]; ok {
		table, _ := asMap(raw)
		path := "providers." + core.ClaudeCodeProvider
		if kind, _ := table["kind"].(string); kind != core.ClaudeCodeKind {
			add(path, "claude-code is built in")
		} else {
			for _, key := range builtinAccountRejectedKeys {
				if _, ok := table[key]; ok {
					add(path+"."+key, "not allowed for the built-in account: it always uses your normal Claude Code login")
				}
			}
		}
	}
	// REQ-CCT-010: the Claude Code accounts are the built-in claude-code and
	// every provider of kind claude-code.
	accounts := claudeAccounts{core.ClaudeCodeProvider: true}
	for _, name := range providerNames {
		provider, _ := asMap(providers[name])
		if kind, _ := provider["kind"].(string); kind == core.ClaudeCodeKind {
			accounts[name] = true
		}
	}
	// API-RTR-001: base_url is required only for openai_compat; the other
	// kinds default to their public API URL.
	for _, name := range providerNames {
		provider, _ := asMap(providers[name])
		kind, _ := provider["kind"].(string)
		if kind != "openai_compat" {
			continue
		}
		if url, _ := provider["base_url"].(string); url == "" {
			add("providers."+name+".base_url", "is required for kind openai_compat")
		}
	}
	// API-RTR-002: combo names do not shadow providers; models are known
	// provider/model addresses, never combos, without duplicates.
	for _, name := range comboNames {
		combo, _ := asMap(combos[name])
		path := "combos." + name
		if contains(providerNames, name) || name == core.ClaudeCodeProvider {
			add(path, "name is already used by a provider")
		}
		raw, hasModels := combo["models"]
		list, ok := asStringSlice(raw)
		switch {
		case !hasModels:
			add(path+".models", "is required")
			continue
		case !ok:
			continue
		case len(list) == 0:
			add(path+".models", "must list at least one model")
		case len(list) > maxComboModels:
			add(path+".models", fmt.Sprintf("must list at most %d models", maxComboModels))
		}
		seen := map[string]bool{}
		for i, item := range list {
			itemPath := fmt.Sprintf("%s.models[%d]", path, i)
			switch {
			case contains(comboNames, item):
				add(itemPath, fmt.Sprintf("%q is a combo; a combo must not list another combo", item))
			case !isAddress(item):
				add(itemPath, fmt.Sprintf("%q is not a provider/model address", item))
			case accounts.address(item):
				if msg := claudeCodeModelMsg(item); msg != "" {
					add(itemPath, msg)
				} else if seen[item] {
					add(itemPath, fmt.Sprintf("model %q is listed twice", item))
				}
			case !contains(providerNames, addressProvider(item)):
				add(itemPath, fmt.Sprintf("unknown provider %q", addressProvider(item)))
			case seen[item]:
				add(itemPath, fmt.Sprintf("model %q is listed twice", item))
			}
			seen[item] = true
		}
	}
	for _, name := range agentNames {
		agent, _ := asMap(agents[name])
		path := "agents." + name
		legacy := validateClaudeKeys(agent, path, accounts, add)
		provider, _ := agent["provider"].(string)
		if raw, ok := agent["provider"]; ok {
			if p, ok := raw.(string); ok && p != "" && !contains(providerNames, p) {
				add(path+".provider", fmt.Sprintf("provider '%s' is not registered (available: %s)", p, strings.Join(providerNames, ", ")))
			}
		}
		// API-RTR-003, AC-RTR-005: without provider, a model address must
		// name a known provider. A bare name that is not a combo is left to
		// the turn, which fails with "unknown provider or combo", so configs
		// written before combos keep loading.
		model, _ := agent["model"].(string)
		switch {
		case legacy:
			// API-CCT-002: the model of a legacy runtime agent is the part
			// after "claude-code/"; validateClaudeKeys checked it.
		case provider == "" && accounts.address(model):
			if msg := claudeCodeModelMsg(model); msg != "" {
				add(path+".model", msg)
			}
		case provider == "" && isModelRef(model) && !contains(providerNames, addressProvider(model)):
			add(path+".model", fmt.Sprintf("unknown provider or combo %q", model))
		}
		// API-RTR-003: fallback entries are provider:model or provider/model
		// of a known provider; a combo is not allowed.
		fallback, _ := asStringSlice(agent["fallback"])
		for i, item := range fallback {
			itemPath := fmt.Sprintf("%s.fallback.%d", path, i)
			switch {
			case contains(comboNames, item):
				add(itemPath, fmt.Sprintf("%q is a combo; fallback takes provider:model or provider/model", item))
			case !isModelRef(item):
				add(itemPath, fmt.Sprintf("%q is not provider:model or provider/model", item))
			case accounts.address(item):
				if msg := claudeCodeModelMsg(item); msg != "" {
					add(itemPath, msg)
				}
			case !contains(providerNames, addressProvider(item)):
				add(itemPath, fmt.Sprintf("unknown provider %q", addressProvider(item)))
			}
		}
		for _, key := range []string{"subagents", "mcp"} {
			list, _ := asStringSlice(agent[key])
			for i, item := range list {
				switch key {
				case "subagents":
					if contains(teamNames, item) {
						add(fmt.Sprintf("%s.subagents.%d", path, i), "is a team; subagents must be agents")
					} else if !contains(agentNames, item) {
						add(fmt.Sprintf("%s.subagents.%d", path, i), fmt.Sprintf("subagent '%s' is not registered", item))
					}
				case "mcp":
					if !contains(mcpNames, item) {
						add(fmt.Sprintf("%s.mcp.%d", path, i), fmt.Sprintf("mcp server '%s' is not registered", item))
					}
				}
			}
		}
	}
	for _, name := range teamNames {
		team, _ := asMap(teams[name])
		path := "teams." + name
		if contains(agentNames, name) {
			add(path, "name is already used by an agent")
		}
		if lead, ok := team["lead"].(string); ok {
			switch {
			case contains(teamNames, lead):
				add(path+".lead", "is a team; a team lead must be an agent")
			case !contains(agentNames, lead):
				add(path+".lead", fmt.Sprintf("agent '%s' is not registered (available: %s)", lead, strings.Join(agentNames, ", ")))
			}
		}
		members, _ := asStringSlice(team["members"])
		for i, member := range members {
			itemPath := fmt.Sprintf("%s.members.%d", path, i)
			switch {
			case contains(teamNames, member):
				add(itemPath, "is a team; teams cannot contain teams")
			case !contains(agentNames, member):
				add(itemPath, fmt.Sprintf("agent '%s' is not registered (available: %s)", member, strings.Join(agentNames, ", ")))
			}
		}
	}
	if routes, ok := asSlice(m["routes"]); ok {
		routable := append([]string{}, agentNames...)
		for _, name := range teamNames {
			if !contains(routable, name) {
				routable = append(routable, name)
			}
		}
		sort.Strings(routable)
		for i, item := range routes {
			route, _ := asMap(item)
			agent, _ := route["agent"].(string)
			if agent != "" && !contains(routable, agent) {
				add(fmt.Sprintf("routes.%d.agent", i), fmt.Sprintf("agent '%s' is not registered (available: %s)", agent, strings.Join(routable, ", ")))
			}
		}
	}
	if router, ok := asMap(m["router"]); ok {
		if background, ok := router["background_model"].(string); ok && background != "" {
			validateBackgroundModel(background, combos, providerNames, accounts, add)
		}
	}
	rows, _ := asSlice(m["modules"])
	ids := map[string]bool{}
	for _, item := range rows {
		row, _ := asMap(item)
		if id, ok := row["id"].(string); ok {
			ids[id] = true
		}
	}
	for _, item := range rows {
		row, _ := asMap(item)
		id, _ := row["id"].(string)
		disabled, _ := row["disabled"].(bool)
		module, _ := row["module"].(string)
		if module == "" && !disabled {
			add("modules."+id+".module", "is required")
		}
		for _, key := range []string{"after", "before"} {
			if target, ok := row[key].(string); ok && target != "" && !ids[target] {
				add("modules."+id+"."+key, fmt.Sprintf("id '%s' does not exist", target))
			}
		}
	}
	_ = positions
}

// isModelRef reports whether s is "provider/model" or "provider:model",
// split at the first "/" or ":", with both parts non-empty.
func isModelRef(s string) bool {
	i := strings.IndexAny(s, "/:")
	return i > 0 && i < len(s)-1
}

// isAddress reports whether s is a "provider/model" address: the first
// separator is "/".
func isAddress(s string) bool {
	return isModelRef(s) && s[strings.IndexAny(s, "/:")] == '/'
}

// addressProvider returns the provider part of a model reference.
func addressProvider(s string) string {
	if i := strings.IndexAny(s, "/:"); i > 0 {
		return s[:i]
	}
	return s
}

func unknownKeyMsg(key string, known []string) string {
	msg := "unknown key"
	if s := suggest(key, known); s != "" {
		msg += " (did you mean: " + s + "?)"
	}
	return msg
}

func suggest(key string, known []string) string {
	best := ""
	bestDist := 3
	for _, k := range known {
		d := levenshtein(strings.ToLower(key), strings.ToLower(k))
		if d < bestDist || (d == bestDist && (best == "" || k < best)) {
			best = k
			bestDist = d
		}
	}
	if bestDist <= 2 {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asSlice(v any) ([]any, bool) {
	list, ok := v.([]any)
	return list, ok
}

func asStringSlice(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss, true
		}
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	}
	return 0, false
}

// asFloat reads a TOML float, accepting an integer value as a float too.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int64, int, int32:
		return "integer"
	case float64:
		return "float"
	case bool:
		return "boolean"
	case []any, []string:
		return "array"
	case map[string]any:
		return "table"
	case time.Time:
		return "datetime"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func typeMsg(want string, got any) string {
	return "must be " + want + ", got " + typeName(got)
}

func enumMsg(value string, choices []string) string {
	sorted := append([]string{}, choices...)
	sort.Strings(sorted)
	return fmt.Sprintf("invalid value '%s' (choices: %s)", value, strings.Join(sorted, ", "))
}

// claudeAccounts is the set of Claude Code account names: the built-in
// claude-code and every provider of kind claude-code (REQ-CCT-010).
type claudeAccounts map[string]bool

// address reports whether s is a "<account>/<model>" address of an account.
func (a claudeAccounts) address(s string) bool {
	return isAddress(s) && a[addressProvider(s)]
}

// claudeCodeModelMsg checks the model part of a Claude Code address
// (REQ-CCT-001); "" means valid.
func claudeCodeModelMsg(address string) string {
	model := address
	if i := strings.IndexByte(address, '/'); i >= 0 {
		model = address[i+1:]
	}
	if strings.HasPrefix(model, "-") {
		return "a claude-code model must not start with '-'"
	}
	if strings.TrimSpace(model) != model || model == "" {
		return fmt.Sprintf("invalid claude-code model %q", model)
	}
	return ""
}

// validateBackgroundModel checks [router] background_model (API-CCT-003): a
// combo or address of a known provider, never with a claude-code target.
func validateBackgroundModel(value string, combos map[string]any, providerNames []string, accounts claudeAccounts, add func(string, string)) {
	const path = "router.background_model"
	if raw, ok := combos[value]; ok {
		combo, _ := asMap(raw)
		list, _ := asStringSlice(combo["models"])
		for _, item := range list {
			if accounts.address(item) {
				add(path, fmt.Sprintf("combo %q lists %q; background passes cannot use claude-code", value, item))
				return
			}
		}
		return
	}
	switch {
	case accounts[addressProvider(value)]:
		add(path, "background passes cannot use claude-code")
	case !isModelRef(value):
		add(path, fmt.Sprintf("unknown provider or combo %q", value))
	case !contains(providerNames, addressProvider(value)):
		add(path, fmt.Sprintf("unknown provider %q", addressProvider(value)))
	}
}

// validateClaudeKeys checks the Claude Code keys of one merged agent table
// (API-CCT-002, API-CCR-001): auth, api_key_env, the legacy runtime key, and
// the reasoning_effort of an agent whose model runs in Claude Code. The keys
// auth, api_key_env, binary, and allow_scheduled are accepted on any agent and
// apply to its Claude Code turns. It reports whether the agent uses the
// legacy runtime key.
func validateClaudeKeys(agent map[string]any, path string, accounts claudeAccounts, add func(string, string)) bool {
	auth, _ := agent["auth"].(string)
	if auth != "" && !contains(runtimeAuthModes, auth) {
		add(path+".auth", enumMsg(auth, runtimeAuthModes))
	}
	if keyEnv, _ := agent["api_key_env"].(string); auth == core.RuntimeAuthAPIKey && keyEnv == "" {
		add(path+".api_key_env", "required when auth is api_key")
	}
	model, _ := agent["model"].(string)
	runtime, isString := agent["runtime"].(string)
	legacy := isString && runtime != ""
	claude := accounts.address(model)
	if legacy {
		names := runtimeNames()
		if !contains(names, runtime) {
			add(path+".runtime", fmt.Sprintf("unknown runtime %q (available: %s)", runtime, strings.Join(names, ", ")))
		}
		for _, key := range runtimeForbiddenKeys {
			if _, ok := agent[key]; ok {
				add(path+"."+key, fmt.Sprintf("a %s agent cannot set %s", runtime, key))
			}
		}
		if strings.HasPrefix(model, "-") {
			add(path+".model", "must not start with '-'")
		}
		claude = true
	}
	if effort, ok := agent["reasoning_effort"].(string); ok && effort != "" && claude {
		allowed := append(append([]string{}, runtimeEfforts...), "off")
		if legacy {
			allowed = runtimeEfforts
		}
		if !contains(allowed, effort) {
			add(path+".reasoning_effort", enumMsg(effort, allowed))
		}
	} else if ok && effort != "" && isAddress(model) && !contains(nativeEfforts, effort) {
		// The layer check leaves an address model to this merged check.
		add(path+".reasoning_effort", enumMsg(effort, nativeEfforts))
	}
	return legacy
}

// withoutString returns list without value.
func withoutString(list []string, value string) []string {
	out := list[:0:0]
	for _, item := range list {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

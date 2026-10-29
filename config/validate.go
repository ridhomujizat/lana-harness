package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

var topKeys = []string{"agents", "channels", "mcp", "modules", "providers", "routes", "version"}
var providerKeys = []string{"api_key_env", "base_url", "headers", "kind", "timeout"}
var agentKeys = []string{"context_window", "fallback", "max_steps", "mcp", "model", "permission", "prompt_file", "provider", "rules", "skills", "subagents", "tools", "workspace"}
var routeKeys = []string{"agent", "channel", "match"}
var mcpKeys = []string{"args", "command", "env", "headers", "url"}
var moduleKeys = []string{"after", "before", "config", "disabled", "id", "module"}

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
			add("version", fmt.Sprintf("versi config %d lebih tinggi dari yang didukung (%d)", n, maxSupportedVersion))
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
						add(path+".id", fmt.Sprintf("id '%s' sudah dipakai di baris %d", id, first))
					} else {
						p := at(path)
						seen[id] = p.Line
					}
				}
				validateModuleRow(val, path, modules, add)
			}
		}
	}
	return errs
}

func validateProvider(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(providerKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, providerKeys))
		}
	}
	kind, ok := v["kind"]
	if !ok {
		add(path+".kind", "wajib diisi")
	} else if s, ok := kind.(string); !ok {
		add(path+".kind", typeMsg("string", kind))
	} else if !contains([]string{"openai_compat", "anthropic"}, s) {
		add(path+".kind", enumMsg(s, []string{"anthropic", "openai_compat"}))
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
	if val, ok := v["timeout"]; ok {
		s, ok := val.(string)
		if !ok {
			add(path+".timeout", typeMsg("string", val))
		} else if _, err := time.ParseDuration(s); err != nil {
			add(path+".timeout", "durasi tidak valid: "+s)
		}
	}
}

func validateAgent(v map[string]any, path string, add func(string, string)) {
	for _, key := range sortedKeys(v) {
		if !contains(agentKeys, key) {
			add(path+"."+key, unknownKeyMsg(key, agentKeys))
		}
	}
	for _, key := range []string{"provider", "model", "prompt_file", "workspace", "permission"} {
		if val, ok := v[key]; ok {
			if _, ok := val.(string); !ok {
				add(path+"."+key, typeMsg("string", val))
			}
		}
	}
	for _, key := range []string{"fallback", "tools", "subagents", "mcp", "skills"} {
		if val, ok := v[key]; ok {
			if _, ok := asStringSlice(val); !ok {
				add(path+"."+key, typeMsg("array string", val))
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
			if key == "max_steps" && (n < 1 || n > 200) {
				add(path+"."+key, "harus di antara 1 dan 200")
			}
			if key == "context_window" && n <= 0 {
				add(path+"."+key, "harus lebih dari 0")
			}
		}
	}
	if val, ok := v["permission"]; ok {
		if s, ok := val.(string); ok && !contains([]string{"ask", "never", "trust"}, s) {
			add(path+".permission", enumMsg(s, []string{"ask", "never", "trust"}))
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
						add(fmt.Sprintf("%s.rules.%s.%d", path, key, i), fmt.Sprintf("pola '%s' tidak valid: %v", item, err))
					}
				}
			}
		}
	}
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
			add(path+"."+key, "wajib diisi")
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
		add(path+".url", "command dan url tidak boleh diisi bersamaan")
	}
	if !hasCommand && !hasURL {
		add(path, "command atau url wajib diisi")
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
		add(path+".id", "wajib diisi")
	} else if id, ok := idRaw.(string); !ok {
		add(path+".id", typeMsg("string", idRaw))
	} else if id == "" {
		add(path+".id", "wajib diisi")
	}
	module, hasModule := v["module"]
	if hasModule {
		name, ok := module.(string)
		if !ok {
			add(path+".module", typeMsg("string", module))
		} else if name != "" && !contains(modules, name) {
			add(path+".module", fmt.Sprintf("modul '%s' tidak dikenal (tersedia: %s)", name, strings.Join(modules, ", ")))
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
		add(path+".before", "after dan before tidak boleh diisi bersamaan")
	}
	if val, ok := v["config"]; ok {
		if _, ok := asMap(val); !ok {
			add(path+".config", typeMsg("table", val))
		}
	}
}

func validateEffective(m map[string]any, positions map[string]pos, add func(string, string)) {
	agents, _ := asMap(m["agents"])
	providers, _ := asMap(m["providers"])
	mcps, _ := asMap(m["mcp"])
	providerNames := sortedKeys(providers)
	agentNames := sortedKeys(agents)
	mcpNames := sortedKeys(mcps)
	for _, name := range agentNames {
		agent, _ := asMap(agents[name])
		path := "agents." + name
		if raw, ok := agent["provider"]; ok {
			if p, ok := raw.(string); ok && p != "" && !contains(providerNames, p) {
				add(path+".provider", fmt.Sprintf("provider '%s' tidak terdaftar (tersedia: %s)", p, strings.Join(providerNames, ", ")))
			}
		}
		for _, key := range []string{"subagents", "mcp"} {
			list, _ := asStringSlice(agent[key])
			for i, item := range list {
				switch key {
				case "subagents":
					if !contains(agentNames, item) {
						add(fmt.Sprintf("%s.subagents.%d", path, i), fmt.Sprintf("subagent '%s' tidak terdaftar", item))
					}
				case "mcp":
					if !contains(mcpNames, item) {
						add(fmt.Sprintf("%s.mcp.%d", path, i), fmt.Sprintf("server mcp '%s' tidak terdaftar", item))
					}
				}
			}
		}
	}
	if routes, ok := asSlice(m["routes"]); ok {
		for i, item := range routes {
			route, _ := asMap(item)
			agent, _ := route["agent"].(string)
			if agent != "" && !contains(agentNames, agent) {
				add(fmt.Sprintf("routes.%d.agent", i), fmt.Sprintf("agent '%s' tidak terdaftar (tersedia: %s)", agent, strings.Join(agentNames, ", ")))
			}
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
			add("modules."+id+".module", "wajib diisi")
		}
		for _, key := range []string{"after", "before"} {
			if target, ok := row[key].(string); ok && target != "" && !ids[target] {
				add("modules."+id+"."+key, fmt.Sprintf("id '%s' tidak ada", target))
			}
		}
	}
	_ = positions
}

func unknownKeyMsg(key string, known []string) string {
	msg := "key tidak dikenal"
	if s := suggest(key, known); s != "" {
		msg += " (maksud Anda: " + s + "?)"
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
	return "harus " + want + ", dapat " + typeName(got)
}

func enumMsg(value string, choices []string) string {
	sorted := append([]string{}, choices...)
	sort.Strings(sorted)
	return fmt.Sprintf("nilai '%s' tidak valid (pilihan: %s)", value, strings.Join(sorted, ", "))
}

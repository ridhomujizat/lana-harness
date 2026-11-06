package telegram

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// DefaultAPIBase is the public Bot API endpoint.
const DefaultAPIBase = "https://api.telegram.org"

// Config is the parsed [channels.telegram] table plus the telegram routes.
type Config struct {
	// Enabled is the [channels.telegram] enabled switch; it defaults to true.
	Enabled  bool
	TokenEnv string
	OwnerIDs map[int64]bool
	APIBase  string
	// Routes maps chat_id to the routed agent, first match wins.
	Routes map[int64]string
}

// FieldError is a config error for one key of [channels.telegram].
type FieldError struct {
	Key    string
	Reason string
}

func (e *FieldError) Error() string { return "channels.telegram." + e.Key + ": " + e.Reason }

// ParseConfig validates [channels.telegram] and the routes with channel "telegram".
func ParseConfig(raw map[string]any, routes []core.RouteConfig) (Config, error) {
	cfg := Config{Enabled: true, TokenEnv: "TELEGRAM_BOT_TOKEN", APIBase: DefaultAPIBase, OwnerIDs: map[int64]bool{}, Routes: map[int64]string{}}
	for key, value := range raw {
		switch key {
		case "enabled":
			enabled, ok := value.(bool)
			if !ok {
				return cfg, &FieldError{key, "must be a boolean"}
			}
			cfg.Enabled = enabled
		case "token_env":
			text, ok := value.(string)
			if !ok || text == "" {
				return cfg, &FieldError{key, "must be a non-empty string"}
			}
			cfg.TokenEnv = text
		case "owner_ids":
			list, ok := value.([]any)
			if !ok {
				return cfg, &FieldError{key, "must be an array of integers"}
			}
			for _, item := range list {
				id, ok := toInt64(item)
				if !ok {
					return cfg, &FieldError{key, "must be an array of integers"}
				}
				cfg.OwnerIDs[id] = true
			}
		case "api_base":
			text, ok := value.(string)
			parsed, err := url.Parse(text)
			if !ok || err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.HasSuffix(text, "/") {
				return cfg, &FieldError{key, "must be an http or https URL without trailing /"}
			}
			cfg.APIBase = text
		default:
			return cfg, &FieldError{key, "unknown key"}
		}
	}
	for i, route := range routes {
		if route.Channel != "telegram" {
			continue
		}
		id, ok := toInt64(route.Match["chat_id"])
		if !ok {
			return cfg, fmt.Errorf("routes[%d]: telegram route needs match = { chat_id = <integer> }", i)
		}
		if _, seen := cfg.Routes[id]; !seen {
			cfg.Routes[id] = route.Agent
		}
	}
	// A disabled channel needs no route (REQ-DMN-005, Q-011). The route
	// entries themselves are still validated above.
	if cfg.Enabled && len(cfg.Routes) == 0 {
		return cfg, fmt.Errorf(`no [[routes]] entry for channel "telegram"`)
	}
	return cfg, nil
}

func toInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
	}
	return 0, false
}

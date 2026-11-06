package telegram

import (
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func testRoutes() []core.RouteConfig {
	return []core.RouteConfig{{Channel: "telegram", Agent: "a", Match: map[string]any{"chat_id": int64(5)}}}
}

func TestTelegramEnabledDefaultTrue(t *testing.T) {
	cfg, err := ParseConfig(nil, testRoutes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("enabled must default to true")
	}
}

func TestTelegramEnabledFalse(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"enabled": false}, testRoutes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("enabled must be false")
	}
	if cfg.TokenEnv != "TELEGRAM_BOT_TOKEN" || cfg.APIBase != DefaultAPIBase {
		t.Fatalf("defaults changed: %+v", cfg)
	}
	if cfg.Routes[5] != "a" {
		t.Fatalf("routes = %v", cfg.Routes)
	}
}

func TestTelegramEnabledMustBeBoolean(t *testing.T) {
	for _, raw := range []map[string]any{{"enabled": "no"}, {"enabled": int64(1)}, {"enabled": nil}} {
		_, err := ParseConfig(raw, testRoutes())
		var field *FieldError
		if !errors.As(err, &field) {
			t.Fatalf("%v: error is not *FieldError: %v", raw, err)
		}
		if field.Key != "enabled" || field.Reason != "must be a boolean" {
			t.Fatalf("%v: field = %+v", raw, field)
		}
	}
}

func TestTelegramEnabledFalseSkipsRouteRequirement(t *testing.T) {
	// AC-TG-027: disabling the channel needs no [[routes]] entry.
	cfg, err := ParseConfig(map[string]any{"enabled": false}, nil)
	if err != nil {
		t.Fatalf("parse without routes: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("enabled must be false")
	}
	if len(cfg.Routes) != 0 {
		t.Fatalf("routes = %v", cfg.Routes)
	}

	// Q-011: the other keys of the table are still validated.
	if _, err := ParseConfig(map[string]any{"enabled": false, "nope": 1}, nil); err == nil {
		t.Fatal("unknown key accepted while disabled")
	}
}

func TestTelegramEnabledKeepsExistingBehavior(t *testing.T) {
	raw := map[string]any{
		"enabled":   true,
		"token_env": "MY_TOKEN",
		"owner_ids": []any{int64(7)},
		"api_base":  "https://example.test",
	}
	cfg, err := ParseConfig(raw, testRoutes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.Enabled || cfg.TokenEnv != "MY_TOKEN" || !cfg.OwnerIDs[7] || cfg.APIBase != "https://example.test" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// The route requirement is unchanged: a Telegram table needs a route.
	if _, err := ParseConfig(raw, nil); err == nil || !strings.Contains(err.Error(), `no [[routes]] entry for channel "telegram"`) {
		t.Fatalf("no routes err = %v", err)
	}
}

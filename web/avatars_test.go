package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/avatar"
)

// avatarTestBackend adds the avatar port to the in-memory backend.
type avatarTestBackend struct {
	*fakeWebBackend
	generate func(ctx context.Context, agent, model string, seed int64) (avatar.Result, error)
}

func (b *avatarTestBackend) GenerateAvatar(ctx context.Context, agent, model string, seed int64) (avatar.Result, error) {
	return b.generate(ctx, agent, model, seed)
}

const avatarTestConfig = t016AgentsConfig + `
[agents.helper]
provider = "mock"
model = "helper"

[teams.crew]
lead = "coder"
members = ["helper"]
`

func avatarRequest(t *testing.T, s *Server, method, target string, body any, cookie *http.Cookie, csrf string) *httptestResponse {
	t.Helper()
	payload := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = string(raw)
	}
	rr := serveAuth(s, settingsRequest(method, target, payload, cookie, csrf))
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

// API-AVT-001: the generate endpoint passes model and seed to the backend,
// maps refused models to 400 invalid_model, and draws locally without a
// backend or when the daemon does not know the agent yet.
func TestAvatarGenerate(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)

	// Without a backend: local.
	rr := avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"seed": 5}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("local status = %d, body = %s", rr.code, rr.body)
	}
	var got generateAvatarResponse
	if err := json.Unmarshal(rr.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "local" || got.Seed != 5 || got.SVG != avatar.Generate(5) || got.Model != "" {
		t.Fatalf("local result = %+v", got)
	}

	var calls []string
	modelSVG := avatar.Generate(77)
	backend := &avatarTestBackend{fakeWebBackend: newFakeWebBackend(), generate: func(_ context.Context, agent, model string, seed int64) (avatar.Result, error) {
		calls = append(calls, agent+"|"+model)
		switch model {
		case "claude-code/opus":
			return avatar.Result{}, avatar.ErrClaudeCodeModel
		case "nope/x":
			return avatar.Result{}, avatar.ErrUnknownModel
		case "boom/x":
			return avatar.Result{}, errors.New("boom")
		case "late/x":
			return avatar.Result{}, ErrUnknownAgent
		}
		return avatar.Result{SVG: modelSVG, Model: "deepseek/deepseek-chat", Seed: seed, Source: "model"}, nil
	}}
	s.SetBackend(backend)

	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"model": " deepseek/deepseek-chat "}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("model status = %d, body = %s", rr.code, rr.body)
	}
	got = generateAvatarResponse{}
	if err := json.Unmarshal(rr.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Source != "model" || got.SVG != modelSVG || got.Model != "deepseek/deepseek-chat" || got.Seed < 1 || got.Seed > maxAvatarSeed {
		t.Fatalf("model result = %+v", got)
	}
	if len(calls) != 1 || calls[0] != "coder|deepseek/deepseek-chat" {
		t.Fatalf("calls = %v", calls)
	}

	for model, code := range map[string]int{"claude-code/opus": 400, "nope/x": 400, "boom/x": 500} {
		rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"model": model}, cookie, csrf)
		if rr.code != code {
			t.Fatalf("%s status = %d, body = %s", model, rr.code, rr.body)
		}
		if code == 400 && decodeError(t, rr).Error.Code != "invalid_model" {
			t.Fatalf("%s body = %s", model, rr.body)
		}
	}
	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"model": "late/x", "seed": 9}, cookie, csrf)
	if rr.code != http.StatusOK || !strings.Contains(string(rr.body), `"source":"local"`) {
		t.Fatalf("unknown to daemon: %d %s", rr.code, rr.body)
	}

	for target, code := range map[string]int{
		"/v1/agents/crew/avatar/generate":  404,
		"/v1/agents/ghost/avatar/generate": 404,
	} {
		rr = avatarRequest(t, s, http.MethodPost, target, map[string]any{}, cookie, csrf)
		if rr.code != code {
			t.Fatalf("%s status = %d", target, rr.code)
		}
	}
	for _, body := range []any{map[string]any{"seed": 0}, map[string]any{"seed": 1 << 40}, map[string]any{"other": 1}} {
		rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", body, cookie, csrf)
		if rr.code != http.StatusBadRequest {
			t.Fatalf("%v status = %d", body, rr.code)
		}
	}
}

// API-AVT-002, API-AVT-003, API-AVT-005: PUT stores the sanitized SVG and
// the color, the list shows them, DELETE goes back to the letter avatar.
func TestAvatarPutDeleteRoundTrip(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)
	path := agenthome.Paths(store.home, "coder").Avatar
	svg := avatar.Generate(3)

	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder"); row.AvatarSVG != avatar.DefaultFor("coder") || row.AvatarSource != "default" {
		t.Fatalf("initial avatar = %q (%s)", row.AvatarSVG, row.AvatarSource)
	}

	// Model-style markup is accepted and stored canonically.
	messy := strings.Replace(svg, `fill="currentColor"`, `fill="currentColor" stroke="#000"`, 1)
	rr := avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"svg": messy, "color": "#12B5A6"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rr.code, rr.body)
	}
	var put avatarResponse
	if err := json.Unmarshal(rr.body, &put); err != nil {
		t.Fatal(err)
	}
	if put.Name != "coder" || put.AvatarSVG != svg || put.AvatarSource != "saved" || put.Color != "#12B5A6" {
		t.Fatalf("PUT response = %+v", put)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != svg {
		t.Fatalf("avatar.svg = %q, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("avatar.svg mode = %v, %v", info, err)
	}
	record := agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"])
	if record.Color != "#12B5A6" || record.Model != "coder" || len(record.Tools) != 2 {
		t.Fatalf("saved record = %+v", record)
	}
	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.AvatarSVG != svg || row.AvatarSource != "saved" || row.Color != "#12B5A6" {
		t.Fatalf("listed row = %q / %q", row.AvatarSVG, row.Color)
	}
	if other := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "helper"); other.AvatarSVG != avatar.DefaultFor("helper") || other.AvatarSource != "default" {
		t.Fatalf("helper avatar = %q (%s)", other.AvatarSVG, other.AvatarSource)
	}

	// Without color the key is kept; a token works too.
	rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"svg": svg}, cookie, csrf)
	if rr.code != http.StatusOK || agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"]).Color != "#12B5A6" {
		t.Fatalf("PUT without color: %d %s", rr.code, rr.body)
	}
	rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"svg": svg, "color": "blue"}, cookie, csrf)
	if rr.code != http.StatusOK || agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"]).Color != "blue" {
		t.Fatalf("PUT token color: %d %s", rr.code, rr.body)
	}

	// Refusals leave everything unchanged.
	before, _ := os.ReadFile(store.path)
	for _, body := range []map[string]any{
		{"svg": svg, "color": "#12B5A"},
		{"svg": svg, "color": "green"},
		{"svg": strings.Replace(svg, "</svg>", "<script>alert(1)</script></svg>", 1)},
		{"svg": "<svg/>"},
	} {
		rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", body, cookie, csrf)
		if rr.code != http.StatusUnprocessableEntity {
			t.Fatalf("%v status = %d, body = %s", body, rr.code, rr.body)
		}
		code := decodeError(t, rr).Error.Code
		if _, hasColor := body["color"]; hasColor && code != "validation_failed" || !hasColor && code != "invalid_avatar" {
			t.Fatalf("%v code = %q", body, code)
		}
	}
	after, _ := os.ReadFile(store.path)
	if string(before) != string(after) {
		t.Fatal("a refused PUT changed config.toml")
	}
	if data, _ := os.ReadFile(path); string(data) != svg {
		t.Fatal("a refused PUT changed avatar.svg")
	}
	if rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/crew/avatar", map[string]any{"svg": svg}, cookie, csrf); rr.code != http.StatusNotFound {
		t.Fatalf("team PUT status = %d", rr.code)
	}

	// A tampered file is not served.
	if err := os.WriteFile(path, []byte(`<svg viewBox="0 0 100 100"><script/></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder"); row.AvatarSVG != avatar.DefaultFor("coder") || row.AvatarSource != "default" {
		t.Fatalf("tampered avatar_svg = %q", row.AvatarSVG)
	}

	for i := 0; i < 2; i++ {
		rr = avatarRequest(t, s, http.MethodDelete, "/v1/agents/coder/avatar", nil, cookie, csrf)
		var del avatarResponse
		if rr.code != http.StatusOK || json.Unmarshal(rr.body, &del) != nil || del.AvatarSVG != avatar.DefaultFor("coder") || del.AvatarSource != "default" || del.Color != "blue" {
			t.Fatalf("DELETE %d: %d %s", i, rr.code, rr.body)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("avatar.svg still exists: %v", err)
	}
	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder"); row.AvatarSVG != avatar.DefaultFor("coder") || row.AvatarSource != "default" {
		t.Fatalf("deleted avatar_svg = %q", row.AvatarSVG)
	}
}

// Spec 43 extends API-ROOM-006: PUT /v1/agents/{name} accepts a hex color.
func TestAgentsHexColor(t *testing.T) {
	if !core.ValidAgentColor("#a1B2c3") || core.ValidAgentColor("#a1b2c") || core.ValidAgentColor("a1b2c3") || core.ValidAgentColor("#a1b2c3d4") {
		t.Fatal("ValidAgentColor hex rules")
	}
}

// API-AVT-006: avatar.svg serves the avatar with the color baked in for the
// theme, with an ETag, and 404 for an unknown name.
func TestAvatarImage(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)
	get := func(target, etag string) *httptest.ResponseRecorder {
		req := settingsRequest(http.MethodGet, target, "", cookie, "")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		return serveAuth(s, req)
	}
	// The default avatar in the derived token color, per theme.
	color := core.ResolveAgentColor("coder", "")
	rr := get("/v1/agents/coder/avatar.svg", "")
	want := avatar.Colorize(avatar.DefaultFor("coder"), avatar.ColorHex(color, "light"))
	if rr.Code != http.StatusOK || rr.Body.String() != want || rr.Header().Get("Content-Type") != "image/svg+xml" ||
		rr.Header().Get("Cache-Control") != "private, max-age=60" || strings.Contains(rr.Body.String(), "currentColor") {
		t.Fatalf("light: %d %v %s", rr.Code, rr.Header(), rr.Body.String())
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if rr = get("/v1/agents/coder/avatar.svg", etag); rr.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d", rr.Code)
	}
	rr = get("/v1/agents/coder/avatar.svg?theme=dark", "")
	if rr.Body.String() != avatar.Colorize(avatar.DefaultFor("coder"), avatar.ColorHex(color, "dark")) || rr.Header().Get("ETag") == etag {
		t.Fatalf("dark: %s", rr.Body.String())
	}

	// A saved avatar with a hex color.
	svg := avatar.Generate(11)
	if r := avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"svg": svg, "color": "#FF6A00"}, cookie, csrf); r.code != http.StatusOK {
		t.Fatalf("PUT: %d %s", r.code, r.body)
	}
	rr = get("/v1/agents/coder/avatar.svg?theme=dark", "")
	if rr.Body.String() != avatar.Colorize(svg, "#ff6a00") {
		t.Fatalf("saved: %s", rr.Body.String())
	}
	for _, target := range []string{"/v1/agents/ghost/avatar.svg", "/v1/agents/Bad..Name/avatar.svg"} {
		if rr = get(target, ""); rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d", target, rr.Code)
		}
	}
}

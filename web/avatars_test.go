package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/avatar"
)

// avatarTestBackend adds the avatar port to the in-memory backend.
type avatarTestBackend struct {
	*fakeWebBackend
	generate func(ctx context.Context, agent, mode, model, prompt string, seed int64, color string) (avatar.Result, error)
}

func (b *avatarTestBackend) GenerateAvatar(ctx context.Context, agent, mode, model, prompt string, seed int64, color string) (avatar.Result, error) {
	return b.generate(ctx, agent, mode, model, prompt, seed, color)
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

func avatarJSON(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// API-PXA-001: the catalog lists every part with an English label.
func TestAvatarCatalog(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)
	rr := avatarRequest(t, s, http.MethodGet, "/v1/avatars/catalog", nil, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.code, rr.body)
	}
	var catalog avatar.CatalogData
	avatarJSON(t, rr.body, &catalog)
	if len(catalog.Species) == 0 || catalog.Species[0].ID != "human" || catalog.Species[0].Label == "" {
		t.Fatalf("species = %+v", catalog.Species)
	}
	for name, list := range map[string][]avatar.CatalogItem{
		"hair": catalog.Hair, "hair_color": catalog.HairColor, "skin": catalog.Skin,
		"eyes": catalog.Eyes, "accessory": catalog.Accessory, "outfit": catalog.Outfit, "pants": catalog.Pants,
	} {
		if len(list) == 0 || list[0].ID == "" || list[0].Label == "" {
			t.Fatalf("%s = %+v", name, list)
		}
	}
}

// API-PXA-002, REQ-PXA-004: random and render build a valid sprite locally with
// the agent (or body) colour; a bad mode, seed, parts, or prompt is 400.
func TestAvatarGenerateRandomAndRender(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)

	// Random without a seed picks one and draws locally.
	rr := avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "random"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("random status = %d, body = %s", rr.code, rr.body)
	}
	var got generateAvatarResponse
	avatarJSON(t, rr.body, &got)
	if got.Source != avatar.SourceLocal || got.Model != "" || got.Avatar.Kind != avatar.KindParts || got.Avatar.Parts == nil {
		t.Fatalf("random result = %+v", got)
	}
	if got.Seed < 1 || got.Seed > maxAvatarSeed {
		t.Fatalf("seed = %d", got.Seed)
	}
	if err := avatar.ValidateSprite(got.Avatar.Sprite); err != nil {
		t.Fatalf("random sprite: %v", err)
	}
	if got.Avatar.Parts.Color != core.ResolveAgentColor("coder", "") {
		t.Fatalf("random colour = %q", got.Avatar.Parts.Color)
	}

	// A seed makes it deterministic; the body colour overrides the pick.
	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "random", "seed": 5, "color": "#12B5A6"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("seeded status = %d, body = %s", rr.code, rr.body)
	}
	got = generateAvatarResponse{}
	avatarJSON(t, rr.body, &got)
	wantParts := avatar.RandomParts(5)
	wantParts.Color = "#12B5A6"
	wantSprite, err := avatar.Render(wantParts)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seed != 5 || got.Avatar.Parts.Color != "#12B5A6" || !reflect.DeepEqual(got.Avatar.Sprite, wantSprite) {
		t.Fatalf("seeded result = %+v", got)
	}

	// Render proves the parts round trip in the body colour.
	parts := avatar.RandomParts(7)
	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "render", "parts": parts, "color": "blue"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("render status = %d, body = %s", rr.code, rr.body)
	}
	got = generateAvatarResponse{}
	avatarJSON(t, rr.body, &got)
	if got.Avatar.Kind != avatar.KindParts || got.Avatar.Parts.Color != "blue" || got.Source != avatar.SourceLocal {
		t.Fatalf("render result = %+v", got)
	}
	renderParts := avatar.RandomParts(7)
	renderParts.Color = "blue"
	renderSprite, err := avatar.Render(renderParts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Avatar.Sprite, renderSprite) {
		t.Fatal("render sprite does not match the parts")
	}

	// Render without a colour uses the agent colour.
	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "render", "parts": avatar.RandomParts(3)}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("render default status = %d, body = %s", rr.code, rr.body)
	}
	got = generateAvatarResponse{}
	avatarJSON(t, rr.body, &got)
	if got.Avatar.Parts.Color != core.ResolveAgentColor("coder", "") {
		t.Fatalf("render default colour = %q", got.Avatar.Parts.Color)
	}

	// A bad part value is 400 invalid_request with the reason.
	bad := avatar.RandomParts(1)
	bad.Species = "dragon"
	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "render", "parts": bad}, cookie, csrf)
	if rr.code != http.StatusBadRequest || !strings.Contains(string(rr.body), "species") {
		t.Fatalf("bad parts: %d %s", rr.code, rr.body)
	}
	if code := decodeError(t, rr).Error.Code; code != "invalid_request" {
		t.Fatalf("bad parts code = %q", code)
	}

	// Other bad requests.
	for _, body := range []map[string]any{
		{"mode": "render"},
		{"mode": "nope"},
		{"mode": "random", "seed": 0},
		{"mode": "random", "seed": 1 << 40},
		{"mode": "random", "prompt": strings.Repeat("x", maxAvatarPromptRunes+1)},
		{"mode": "random", "color": "green"},
	} {
		rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", body, cookie, csrf)
		if rr.code != http.StatusBadRequest {
			t.Fatalf("%v status = %d, body = %s", body, rr.code, rr.body)
		}
		if code := decodeError(t, rr).Error.Code; code != "invalid_request" {
			t.Fatalf("%v code = %q", body, code)
		}
	}

	// Unknown agents and teams are 404.
	for _, target := range []string{"/v1/agents/ghost/avatar/generate", "/v1/agents/crew/avatar/generate"} {
		if rr = avatarRequest(t, s, http.MethodPost, target, map[string]any{"mode": "random"}, cookie, csrf); rr.code != http.StatusNotFound {
			t.Fatalf("%s status = %d", target, rr.code)
		}
	}
}

// API-PXA-002, REQ-PXA-007: the ai modes go through the backend, map refused
// models to 400 invalid_model, and fall back to local when the daemon does not
// know the agent.
func TestAvatarGenerateAI(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)
	var calls []string
	backend := &avatarTestBackend{fakeWebBackend: newFakeWebBackend(), generate: func(_ context.Context, agent, mode, model, prompt string, seed int64, color string) (avatar.Result, error) {
		calls = append(calls, agent+"|"+mode+"|"+model)
		switch model {
		case "nope/x":
			return avatar.Result{}, avatar.ErrUnknownModel
		case "boom/x":
			return avatar.Result{}, errors.New("boom")
		case "late/x":
			return avatar.Result{}, ErrUnknownAgent
		}
		p := avatar.RandomParts(seed)
		p.Color = color
		sprite, err := avatar.Render(p)
		if err != nil {
			return avatar.Result{}, err
		}
		return avatar.Result{Avatar: avatar.Document{Kind: avatar.KindParts, Parts: &p, Sprite: sprite}, Model: "deepseek/deepseek-chat", Seed: seed, Source: avatar.SourceModel}, nil
	}}
	s.SetBackend(backend)

	rr := avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "ai_parts", "model": " deepseek/deepseek-chat ", "prompt": "a wizard"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("ai_parts status = %d, body = %s", rr.code, rr.body)
	}
	var got generateAvatarResponse
	avatarJSON(t, rr.body, &got)
	if got.Source != avatar.SourceModel || got.Model != "deepseek/deepseek-chat" || got.Avatar.Kind != avatar.KindParts {
		t.Fatalf("ai_parts result = %+v", got)
	}
	if got.Avatar.Parts.Color != core.ResolveAgentColor("coder", "") {
		t.Fatalf("ai_parts colour = %q", got.Avatar.Parts.Color)
	}

	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "ai_draw", "color": "#123456"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("ai_draw status = %d, body = %s", rr.code, rr.body)
	}
	got = generateAvatarResponse{}
	avatarJSON(t, rr.body, &got)
	if got.Source != avatar.SourceModel || got.Avatar.Parts.Color != "#123456" {
		t.Fatalf("ai_draw result = %+v", got)
	}
	if want := []string{"coder|ai_parts|deepseek/deepseek-chat", "coder|ai_draw|"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}

	for model, code := range map[string]int{"nope/x": http.StatusBadRequest, "boom/x": http.StatusInternalServerError} {
		rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "ai_parts", "model": model}, cookie, csrf)
		if rr.code != code {
			t.Fatalf("%s status = %d, body = %s", model, rr.code, rr.body)
		}
		if code == http.StatusBadRequest && decodeError(t, rr).Error.Code != "invalid_model" {
			t.Fatalf("%s body = %s", model, rr.body)
		}
	}

	rr = avatarRequest(t, s, http.MethodPost, "/v1/agents/coder/avatar/generate", map[string]any{"mode": "ai_draw", "model": "late/x", "seed": 9}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("unknown to daemon: %d %s", rr.code, rr.body)
	}
	got = generateAvatarResponse{}
	avatarJSON(t, rr.body, &got)
	if got.Source != avatar.SourceLocal || got.Seed != 9 {
		t.Fatalf("fallback result = %+v", got)
	}
}

// API-PXA-003, API-PXA-004, API-PXA-005: PUT stores the avatar and the color,
// the rows show them, DELETE goes back to the default, and a tampered file
// falls back.
func TestAvatarPutDeleteRoundTrip(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)
	path := agenthome.Paths(store.home, "coder").Avatar
	color := core.ResolveAgentColor("coder", "")
	base := avatar.DefaultFor("coder", color)

	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder"); row.AvatarSource != "default" || !reflect.DeepEqual(row.Avatar, base) {
		t.Fatalf("initial avatar source = %q", row.AvatarSource)
	}

	// A parts avatar saves canonically in the requested colour.
	rr := avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"avatar": base, "color": "#12B5A6"}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rr.code, rr.body)
	}
	var put avatarResponse
	avatarJSON(t, rr.body, &put)
	if put.Name != "coder" || put.Avatar.Kind != avatar.KindParts || put.Avatar.Parts.Color != "#12B5A6" || put.AvatarSource != "saved" || put.Color != "#12B5A6" {
		t.Fatalf("PUT response = %+v", put)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("avatar.json: %v", err)
	}
	var stored avatar.Document
	avatarJSON(t, data, &stored)
	if stored.Kind != avatar.KindParts || stored.Parts.Color != "#12B5A6" {
		t.Fatalf("avatar.json = %+v", stored)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("avatar.json mode = %v, %v", info, err)
	}
	record := agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"])
	if record.Color != "#12B5A6" || record.Model != "coder" || len(record.Tools) != 2 {
		t.Fatalf("saved record = %+v", record)
	}
	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.Avatar.Parts.Color != "#12B5A6" || row.AvatarSource != "saved" || row.Color != "#12B5A6" {
		t.Fatalf("listed row = %+v", row)
	}
	if other := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "helper"); other.AvatarSource != "default" || !reflect.DeepEqual(other.Avatar, avatar.DefaultFor("helper", core.ResolveAgentColor("helper", ""))) {
		t.Fatalf("helper avatar source = %q", other.AvatarSource)
	}

	// Without color the key and the colour are kept.
	rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"avatar": base}, cookie, csrf)
	if rr.code != http.StatusOK || agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"]).Color != "#12B5A6" {
		t.Fatalf("PUT without color: %d %s", rr.code, rr.body)
	}
	put = avatarResponse{}
	avatarJSON(t, rr.body, &put)
	if put.Avatar.Parts.Color != "#12B5A6" {
		t.Fatalf("PUT without color avatar = %+v", put.Avatar)
	}

	// A token colour is accepted and applied to the sprite.
	rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"avatar": base, "color": "blue"}, cookie, csrf)
	if rr.code != http.StatusOK || agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"]).Color != "blue" {
		t.Fatalf("PUT token color: %d %s", rr.code, rr.body)
	}
	put = avatarResponse{}
	avatarJSON(t, rr.body, &put)
	if put.Color != "blue" || put.Avatar.Parts.Color != "blue" {
		t.Fatalf("PUT token avatar = %+v", put)
	}

	// A drawn sprite round-trips as it is.
	rr = avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"avatar": avatar.Document{Kind: avatar.KindDrawn, Sprite: base.Sprite, Prompt: "a hero"}}, cookie, csrf)
	if rr.code != http.StatusOK {
		t.Fatalf("PUT drawn: %d %s", rr.code, rr.body)
	}
	put = avatarResponse{}
	avatarJSON(t, rr.body, &put)
	if put.Avatar.Kind != avatar.KindDrawn || !reflect.DeepEqual(put.Avatar.Sprite, base.Sprite) {
		t.Fatalf("PUT drawn avatar = %+v", put.Avatar)
	}

	// Refusals change nothing.
	before, _ := os.ReadFile(store.path)
	beforeAvatar, _ := os.ReadFile(path)
	for _, body := range []map[string]any{
		{"avatar": avatar.Document{Kind: avatar.KindParts}},
		{"avatar": avatar.Document{Kind: avatar.KindDrawn, Sprite: avatar.Sprite{}}},
		{"avatar": base, "color": "#12B5A"},
		{"avatar": base, "color": "green"},
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
	if data, _ := os.ReadFile(path); string(data) != string(beforeAvatar) {
		t.Fatal("a refused PUT changed avatar.json")
	}
	for target := range map[string]bool{"/v1/agents/crew/avatar": true, "/v1/agents/ghost/avatar": true} {
		if rr = avatarRequest(t, s, http.MethodPut, target, map[string]any{"avatar": base}, cookie, csrf); rr.code != http.StatusNotFound {
			t.Fatalf("%s status = %d", target, rr.code)
		}
	}

	// A tampered file is ignored and the default shows.
	if err := os.WriteFile(path, []byte(`{"kind":"parts"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder"); row.AvatarSource != "default" || !reflect.DeepEqual(row.Avatar, avatar.DefaultFor("coder", "blue")) {
		t.Fatalf("tampered avatar source = %q", row.AvatarSource)
	}

	// DELETE is idempotent and removes the file.
	for i := 0; i < 2; i++ {
		rr = avatarRequest(t, s, http.MethodDelete, "/v1/agents/coder/avatar", nil, cookie, csrf)
		var del avatarResponse
		if rr.code != http.StatusOK || json.Unmarshal(rr.body, &del) != nil || del.AvatarSource != "default" || del.Color != "blue" || !reflect.DeepEqual(del.Avatar, avatar.DefaultFor("coder", "blue")) {
			t.Fatalf("DELETE %d: %d %s", i, rr.code, rr.body)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("avatar.json still exists: %v", err)
	}
	if row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder"); row.AvatarSource != "default" {
		t.Fatalf("deleted avatar source = %q", row.AvatarSource)
	}
}

// Spec 43 extends API-ROOM-006: PUT /v1/agents/{name} accepts a hex color.
func TestAgentsHexColor(t *testing.T) {
	if !core.ValidAgentColor("#a1B2c3") || core.ValidAgentColor("#a1b2c") || core.ValidAgentColor("a1b2c3") || core.ValidAgentColor("#a1b2c3d4") {
		t.Fatal("ValidAgentColor hex rules")
	}
}

// API-PXA-006: avatar.svg serves frame 0 with the outline, the bust by default
// and the full sprite for ?view=full, with an ETag, and 404 for an unknown
// name.
func TestAvatarImage(t *testing.T) {
	s, _, cookie, csrf := newT016AgentsServer(t, avatarTestConfig)
	get := func(target, etag string) *httptest.ResponseRecorder {
		req := settingsRequest(http.MethodGet, target, "", cookie, "")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		return serveAuth(s, req)
	}
	color := core.ResolveAgentColor("coder", "")
	doc := avatar.DefaultFor("coder", color)
	bust := avatar.SVG(doc.Sprite, "")
	rr := get("/v1/agents/coder/avatar.svg", "")
	if rr.Code != http.StatusOK || rr.Body.String() != bust || rr.Header().Get("Content-Type") != "image/svg+xml" ||
		rr.Header().Get("Cache-Control") != "private, max-age=60" || rr.Header().Get("Content-Security-Policy") != "default-src 'none'" {
		t.Fatalf("bust: %d %v %s", rr.Code, rr.Header(), rr.Body.String())
	}
	if !strings.Contains(bust, `viewBox="0 -1 16 16"`) {
		t.Fatalf("bust viewBox missing: %s", bust)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if rr = get("/v1/agents/coder/avatar.svg", etag); rr.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d", rr.Code)
	}

	// The full view adds the ground bar and its own viewBox.
	full := avatar.SVG(doc.Sprite, "full")
	rr = get("/v1/agents/coder/avatar.svg?view=full", "")
	if rr.Body.String() != full || !strings.Contains(full, `viewBox="-0.5 -1.5 17 21"`) || rr.Header().Get("ETag") == etag {
		t.Fatalf("full: %s", rr.Body.String())
	}
	// The theme query is accepted and ignored.
	if rr = get("/v1/agents/coder/avatar.svg?theme=dark", ""); rr.Body.String() != bust {
		t.Fatalf("theme ignored: %s", rr.Body.String())
	}

	// A saved avatar is served from avatar.json.
	saved := avatarRequest(t, s, http.MethodPut, "/v1/agents/coder/avatar", map[string]any{"avatar": doc, "color": "#FF6A00"}, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("PUT: %d %s", saved.code, saved.body)
	}
	var put avatarResponse
	avatarJSON(t, saved.body, &put)
	if got := get("/v1/agents/coder/avatar.svg", ""); got.Body.String() != avatar.SVG(put.Avatar.Sprite, "") {
		t.Fatalf("saved: %s", got.Body.String())
	}

	for _, target := range []string{"/v1/agents/ghost/avatar.svg", "/v1/agents/Bad..Name/avatar.svg"} {
		if rr := get(target, ""); rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d", target, rr.Code)
		}
	}
}

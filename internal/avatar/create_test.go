package avatar

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeRouter answers Provider and Resolve for the create tests and serves a
// canned model reply through StreamChain. answer is called with the 0-based
// attempt number; every call is recorded in metas and reqs.
type fakeRouter struct {
	known  map[string]bool
	answer func(n int) string
	metas  []core.LLMCallMeta
	reqs   []core.ChatRequest
}

func (r *fakeRouter) Provider(name string) (core.Provider, bool) {
	if r.known[name] {
		return fakeProvider{}, true
	}
	return nil, false
}

func (r *fakeRouter) Names() []string { return []string{"deepseek"} }

func (r *fakeRouter) Resolve(address string) ([]core.LLMTarget, error) {
	i := -1
	for j := 0; j < len(address); j++ {
		if address[j] == '/' || address[j] == ':' {
			i = j
			break
		}
	}
	if i <= 0 {
		return nil, errors.New("unknown")
	}
	if !r.known[address[:i]] {
		return nil, errors.New("unknown")
	}
	return []core.LLMTarget{{Provider: address[:i], Model: address[i+1:]}}, nil
}

func (r *fakeRouter) StreamChain(_ context.Context, chain []core.LLMTarget, req core.ChatRequest, meta core.LLMCallMeta, _ func(core.LLMAttempt)) (core.ChatStream, core.LLMTarget, error) {
	n := len(r.metas)
	r.metas = append(r.metas, meta)
	r.reqs = append(r.reqs, req)
	text := ""
	if r.answer != nil {
		text = r.answer(n)
	}
	target := core.LLMTarget{Provider: "deepseek", Model: "chat"}
	if len(chain) > 0 {
		target = chain[0]
	}
	stream := func(yield func(core.ChatEvent, error) bool) {
		if text == "" {
			return
		}
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil)
	}
	return stream, target, nil
}

func (r *fakeRouter) Embed(context.Context, string, []string, core.LLMCallMeta) ([][]float32, error) {
	return nil, nil
}

type fakeProvider struct{}

func (fakeProvider) Stream(context.Context, core.ChatRequest) (core.ChatStream, error) {
	return nil, errors.New("not used")
}

// REQ-PXA-007: without a model target Create returns the local parts.
func TestCreateLocalWithoutLLM(t *testing.T) {
	got, err := Create(context.Background(), nil, Request{Agent: "coder", Mode: ModeAIParts, Seed: 3, Color: "#123456"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceLocal || got.Model != "" || got.Seed != 3 {
		t.Fatalf("Create = %+v", got)
	}
	if got.Avatar.Kind != KindParts || got.Avatar.Parts == nil || got.Avatar.Parts.Color != "#123456" {
		t.Fatalf("avatar = %+v", got.Avatar)
	}
	if err := ValidateSprite(got.Avatar.Sprite); err != nil {
		t.Fatalf("local sprite: %v", err)
	}
}

// REQ-PXA-007: an agent with only a background-less Claude Code config still
// draws locally.
func TestCreateLocalWithoutBackground(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"claude-code": true}}
	got, err := Create(context.Background(), r, Request{Config: core.AgentConfig{Model: "claude-code/opus"}, Mode: ModeAIParts, Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceLocal {
		t.Fatalf("Create = %+v", got)
	}
	if len(r.metas) != 0 {
		t.Fatalf("Create called the model without a background target: %+v", r.metas)
	}
}

// REQ-PXA-007: a requested Claude Code model is refused.
func TestCreateRefusesClaudeCode(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"claude-code": true, "deepseek": true}}
	if _, err := Create(context.Background(), r, Request{Model: "claude-code/sonnet", Mode: ModeAIParts, Seed: 3}); !errors.Is(err, ErrClaudeCodeModel) {
		t.Fatalf("claude-code model: %v", err)
	}
	if _, err := Create(context.Background(), r, Request{Model: "nope/x", Mode: ModeAIParts, Seed: 3}); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("unknown model: %v", err)
	}
}

// API-PXA-002: an unknown mode is refused.
func TestCreateUnknownMode(t *testing.T) {
	for _, mode := range []string{"", "random", "render", "draw"} {
		if _, err := Create(context.Background(), nil, Request{Mode: mode, Seed: 3}); !errors.Is(err, ErrInvalidMode) {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
}

// REQ-PXA-005: a valid JSON parts reply wrapped in prose and a code fence
// gives a model result with those parts.
func TestCreatePartsFromModel(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"deepseek": true}}
	r.answer = func(int) string {
		return "Sure! Here you go:\n```json\n" +
			`{"species":"robot","hair":"bun","hair_color":"pink","skin":"deep","eyes":"happy","accessory":"glasses","outfit":"tie","pants":"khaki","blush":true,"accessory_color":"#AABBCC"}` +
			"\n```\n"
	}
	got, err := Create(context.Background(), r, Request{
		Agent: "coder", Config: core.AgentConfig{Model: "deepseek/chat"},
		Mode: ModeAIParts, Prompt: "a pink-haired robot", Seed: 7, Color: "#123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceModel || got.Model != "deepseek/chat" || got.Seed != 7 {
		t.Fatalf("Create = %+v", got)
	}
	if got.Avatar.Kind != KindParts || got.Avatar.Parts == nil {
		t.Fatalf("avatar = %+v", got.Avatar)
	}
	p := got.Avatar.Parts
	if p.Species != "robot" || p.Hair != "bun" || p.HairColor != "pink" || p.Skin != "deep" ||
		p.Eyes != "happy" || p.Accessory != "glasses" || p.Outfit != "tie" || p.Pants != "khaki" || !p.Blush {
		t.Fatalf("parts = %+v", p)
	}
	if p.AccessoryColor != "#aabbcc" {
		t.Fatalf("accessory colour not lower-cased: %q", p.AccessoryColor)
	}
	if p.Color != "#123456" {
		t.Fatalf("agent colour not applied: %q", p.Color)
	}
	if err := ValidateSprite(got.Avatar.Sprite); err != nil {
		t.Fatalf("model sprite: %v", err)
	}
}

// REQ-PXA-005: missing and unknown values are filled from RandomParts(seed).
func TestCreatePartsFillsMissingAndUnknown(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"deepseek": true}}
	r.answer = func(int) string {
		return `{"species":"robot","hair":"mullet","blush":false,"accessory_color":"red"}`
	}
	got, err := Create(context.Background(), r, Request{
		Agent: "coder", Config: core.AgentConfig{Model: "deepseek/chat"},
		Mode: ModeAIParts, Seed: 7, Color: "#123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceModel {
		t.Fatalf("Create = %+v", got)
	}
	base := RandomParts(7)
	p := got.Avatar.Parts
	if p.Species != "robot" {
		t.Fatalf("known species lost: %q", p.Species)
	}
	if p.Hair != base.Hair {
		t.Fatalf("unknown hair = %q, want filled %q", p.Hair, base.Hair)
	}
	if p.HairColor != base.HairColor || p.Skin != base.Skin || p.Eyes != base.Eyes ||
		p.Accessory != base.Accessory || p.Outfit != base.Outfit || p.Pants != base.Pants {
		t.Fatalf("missing values not filled: %+v (base %+v)", p, base)
	}
	if p.Blush {
		t.Fatal("explicit blush=false was not kept")
	}
	if p.AccessoryColor != base.AccessoryColor {
		t.Fatalf("bad accessory colour = %q, want filled %q", p.AccessoryColor, base.AccessoryColor)
	}
	if err := ValidateSprite(got.Avatar.Sprite); err != nil {
		t.Fatalf("model sprite: %v", err)
	}
}

// REQ-PXA-007: a reply with no JSON object is retried once and then falls back
// to the local parts.
func TestCreatePartsJunkFallsBack(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"deepseek": true}}
	r.answer = func(int) string { return "I can't draw that." }
	got, err := Create(context.Background(), r, Request{
		Agent: "coder", Config: core.AgentConfig{Model: "deepseek/chat"},
		Mode: ModeAIParts, Seed: 7, Color: "#123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceLocal || got.Model != "" || got.Seed != 7 {
		t.Fatalf("Create = %+v", got)
	}
	if got.Avatar.Kind != KindParts || got.Avatar.Parts == nil || got.Avatar.Parts.Color != "#123456" {
		t.Fatalf("avatar = %+v", got.Avatar)
	}
	if err := ValidateSprite(got.Avatar.Sprite); err != nil {
		t.Fatalf("local sprite: %v", err)
	}
	if len(r.metas) != 2 {
		t.Fatalf("the model was called %d times, want 2", len(r.metas))
	}
}

// REQ-PXA-006: a valid sprite reply gives a drawn model result with the prompt
// kept.
func TestCreateDrawFromModel(t *testing.T) {
	sprite := cloneSprite(DefaultFor("x", "").Sprite)
	sprite.Palette["h"] = "#ff00ff"
	data, err := json.Marshal(sprite)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRouter{known: map[string]bool{"deepseek": true}}
	r.answer = func(int) string { return "Here you go:\n" + string(data) }
	got, err := Create(context.Background(), r, Request{
		Agent: "x", Config: core.AgentConfig{Model: "deepseek/chat"},
		Mode: ModeAIDraw, Prompt: "a wizard", Seed: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceModel || got.Model != "deepseek/chat" || got.Seed != 9 {
		t.Fatalf("Create = %+v", got)
	}
	if got.Avatar.Kind != KindDrawn || got.Avatar.Parts != nil {
		t.Fatalf("avatar = %+v", got.Avatar)
	}
	if got.Avatar.Prompt != "a wizard" {
		t.Fatalf("prompt = %q", got.Avatar.Prompt)
	}
	if got.Avatar.Sprite.Palette["h"] != "#ff00ff" {
		t.Fatalf("sprite palette = %+v", got.Avatar.Sprite.Palette)
	}
	if err := ValidateSprite(got.Avatar.Sprite); err != nil {
		t.Fatalf("drawn sprite: %v", err)
	}
}

// REQ-PXA-007: an invalid sprite twice falls back to the local parts.
func TestCreateDrawInvalidFallsBack(t *testing.T) {
	sprite := cloneSprite(DefaultFor("x", "").Sprite)
	sprite.Head = sprite.Head[:spriteH-1]
	data, err := json.Marshal(sprite)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRouter{known: map[string]bool{"deepseek": true}}
	r.answer = func(int) string { return string(data) }
	got, err := Create(context.Background(), r, Request{
		Agent: "x", Config: core.AgentConfig{Model: "deepseek/chat"},
		Mode: ModeAIDraw, Prompt: "a wizard", Seed: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceLocal || got.Model != "" || got.Seed != 9 {
		t.Fatalf("Create = %+v", got)
	}
	if got.Avatar.Kind != KindParts {
		t.Fatalf("fallback kind = %q", got.Avatar.Kind)
	}
	if len(r.metas) != 2 {
		t.Fatalf("the model was called %d times, want 2", len(r.metas))
	}
}

// REQ-PXA-007: the call record carries source "avatar", the agent, and the
// requested model (the background value, or the requested address).
func TestCreateCallMeta(t *testing.T) {
	reply := `{"species":"human"}`
	background := &fakeRouter{known: map[string]bool{"deepseek": true}, answer: func(int) string { return reply }}
	if _, err := Create(context.Background(), background, Request{
		Agent: "coder", Config: core.AgentConfig{Model: "deepseek/chat"}, Mode: ModeAIParts, Seed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if len(background.metas) == 0 {
		t.Fatal("no call recorded")
	}
	meta := background.metas[0]
	if meta.Source != CallSource || meta.Agent != "coder" || meta.Requested != "deepseek/chat" {
		t.Fatalf("background meta = %+v", meta)
	}
	if meta.Source != "avatar" {
		t.Fatalf("call record source = %q", meta.Source)
	}

	requested := &fakeRouter{known: map[string]bool{"deepseek": true}, answer: func(int) string { return reply }}
	if _, err := Create(context.Background(), requested, Request{
		Agent: "coder", Model: "deepseek/chat", Mode: ModeAIParts, Seed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if requested.metas[0].Requested != "deepseek/chat" {
		t.Fatalf("requested meta = %+v", requested.metas[0])
	}
}

// REQ-PXA-005: the parts system prompt lists every catalog id.
func TestCreatePartsPromptListsCatalog(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"deepseek": true}, answer: func(int) string {
		return `{"species":"human"}`
	}}
	if _, err := Create(context.Background(), r, Request{
		Agent: "coder", Config: core.AgentConfig{Model: "deepseek/chat"}, Mode: ModeAIParts, Seed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	system := r.reqs[0].System
	c := Catalog()
	for _, items := range [][]CatalogItem{c.Species, c.Hair, c.HairColor, c.Skin, c.Eyes, c.Accessory, c.Outfit, c.Pants} {
		for _, item := range items {
			if !strings.Contains(system, item.ID) {
				t.Fatalf("the parts prompt is missing the catalog id %q", item.ID)
			}
		}
	}
	if !strings.Contains(r.reqs[0].Messages[0].Content[0].Text, "Variation seed: 7") {
		t.Fatalf("the parts user message has no seed: %q", r.reqs[0].Messages[0].Content[0].Text)
	}
}

// REQ-PXA-006: the draw system prompt shows the agent's default sprite as a
// worked example with one row per line.
func TestCreateDrawPromptHasExample(t *testing.T) {
	sprite := DefaultFor("x", "#123456").Sprite
	data, err := json.Marshal(sprite)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRouter{known: map[string]bool{"deepseek": true}, answer: func(int) string { return string(data) }}
	if _, err := Create(context.Background(), r, Request{
		Agent: "x", Config: core.AgentConfig{Model: "deepseek/chat"},
		Mode: ModeAIDraw, Color: "#123456", Seed: 9,
	}); err != nil {
		t.Fatal(err)
	}
	system := r.reqs[0].System
	if !strings.Contains(system, "Example:") {
		t.Fatal("the draw prompt has no example")
	}
	for _, row := range []string{sprite.Head[0], sprite.Head[7], sprite.Body[13]} {
		if !strings.Contains(system, row) {
			t.Fatalf("the draw prompt is missing the example row %q", row)
		}
	}
	if !strings.Contains(r.reqs[0].Messages[0].Content[0].Text, "Variation seed: 9") {
		t.Fatalf("the draw user message has no seed: %q", r.reqs[0].Messages[0].Content[0].Text)
	}
}

// REQ-PXA-005: the fallback user message is used when the description is empty.
func TestCreateEmptyPrompt(t *testing.T) {
	r := &fakeRouter{known: map[string]bool{"deepseek": true}, answer: func(int) string {
		return `{"species":"human"}`
	}}
	if _, err := Create(context.Background(), r, Request{
		Config: core.AgentConfig{Model: "deepseek/chat"}, Mode: ModeAIParts, Seed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.reqs[0].Messages[0].Content[0].Text, partsFallbackPrompt) {
		t.Fatalf("empty description did not use the fallback: %q", r.reqs[0].Messages[0].Content[0].Text)
	}
}

// API-PXA-002: a seed of 0 draws a fresh seed in 1..2^31-1.
func TestCreateRandomSeed(t *testing.T) {
	got, err := Create(context.Background(), nil, Request{Mode: ModeAIParts, Seed: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Seed < 1 || got.Seed > int64(maxSeed) {
		t.Fatalf("random seed = %d", got.Seed)
	}
}

package avatar

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

const goodSVG = `<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 100 100" width="100" height="100">
  <title>bob</title>
  <!-- a body -->
  <path d="M50 12C72 12 88 28 88 50S72 88 50 88 12 72 12 50 28 12 50 12z" fill="currentColor" stroke="#000" opacity="0.9"/>
  <rect x="36" y="38" width="7" height="16" rx="3.5" fill="#FFF" transform="rotate(-12 39.5 46)"/>
  <ellipse cx="60" cy="46" rx="4" ry="7" fill="white"/>
</svg>`

func TestSanitizeAcceptsAndCanonicalizes(t *testing.T) {
	got, err := Sanitize("Here you go:\n```svg\n" + goodSVG + "\n```")
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	want := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
		`<path d="M 50 12 C 72 12 88 28 88 50 S 72 88 50 88 S 12 72 12 50 S 28 12 50 12 z" fill="currentColor"/>` +
		`<rect x="36" y="38" width="7" height="16" rx="3.5" fill="#ffffff" transform="rotate(-12 39.5 46)"/>` +
		`<ellipse cx="60" cy="46" rx="4" ry="7" fill="#ffffff"/></svg>`
	if got != want {
		t.Fatalf("Sanitize =\n%s\nwant\n%s", got, want)
	}
	again, err := Sanitize(got)
	if err != nil || again != got {
		t.Fatalf("Sanitize is not idempotent: %v\n%s", err, again)
	}
}

func TestSanitizeGroupsAndInheritedFill(t *testing.T) {
	in := `<svg viewBox="0,0,100,100"><g fill="currentColor"><circle cx="50" cy="50" r="38"/></g>` +
		`<g transform="translate(2 0)" fill="#ffffff"><ellipse cx="40" cy="45" rx="4" ry="8"/><ellipse cx="58" cy="45" rx="4" ry="8"/></g>` +
		`<circle cx="50" cy="70" r="3" fill="#ff0000"/></svg>`
	got, err := Sanitize(in)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	want := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
		`<circle cx="50" cy="50" r="38" fill="currentColor"/>` +
		`<g transform="translate(2 0)"><ellipse cx="40" cy="45" rx="4" ry="8" fill="#ffffff"/><ellipse cx="58" cy="45" rx="4" ry="8" fill="#ffffff"/></g></svg>`
	if got != want {
		t.Fatalf("Sanitize =\n%s\nwant\n%s", got, want)
	}
}

func TestSanitizeRejects(t *testing.T) {
	body := `<circle cx="50" cy="50" r="38" fill="currentColor"/>`
	eyes := `<ellipse cx="40" cy="45" rx="4" ry="8" fill="#fff"/><ellipse cx="60" cy="45" rx="4" ry="8" fill="#fff"/>`
	wrap := func(inner string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` + inner + `</svg>`
	}
	cases := map[string]string{
		"script":        wrap(body + eyes + `<script>alert(1)</script>`),
		"nested script": wrap(`<g><title><script>x</script></title></g>` + body + eyes),
		"onload":        `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" onload="alert(1)">` + body + eyes + `</svg>`,
		"href":          wrap(body + eyes + `<a href="https://x.test"><circle cx="1" cy="1" r="1" fill="#fff"/></a>`),
		"xlink href":    wrap(body + `<ellipse cx="40" cy="45" rx="4" ry="8" fill="#fff" xlink:href="#x"/><ellipse cx="60" cy="45" rx="4" ry="8" fill="#fff"/>`),
		"use":           wrap(body + eyes + `<use x="1"/>`),
		"image":         wrap(body + eyes + `<image x="0" y="0" width="10" height="10"/>`),
		"style attr":    wrap(`<circle cx="50" cy="50" r="38" fill="currentColor" style="fill:red"/>` + eyes),
		"style element": wrap(`<style>circle{fill:red}</style>` + body + eyes),
		"external fill": wrap(`<circle cx="50" cy="50" r="38" fill="url(https://x.test/a.svg#g)"/>` + eyes),
		"foreignObject": wrap(body + eyes + `<foreignObject><div/></foreignObject>`),
		"doctype":       `<!DOCTYPE svg [<!ENTITY a "x">]>` + wrap(body+eyes),
		"entity use":    wrap(body + eyes + `<desc>&a;</desc>`),
		"proc inst":     wrap(`<?xml-stylesheet href="x.css"?>` + body + eyes),
		"three eyes":    wrap(body + eyes + `<ellipse cx="50" cy="60" rx="4" ry="8" fill="#fff"/>`),
		"one eye":       wrap(body + `<ellipse cx="40" cy="45" rx="4" ry="8" fill="#fff"/>`),
		"no body":       wrap(`<circle cx="50" cy="50" r="38" fill="#123456"/>` + eyes),
		"far outside":   wrap(`<circle cx="500" cy="50" r="38" fill="currentColor"/>` + eyes),
		"path outside":  wrap(`<path d="M 50 10 L 400 50 L 50 90 Z" fill="currentColor"/>` + eyes),
		"bad viewBox":   `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` + body + eyes + `</svg>`,
		"matrix":        wrap(`<circle cx="50" cy="50" r="38" fill="currentColor" transform="matrix(1 0 0 1 0 0)"/>` + eyes),
		"bad path":      wrap(`<path d="M 50 10 X 3" fill="currentColor"/>` + eyes),
		"eye outside":   wrap(`<circle cx="30" cy="30" r="20" fill="currentColor"/><ellipse cx="80" cy="85" rx="4" ry="8" fill="#fff"/><ellipse cx="85" cy="85" rx="4" ry="8" fill="#fff"/>`),
		"huge eye":      wrap(body + `<ellipse cx="45" cy="50" rx="30" ry="30" fill="#fff"/><ellipse cx="60" cy="45" rx="4" ry="8" fill="#fff"/>`),
		"oversized":     wrap(body + eyes + `<desc>` + strings.Repeat("x", MaxSize) + `</desc>`),
		"long path":     wrap(`<path d="M 50 10` + strings.Repeat(" L 50 10", 700) + ` Z" fill="currentColor"/>` + eyes),
		"not xml":       `<svg viewBox="0 0 100 100"><circle></svg>`,
		"no svg":        `I cannot draw that.`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Sanitize(in)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Sanitize = %q, %v; want ErrInvalid", got, err)
			}
		})
	}
}

func TestPathArcFlagsWithoutSeparators(t *testing.T) {
	cmds, err := parsePath("M10 50a40 40 0 1150 0a40 40 0 1 1-50 0z")
	if err != nil {
		t.Fatal(err)
	}
	if got := formatPath(cmds); got != "M 10 50 a 40 40 0 1 1 50 0 a 40 40 0 1 1 -50 0 z" {
		t.Fatalf("formatPath = %q", got)
	}
}

// REQ-AVT-005: every seed draws a valid, canonical avatar.
func TestGenerateValidForManySeeds(t *testing.T) {
	families := map[string]int{}
	for seed := int64(1); seed <= 500; seed++ {
		svg := Generate(seed)
		got, err := Sanitize(svg)
		if err != nil {
			t.Fatalf("seed %d (%s): %v\n%s", seed, FamilyFor(seed), err, svg)
		}
		if got != svg {
			t.Fatalf("seed %d: Generate is not canonical\n%s\n%s", seed, svg, got)
		}
		if len(svg) > 2500 {
			t.Fatalf("seed %d: %d bytes", seed, len(svg))
		}
		families[FamilyFor(seed)]++
	}
	if len(families) != len(Families) {
		t.Fatalf("families drawn = %v", families)
	}
}

func TestGenerateDeterministicAndVaried(t *testing.T) {
	first, second := Generate(42), Generate(42)
	if first != second {
		t.Fatal("equal seeds gave different avatars")
	}
	seen := map[string]bool{}
	for seed := int64(1); seed <= 50; seed++ {
		seen[Generate(seed)] = true
	}
	if len(seen) != 50 {
		t.Fatalf("50 seeds gave %d distinct avatars", len(seen))
	}
	for _, family := range Families {
		if GenerateFamily(family, 1) == GenerateFamily(family, 2) {
			t.Fatalf("%s: seeds 1 and 2 drew the same shape", family)
		}
	}
}

func TestNextSeed(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 1<<31 - 1, -5} {
		next := NextSeed(seed)
		if next < 1 || next > 1<<31-1 || next == seed {
			t.Fatalf("NextSeed(%d) = %d", seed, next)
		}
	}
}

// fakeRouter answers every call with the next reply and records the calls.
type fakeRouter struct {
	replies []string
	chains  [][]core.LLMTarget
	metas   []core.LLMCallMeta
	reqs    []core.ChatRequest
}

type fakeProvider struct{}

func (fakeProvider) Stream(context.Context, core.ChatRequest) (core.ChatStream, error) {
	return nil, errors.New("not used")
}

func (r *fakeRouter) Provider(name string) (core.Provider, bool) {
	switch name {
	case "deepseek", "claude-code":
		return fakeProvider{}, true
	}
	return nil, false
}
func (r *fakeRouter) Names() []string { return []string{"deepseek"} }
func (r *fakeRouter) Resolve(address string) ([]core.LLMTarget, error) {
	if address == "mixed" {
		return []core.LLMTarget{{Provider: "claude-code", Model: "opus"}, {Provider: "deepseek", Model: "chat"}}, nil
	}
	i := strings.IndexAny(address, "/:")
	if i <= 0 {
		return nil, errors.New("unknown")
	}
	if _, ok := r.Provider(address[:i]); !ok {
		return nil, errors.New("unknown")
	}
	return []core.LLMTarget{{Provider: address[:i], Model: address[i+1:]}}, nil
}
func (r *fakeRouter) StreamChain(_ context.Context, chain []core.LLMTarget, req core.ChatRequest, meta core.LLMCallMeta, _ func(core.LLMAttempt)) (core.ChatStream, core.LLMTarget, error) {
	r.chains = append(r.chains, chain)
	r.metas = append(r.metas, meta)
	r.reqs = append(r.reqs, req)
	reply := ""
	if len(r.replies) > 0 {
		reply, r.replies = r.replies[0], r.replies[1:]
	}
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: reply}, nil)
	}, chain[0], nil
}
func (r *fakeRouter) Embed(context.Context, string, []string, core.LLMCallMeta) ([][]float32, error) {
	return nil, nil
}

func TestCreateModelResult(t *testing.T) {
	r := &fakeRouter{replies: []string{goodSVG}}
	cfg := core.AgentConfig{Model: "deepseek/chat"}
	got, err := Create(context.Background(), r, Request{Agent: "bob", Config: cfg, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceModel || got.Model != "deepseek/chat" || got.Seed != 7 || !strings.HasPrefix(got.SVG, svgOpen) {
		t.Fatalf("Create = %+v", got)
	}
	if len(r.metas) != 1 || r.metas[0].Source != "avatar" || r.metas[0].Agent != "bob" || r.metas[0].Requested != "deepseek/chat" {
		t.Fatalf("metas = %+v", r.metas)
	}
	req := r.reqs[0]
	if req.MaxTokens != maxTokens || req.ReasoningEffort != "off" || !strings.Contains(req.System, "currentColor") ||
		!strings.Contains(req.Messages[0].Content[0].Text, familyHints[FamilyFor(7)]) {
		t.Fatalf("request = %+v", req)
	}
}

func TestCreateInvalidFallsBackToLocal(t *testing.T) {
	r := &fakeRouter{replies: []string{"<svg viewBox=\"0 0 100 100\"><script/></svg>", "no svg here"}}
	got, err := Create(context.Background(), r, Request{Agent: "bob", Config: core.AgentConfig{Model: "deepseek/chat"}, Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceLocal || got.Model != "" || got.SVG != Generate(9) || got.Seed != 9 {
		t.Fatalf("Create = %+v", got)
	}
	if len(r.reqs) != 2 {
		t.Fatalf("calls = %d, want one retry", len(r.reqs))
	}
	if r.reqs[0].Messages[0].Content[0].Text == r.reqs[1].Messages[0].Content[0].Text {
		t.Fatal("the retry did not use a new seed")
	}
}

func TestCreateRetrySucceeds(t *testing.T) {
	r := &fakeRouter{replies: []string{"nope", goodSVG}}
	got, err := Create(context.Background(), r, Request{Config: core.AgentConfig{Model: "deepseek/chat"}, Seed: 9})
	if err != nil || got.Source != SourceModel || got.Seed != NextSeed(9) {
		t.Fatalf("Create = %+v, %v", got, err)
	}
}

func TestCreateModels(t *testing.T) {
	r := &fakeRouter{replies: []string{goodSVG, goodSVG}}
	ccOnly := core.AgentConfig{Model: "claude-code/opus"}
	// A Claude Code agent without background model draws locally.
	got, err := Create(context.Background(), r, Request{Config: ccOnly, Seed: 3})
	if err != nil || got.Source != SourceLocal || len(r.reqs) != 0 {
		t.Fatalf("claude-only agent: %+v, %v, calls %d", got, err, len(r.reqs))
	}
	// A requested Claude Code model is refused.
	if _, err := Create(context.Background(), r, Request{Config: ccOnly, Model: "claude-code/sonnet", Seed: 3}); !errors.Is(err, ErrClaudeCodeModel) {
		t.Fatalf("claude-code model: %v", err)
	}
	if _, err := Create(context.Background(), r, Request{Config: ccOnly, Model: "nope/x", Seed: 3}); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("unknown model: %v", err)
	}
	// A requested model is used for a Claude Code agent; a combo loses its
	// Claude Code targets.
	got, err = Create(context.Background(), r, Request{Config: ccOnly, Model: "mixed", Seed: 3})
	if err != nil || got.Source != SourceModel || len(r.chains[0]) != 1 || r.chains[0][0].Provider != "deepseek" || r.metas[0].Requested != "mixed" {
		t.Fatalf("combo: %+v, %v, %+v", got, err, r.chains)
	}
	// No LLM service at all: local.
	got, err = Create(context.Background(), nil, Request{Seed: 3})
	if err != nil || got.Source != SourceLocal {
		t.Fatalf("nil llm: %+v, %v", got, err)
	}
}

// REQ-AVT-008: the default avatar is stable per name and valid.
func TestDefaultAvatar(t *testing.T) {
	a, b := DefaultFor("coder"), DefaultFor("coder")
	if a != b {
		t.Fatal("the default avatar is not stable")
	}
	// The seed is part of the contract: a name keeps its face across builds.
	if got := SeedForName("coder"); got != 1524374639 {
		t.Fatalf("SeedForName(coder) = %d", got)
	}
	seen := map[string]bool{}
	for _, name := range []string{"coder", "default", "finance", "research", "helper", "lana", "ops", "writer"} {
		svg := DefaultFor(name)
		if got, err := Sanitize(svg); err != nil || got != svg {
			t.Fatalf("%s: %v", name, err)
		}
		seen[svg] = true
	}
	if len(seen) != 8 {
		t.Fatalf("8 names drew %d avatars", len(seen))
	}
}

func TestColorize(t *testing.T) {
	svg := Generate(4)
	got := Colorize(svg, ColorHex("#12B5A6", "light"))
	if strings.Contains(got, "currentColor") || !strings.Contains(got, `fill="#12b5a6"`) || !strings.Contains(got, `fill="#ffffff"`) {
		t.Fatalf("Colorize = %s", got)
	}
	if ColorHex("brand", "light") == ColorHex("brand", "dark") || ColorHex("nope", "light") != ColorHex("brand", "light") {
		t.Fatal("ColorHex tokens")
	}
}

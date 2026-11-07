package anthropic

import (
	"strconv"
	"strings"
)

// Thinking and effort follow the claude-api skill (models table cached
// 2026-10-06, "Thinking & Effort" quick reference):
//
//   - Claude 4.6 and later take adaptive thinking ({type: "adaptive"}) with
//     output_config.effort. budget_tokens is rejected from 4.7 on.
//   - Fable 5/5.1, Mythos, and Opus 5.5 cannot turn thinking off: an explicit
//     {type: "disabled"} is a 400. Effort is the only control.
//   - Opus 5 accepts disabled only at effort high or below, and the skill
//     warns that disabled thinking on it leaks tool calls into text; "off"
//     becomes the lowest effort instead.
//   - Sonnet 5.5 rejects disabled; its off switch is {type: "between_tools"}.
//   - Opus 4.7/4.8 run without thinking when the field is omitted.
//   - From Opus 4.7 on the thinking display defaults to "omitted" (empty
//     thinking text); lana streams thinking to the owner, so it asks for
//     "summarized". 4.6 models summarize by default.
//   - Sampling parameters (temperature) are rejected from Opus 4.7 on and on
//     the 5.x models; 4.6 and older accept them.
//   - Haiku 4.5 and older models use {type: "enabled", budget_tokens: N},
//     which must be at least 1024 and below max_tokens.

// offMode is how a profile sends reasoning effort "off".
type offMode int

const (
	// offOmit sends no thinking field; the model then runs without thinking.
	offOmit offMode = iota
	// offDisabled sends {type: "disabled"}.
	offDisabled
	// offLowEffort keeps adaptive thinking at effort low: the model cannot
	// turn thinking off.
	offLowEffort
	// offBetweenTools sends {type: "between_tools"} (Sonnet 5.5).
	offBetweenTools
)

// profile is the thinking surface of a model family.
type profile struct {
	// adaptive: adaptive thinking with output_config.effort; else budget.
	adaptive bool
	// summarize asks for display "summarized" (the default is "omitted").
	summarize bool
	off       offMode
	// sampling: the model accepts temperature.
	sampling bool
	// noThinking: the model has no extended thinking at all.
	noThinking bool
}

var (
	profileAlwaysOn    = profile{adaptive: true, summarize: true, off: offLowEffort}
	profileOpus5       = profile{adaptive: true, summarize: true, off: offLowEffort}
	profileOpus47      = profile{adaptive: true, summarize: true, off: offOmit}
	profileSonnet55    = profile{adaptive: true, summarize: true, off: offBetweenTools}
	profileDisableable = profile{adaptive: true, summarize: true, off: offDisabled}
	profile46          = profile{adaptive: true, off: offOmit, sampling: true}
	profileBudget      = profile{off: offOmit, sampling: true}
	profileNone        = profile{off: offOmit, sampling: true, noThinking: true}
	// profileUnknown is a model this adapter cannot place: adaptive thinking
	// works on every current model, and no sampling parameter is sent.
	profileUnknown = profile{adaptive: true, off: offOmit}
)

// profileFor returns the thinking profile of a model ID. It accepts dated
// IDs ("claude-haiku-4-5-20251001"), dotted IDs ("claude-sonnet-4.6"), and
// prefixed IDs ("anthropic/claude-opus-5-5", "anthropic.claude-opus-5-5").
func profileFor(model string) profile {
	id := strings.ToLower(strings.TrimSpace(model))
	if i := strings.Index(id, "["); i >= 0 {
		id = id[:i]
	}
	if i := strings.Index(id, "@"); i >= 0 {
		id = id[:i]
	}
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	id = strings.TrimPrefix(id, "anthropic.")
	id = strings.ReplaceAll(id, ".", "-")
	rest, ok := strings.CutPrefix(id, "claude-")
	if !ok {
		return profileUnknown
	}
	parts := strings.Split(rest, "-")
	// Drop a trailing date suffix such as "20251001".
	for len(parts) > 1 && len(parts[len(parts)-1]) >= 6 {
		if _, err := strconv.Atoi(parts[len(parts)-1]); err != nil {
			break
		}
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return profileUnknown
	}
	if _, err := strconv.Atoi(parts[0]); err == nil {
		// Old naming "claude-3-7-sonnet": only 3.7 Sonnet thinks.
		if strings.HasPrefix(rest, "3-7-") {
			return profileBudget
		}
		return profileNone
	}
	family := parts[0]
	switch family {
	case "fable", "mythos":
		return profileAlwaysOn
	}
	if len(parts) < 2 {
		return profileUnknown
	}
	major, err := strconv.Atoi(parts[1])
	if err != nil {
		return profileUnknown
	}
	minor := 0
	if len(parts) >= 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil && n < 10 {
			minor = n
		}
	}
	version := major*10 + minor
	switch family {
	case "opus":
		switch {
		case version >= 55:
			return profileAlwaysOn
		case version >= 50:
			return profileOpus5
		case version >= 47:
			return profileOpus47
		case version == 46:
			return profile46
		default:
			return profileBudget
		}
	case "sonnet":
		switch {
		case version > 55:
			return profileAlwaysOn
		case version == 55:
			return profileSonnet55
		case version >= 50:
			return profileDisableable
		case version >= 46:
			return profile46
		default:
			return profileBudget
		}
	case "haiku":
		switch {
		case version > 55:
			return profileAlwaysOn
		case version >= 50:
			return profileDisableable
		default:
			return profileBudget
		}
	}
	return profileUnknown
}

// thinkingConfig is the request "thinking" field.
type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

// outputConfig is the request "output_config" field.
type outputConfig struct {
	Effort string `json:"effort"`
}

// budgetFor is the budget_tokens of an effort on a budget model.
var budgetFor = map[string]int{"low": 4096, "medium": 8192, "high": 16000, "xhigh": 24000, "max": 32000}

// thinkingPlan is the thinking part of a request.
type thinkingPlan struct {
	thinking *thinkingConfig
	output   *outputConfig
	// maxTokens is the max_tokens to send; a budget model may raise it above
	// the budget.
	maxTokens int
	// sampling: temperature may be sent.
	sampling bool
}

// planThinking maps a reasoning effort ("", "off", "low", "high", "max",
// and the API levels "medium" and "xhigh") to the thinking fields of a model.
// "" keeps the provider default but turns adaptive thinking on where the
// model would otherwise run without it. maxOutput is the model's catalog
// output cap, 0 when unknown.
func planThinking(model, effort string, maxTokens, maxOutput int) thinkingPlan {
	prof := profileFor(model)
	plan := thinkingPlan{maxTokens: maxTokens}
	switch effort {
	case "", "off", "low", "medium", "high", "xhigh", "max":
	default:
		effort = ""
	}
	if prof.noThinking {
		plan.sampling = prof.sampling
		return plan
	}
	if !prof.adaptive {
		budget, ok := budgetFor[effort]
		if !ok {
			plan.sampling = prof.sampling
			return plan
		}
		if plan.maxTokens <= budget {
			plan.maxTokens = budget + 8192
		}
		if maxOutput > 0 && plan.maxTokens > maxOutput {
			plan.maxTokens = maxOutput
		}
		if budget >= plan.maxTokens {
			budget = plan.maxTokens / 2
		}
		if budget < 1024 {
			plan.sampling = prof.sampling
			return plan
		}
		plan.thinking = &thinkingConfig{Type: "enabled", BudgetTokens: budget}
		return plan
	}
	adaptive := &thinkingConfig{Type: "adaptive"}
	if prof.summarize {
		adaptive.Display = "summarized"
	}
	switch effort {
	case "":
		plan.thinking = adaptive
	case "off":
		switch prof.off {
		case offOmit:
			plan.sampling = prof.sampling
		case offDisabled:
			plan.thinking = &thinkingConfig{Type: "disabled"}
			plan.sampling = prof.sampling
		case offLowEffort:
			plan.thinking = adaptive
			plan.output = &outputConfig{Effort: "low"}
		case offBetweenTools:
			plan.thinking = &thinkingConfig{Type: "between_tools"}
		}
	default:
		plan.thinking = adaptive
		plan.output = &outputConfig{Effort: effort}
	}
	return plan
}

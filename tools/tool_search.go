package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode"

	"github.com/ridhomujizat/lana-harness/core"
)

var toolSearchSpec = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Keywords, or select:<name>[,<name>...] for exact tools."},"limit":{"type":"integer","minimum":1,"maximum":10,"description":"Max results, default 5."}},"required":["query"]}`)

// toolSearchDescription is a static string: it never names an agent or a
// session, so the prompt prefix cache stays valid (spec 65 prefix cache).
const toolSearchDescription = "Find a deferred MCP tool that is not in your tool list yet. Give keywords, or select:<name>[,<name>...] to load tools by name. A matched tool's description and input schema are returned, and the tool becomes callable from your next step."

// Deferred tool search limits (API-DTL-003).
const (
	toolSearchDefaultLimit = 5
	toolSearchMaxLimit     = 10
)

type toolSearchTool struct{}

// NewToolSearch creates the tool_search tool.
func NewToolSearch() core.Tool { return &toolSearchTool{} }

func (t *toolSearchTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "tool_search", Description: toolSearchDescription, InputSchema: toolSearchSpec}
}

// Risk is read: tool_search only reads the deferred specs and loads names.
func (t *toolSearchTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *toolSearchTool) Call(_ context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if len(tc.DeferredTools) == 0 {
		return errorOutput("no deferred tools")
	}
	query := strings.TrimSpace(in.Query)
	var found []core.ToolSpec
	var notFound []string
	if list, ok := selectQuery(query); ok {
		found, notFound = selectDeferred(tc.DeferredTools, list)
	} else {
		found = keywordDeferred(tc.DeferredTools, query, searchLimit(in.Limit))
	}
	if len(found) == 0 {
		return successOutput(noDeferredMatch(notFound))
	}
	names := make([]string, 0, len(found))
	for _, spec := range found {
		names = append(names, spec.Name)
	}
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: deferredToolsOutput(found, notFound), Loaded: names}, nil
}

// selectQuery reports whether query is the select: form and returns the
// comma-separated name list after the prefix.
func selectQuery(query string) (string, bool) {
	const prefix = "select:"
	if len(query) < len(prefix) || !strings.EqualFold(query[:len(prefix)], prefix) {
		return "", false
	}
	return query[len(prefix):], true
}

// selectDeferred resolves a select: list. It returns the matched tools in list
// order, deduplicated, and the names that matched no deferred tool.
func selectDeferred(tools []core.ToolSpec, list string) (found []core.ToolSpec, notFound []string) {
	seen := map[string]bool{}
	missing := map[string]bool{}
	for _, raw := range strings.Split(list, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		spec, ok := resolveDeferred(tools, name)
		if !ok {
			if !missing[name] {
				missing[name] = true
				notFound = append(notFound, name)
			}
			continue
		}
		if seen[spec.Name] {
			continue
		}
		seen[spec.Name] = true
		found = append(found, spec)
	}
	return found, notFound
}

// resolveDeferred matches one select: name. A full name matches exactly; a bare
// tool part matches when exactly one deferred tool has it.
func resolveDeferred(tools []core.ToolSpec, name string) (core.ToolSpec, bool) {
	for _, spec := range tools {
		if spec.Name == name {
			return spec, true
		}
	}
	match := -1
	for i, spec := range tools {
		if _, tool, ok := splitDeferredName(spec.Name); ok && tool == name {
			if match >= 0 {
				return core.ToolSpec{}, false
			}
			match = i
		}
	}
	if match >= 0 {
		return tools[match], true
	}
	return core.ToolSpec{}, false
}

// keywordDeferred ranks tools by keyword score and returns the best limit with
// a score above zero. Ties keep name order.
func keywordDeferred(tools []core.ToolSpec, query string, limit int) []core.ToolSpec {
	words := splitWords(query, 2)
	if len(words) == 0 {
		return nil
	}
	type scored struct {
		spec  core.ToolSpec
		score int
	}
	ranked := make([]scored, 0, len(tools))
	for _, spec := range tools {
		if score := deferredScore(spec, words); score > 0 {
			ranked = append(ranked, scored{spec: spec, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].spec.Name < ranked[j].spec.Name
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]core.ToolSpec, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.spec)
	}
	return out
}

// deferredScore sums the per-word score of one tool: 3 when a word of its tool
// part equals the query word, 2 when its server name contains it, 1 when its
// description contains it.
func deferredScore(spec core.ToolSpec, words []string) int {
	server, tool, _ := splitDeferredName(spec.Name)
	toolWords := splitWords(tool, 1)
	description := strings.ToLower(spec.Description)
	score := 0
	for _, word := range words {
		for _, candidate := range toolWords {
			if candidate == word {
				score += 3
				break
			}
		}
		if server != "" && strings.Contains(server, word) {
			score += 2
		}
		if strings.Contains(description, word) {
			score += 1
		}
	}
	return score
}

// splitDeferredName splits mcp__<server>__<tool> into its server and tool parts.
// ok is false for any other name.
func splitDeferredName(name string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(name, "mcp__")
	if !found {
		return "", "", false
	}
	i := strings.Index(rest, "__")
	if i <= 0 || i+2 >= len(rest) {
		return "", "", false
	}
	return rest[:i], rest[i+2:], true
}

// splitWords lowercases s and splits it on non-alphanumeric characters,
// dropping words shorter than min.
func splitWords(s string, min int) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) >= min {
			out = append(out, field)
		}
	}
	return out
}

// searchLimit clamps the requested limit: 0 or less is the default 5, more than
// 10 is capped at 10 (API-DTL-003).
func searchLimit(limit int) int {
	switch {
	case limit <= 0:
		return toolSearchDefaultLimit
	case limit > toolSearchMaxLimit:
		return toolSearchMaxLimit
	default:
		return limit
	}
}

// deferredToolsOutput renders one block per tool per API-DTL-003, then the
// not-found names, then the final loaded line.
func deferredToolsOutput(tools []core.ToolSpec, notFound []string) string {
	blocks := make([]string, 0, len(tools))
	names := make([]string, 0, len(tools))
	for _, spec := range tools {
		names = append(names, spec.Name)
		blocks = append(blocks, "## "+spec.Name+"\n"+spec.Description+"\nInput schema: "+compactSchema(spec.InputSchema))
	}
	body := strings.Join(blocks, "\n\n")
	if len(notFound) > 0 {
		body += "\n\nNot found: " + strings.Join(notFound, ", ")
	}
	return body + "\n\nLoaded: " + strings.Join(names, ", ") + ". Call them directly in your next step."
}

// noDeferredMatch is the success result when nothing matched. notFound lists
// the select: names that matched no deferred tool.
func noDeferredMatch(notFound []string) string {
	message := "No deferred tool matched. Try other words, or select:<name> with a full name or a unique tool name."
	if len(notFound) > 0 {
		message = "No deferred tool matched: not found: " + strings.Join(notFound, ", ") + ". Try other words, or select:<name> with a full name or a unique tool name."
	}
	return message
}

// compactSchema returns the input schema as compact JSON.
func compactSchema(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

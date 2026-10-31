package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

const memoryQueryMax = 10

var memoryReadSpec = json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","enum":["user","memory"],"description":"Which memory file to read: user for facts about the owner, memory for the agent's own notes."},"query":{"type":"string","description":"Search terms. Returns up to 10 entries at or above the relevance minimum, best first, each with its m<n> id. Use this when the prompt shows only the memory index."},"ids":{"type":"array","items":{"anyOf":[{"type":"string","pattern":"^m[0-9]+$"},{"type":"integer","minimum":1}]},"description":"Entry ids to return in full, as \"m3\" strings or integers. Takes precedence over query."}},"required":["target"],"additionalProperties":false}`)
var memoryWriteSpec = json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","enum":["user","memory"]},"action":{"type":"string","enum":["add","replace","remove"]},"content":{"type":"string"},"old_text":{"type":"string"}},"required":["target","action"],"additionalProperties":false}`)

// memoryWriteGuidance is the head-prompt note shown when memory_write is
// offered (REQ-ECX-011, REQ-ECX-026). It replaces the old # Memory capture
// section.
const memoryWriteGuidance = "When the owner's message carries a durable fact, save it with memory_write before you answer: how they want to be addressed, their name or role, language or style preferences, standing instructions, decisions, or a correction of something you got wrong. This includes facts that are only implied or said in passing, so they survive compaction and later sessions.\n" +
	"- target=user for facts about the owner, target=memory for your own notes about the environment.\n" +
	"- When a new fact contradicts an entry shown above, use action=replace with old_text from that entry. When the current turn shows an entry is wrong or outdated, including a workaround that a tool now covers, replace or remove that entry instead of adding a second one.\n" +
	"- Keep each entry one short sentence, and leave out secrets such as passwords, API keys, and tokens."

type memoryReadTool struct{}
type memoryWriteTool struct{}

// NewMemoryRead creates the memory_read tool.
func NewMemoryRead() core.Tool { return &memoryReadTool{} }

// NewMemoryWrite creates the memory_write tool.
func NewMemoryWrite() core.Tool { return &memoryWriteTool{} }

func (t *memoryReadTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "memory_read", Description: "Read your memory entries. target=user for facts about the user, target=memory for your own notes.", InputSchema: memoryReadSpec}
}
func (t *memoryReadTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

// PromptGuidance returns "" so the memory rules render once, under
// memory_write (REQ-ECX-011).
func (t *memoryReadTool) PromptGuidance() string { return "" }

func (t *memoryWriteTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "memory_write", Description: "Save durable facts. target=user for facts about the user, target=memory for your own notes. action=add appends content; replace and remove need old_text that matches exactly one entry. Keep entries short; consolidate when near the limit.", InputSchema: memoryWriteSpec}
}
func (t *memoryWriteTool) Risk(json.RawMessage) core.Risk { return core.RiskMemory }

func (t *memoryWriteTool) PromptGuidance() string { return memoryWriteGuidance }

// memoryFile returns the path and limit of target for the calling agent.
func memoryFile(tc core.ToolCtx, target string) (string, int, error) {
	if tc.Home == "" || !agenthome.ValidName(tc.AgentName) {
		return "", 0, errors.New("memory is not available")
	}
	home := agenthome.Paths(tc.Home, tc.AgentName)
	switch target {
	case "user":
		return agenthome.SharedUser(tc.Home), agenthome.UserLimit, nil
	case "memory":
		return home.Memory, agenthome.MemoryLimit, nil
	}
	return "", 0, fmt.Errorf("invalid input: target must be user or memory")
}

// FormatMemory renders all entries of target with their m<n> ids, as shown by
// memory_read.
func FormatMemory(target string, entries []string, limit int) string {
	ids := make([]int, len(entries))
	for i := range entries {
		ids[i] = i + 1
	}
	return formatMemory(target, entries, limit, ids)
}

// formatMemory renders the selected 1-based ids with the file header.
func formatMemory(target string, entries []string, limit int, ids []int) string {
	lines := []string{fmt.Sprintf("%s %d/%d characters, %d entries", target, agenthome.Size(entries), limit, len(entries))}
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf("m%d: %s", id, strings.ReplaceAll(entries[id-1], "\n", "\n   ")))
	}
	return strings.Join(lines, "\n")
}

// memoryIDs parses the ids input, which accepts "m3" strings and integers. It
// keeps request order and drops duplicates.
func memoryIDs(values []any) ([]int, error) {
	ids := make([]int, 0, len(values))
	seen := make(map[int]bool, len(values))
	for _, value := range values {
		id, err := memoryID(value)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// memoryID parses one id value.
func memoryID(value any) (int, error) {
	errInvalid := errors.New(`ids must be "m<n>" or an integer of at least 1`)
	switch v := value.(type) {
	case float64:
		id := int(v)
		if v != float64(id) || id < 1 {
			return 0, errInvalid
		}
		return id, nil
	case string:
		if !strings.HasPrefix(v, "m") || len(v) == 1 {
			return 0, errInvalid
		}
		for _, r := range v[1:] {
			if r < '0' || r > '9' {
				return 0, errInvalid
			}
		}
		id, err := strconv.Atoi(v[1:])
		if err != nil || id < 1 {
			return 0, errInvalid
		}
		return id, nil
	default:
		return 0, errInvalid
	}
}

func (t *memoryReadTool) Call(_ context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Target string `json:"target"`
		Query  string `json:"query"`
		Ids    []any  `json:"ids"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	path, limit, err := memoryFile(tc, in.Target)
	if err != nil {
		return errorOutput(err.Error())
	}
	entries, err := agenthome.ReadEntries(path)
	if err != nil {
		return errorOutput(err.Error())
	}
	switch {
	case len(in.Ids) > 0:
		ids, err := memoryIDs(in.Ids)
		if err != nil {
			return inputErrorOutput(err)
		}
		for _, id := range ids {
			if id > len(entries) {
				return errorOutput(fmt.Sprintf("no entry m%d in %s", id, in.Target))
			}
		}
		return successOutput(formatMemory(in.Target, entries, limit, ids))
	case strings.TrimSpace(in.Query) != "":
		ranked := agenthome.Rank(in.Query, entries)
		if len(ranked) > memoryQueryMax {
			ranked = ranked[:memoryQueryMax]
		}
		ids := make([]int, 0, len(ranked))
		for _, item := range ranked {
			ids = append(ids, item.ID)
		}
		return successOutput(formatMemory(in.Target, entries, limit, ids))
	}
	return successOutput(FormatMemory(in.Target, entries, limit))
}

func (t *memoryWriteTool) Call(_ context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Target  string `json:"target"`
		Action  string `json:"action"`
		Content string `json:"content"`
		OldText string `json:"old_text"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	path, limit, err := memoryFile(tc, in.Target)
	if err != nil {
		return errorOutput(err.Error())
	}
	entries, err := agenthome.Update(path, func(entries []string) ([]string, error) {
		return agenthome.Apply(entries, in.Action, in.Content, in.OldText, limit)
	})
	var applyErr *agenthome.ApplyError
	switch {
	case errors.As(err, &applyErr):
		switch applyErr.Kind {
		case agenthome.ErrOverLimit:
			return errorOutput(fmt.Sprintf("%s would be %d/%d characters; remove or merge entries first", in.Target, applyErr.Size, applyErr.Limit))
		case agenthome.ErrNoMatch:
			return errorOutput("no entry in " + in.Target + " contains old_text")
		case agenthome.ErrAmbiguous:
			return errorOutput(fmt.Sprintf("old_text matches %d entries in %s; use more specific text", applyErr.Matches, in.Target))
		}
		return errorOutput("invalid input: " + applyErr.Field)
	case err != nil:
		return errorOutput(err.Error())
	}
	size := fmt.Sprintf("(%d/%d characters)", agenthome.Size(entries), limit)
	switch in.Action {
	case "add":
		return successOutput("added to " + in.Target + " " + size)
	case "replace":
		return successOutput("replaced 1 entry in " + in.Target + " " + size)
	}
	return successOutput("removed 1 entry from " + in.Target + " " + size)
}

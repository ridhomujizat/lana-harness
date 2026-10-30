package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	maxToolNameLength = 64
	maxDescription    = 1024
	maxOutputLength   = 30000
)

// toolWrapper exposes one MCP tool through core.Tool.
type toolWrapper struct {
	manager     *Manager
	server      string
	tool        string
	spec        core.ToolSpec
	annotations *sdk.ToolAnnotations
}

func newToolWrapper(manager *Manager, server string, tool *sdk.Tool) *toolWrapper {
	schema := tool.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		raw = []byte(`{"type":"object"}`)
	}
	return &toolWrapper{
		manager:     manager,
		server:      server,
		tool:        tool.Name,
		spec:        core.ToolSpec{Name: toolName(server, tool.Name), Description: truncateRunes(tool.Description, maxDescription), InputSchema: raw},
		annotations: tool.Annotations,
	}
}

func (t *toolWrapper) Spec() core.ToolSpec { return t.spec }

func (t *toolWrapper) Risk(json.RawMessage) core.Risk {
	if t.annotations != nil && t.annotations.ReadOnlyHint {
		return core.RiskRead
	}
	return core.RiskExec
}

func (t *toolWrapper) Call(ctx context.Context, _ core.ToolCtx, input json.RawMessage) (core.ToolOutput, error) {
	s, ok := t.manager.lookupServer(t.server)
	if !ok {
		return errorOutput("mcp: server " + t.server + " is not running"), nil
	}

	args := map[string]any{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return errorOutput("invalid input: " + err.Error()), nil
		}
		if args == nil {
			args = map[string]any{}
		}
	}

	s.mu.Lock()
	if s.state != stateRunning || s.session == nil {
		s.mu.Unlock()
		return errorOutput("mcp: server " + t.server + " is not running"), nil
	}
	session := s.session
	s.inflight++
	s.lastUsed = t.manager.now()
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.inflight--
		s.lastUsed = t.manager.now()
		s.mu.Unlock()
	}()

	callCtx, cancel := context.WithTimeout(ctx, t.manager.CallTimeout)
	defer cancel()
	result, err := session.CallTool(callCtx, &sdk.CallToolParams{Name: t.tool, Arguments: args})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || callCtx.Err() == context.DeadlineExceeded {
			return errorOutput(fmt.Sprintf("timed out after %d seconds", int(t.manager.CallTimeout.Seconds()))), nil
		}
		if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
			return core.ToolOutput{Status: core.ToolStatusError, Output: "cancelled"}, nil
		}
		t.manager.markStopped(s, session)
		return errorOutput("mcp: " + err.Error()), nil
	}

	if result == nil {
		return core.ToolOutput{Status: core.ToolStatusSuccess}, nil
	}
	status := core.ToolStatusSuccess
	if result.IsError {
		status = core.ToolStatusError
	}
	return core.ToolOutput{Status: status, Output: formatResult(result)}, nil
}

// formatResult renders a tool result for the model.
func formatResult(result *sdk.CallToolResult) string {
	if result == nil {
		return ""
	}
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		switch item := content.(type) {
		case *sdk.TextContent:
			parts = append(parts, item.Text)
		case *sdk.ImageContent:
			parts = append(parts, "[image omitted]")
		case *sdk.AudioContent:
			parts = append(parts, "[audio omitted]")
		case *sdk.ResourceLink, *sdk.EmbeddedResource:
			parts = append(parts, "[resource omitted]")
		}
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		if raw, err := json.Marshal(result.StructuredContent); err == nil {
			return truncateText(string(raw), maxOutputLength)
		}
	}
	return truncateText(strings.Join(parts, "\n"), maxOutputLength)
}

// toolName returns the model-facing name of an MCP tool.
func toolName(server, tool string) string {
	name := "mcp__" + server + "__" + tool
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	runes := []rune(b.String())
	if len(runes) > maxToolNameLength {
		runes = runes[:maxToolNameLength]
	}
	return string(runes)
}

// truncateRunes cuts s to limit runes without a note.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// truncateText cuts s to limit runes and appends the truncation note.
func truncateText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + fmt.Sprintf("\n...[truncated from %d characters]", len(runes))
}

func errorOutput(msg string) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusError, Output: msg}
}

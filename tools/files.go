package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

var readFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"],"additionalProperties":false}`)
var writeFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)
var editFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string","minLength":1},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}`)

type readFileTool struct{ guard *Guard }

// NewReadFile creates the read_file tool.
func NewReadFile(guard *Guard) core.Tool { return &readFileTool{guard: guard} }

func (t *readFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "read_file", Description: "Reads the contents of a text file in the workspace.", InputSchema: readFileSpec}
}
func (t *readFileTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *readFileTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.Path == "" {
		return inputErrorOutput(fmt.Errorf("path is required"))
	}
	if in.Offset == 0 {
		in.Offset = 1
	}
	if in.Offset < 1 {
		return inputErrorOutput(fmt.Errorf("offset must be at least 1"))
	}
	if in.Limit == 0 {
		in.Limit = 2000
	}
	if in.Limit < 1 || in.Limit > 2000 {
		return inputErrorOutput(fmt.Errorf("limit must be between 1 and 2000"))
	}
	path, rejected := resolveReadToolPath(t.guard, tc, tc.Workspace, in.Path)
	if rejected != nil {
		return *rejected, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorOutput("file not found")
		}
		return errorOutput(err.Error())
	}
	if bytes.IndexByte(data[:minInt(len(data), 8000)], 0) >= 0 {
		return errorOutput("binary file")
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := in.Offset - 1
	if start >= len(lines) {
		return successOutput("")
	}
	end := start + in.Limit
	if end > len(lines) {
		end = len(lines)
	}
	return successOutput(truncateText(strings.Join(lines[start:end], "\n"), 30000))
}

type writeFileTool struct{ guard *Guard }

// NewWriteFile creates the write_file tool.
func NewWriteFile(guard *Guard) core.Tool { return &writeFileTool{guard: guard} }

func (t *writeFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "write_file", Description: "Creates or overwrites a text file in the workspace.", InputSchema: writeFileSpec}
}
func (t *writeFileTool) Risk(json.RawMessage) core.Risk { return core.RiskWrite }

func (t *writeFileTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.Path == "" {
		return inputErrorOutput(fmt.Errorf("path is required"))
	}
	path, rejected := resolveToolPath(t.guard, tc.Workspace, in.Path)
	if rejected != nil {
		return *rejected, nil
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return errorOutput("parent directory does not exist")
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(fmt.Sprintf("wrote %d bytes", len(in.Content)))
}

type editFileTool struct{ guard *Guard }

// NewEditFile creates the edit_file tool.
func NewEditFile(guard *Guard) core.Tool { return &editFileTool{guard: guard} }

func (t *editFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "edit_file", Description: "Replaces one exact occurrence of text in a file.", InputSchema: editFileSpec}
}
func (t *editFileTool) Risk(json.RawMessage) core.Risk { return core.RiskWrite }

func (t *editFileTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.Path == "" {
		return inputErrorOutput(fmt.Errorf("path is required"))
	}
	if in.OldString == "" {
		return inputErrorOutput(fmt.Errorf("old_string must not be empty"))
	}
	path, rejected := resolveToolPath(t.guard, tc.Workspace, in.Path)
	if rejected != nil {
		return *rejected, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorOutput("file not found")
		}
		return errorOutput(err.Error())
	}
	count := strings.Count(string(data), in.OldString)
	switch {
	case count == 0:
		return errorOutput("not found")
	case count > 1:
		return errorOutput(fmt.Sprintf("found %d times; add more context", count))
	}
	out := strings.Replace(string(data), in.OldString, in.NewString, 1)
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return errorOutput(err.Error())
	}
	return successOutput("replaced 1 occurrence")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

// NewReadFile membuat tool read_file.
func NewReadFile(guard *Guard) core.Tool { return &readFileTool{guard: guard} }

func (t *readFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "read_file", Description: "Membaca isi file teks di workspace.", InputSchema: readFileSpec}
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
		return inputErrorOutput(fmt.Errorf("path wajib diisi"))
	}
	if in.Offset == 0 {
		in.Offset = 1
	}
	if in.Offset < 1 {
		return inputErrorOutput(fmt.Errorf("offset minimal 1"))
	}
	if in.Limit == 0 {
		in.Limit = 2000
	}
	if in.Limit < 1 || in.Limit > 2000 {
		return inputErrorOutput(fmt.Errorf("limit harus 1 sampai 2000"))
	}
	path, rejected := resolveToolPath(t.guard, tc.Workspace, in.Path)
	if rejected != nil {
		return *rejected, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorOutput("file tidak ditemukan")
		}
		return errorOutput(err.Error())
	}
	if bytes.IndexByte(data[:minInt(len(data), 8000)], 0) >= 0 {
		return errorOutput("file biner")
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

// NewWriteFile membuat tool write_file.
func NewWriteFile(guard *Guard) core.Tool { return &writeFileTool{guard: guard} }

func (t *writeFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "write_file", Description: "Membuat atau mengganti file teks di workspace.", InputSchema: writeFileSpec}
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
		return inputErrorOutput(fmt.Errorf("path wajib diisi"))
	}
	path, rejected := resolveToolPath(t.guard, tc.Workspace, in.Path)
	if rejected != nil {
		return *rejected, nil
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return errorOutput("direktori induk tidak ada")
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(fmt.Sprintf("ditulis %d byte", len(in.Content)))
}

type editFileTool struct{ guard *Guard }

// NewEditFile membuat tool edit_file.
func NewEditFile(guard *Guard) core.Tool { return &editFileTool{guard: guard} }

func (t *editFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "edit_file", Description: "Mengganti satu kemunculan teks persis dalam file.", InputSchema: editFileSpec}
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
		return inputErrorOutput(fmt.Errorf("path wajib diisi"))
	}
	if in.OldString == "" {
		return inputErrorOutput(fmt.Errorf("old_string tidak boleh kosong"))
	}
	path, rejected := resolveToolPath(t.guard, tc.Workspace, in.Path)
	if rejected != nil {
		return *rejected, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorOutput("file tidak ditemukan")
		}
		return errorOutput(err.Error())
	}
	count := strings.Count(string(data), in.OldString)
	switch {
	case count == 0:
		return errorOutput("tidak ditemukan")
	case count > 1:
		return errorOutput(fmt.Sprintf("ditemukan %d kali; perbanyak konteks", count))
	}
	out := strings.Replace(string(data), in.OldString, in.NewString, 1)
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return errorOutput(err.Error())
	}
	return successOutput("diubah 1 kemunculan")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

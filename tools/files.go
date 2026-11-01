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
	"github.com/ridhomujizat/lana-harness/internal/attach"
)

const (
	readFileDescription = "Reads a UTF-8 text file and returns numbered lines (`<n>\\t<line>`). " +
		"Also reads PDFs as text with `--- page <k> ---` markers (a scanned PDF has no text and returns an error), " +
		"and images, which come back as an image you can see when the model supports images; " +
		"offset and limit do not apply to images. " +
		"Use this, not `cat`/`head`/`tail` in bash, to inspect files. " +
		"Default 2000 lines from `offset`; read large files in parts. " +
		"The number prefix is not part of the file: never copy it into edit_file."
	writeFileDescription = "Creates a file or replaces its whole content. Creates parent directories. " +
		"To change part of an existing file use edit_file instead; never write files with bash `echo`/`printf`/heredocs."
	editFileDescription = "Replaces exact text in an existing file. " +
		"Read the file first and copy `old_string` exactly, including indentation, without the line-number prefix. " +
		"`old_string` must occur once unless `replace_all` is true; add surrounding lines to make it unique. " +
		"Use this, not `sed` in bash."
	// editFileGuidance is the head-prompt note shown when edit_file is offered
	// (REQ-ECX-011).
	editFileGuidance = "Read the file, or the relevant range, before editing it; a matching old_string is easier to get right from the current text. " +
		"When an edit fails, read the file again instead of guessing at the difference. " +
		"Prefer one edit per logical change so a failure is easy to isolate."
)

var readFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path, relative to the workspace."},"offset":{"type":"integer","minimum":1,"description":"1-based line number to start reading at. Defaults to 1."},"limit":{"type":"integer","minimum":1,"maximum":2000,"description":"Maximum number of lines to return (1-2000). Defaults to 2000."}},"required":["path"],"additionalProperties":false}`)
var writeFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path, relative to the workspace. Missing parent directories are created."},"content":{"type":"string","description":"Full new content of the file."}},"required":["path","content"],"additionalProperties":false}`)
var editFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path, relative to the workspace."},"old_string":{"type":"string","minLength":1,"description":"Exact text to replace, without the read_file line-number prefix. Must occur once unless replace_all is true."},"new_string":{"type":"string","description":"Replacement text."},"replace_all":{"type":"boolean","default":false,"description":"Replace every occurrence of old_string instead of requiring exactly one. Defaults to false."}},"required":["path","old_string","new_string"],"additionalProperties":false}`)

type readFileTool struct{ guard *Guard }

// NewReadFile creates the read_file tool.
func NewReadFile(guard *Guard) core.Tool { return &readFileTool{guard: guard} }

func (t *readFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "read_file", Description: readFileDescription, InputSchema: readFileSpec}
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
	head := data[:minInt(len(data), 512)]
	rel := filepath.ToSlash(filepath.Clean(in.Path))
	switch attach.Kind(head, rel) {
	case "image":
		block, w, h, err := attach.Image(path, rel)
		if err != nil {
			return errorOutput(fmt.Sprintf("could not read %s: %v", rel, err))
		}
		out, _ := successOutput(fmt.Sprintf("Image %s (%dx%d).", rel, w, h))
		out.Images = []core.ContentBlock{block}
		return out, nil
	case "pdf":
		pages, err := attach.PDFText(path)
		if err != nil {
			return errorOutput(fmt.Sprintf("could not read %s: %v", rel, err))
		}
		if !pagesHaveText(pages) {
			return errorOutput(fmt.Sprintf("Attachment %s (PDF, %d pages) has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead.", rel, len(pages)))
		}
		var b strings.Builder
		for i, page := range pages {
			if i > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "--- page %d ---", i+1)
			b.WriteByte('\n')
			b.WriteString(page)
		}
		return readTextRange(b.String(), in.Offset, in.Limit)
	}
	if bytes.IndexByte(data[:minInt(len(data), 8000)], 0) >= 0 {
		return errorOutput("binary file")
	}
	return readTextRange(string(data), in.Offset, in.Limit)
}

// readTextRange numbers the lines of content and returns the window selected by
// offset (1-based) and limit, followed by a notice when later lines remain. The
// text-file and PDF paths of read_file share it.
func readTextRange(content string, offset, limit int) (core.ToolOutput, error) {
	lines := fileLines(content)
	start := offset - 1
	if start >= len(lines) {
		return successOutput("")
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	text := truncateText(formatNumberedLines(lines, start, end), 30000)
	if end < len(lines) {
		text += fmt.Sprintf("\n(Showing lines %d–%d of %d. Use offset=%d to read more.)", start+1, end, len(lines), end+1)
	}
	return successOutput(text)
}

// pagesHaveText reports whether any page of a PDF carries text. A PDF with no
// pages has none, so it reads as a scan.
func pagesHaveText(pages []string) bool {
	for _, p := range pages {
		if p != "" {
			return true
		}
	}
	return false
}

type writeFileTool struct{ guard *Guard }

// NewWriteFile creates the write_file tool.
func NewWriteFile(guard *Guard) core.Tool { return &writeFileTool{guard: guard} }

func (t *writeFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "write_file", Description: writeFileDescription, InputSchema: writeFileSpec}
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
	mode := os.FileMode(0o644)
	existed := false
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return errorOutput("path is a directory")
		}
		mode = info.Mode().Perm()
		existed = true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errorOutput(err.Error())
	}
	if err := os.WriteFile(path, []byte(in.Content), mode); err != nil {
		return errorOutput(err.Error())
	}
	verb := "Created"
	if existed {
		verb = "Overwrote"
	}
	return successOutput(fmt.Sprintf("%s %s (%d bytes).", verb, in.Path, len(in.Content)))
}

type editFileTool struct{ guard *Guard }

// NewEditFile creates the edit_file tool.
func NewEditFile(guard *Guard) core.Tool { return &editFileTool{guard: guard} }

func (t *editFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "edit_file", Description: editFileDescription, InputSchema: editFileSpec}
}
func (t *editFileTool) Risk(json.RawMessage) core.Risk { return core.RiskWrite }

func (t *editFileTool) PromptGuidance() string { return editFileGuidance }

func (t *editFileTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
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
	if in.OldString == in.NewString {
		return inputErrorOutput(fmt.Errorf("old_string and new_string must differ"))
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
	content := string(data)
	count := strings.Count(content, in.OldString)
	switch {
	case count == 0:
		return errorOutput(notFoundHint(in.Path, content, in.OldString))
	case count > 1 && !in.ReplaceAll:
		return errorOutput(multipleMatchesError(in.Path, content, in.OldString, count))
	}
	replaced := 1
	n := 1
	if in.ReplaceAll {
		replaced = count
		n = -1
	}
	firstLine := strings.Count(content[:strings.Index(content, in.OldString)], "\n") + 1
	out := strings.Replace(content, in.OldString, in.NewString, n)
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(out), mode); err != nil {
		return errorOutput(err.Error())
	}
	text := fmt.Sprintf("Edited %s: replaced %d occurrence(s).", in.Path, replaced)
	if snippet := editSnippet(out, firstLine, in.NewString); snippet != "" {
		text += "\n" + snippet
	}
	return successOutput(text)
}

// fileLines splits content the way read_file numbers it: a single trailing
// newline does not add an empty final line.
func fileLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// formatNumberedLines renders lines[from:to] (0-based, half-open) with the
// read_file prefix.
func formatNumberedLines(lines []string, from, to int) string {
	var b strings.Builder
	for i := from; i < to && i < len(lines); i++ {
		if i > from {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%6d\t%s", i+1, lines[i])
	}
	return b.String()
}

// editSnippet returns the numbered changed region of the new content with two
// lines of context on each side, capped at editSnippetMaxLines.
func editSnippet(content string, firstLine int, newString string) string {
	lines := fileLines(content)
	if len(lines) == 0 {
		return ""
	}
	lastLine := firstLine + strings.Count(newString, "\n")
	start := firstLine - 2
	if start < 1 {
		start = 1
	}
	end := lastLine + 2
	if end > len(lines) {
		end = len(lines)
	}
	if end-start+1 > editSnippetMaxLines {
		end = start + editSnippetMaxLines - 1
		if end > len(lines) {
			end = len(lines)
		}
	}
	return formatNumberedLines(lines, start-1, end)
}

func multipleMatchesError(path, content, old string, count int) string {
	lines := matchLineNumbers(content, old, multipleMatchLineLimit)
	return fmt.Sprintf("old_string matches %d times in %s. Add more surrounding context to make it unique, or set replace_all to true. Matches at lines %s.",
		count, path, joinInts(lines))
}

// matchLineNumbers returns the 1-based line of each non-overlapping match,
// capped at limit.
func matchLineNumbers(content, old string, limit int) []int {
	var lines []int
	pos := 0
	for len(lines) < limit {
		idx := strings.Index(content[pos:], old)
		if idx < 0 {
			break
		}
		abs := pos + idx
		lines = append(lines, strings.Count(content[:abs], "\n")+1)
		pos = abs + len(old)
	}
	return lines
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%d", value)
	}
	return strings.Join(parts, ", ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

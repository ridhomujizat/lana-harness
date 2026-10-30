package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/ridhomujizat/lana-harness/core"
	ignore "github.com/sabhiram/go-gitignore"
)

var globSpec = json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","minLength":1},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`)
var grepSpec = json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","minLength":1},"path":{"type":"string"},"glob":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`)

type globTool struct{ guard *Guard }

// NewGlob creates the glob tool.
func NewGlob(guard *Guard) core.Tool { return &globTool{guard: guard} }

func (t *globTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "glob", Description: "Finds paths matching a glob pattern in the workspace.", InputSchema: globSpec}
}
func (t *globTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *globTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.Pattern == "" {
		return inputErrorOutput(fmt.Errorf("pattern is required"))
	}
	ws := effectiveWorkspace(t.guard, tc.Workspace)
	if err := checkWorkspace(ws); err != nil {
		return errorOutput(err.Error())
	}
	base := ws
	if in.Path != "" {
		resolved, rejected := resolveToolPath(t.guard, ws, in.Path)
		if rejected != nil {
			return *rejected, nil
		}
		base = resolved
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return errorOutput("path is not a directory")
	}
	ig := compileRootIgnore(ws)
	matches, err := doublestar.Glob(os.DirFS(base), in.Pattern)
	if err != nil {
		return errorOutput("invalid pattern: " + err.Error())
	}
	var paths []string
	for _, match := range matches {
		if err := ctx.Err(); err != nil {
			return errorOutput(err.Error())
		}
		abs := filepath.Join(base, filepath.FromSlash(match))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		rel, err := filepath.Rel(ws, abs)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if ignoredPath(ig, rel) {
			continue
		}
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	total := len(paths)
	if total == 0 {
		return successOutput("no results")
	}
	if total > 1000 {
		paths = paths[:1000]
	}
	out := strings.Join(paths, "\n")
	if total > 1000 {
		out += fmt.Sprintf("\n(of %d results)", total)
	}
	return successOutput(truncateText(out, 30000))
}

type grepTool struct{ guard *Guard }

// NewGrep creates the grep tool.
func NewGrep(guard *Guard) core.Tool { return &grepTool{guard: guard} }

func (t *grepTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "grep", Description: "Searches text with an RE2 regex in the workspace.", InputSchema: grepSpec}
}
func (t *grepTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *grepTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.Pattern == "" {
		return inputErrorOutput(fmt.Errorf("pattern is required"))
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return errorOutput("invalid pattern: " + err.Error())
	}
	ws := effectiveWorkspace(t.guard, tc.Workspace)
	if err := checkWorkspace(ws); err != nil {
		return errorOutput(err.Error())
	}
	base := ws
	if in.Path != "" {
		resolved, rejected := resolveToolPath(t.guard, ws, in.Path)
		if rejected != nil {
			return *rejected, nil
		}
		base = resolved
	}
	ig := compileRootIgnore(ws)
	var results []string
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if ignoredPath(ig, rel) {
			return nil
		}
		if in.Glob != "" {
			ok, err := doublestar.Match(in.Glob, rel)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > 1<<20 {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil
		}
		if !within(ws, resolved) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		head, _ := io.ReadAll(io.LimitReader(f, 8000))
		if bytes.IndexByte(head, 0) >= 0 {
			_ = f.Close()
			return nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			_ = f.Close()
			return nil
		}
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if !re.MatchString(line) {
				continue
			}
			if len([]rune(line)) > 500 {
				line = string([]rune(line)[:500]) + "..."
			}
			results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNo, line))
			if len(results) >= 200 {
				_ = f.Close()
				return io.EOF
			}
		}
		_ = f.Close()
		return nil
	})
	if err != nil && err != io.EOF {
		return errorOutput(err.Error())
	}
	if len(results) == 0 {
		return successOutput("no matches")
	}
	return successOutput(truncateText(strings.Join(results, "\n"), 30000))
}

func compileRootIgnore(ws string) *ignore.GitIgnore {
	ig, err := ignore.CompileIgnoreFile(filepath.Join(ws, ".gitignore"))
	if err != nil {
		return nil
	}
	return ig
}

func ignoredPath(ig *ignore.GitIgnore, rel string) bool {
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return true
	}
	if ig == nil {
		return false
	}
	return ig.MatchesPath(rel)
}

func within(ws, path string) bool {
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	rel, err := filepath.Rel(ws, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

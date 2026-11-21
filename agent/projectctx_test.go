package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// projectHome points HOME at a fresh temp dir and returns its canonical path,
// so the project context chain (which stops before the owner's home,
// REQ-PCX-002) is bounded by a test directory.
func projectHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

// projectSession gives h a fresh session whose workspace is <home>/workspace,
// so the prompt-time chain is exactly the workspace directory.
func projectSession(t *testing.T, h *harness, home string) {
	t.Helper()
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	h.info = info
}

// writeRepoFiles writes a map of slash-separated relative paths under dir.
func writeRepoFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// readCall builds a read_file tool call response for an absolute path.
func readCall(id, path string) mock.Response {
	return toolResponse(id, "read_file", fmt.Sprintf(`{"path":%q}`, path))
}

// toolResults returns the session's tool results keyed by call id.
func toolResults(t *testing.T, h *harness) map[string]core.ToolResultPayload {
	t.Helper()
	out := map[string]core.ToolResultPayload{}
	for _, event := range h.events(t) {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out[payload.CallID] = payload
	}
	return out
}

// AC-PCX-006, AC-PCX-007: the first read_file into a repository appends that
// repository's CLAUDE.md as a <project-context> block; a later read in the
// same repository, in the same turn, adds nothing.
func TestProjectNoteOnToolResult(t *testing.T) {
	home := projectHome(t)
	repo := filepath.Join(home, "repo")
	writeRepoFiles(t, repo, map[string]string{
		"CLAUDE.md": "repo instructions",
		"sub/x.go":  "package sub",
		"sub/y.go":  "package sub",
	})
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		readCall("c1", filepath.Join(repo, "sub", "x.go")),
		readCall("c2", filepath.Join(repo, "sub", "y.go")),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	projectSession(t, h, home)

	if result := h.run(t, "read them"); result.StopReason != "end_turn" {
		t.Fatalf("result = %+v", result)
	}
	block := `<project-context path="` + filepath.Join(repo, "CLAUDE.md") + `">`
	results := toolResults(t, h)
	if out := results["c1"].Output; !strings.Contains(out, block) || !strings.Contains(out, "repo instructions") {
		t.Fatalf("first read lacks the project block:\n%s", out)
	}
	if out := results["c2"].Output; strings.Contains(out, "<project-context") {
		t.Fatalf("second read must not repeat the block:\n%s", out)
	}
}

// AC-PCX-010: after a compaction, the next touch of the repository shows its
// CLAUDE.md again.
func TestProjectNoteReturnsAfterCompaction(t *testing.T) {
	home := projectHome(t)
	repo := filepath.Join(home, "repo")
	writeRepoFiles(t, repo, map[string]string{
		"CLAUDE.md": "repo instructions",
		"sub/x.go":  "package sub",
	})
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		readCall("c1", filepath.Join(repo, "sub", "x.go")),
		textResponse("one"),
		readCall("c2", filepath.Join(repo, "sub", "x.go")),
		textResponse("two"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	projectSession(t, h, home)

	h.run(t, "first")
	if out := toolResults(t, h)["c1"].Output; !strings.Contains(out, "<project-context") {
		t.Fatalf("first read lacks the project block:\n%s", out)
	}

	events := &Events{Store: h.store, SessionID: h.info.ID}
	if _, err := events.Append(context.Background(), core.EventDraft{
		Kind:    core.KindCompactionSummary,
		Payload: core.CompactionSummary{V: core.PayloadVersion, Summary: "older context", UptoSeq: 1},
	}); err != nil {
		t.Fatal(err)
	}

	h.run(t, "second")
	if out := toolResults(t, h)["c2"].Output; !strings.Contains(out, "<project-context") {
		t.Fatalf("read after a compaction lacks the project block:\n%s", out)
	}
}

// AC-PCX-002: context_files = [] shows no project section and appends no block,
// even when a context file sits next to the workspace and in the touched repo.
func TestProjectContextDisabledWithEmptyList(t *testing.T) {
	home := projectHome(t)
	writeRepoFiles(t, home, map[string]string{"CLAUDE.md": "home-level"})
	lanaDir := filepath.Join(home, "lana")
	writeRepoFiles(t, lanaDir, map[string]string{"AGENTS.md": "lana-level"})
	repo := filepath.Join(home, "repo")
	writeRepoFiles(t, repo, map[string]string{"CLAUDE.md": "repo instructions", "sub/x.go": "package sub"})
	empty := []string{}
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		readCall("c1", filepath.Join(repo, "sub", "x.go")),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, ContextFiles: &empty}})
	info, err := h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: filepath.Join(lanaDir, "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	h.info = info

	h.run(t, "hi")
	if prompt := h.systemPrompts(t)[0]; strings.Contains(prompt, "# Project instructions") {
		t.Fatalf("prompt has a project section with context_files = []:\n%s", prompt)
	}
	if out := toolResults(t, h)["c1"].Output; strings.Contains(out, "<project-context") {
		t.Fatalf("tool result has a project block with context_files = []:\n%s", out)
	}
}

// AC-PCX-004: the project section holds the context file of the workspace's
// parent, and no file from the home directory itself.
func TestProjectSectionInPromptCoversPcx004(t *testing.T) {
	home := projectHome(t)
	lanaDir := filepath.Join(home, "lana")
	writeRepoFiles(t, lanaDir, map[string]string{"AGENTS.md": "project instructions"})
	writeRepoFiles(t, home, map[string]string{"CLAUDE.md": "home-level"})
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}})
	info, err := h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: filepath.Join(lanaDir, "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	h.info = info

	h.run(t, "hi")
	prompt := h.systemPrompts(t)[0]
	if !strings.Contains(prompt, "# Project instructions") {
		t.Fatalf("prompt lacks the project section:\n%s", prompt)
	}
	block := `<project-context path="` + filepath.Join(lanaDir, "AGENTS.md") + `">`
	if !strings.Contains(prompt, block) || !strings.Contains(prompt, "project instructions") {
		t.Fatalf("prompt lacks the context file block:\n%s", prompt)
	}
	if strings.Contains(prompt, `<project-context path="`+filepath.Join(home, "CLAUDE.md")+`">`) {
		t.Fatalf("prompt must not include a file from the home directory:\n%s", prompt)
	}
}

// REQ-PCX-003: a failed call adds no project block.
func TestProjectNoteNotOnFailedCall(t *testing.T) {
	home := projectHome(t)
	repo := filepath.Join(home, "repo")
	writeRepoFiles(t, repo, map[string]string{"CLAUDE.md": "repo instructions"})
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		readCall("c1", filepath.Join(repo, "sub", "missing.go")),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	projectSession(t, h, home)

	h.run(t, "hi")
	out := toolResults(t, h)["c1"]
	if out.Status != core.ToolStatusError {
		t.Fatalf("read of a missing file status = %q", out.Status)
	}
	if strings.Contains(out.Output, "<project-context") {
		t.Fatalf("a failed call must not get a project block:\n%s", out.Output)
	}
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func callTool(t *testing.T, tool core.Tool, workspace, input string) core.ToolOutput {
	t.Helper()
	return callToolCtx(t, tool, core.ToolCtx{Workspace: workspace}, input)
}

func callToolCtx(t *testing.T, tool core.Tool, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadFileLines(t *testing.T) {
	ws := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 2100; i++ {
		fmt.Fprintf(&b, "l%d\n", i)
	}
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile(&Guard{Workspace: ws})
	out := callTool(t, tool, ws, `{"path":"a.txt"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	lines := strings.Split(out.Output, "\n")
	if len(lines) != 2001 {
		t.Fatalf("lines=%d", len(lines))
	}
	if lines[0] != fmt.Sprintf("%6d\tl1", 1) || lines[1999] != fmt.Sprintf("%6d\tl2000", 2000) {
		t.Fatalf("first=%q last=%q", lines[0], lines[1999])
	}
	wantNotice := "(Showing lines 1–2000 of 2100. Use offset=2001 to read more.)"
	if lines[2000] != wantNotice {
		t.Fatalf("notice=%q want %q", lines[2000], wantNotice)
	}
	out = callTool(t, tool, ws, `{"path":"a.txt","offset":10,"limit":5}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	want := fmt.Sprintf("%6d\tl10\n%6d\tl11\n%6d\tl12\n%6d\tl13\n%6d\tl14\n(Showing lines 10–14 of 2100. Use offset=15 to read more.)",
		10, 11, 12, 13, 14)
	if out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
}

func TestReadFileNumbered(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("alpha\nbeta\ngamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt"}`)
	want := fmt.Sprintf("%6d\talpha\n%6d\tbeta\n%6d\tgamma", 1, 2, 3)
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
}

func TestReadFileBinary(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "bin")
	if err := os.WriteFile(path, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"bin"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "binary file") {
		t.Fatalf("out=%+v", out)
	}
}

func TestWriteFileCreate(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","content":"hello"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "Created a.txt (5 bytes)." {
		t.Fatalf("out=%+v", out)
	}
	data, err := os.ReadFile(filepath.Join(ws, "a.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","content":"new"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "Overwrote a.txt (3 bytes)." {
		t.Fatalf("out=%+v", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("data=%q", data)
	}
}

func TestWriteFileCreatesParentDirs(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"a/b/c.txt","content":"hi"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "Created a/b/c.txt (2 bytes)." {
		t.Fatalf("out=%+v", out)
	}
	data, err := os.ReadFile(filepath.Join(ws, "a", "b", "c.txt"))
	if err != nil || string(data) != "hi" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestWriteFileKeepsMode(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","content":"new"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode=%o want 640", got)
	}
}

func TestEditFileOnce(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("one two three"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"two","new_string":"D"}`)
	want := fmt.Sprintf("Edited a.txt: replaced 1 occurrence(s).\n%6d\tone D three", 1)
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one D three" {
		t.Fatalf("data=%q", data)
	}
}

func TestEditFileReplaceAll(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("foo foo foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"foo","new_string":"bar","replace_all":true}`)
	want := fmt.Sprintf("Edited a.txt: replaced 3 occurrence(s).\n%6d\tbar bar bar", 1)
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "bar bar bar\n" {
		t.Fatalf("data=%q", data)
	}
}

func TestEditFileSameString(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"same","new_string":"same"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "must differ") {
		t.Fatalf("out=%+v", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "same" {
		t.Fatalf("file changed: %q", data)
	}
}

func TestEditFileCount(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("x\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFile(&Guard{Workspace: ws})
	out := callTool(t, tool, ws, `{"path":"a.txt","old_string":"x","new_string":"y"}`)
	for _, want := range []string{"matches 2 times", "lines 1, 2", "replace_all"} {
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, want) {
			t.Fatalf("out=%+v lacks %q", out, want)
		}
	}
	out = callTool(t, tool, ws, `{"path":"a.txt","old_string":"z","new_string":"y"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "not found") {
		t.Fatalf("out=%+v", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "x\nx" {
		t.Fatalf("file changed: %q", data)
	}
}

func TestEditFileNotFoundWhitespaceHint(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("func main() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws,
		`{"path":"a.txt","old_string":"  func main() {\n\treturn  ","new_string":"x"}`)
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v", out)
	}
	for _, want := range []string{"not found", "whitespace", "lines 1-2", "return"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output=%q lacks %q", out.Output, want)
		}
	}
}

func TestEditFileNotFoundFirstLineHint(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("func main() {\n\tprintln(1)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws,
		`{"path":"a.txt","old_string":"func main() {\n\tprintln(2)\n}","new_string":"x"}`)
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v", out)
	}
	for _, want := range []string{"The first line of old_string appears at line 1", "func main() {", "println(1)"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output=%q lacks %q", out.Output, want)
		}
	}
}

func TestEditFileNotFoundReadHint(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("one two three"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"zzz","new_string":"x"}`)
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Output, "Read the file with read_file and copy old_string exactly.") {
		t.Fatalf("output=%q", out.Output)
	}
}

func TestEditFileHintCap(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	long := strings.Repeat("x", 300)
	if err := os.WriteFile(path, []byte(strings.Repeat(long+"\n", 30)), 0o644); err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("  "+long+"  \n", 30)
	input, err := json.Marshal(map[string]string{"path": "a.txt", "old_string": old, "new_string": "x"})
	if err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, string(input))
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v", out)
	}
	if got := len([]rune(out.Output)); got > editHintMaxChars {
		t.Fatalf("hint length=%d want <= %d", got, editHintMaxChars)
	}
}

func TestEditFileSnippetContext(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line-%d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"line-50","new_string":"LINE-50"}`)
	want := "Edited a.txt: replaced 1 occurrence(s).\n" +
		fmt.Sprintf("%6d\tline-48\n%6d\tline-49\n%6d\tLINE-50\n%6d\tline-51\n%6d\tline-52", 48, 49, 50, 51, 52)
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
}

func TestEditFileKeepsMode(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("one two"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"two","new_string":"2"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode=%o want 640", got)
	}
}

func TestEditFileEmpty(t *testing.T) {
	ws := t.TempDir()
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt","old_string":"","new_string":"x"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "invalid input") {
		t.Fatalf("out=%+v", out)
	}
}

func TestGuardOutside(t *testing.T) {
	ws := t.TempDir()
	if _, err := ResolveInWorkspace(ws, "/etc/passwd"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("err=%v", err)
	}
	out := callToolCtx(t, NewReadFile(&Guard{Workspace: ws}), core.ToolCtx{Workspace: ws, WorkspaceOnly: true}, `{"path":"/etc/passwd"}`)
	if out.Status != core.ToolStatusRejected || out.Output != "outside workspace" {
		t.Fatalf("out=%+v", out)
	}
}

func TestGuardSymlinkOut(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	out := callToolCtx(t, NewReadFile(&Guard{Workspace: ws}), core.ToolCtx{Workspace: ws, WorkspaceOnly: true}, `{"path":"link"}`)
	if out.Status != core.ToolStatusRejected {
		t.Fatalf("out=%+v", out)
	}
}

func TestBuiltinToolDescriptions(t *testing.T) {
	builtins := []core.Tool{
		NewBash(&Guard{}),
		NewReadFile(&Guard{}),
		NewWriteFile(&Guard{}),
		NewEditFile(&Guard{}),
		NewGlob(&Guard{}),
		NewGrep(&Guard{}),
		NewWebFetch(&Guard{}),
		NewJobOutput(),
		NewJobList(),
		NewJobKill(),
	}
	for _, tool := range builtins {
		spec := tool.Spec()
		if strings.TrimSpace(spec.Description) == "" {
			t.Errorf("%s has no description", spec.Name)
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Errorf("%s schema: %v", spec.Name, err)
			continue
		}
		for name, prop := range schema.Properties {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s property %s has no description", spec.Name, name)
			}
		}
	}
}

func TestReadFileTooLarge(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "big.txt")
	writeSparseFile(t, path, maxReadFileBytes+1)
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"big.txt"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "file is too large to read") || !strings.Contains(out.Output, "sed -n") {
		t.Fatalf("out=%+v", out)
	}
}

func TestReadFileNotRegular(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"dir"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "not a regular file") {
		t.Fatalf("out=%+v", out)
	}
}

func TestEditFileTooLarge(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "big.txt")
	writeSparseFile(t, path, maxReadFileBytes+1)
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"big.txt","old_string":"a","new_string":"b"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "file is too large to read") {
		t.Fatalf("out=%+v", out)
	}
}

// writeSparseFile creates a file of size bytes without writing the data.
func writeSparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestWriteFileThroughSymlinkKeepsLink checks that the atomic write replaces the
// symlink target and leaves the symlink a symlink.
func TestWriteFileThroughSymlinkKeepsLink(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "real.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "link.txt")
	if err := os.Symlink("real.txt", link); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewWriteFile(&Guard{Workspace: ws}), ws, `{"path":"link.txt","content":"new"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link is no longer a symlink: %v", info.Mode())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("target data=%q err=%v", data, err)
	}
}

func TestEditFileThroughSymlinkKeepsLink(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "real.txt")
	if err := os.WriteFile(target, []byte("one two"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "link.txt")
	if err := os.Symlink("real.txt", link); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewEditFile(&Guard{Workspace: ws}), ws, `{"path":"link.txt","old_string":"two","new_string":"2"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link is no longer a symlink: %v", info.Mode())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "one 2" {
		t.Fatalf("target data=%q err=%v", data, err)
	}
}

func TestToolDescriptionKeyPhrases(t *testing.T) {
	cases := map[string][]string{
		"bash": {
			"run_in_background",
			"never use &, nohup, setsid, or disown",
			"Do not use it to read, search, create, or edit files",
			"read_file", "grep", "glob", "write_file", "edit_file",
			"cat", "sed", "find", "ls -R",
		},
		"read_file":  {"numbered lines", "not `cat`/`head`/`tail`", "Default 2000 lines", "never copy it into edit_file", "images", "PDF"},
		"write_file": {"Creates parent directories", "edit_file", "echo"},
		"edit_file":  {"replace_all", "line-number prefix", "sed"},
		"glob":       {"find", "ls -R", "1000"},
		"grep":       {"grep", "rg", "200"},
	}
	tools := map[string]core.Tool{
		"bash":       NewBash(&Guard{}),
		"read_file":  NewReadFile(&Guard{}),
		"write_file": NewWriteFile(&Guard{}),
		"edit_file":  NewEditFile(&Guard{}),
		"glob":       NewGlob(&Guard{}),
		"grep":       NewGrep(&Guard{}),
	}
	for name, phrases := range cases {
		description := tools[name].Spec().Description
		for _, phrase := range phrases {
			if !strings.Contains(description, phrase) {
				t.Errorf("%s description lacks %q: %s", name, phrase, description)
			}
		}
	}
}

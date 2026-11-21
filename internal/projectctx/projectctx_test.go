package projectctx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// writeFile writes text to path, failing the test on error.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkdir makes path and its parents, failing the test on error.
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestChainBounds(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "coding", "repo")
	sub := filepath.Join(repo, "sub")
	mkdir(t, sub)

	got := Chain(sub, home)
	want := []string{filepath.Join(home, "coding"), repo, sub}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chain under home = %q want %q", got, want)
	}

	if got := Chain(home, home); len(got) != 0 {
		t.Fatalf("Chain(home, home) = %q want empty", got)
	}

	outside := filepath.Join(string(filepath.Separator), "a", "b", "c")
	wantOutside := []string{
		filepath.Join(string(filepath.Separator), "a"),
		filepath.Join(string(filepath.Separator), "a", "b"),
		outside,
	}
	if got := Chain(outside, home); !reflect.DeepEqual(got, wantOutside) {
		t.Fatalf("Chain outside home = %q want %q", got, wantOutside)
	}
	if got := Chain(outside, ""); !reflect.DeepEqual(got, wantOutside) {
		t.Fatalf("Chain empty home = %q want %q", got, wantOutside)
	}

	// Comparison is case-insensitive: an upper-cased home prefix still stops
	// the walk at home.
	upper := strings.ToUpper(home)
	caseStart := filepath.Join(upper, "coding", "repo")
	wantCase := []string{filepath.Join(upper, "coding"), caseStart}
	if got := Chain(caseStart, home); !reflect.DeepEqual(got, wantCase) {
		t.Fatalf("Chain case-insensitive = %q want %q", got, wantCase)
	}

	// The root itself is excluded.
	if got := Chain(string(filepath.Separator)+"a", ""); !reflect.DeepEqual(got, []string{string(filepath.Separator) + "a"}) {
		t.Fatalf("Chain at root = %q want [%q]", got, string(filepath.Separator)+"a")
	}
}

func TestDiscoverFirstWins(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkdir(t, repo)
	writeFile(t, filepath.Join(repo, "AGENTS.md"), "agents")
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "claude")

	files := Discover(repo, home, []string{"AGENTS.md", "CLAUDE.md"})
	if len(files) != 1 {
		t.Fatalf("files = %d want 1", len(files))
	}
	if filepath.Base(files[0].Path) != "AGENTS.md" || files[0].Text != "agents" {
		t.Fatalf("file = %q %q want AGENTS.md agents", files[0].Path, files[0].Text)
	}
}

func TestDiscoverOrderTopDown(t *testing.T) {
	home := t.TempDir()
	coding := filepath.Join(home, "coding")
	repo := filepath.Join(coding, "repo")
	mkdir(t, repo)
	writeFile(t, filepath.Join(coding, "AGENTS.md"), "coding")
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "repo")

	files := Discover(repo, home, []string{"AGENTS.md", "CLAUDE.md"})
	want := []string{filepath.Join(coding, "AGENTS.md"), filepath.Join(repo, "CLAUDE.md")}
	if len(files) != len(want) {
		t.Fatalf("files = %d want %d", len(files), len(want))
	}
	for i := range want {
		if files[i].Path != want[i] {
			t.Fatalf("files[%d].Path = %q want %q", i, files[i].Path, want[i])
		}
	}
}

func TestDiscoverSkipsDirectoryNamedFile(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkdir(t, filepath.Join(repo, "AGENTS.md"))
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "claude")

	files := Discover(repo, home, []string{"AGENTS.md", "CLAUDE.md"})
	if len(files) != 1 || filepath.Base(files[0].Path) != "CLAUDE.md" {
		t.Fatalf("files = %+v want one CLAUDE.md", files)
	}
}

func TestDiscoverEmptyNames(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkdir(t, repo)
	writeFile(t, filepath.Join(repo, "AGENTS.md"), "agents")

	if files := Discover(repo, home, nil); files != nil {
		t.Fatalf("files = %+v want nil", files)
	}
}

// TestDiscoverTruncation pins the 16 KiB cut and the truncation note
// (AC-PCX-011).
func TestDiscoverTruncation(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkdir(t, repo)
	path := filepath.Join(repo, "AGENTS.md")
	writeFile(t, path, strings.Repeat("a", 40*1024))

	files := Discover(repo, home, []string{"AGENTS.md"})
	if len(files) != 1 {
		t.Fatalf("files = %d want 1", len(files))
	}
	f := files[0]
	if !f.Truncated || f.Size != 40*1024 || len(f.Text) != fileCap {
		t.Fatalf("file = truncated:%v size:%d len:%d", f.Truncated, f.Size, len(f.Text))
	}

	want := "<project-context path=\"" + path + "\">\n" +
		strings.Repeat("a", fileCap) +
		"\n[truncated: 16384 of 40960 bytes; read the file for the rest]\n</project-context>"
	if got := Render(files); got != want {
		t.Fatalf("Render =\n%q\nwant\n%q", got, want)
	}
}

// TestRenderInjectionCap pins the 32 KiB injection cap: the third 15 KiB file is
// listed by path only.
func TestRenderInjectionCap(t *testing.T) {
	home := t.TempDir()
	d1 := filepath.Join(home, "d1")
	d2 := filepath.Join(d1, "d2")
	d3 := filepath.Join(d2, "d3")
	mkdir(t, d3)
	chunk := strings.Repeat("x", 15*1024)
	for _, dir := range []string{d1, d2, d3} {
		writeFile(t, filepath.Join(dir, "AGENTS.md"), chunk)
	}

	files := Discover(d3, home, []string{"AGENTS.md"})
	if len(files) != 3 {
		t.Fatalf("files = %d want 3", len(files))
	}
	got := Render(files)

	third := filepath.Join(d3, "AGENTS.md")
	wantThird := "<project-context path=\"" + third + "\">\n" +
		"[truncated: 0 of 15360 bytes; read the file for the rest]\n</project-context>"
	if !strings.Contains(got, wantThird) {
		t.Fatalf("third block not path-only:\n%s", got)
	}
	if strings.Contains(got, "<project-context path=\""+third+"\">\n"+chunk) {
		t.Fatalf("third block should not carry its text")
	}
}

// TestDiscoverSkipsBadFiles covers the failure silence of REQ-PCX-006
// (AC-PCX-012).
func TestDiscoverSkipsBadFiles(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	mkdir(t, repo)
	path := filepath.Join(repo, "AGENTS.md")

	writeFile(t, path, "")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	if files := Discover(repo, home, []string{"AGENTS.md"}); len(files) != 0 {
		t.Fatalf("invalid UTF-8: files = %+v want none", files)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are ignored")
	}
	writeFile(t, path, "ok")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if files := Discover(repo, home, []string{"AGENTS.md"}); len(files) != 0 {
		t.Fatalf("unreadable: files = %+v want none", files)
	}
}

// TestSectionFormat pins the section text of the contract.
func TestSectionFormat(t *testing.T) {
	if Section(nil) != "" {
		t.Fatalf("Section(nil) = %q want empty", Section(nil))
	}

	home := t.TempDir()
	repo := filepath.Join(home, "lana")
	mkdir(t, repo)
	path := filepath.Join(repo, "AGENTS.md")
	writeFile(t, path, "…file text…")

	files := Discover(repo, home, []string{"AGENTS.md"})
	want := "# Project instructions\n" +
		"These are project instructions written for coding agents. Follow the ones that apply to your tools.\n\n" +
		"<project-context path=\"" + path + "\">\n…file text…\n</project-context>"
	if got := Section(files); got != want {
		t.Fatalf("Section =\n%q\nwant\n%q", got, want)
	}
}

// trackerFixture builds a home with a repository holding a context file and a
// separate workspace.
func trackerFixture(t *testing.T) (home, repo, workspace string) {
	t.Helper()
	home = t.TempDir()
	repo = filepath.Join(home, "coding", "repo")
	mkdir(t, repo)
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "repo rules")
	workspace = filepath.Join(home, "workspace")
	mkdir(t, workspace)
	return home, repo, workspace
}

func fileInput(t *testing.T, path string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestNoteReadFile covers AC-PCX-006 and AC-PCX-007.
func TestNoteReadFile(t *testing.T) {
	home, repo, workspace := trackerFixture(t)
	sub := filepath.Join(repo, "sub")
	mkdir(t, sub)

	tr := NewTracker([]string{"AGENTS.md", "CLAUDE.md"}, home, workspace, nil)
	input := fileInput(t, filepath.Join(sub, "x.go"))

	got := tr.Note("read_file", input)
	if !strings.Contains(got, "repo rules") || !strings.Contains(got, filepath.Join(repo, "CLAUDE.md")) {
		t.Fatalf("first Note = %q want the repo CLAUDE.md", got)
	}
	if again := tr.Note("read_file", input); again != "" {
		t.Fatalf("second Note = %q want empty (AC-PCX-007)", again)
	}
}

// TestNoteBash covers the leading cd of AC-PCX-008 and the tools that do not
// count.
func TestNoteBash(t *testing.T) {
	home, repo, workspace := trackerFixture(t)
	names := []string{"AGENTS.md", "CLAUDE.md"}

	tr := NewTracker(names, home, workspace, nil)
	cmd, err := json.Marshal(map[string]string{"command": "cd " + repo + " && go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Note("bash", cmd); !strings.Contains(got, "repo rules") {
		t.Fatalf("bash cd Note = %q want the repo CLAUDE.md", got)
	}

	ls, _ := json.Marshal(map[string]string{"command": "ls"})
	if got := tr.Note("bash", ls); got != "" {
		t.Fatalf("bash ls Note = %q want empty", got)
	}
	if got := tr.Note("web_fetch", json.RawMessage(`{"url":"https://example.com"}`)); got != "" {
		t.Fatalf("web_fetch Note = %q want empty", got)
	}
}

// TestNoteWriteFileNewDir covers a path that does not exist yet: the walk starts
// at its nearest existing ancestor.
func TestNoteWriteFileNewDir(t *testing.T) {
	home, repo, workspace := trackerFixture(t)
	tr := NewTracker([]string{"AGENTS.md", "CLAUDE.md"}, home, workspace, nil)

	input := fileInput(t, filepath.Join(repo, "newdir", "deeper", "new.go"))
	if got := tr.Note("write_file", input); !strings.Contains(got, "repo rules") {
		t.Fatalf("write_file Note = %q want the repo CLAUDE.md", got)
	}
}

// TestNoteGlobEmptyPath covers the workspace fallback of an empty glob path.
func TestNoteGlobEmptyPath(t *testing.T) {
	home, _, workspace := trackerFixture(t)
	writeFile(t, filepath.Join(workspace, "AGENTS.md"), "workspace rules")
	tr := NewTracker([]string{"AGENTS.md", "CLAUDE.md"}, home, workspace, nil)

	if got := tr.Note("glob", json.RawMessage(`{}`)); !strings.Contains(got, "workspace rules") {
		t.Fatalf("glob Note = %q want the workspace AGENTS.md", got)
	}
}

// TestNoteEmptyNames covers the feature being off.
func TestNoteEmptyNames(t *testing.T) {
	home, repo, workspace := trackerFixture(t)
	tr := NewTracker(nil, home, workspace, nil)
	if got := tr.Note("read_file", fileInput(t, filepath.Join(repo, "x.go"))); got != "" {
		t.Fatalf("Note with empty names = %q want empty", got)
	}
}

// TestNoteConcurrent runs with -race: 20 goroutines touching the same
// repository must yield exactly one non-empty result.
func TestNoteConcurrent(t *testing.T) {
	home, repo, workspace := trackerFixture(t)
	tr := NewTracker([]string{"AGENTS.md", "CLAUDE.md"}, home, workspace, nil)

	const n = 20
	results := make([]string, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			path := filepath.Join(repo, fmt.Sprintf("file%d.go", i))
			results[i] = tr.Note("read_file", json.RawMessage(`{"path":`+strconv.Quote(path)+`}`))
		}(i)
	}
	close(start)
	wg.Wait()

	nonEmpty := 0
	for _, r := range results {
		if r != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Fatalf("non-empty results = %d want 1", nonEmpty)
	}
}

// TestSeenFromEvents covers the rebuild of the seen set (AC-PCX-009, AC-PCX-010
// at package level).
func TestSeenFromEvents(t *testing.T) {
	system := func(text string) core.SessionEvent {
		b, err := json.Marshal(core.SystemMessage{V: core.PayloadVersion, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return core.SessionEvent{Kind: core.KindSystemMessage, Payload: b}
	}
	note := func(text string) core.SessionEvent {
		b, err := json.Marshal(core.SystemMessage{V: core.PayloadVersion, Subtype: core.SystemSubtypeNote, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return core.SessionEvent{Kind: core.KindSystemMessage, Payload: b}
	}
	toolResult := func(output string) core.SessionEvent {
		b, err := json.Marshal(core.ToolResultPayload{V: core.PayloadVersion, Status: core.ToolStatusSuccess, Output: output})
		if err != nil {
			t.Fatal(err)
		}
		return core.SessionEvent{Kind: core.KindToolResult, Payload: b}
	}
	compaction := core.SessionEvent{Kind: core.KindCompactionSummary, Payload: json.RawMessage(`{"v":1,"summary":"x"}`)}
	marker := func(path string) string {
		return "<project-context path=\"" + path + "\">\nbody\n</project-context>"
	}

	cases := []struct {
		name   string
		events []core.SessionEvent
		want   []string
	}{
		{
			name:   "head and tool results without a compaction",
			events: []core.SessionEvent{system(marker("/a/AGENTS.md")), toolResult(marker("/b/CLAUDE.md"))},
			want:   []string{"/a/AGENTS.md", "/b/CLAUDE.md"},
		},
		{
			name:   "markers before a compaction are ignored",
			events: []core.SessionEvent{system(marker("/before/AGENTS.md")), compaction, toolResult(marker("/after/CLAUDE.md"))},
			want:   []string{"/after/CLAUDE.md"},
		},
		{
			name: "the head after a compaction counts and a note does not",
			events: []core.SessionEvent{
				compaction,
				system(marker("/head/AGENTS.md")),
				note(marker("/note/AGENTS.md")),
				toolResult(marker("/tool/CLAUDE.md")),
			},
			want: []string{"/head/AGENTS.md", "/tool/CLAUDE.md"},
		},
		{
			name: "unique paths in first-seen order",
			events: []core.SessionEvent{
				toolResult(marker("/x/AGENTS.md")),
				toolResult(marker("/y/AGENTS.md")),
				toolResult(marker("/x/AGENTS.md")),
			},
			want: []string{"/x/AGENTS.md", "/y/AGENTS.md"},
		},
		{
			name: "events that fail to unmarshal are ignored",
			events: []core.SessionEvent{
				{Kind: core.KindToolResult, Payload: json.RawMessage(`not json`)},
				{Kind: core.KindSystemMessage, Payload: json.RawMessage(`{}`)},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SeenFromEvents(tc.events)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SeenFromEvents = %q want %q", got, tc.want)
			}
		})
	}
}

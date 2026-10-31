package agenthome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, name := range []string{"a", "helper", "my-agent_2", strings.Repeat("a", 32)} {
		if !ValidName(name) {
			t.Fatalf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "Helper", "helper bot", "1abc", "-a", "../x", strings.Repeat("a", 33)} {
		if ValidName(name) {
			t.Fatalf("%q should be invalid", name)
		}
	}
}

func TestPathsAndSharedUser(t *testing.T) {
	lanaHome := t.TempDir()
	dir := filepath.Join(lanaHome, "agents", "helper")
	home := Paths(lanaHome, "helper")
	if home.Dir != dir {
		t.Fatalf("dir = %q, want %q", home.Dir, dir)
	}
	if home.Agent != filepath.Join(dir, "AGENT.md") {
		t.Fatalf("agent = %q", home.Agent)
	}
	if home.Memory != filepath.Join(dir, "MEMORY.md") {
		t.Fatalf("memory = %q", home.Memory)
	}
	if got, want := SharedUser(lanaHome), filepath.Join(lanaHome, "USER.md"); got != want {
		t.Fatalf("shared user = %q, want %q", got, want)
	}
}

func TestParseAndJoin(t *testing.T) {
	entries := Parse("one\n§\n\n  §  \ntwo\nlines\n§\n")
	if len(entries) != 2 || entries[0] != "one" || entries[1] != "two\nlines" {
		t.Fatalf("entries = %#v", entries)
	}
	if got := Join(entries); got != "one\n§\ntwo\nlines" {
		t.Fatalf("join = %q", got)
	}
}

func TestApply(t *testing.T) {
	entries, err := Apply(nil, "add", "Owner uses fish shell", "", MemoryLimit)
	if err != nil || len(entries) != 1 {
		t.Fatalf("add = %#v, %v", entries, err)
	}
	entries, _ = Apply(entries, "add", "Server kenari runs Debian", "", MemoryLimit)
	if _, err := Apply(entries, "replace", "x", "s", MemoryLimit); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous err = %v", err)
	}
	if _, err := Apply(entries, "remove", "", "nothing", MemoryLimit); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("no match err = %v", err)
	}
	entries, err = Apply(entries, "replace", "Server kenari runs Debian 13", "kenari", MemoryLimit)
	if err != nil || entries[1] != "Server kenari runs Debian 13" {
		t.Fatalf("replace = %#v, %v", entries, err)
	}
	entries, err = Apply(entries, "remove", "", "fish", MemoryLimit)
	if err != nil || len(entries) != 1 {
		t.Fatalf("remove = %#v, %v", entries, err)
	}
	if _, err := Apply(entries, "add", "a\n§\nb", "", MemoryLimit); !errors.Is(err, ErrInvalid) {
		t.Fatalf("separator err = %v", err)
	}
	if _, err := Apply(entries, "add", " ", "", MemoryLimit); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty err = %v", err)
	}
}

func TestApplyOverLimit(t *testing.T) {
	entries := []string{strings.Repeat("x", 47)}
	_, err := Apply(entries, "add", strings.Repeat("y", 100), "", 100)
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || applyErr.Kind != ErrOverLimit || applyErr.Size != 150 || applyErr.Limit != 100 {
		t.Fatalf("err = %#v", err)
	}
}

func TestUpdateConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents", "a", "MEMORY.md")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Update(path, func(entries []string) ([]string, error) {
				return Apply(entries, "add", fmt.Sprintf("entry %02d", i), "", MemoryLimit)
			})
			if err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	entries, err := ReadEntries(path)
	if err != nil || len(entries) != 20 {
		t.Fatalf("entries = %d, %v", len(entries), err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
}

func TestUpdateKeepsFileOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "USER.md")
	if err := WriteFile(path, "keep"); err != nil {
		t.Fatal(err)
	}
	_, err := Update(path, func(entries []string) ([]string, error) {
		return Apply(entries, "remove", "", "missing", UserLimit)
	})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestBuild(t *testing.T) {
	got := Build(PromptInput{
		Persona: "You are helper.\n", User: []string{"Name: Ridho"}, Memory: []string{"Uses fish", "Server kenari"},
		Agent: "helper", Channel: "cli", Workspace: "/tmp/x",
	})
	want := `You are helper.

# About the user
Name: Ridho

# Your memory
Uses fish
§
Server kenari

# When something blocks you
- If a tool or MCP server you need is unavailable, or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry).
- Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash).
- If you cannot finish the request, say so plainly and say what is still needed. Never end a turn without telling the owner the result.

# Runtime context
- agent: helper
- channel: cli
- workspace: /tmp/x`
	if got != want {
		t.Fatalf("prompt:\n%s\n--- want:\n%s", got, want)
	}
	if strings.Contains(got, "- date:") {
		t.Fatalf("head prompt must not carry the date:\n%s", got)
	}
}

func TestBuildTruncationWithoutOnboarding(t *testing.T) {
	got := Build(PromptInput{Agent: "a", Memory: []string{strings.Repeat("m", MemoryLimit+10)}})
	if strings.Contains(strings.ToLower(got), "onboarding") || strings.Contains(got, "# About the user") {
		t.Fatalf("empty user file must add nothing:\n%s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("[truncated: %d/%d characters; consolidate with memory_write]", MemoryLimit+10, MemoryLimit)) {
		t.Fatal("truncation marker missing")
	}
	if strings.Index(got, "# Your memory") > strings.Index(got, "# Runtime context") {
		t.Fatal("section order wrong")
	}
}

func TestTemplates(t *testing.T) {
	if got := strings.Join(TemplateNames(), ","); got != "assistant,coder,worker" {
		t.Fatalf("names = %s", got)
	}
	for _, name := range TemplateNames() {
		tpl, _ := LookupTemplate(name)
		persona, err := tpl.Persona("bob")
		if err != nil || !strings.Contains(persona, "You are bob") {
			t.Fatalf("%s persona = %q, %v", name, persona, err)
		}
		if !contains(tpl.Tools, "memory_read") || !contains(tpl.Tools, "memory_write") {
			t.Fatalf("%s lacks memory tools", name)
		}
	}
}

func TestTemplateToolsFullSet(t *testing.T) {
	wantTools := []string{"read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch", "memory_read", "memory_write", "skill_write", "ask_user"}
	for _, name := range TemplateNames() {
		tpl, ok := LookupTemplate(name)
		if !ok {
			t.Fatalf("%s not found", name)
		}
		if !slices.Equal(tpl.Tools, wantTools) {
			t.Fatalf("%s tools = %v", name, tpl.Tools)
		}
		if tpl.Permission != "ask" {
			t.Fatalf("%s permission = %q", name, tpl.Permission)
		}
	}

	// Each template owns its Tools slice: mutating one must not leak into
	// another template or a fresh lookup of one.
	first, _ := LookupTemplate("assistant")
	second, _ := LookupTemplate("worker")
	fresh, _ := LookupTemplate("coder")
	first.Tools[0] = "mutated"
	defer func() { first.Tools[0] = "read_file" }()
	if slices.Contains(second.Tools, "mutated") || slices.Contains(fresh.Tools, "mutated") {
		t.Fatalf("templates share a tools slice: worker=%v coder=%v", second.Tools, fresh.Tools)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

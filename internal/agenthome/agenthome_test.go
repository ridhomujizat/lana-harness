package agenthome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	now := time.Date(2026, 10, 29, 21, 5, 0, 0, time.FixedZone("WIB", 7*3600))
	got := Build(PromptInput{
		Persona: "You are helper.\n", User: []string{"Name: Ridho"}, Memory: []string{"Uses fish", "Server kenari"},
		Agent: "helper", Channel: "cli", Workspace: "/tmp/x", Now: now,
	})
	want := `You are helper.

# About the user
Name: Ridho

# Your memory
Uses fish
§
Server kenari

# Runtime context
- agent: helper
- date: 2026-10-29 Thursday 21:05 WIB (UTC+07:00)
- channel: cli
- workspace: /tmp/x`
	if got != want {
		t.Fatalf("prompt:\n%s\n--- want:\n%s", got, want)
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

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

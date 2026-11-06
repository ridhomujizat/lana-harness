package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

func TestEvalTaskSet(t *testing.T) {
	want := []string{
		"edit-small", "fix-bug", "rename-symbol", "serve-background",
		"find-definition", "large-output", "recover-error", "memory-recall",
		"greeting", "edit-indent", "blocked", "long-context",
	}
	if len(evalTasks) != len(want) {
		t.Fatalf("task count = %d, want %d", len(evalTasks), len(want))
	}
	byName := map[string]evalTask{}
	for _, task := range evalTasks {
		if _, exists := byName[task.Name]; exists {
			t.Fatalf("duplicate task name %q", task.Name)
		}
		byName[task.Name] = task
		if strings.TrimSpace(task.Prompt) == "" {
			t.Errorf("%s has an empty prompt", task.Name)
		}
		if strings.TrimSpace(task.Check) == "" {
			t.Errorf("%s has an empty check", task.Name)
		}
	}
	for _, name := range want {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing task %q", name)
		}
	}
	if byName["long-context"].Agent["context_window"] != 24000 {
		t.Errorf("long-context context_window = %v, want 24000",
			byName["long-context"].Agent["context_window"])
	}
	if !strings.Contains(byName["serve-background"].Prompt, "{{port}}") ||
		!strings.Contains(byName["serve-background"].Check, "{{port}}") {
		t.Error("serve-background does not use {{port}}")
	}
	if !strings.Contains(byName["long-context"].Check, "LANA_EVAL_COMPACTIONS") {
		t.Error("long-context check does not read LANA_EVAL_COMPACTIONS")
	}
}

func TestMemoryRecallTask(t *testing.T) {
	task := taskByName(t, "memory-recall")
	if len(task.Memory) != 40 {
		t.Fatalf("memory entries = %d, want 40", len(task.Memory))
	}
	if size := agenthome.Size(task.Memory); size <= 2000 {
		t.Fatalf("memory size = %d, want an index-mode size above 2000", size)
	}
	var hasBuildMode bool
	for _, entry := range task.Memory {
		if strings.Contains(entry, "BUILD_MODE=fast") {
			hasBuildMode = true
		}
	}
	if !hasBuildMode {
		t.Fatal("memory has no BUILD_MODE=fast entry")
	}
	if task.Prompt != "run the build and tell me the result" {
		t.Fatalf("prompt = %q", task.Prompt)
	}
	if !strings.Contains(task.Files["build.sh"], "build failed: exit 3") {
		t.Fatal("build.sh does not fail with the generic exit 3 message")
	}
}

func TestGreetingTask(t *testing.T) {
	task := taskByName(t, "greeting")
	if task.Prompt != "hi" {
		t.Fatalf("prompt = %q", task.Prompt)
	}
	wantTools := []string{"read_file", "write_file", "edit_file", "glob", "grep", "bash"}
	if len(task.Tools) != len(wantTools) {
		t.Fatalf("tools = %v, want %v", task.Tools, wantTools)
	}
	for i := range wantTools {
		if task.Tools[i] != wantTools[i] {
			t.Fatalf("tools = %v, want %v", task.Tools, wantTools)
		}
	}
	if len(task.Memory) == 0 {
		t.Fatal("greeting has no seeded tool notes")
	}
}

func TestEditIndentUsesMixedWhitespace(t *testing.T) {
	task := taskByName(t, "edit-indent")
	source := task.Files["render.py"]
	// The obvious old_string uses spaces only; it must not match.
	naive := "        if item:\n            lines.append(\"- \" + item)"
	if strings.Contains(source, naive) {
		t.Fatal("render.py matches a spaces-only old_string")
	}
	if !strings.Contains(source, "\t\tif item:\n\t\t    lines.append") {
		t.Fatalf("render.py does not use the mixed indentation:\n%s", source)
	}
}

func TestFixBugTestHash(t *testing.T) {
	var testFile string
	for _, task := range evalTasks {
		if task.Name == "fix-bug" {
			testFile = task.Files["test_calc.py"]
		}
	}
	if testFile == "" {
		t.Fatal("fix-bug test_calc.py is missing")
	}
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(testFile)))
	if got != fixBugTestSHA256 {
		t.Fatalf("fix-bug test hash = %s, want %s", got, fixBugTestSHA256)
	}
}

func TestEditIndentTestHash(t *testing.T) {
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(editIndentTestFile)))
	if got != editIndentTestSHA256 {
		t.Fatalf("edit-indent test hash = %s, want %s", got, editIndentTestSHA256)
	}
}

func TestSetupScriptsArePython39(t *testing.T) {
	for _, task := range evalTasks {
		if usesFString(task.Setup) || usesFString(task.Check) {
			t.Errorf("%s uses an f-string; keep scripts Python 3.9 compatible", task.Name)
		}
	}
}

// taskByName returns the task with name.
func taskByName(t *testing.T, name string) evalTask {
	t.Helper()
	for _, task := range evalTasks {
		if task.Name == name {
			return task
		}
	}
	t.Fatalf("task %q is missing", name)
	return evalTask{}
}

// runTaskCheck runs a task's check in workspace with env.
func runTaskCheck(t *testing.T, task evalTask, workspace string, env map[string]string) (string, error) {
	t.Helper()
	return runShellEnv(context.Background(), workspace, task.Check, commandTimeout, env)
}

func TestMemoryRecallCheck(t *testing.T) {
	task := taskByName(t, "memory-recall")
	workspace := t.TempDir()
	if err := writeTaskFiles(workspace, task.Files); err != nil {
		t.Fatal(err)
	}

	// Without BUILD_MODE=fast the build fails with the generic message.
	output, err := runShellEnv(context.Background(), workspace, "sh build.sh", commandTimeout, nil)
	if err == nil {
		t.Fatal("build.sh succeeded without BUILD_MODE=fast")
	}
	if !strings.Contains(output, "build failed: exit 3") {
		t.Fatalf("build output = %q, want the generic error", output)
	}
	if _, err := runTaskCheck(t, task, workspace, checkEnv(0, 0)); err == nil {
		t.Fatal("check passed without an artifact")
	}

	// With BUILD_MODE=fast the artifact exists and the check passes.
	if _, err := runShellEnv(context.Background(), workspace, "sh build.sh", commandTimeout,
		map[string]string{"BUILD_MODE": "fast"}); err != nil {
		t.Fatalf("build.sh with BUILD_MODE=fast: %v", err)
	}
	if _, err := runTaskCheck(t, task, workspace, checkEnv(0, 0)); err != nil {
		t.Fatalf("check failed with an artifact: %v", err)
	}
}

func TestGreetingCheck(t *testing.T) {
	task := taskByName(t, "greeting")
	tests := []struct {
		name   string
		answer string
		wantOK bool
	}{
		{name: "greeting", answer: "Hello! How can I help you today?", wantOK: true},
		{name: "tool talk", answer: "The bash tool is ready.", wantOK: false},
		{name: "mcp talk", answer: "No MCP servers are connected.", wantOK: false},
		{name: "memory talk", answer: "My memory is empty.", wantOK: false},
		{name: "too long", answer: strings.Repeat("a", 300), wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := writeAnswer(workspace, tt.answer); err != nil {
				t.Fatal(err)
			}
			_, err := runTaskCheck(t, task, workspace, checkEnv(0, 0))
			if tt.wantOK && err != nil {
				t.Fatalf("check failed: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("check passed a failing answer")
			}
		})
	}
}

func TestEditIndentCheck(t *testing.T) {
	task := taskByName(t, "edit-indent")
	workspace := t.TempDir()
	if err := writeTaskFiles(workspace, task.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := runTaskCheck(t, task, workspace, checkEnv(0, 0)); err == nil {
		t.Fatal("check passed the buggy source")
	}

	if err := os.WriteFile(filepath.Join(workspace, "render.py"), []byte(editIndentFixedSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runTaskCheck(t, task, workspace, checkEnv(0, 0)); err != nil {
		t.Fatalf("check failed the fixed source: %v", err)
	}

	// Changing the tests must fail even when the source is fixed.
	changed := editIndentTestFile + "# changed\n"
	if err := os.WriteFile(filepath.Join(workspace, "test_render.py"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runTaskCheck(t, task, workspace, checkEnv(0, 0)); err == nil {
		t.Fatal("check passed a changed test file")
	}
}

func TestBlockedCheck(t *testing.T) {
	task := taskByName(t, "blocked")
	tests := []struct {
		name   string
		answer string
		env    map[string]string
		wantOK bool
	}{
		{name: "reports permission", answer: "I could not stage the release: permission denied: /opt/vendor/secret.key.", env: checkEnv(0, 3), wantOK: true},
		{name: "misses permission", answer: "The release was staged.", env: checkEnv(0, 3), wantOK: false},
		{name: "too many calls", answer: "Permission denied: /opt/vendor/secret.key.", env: checkEnv(0, 13), wantOK: false},
		{name: "missing call count", answer: "Permission denied: /opt/vendor/secret.key.", env: map[string]string{}, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := writeAnswer(workspace, tt.answer); err != nil {
				t.Fatal(err)
			}
			_, err := runTaskCheck(t, task, workspace, tt.env)
			if tt.wantOK && err != nil {
				t.Fatalf("check failed: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("check passed a failing answer")
			}
		})
	}
}

func TestLongContextCheck(t *testing.T) {
	task := taskByName(t, "long-context")
	workspace := t.TempDir()

	writeAnswers := func(t *testing.T, words []string) {
		t.Helper()
		text := strings.Join(words, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(workspace, "answers.txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeAnswers(t, longContextWords)
	if _, err := runTaskCheck(t, task, workspace, checkEnv(1, 0)); err != nil {
		t.Fatalf("check failed with one compaction and the right words: %v", err)
	}
	if _, err := runTaskCheck(t, task, workspace, checkEnv(0, 0)); err == nil {
		t.Fatal("check passed without a compaction")
	}

	wrong := append([]string(nil), longContextWords...)
	wrong[0] = "wrong"
	writeAnswers(t, wrong)
	if _, err := runTaskCheck(t, task, workspace, checkEnv(1, 0)); err == nil {
		t.Fatal("check passed the wrong words")
	}
}

// usesFString reports whether script holds a Python f-string. The preceding
// character check avoids matching words such as "golf".
func usesFString(script string) bool {
	for i := 0; i+1 < len(script); i++ {
		if script[i] != 'f' || (script[i+1] != '"' && script[i+1] != '\'') {
			continue
		}
		if i == 0 {
			return true
		}
		prev := script[i-1]
		if prev != '_' && (prev < '0' || prev > '9') && (prev < 'a' || prev > 'z') && (prev < 'A' || prev > 'Z') {
			return true
		}
	}
	return false
}

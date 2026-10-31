package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestEvalTaskSet(t *testing.T) {
	want := []string{
		"edit-small", "fix-bug", "rename-symbol", "serve-background",
		"find-definition", "large-output", "recover-error", "long-context",
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

func TestSetupScriptsArePython39(t *testing.T) {
	for _, task := range evalTasks {
		if usesFString(task.Setup) || usesFString(task.Check) {
			t.Errorf("%s uses an f-string; keep scripts Python 3.9 compatible", task.Name)
		}
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

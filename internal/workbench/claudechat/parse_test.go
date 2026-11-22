package claudechat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadScreen reads a rendered screen fixture as it is stored: plain text split
// on "\n", trailing spaces preserved.
func loadScreen(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "screens", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return strings.Split(string(b), "\n")
}

type optionWant struct {
	key      string
	label    string
	selected bool
}

func wantOptions(t *testing.T, got []DialogOption, want []optionWant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("options = %+v, want %d options", got, len(want))
	}
	for i, w := range want {
		if got[i].Key != w.key || got[i].Label != w.label || got[i].Selected != w.selected {
			t.Errorf("option %d = %+v, want {key:%q label:%q selected:%v}", i, got[i], w.key, w.label, w.selected)
		}
	}
}

func wantDialog(t *testing.T, s Screen, kind, title string) *Dialog {
	t.Helper()
	if s.Dialog == nil {
		t.Fatalf("Dialog = nil, want kind %q", kind)
	}
	if s.Dialog.Kind != kind {
		t.Errorf("dialog kind = %q, want %q", s.Dialog.Kind, kind)
	}
	if s.Dialog.Title != title {
		t.Errorf("dialog title = %q, want %q", s.Dialog.Title, title)
	}
	return s.Dialog
}

func checkMode(mode string) func(*testing.T, Screen) {
	return func(t *testing.T, s Screen) {
		t.Helper()
		if s.Mode != mode {
			t.Errorf("Mode = %q, want %q", s.Mode, mode)
		}
		if s.Dialog != nil {
			t.Errorf("Dialog = %+v, want nil", s.Dialog)
		}
	}
}

func TestParseScreenFixtures(t *testing.T) {
	cases := []struct {
		file  string
		check func(t *testing.T, s Screen)
	}{
		{"01-idle.txt", func(t *testing.T, s Screen) {
			if s.Working {
				t.Errorf("Working = true, want false")
			}
			if s.Status != "" {
				t.Errorf("Status = %q, want empty", s.Status)
			}
			if s.Mode != "default" {
				t.Errorf("Mode = %q, want default", s.Mode)
			}
			if s.Effort != "medium" {
				t.Errorf("Effort = %q, want medium", s.Effort)
			}
			if !s.InputEmpty || s.Input != "" {
				t.Errorf("input = (%q, empty=%v), want empty", s.Input, s.InputEmpty)
			}
			if s.Dialog != nil {
				t.Errorf("Dialog = %+v, want nil", s.Dialog)
			}
		}},
		{"02-typed.txt", func(t *testing.T, s Screen) {
			if s.InputEmpty {
				t.Errorf("InputEmpty = true, want false")
			}
			if s.Input != "hello world" {
				t.Errorf("Input = %q, want %q", s.Input, "hello world")
			}
		}},
		{"03-mode-0.txt", checkMode("acceptEdits")},
		{"03-mode-1.txt", checkMode("plan")},
		{"03-mode-2.txt", checkMode("auto")},
		{"03-mode-3.txt", checkMode("default")},
		{"05-working.txt", func(t *testing.T, s Screen) {
			if !s.Working {
				t.Errorf("Working = false, want true")
			}
			if s.Status != "· Incubating…" {
				t.Errorf("Status = %q, want %q", s.Status, "· Incubating…")
			}
			if s.Mode != "acceptEdits" {
				t.Errorf("Mode = %q, want acceptEdits", s.Mode)
			}
			if !s.InputEmpty {
				t.Errorf("InputEmpty = false, want true")
			}
		}},
		{"p-working-02.txt", func(t *testing.T, s Screen) {
			if !s.Working {
				t.Errorf("Working = false, want true")
			}
			if s.Status != "✶ Flowing…" {
				t.Errorf("Status = %q, want %q", s.Status, "✶ Flowing…")
			}
		}},
		{"07-after.txt", func(t *testing.T, s Screen) {
			if s.Working {
				t.Errorf("Working = true, want false")
			}
			if s.Status != "" {
				t.Errorf("Status = %q, want empty (finished line is not live)", s.Status)
			}
			if !s.InputEmpty {
				t.Errorf("InputEmpty = false, want true (dim suggestion)")
			}
		}},
		{"09-question-after.txt", func(t *testing.T, s Screen) {
			if s.Working {
				t.Errorf("Working = true, want false")
			}
			if s.Status != "" {
				t.Errorf("Status = %q, want empty", s.Status)
			}
			if !s.InputEmpty {
				t.Errorf("InputEmpty = false, want true")
			}
			if s.Dialog != nil {
				t.Errorf("Dialog = %+v, want nil", s.Dialog)
			}
		}},
		{"08-question.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "question", "What is your favorite color?")
			if d == nil {
				return
			}
			wantOptions(t, d.Options, []optionWant{
				{"1", "Red", true},
				{"2", "Blue", false},
				{"3", "Type something.", false},
				{"4", "Chat about this", false},
			})
		}},
		{"08b-question-down.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "question", "What is your favorite color?")
			if d == nil {
				return
			}
			wantOptions(t, d.Options, []optionWant{
				{"1", "Red", false},
				{"2", "Blue", true},
				{"3", "Type something.", false},
				{"4", "Chat about this", false},
			})
		}},
		{"13-plan.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "plan", "Claude has written up a plan and is ready to execute. Would you like to proceed?")
			if d == nil {
				return
			}
			wantOptions(t, d.Options, []optionWant{
				{"1", "Yes, and use auto mode", true},
				{"2", "Yes, manually approve edits", false},
				{"3", "Tell Claude what to change", false},
			})
		}},
		{"p-permission.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "permission", "Do you want to proceed?")
			if d == nil {
				return
			}
			// No option carries a ❯ (the render glitch wiped option 1's
			// marker), so the rules make the first option — the prepended
			// "Yes" — the selected one.
			wantOptions(t, d.Options, []optionWant{
				{"1", "Yes", true},
				{"2", "Yes, and always allow access to /tmp/probe-proj from this project", false},
				{"3", "Yes, and switch to auto mode · auto mode handles these prompts for you", false},
				{"4", "No", false},
			})
			if !contains(d.Lines, "touch probe-file.txt") {
				t.Errorf("dialog lines = %q, want to include %q", d.Lines, "touch probe-file.txt")
			}
		}},
		{"p-permission-down.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "permission", "Do you want to proceed?")
			if d == nil {
				return
			}
			if len(d.Options) != 4 || d.Options[1].Key != "2" || !d.Options[1].Selected {
				t.Errorf("options = %+v, want option 2 selected", d.Options)
			}
		}},
		{"14-plan-esc.txt", func(t *testing.T, s Screen) {
			if s.Dialog != nil {
				t.Errorf("Dialog = %+v, want nil", s.Dialog)
			}
		}},
		{"p-after-esc.txt", func(t *testing.T, s Screen) {
			if s.Dialog != nil {
				t.Errorf("Dialog = %+v, want nil", s.Dialog)
			}
		}},
		{"00-trust.txt", func(t *testing.T, s Screen) {
			if s.Dialog == nil {
				t.Fatalf("Dialog = nil, want the trust prompt")
			}
			if n := len(s.Dialog.Options); n != 2 {
				t.Fatalf("options = %+v, want 2", s.Dialog.Options)
			}
			if s.Dialog.Options[0].Label != "No, exit" || !s.Dialog.Options[0].Selected {
				t.Errorf("option 1 = %+v, want selected \"No, exit\"", s.Dialog.Options[0])
			}
			if s.Dialog.Options[1].Label != "Yes, I trust this folder" || s.Dialog.Options[1].Selected {
				t.Errorf("option 2 = %+v", s.Dialog.Options[1])
			}
		}},
		{"00-start.txt", func(t *testing.T, s Screen) {
			// 00-start.txt was captured in an already trusted folder, so it
			// is the idle screen; 00-trust.txt holds the trust prompt.
			if s.Dialog != nil {
				t.Errorf("Dialog = %+v, want nil (this fixture has no trust prompt)", s.Dialog)
			}
			if !s.InputEmpty {
				t.Errorf("InputEmpty = false, want true (the ❯ line is the placeholder)")
			}
			if s.Mode != "default" {
				t.Errorf("Mode = %q, want default", s.Mode)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			tc.check(t, ParseScreen(loadScreen(t, tc.file)))
		})
	}
}

// TestParseUnnumberedTrustDialog covers the unnumbered list the fixtures do not
// contain: a ❯-marked choice with sibling options at the label column.
func TestParseUnnumberedTrustDialog(t *testing.T) {
	lines := []string{
		"",
		"Do you trust the files in this folder?",
		"",
		"❯  No, exit",
		"   Yes, I trust this folder",
		"",
		"Enter to confirm · Esc to cancel",
		"",
	}
	s := ParseScreen(lines)
	d := wantDialog(t, s, "other", "Do you trust the files in this folder?")
	if d == nil {
		return
	}
	wantOptions(t, d.Options, []optionWant{
		{"1", "No, exit", true},
		{"2", "Yes, I trust this folder", false},
	})
}

func TestParseScreenNeverPanics(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{""},
		{"❯"},
		{"❯ "},
		{"─"},
		{"─", "❯", "─"},
		{"1."},
		{"１． 数字"},
		{"\xff\xfe"},
		{"\x1b[31mred\x1b[0m"},
	}
	for _, lines := range cases {
		_ = ParseScreen(lines)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

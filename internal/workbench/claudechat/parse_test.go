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

type tabWant struct {
	label string
	done  bool
}

func wantTabs(t *testing.T, got []DialogTab, want []tabWant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tabs = %+v, want %d tabs", got, len(want))
	}
	for i, w := range want {
		if got[i].Label != w.label || got[i].Done != w.done {
			t.Errorf("tab %d = %+v, want {label:%q done:%v}", i, got[i], w.label, w.done)
		}
	}
}

func wantBody(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("body[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// wantActive checks which tabs are active: exactly the given labels, in order.
// With no labels it requires that no tab is active.
func wantActive(t *testing.T, got []DialogTab, want ...string) {
	t.Helper()
	var labels []string
	for _, tab := range got {
		if tab.Active {
			labels = append(labels, tab.Label)
		}
	}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("active tabs = %q, want %q", labels, want)
	}
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
			// A plain question draws nothing between its title and options.
			wantBody(t, d.Body, nil)
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
		{"question-preview.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "question", "Pilih tampilan untuk ditampilkan:")
			if d == nil {
				return
			}
			wantOptions(t, d.Options, []optionWant{
				{"1", "Kartu sederhana", false},
				{"2", "Dua kolom", false},
				{"3", "Tabel", true},
				{"", "Chat about this", false},
			})
			if !strings.HasPrefix(d.Preview, "┌──────┬──────────┬───────┐") {
				t.Errorf("preview = %q, want it to start with the box's inner top border", d.Preview)
			}
			if !strings.Contains(d.Preview, "│ 1    │ Alpha    │ 90    │") {
				t.Errorf("preview = %q, want it to contain the first table row", d.Preview)
			}
			for _, o := range d.Options {
				if strings.ContainsAny(o.Label, "┌│") {
					t.Errorf("option label %q still carries box characters", o.Label)
				}
			}
		}},
		{"question-review.txt", func(t *testing.T, s Screen) {
			d := wantDialog(t, s, "question", "Review your answers")
			if d == nil {
				return
			}
			wantTabs(t, d.Tabs, []tabWant{
				{"Topik", true},
				{"Format", true},
				{"Submit", true},
			})
			wantOptions(t, d.Options, []optionWant{
				{"1", "Submit answers", true},
				{"2", "Cancel", false},
			})
			// The review screen repeats the answered questions below its title.
			wantBody(t, d.Body, []string{
				"● Pilih topik pertanyaan yang Anda mau (boleh lebih dari satu):",
				"→ Coding, API / Postman",
				"● Format pertanyaan seperti apa yang Anda mau?",
				"→ Isian singkat",
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
		{"plan-live.txt", func(t *testing.T, s Screen) {
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
		{"idle-numbered.txt", func(t *testing.T, s Screen) {
			if s.Dialog != nil {
				t.Errorf("Dialog = %+v, want nil (a numbered reply, not a dialog)", s.Dialog)
			}
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

// TestParsePlanWrappedTitle covers a narrow pane whose plan approval title
// wraps onto two lines: the join puts the sentence back together.
func TestParsePlanWrappedTitle(t *testing.T) {
	lines := loadScreen(t, "plan-live.txt")
	full := "Claude has written up a plan and is ready to execute. Would you like to proceed?"
	wrapped := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		if strings.TrimSpace(line) == full {
			wrapped = append(wrapped,
				"   Claude has written up a plan and is ready to execute. Would you",
				"   like to proceed?")
			continue
		}
		wrapped = append(wrapped, line)
	}
	s := ParseScreen(wrapped)
	d := wantDialog(t, s, "plan", full)
	if d == nil {
		return
	}
	wantOptions(t, d.Options, []optionWant{
		{"1", "Yes, and use auto mode", true},
		{"2", "Yes, manually approve edits", false},
		{"3", "Tell Claude what to change", false},
	})
}

// TestParseQuestionTabs covers the tabbed AskUserQuestion dialog as rendered
// from the raw pane replay: a chips row, the title below it, per-option
// descriptions and the option outside the box.
func TestParseQuestionTabs(t *testing.T) {
	m := NewMirror(100, 40)
	defer m.Close()
	if _, err := m.Write(rawPane(t, "question.raw")); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := ParseScreen(m.Lines())
	d := wantDialog(t, s, "question", "Pick a color.")
	if d == nil {
		return
	}
	wantTabs(t, d.Tabs, []tabWant{
		{"Color", false},
		{"Toppings", false},
		{"Size", false},
		{"Submit", true},
	})
	if len(d.Options) != 5 {
		t.Fatalf("options = %+v, want 5", d.Options)
	}
	o := d.Options
	if o[0].Key != "1" || o[0].Label != "Red (Recommended)" || !o[0].Selected || o[0].Description != "Bold, high-energy choice" {
		t.Errorf("option 1 = %+v", o[0])
	}
	if o[1].Key != "2" || o[1].Label != "Blue" || o[1].Selected || o[1].Description != "Calm, neutral choice" {
		t.Errorf("option 2 = %+v", o[1])
	}
	if o[2].Key != "3" || o[2].Label != "Green" || o[2].Description != "Fresh, natural choice" {
		t.Errorf("option 3 = %+v", o[2])
	}
	if o[3].Key != "4" || o[3].Label != "Type something." {
		t.Errorf("option 4 = %+v", o[3])
	}
	if o[4].Key != "5" || o[4].Label != "Chat about this" {
		t.Errorf("option 5 = %+v", o[4])
	}
}

// TestParseMultiSelectQuestion covers a multi-select question whose options
// carry checked markers, and whose title comes from below the chips row rather
// than the "?" line above the box.
func TestParseMultiSelectQuestion(t *testing.T) {
	lines := []string{
		"Something earlier?",
		"────────────────────────────────────────",
		"←  ☐ Toppings  ✔ Submit  →",
		"Pick your toppings.",
		"❯ 1. [✔] Cheese",
		"     Aged",
		"  2. [ ] Mushroom",
		"     Earthy",
		"  3. [X] Olive",
		"────────────────────────────────────────",
		"  4. Chat about this",
		"Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
	}
	s := ParseScreen(lines)
	d := wantDialog(t, s, "question", "Pick your toppings.")
	if d == nil {
		return
	}
	wantTabs(t, d.Tabs, []tabWant{{"Toppings", false}, {"Submit", true}})
	if len(d.Options) != 4 {
		t.Fatalf("options = %+v, want 4", d.Options)
	}
	if d.Options[0].Label != "Cheese" || !d.Options[0].Checked || d.Options[0].Description != "Aged" {
		t.Errorf("option 1 = %+v", d.Options[0])
	}
	if d.Options[1].Label != "Mushroom" || d.Options[1].Checked || d.Options[1].Description != "Earthy" {
		t.Errorf("option 2 = %+v", d.Options[1])
	}
	if d.Options[2].Label != "Olive" || !d.Options[2].Checked {
		t.Errorf("option 3 = %+v", d.Options[2])
	}
	if d.Options[3].Label != "Chat about this" || d.Options[3].Checked {
		t.Errorf("option 4 = %+v", d.Options[3])
	}
}

// TestParseMultiSelectScreen covers the real multi-select question screen: the
// tab chips, checkbox options whose descriptions stay out of the labels, the
// keyless "Next" row under the last option and the keyless "Chat about this"
// after the rule.
func TestParseMultiSelectScreen(t *testing.T) {
	s := ParseScreen(loadScreen(t, "question-multi.txt"))
	d := wantDialog(t, s, "question", "Pick your toppings.")
	if d == nil {
		return
	}
	if !d.Multi {
		t.Errorf("dialog Multi = false, want true")
	}
	wantTabs(t, d.Tabs, []tabWant{
		{"Color", true},
		{"Toppings", true},
		{"Size", false},
		{"Submit", true},
	})
	wantOptions(t, d.Options, []optionWant{
		{"1", "Cheese (Recommended)", false},
		{"2", "Mushroom", true},
		{"3", "Olive", false},
		{"4", "Pepper", false},
		{"5", "Type something", false},
		{"", "Next", false},
		{"", "Chat about this", false},
	})
	if d.Options[0].Checked != true || d.Options[0].Description != "Classic, melty base topping" {
		t.Errorf("option 1 = %+v, want checked with its description", d.Options[0])
	}
	if d.Options[1].Checked != true || d.Options[1].Description != "Earthy, savory flavor" {
		t.Errorf("option 2 = %+v, want checked with its description", d.Options[1])
	}
	if d.Options[2].Checked || d.Options[2].Description != "Salty, briny bite" {
		t.Errorf("option 3 = %+v, want unchecked with its description", d.Options[2])
	}
	if d.Options[3].Checked || d.Options[3].Description != "Sweet or spicy crunch" {
		t.Errorf("option 4 = %+v, want unchecked with its description", d.Options[3])
	}
}

// TestParseQuestionActiveTabMarked covers the active chip of a tabbed
// AskUserQuestion dialog: the raw pane replay draws the first chip's row with a
// background highlight, so parsing the marked screen makes Color the only active
// tab. It checks the marks survive the replay arriving whole and in small writes.
func TestParseQuestionActiveTabMarked(t *testing.T) {
	raw := rawPane(t, "question.raw")
	cases := []struct {
		name  string
		chunk int
	}{
		{"whole", 0},
		{"chunked", 37},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMirror(100, 40)
			defer m.Close()
			writeRaw(t, m, raw, tc.chunk)
			lines, marked := m.Snapshot()
			s := ParseScreenMarked(lines, marked)
			d := wantDialog(t, s, "question", "Pick a color.")
			if d == nil {
				return
			}
			wantTabs(t, d.Tabs, []tabWant{
				{"Color", false},
				{"Toppings", false},
				{"Size", false},
				{"Submit", true},
			})
			wantActive(t, d.Tabs, "Color")
		})
	}
}

// TestParseScreenMarkedSecondChip covers a chips row whose second chip carries
// the background highlight: the marks make B (the second chip) the active tab
// and leave the others inactive.
func TestParseScreenMarkedSecondChip(t *testing.T) {
	raw := "←  ☒ A  \x1b[48;2;177;185;249m☐ B\x1b[49m  ☐ C  ✔ Submit  →\r\n" +
		"\r\n" +
		"Pick one?\r\n" +
		"\r\n" +
		"❯ 1. Yes\r\n" +
		"  2. No\r\n" +
		"\r\n" +
		"Enter to select · Esc to cancel"
	m := NewMirror(100, 12)
	defer m.Close()
	if _, err := m.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines, marked := m.Snapshot()
	d := wantDialog(t, ParseScreenMarked(lines, marked), "question", "Pick one?")
	if d == nil {
		return
	}
	wantTabs(t, d.Tabs, []tabWant{
		{"A", true},
		{"B", false},
		{"C", false},
		{"Submit", true},
	})
	wantActive(t, d.Tabs, "B")
}

// TestParseScreenMarkedNoMarks checks that without marks (nil, empty or a short
// row) no tab is active, keeping the plain-text behaviour.
func TestParseScreenMarkedNoMarks(t *testing.T) {
	lines := []string{
		"←  ☐ A  ☐ B  ✔ Submit  →",
		"",
		"Pick one?",
		"",
		"❯ 1. Yes",
		"  2. No",
		"",
		"Enter to select · Esc to cancel",
	}
	cases := map[string][][]bool{
		"nil":   nil,
		"empty": {},
		"short": {{true}},
	}
	for name, marked := range cases {
		t.Run(name, func(t *testing.T) {
			d := wantDialog(t, ParseScreenMarked(lines, marked), "question", "Pick one?")
			if d == nil {
				return
			}
			wantTabs(t, d.Tabs, []tabWant{{"A", false}, {"B", false}, {"Submit", true}})
			wantActive(t, d.Tabs)
		})
	}
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

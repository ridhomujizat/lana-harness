package workbench

import (
	"context"
	"strings"
	"testing"
)

// TestScanBellOSCTitle checks the core's own OSC parser: an OSC 0 or OSC 2
// payload is the window title, accumulated across chunks so a rune split in
// two arrives whole, and its terminator is not a bell (spec 55 A5).
func TestScanBellOSCTitle(t *testing.T) {
	s := &session{}
	s.scanBell([]byte("\x1b]0;\xe2\xa0"))
	s.scanBell([]byte("\x82 Writing tests\x07"))
	if s.oscTitle != "⠂ Writing tests" {
		t.Fatalf("title = %q, want %q", s.oscTitle, "⠂ Writing tests")
	}
	if s.bellNeeds {
		t.Fatal("an OSC terminator was treated as a bell")
	}

	// OSC 2 with the ESC \ terminator.
	s.scanBell([]byte("\x1b]2;✳ Fix the login bug\x1b\\"))
	if s.oscTitle != "✳ Fix the login bug" {
		t.Fatalf("title = %q, want %q", s.oscTitle, "✳ Fix the login bug")
	}

	// Another OSC command is not a title.
	s.scanBell([]byte("\x1b]7;file://host/tmp\x07"))
	if s.oscTitle != "✳ Fix the login bug" {
		t.Fatalf("title was overwritten by OSC 7: %q", s.oscTitle)
	}
}

// TestScanBellOSCTitleCap checks that a payload past the cap is dropped while
// the terminator is still found.
func TestScanBellOSCTitleCap(t *testing.T) {
	s := &session{}
	s.scanBell([]byte("\x1b]0;"))
	s.scanBell([]byte(strings.Repeat("x", oscTitleMax+100)))
	s.scanBell([]byte("\x07"))
	if got := len(s.oscTitle); got != oscTitleMax-2 {
		t.Fatalf("title length = %d, want %d", got, oscTitleMax-2)
	}
	if s.bellNeeds {
		t.Fatal("an OSC terminator was treated as a bell")
	}
}

// TestSessionOSCTitle checks end to end that a session reports the window title
// its program sets, decoded whole.
func TestSessionOSCTitle(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", `printf '\033]0;✳ Fix the login bug\a'; sleep 3`}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "title", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Title == "✳ Fix the login bug"
	})
	if g, err := c.Get(ctx, s.ID); err == nil && g.NeedsYou {
		t.Fatal("an OSC terminator was treated as a bell")
	}
}

// TestSessionOSCTitleSplitAcrossReads writes the braille spinner "⠂" (U+2802)
// as two chunks split after its second byte, so only accumulation across reads
// can decode it, and sets the title with an OSC 2 string ended by ESC \.
func TestSessionOSCTitleSplitAcrossReads(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	script := `printf '\033]2;\342\240'; sleep 0.2; printf '\202 Writing tests\033\\'; sleep 3`
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "title", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Title == "⠂ Writing tests"
	})
}

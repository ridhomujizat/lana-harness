package main

import "testing"

func TestVersionExit(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if code := run([]string{"-V"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestHelpExit(t *testing.T) {
	if code := run([]string{"--help"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestUnknownCommand(t *testing.T) {
	if code := run([]string{"tidak-ada"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
}

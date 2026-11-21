package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store"
)

// buildReplayDB creates a temp database with one session in a fresh workspace
// and appends the given tool/call events. It returns the database path.
func buildReplayDB(t *testing.T, drafts []core.EventDraft) string {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "lana.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	info, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.Append(ctx, info.ID, drafts); err != nil {
		t.Fatalf("append events: %v", err)
	}
	return dbPath
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func toolCall(t *testing.T, name string, input json.RawMessage) core.EventDraft {
	t.Helper()
	return core.EventDraft{
		Kind:    core.KindToolCall,
		TurnID:  "turn-1",
		Payload: core.ToolCallPayload{V: core.PayloadVersion, CallID: name + "-1", Name: name, Input: input},
	}
}

func TestHardlineReplay(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".lana", "config.toml")
	dbPath := buildReplayDB(t, []core.EventDraft{
		toolCall(t, "bash", mustJSON(t, map[string]string{"command": "rm -rf ~"})),
		toolCall(t, "bash", mustJSON(t, map[string]string{"command": "ls"})),
		toolCall(t, "write_file", mustJSON(t, map[string]string{"path": cfgPath})),
	})

	var stdout, stderr bytes.Buffer
	if code := hardlineReplay([]string{"--db", dbPath, "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("hardlineReplay exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6:\n%s", len(lines), out)
	}
	for i, suffix := range []string{
		"  lana  HL-B-001  block  bash  rm -rf ~",
		"  lana  HL-B-004  block  write_file  " + cfgPath,
	} {
		if !strings.HasSuffix(lines[i], suffix) {
			t.Fatalf("line %d = %q, want suffix %q", i, lines[i], suffix)
		}
		if _, err := time.Parse(time.RFC3339, strings.SplitN(lines[i], "  ", 2)[0]); err != nil {
			t.Fatalf("line %d has no RFC3339 timestamp: %q", i, lines[i])
		}
	}
	if lines[2] != "" {
		t.Fatalf("line 2 = %q, want a blank separator", lines[2])
	}
	wantTail := []string{"HL-B-001  1", "HL-B-004  1", "3 calls scanned"}
	for i, want := range wantTail {
		if lines[3+i] != want {
			t.Fatalf("line %d = %q, want %q", 3+i, lines[3+i], want)
		}
	}

	// A second run must return the same report: the connection is read-only.
	var again bytes.Buffer
	if code := hardlineReplay([]string{"--db", dbPath, "--home", home}, &again, &stderr); code != 0 {
		t.Fatalf("second hardlineReplay exit = %d", code)
	}
	if again.String() != out {
		t.Fatalf("second run differs:\n%s\n---\n%s", out, again.String())
	}
}

func TestHardlineReplaySince(t *testing.T) {
	home := t.TempDir()
	dbPath := buildReplayDB(t, []core.EventDraft{
		toolCall(t, "bash", mustJSON(t, map[string]string{"command": "rm -rf ~"})),
	})
	var stdout, stderr bytes.Buffer
	if code := hardlineReplay([]string{"--db", dbPath, "--home", home, "--since", "2999-01-01"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hardlineReplay exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "0 calls scanned\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestHardlineReplayTruncatesArgument(t *testing.T) {
	home := t.TempDir()
	command := "rm -rf ~ " + strings.Repeat("x", 200)
	dbPath := buildReplayDB(t, []core.EventDraft{
		toolCall(t, "bash", mustJSON(t, map[string]string{"command": command})),
	})
	var stdout, stderr bytes.Buffer
	if code := hardlineReplay([]string{"--db", dbPath, "--home", home, "--limit-arg", "10"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hardlineReplay exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rm -rf ~ x…") {
		t.Fatalf("argument was not truncated at 10 runes:\n%s", stdout.String())
	}
}

func TestHardlineReplayMissingDB(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	if code := hardlineReplay([]string{"--db", dbPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("hardlineReplay exit = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("missing DB produced no stderr message")
	}
	if stdout.Len() != 0 {
		t.Fatalf("missing DB wrote to stdout: %q", stdout.String())
	}
}

func TestParseReplaySince(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "", want: 0},
		{in: "2026-11-21", want: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{in: "2026-11-21T10:00:00Z", want: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC).UnixMilli()},
		{in: "2026-11-21T10:00:00+02:00", want: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC).UnixMilli()},
		{in: "yesterday", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseReplaySince(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseReplaySince(%q) = %d, want an error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseReplaySince(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseReplaySince(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestShortenArg(t *testing.T) {
	tests := []struct {
		in    string
		limit int
		want  string
	}{
		{in: "abc", limit: 5, want: "abc"},
		{in: "abcdef", limit: 3, want: "abc…"},
		{in: "héllo", limit: 2, want: "hé…"},
		{in: "abc", limit: 0, want: "abc"},
	}
	for _, tt := range tests {
		if got := shortenArg(tt.in, tt.limit); got != tt.want {
			t.Fatalf("shortenArg(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
		}
	}
}

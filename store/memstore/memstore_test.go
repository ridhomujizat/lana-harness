package memstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func newSession(t *testing.T) (*Store, core.SessionInfo) {
	t.Helper()
	s := New()
	info, err := s.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return s, info
}

// TestMemstoreScanEventsCoversCoreEventScanner mirrors the SQLite scan: kind
// filtering, the sinceMS bound, the agent, and the reduced tool/result payload
// (design.md §2).
func TestMemstoreScanEventsCoversCoreEventScanner(t *testing.T) {
	s := New()
	first, err := s.Create(context.Background(), core.NewSession{Agent: "alice", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(context.Background(), core.NewSession{Agent: "bob", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	longOutput := strings.Repeat("x", 500) + `"quoted"`
	if _, err := s.Append(context.Background(), first.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindToolCall, TurnID: "t1", Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file"}},
		{Kind: core.KindToolResult, TurnID: "t1", Payload: core.ToolResultPayload{V: 1, CallID: "c1", Status: core.ToolStatusError, Output: longOutput}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(context.Background(), second.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}},
	}); err != nil {
		t.Fatal(err)
	}

	kinds := []string{core.KindTurnStart, core.KindToolCall, core.KindToolResult}
	var got []core.ScannedEvent
	for event, err := range s.ScanEvents(context.Background(), kinds, 0) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if len(got) != 4 {
		t.Fatalf("scanned %d events, want 4: %+v", len(got), got)
	}
	agents := map[string]string{}
	var result core.ScannedEvent
	for _, event := range got {
		agents[event.SessionID] = event.Agent
		if event.Kind == core.KindToolResult {
			result = event
		}
	}
	if agents[first.ID] != "alice" || agents[second.ID] != "bob" {
		t.Fatalf("agents = %+v", agents)
	}

	var reduced struct {
		CallID string `json:"call_id"`
		Status string `json:"status"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result.Payload, &reduced); err != nil {
		t.Fatalf("reduced payload %q: %v", result.Payload, err)
	}
	if reduced.CallID != "c1" || reduced.Status != "error" {
		t.Fatalf("reduced = %+v", reduced)
	}
	if len([]rune(reduced.Output)) > 300 {
		t.Fatalf("reduced output = %d runes, want <= 300", len([]rune(reduced.Output)))
	}

	count := 0
	for _, err := range s.ScanEvents(context.Background(), nil, 0) {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 0 {
		t.Fatalf("empty kinds scanned %d events", count)
	}
}

func TestMemstoreSeq(t *testing.T) {
	s, info := newSession(t)
	events, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindUserMessage, TurnID: "t1", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("seq=%v", events)
	}
	var got []int64
	for event, err := range s.Events(context.Background(), info.ID, 1) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, event.Seq)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("events=%v", got)
	}
	var created core.SessionCreated
	first, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.LastSeq != 3 {
		t.Fatalf("lastSeq=%d", first.LastSeq)
	}
	chain, err := s.ChainEvents(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(chain[0].Payload, &created); err != nil {
		t.Fatal(err)
	}
	if created.Channel != "library" || created.Workspace == "" {
		t.Fatalf("created=%+v", created)
	}
}

func TestMemstoreQuestionWaitingProjection(t *testing.T) {
	s, info := newSession(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindQuestionAsked, TurnID: "t", Payload: core.QuestionAsked{V: 1, RequestID: "q1", CallID: "c1"}},
	}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Info(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_input" {
		t.Fatalf("status after ask=%q", waiting.Status)
	}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}},
	}); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("turn start while waiting err=%v", err)
	}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindQuestionAnswered, TurnID: "t", Payload: core.QuestionAnswered{V: 1, RequestID: "q1", CallID: "c1", Outcome: core.QuestionStatusAnswered, Responder: "web"}},
	}); err != nil {
		t.Fatal(err)
	}
	running, err := s.Info(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != "running" {
		t.Fatalf("status after answer=%q", running.Status)
	}
}

func TestMemstoreForkUnsupported(t *testing.T) {
	s, info := newSession(t)
	if _, err := s.Fork(context.Background(), info.ID, 1); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("fork err=%v", err)
	}
	release, err := s.TryLease(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestMemstoreNotDurable(t *testing.T) {
	s, info := newSession(t)
	if _, err := s.Info(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	other := New()
	if _, err := other.Info(context.Background(), info.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("old session found in new store: %v", err)
	}
}

// REQ-ATT-004: a tool/result event keeps its images through Append and derive.
func TestMemstoreToolResultImages(t *testing.T) {
	s, info := newSession(t)
	ctx := context.Background()
	image := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("png-bytes"), Path: "attachments/a.png"}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
		}}},
		{Kind: core.KindToolResult, TurnID: "t", Payload: core.ToolResultPayload{V: 1, CallID: "c1", Status: core.ToolStatusSuccess, Output: "Image a.png (2x2).", Images: []core.ContentBlock{image}}},
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := s.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
	block := messages[1].Content[0]
	if block.Kind != core.BlockToolResult || len(block.Images) != 1 || !reflect.DeepEqual(block.Images[0], image) {
		t.Fatalf("block=%+v", block)
	}
}

// REQ-ATT-002: a user/message image block keeps its media type, data, and path.
func TestMemstoreUserMessageImage(t *testing.T) {
	s, info := newSession(t)
	ctx := context.Background()
	image := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/jpeg", Data: []byte("jpeg-bytes"), Path: "attachments/2026-11-01/a.jpg"}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "look"},
			image,
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := s.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 2 {
		t.Fatalf("messages=%+v", messages)
	}
	got := messages[0].Content[1]
	if got.Kind != core.BlockImage || got.MediaType != image.MediaType || !bytes.Equal(got.Data, image.Data) || got.Path != image.Path {
		t.Fatalf("image block=%+v", got)
	}
}

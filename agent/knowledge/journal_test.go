package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// textStore wraps the in-memory store with the conversation-text reader the
// journal pass needs and a settable title.
type textStore struct {
	*memstore.Store

	mu    sync.Mutex
	title string
	rows  []core.TextRow
}

func newTextStore() *textStore { return &textStore{Store: memstore.New()} }

func (s *textStore) setTitle(title string) {
	s.mu.Lock()
	s.title = title
	s.mu.Unlock()
}

func (s *textStore) setRows(rows []core.TextRow) {
	s.mu.Lock()
	s.rows = rows
	s.mu.Unlock()
}

// TextHeads returns one head per session with its newest seq.
func (s *textStore) TextHeads(ctx context.Context) ([]core.TextHead, error) {
	s.mu.Lock()
	rows := append([]core.TextRow(nil), s.rows...)
	s.mu.Unlock()
	max := map[string]int64{}
	for _, row := range rows {
		if row.Seq > max[row.SessionID] {
			max[row.SessionID] = row.Seq
		}
	}
	heads := make([]core.TextHead, 0, len(max))
	for sessionID, seq := range max {
		info, err := s.Store.Info(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		heads = append(heads, core.TextHead{Session: info, MaxSeq: seq})
	}
	return heads, nil
}

func (s *textStore) TextRows(ctx context.Context, sessionID string, afterSeq int64, maxRunes int) ([]core.TextRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []core.TextRow
	total := 0
	for _, row := range s.rows {
		if row.SessionID != sessionID || row.Seq <= afterSeq {
			continue
		}
		n := utf8.RuneCountInString(row.Text)
		if len(out) > 0 && (maxRunes <= 0 || total+n > maxRunes) {
			break
		}
		out = append(out, row)
		total += n
	}
	return out, nil
}

// Info returns the session with the test title, when one was set.
func (s *textStore) Info(ctx context.Context, id string) (core.SessionInfo, error) {
	info, err := s.Store.Info(ctx, id)
	if err != nil {
		return info, err
	}
	s.mu.Lock()
	if s.title != "" {
		info.Title = s.title
	}
	s.mu.Unlock()
	return info, nil
}

// journalEnv is one knowledge service over a textStore and a fresh home.
type journalEnv struct {
	store    *textStore
	svc      *Service
	home     string
	info     core.SessionInfo
	provider *fakeProvider
}

func newJournalEnv(t *testing.T, provider *fakeProvider, cfg core.KnowledgeConfig) *journalEnv {
	t.Helper()
	store := newTextStore()
	home := t.TempDir()
	created, err := store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "telegram", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	svc := NewService(base, home, cfg, store, fakeLLM{provider: provider})
	svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		if name != "assistant" {
			return core.AgentConfig{}, false
		}
		return core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}}, true
	}
	t.Cleanup(func() {
		cancel()
		svc.drain()
		_ = svc.Close()
	})
	return &journalEnv{store: store, svc: svc, home: home, info: created, provider: provider}
}

// journalMark reads the session's journal mark.
func (e *journalEnv) journalMark(t *testing.T) int64 {
	t.Helper()
	_, idx, err := e.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	mark, err := idx.JournalMark(e.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	return mark
}

// journalText reads the journal file of one day.
func (e *journalEnv) journalText(t *testing.T, day string) string {
	t.Helper()
	vault, _, err := e.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(vault.Root(), "journal", day+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// waitJournalMark waits until the journal mark of the session moves.
func (e *journalEnv) waitJournalMark(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, idx, err := e.svc.open(); err == nil {
			if mark, err := idx.JournalMark(e.info.ID); err == nil && mark > 0 {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("journal mark did not move")
}

func twoTurnRows(info core.SessionInfo, at time.Time) []core.TextRow {
	return []core.TextRow{
		{SessionID: info.ID, Seq: 3, Role: "user", CreatedAt: at.UnixMilli(), Text: "Deploy staging on Friday"},
		{SessionID: info.ID, Seq: 4, Role: "assistant", CreatedAt: at.UnixMilli(), Text: "I scheduled the deploy"},
	}
}

// AC-KNU-001 (REQ-KNU-001, -002, -004): one request for a two-turn session,
// notes land in the day file of the newest row with the header, the mark moves,
// and a second pass without new text makes no request.
func TestJournalSessionAppendsNotes(t *testing.T) {
	provider := &fakeProvider{text: "```json\n{\"notes\": [\"note one\", \"note two\"]}\n```"}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	at := time.Now()
	env.store.setRows(twoTurnRows(env.info, at))

	info := env.info
	info.Title = "Deploy staging"
	env.svc.journalSession(context.Background(), info)

	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
	req := provider.recorded()[0]
	if req.System != journalSystem || req.MaxTokens != journalMaxTokens || len(req.Tools) != 0 {
		t.Fatalf("request shape = system %q max %d tools %d", req.System, req.MaxTokens, len(req.Tools))
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != core.RoleUser {
		t.Fatalf("messages = %+v", req.Messages)
	}
	excerpt := req.Messages[0].Content[0].Text
	if !strings.Contains(excerpt, "owner: Deploy staging on Friday") || !strings.Contains(excerpt, "agent: I scheduled the deploy") {
		t.Fatalf("excerpt = %q", excerpt)
	}

	day := at.Format("2006-01-02")
	block := env.journalText(t, day)
	wantHeader := fmt.Sprintf("## %s · assistant · telegram · session %s %q", at.Format("15:04"), env.info.ID, "Deploy staging")
	if !strings.Contains(block, wantHeader) {
		t.Fatalf("journal block = %q, want header %q", block, wantHeader)
	}
	if !strings.Contains(block, "- note one") || !strings.Contains(block, "- note two") {
		t.Fatalf("journal notes missing: %q", block)
	}
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("journal mark = %d, want 4", mark)
	}

	// A second pass with no new text makes no request and keeps the mark.
	env.svc.journalSession(context.Background(), info)
	if provider.count() != 1 {
		t.Fatalf("provider calls after second pass = %d, want 1", provider.count())
	}
}

// REQ-KNU-002: text with no owner message moves the mark without a model call.
func TestJournalSessionNoOwnerMovesMark(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["x"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	at := time.Now()
	env.store.setRows([]core.TextRow{
		{SessionID: env.info.ID, Seq: 5, Role: "assistant", CreatedAt: at.UnixMilli(), Text: "only agent text"},
	})

	env.svc.journalSession(context.Background(), env.info)

	if provider.count() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.count())
	}
	if mark := env.journalMark(t); mark != 5 {
		t.Fatalf("journal mark = %d, want 5", mark)
	}
}

// AC-KNU-001: a provider error keeps the mark unchanged.
func TestJournalSessionProviderErrorKeepsMark(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["note one"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	at := time.Now()
	env.store.setRows(twoTurnRows(env.info, at))

	info := env.info
	info.Title = "Deploy staging"
	env.svc.journalSession(context.Background(), info)
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("mark after first pass = %d, want 4", mark)
	}

	provider.err = errors.New("boom")
	env.store.setRows(append(twoTurnRows(env.info, at), core.TextRow{
		SessionID: env.info.ID, Seq: 5, Role: "user", CreatedAt: at.UnixMilli(), Text: "another owner line",
	}))
	env.svc.journalSession(context.Background(), info)

	if provider.count() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.count())
	}
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("mark after failed pass = %d, want 4", mark)
	}
}

// AC-KNU-002 (REQ-KNU-003): a compaction observer call (JournalSoon) triggers
// exactly one journal pass for that session.
func TestJournalSoonTriggersOnePass(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["a note"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	env.store.setTitle("Deploy staging")
	env.store.setRows(twoTurnRows(env.info, time.Now()))

	env.svc.JournalSoon(env.info.ID, env.info.Agent)
	env.waitJournalMark(t)

	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
}

// AC-KNU-002: a session whose agent has no knowledge tool is skipped.
func TestJournalSessionSkipsAgentWithoutKnowledgeTool(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["a note"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	env.svc.LookupAgent = func(string) (core.AgentConfig, bool) {
		return core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"shell"}}, true
	}
	env.store.setRows(twoTurnRows(env.info, time.Now()))

	env.svc.journalSession(context.Background(), env.info)

	if provider.count() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.count())
	}
	if mark := env.journalMark(t); mark != 0 {
		t.Fatalf("journal mark = %d, want 0", mark)
	}
}

// AC-KNU-002: knowledge.journal = false skips the pass.
func TestJournalSessionDisabledByConfig(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["a note"]}`}
	off := false
	env := newJournalEnv(t, provider, core.KnowledgeConfig{Journal: &off})
	env.store.setRows(twoTurnRows(env.info, time.Now()))

	env.svc.journalSession(context.Background(), env.info)

	if provider.count() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.count())
	}
}

// REQ-KNU-001: JournalAll journals every session with unread text.
func TestJournalAllJournalsUnreadSessions(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["a note"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	env.store.setRows(twoTurnRows(env.info, time.Now()))

	if err := env.svc.JournalAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("journal mark = %d, want 4", mark)
	}
	// A second run has nothing unread: no new request.
	if err := env.svc.JournalAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("provider calls after second run = %d, want 1", provider.count())
	}
}

// design §4: notes are truncated to 500 runes, secret-like notes are dropped,
// and at most 12 are kept.
func TestJournalNotesFiltering(t *testing.T) {
	long := strings.Repeat("y", 600)
	raw := []string{"keep one", "password: hunter22xyz", long}
	for i := 0; i < 20; i++ {
		raw = append(raw, fmt.Sprintf("note %d", i))
	}
	got := journalNotes(raw)
	if len(got) != journalMaxNotes {
		t.Fatalf("kept %d notes, want %d", len(got), journalMaxNotes)
	}
	if got[0] != "keep one" {
		t.Fatalf("got[0] = %q", got[0])
	}
	if got[1] != strings.Repeat("y", journalNoteRunes) {
		t.Fatalf("long note was not truncated to %d runes: %d", journalNoteRunes, utf8.RuneCountInString(got[1]))
	}
	if got[2] != "note 0" || got[len(got)-1] != "note 9" {
		t.Fatalf("note order = %q", got)
	}
	for _, note := range got {
		if strings.Contains(note, "hunter22xyz") {
			t.Fatal("a secret-like note was kept")
		}
	}
}

// design §4: parseJournalNotes strips a fence and rejects a non-JSON reply.
func TestParseJournalNotes(t *testing.T) {
	notes, err := parseJournalNotes("```json\n{\"notes\": [\"a\"]}\n```")
	if err != nil || len(notes) != 1 || notes[0] != "a" {
		t.Fatalf("notes = %v err = %v", notes, err)
	}
	for _, reply := range []string{"not json", `["a"]`, `{"notes": }`} {
		if _, err := parseJournalNotes(reply); err == nil {
			t.Fatalf("parseJournalNotes(%q) accepted an invalid reply", reply)
		}
	}
}

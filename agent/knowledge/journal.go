package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/internal/secretlike"
)

// Journal-pass bounds (design §4).
const (
	journalMaxRunes    = 40000
	journalMaxNotes    = 12
	journalNoteRunes   = 500
	journalMaxTokens   = 2048
	journalCallTimeout = 120 * time.Second
)

// journalSystem is the system prompt of one journal call (design §4).
const journalSystem = `You keep a journal of what the owner and the agent did and learned. From the conversation excerpt, write short notes worth keeping for months: facts learned about people, projects, places, and topics; decisions and their reasons; plans and deadlines; documents or links the owner shared; outcomes of work. Skip greetings, chit-chat, tool mechanics, and anything temporary. Keep names, numbers, and dates. Write in the language of the conversation. Never include passwords, API keys, or tokens.
Reply with only JSON: {"notes": ["...", "..."]}, or {"notes": []} when nothing is worth keeping.
Return valid JSON: escape newlines inside strings as \n.`

// journalReply is the JSON reply of one journal call (design §4).
type journalReply struct {
	Notes []string `json:"notes"`
}

// journalFlight is the per-session in-flight set of journal passes. A second
// pass for a session already running is dropped; the next trigger catches up
// (design §4).
var journalFlight = struct {
	mu sync.Mutex
	m  map[string]bool
}{m: map[string]bool{}}

// journalSession appends the notes of one session to the journal of the day of
// its newest row. REQ-KNU-001–004: it reads at most journalMaxRunes of unread
// conversation text, makes one model call, and moves the journal mark.
func (s *Service) journalSession(ctx context.Context, info core.SessionInfo) {
	agentCfg, ok := s.lookupAgent(info.Agent)
	if !ok || !hasKnowledgeTool(agentCfg) {
		return
	}
	if s.cfg.Journal != nil && !*s.cfg.Journal {
		return
	}
	reader, ok := s.store.(core.SessionTextReader)
	if !ok || reader == nil {
		return
	}
	if !journalBegin(info.ID) {
		return
	}
	defer journalEnd(info.ID)

	vault, idx, err := s.open()
	if err != nil {
		slog.Warn("knowledge journal: open failed", "session_id", info.ID, "error", err)
		return
	}
	mark, err := idx.JournalMark(info.ID)
	if err != nil {
		slog.Warn("knowledge journal: reading mark failed", "session_id", info.ID, "error", err)
		return
	}
	rows, err := reader.TextRows(ctx, info.ID, mark, journalMaxRunes)
	if err != nil {
		slog.Warn("knowledge journal: reading text failed", "session_id", info.ID, "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	last := rows[len(rows)-1]
	// REQ-KNU-002: text with no owner message moves the mark without a model
	// call.
	if !hasOwnerRow(rows) {
		s.setJournalMark(idx, info.ID, last.Seq)
		return
	}

	reply, err := s.journalRequest(ctx, agentCfg, rows)
	if err != nil {
		slog.Warn("knowledge journal: request failed", "session_id", info.ID, "error", err)
		return
	}
	notes, err := parseJournalNotes(reply)
	if err != nil {
		slog.Warn("knowledge journal: parsing reply failed", "session_id", info.ID, "error", err)
		return
	}
	notes = journalNotes(notes)
	if len(notes) > 0 {
		at := time.UnixMilli(last.CreatedAt)
		block := journalBlock(info, at, notes)
		rel, err := vault.AppendJournal(at.Format("2006-01-02"), block)
		if err != nil {
			slog.Warn("knowledge journal: appending failed", "session_id", info.ID, "error", err)
			return
		}
		if err := idx.SyncFile(filepath.Join(vault.Root(), filepath.FromSlash(rel))); err != nil {
			slog.Warn("knowledge journal: re-index failed", "path", rel, "error", err)
		}
	}
	s.setJournalMark(idx, info.ID, last.Seq)
}

// journalRequest makes the one model call of a journal pass and returns its
// raw reply (design §4).
func (s *Service) journalRequest(ctx context.Context, agentCfg core.AgentConfig, rows []core.TextRow) (string, error) {
	if s.llm == nil {
		return "", errors.New("no llm registry")
	}
	provider, ok := s.llm.Provider(agentCfg.Provider)
	if !ok {
		return "", fmt.Errorf("unknown provider %q", agentCfg.Provider)
	}
	callCtx, cancel := context.WithTimeout(ctx, journalCallTimeout)
	defer cancel()
	stream, err := provider.Stream(callCtx, core.ChatRequest{
		Model:  agentCfg.Model,
		System: journalSystem,
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: journalExcerpt(rows)},
		}}},
		MaxTokens: journalMaxTokens,
	})
	if err != nil {
		return "", err
	}
	text, _, err := collectReply(stream)
	return text, err
}

// JournalSoon runs one journal pass for a session on the service base context,
// in a new goroutine (REQ-KNU-003a, design §4). It is the compaction observer.
func (s *Service) JournalSoon(sessionID, agent string) {
	if s.store == nil || sessionID == "" {
		return
	}
	go func() {
		info, err := s.store.Info(s.base, sessionID)
		if err != nil {
			slog.Warn("knowledge journal: session lookup failed", "session_id", sessionID, "error", err)
			return
		}
		if agent != "" {
			info.Agent = agent
		}
		s.journalSession(s.base, info)
	}()
}

// JournalAll journals every eligible session with unread text, one at a time in
// the calling goroutine, honoring ctx cancellation. A failing session does not
// stop the rest; the errors are joined (REQ-KNU-003b, design §5).
func (s *Service) JournalAll(ctx context.Context) error {
	reader, ok := s.store.(core.SessionTextReader)
	if !ok || reader == nil {
		return nil
	}
	heads, err := reader.TextHeads(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, head := range heads {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		_, idx, err := s.open()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		mark, err := idx.JournalMark(head.Session.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if head.MaxSeq <= mark {
			continue
		}
		s.journalSession(ctx, head.Session)
	}
	return errors.Join(errs...)
}

// journalExcerpt renders the rows as "seq <n> <time> <owner|agent>: <text>"
// lines (design §4).
func journalExcerpt(rows []core.TextRow) string {
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		who := "agent"
		if row.Role == "user" {
			who = "owner"
		}
		fmt.Fprintf(&b, "seq %d %s %s: %s", row.Seq, time.UnixMilli(row.CreatedAt).Format("15:04"), who, row.Text)
	}
	return b.String()
}

// journalBlock renders the journal entry of one pass (design §4).
func journalBlock(info core.SessionInfo, at time.Time, notes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s · %s · %s · session %s %q\n", at.Format("15:04"), info.Agent, info.Channel, info.ID, info.Title)
	for _, note := range notes {
		b.WriteString("- ")
		b.WriteString(note)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// hasOwnerRow reports whether any row is an owner (user) message
// (REQ-KNU-002).
func hasOwnerRow(rows []core.TextRow) bool {
	for _, row := range rows {
		if row.Role == "user" {
			return true
		}
	}
	return false
}

// parseJournalNotes strips a code fence and parses the reply as
// {"notes": [...]} (design §4).
func parseJournalNotes(text string) ([]string, error) {
	trimmed := stripFence(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, errors.New("journal reply is not a JSON object")
	}
	var reply journalReply
	if err := decodeLLMJSON(trimmed, &reply); err != nil {
		return nil, err
	}
	return reply.Notes, nil
}

// journalNotes truncates each note to journalNoteRunes, drops empty and
// secret-like notes, and keeps at most journalMaxNotes (design §4).
func journalNotes(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, note := range raw {
		note = strings.TrimSpace(firstRunes(note, journalNoteRunes))
		if note == "" || secretlike.Match(note) {
			continue
		}
		out = append(out, note)
		if len(out) >= journalMaxNotes {
			break
		}
	}
	return out
}

// setJournalMark records the newest seq already journaled, logging but not
// failing on error.
func (s *Service) setJournalMark(idx *kv.Index, sessionID string, seq int64) {
	if err := idx.SetJournalMark(sessionID, seq); err != nil {
		slog.Warn("knowledge journal: saving mark failed", "session_id", sessionID, "error", err)
	}
}

// journalBegin claims the per-session in-flight slot, reporting false when a
// pass for the session is already running (design §4).
func journalBegin(sessionID string) bool {
	journalFlight.mu.Lock()
	defer journalFlight.mu.Unlock()
	if journalFlight.m[sessionID] {
		return false
	}
	journalFlight.m[sessionID] = true
	return true
}

// journalEnd releases the per-session in-flight slot.
func journalEnd(sessionID string) {
	journalFlight.mu.Lock()
	delete(journalFlight.m, sessionID)
	journalFlight.mu.Unlock()
}

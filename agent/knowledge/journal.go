package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
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
	journalMaxTokens   = 8192
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

// journalOutcome is the result of one journal pass (REQ-KBG-010).
type journalOutcome int

const (
	// journalWritten means notes were appended to the vault; the mark moves.
	journalWritten journalOutcome = iota
	// journalEmpty means the model returned no notes; the mark moves.
	journalEmpty
	// journalSkipped means the pass did not run; the mark is unchanged unless
	// the session had no owner text, which moves it.
	journalSkipped
	// journalFailed means the request or parse failed; the mark stays.
	journalFailed
	// journalPreempted means the pass context was cancelled; the mark stays and
	// the pass is eligible again (REQ-KBG-031).
	journalPreempted
)

// journalFailMemory is the service's in-memory record of the most recent failed
// journal pass of one session (REQ-KBG-012).
type journalFailMemory struct {
	err      string
	at       time.Time
	failures int
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
// conversation text, makes one model call, and moves the journal mark. It
// reports the pass outcome (REQ-KBG-010) and, for a failed pass, the error text.
func (s *Service) journalSession(ctx context.Context, info core.SessionInfo) (outcome journalOutcome, errText string) {
	outcome = journalSkipped
	defer func() { s.recordJournalOutcome(info.ID, outcome, errText) }()

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
		return s.logJournalFailure(ctx, info.ID, "knowledge journal: open failed", err)
	}
	mark, err := idx.JournalMark(info.ID)
	if err != nil {
		return s.logJournalFailure(ctx, info.ID, "knowledge journal: reading mark failed", err)
	}
	rows, err := reader.TextRows(ctx, info.ID, mark, journalMaxRunes)
	if err != nil {
		return s.logJournalFailure(ctx, info.ID, "knowledge journal: reading text failed", err)
	}
	if len(rows) == 0 {
		return journalSkipped, ""
	}
	last := rows[len(rows)-1]
	// REQ-KNU-002: text with no owner message moves the mark without a model
	// call.
	if !hasOwnerRow(rows) {
		s.setJournalMark(idx, info.ID, last.Seq)
		return journalSkipped, ""
	}

	reply, err := s.journalRequest(ctx, info, agentCfg, rows)
	if err != nil {
		return s.logJournalFailure(ctx, info.ID, "knowledge journal: request failed", err)
	}
	notes, err := parseJournalNotes(reply)
	if err != nil {
		return s.logJournalFailure(ctx, info.ID, "knowledge journal: parsing reply failed", err)
	}
	notes = journalNotes(notes)
	wrote := false
	if len(notes) > 0 {
		at := time.UnixMilli(last.CreatedAt)
		block := journalBlock(info, at, notes)
		// REQ-DKN-030: a session whose agent has a domain journals into that
		// domain; every other session (for example a team) keeps the shared
		// journal.
		scope := kv.ScopeShared
		if domainAgent(s.Config, info.Agent) {
			scope = info.Agent
		}
		rel, err := vault.AppendJournalIn(scope, at.Format("2006-01-02"), block)
		if err != nil {
			return s.logJournalFailure(ctx, info.ID, "knowledge journal: appending failed", err)
		}
		if err := idx.SyncFile(filepath.Join(vault.Root(), filepath.FromSlash(rel))); err != nil {
			slog.Warn("knowledge journal: re-index failed", "path", rel, "error", err)
		}
		wrote = true
	}
	s.setJournalMark(idx, info.ID, last.Seq)
	if wrote {
		return journalWritten, ""
	}
	return journalEmpty, ""
}

// logJournalFailure maps one model or storage error to an outcome and logs it:
// a preempted pass logs at debug only (REQ-KBG-031), any other failure at warn.
func (s *Service) logJournalFailure(ctx context.Context, sessionID, msg string, err error) (journalOutcome, string) {
	outcome, errText := journalFailureOutcome(ctx, err)
	if outcome == journalPreempted {
		slog.Debug("knowledge journal: preempted", "session_id", sessionID)
	} else {
		slog.Warn(msg, "session_id", sessionID, "error", err)
	}
	return outcome, errText
}

// journalFailureOutcome maps a model or storage error to journalPreempted when
// the error is a cancellation and the pass context itself is done, else to
// journalFailed with the error text (REQ-KBG-010, REQ-KBG-031).
func journalFailureOutcome(ctx context.Context, err error) (journalOutcome, string) {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return journalPreempted, ""
	}
	return journalFailed, err.Error()
}

// recordJournalOutcome updates the in-memory failure memory of one session
// (REQ-KBG-012): a failure sets the error, stamps the time, and counts one more
// consecutive failure; a written or empty outcome clears it. A preempted or
// skipped outcome leaves it unchanged.
func (s *Service) recordJournalOutcome(sessionID string, outcome journalOutcome, errText string) {
	s.journalFailMu.Lock()
	defer s.journalFailMu.Unlock()
	switch outcome {
	case journalFailed:
		if s.journalFail == nil {
			s.journalFail = map[string]journalFailMemory{}
		}
		prev := s.journalFail[sessionID]
		s.journalFail[sessionID] = journalFailMemory{err: errText, at: s.now(), failures: prev.failures + 1}
	case journalWritten, journalEmpty:
		delete(s.journalFail, sessionID)
	}
}

// journalFailureStatus returns the sessions whose most recent pass failed,
// newest first (REQ-KBG-012). The result is never nil.
func (s *Service) journalFailureStatus() []core.KnowledgeJournalFailure {
	s.journalFailMu.Lock()
	defer s.journalFailMu.Unlock()
	out := make([]core.KnowledgeJournalFailure, 0, len(s.journalFail))
	for id, f := range s.journalFail {
		out = append(out, core.KnowledgeJournalFailure{
			SessionID: id, Error: f.err, At: f.at.UnixMilli(), Failures: f.failures,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

// journalRequest makes the one model call of a journal pass and returns its
// raw reply (design §4).
func (s *Service) journalRequest(ctx context.Context, info core.SessionInfo, agentCfg core.AgentConfig, rows []core.TextRow) (string, error) {
	call, err := s.agentCall(info.Agent, agentCfg)
	if err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, journalCallTimeout)
	defer cancel()
	stream, err := s.stream(callCtx, call, info.ID, core.ChatRequest{
		System: journalSystem,
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: journalExcerpt(rows)},
		}}},
		MaxTokens:       journalMaxTokens,
		ReasoningEffort: "off",
	})
	if err != nil {
		return "", err
	}
	text, _, err := collectReply(stream)
	return text, err
}

// JournalSoon runs one journal pass for a session on the service base context,
// in a new goroutine (REQ-KNU-003a, design §4). It is the compaction observer.
// REQ-KBG-023: while a turn runs it queues the session instead of calling the
// model; the background tick picks it up, since the session stays eligible by
// its mark.
func (s *Service) JournalSoon(sessionID, agent string) {
	if s.store == nil || sessionID == "" {
		return
	}
	if s.act != nil && !s.act.quiet(s.now()) {
		return
	}
	passCtx, release := s.act.preemptible(s.base)
	go func() {
		defer release()
		info, err := s.store.Info(s.base, sessionID)
		if err != nil {
			slog.Warn("knowledge journal: session lookup failed", "session_id", sessionID, "error", err)
			return
		}
		if agent != "" {
			info.Agent = agent
		}
		s.journalSession(passCtx, info)
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

// parseJournalNotes parses the reply as {"notes": [...]}. It tolerates a code
// fence and prose around the JSON value; an empty reply fails (REQ-KBG-004,
// design §4).
func parseJournalNotes(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty reply")
	}
	var reply journalReply
	if err := decodeLLMJSON(text, &reply); err != nil {
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

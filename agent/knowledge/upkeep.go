package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Upkeep loop tuning (design §5).
const (
	upkeepTickInterval   = time.Minute
	inboxScanInterval    = 5 * time.Minute
	defaultConsolidateAt = "03:00"
)

// Service implements the knowledge upkeep surface (design §5).
var _ core.KnowledgeAdmin = (*Service)(nil)

// Upkeep state keys stored in the index upkeep table (design §2).
const (
	upkeepKeyConsolidationAt    = "last_consolidation_at"
	upkeepKeyConsolidationError = "last_consolidation_error"
	upkeepKeyReport             = "last_report"
)

// upkeepState is the per-service state of the background upkeep loop: the
// one-shot start guard, the running flag, the last inbox scan, a requested
// scan, and the unsupported inbox files and freshly saved files.
type upkeepState struct {
	once          sync.Once
	running       atomic.Bool
	bgJournal     atomic.Bool
	mu            sync.Mutex
	unsupported   []string
	lastScan      time.Time
	scanRequested bool
	justSaved     map[string]bool
}

// StartUpkeep starts the upkeep goroutine once: a 1-minute ticker whose first
// tick runs right after start (REQ-KNU-011, -020, design §5). Later calls
// no-op.
func (s *Service) StartUpkeep(ctx context.Context) {
	if ctx == nil {
		ctx = s.base
	}
	s.upkeep.once.Do(func() {
		s.seedFirstStart()
		go s.upkeepLoop(ctx)
	})
}

// seedFirstStart records the current time as last_consolidation_at when it has
// never been set, so a fresh install waits for the next consolidate_at instead
// of running a full consolidation on its very first tick (REQ-KNU-020). A
// stored value is left untouched, so a run missed while the daemon was down
// still catches up.
func (s *Service) seedFirstStart() {
	if _, ok := s.lastConsolidation(); ok {
		return
	}
	s.setUpkeepValue(upkeepKeyConsolidationAt, strconv.FormatInt(s.now().UnixMilli(), 10))
}

// upkeepLoop runs the first tick at once and then one tick a minute until ctx is
// done (design §5).
func (s *Service) upkeepLoop(ctx context.Context) {
	ticker := time.NewTicker(upkeepTickInterval)
	defer ticker.Stop()
	s.upkeepTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.upkeepTick(ctx)
		}
	}
}

// upkeepTick runs one tick: a due consolidation, else a due inbox scan
// (design §5).
func (s *Service) upkeepTick(ctx context.Context) {
	now := s.now()
	if s.consolidationDue(now) {
		if err := s.Consolidate(ctx); err != nil && !errors.Is(err, core.ErrKnowledgeBusy) {
			slog.Warn("knowledge upkeep: consolidation failed", "error", err)
		}
		return
	}
	// REQ-KBG-021: after the consolidation check, run one background journal
	// pass while the owner is quiet, before the inbox scan.
	s.backgroundJournal(ctx)
	if s.inboxScanDue(now) {
		if err := s.scanInbox(ctx); err != nil {
			slog.Warn("knowledge upkeep: inbox scan failed", "error", err)
		}
	}
}

// inboxScanDue reports whether an inbox scan is due: 5 minutes since the last
// scan, or a scan was requested, or none has run yet (REQ-KNU-011, design §5).
func (s *Service) inboxScanDue(now time.Time) bool {
	s.upkeep.mu.Lock()
	defer s.upkeep.mu.Unlock()
	if s.upkeep.scanRequested {
		return true
	}
	return s.upkeep.lastScan.IsZero() || now.Sub(s.upkeep.lastScan) >= inboxScanInterval
}

// consolidateAt returns the hour and minute of knowledge.consolidate_at. ok is
// false when nightly runs are off or the value is malformed (design §5).
func (s *Service) consolidateAt() (hour, minute int, ok bool) {
	value := defaultConsolidateAt
	if s.cfg.ConsolidateAt != nil {
		value = *s.cfg.ConsolidateAt
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, false
	}
	return parsed.Hour(), parsed.Minute(), true
}

// consolidationDue reports whether the nightly run is due: now is at or past
// today's consolidate_at, the last run was before it, and the owner is quiet
// (REQ-KNU-020, REQ-KBG-040, design §5).
func (s *Service) consolidationDue(now time.Time) bool {
	hour, minute, ok := s.consolidateAt()
	if !ok {
		return false
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if now.Before(at) {
		return false
	}
	if last, ok := s.lastConsolidation(); ok && !last.Before(at) {
		return false
	}
	// REQ-KBG-040: a catch-up after the laptop wakes waits until the owner has
	// been idle for the quiet window.
	if s.act != nil && !s.act.quiet(now) {
		return false
	}
	return true
}

// nextConsolidationAt returns the next scheduled run time in Unix milliseconds,
// or 0 when nightly runs are off (design §5).
func (s *Service) nextConsolidationAt(now time.Time) int64 {
	hour, minute, ok := s.consolidateAt()
	if !ok {
		return 0
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !now.Before(at) {
		at = at.AddDate(0, 0, 1)
	}
	return at.UnixMilli()
}

// Consolidate runs one nightly pass in order: inbox scan, journal passes, then
// promotion, then lint. Each step's error is collected and the run continues
// (REQ-KNU-020, -024, design §5). It returns ErrKnowledgeBusy while a run is in
// progress.
func (s *Service) Consolidate(ctx context.Context) error {
	if ctx == nil {
		ctx = s.base
	}
	if !s.upkeep.running.CompareAndSwap(false, true) {
		return core.ErrKnowledgeBusy
	}
	defer s.upkeep.running.Store(false)

	var (
		errs  []error
		lines []string
	)

	if err := s.scanInbox(ctx); err != nil {
		errs = append(errs, fmt.Errorf("inbox: %w", err))
		lines = append(lines, "- error: inbox: "+err.Error())
	}

	if counts, err := s.journalPasses(ctx); err != nil {
		errs = append(errs, fmt.Errorf("journal: %w", err))
		lines = append(lines, "- error: journal: "+err.Error())
	} else {
		lines = append(lines, formatJournalLines(counts)...)
	}

	promotions, err := s.promote(ctx)
	for _, p := range promotions {
		lines = append(lines, fmt.Sprintf("- promoted %s (%d operations)", p.Path, p.Ops))
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("promotion: %w", err))
		lines = append(lines, "- error: promotion: "+err.Error())
	}

	// REQ-DKN-033: domain promotion runs after the shared promotion and before
	// lint, one agent at a time in name order. A failing agent does not stop
	// the run.
	for _, agent := range domainAgents(s.Config) {
		res, err := s.promoteDomain(ctx, agent)
		lines = append(lines, fmt.Sprintf("- promoted %s: %d ops", agent, res.Applied))
		if err != nil {
			errs = append(errs, fmt.Errorf("promotion %s: %w", agent, err))
			lines = append(lines, "- error: promotion "+agent+": "+err.Error())
		}
	}

	reportPath, err := s.lint(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("lint: %w", err))
		lines = append(lines, "- error: lint: "+err.Error())
	} else if reportPath != "" {
		lines = append(lines, "- report [["+strings.TrimSuffix(reportPath, ".md")+"]]")
	}

	now := s.now()
	s.setUpkeepValue(upkeepKeyConsolidationAt, strconv.FormatInt(now.UnixMilli(), 10))
	errText := ""
	if len(errs) > 0 {
		errText = errors.Join(errs...).Error()
	}
	s.setUpkeepValue(upkeepKeyConsolidationError, errText)
	if reportPath != "" {
		s.setUpkeepValue(upkeepKeyReport, reportPath)
	}
	if err := s.appendUpkeepLog(now, lines); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// journalFailure is one session whose journal pass failed, with the error text
// the consolidation log shows (REQ-KBG-011).
type journalFailure struct {
	sessionID string
	err       string
}

// journalPassCounts tallies the outcomes of one journal pass and lists the
// failed sessions (REQ-KBG-011).
type journalPassCounts struct {
	written   int
	empty     int
	skipped   int
	failed    int
	preempted int
	failures  []journalFailure
}

// Journal-log bounds (REQ-KBG-011): at most journalFailedLogMax failed-session
// lines, each error cut to journalFailRunes runes.
const (
	journalFailedLogMax = 10
	journalFailRunes    = 120
)

// formatJournalLines renders the journal lines of one consolidation entry: the
// journaled count (written plus empty), the failed block, and the preempted
// count (REQ-KBG-011).
func formatJournalLines(c journalPassCounts) []string {
	var lines []string
	if n := c.written + c.empty; n > 0 {
		lines = append(lines, fmt.Sprintf("- journaled %d sessions", n))
	}
	if c.failed > 0 {
		lines = append(lines, fmt.Sprintf("- journal failed: %d sessions", c.failed))
		for i, f := range c.failures {
			if i >= journalFailedLogMax {
				break
			}
			lines = append(lines, "  - "+f.sessionID+": "+firstRunes(f.err, journalFailRunes))
		}
	}
	if c.preempted > 0 {
		lines = append(lines, fmt.Sprintf("- journal preempted: %d sessions (queued again)", c.preempted))
	}
	return lines
}

// journalPasses journals every eligible session with unread text, one at a
// time, and returns the per-outcome counts of the pass (REQ-KNU-003b,
// REQ-KBG-011, design §5).
func (s *Service) journalPasses(ctx context.Context) (journalPassCounts, error) {
	var counts journalPassCounts
	reader, ok := s.store.(core.SessionTextReader)
	if !ok || reader == nil {
		return counts, nil
	}
	if s.cfg.Journal != nil && !*s.cfg.Journal {
		return counts, nil
	}
	heads, err := reader.TextHeads(ctx)
	if err != nil {
		return counts, err
	}
	for _, head := range heads {
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		agentCfg, ok := s.lookupAgent(head.Session.Agent)
		if !ok || !hasKnowledgeTool(agentCfg) {
			continue
		}
		_, idx, err := s.open()
		if err != nil {
			return counts, err
		}
		mark, err := idx.JournalMark(head.Session.ID)
		if err != nil {
			return counts, err
		}
		if head.MaxSeq <= mark {
			continue
		}
		// REQ-KBG-030: each pass runs on a context a turn/start can cancel. A
		// preempted pass is queued again after the next quiet window
		// (REQ-KBG-031).
		passCtx, release := s.act.preemptible(ctx)
		outcome, errText := s.journalSession(passCtx, head.Session)
		release()
		switch outcome {
		case journalWritten:
			counts.written++
		case journalEmpty:
			counts.empty++
		case journalSkipped:
			counts.skipped++
		case journalFailed:
			counts.failed++
			counts.failures = append(counts.failures, journalFailure{sessionID: head.Session.ID, err: errText})
		case journalPreempted:
			if err := ctx.Err(); err != nil {
				return counts, err
			}
			counts.preempted++
			if err := s.act.waitQuiet(ctx); err != nil {
				return counts, err
			}
		}
	}
	return counts, nil
}

// recordJournalDone records the time of a successful background journal pass of
// one session, so the cooldown applies before the next background try
// (REQ-KBG-021).
func (s *Service) recordJournalDone(sessionID string) {
	s.journalDoneMu.Lock()
	if s.journalDone == nil {
		s.journalDone = map[string]time.Time{}
	}
	s.journalDone[sessionID] = s.now()
	s.journalDoneMu.Unlock()
}

// lastJournalDone returns the time of the last successful background journal
// pass of one session (REQ-KBG-021).
func (s *Service) lastJournalDone(sessionID string) (time.Time, bool) {
	s.journalDoneMu.Lock()
	defer s.journalDoneMu.Unlock()
	at, ok := s.journalDone[sessionID]
	return at, ok
}

// backgroundEligible reports whether a session may run a background journal pass
// now: its last successful pass is at least journalCooldown old, or it has none,
// and it has not failed journalMaxFailures times in a row nor failed within the
// cooldown (REQ-KBG-021, REQ-KBG-022).
func (s *Service) backgroundEligible(sessionID string) bool {
	now := s.now()
	if done, ok := s.lastJournalDone(sessionID); ok && now.Sub(done) < journalCooldown {
		return false
	}
	s.journalFailMu.Lock()
	f := s.journalFail[sessionID]
	s.journalFailMu.Unlock()
	if f.failures >= journalMaxFailures {
		return false
	}
	if f.failures > 0 && now.Sub(f.at) < journalCooldown {
		return false
	}
	return true
}

// backgroundJournal runs at most one background journal pass of the oldest
// eligible session, in a goroutine, while the owner is quiet (REQ-KBG-021,
// REQ-KBG-022). The pass is preemptible (REQ-KBG-030) and never blocks the
// caller.
func (s *Service) backgroundJournal(ctx context.Context) {
	if s.act == nil || !s.act.quiet(s.now()) {
		return
	}
	if !s.upkeep.bgJournal.CompareAndSwap(false, true) {
		return
	}
	info, ok := s.pickBackgroundSession(ctx)
	if !ok {
		s.upkeep.bgJournal.Store(false)
		return
	}
	passCtx, release := s.act.preemptible(s.base)
	go func() {
		defer s.upkeep.bgJournal.Store(false)
		defer release()
		outcome, _ := s.journalSession(passCtx, info)
		switch outcome {
		case journalWritten, journalEmpty, journalSkipped:
			// REQ-KBG-021: the cooldown starts at the last successful pass.
			s.recordJournalDone(info.ID)
		case journalPreempted:
			// REQ-KBG-031: the mark stays, no failure count, and the session is
			// eligible again after the next quiet window.
			slog.Debug("knowledge journal: background pass preempted", "session_id", info.ID)
		}
	}()
}

// pickBackgroundSession returns the oldest eligible session for a background
// journal pass: one whose agent has a knowledge tool, that has text after its
// journal mark, and that is past its cooldown (REQ-KBG-021). It reports false
// when none is eligible.
func (s *Service) pickBackgroundSession(ctx context.Context) (core.SessionInfo, bool) {
	reader, ok := s.store.(core.SessionTextReader)
	if !ok || reader == nil {
		return core.SessionInfo{}, false
	}
	if s.cfg.Journal != nil && !*s.cfg.Journal {
		return core.SessionInfo{}, false
	}
	heads, err := reader.TextHeads(ctx)
	if err != nil {
		slog.Warn("knowledge journal: reading text heads failed", "error", err)
		return core.SessionInfo{}, false
	}
	_, idx, err := s.open()
	if err != nil {
		slog.Warn("knowledge journal: open failed", "error", err)
		return core.SessionInfo{}, false
	}
	var eligible []core.SessionInfo
	for _, head := range heads {
		agentCfg, ok := s.lookupAgent(head.Session.Agent)
		if !ok || !hasKnowledgeTool(agentCfg) {
			continue
		}
		mark, err := idx.JournalMark(head.Session.ID)
		if err != nil {
			slog.Warn("knowledge journal: reading mark failed", "session_id", head.Session.ID, "error", err)
			continue
		}
		if head.MaxSeq <= mark || !s.backgroundEligible(head.Session.ID) {
			continue
		}
		eligible = append(eligible, head.Session)
	}
	if len(eligible) == 0 {
		return core.SessionInfo{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].UpdatedAt < eligible[j].UpdatedAt })
	return eligible[0], true
}

// appendUpkeepLog appends one consolidation entry to log.md (REQ-KNU-024,
// design §5).
func (s *Service) appendUpkeepLog(now time.Time, lines []string) error {
	vault, _, err := s.open()
	if err != nil {
		return err
	}
	path := filepath.Join(vault.Root(), "log.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		prefix = "\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s consolidation\n", now.Format("2006-01-02 15:04"))
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	_, err = f.WriteString(prefix + b.String())
	return err
}

// setUpkeepValue writes one upkeep state value, logging but not failing on
// error (design §2).
func (s *Service) setUpkeepValue(key, value string) {
	_, idx, err := s.open()
	if err != nil {
		slog.Warn("knowledge upkeep: open failed", "error", err)
		return
	}
	if err := idx.SetUpkeepValue(key, value); err != nil {
		slog.Warn("knowledge upkeep: saving state failed", "key", key, "error", err)
	}
}

// lastConsolidation returns the time of the last nightly run, if any
// (design §5).
func (s *Service) lastConsolidation() (time.Time, bool) {
	_, idx, err := s.open()
	if err != nil {
		return time.Time{}, false
	}
	value, ok, err := idx.UpkeepValue(upkeepKeyConsolidationAt)
	if err != nil || !ok {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

// ScanInboxNow requests an inbox scan on the next tick (REQ-KNU-011).
func (s *Service) ScanInboxNow() {
	s.upkeep.mu.Lock()
	s.upkeep.scanRequested = true
	s.upkeep.mu.Unlock()
}

// Status reports the vault state for GET /v1/knowledge (API-KNU-003,
// REQ-KNU-040).
func (s *Service) Status(ctx context.Context) (core.KnowledgeStatus, error) {
	status := core.KnowledgeStatus{
		Available:        true,
		Path:             s.vaultPath,
		SourcesPending:   []string{},
		SourcesFailed:    []core.KnowledgeSourceError{},
		InboxUnsupported: []string{},
		Embedding: core.KnowledgeEmbeddingStatus{
			Provider: s.cfg.EmbeddingProvider,
			Model:    s.cfg.EmbeddingModel,
		},
		Log:           []core.KnowledgeLogEntry{},
		Scopes:        []core.KnowledgeScopeStat{},
		JournalFailed: []core.KnowledgeJournalFailure{},
	}
	if _, err := os.Stat(s.vaultPath); err == nil {
		status.Exists = true
	}

	vault, idx, err := s.open()
	if err != nil {
		return status, err
	}
	status.Counts = vaultCounts(vault.Root())

	if states, err := idx.ListSourceStates(statePending); err == nil {
		for _, st := range states {
			status.SourcesPending = append(status.SourcesPending, st.Path)
		}
	}
	if states, err := idx.ListSourceStates(stateFailed); err == nil {
		for _, st := range states {
			status.SourcesFailed = append(status.SourcesFailed, core.KnowledgeSourceError{Path: st.Path, Error: st.Error})
		}
	}
	status.InboxUnsupported = s.inboxUnsupportedList()
	status.Embedding.Chunks, status.Embedding.Vectors = countIndex(s.indexPath)

	status.Agent, status.AgentError = knowledgeAgent(s.Config)

	status.Consolidation.Running = s.upkeep.running.Load()
	if last, ok := s.lastConsolidation(); ok {
		status.Consolidation.LastAt = last.UnixMilli()
	}
	if value, ok, err := idx.UpkeepValue(upkeepKeyConsolidationError); err == nil && ok {
		status.Consolidation.LastError = value
	}
	status.Consolidation.NextAt = s.nextConsolidationAt(s.now())
	if value, ok, err := idx.UpkeepValue(upkeepKeyReport); err == nil && ok {
		status.Consolidation.Report = value
	}
	status.Log = s.recentLog(vault.Root(), 20)
	// REQ-KBG-012: the most recent failed journal pass of each session.
	status.JournalFailed = s.journalFailureStatus()
	// REQ-DKN-012, API-DKN-003: per-scope counts and the pending review count
	// (the web layer adds pending skill proposals later).
	if stats, err := idx.ScopeStats(); err == nil {
		for _, st := range stats {
			status.Scopes = append(status.Scopes, core.KnowledgeScopeStat{
				Scope:       st.Scope,
				Pages:       st.Pages,
				JournalDays: st.JournalDays,
				UpdatedAt:   st.UpdatedAt,
			})
			// API-DKN-003: counts cover every scope; vaultCounts saw shared only.
			if st.Scope != "shared" {
				status.Counts.Pages += st.Pages
				status.Counts.JournalDays += st.JournalDays
			}
		}
	}
	if proposals, err := idx.PendingProposals(); err == nil {
		status.ReviewPending = len(proposals)
	}
	return status, nil
}

// Report returns the path and markdown of the latest lint report
// (API-KNU-004). It wraps os.ErrNotExist when no report exists.
func (s *Service) Report(ctx context.Context) (string, string, error) {
	_, idx, err := s.open()
	if err != nil {
		return "", "", err
	}
	path, ok, err := idx.UpkeepValue(upkeepKeyReport)
	if err != nil {
		return "", "", err
	}
	if !ok || path == "" {
		return "", "", fmt.Errorf("knowledge report: %w", os.ErrNotExist)
	}
	data, err := os.ReadFile(filepath.Join(s.vaultPath, filepath.FromSlash(path)))
	if err != nil {
		return "", "", fmt.Errorf("knowledge report: %w", err)
	}
	return path, string(data), nil
}

// inboxUnsupportedList returns a copy of the unsupported inbox files found by
// the last scan (REQ-KNU-010).
func (s *Service) inboxUnsupportedList() []string {
	s.upkeep.mu.Lock()
	defer s.upkeep.mu.Unlock()
	out := make([]string, len(s.upkeep.unsupported))
	copy(out, s.upkeep.unsupported)
	return out
}

// setInboxUnsupported records the unsupported inbox files of one scan.
func (s *Service) setInboxUnsupported(list []string) {
	if list == nil {
		list = []string{}
	}
	s.upkeep.mu.Lock()
	s.upkeep.unsupported = list
	s.upkeep.mu.Unlock()
}

// vaultCounts counts the pages, sources, and journal days under root
// (API-KNU-003).
func vaultCounts(root string) core.KnowledgeCounts {
	return core.KnowledgeCounts{
		Pages:       countFiles(filepath.Join(root, "wiki"), func(name string) bool { return strings.EqualFold(filepath.Ext(name), ".md") }),
		Sources:     countFiles(filepath.Join(root, "sources"), nil),
		JournalDays: countFiles(filepath.Join(root, "journal"), func(name string) bool { return strings.EqualFold(filepath.Ext(name), ".md") }),
	}
}

// countFiles counts regular files under dir, skipping dot entries. A nil match
// counts every file.
func countFiles(dir string, match func(name string) bool) int {
	count := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		if match == nil || match(d.Name()) {
			count++
		}
		return nil
	})
	return count
}

// countIndex returns the chunk and vector counts of the index database, or 0
// when it is missing or unreadable (API-KNU-003).
func countIndex(indexPath string) (chunks, vectors int) {
	if _, err := os.Stat(indexPath); err != nil {
		return 0, 0
	}
	db, err := sql.Open("sqlite", "file:"+indexPath+"?mode=ro")
	if err != nil {
		return 0, 0
	}
	defer func() { _ = db.Close() }()
	_ = db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&chunks)
	_ = db.QueryRow("SELECT COUNT(*) FROM vectors").Scan(&vectors)
	return chunks, vectors
}

// recentLog returns the newest limit log.md entries, newest first
// (API-KNU-003).
func (s *Service) recentLog(root string, limit int) []core.KnowledgeLogEntry {
	data, err := os.ReadFile(filepath.Join(root, "log.md"))
	if err != nil {
		return []core.KnowledgeLogEntry{}
	}
	entries := parseLogEntries(string(data))
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	if entries == nil {
		entries = []core.KnowledgeLogEntry{}
	}
	return entries
}

// parseLogEntries splits a log.md body into entries at "## " headings, in file
// order. Each entry's lines are the non-empty lines after its heading
// (API-KNU-003).
func parseLogEntries(raw string) []core.KnowledgeLogEntry {
	var entries []core.KnowledgeLogEntry
	current := -1
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			entries = append(entries, core.KnowledgeLogEntry{Heading: strings.TrimSpace(line[3:]), Lines: []string{}})
			current = len(entries) - 1
			continue
		}
		if current < 0 {
			continue
		}
		if trimmed := strings.TrimRight(line, " \t"); strings.TrimSpace(trimmed) != "" {
			entries[current].Lines = append(entries[current].Lines, trimmed)
		}
	}
	return entries
}

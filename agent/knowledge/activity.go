package knowledge

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// activityMaxBytes caps how much of log.md is read; a larger file is read from
// its tail (design §2).
const activityMaxBytes = 4 << 20

// Activity kinds (API-KGR-004). writes is a filter, not a stored kind.
const (
	activityKindRun       = "run"
	activityKindPromotion = "promotion"
	activityKindWrite     = "write"
	activityKindReview    = "review"
	activityKindLint      = "lint"
	activityKindError     = "error"
	activityKindOther     = "other"
	activityKindWrites    = "writes"
)

// activityHeadingTime is the heading timestamp format of log.md, parsed in the
// local zone (design §2).
const activityHeadingTime = "2006-01-02 15:04"

// Health line patterns (design §2).
var (
	activityJournaledRE = regexp.MustCompile(`^journaled (\d+) sessions$`)
	activityAppliedRE   = regexp.MustCompile(`^applied (\d+) ops \((\d+) skipped\)$`)
	activityLintPagesRE = regexp.MustCompile(`^Pages: (\d+)\.`)
)

// activityEntry is one parsed log.md entry with its parsed local time.
type activityEntry struct {
	at      time.Time
	heading string
	kind    string
	actor   string
	lines   []string
}

// Activity returns the newest log.md entries, newest first, paged by limit and
// before (API-KGR-004, REQ-KGR-040). before keeps only entries strictly older;
// a page cut at limit also takes every further entry tied with its last one, so
// paging by next_before loses nothing (API-KGR-004). kind is one of the activity
// kinds or "writes" (promotion plus write). An entry whose heading carries no
// parsable time is dropped.
func (s *Service) Activity(ctx context.Context, limit int, before time.Time, kind string) (core.KnowledgeActivityPage, error) {
	page := core.KnowledgeActivityPage{Entries: []core.KnowledgeActivityEntry{}}
	vault, _, err := s.open()
	if err != nil {
		return page, err
	}
	raw, err := readActivityLog(vault.Root())
	if err != nil {
		return page, err
	}
	entries := parseActivityEntries(raw)

	filtered := make([]activityEntry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !before.IsZero() && !e.at.Before(before) {
			continue
		}
		if !activityKindMatch(kind, e.kind) {
			continue
		}
		filtered = append(filtered, e)
	}
	if limit < 1 {
		limit = 1
	}
	if len(filtered) > limit {
		// API-KGR-004: log times have minute resolution, so a page cut at limit
		// also takes every further entry tied with its last one; before =
		// next_before then loses nothing.
		last := filtered[limit-1].at
		end := limit
		for end < len(filtered) && filtered[end].at.Equal(last) {
			end++
		}
		if end < len(filtered) && filtered[end].at.Before(last) {
			page.NextBefore = last.Format(time.RFC3339)
		}
		filtered = filtered[:end]
	}
	for _, e := range filtered {
		lines := e.lines
		if lines == nil {
			lines = []string{}
		}
		page.Entries = append(page.Entries, core.KnowledgeActivityEntry{
			At: e.at.Format(time.RFC3339), Heading: e.heading, Kind: e.kind, Actor: e.actor, Lines: lines,
		})
	}
	return page, nil
}

// Health returns the upkeep health of the vault: the last run, the next run
// time, the latest lint counts, and the pending review count (API-KGR-005,
// REQ-KGR-041). The memory blocks are added by the runtime.
func (s *Service) Health(ctx context.Context) (core.KnowledgeHealth, error) {
	health := core.KnowledgeHealth{Memory: []core.KnowledgeHealthMemory{}}
	vault, idx, err := s.open()
	if err != nil {
		return health, err
	}
	raw, err := readActivityLog(vault.Root())
	if err != nil {
		return health, err
	}
	health.LastRun = lastRunHealth(parseActivityEntries(raw))
	if health.LastRun != nil && health.LastRun.Error == "" {
		if value, ok, err := idx.UpkeepValue(upkeepKeyConsolidationError); err == nil && ok {
			health.LastRun.Error = value
		}
	}
	if next := s.nextConsolidationAt(s.now()); next > 0 {
		health.NextAt = time.UnixMilli(next).Format(time.RFC3339)
	}
	if path, ok, err := idx.UpkeepValue(upkeepKeyReport); err == nil && ok && path != "" {
		if data, err := os.ReadFile(filepath.Join(s.vaultPath, filepath.FromSlash(path))); err == nil {
			lint := parseLintReport(path, string(data))
			health.Lint = &lint
		}
	}
	if proposals, err := idx.PendingProposals(); err == nil {
		health.ReviewPending = len(proposals)
	}
	return health, nil
}

// readActivityLog reads log.md, or its tail when it is larger than
// activityMaxBytes. A missing file reads as empty.
func readActivityLog(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "log.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(data) > activityMaxBytes {
		data = activityTail(data[len(data)-activityMaxBytes:])
	}
	return string(data), nil
}

// activityTail trims a byte window back to the start of the first heading so a
// partial entry is never parsed.
func activityTail(data []byte) []byte {
	if i := bytes.Index(data, []byte("\n## ")); i >= 0 {
		return data[i+1:]
	}
	return data
}

// parseActivityEntries splits a log.md body into entries at "## " headings, in
// file order. The heading form is "YYYY-MM-DD HH:MM rest"; an entry without a
// parsable time is dropped. Lines drop the leading "- ".
func parseActivityEntries(raw string) []activityEntry {
	var out []activityEntry
	current := -1
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			at, heading, ok := parseActivityHeading(strings.TrimSpace(line[3:]))
			if !ok {
				current = -1
				continue
			}
			out = append(out, activityEntry{
				at: at, heading: heading, kind: activityKind(heading), actor: activityActor(heading),
				lines: []string{},
			})
			current = len(out) - 1
			continue
		}
		if current < 0 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			trimmed = strings.TrimSpace(trimmed[2:])
		}
		out[current].lines = append(out[current].lines, trimmed)
	}
	for i := range out {
		// design §2: a run whose log holds an "error:" line is an error entry.
		if out[i].kind == activityKindRun && hasErrorLine(out[i].lines) {
			out[i].kind = activityKindError
		}
	}
	return out
}

// parseActivityHeading parses the "YYYY-MM-DD HH:MM rest" heading form, in the
// local zone.
func parseActivityHeading(rest string) (time.Time, string, bool) {
	if len(rest) < len(activityHeadingTime) {
		return time.Time{}, "", false
	}
	at, err := time.ParseInLocation(activityHeadingTime, rest[:len(activityHeadingTime)], time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	return at, strings.TrimSpace(rest[len(activityHeadingTime):]), true
}

// activityKind maps a heading to its kind. The first word is checked against
// every heading the code writes today: consolidation (appendUpkeepLog),
// promotion (AppendLog and the promotion actor), compile and agent (Apply
// actors), and the reserved lint, review, and write words (design §2).
func activityKind(heading string) string {
	word, _ := splitFirstWord(heading)
	switch word {
	case "consolidation":
		return activityKindRun
	case "promotion":
		return activityKindPromotion
	case "compile", "agent", "write":
		return activityKindWrite
	case "lint":
		return activityKindLint
	case "review", "proposal":
		return activityKindReview
	default:
		return activityKindOther
	}
}

// activityActor returns the agent name a heading names, else "" (design §2).
func activityActor(heading string) string {
	word, rest := splitFirstWord(heading)
	switch word {
	case "promotion", "agent":
		name, _ := splitFirstWord(rest)
		return name
	}
	return ""
}

// splitFirstWord splits s at the first space.
func splitFirstWord(s string) (first, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

// activityKindMatch reports whether an entry of kind passes the kind filter.
func activityKindMatch(filter, kind string) bool {
	switch filter {
	case "":
		return true
	case activityKindWrites:
		return kind == activityKindPromotion || kind == activityKindWrite
	default:
		return filter == kind
	}
}

// hasErrorLine reports whether any line is an "error:" line.
func hasErrorLine(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, "error:") {
			return true
		}
	}
	return false
}

// lastRunHealth builds the last-run block from the newest run or error entry,
// summing the applied and skipped ops of the promotion entries after the
// previous run entry and up to it (design §2). It returns nil when the log has
// no run or error entry.
func lastRunHealth(entries []activityEntry) *core.KnowledgeHealthRun {
	last := -1
	for i, e := range entries {
		if e.kind == activityKindRun || e.kind == activityKindError {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	prev := -1
	for i := 0; i < last; i++ {
		if entries[i].kind == activityKindRun || entries[i].kind == activityKindError {
			prev = i
		}
	}
	run := &core.KnowledgeHealthRun{At: entries[last].at.Format(time.RFC3339)}
	for _, line := range entries[last].lines {
		if n, ok := parseJournaledLine(line); ok {
			run.Journaled = n
		}
		if e := errorText(line); e != "" && run.Error == "" {
			run.Error = e
		}
	}
	for i := prev + 1; i <= last; i++ {
		if entries[i].kind != activityKindPromotion {
			continue
		}
		for _, line := range entries[i].lines {
			if applied, skipped, ok := parseAppliedLine(line); ok {
				run.Applied += applied
				run.Skipped += skipped
			}
		}
	}
	return run
}

// parseJournaledLine reads a "journaled N sessions" line.
func parseJournaledLine(line string) (int, bool) {
	m := activityJournaledRE.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// parseAppliedLine reads an "applied N ops (M skipped)" line.
func parseAppliedLine(line string) (applied, skipped int, ok bool) {
	m := activityAppliedRE.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	applied, errA := strconv.Atoi(m[1])
	skipped, errS := strconv.Atoi(m[2])
	return applied, skipped, errA == nil && errS == nil
}

// errorText returns the text of an "error:" line, else "".
func errorText(line string) string {
	if !strings.HasPrefix(line, "error:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "error:"))
}

// parseLintReport reads the counts of a rendered lint report (design §2). Pages
// comes from the "Pages: N." header line; every "- " line under a section
// heading counts toward that section.
func parseLintReport(path, markdown string) core.KnowledgeHealthLint {
	lint := core.KnowledgeHealthLint{Path: path, Date: lintReportDate(path)}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if m := activityLintPagesRE.FindStringSubmatch(trimmed); m != nil {
			lint.Pages, _ = strconv.Atoi(m[1])
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			section = strings.TrimSpace(trimmed[3:])
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		switch section {
		case "Contradictions":
			lint.Contradictions++
		case "Marked conflicts":
			lint.Conflicts++
		case "Likely duplicates":
			lint.Duplicates++
		case "Broken links":
			lint.Broken++
		case "Orphan pages":
			lint.Orphans++
		case "Missing details":
			lint.Missing++
		}
	}
	return lint
}

// lintReportDate reads the date from a report path like
// "reports/lint-2026-11-11.md".
func lintReportDate(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".md")
	return strings.TrimPrefix(base, "lint-")
}

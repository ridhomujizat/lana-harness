package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
)

// activityFixture is the real log.md format written by the promotion and
// consolidation passes (spec 31/46 output, copied verbatim).
const activityFixture = `## 2026-11-11 06:43 promotion lana
- created [[GitHub Trending Snapshot]] (lana)
- from [[agents/lana/journal/2026-11-08]]

## 2026-11-11 06:43 promotion lana
- journal: agents/lana/journal/2026-11-08.md
- applied 2 ops (0 skipped)
- proposals applied: 

## 2026-11-11 06:44 consolidation
- journaled 11 sessions
- promoted lana: 2 ops
- report [[reports/lint-2026-11-11]]
`

// writeLog writes the vault log.md.
func (e *vaultEnv) writeLog(content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.vaultDir, "log.md"), []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// atLocal builds a local time for the assertions.
func atLocal(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.Local)
}

// AC-KGR-004: the real log format parses into entries with their kind, actor,
// and lines, newest first.
func TestActivityParsesRealLog(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(activityFixture)

	page, err := env.svc.Activity(context.Background(), 50, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("entries = %d, want 3: %+v", len(page.Entries), page.Entries)
	}
	if page.NextBefore != "" {
		t.Fatalf("next_before = %q, want empty", page.NextBefore)
	}
	run := page.Entries[0]
	if run.Kind != "run" || run.Heading != "consolidation" || run.Actor != "" {
		t.Fatalf("run entry = %+v", run)
	}
	if run.At != atLocal(2026, 11, 11, 6, 44).Format(time.RFC3339) {
		t.Fatalf("run at = %q", run.At)
	}
	wantLines := []string{"journaled 11 sessions", "promoted lana: 2 ops", "report [[reports/lint-2026-11-11]]"}
	if len(run.Lines) != len(wantLines) {
		t.Fatalf("run lines = %v", run.Lines)
	}
	for i, want := range wantLines {
		if run.Lines[i] != want {
			t.Fatalf("run lines[%d] = %q, want %q", i, run.Lines[i], want)
		}
	}
	promo := page.Entries[1]
	if promo.Kind != "promotion" || promo.Actor != "lana" || promo.Heading != "promotion lana" {
		t.Fatalf("promotion entry = %+v", promo)
	}
	if promo.At != atLocal(2026, 11, 11, 6, 43).Format(time.RFC3339) {
		t.Fatalf("promotion at = %q", promo.At)
	}
	if len(promo.Lines) != 3 || promo.Lines[0] != "journal: agents/lana/journal/2026-11-08.md" || promo.Lines[2] != "proposals applied:" {
		t.Fatalf("promotion lines = %v", promo.Lines)
	}
}

// AC-KGR-004: the kind filter narrows the list; writes matches promotion plus
// write.
func TestActivityKindFilter(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(activityFixture)

	cases := []struct {
		kind string
		want int
	}{
		{"", 3},
		{"run", 1},
		{"promotion", 2},
		{"writes", 2},
		{"write", 0},
		{"review", 0},
		{"lint", 0},
		{"error", 0},
		{"other", 0},
	}
	for _, tc := range cases {
		page, err := env.svc.Activity(context.Background(), 50, time.Time{}, tc.kind)
		if err != nil {
			t.Fatalf("kind %q: %v", tc.kind, err)
		}
		if len(page.Entries) != tc.want {
			t.Fatalf("kind %q entries = %d, want %d", tc.kind, len(page.Entries), tc.want)
		}
	}
}

// AC-KGR-004: before keeps only entries strictly older.
func TestActivityBeforeStrictlyOlder(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(activityFixture)

	page, err := env.svc.Activity(context.Background(), 50, atLocal(2026, 11, 11, 6, 44), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("before 06:44 entries = %d, want 2", len(page.Entries))
	}
	for _, e := range page.Entries {
		if e.Kind != "promotion" {
			t.Fatalf("entry = %+v, want only promotions older than 06:44", e)
		}
	}
	page, err = env.svc.Activity(context.Background(), 50, atLocal(2026, 11, 11, 6, 43), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("before 06:43 entries = %d, want 0 (strictly older)", len(page.Entries))
	}
}

// AC-KGR-004: paging with limit returns next_before, and before reaches the
// next page.
func TestActivityPaging(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(`## 2026-11-11 06:41 promotion lana
- applied 1 ops (0 skipped)

## 2026-11-11 06:42 consolidation
- journaled 3 sessions

## 2026-11-11 06:43 promotion rio
- applied 2 ops (1 skipped)
`)

	first, err := env.svc.Activity(context.Background(), 2, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.Entries[0].Actor != "rio" || first.Entries[1].Kind != "run" {
		t.Fatalf("first page = %+v", first.Entries)
	}
	if first.NextBefore != atLocal(2026, 11, 11, 6, 42).Format(time.RFC3339) {
		t.Fatalf("next_before = %q", first.NextBefore)
	}
	before, _ := time.Parse(time.RFC3339, first.NextBefore)
	second, err := env.svc.Activity(context.Background(), 2, before, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Kind != "promotion" || second.Entries[0].Actor != "lana" {
		t.Fatalf("second page = %+v", second.Entries)
	}
	if second.NextBefore != "" {
		t.Fatalf("second next_before = %q, want empty", second.NextBefore)
	}
}

// AC-KGR-004: a page cut inside a tied minute keeps the whole tied group, and
// next_before is empty when nothing older remains.
func TestActivityPagingTieAtLimit(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(`## 2026-11-11 06:42 promotion lana
- applied 1 ops (0 skipped)

## 2026-11-11 06:42 promotion rio
- applied 2 ops (0 skipped)

## 2026-11-11 06:42 promotion sari
- applied 3 ops (0 skipped)

## 2026-11-11 06:43 consolidation
- journaled 4 sessions
`)

	// Newest first: entry 1 (06:43) then the tied entries 2-4 (06:42). A limit
	// of 2 cuts at entry 2, so the whole tied group joins page 1.
	page, err := env.svc.Activity(context.Background(), 2, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 4 {
		t.Fatalf("entries = %d, want 4 (entry 1 plus the tied 2-4)", len(page.Entries))
	}
	if page.NextBefore != "" {
		t.Fatalf("next_before = %q, want empty (nothing older remains)", page.NextBefore)
	}
	if page.Entries[0].At != atLocal(2026, 11, 11, 6, 43).Format(time.RFC3339) {
		t.Fatalf("entry 1 at = %q, want 06:43", page.Entries[0].At)
	}
	for i := 1; i < 4; i++ {
		if page.Entries[i].At != atLocal(2026, 11, 11, 6, 42).Format(time.RFC3339) {
			t.Fatalf("entry %d at = %q, want 06:42", i+1, page.Entries[i].At)
		}
	}
}

// AC-KGR-004: paging across a tied minute loses and repeats no entry.
func TestActivityPagingTieAcrossPages(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(`## 2026-11-11 06:38 promotion lana
- applied 1 ops (0 skipped)

## 2026-11-11 06:42 promotion rio
- applied 2 ops (0 skipped)

## 2026-11-11 06:42 promotion sari
- applied 3 ops (0 skipped)

## 2026-11-11 06:42 promotion bayu
- applied 4 ops (0 skipped)

## 2026-11-11 06:44 consolidation
- journaled 5 sessions
`)

	// Newest first: consolidation (06:44), the tied group rio/sari/bayu (06:42),
	// then lana (06:38). A limit of 2 cuts inside the tied group.
	first, err := env.svc.Activity(context.Background(), 2, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 4 {
		t.Fatalf("first page entries = %d, want 4", len(first.Entries))
	}
	if first.Entries[0].Heading != "consolidation" {
		t.Fatalf("first entry = %+v, want consolidation", first.Entries[0])
	}
	for i := 1; i < 4; i++ {
		if first.Entries[i].At != atLocal(2026, 11, 11, 6, 42).Format(time.RFC3339) {
			t.Fatalf("first page entry %d at = %q, want 06:42", i, first.Entries[i].At)
		}
	}
	if first.NextBefore != atLocal(2026, 11, 11, 6, 42).Format(time.RFC3339) {
		t.Fatalf("next_before = %q, want 06:42", first.NextBefore)
	}

	before, _ := time.Parse(time.RFC3339, first.NextBefore)
	second, err := env.svc.Activity(context.Background(), 2, before, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Actor != "lana" {
		t.Fatalf("second page = %+v, want only lana (06:38)", second.Entries)
	}
	if second.NextBefore != "" {
		t.Fatalf("second next_before = %q, want empty", second.NextBefore)
	}

	// Both pages together carry every entry exactly once.
	got := append([]core.KnowledgeActivityEntry{}, first.Entries...)
	got = append(got, second.Entries...)
	seen := map[string]int{}
	for _, e := range got {
		seen[e.Heading]++
	}
	if len(seen) != 5 {
		t.Fatalf("distinct entries across pages = %d, want 5: %v", len(seen), seen)
	}
	for heading, n := range seen {
		if n != 1 {
			t.Fatalf("entry %q appears %d times, want once", heading, n)
		}
	}
}

// AC-KGR-004: a run entry that carries an error line is an error entry.
func TestActivityRunWithErrorIsError(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(`## 2026-11-11 06:44 consolidation
- journaled 2 sessions
- error: lint: boom
`)

	page, err := env.svc.Activity(context.Background(), 50, time.Time{}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Kind != "error" {
		t.Fatalf("entries = %+v, want one error", page.Entries)
	}
}

// AC-KGR-004: health reads the last run, the summed promotion ops, and the lint
// counts of the rendered report.
func TestHealthFromLogAndReport(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(activityFixture)

	report := `# Knowledge lint 2026-11-11

Pages: 2. Problems: 3.

## Contradictions
- [[A]]: "x" contradicts "y"

## Broken links
- [[A]] → [[Missing]]

## Orphan pages
- [[B]]
`
	reportRel := "reports/lint-2026-11-11.md"
	if err := os.MkdirAll(filepath.Join(env.vaultDir, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.vaultDir, filepath.FromSlash(reportRel)), []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	env.svc.setUpkeepValue(upkeepKeyReport, reportRel)

	health, err := env.svc.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.LastRun == nil {
		t.Fatal("last_run is null")
	}
	if health.LastRun.At != atLocal(2026, 11, 11, 6, 44).Format(time.RFC3339) {
		t.Fatalf("last_run at = %q", health.LastRun.At)
	}
	if health.LastRun.Journaled != 11 || health.LastRun.Applied != 2 || health.LastRun.Skipped != 0 {
		t.Fatalf("last_run = %+v", health.LastRun)
	}
	if health.LastRun.Error != "" {
		t.Fatalf("last_run error = %q, want empty", health.LastRun.Error)
	}
	if health.NextAt != atLocal(2026, 11, 2, 3, 0).Format(time.RFC3339) {
		t.Fatalf("next_at = %q", health.NextAt)
	}
	if health.Lint == nil {
		t.Fatal("lint is null")
	}
	want := core.KnowledgeHealthLint{
		Date: "2026-11-11", Path: reportRel, Pages: 2, Contradictions: 1, Broken: 1, Orphans: 1,
	}
	if *health.Lint != want {
		t.Fatalf("lint = %+v, want %+v", *health.Lint, want)
	}
	if health.ReviewPending != 0 {
		t.Fatalf("review_pending = %d, want 0", health.ReviewPending)
	}
	if health.Memory == nil {
		t.Fatal("memory is null")
	}
}

// AC-KGR-004: health falls back to the stored consolidation error and is null
// when the log has no run.
func TestHealthErrorFallbackAndNullRun(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(`## 2026-11-11 06:43 promotion lana
- applied 1 ops (0 skipped)
`)
	env.svc.setUpkeepValue(upkeepKeyConsolidationError, "promotion: model failed")

	health, err := env.svc.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.LastRun != nil {
		t.Fatalf("last_run = %+v, want null without a run entry", health.LastRun)
	}
	if health.Lint != nil {
		t.Fatalf("lint = %+v, want null", health.Lint)
	}
}

// AC-KGR-004: the error line of the last run is the health error.
func TestHealthErrorFromRunLine(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeLog(`## 2026-11-11 06:44 consolidation
- journaled 2 sessions
- error: lint: boom
`)
	health, err := env.svc.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.LastRun == nil || health.LastRun.Error != "lint: boom" {
		t.Fatalf("last_run = %+v", health.LastRun)
	}
}

// API-KGR-003, REQ-KGR-030: decided proposals are newest decided first and
// pending ones are left out.
func TestDecidedProposalsOrder(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	_, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	seed := []kv.Proposal{
		{ID: "p_applied", Agent: "dana", Status: proposalStatusApplied, DecidedAt: 3000},
		{ID: "p_approved", Agent: "dana", Status: proposalStatusApproved, DecidedAt: 5000},
		{ID: "p_rejected", Agent: "rio", Status: proposalStatusRejected, DecidedAt: 4000},
		{ID: "p_pending", Agent: "rio", Status: proposalStatusPending, DecidedAt: 0},
	}
	for _, p := range seed {
		if err := idx.AddProposal(p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := env.svc.DecidedProposals(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"p_approved", "p_rejected", "p_applied"}
	if len(got) != len(want) {
		t.Fatalf("decided = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("decided[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
	limited, err := env.svc.DecidedProposals(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ID != "p_approved" || limited[1].ID != "p_rejected" {
		t.Fatalf("limited = %+v", limited)
	}
}

// AC-KGR-003, API-KGR-002: Page returns the detail fields, the resolved out
// links, the unresolved targets, and the backlinks with context.
func TestPageDetailFields(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeWiki("A.md", "---\ntype: topic\ndescription: halaman A\nupdated: 2026-11-11\nsources:\n  - sources/2026-11-01 Rapat.pdf\n---\n# A\n\nSee [[B]] and [[Missing]].\n")
	env.writeWiki("B.md", "---\ntype: topic\n---\n# B\n\nBack to [[A]].\n")

	doc, raw, err := env.svc.Page(context.Background(), "wiki/A.md")
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || doc.Path != "wiki/A.md" || doc.Scope != "shared" {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Type != "topic" || doc.Description != "halaman A" || doc.Updated != "2026-11-11" {
		t.Fatalf("detail = type %q description %q updated %q", doc.Type, doc.Description, doc.Updated)
	}
	if len(doc.Sources) != 1 || doc.Sources[0] != "sources/2026-11-01 Rapat.pdf" {
		t.Fatalf("sources = %v", doc.Sources)
	}
	if len(doc.LinksOut) != 1 || doc.LinksOut[0].Path != "wiki/B.md" || doc.LinksOut[0].Title != "B" || doc.LinksOut[0].Scope != "shared" {
		t.Fatalf("links_out = %+v", doc.LinksOut)
	}
	if len(doc.Unresolved) != 1 || doc.Unresolved[0] != "Missing" {
		t.Fatalf("unresolved = %v", doc.Unresolved)
	}
	if len(doc.LinksIn) != 1 || doc.LinksIn[0].Path != "wiki/B.md" || doc.LinksIn[0].Title != "B" {
		t.Fatalf("links_in = %+v", doc.LinksIn)
	}
	if doc.LinksIn[0].Context == "" {
		t.Fatal("links_in context is empty")
	}
}

// API-KGR-002: frontmatter sources render as link targets, without brackets or
// a display label.
func TestSourceTarget(t *testing.T) {
	cases := map[string]string{
		"[[agents/lana/journal/2026-11-08]]": "agents/lana/journal/2026-11-08",
		"[[sources/x.pdf|Rapat]]":            "sources/x.pdf",
		"sources/x.pdf":                      "sources/x.pdf",
		"  [[  spaced ]]  ":                  "spaced",
		"":                                   "",
	}
	for in, want := range cases {
		if got := sourceTarget(in); got != want {
			t.Fatalf("sourceTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

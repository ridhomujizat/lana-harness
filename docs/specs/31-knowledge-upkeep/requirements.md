# Requirements: Knowledge upkeep

- **Issue**: —
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (config keys, Telegram command, HTTP)
- **Depends on**: spec 29 (conversation text index), spec 30 (vault, index, compile pass)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-01 | "implement 3 spec yg kamu usulkan"; the proposal: nightly consolidation (contradictions, orphans, duplicates) and auto-ingest from attachments/Telegram into `sources/`, plus the memory flush before compaction moved here | goal |
| OpenClaw memory docs | pre-compaction memory flush to the daily note (on by default); "dreaming": a recurring job promotes candidates to long-term memory | reference design |
| Karpathy LLM wiki, ai-second-brain | periodic health check for contradictions, orphan pages, missing concept pages | reference design |
| evonic | nightly janitor merges duplicates and fixes links; it may delete files, a prompt-injection risk (`memory_manager.py:211-388`) | reference design, and a risk to avoid |

## Goal

Knowledge grows without the owner asking each time. What is said in conversations lands in a daily
journal, the journal and dropped files become wiki pages, and once a day lana checks the wiki for
problems and reports them. Like sleep for a human brain: consolidate, do not destroy.

## Definitions

- **Journal**: `journal/YYYY-MM-DD.md` in the vault, append-only notes taken from conversations.
- **Upkeep**: the background work of this spec. It runs only in the daemon (`lana daemon`).
- **Knowledge agent**: the agent whose provider and model run the upkeep passes that do not belong
  to one session.

## Requirements

### Journal (memory flush)

- **REQ-KNU-001** A journal pass reads the conversation text (spec 29) of one searchable session
  that was not journaled yet and appends notes worth keeping long-term (facts learned, decisions,
  plans, things the owner shared, results) to the journal of the day of the newest message read. It
  skips chit-chat and task mechanics. A pass reads at most 40,000 characters, oldest first; the rest
  waits for the next pass.
- **REQ-KNU-002** Each session has a journal mark: the last seq read. A pass that fails leaves the
  mark unchanged. Text with no owner message moves the mark without a model call.
- **REQ-KNU-003** A journal pass runs (a) in the background right after compaction writes a summary
  for that session, and (b) for every session with unread text during the nightly run. Only sessions
  whose agent has a knowledge tool are journaled, and only when `knowledge.journal` is not `false`.
- **REQ-KNU-004** A journal pass uses the provider and model of the session's agent. A journal entry
  names the time, agent, channel, and session id, so `session_search` can open the source.

### Inbox and Telegram

- **REQ-KNU-010** Files the owner puts in `inbox/` (`.md`, `.txt`, `.pdf`, `.html`) are ingested as
  sources with the file name as title and compiled by the knowledge agent. An ingested inbox file is
  removed from `inbox/` (moved into `sources/`, or dropped when it is a duplicate). Files changed in
  the last 10 seconds wait. Unsupported files stay and are listed in the status.
- **REQ-KNU-011** The daemon scans `inbox/` at start and every 5 minutes, and at once after a web
  upload.
- **REQ-KNU-012** Telegram `/save` stores a source and starts its compile with the chat's current
  agent: as a reply to a message, it saves that message's document (`.md`, `.txt`, `.pdf`, `.html`)
  or text; with arguments, it saves the argument text, or fetches it when the argument is one URL; as
  the caption of a document, it saves the document. In groups, only the owner can use it. The reply
  names the stored path or the existing duplicate.

### Nightly consolidation

- **REQ-KNU-020** Once a day at `knowledge.consolidate_at` (local time, default `03:00`, `""` turns
  it off), the daemon runs, in order: inbox scan, journal passes, promotion, lint. A run missed while
  the daemon was down starts within a minute of the next start. Only one run at a time.
- **REQ-KNU-021** Promotion compiles the journal text added since the last promotion into wiki pages
  with the spec 30 compile pass, without a source summary page; the pages cite the journal day in
  `sources`.
- **REQ-KNU-022** Lint checks every wiki page and writes `reports/lint-YYYY-MM-DD.md`: broken
  `[[links]]`, orphan pages (no inbound link, source pages excluded), likely duplicates (titles or
  aliases equal after case, accent, and punctuation folding), pages missing `type` or `description`,
  and marked conflicts (`**Conflict:**` lines). It also asks the knowledge agent's model about
  contradictions on the pages changed since the last run (at most 20 pages, each with up to 3 linked
  pages).
- **REQ-KNU-023** Upkeep never deletes or rewrites pages. Its only page changes are the
  compile-pass operations of spec 30. It never follows instructions found in sources or
  conversations to change files outside the vault.
- **REQ-KNU-024** Each run appends a `log.md` entry with what it did and the report path. A failed
  step is logged and the next steps still run.

### Model for upkeep

- **REQ-KNU-030** The knowledge agent is `knowledge.agent`, default the first agent in name order
  whose `tools` list has a knowledge tool. Without one, steps that need it are skipped and the
  status says why.

### Web

- **REQ-KNU-040** A web page "Knowledge" shows: the vault path, page/source/journal counts, pending
  and failed sources with errors, unsupported inbox files, embedding provider and indexed vectors,
  the last and next consolidation, the latest lint report, and the 20 newest `log.md` entries.
- **REQ-KNU-041** The page uploads files into `inbox/` and has a "Run consolidation now" button.
  A run already in progress is reported, not started twice.

## Acceptance criteria

- **AC-KNU-001** (REQ-KNU-001, -002, -004) A fake provider gets one journal request for a session
  with two turns, notes land in the right day file with the header, the mark moves; a second pass
  without new text makes no request; a provider error keeps the mark. Evidence: service test.
- **AC-KNU-002** (REQ-KNU-003) A compaction summary triggers one journal pass for that session; a
  session whose agent has no knowledge tool is skipped; `journal = false` skips. Evidence: test.
- **AC-KNU-003** (REQ-KNU-010, -011) An inbox PDF becomes a source and leaves `inbox/`; a duplicate
  is dropped; a `.docx` stays and is listed; a file modified 1 s ago waits. Evidence: test.
- **AC-KNU-004** (REQ-KNU-012) `/save` as a reply to a document, to a text, with a URL argument, with
  text, as a caption; a non-owner in a group is refused. Evidence: Telegram channel test with a fake
  backend.
- **AC-KNU-005** (REQ-KNU-020, -021, -024) With a fake clock: a run fires once at 03:00, not twice;
  a missed run fires after start; promotion compiles only new journal text. Evidence: test.
- **AC-KNU-006** (REQ-KNU-022, -023) A fixture vault with a broken link, an orphan, a duplicate pair,
  a page without description, and a conflict line yields a report listing each; no page file changes.
  Evidence: lint test.
- **AC-KNU-007** (REQ-KNU-030) Default knowledge agent selection and the skip status. Evidence: test.
- **AC-KNU-008** (REQ-KNU-040, -041) API responses and the page render; upload lands in `inbox/`;
  a second consolidate while busy returns `409`. Evidence: Go handler tests and vitest.

## Out of scope

- Automatic merging or deletion of duplicate pages: the owner acts on the report.
- Journal passes for scheduled turns (no scheduler exists).
- A CLI command for consolidation.
- Knowledge upkeep usage in the monitoring dashboard.

## Resolved questions

| Question | Resolution |
|---|---|
| Flush target: `MEMORY.md` or the vault? | Rekomendasi agent: the vault journal. `MEMORY.md` is bounded at 32,000 characters and is meant for short facts. |
| Ingest every Telegram attachment? | Rekomendasi agent: no, only on `/save`. Many attachments are questions, not knowledge. |
| Can lint fix things? | Rekomendasi agent: report only. evonic's janitor deletes files on LLM judgment, which is the risk this spec avoids. |
| Upkeep in the CLI? | Rekomendasi agent: daemon only. The CLI may exit mid-pass; journal marks make the nightly run catch up. The compaction-triggered journal pass still runs in any process. |

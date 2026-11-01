# Tasks: Knowledge upkeep

- **Status**: Completed
- **Revision**: 1

Each task is one `dsh-coder` run. Test command for every task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; UI tasks also
`cd web/ui && npx vitest run && npm run build`. All tasks need spec 29 and spec 30 done.

## Wave 1

### T-001 Text reader, state tables, journal file, config

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok; tests cover `TextHeads`, `TextRows`, index v1→v2, marks, `AppendJournal`, config keys.
- Files: `core/sessionsearch.go`, `store/store.go` + test, `internal/knowledge` (index schema v2,
  `AppendJournal`, `inbox/` and `journal/` in `Open`) + tests, `core/config.go`,
  `config/validate.go` + test, `providers/openai/module.go`.
- Covers: design §1, §2, §3 (config only). API-KNU-001.

## Wave 2

### T-002 Journal pass and compaction trigger

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok; tests cover AC-KNU-001, AC-KNU-002, AC-KNU-007, compaction observer on success only. 2 dsh runs (first left a compile error).
- Files: new `agent/knowledge/journal.go`, `agent/knowledge/agent.go` (knowledge agent),
  `agent/knowledge/module.go`, `agent/compaction/compaction.go` (`OnCompacted`), tests.
- Covers: REQ-KNU-001–004, -030. Design §3 (agent selection), §4.
- Acceptance: AC-KNU-001, AC-KNU-002, AC-KNU-007.

## Wave 3

### T-003 Upkeep loop, inbox, promotion, lint, runtime wiring

- Status: Done 2026-11-01. Evidence: `go vet` clean, `go test -count=1 ./...` all ok; tests cover AC-KNU-003, AC-KNU-005, AC-KNU-006, busy, status shape. Runtime wiring done in T-005.
- Files: new `agent/knowledge/{upkeep.go,inbox.go,promote.go,lint.go}` + tests,
  `agent/knowledge/compile.go` (refactor to `compileInput`), new `core.KnowledgeAdmin` in
  `core/knowledge.go`, runtime wiring in `lana.go` and `cmd/lana/daemon.go` moved to T-004 to avoid a parallel edit of `lana.go`.
- Covers: REQ-KNU-010, -011, -020–024. Design §5–§9.
- Acceptance: AC-KNU-003, AC-KNU-005, AC-KNU-006.

## Wave 4

### T-004 Telegram `/save`

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok; `channels/telegram/save_test.go` covers AC-KNU-004 (reply document/text, URL, text, caption, non-owner, unsupported, unavailable, duplicate, `/help`).
- Files: `channels/telegram/{channel.go,chat.go}` + tests, `lana.go` (`IngestKnowledge`),
  `tools/webfetch.go` (export `FetchMarkdown`), `cmd/lana/daemon.go` (bridge).
- Covers: REQ-KNU-012, API-KNU-002. Design §9 (`IngestKnowledge`), §10.
- Acceptance: AC-KNU-004.

### T-005 Web Knowledge page

- Status: Done 2026-11-01. Evidence: `go test -count=1 ./...` all ok (web handler tests for every API-KNU-003–006 status); vitest 28 files / 186 tests; `web/dist` rebuilt. Runtime upkeep wiring (`StartKnowledgeUpkeep`, status, report, upload, consolidate) included.

## End-to-end check 2026-11-01

Real DeepSeek (`deepseek-flash`), temp `LANA_HOME`:
- `lana run` + `knowledge_write` created `wiki/Pak Budi Santoso.md`; `index.md` and `log.md` updated.
- Next turn: `<context>` held `relevant knowledge: - Pak Budi Santoso (...)`; the answer used it with no tool call.
- `lana daemon`: an `inbox/` note moved to `sources/`, compiled into 7 pages (source page, Proyek Atlas, PT Sinar, Rina Wijaya, PostgreSQL 16, Ridho, Jakarta link), appended to the existing Pak Budi page with a `**Conflict:**` line for the moved deadline.
- Journal pass wrote notes for 2 sessions; lint wrote `reports/lint-2026-11-01.md`.
- Bugs found and fixed: raw newlines in model JSON (lenient decoder), compile prompt lacked the op schema (model invented keys; schema added), heading sent with `##` hashes, failed compile logged at Info (now Warn), fresh install consolidated on first start (now seeds the time), journal blocks without a blank line, flaky upkeep test cleanup.
- Files: new `web/knowledge.go` + test, `web/server.go` (backend detection, routes),
  `cmd/lana/daemon.go` or `cmd/lana/web.go` (bridge), `web/ui/src/views/knowledge.tsx` + test, nav and
  route files, API client, `web/dist`.
- Covers: REQ-KNU-040, -041, API-KNU-003–006. Design §11.
- Acceptance: AC-KNU-008.

## Readiness check 2026-11-01 (second pass)

Real DeepSeek, temp `LANA_HOME`, daemon + web API over curl:
- `session_search` called by the agent found the earlier Pak Budi session with snippets.
- `knowledge_ingest` stored a PDF from the workspace and a URL (example.com) as sources.
- Sources whose compile was cut by `lana run` exiting now stay `pending` (was `failed: context canceled`); the daemon re-queued and compiled them on start.
- Web: sign-in claim, `GET /v1/knowledge` (status), `POST /v1/knowledge/upload` (200; `.docx` → 400), `POST /v1/knowledge/consolidate` while running → 409 `busy`, `GET /v1/knowledge/report` → 200.
- Consolidation: journaled 2 sessions, promoted the journal (3 operations), lint report listed the marked conflict; an uploaded note updated Rina Wijaya and Proyek Atlas and created Tim Bandung.
- Not checked live: embeddings (no provider key or Ollama on this machine), Telegram `/save` (unit tests only), the web page in a browser (vitest only).

# Tasks: Domain knowledge per agent

- **Status**: Completed
- **Revision**: 1

Each Go task is one `dsh-coder` run (implementer per `.claude/implementer.local`). UI is coded
directly. Test command for every Go task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./<affected>/...`; the full
suite (`go test -race -count=1 ./...`, `golangci-lint run`) runs after each wave.

## Wave 1

### T-001 Vault scopes and supersede (`internal/knowledge` vault side)

- Status: Done 2026-11-10 (a5e0885). Evidence: `go test ./internal/knowledge/...` ok; tests for scope helpers, domain create/append, two-scope titles, domain and vault index, log suffix, `AppendJournalIn`, supersede.
- Files: new `internal/knowledge/scope.go` + test; `vault.go`, `write.go`, `page.go` + tests.
- Covers: REQ-DKN-001–003, -025. Design §1.
- Acceptance: domain create/append, same title in two scopes, domain `index.md`, vault `index.md`
  domains section, log scope suffix, `AppendJournalIn`, supersede format and errors.

### T-002 Agent memory limit and skill proposals (`internal/agenthome`, config)

- Status: Done 2026-11-10 (b832f6d). Evidence: `go test` ok for agenthome, config, web, memcapture, tools; tests for the non-growing rule, `RemoveEntries`, skill proposals, config validation, web PUT 200/400.
- Files: `internal/agenthome/agenthome.go`, `skills.go` + tests; `core/config.go`
  (`SkillWrite`), `config/validate.go` + test; `web/agentfiles.go` + test.
- Covers: REQ-DKN-040, -050, -051 (storage side), API-DKN-002, -009. Design §6, §7.
- Acceptance: AC-DKN-006 (agenthome and web parts), skill proposal create/replace/list/approve/
  reject, config validation.

## Wave 2

### T-003 Index schema v3 (`internal/knowledge` index side)

- Status: Done 2026-11-10 (6cb1afc). Evidence: `go test ./internal/knowledge/...` ok; migration v2→v3, scope index, scope filter, recall tie-break, stripped superseded text, proposals CRUD, `ScopeStats`, `Pages`.
- Files: `internal/knowledge/index.go`, `search.go`, `chunk.go` + tests.
- Covers: REQ-DKN-004, -012 (index part), -026. Design §2.
- Acceptance: AC-DKN-001 (index part), scope filter, recall preference, stripped superseded text,
  proposals CRUD, `ScopeStats`, `Pages`, v2→v3 migration.

## Wave 3

### T-004 Core types and scoped service

- Status: Done 2026-11-10 (f506d7c). Evidence: `go test ./agent/... ./tools/... ./web/...` ok; journal routing, proposals, `ReadIn` order, page path validation, domain stats, lint scopes.
- Files: `core/knowledge.go`, `core/tool.go` (`OwnerTurn`), `agent/knowledge/service.go`,
  `journal.go`, `lint.go`, `agent.go` + tests; callers of `Recall` adjusted to compile.
- Covers: REQ-DKN-010–011 (service), -020–022, -030, -060 (proposal part). Design §3, §4 (not
  4.2/4.3), §4.4.
- Acceptance: AC-DKN-003 (service part), journal routing, `Propose`/`DecideProposal`, `ReadIn`
  order, lint scope.

## Wave 4

### T-005 Domain promotion and nightly order

- Status: Done 2026-11-10 (271eb13, fixes 6f39866, 6e35f32, 5aaa31f, 3cf7774). Evidence: `go test ./agent/knowledge/...` ok (AC-DKN-005); live smoke below found four proposal-handling bugs, all fixed with tests.
- Files: new `agent/knowledge/promote_domain.go` + test; `upkeep.go`, `promote.go`.
- Covers: REQ-DKN-031–034. Design §4.2, §4.3.
- Acceptance: AC-DKN-005.

### T-006 Tools and turn

- Status: Done 2026-11-10 (0a0676d skill_write + OwnerTurn, b2c7717 knowledge tools + recall, 9eeafa8 supersede guidance). Evidence: `go test ./tools/... ./agent/` ok (AC-DKN-002, -003, -007).
- Files: `tools/knowledge.go`, `tools/skills.go` + tests; `agent/turn.go` (recall scopes,
  `OwnerTurn`) + test.
- Covers: REQ-DKN-010–012, -020–021, -025, -050, -052. Design §5, §8. API-DKN-001.
- Acceptance: AC-DKN-002, -003 (tool part), -007.

## Wave 5

### T-007 Runtime and web API

- Status: Done 2026-11-10 (dd78d4b, 78f3439). Evidence: `go test . ./web/... ./cmd/...` ok; handler tests for API-DKN-003–008; `lana_review_test.go`.
- Files: `lana.go`, `web/knowledge.go`, `web/overview.go`, `cmd/lana/daemon.go` (or the web
  bridge file) + tests.
- Covers: REQ-DKN-060, -061, API-DKN-003–008. Design §9.
- Acceptance: AC-DKN-008 (Go part).

### T-008 Web UI

- Status: Done 2026-11-10 (e59a876, cada1bf). Evidence: vitest 560/560; screenshots 1440 and 390 px on the smoke daemon.
- Files: `web/ui/src/api/knowledge.ts`, `views/knowledge.tsx`, `views/agent-overview.tsx`, the
  sidebar component, styles, tests; `web/dist`.
- Covers: REQ-DKN-070–072. Design §10.
- Acceptance: AC-DKN-008 (vitest part).

## Wave 6

### T-009 Live smoke

- Status: Done 2026-11-10. Evidence below.
- Scratch `LANA_HOME`, real model, two agents with jobs; evidence recorded below.

## Live smoke 2026-11-10

Scratch `LANA_HOME`, real DeepSeek (`deepseek/deepseek-flash`), agents `dana` (payroll) and `rio`
(tax), each with `JOB.md` and knowledge tools; daemon on port 18399; API over curl; Chrome
headless screenshots through CDP.

- `dana` wrote `agents/dana/wiki/Kebijakan Penggajian.md` (scope dana); `rio` wrote its own page.
- `dana` asked to note something for `rio` → proposal `p_…` to `rio`, `owner_turn = 1`, no file
  change; `rio` → `dana` THR proposal likewise.
- A changed fact (BPJS 4% → 5%, transport 500k → 600k) kept the old line struck through and dated.
  The model first hand-wrote the strikethrough with `replace`; after the guidance fix (9eeafa8) it
  called `supersede`, then restyled the line in the page's own words.
- `skill_write` stored `agents/dana/proposed-skills/cek-payroll-bulanan`; `POST /v1/review/s_dana.cek-payroll-bulanan/approve`
  → `applied`, skill moved to `agents/dana/skills/`; a second approve → 404.
- Consolidation: 5 sessions journaled into `agents/<agent>/journal/2026-11-10.md`; domain promotion
  ran per agent with its own model; log lines show applied and skipped ops with reasons.
- Recall: `dana`'s `<context>` listed its domain page; `rio`'s had no `relevant knowledge` line
  for the same topic (rio still found dana's page through an explicit `knowledge_search scope=all`,
  as REQ-DKN-010 allows).
- API: `/v1/knowledge` scopes and `review_pending`, `/v1/knowledge/pages?scope=dana`, a bad scope
  and a `..` page path → 400, `/v1/agents/dana/overview` knowledge block.
- Bugs found and fixed: a proposal marked applied while its op was skipped (6f39866); a create op
  without `type` failed and the listed proposal was still marked applied (6e35f32); status counts
  ignored domains (78f3439); owner-approved proposals still needed 0.8 (5aaa31f); proposals reached
  the model as one cut line, so a fact was lost and a create became an empty page (3cf7774).
- After the fixes: an owner-approved `dana` → `rio` proposal became a full `agents/rio/wiki/PPh 21 atas THR.md`
  page and the proposal `applied`.
- Not checked live: the Claude Code runtime path (its tool calls do not set `OwnerTurn`, so its
  proposals always wait for the owner); Telegram; MEMORY.md pruning with a real model (unit tests
  only); a nightly run by the clock.

## Live test 2026-11-10 (round 2, owner: "ikuti rekomendasi mu, dan test live")

- Claude Code path: an agent on `claude-code/sonnet` proposed to `dana` through the loopback MCP
  tools; the proposal had `owner_turn = 1` after 29df2f0.
- Nightly by the clock: `consolidate_at` set three minutes ahead; the run fired one tick after the
  time and journaled, promoted, and linted.
- MEMORY.md pruning with a real model: the first run pruned four entries that no page held (text
  kept only in `log.md`). Fixed in 96a99dd: an entry is removed only when 60% of its words are in
  the text the passing ops wrote. Rerun: three entries kept and logged ("not found in written
  pages"), the pruned ones were present in `Kebijakan Penggajian`. The model's `update` op now maps
  to `append`.
- Copy of the owner's real `~/.lana` (real sessions and vault; knowledge tools added to `lana`;
  port 18399): index migrated v2 → v3 without vault changes; `lana` (combo `claude-code/sonnet`)
  wrote `agents/lana/wiki/lana-harness.md`; a limited consolidation (3 newest sessions) journaled
  into `agents/lana/journal/` and promoted one page; profile "What lana knows" and the Knowledge
  scope filter rendered. Found and fixed a stale scope count after promotion (b781709).
- Backlog note: adding knowledge tools to `lana` makes its 25 older sessions (~889k characters)
  eligible for journaling; the first nightly run reads them 40,000 characters per session per run
  with the agent's own model.

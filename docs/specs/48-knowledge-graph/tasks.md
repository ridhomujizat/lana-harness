# Tasks: Knowledge graph and tabbed Knowledge page

- **Status**: Completed
- **Revision**: 1

Go tasks run through `dsh-coder`; the web UI is coded directly. Test command per Go task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./<affected>/...`; full suite
(`go test -race -count=1 ./...`, `golangci-lint run`) after each wave.

## Wave 1

### T-001 Link index and graph queries (`internal/knowledge`)

- Status: Done 2026-11-11 (fd5db62). Evidence: `go test ./internal/knowledge/...` ok; ParseLinks forms, sync/edit/delete, v3→v4 backfill with unchanged chunk and vector counts, graph and page-link cases.
- Files: new `links.go` + `links_test.go`; `index.go` (schema v4, syncOne, deleteFile, backfill)
  + tests.
- Covers: REQ-KGR-001–003, -010–012 (index part), -020 (links part). Design §1.
- Acceptance: AC-KGR-001, AC-KGR-002 (index part), AC-KGR-003 (index part).

## Wave 2

### T-002 Service, runtime, and web API

- Status: Done 2026-11-11 (4652c66). Evidence: activity and health tests on the real log and lint formats, minute-tie paging, handler tests with 400 `invalid_request`, decided review and `old_text`.
- Files: `core/knowledge.go`; `agent/knowledge/service.go`, new `activity.go` + tests; `lana.go`;
  `web/knowledge.go` + tests; `cmd/lana/web.go`, `cmd/lana/daemon.go`.
- Covers: REQ-KGR-010–012, -020, -030, -040, -041. API-KGR-001–005. Design §2, §3.
- Acceptance: AC-KGR-002–005.

## Wave 3

### T-003 Web UI

- Status: Done 2026-11-11 (072bb86, 2b9f1a0, smoke fixes). Evidence: vitest 570/570 (17 knowledge cases); force-graph 1.52.0 as a lazy chunk; `web/dist` rebuilt.
- Files: `web/ui/package.json`, `api/knowledge.ts`, `views/knowledge*.tsx`, styles, tests,
  `web/dist`.
- Covers: REQ-KGR-050–056. Design §4.
- Acceptance: AC-KGR-006.

## Wave 4

### T-004 Live smoke

- Status: Done 2026-11-11. Evidence below.
- Scratch `LANA_HOME` with two agents and cross links, and a copy of the owner's `~/.lana`;
  screenshots 1440 and 390 px, light and dark.

## Live smoke 2026-11-11

Scratch `LANA_HOME` built from the owner's real vault and `knowledge.db` (schema 3, no `lana.db`,
port 18399), plus seeded domain pages for `dana` and `karin` and shared pages with cross links.

- The index migrated 3 → 4 on start and backfilled 44 links; no reindex of chunks or vectors.
- `GET /v1/knowledge/graph`: 4 hubs (dana, karin, lana, shared), 18 pages, 18 links; the broken
  count listed only real misses after seed journals were added.
- Page detail: links out, `unresolved: ["Slip Gaji"]`, three backlinks with context.
- Activity: `limit=2` returned six entries sharing one minute and a `next_before`; review
  `status=decided` returned `[]`, `status=x` 400.
- Screenshots (headless Chrome over CDP) at 1440 and 390 px, light and dark: graph, graph with a
  selected page, pages reader, review, activity.
- Fixed during the smoke: separate clusters drifted apart (gentle centre gravity added); journal
  and inbox targets listed under "Links to" (now under "Learned from"); raw `[[ ]]` in backlink
  context; timeline run entries took the summary card style (class clash); empty
  "proposals applied:" lines; bottom sheet height on phones.
- Not checked live: a real cross-agent proposal in the new Review cards (unit tests only; the
  proposal flow itself was live-tested in spec 46).

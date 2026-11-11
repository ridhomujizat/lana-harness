# Tasks: Knowledge graph and tabbed Knowledge page

- **Status**: In progress
- **Revision**: 1

Go tasks run through `dsh-coder`; the web UI is coded directly. Test command per Go task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./<affected>/...`; full suite
(`go test -race -count=1 ./...`, `golangci-lint run`) after each wave.

## Wave 1

### T-001 Link index and graph queries (`internal/knowledge`)

- Status: Pending
- Files: new `links.go` + `links_test.go`; `index.go` (schema v4, syncOne, deleteFile, backfill)
  + tests.
- Covers: REQ-KGR-001–003, -010–012 (index part), -020 (links part). Design §1.
- Acceptance: AC-KGR-001, AC-KGR-002 (index part), AC-KGR-003 (index part).

## Wave 2

### T-002 Service, runtime, and web API

- Status: Pending
- Files: `core/knowledge.go`; `agent/knowledge/service.go`, new `activity.go` + tests; `lana.go`;
  `web/knowledge.go` + tests; `cmd/lana/web.go`, `cmd/lana/daemon.go`.
- Covers: REQ-KGR-010–012, -020, -030, -040, -041. API-KGR-001–005. Design §2, §3.
- Acceptance: AC-KGR-002–005.

## Wave 3

### T-003 Web UI

- Status: Pending
- Files: `web/ui/package.json`, `api/knowledge.ts`, `views/knowledge*.tsx`, styles, tests,
  `web/dist`.
- Covers: REQ-KGR-050–056. Design §4.
- Acceptance: AC-KGR-006.

## Wave 4

### T-004 Live smoke

- Status: Pending
- Scratch `LANA_HOME` with two agents and cross links, and a copy of the owner's `~/.lana`;
  screenshots 1440 and 390 px, light and dark.

# Tasks: Monitoring dashboard

- **Status**: In Progress

Runs in parallel with spec 27 (trajectory). Spec 27 owns `web/ui/src/views/session.tsx`,
`web/ui/src/app/App.tsx`, and new `web/ui/src/views/trajectory*` and `styles/trajectory.css`.
These tasks must not touch them.

## Wave 1 (parallel, disjoint files)

### T-001 Backend: scan, stats, log buffer, endpoint

- Status: Done 2026-11-01. Evidence: `go build`, `go vet`, `go test -race ./...`, golangci-lint (0 issues)
  pass. Live API on a copy of the owner's DB: `24h`/`7d`/`30d` return 200 with the API-DSH-001 shape;
  `range=bad` returns 400 `invalid_request`. Review fix: a cancelled `assistant/attempt` is not a retry.
- Files: `core/session.go`, `store/store.go`, `store/memstore/memstore.go`, `lana.go`, new
  `internal/stats/*`, new `internal/logring/*`, new `web/dashboard.go`, `web/dashboard_test.go`,
  `web/sessions.go` (`SetBackend` only), `web/server.go` (`routes()` only), `cmd/lana/daemon.go`,
  `cmd/lana/web.go`, their tests.
- Covers: REQ-DSH-002–009 (data), API-DSH-001, AC-DSH-001–003. Design §2–§6.

### T-002 Web UI dashboard

- Status: Done 2026-11-01. Evidence: typecheck and lint clean; `home.test.tsx` 10/10 pass. Review fix: only the
  latest request applies its result (range-switch race), and expanded error rows use stable keys.
- Files: new `web/ui/src/api/dashboard.ts`, `web/ui/src/views/home.tsx`, `web/ui/src/views/home.test.tsx`,
  new `web/ui/src/styles/dashboard.css`, `web/ui/src/app/Sidebar.tsx` (+ its test if one asserts
  the label).
- Covers: REQ-DSH-001–010 (UI), AC-DSH-004–006. Design §7. Codes against API-DSH-001 with a mocked
  client; does not need T-001 to run.

## Wave 2 (main session)

### T-003 Rebuild `web/dist`, run full checks, verify in the browser

- Status: In Progress. `web/dist` rebuilt and full Go and UI checks pass on 2026-11-01. Browser check by the
  owner is pending.

## Wave 3 (revision 2: agent filter; parallel, disjoint files)

### T-004 Backend agent filter

- Status: Done 2026-11-01. Evidence: `go test -race ./...` all ok, golangci-lint 0 issues.
- Files: `internal/stats/stats.go`, `internal/stats/stats_test.go`, `web/dashboard.go`,
  `web/dashboard_test.go`, `cmd/lana/web.go`, `cmd/lana/web_dashboard_test.go`.
- Covers: REQ-DSH-011, API-DSH-001 r2, AC-DSH-007.

### T-005 UI agent filter

- Status: Done 2026-11-01. Evidence: typecheck and lint clean, vitest 27 files / 174 tests pass;
  `web/dist` rebuilt; daemon restarted on the new build.
- Files: `web/ui/src/api/dashboard.ts`, `web/ui/src/views/home.tsx`, `web/ui/src/views/home.test.tsx`,
  `web/ui/src/styles/dashboard.css`.
- Covers: REQ-DSH-011, AC-DSH-008.

### T-006 List configured agents in `agents`

- Status: Done 2026-11-01 (race tests and lint ok; daemon restarted). Owner report: the select showed only `lana` on 24h because `default`
  had no events in the last 24 hours.
- Files: `cmd/lana/web.go`, `cmd/lana/web_dashboard_test.go`.
- Covers: API-DSH-001 r2 `agents` rule, REQ-DSH-011.

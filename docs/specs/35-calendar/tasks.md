# Tasks: Calendar

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Design**: [design.md](design.md)
- **Requirements revision**: 1
- **Contract revision**: 1
- **Design revision**: 1
- **Status**: Completed

## Execution rules

- Owner order (2026-11-05): spec first, then the web UI. The web UI is built against the contract with preview
  fixtures and the workflow fallback, before the Go backend exists.
- `web/ui` is coded in the main session. Go goes through Opus subagents while dsh is paused (memory
  `dsh-paused-opus-subagents`), else `dsh-coder` per CLAUDE.md.
- Waves (backend, later): T-001 → (T-002 ‖ T-003) → T-004 → (T-005 ‖ T-006 ‖ T-007) → T-008 → T-009.

## Coverage

| Acceptance criterion | Evidence owner | Verification |
|---|---|---|
| `AC-CAL-001`, `AC-CAL-002` | `T-002` | `go test ./internal/calendar -run TestValidate` |
| `AC-CAL-003`, `AC-CAL-008`, `AC-CAL-009`, `AC-CAL-015` | `T-002` | `go test ./internal/calendar -run TestEntries` |
| `AC-CAL-004` | `T-005` | `go test ./web -run TestCalendar` |
| `AC-CAL-005` | `T-002` | `go test ./internal/calendar -run TestExpand` |
| `AC-CAL-006`, `AC-CAL-007` | `T-004` | `go test ./internal/calendar ./internal/workflow -run TestSlot` |
| `AC-CAL-010`–`AC-CAL-014`, `AC-CAL-019` | `T-004` | `go test ./internal/calendar -run TestScheduler` |
| `AC-CAL-016` | `T-006` | `go test ./tools -run TestCalendarTool` |
| `AC-CAL-017`, `AC-CAL-018`, `AC-CAL-024` | `T-011` | `npx vitest run src/views/calendar` |
| `AC-CAL-020`–`AC-CAL-022` | `T-007` | `go test ./internal/calendar -run TestGoogle` |
| `AC-CAL-023` | `T-005` | `go test ./internal/calendar ./web -run TestICS` |

## Tasks

### Web UI (now)

- [x] `T-010` API types + model + fallback — `api/calendar.ts`, `views/calendar-model.ts`, `views/calendar-fallback.ts`
  - Evidence: `npx vitest run src/views/calendar-model` 13 pass (ranges, lanes, moves, AC-CAL-015 fallback) · 2026-11-05
- [x] `T-011` Calendar page: views, popover, editor, settings drawer, drag and drop, stream, CSS, sidebar + route,
  preview fixtures, vitest
  - Evidence: `npx vitest run src/views/calendar` 6 pass (AC-CAL-017 drag → override, AC-CAL-018 keyboard Move…,
    AC-CAL-024 stream reload, create task, fallback on 404) · full suite 277 pass · `tsc --noEmit` and `eslint .` clean
  - Note: days and times are laid out in the browser's zone; the settings drawer says so when it differs from the
    calendar zone.
- [x] `T-012` Build `web/dist`, preview screenshots (light/dark, each view)
  - Evidence: `make web-check` ok after `npm run build`; headless Chrome shots of Week, Month, Day (dark), Agenda
    (dark), entry popover, drag result, item editor, settings drawer and phone width against `preview.html`.

### Backend

Built 2026-11-05 by Opus subagents (dsh paused), reviewed and gated in the main session.

- [x] `T-001` Model, Store, MemStore, migration `0007_calendar.sql`, SQLite store — `internal/calendar/{model,store,memstore}.go`,
  `store/migrations/0007_calendar.sql`, `store/calendar.go`
  - Evidence: `go test -race ./store ./internal/calendar` pass (CAS race test, v6→v7 migration test)
- [x] `T-002` Validate, expand, entries builder, agenda text, ICS writer + tests — `internal/calendar/{validate,expand,entries,agenda,ics}.go`
  - Evidence: AC-CAL-001/002/003/005/008/009/015 tests, ICS golden `testdata/feed.ics`
- [x] `T-003` Workflow hook — `internal/workflow/slots.go`: `SetSlotFilter`, `Slots(from, to)`; run trigger `calendar`
  - Evidence: `TestSlotFilterSchedule`, `TestSlotFilterInterval`, `TestSlots`, `TestStartRunCalendarTrigger`
- [x] `T-004` Service + scheduler — `internal/calendar/{deps,service,scheduler}.go`
  - Evidence: AC-CAL-006/007/010–014/019 tests with a fake clock
- [x] `T-005` Web handlers, ICS feed, SSE — `web/calendar.go` (+ `web/server.go` registration)
  - Evidence: `go test -race ./web` (AC-CAL-004, AC-CAL-023, 404 without backend, OAuth state flow)
- [x] `T-006` Agent tool `calendar` (auto-offered to every agent, `disabled_tools` removes it), runtime glue, Telegram
  `/agenda` + notices — `tools/calendar.go`, `agent/tooloffers.go`, `lana_calendar.go`, `cmd/lana/calendar_notify.go`
  - Evidence: AC-CAL-016 test, `TestTurnOffersCalendarTool`, `/agenda` routing test
- [x] `T-007` Google OAuth (PKCE) + two-way sync — `internal/calendar/google.go`, secrets in `secrets.env`
  (`CALENDAR_GOOGLE_*`)
  - Evidence: AC-CAL-020/021/022 tests against a fake Google API (sync tokens, 410, If-Match, 412 LWW, invalid_grant)
  - Not exercised live: needs the owner's OAuth client.
- [x] `T-008` Wiring `cmd/lana` + live smoke
  - Evidence (2026-11-05, LANA_HOME copy, port 18399, Telegram off, real DeepSeek): every `/v1/calendar/*` route answered
    as in the contract (range_too_large 400, invalid_item details, not_repeating 400, invalid_settings 400, delete → 404);
    ICS 200 `text/calendar` with 94 VEVENTs without a cookie and 404 for a wrong token; a schedule workflow showed three
    07:00 slots; a `send_message` reminder failed with "telegram is not connected" (Telegram off, as expected); an
    `agent_prompt` reminder ran one lana turn that called the `calendar` tool and created "Smoke from agent" for tomorrow
    with `created_by: agent:lana`; the real web UI showed all of it. `gofmt`, `go vet ./...`, `go test -race ./...`,
    `golangci-lint run ./...` clean.
- [ ] `T-009` Keep the web fallback for older daemons (no code change needed).

### Known gaps (follow-ups)

- Moving or skipping one occurrence of a Google-synced repeating item is not written to Google as an instance exception.
- A cached Google event edited in lana does not become a lana item yet (Google events stay read-only in the web UI).
- `send_message` with target `web` falls back to Telegram; the web has no notice channel yet.
- Not exercised live: Telegram delivery and `/agenda`, Google OAuth, the daily agenda at 07:00.

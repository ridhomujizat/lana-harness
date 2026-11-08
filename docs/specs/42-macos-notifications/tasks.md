# Tasks: macOS notifications

- **Status**: Completed 2026-11-08 · branch `wip/mac-notify` (not committed)
- **Requirements**: [requirements.md](requirements.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-NTF-01 | `internal/notify`: bus (ring of 200 / 1 h, resume, dedupe keys, presence with 45 s TTL), event builders, markdown excerpt | REQ-NTF-002..005 | `go test ./internal/notify` (bus, resume, limits, presence, excerpt, mapping) |
| T-NTF-02 | Root package `lana_notify.go`: `Runtime.Notifications`, `StartNotifications`; reply feeder on the session event hook, workflow run notifier on the workflow change stream | REQ-NTF-002, 003 | `go test . -run 'Notif\|ReplyFeeder\|WorkflowRunNotifier'`: web reply excerpt, visible-session and Telegram suppression, cancelled/empty/child/workflow/library turns, run start/done/failed/waiting/decided, resumed runs |
| T-NTF-03 | `web`: broker observers (approvals, questions) publish asked and closed; `GET /v1/local/events`, `POST /v1/local/approvals/{id}`, `POST /v1/sessions/{id}/presence` | API-NTF-001..003 | `go test ./web -run 'Local\|Presence\|Question\|Approval'`: 401/403/405/503, stream + resume, local answer through the web broker end to end, workflow session url, presence + CSRF |
| T-NTF-04 | `cmd/lana`: `SetNotifications` + `StartNotifications` in the daemon | REQ-NTF-001 | end to end below |
| T-NTF-05 | UI: `api/presence.ts`, `lib/session-presence.ts` hook in `session.tsx` and the room timeline | REQ-NTF-005 | vitest 432/432 (presence hook: visible, repeat, hidden, blur, unmount, session switch), `tsc`, eslint, `make web-check` |
| T-NTF-06 | Swift `LanaMenuCore`: `LanaEvent`, `NotificationSettings`, `planNotification`, SSE parser, `EventSubscription` (backoff, resume, dedupe), local approval request | REQ-NTF-006, 007 | `make macos-app-test`: 34 tests (mapping, filters, preview, removal, sounds, SSE, backoff, requests) |
| T-NTF-07 | Swift `LanaMenu`: `Notifier` (permission, categories, post, remove, click, actions), Settings section, `--dump-status`, `--simulate-event`, `--watch-events` | REQ-NTF-006, 007, API-NTF-004 | `make macos-app` |
| T-NTF-08 | End to end: this branch's daemon in a temp home on 127.0.0.1:18431 with a fake OpenAI-compatible model; `LanaMenu --dump-status` shows `subscribed`; `--watch-events --answer allowed_once` received reply, approval asked → answered → decided, reply after the tool, no reply while presence visible, workflow started → waiting → answered (approve) → decided → done, failing workflow started → failed; `curl ?after=` replayed | AC-NTF-001..003 | see the run log in the task report |
| T-NTF-09 | Docs: README "macOS menu bar", CHANGELOG Unreleased, this spec, index row | — | — |

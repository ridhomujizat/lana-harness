# Requirements: macOS notifications

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner request; the details are agent recommendations, approved under the
  owner's "atur sesuai rekomendasi kamu")
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file ([Contract](#contract)) — two daemon-only local endpoints, one
  dashboard endpoint, the event shape, and the app's flags
- **Related ADR**: [0013](../../adr/README.md) (the lana binary stays CGO-free)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "sekarang notif ke macos jika ada pesan, atau confirm dan menjalankan workflow. untuk pesan munculkan span di deskripsi, atur sesuai rekomendasi kamu." | goal: replies, approvals, workflow runs; reply text in the banner |
| Spec 41 | [macOS menu bar app](../41-macos-menubar/requirements.md) | the app that posts the banners, `daemon.secret` |
| Spec 18 | [friendly web login](../18-friendly-web-login/requirements.md) API-WLG-008 | local secret and loopback checks |
| Spec 33 | [workflows](../33-workflows/requirements.md) | run lifecycle, approval decisions |

## Goal

When lana needs the owner or has news, macOS says so: an agent reply with an excerpt of its text, a
tool approval with Allow once and Reject buttons, an ask_user question, and workflow runs that start,
finish, fail, or wait for approval. A click opens the right dashboard page.

## Requirements

### REQ-NTF-001: The menu bar app posts, the daemon publishes

The menu bar app (spec #41) shall post every notification with `UNUserNotificationCenter`. It asks
for permission on its first launch. The daemon never posts a notification; it publishes events on an
in-process bus that `GET /v1/local/events` streams. The Go build stays CGO-free.

### REQ-NTF-002: Kinds

| Event | Title | Body | Behavior |
|---|---|---|---|
| Agent reply (turn end with assistant text) | `<agent> · <session title or "New chat">` | excerpt of the final assistant text: markdown removed (code fences → `[code]`, links → their text, heading → "Heading:"), one line, ≤ 140 characters ending in `…` | click opens the session; thread = session id |
| Approval asked (answerable through the web broker: channels web and workflow) | `Approval needed · <agent>` | `<tool>: <primary argument>` (toolfmt.PrimaryArg) ≤ 120 | actions **Allow once**, **Reject**; click opens the session (or the workflow); time sensitive; removed when it is decided anywhere |
| Question asked (ask_user on the web broker) | `Question from <agent>` | the first question ≤ 140, `(+N more)` | click opens the session; removed when answered or closed |
| Workflow run started | `Workflow started · <name>` | `Trigger: <manual / schedule / calendar / webhook …>` | no sound |
| Workflow run succeeded | `Workflow done · <name> · <duration>` | run summary, else "Run #N succeeded" | click opens the run |
| Workflow run failed | `Workflow failed · <name>` | error excerpt ≤ 140 | sound; click opens the run |
| Workflow waiting for an approval decision | `Approval needed · <name>` | the decision message | Allow once / Reject (approve / deny); a guardian decision has no buttons |

- `AC-NTF-001`: A web turn that ends with text publishes one reply event whose body is the excerpt.
- `AC-NTF-002`: An approval of a web session publishes `approval_asked` with the broker id; the
  local answer resolves the same broker request and publishes `approval_decided`.

### REQ-NTF-003: Rules

- A reply does not notify while a dashboard tab shows that session (REQ-NTF-005).
- Replies notify only for root sessions on channel `web`. Telegram replies already reach Telegram;
  cli, library, workflow, and subagent sessions do not notify a reply. Approvals and questions of a
  Telegram session are answered in Telegram and never reach the web broker, so they do not notify
  either; those of web and workflow sessions do.
- A cancelled turn and a turn without text do not notify.
- A runtime used as a library, a test, or a background pass (titles, memory, knowledge) never starts
  the feeders, so it publishes nothing.
- Every event has a unique id; the bus drops a second publish of the same occurrence (reply per turn,
  approval per request, run start per run …); the app drops a repeated id.
- Banners group by `thread`: the session id, or `workflow:<id>:<run>`.

### REQ-NTF-004: Event bus

The bus keeps the last 200 events of the last hour. A subscriber that reconnects with
`?after=<id>` gets the events after that id; an id of another daemon process gets everything
buffered; no `after` starts live.

### REQ-NTF-005: Presence

The session page and the chat room page report `POST /v1/sessions/{id}/presence {"visible":bool}`
when the tab becomes visible and focused, when it is hidden or loses focus, on leave, and every 30 s
while visible. The daemon keeps a visible report for 45 s.

- `AC-NTF-003`: After a visible report, the next reply of that session publishes nothing; 45 s
  without a report or a hidden report ends that.

### REQ-NTF-006: App settings

Settings shall show toggles Messages, Show message preview, Approvals, Questions, Workflow started,
and Workflow finished or failed — all on by default (Workflow started banners are silent). Preview
off shows "New message" instead of the reply or question text. Settings shows the permission state
with **Open System Settings…** (Notifications → lana) and the daemon event connection state.

### REQ-NTF-007: App subscription

While the daemon runs, the app follows `GET /v1/local/events` with the secret from `daemon.secret`,
reconnects with backoff (1, 2, 4, 8, 16, then 30 s), resumes with `after=`, and waits for
`daemon.secret` while the daemon is stopped. A click opens `http://<addr>/<url>`; Allow once and
Reject call `POST /v1/local/approvals/{id}`; `approval_decided` and `question_answered` remove the
banner.

## Contract

### API-NTF-001: `GET /v1/local/events`

Daemon-only, same checks as API-WLG-008: `X-Lana-Local-Secret` must equal `daemon.secret` (401
`unauthorized`), the caller must be loopback (403 `forbidden_remote`), only GET (405). 503
`unavailable` without a bus. Query `after` (or `Last-Event-ID`). The stream starts with
`event: ready` `data: {"last_id": "<id or empty>"}`, then the replay, then live events, each as

```
id: <id>
event: notify
data: {"id","kind","at","session_id?","agent?","title","body","url","approval_id?","question_id?",
       "workflow_id?","workflow_run_id?","outcome?","silent?","time_sensitive?","thread"}
```

with a `: ping` comment every 20 s and `event: shutdown` when the daemon stops. `kind` is one of
`reply`, `approval_asked`, `approval_decided`, `question_asked`, `question_answered`,
`workflow_started`, `workflow_finished`, `workflow_failed`, `workflow_waiting`. `url` is a dashboard
hash: `#/sessions/<id>`, `#/workflows/<id>`, `#/workflows/<id>/runs/<n>`. `id` is
`<boot>-<seq>`.

### API-NTF-002: `POST /v1/local/approvals/{id}`

Same checks as API-NTF-001, only POST. Body `{"outcome":"allowed_once"|"rejected"}` (else 400). A
broker id answers through the same broker as `POST /v1/approvals/{id}`; `wf:<workflow>:<decision>`
answers a workflow approval decision with approve or deny. 200 `{"outcome"}`, 404 `not_found`, 409
`approval_closed` (or `not_approvable` for a guardian decision). The daemon log records
`source=macos`; the session log has no answer source.

### API-NTF-003: `POST /v1/sessions/{id}/presence`

Browser session and CSRF like every `/v1` route. Body `{"visible": true|false}` (400 without it).
204.

### API-NTF-004: App flags (extends API-MAC-003)

| Flag | Effect |
|---|---|
| `--dump-status` | adds `notifications`: `permission`, `subscription` (a one-shot connect to API-NTF-001), `settings` |
| `--simulate-event JSON` | prints the plan for one event JSON and posts it (needs Lana.app and permission; never prompts) |
| `--watch-events [--seconds N] [--answer allowed_once\|rejected]` | follows API-NTF-001 and prints each event with its plan as JSON lines; `--answer` answers approvals like the buttons |

## Not included

- A time-sensitive entitlement: the ad-hoc signed app asks for time-sensitive delivery, but macOS
  only honors it for apps with the entitlement, so Focus modes may hold approval banners.
- Answering a question or giving a reason from the banner (the click opens the dashboard).
- Notifications for calendar items and the daily agenda (they go to Telegram, spec #35).
- Persisting the bus across daemon restarts.

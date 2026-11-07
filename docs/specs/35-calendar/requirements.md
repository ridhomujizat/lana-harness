# Requirements: Calendar

- **Issue**: `—`
- **Status**: Approved
- **Revision**: 1
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: [ADR Index](../../adr/README.md) — `—`
- **Related diagram**: [Diagram index](../../diagram/README.md) — `—`

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner interview | Chat, 2026-11-05 (memory `calendar-feature-decisions`) | Item types, actions, agent rights, views, missed policy, Google, ICS, Telegram, sidebar |
| Owner decision | Chat, 2026-11-05: "tulis spec dulu lalu implementasi FEnya" | Spec first, then the web UI; no figcraft step |
| External reference | `github.com/gr8rstudio/gr8r-studio` `src/views/calendar.js`, `src/styles/views/calendar.css` | Month and week layout, chips, cards, "+" per day, drag to move |
| Existing specs | [#33 workflows](../33-workflows/requirements.md), [#32 rooms](../32-agent-rooms/requirements.md), [#6 Telegram](../6-daemon-telegram/requirements.md), [#9 web](../9-web-settings-chat/requirements.md) | Schedule triggers, runs, rooms, owner chat, web auth |

> **Gate lifted (2026-11-05).** The owner answered the interview and asked for the spec and then the web UI.
> Details the interview did not cover are resolved with `Rekomendasi agent:` in the resolution table and
> marked Approved on behalf of the owner. The owner can change them through a new revision.

## Goal

The owner sees, in one calendar, everything that happens on a schedule: what workflows will run, what
they already ran, and the owner's own events, tasks, reminders, notes and habits. The calendar is also a
scheduler: an item can run something when it is due. Agents use the same calendar as the owner and can
add or change items themselves.

## Actors

- **Owner** — signed in to the web UI or talking in the Telegram owner chat. Full rights.
- **Agent** — any lana agent, through the `calendar` tool. Same rights as the owner on calendar items
  (owner decision: "Selalu bebas"). An agent cannot change workflow definitions through the calendar.
- **Google Calendar** — an external system linked by the owner. Two-way sync.
- **ICS subscriber** — any calendar app that holds the secret feed URL. Read only.

## Definitions

- **Item** — a calendar record stored by lana. Kinds: `event`, `task`, `reminder`, `note`, `habit`.
- **Occurrence** — one dated instance of an item. A non-repeating item has one occurrence. A repeating
  item has one occurrence per matching date.
- **Action** — what lana runs when an occurrence is due: `agent_prompt`, `run_workflow` or `send_message`.
- **Workflow slot** — a future time at which an active workflow's schedule or interval trigger fires.
  It is computed, not stored.
- **Workflow run entry** — a past or current workflow run shown at its start time.
- **Override** — a stored change to one occurrence of a repeating item or one workflow slot: moved to
  another time, or skipped.
- **Entry** — anything the calendar shows: an item occurrence, a workflow slot, a workflow run entry or a
  Google event.
- **Missed** — an occurrence with an action whose due time passed while lana could not run it.
- **Calendar time zone** — the IANA zone used to lay out days and expand repeats. Default: the daemon
  host's local zone.

## Scope

### Included

- Items of the five kinds, with optional repeat, assignee and action.
- Habits with a daily checkbox and a streak.
- Workflow slots and workflow run history on the calendar.
- Moving or skipping one workflow slot without changing the workflow's cron.
- Moving or skipping one occurrence of a repeating item.
- Actions that run when due, and the missed rule.
- Web views: Month, Week, Day (hourly timeline) and Agenda (list).
- The agent `calendar` tool.
- Daily agenda message in the Telegram owner chat and the `/agenda` command.
- Two-way Google Calendar sync for `event` and `reminder` items.
- A read-only ICS feed with a secret URL.
- Sidebar entry "Calendar" after "Workflows".

### Not included

- Running a missed action late on its own. The owner runs it by hand (REQ-CAL-007).
- Google Tasks. Tasks, notes and habits are not synced to Google.
- Inviting other people, attendees, RSVP, video links.
- Several owners or shared calendars between lana installs.
- Changing a workflow's cron from the calendar. The workflow editor stays the only place.
- Background jobs, knowledge upkeep and chat activity on the calendar (owner decision).
- Writing through the ICS feed (CalDAV).
- Repeat rules beyond REQ-CAL-003 (no "second Tuesday", no exceptions lists other than overrides).

## Assumptions

- Workflow runs and schedule triggers exist as in spec #33 (`trigger.schedule` with cron + time zone,
  `trigger.interval` with minutes). — Source: `internal/workflow`.
- The web server binds to loopback. The Google OAuth redirect uses the loopback address; an ICS subscriber
  outside the machine needs a tunnel, as with webhooks. — Source: spec #9, spec #33.
- An agent turn can be started for a named agent's room from inside the daemon. — Source: spec #32.
- The Telegram owner chat can receive a plain message from the daemon. — Source: spec #6.

## Behavior rules

### REQ-CAL-001: Items

The system shall store items with: id, kind, title (1–200 characters), notes (up to 10 000 characters),
time, repeat, assignee, action, color, created by, updated by and timestamps.

- `event`: start and end time, or an all-day date range.
- `task`: a due date, optionally with a due time; state `open` or `done`.
- `reminder`: one time.
- `note`: one date, no time.
- `habit`: a repeat of `daily` or `weekly` on chosen weekdays, and an optional time of day.
- Assignee: `owner` or an agent name. Default `owner`.
- Created by / updated by: `owner`, `agent:<name>` or `google`.

#### Acceptance criteria

- `AC-CAL-001`: Given a valid item of each kind, when it is created, then it is returned with an id,
  `created_by` set to the caller, and it appears in the entries of its date range.
- `AC-CAL-002`: Given an event whose end is before its start, or a habit without a repeat, when it is
  saved, then the save fails with `invalid_item` and nothing is stored.

### REQ-CAL-002: Entries for a range

The system shall return, for a time range of at most 92 days, every entry that overlaps it: item
occurrences (with overrides applied), workflow slots, workflow run entries and Google events not already
linked to an item. Entries are sorted by start time, then by title.

#### Acceptance criteria

- `AC-CAL-003`: Given a daily habit, a workflow with cron `0 7 * * *` that is active, and one finished run
  of it, when the entries for one week are read, then the result has 7 habit occurrences, 7 workflow slots
  in the future part of the week only, and 1 run entry at the run's start time.
- `AC-CAL-004`: Given a range longer than 92 days, when entries are read, then the call fails with
  `range_too_large`.

### REQ-CAL-003: Repeat

The system shall support these repeats for every kind except `note`: none, `daily`, `weekdays`
(Mon–Fri), `weekly` on one or more weekdays, `monthly` on the start date's day of month, and `yearly`.
A repeat may end on a date. Repeats expand in the calendar time zone. A monthly repeat on day 29–31
skips months without that day.

#### Acceptance criteria

- `AC-CAL-005`: Given a weekly repeat on Mon and Thu that ends on a Thursday, when that month is
  expanded, then occurrences fall only on Mondays and Thursdays up to and including the end date.

### REQ-CAL-004: Overrides

The system shall let the owner or an agent move one occurrence to another time, or skip it, for a
repeating item and for a workflow slot. A moved workflow slot fires at the new time and not at the old
one. The workflow's cron does not change. Removing an override restores the computed time.

#### Acceptance criteria

- `AC-CAL-006`: Given a workflow slot at 07:00 moved to 09:30 on one day, when the scheduler reaches
  07:00 that day, then no run starts; at 09:30 one run starts with trigger `calendar`; the next day the
  slot is at 07:00 again.
- `AC-CAL-007`: Given a skipped workflow slot, when its time passes, then no run starts and the entry
  shows `skipped`.

### REQ-CAL-005: Tasks and habits

The system shall let the owner or an agent mark a task done or open, and mark one habit occurrence done
or not done. The streak of a habit is the number of consecutive scheduled occurrences marked done,
counted back from today; if today is not yet done, counting starts from the previous scheduled
occurrence.

#### Acceptance criteria

- `AC-CAL-008`: Given a daily habit done on the last 4 days and not yet today, when entries are read
  today, then the habit's streak is 4; after today is ticked, it is 5.
- `AC-CAL-009`: Given a weekly habit on Mon/Wed/Fri done on the last 3 scheduled days, when entries are
  read on Tuesday, then the streak is 3 (Tuesday is not a scheduled day).

### REQ-CAL-006: Actions

The system shall run an item's action once per occurrence when the occurrence is due:

- `agent_prompt` (agent, prompt): the named agent runs one turn with the prompt in its room. The turn
  uses that agent's own tools and permissions.
- `run_workflow` (workflow id, optional JSON input): one run of the workflow with trigger `calendar`.
  The workflow's permission lock applies.
- `send_message` (text, target `telegram` or `web`): the text is sent to the owner, with no model call.

Each occurrence records a firing: `scheduled`, `running`, `done`, `failed` or `missed`, with a link to the
session or run and the error text if any. A firing runs at most once, also across a daemon restart.

#### Acceptance criteria

- `AC-CAL-010`: Given a reminder with a `send_message` action due at 10:00, when 10:00 passes while the
  daemon runs, then one message is sent and the firing is `done`.
- `AC-CAL-011`: Given an item with `run_workflow` for a paused workflow, when it is due, then no run
  starts, the firing is `failed` with the reason, and the owner is told in the Telegram owner chat.
- `AC-CAL-012`: Given a firing that is `running` when the daemon stops, when the daemon starts again, then
  the action is not started a second time.

### REQ-CAL-007: Missed

The system shall mark a firing `missed`, and never run it on its own, when its due time is more than
5 minutes in the past at the moment lana first sees it (for example after the daemon was stopped). The
owner is told once per daemon start with the list of missed firings. The owner can run a missed firing
by hand or dismiss it.

#### Acceptance criteria

- `AC-CAL-013`: Given the daemon is stopped from 09:00 to 11:00 and an action was due at 10:00, when it
  starts at 11:00, then the firing is `missed`, no action runs, and one Telegram message lists it.
- `AC-CAL-014`: Given a missed firing, when the owner presses Run now, then the action runs once and the
  firing becomes `running`, then `done` or `failed`.

### REQ-CAL-008: Workflow entries

The system shall show:

- for each active workflow with an enabled schedule trigger, one slot per fire time in the future part
  of the range;
- for an interval trigger of 60 minutes or more, one slot per fire time; for a shorter interval, one
  all-day entry per day "Every N min";
- one run entry per run that started in the range, with its status, number and a link to the run.

A slot whose fire time has passed is replaced by its run entry, or shown as `skipped` if overridden.

#### Acceptance criteria

- `AC-CAL-015`: Given an active workflow with a 5-minute interval, when a day is read, then the day has
  one entry "Every 5 min" for it and not 288 slots.

### REQ-CAL-009: Agent tool

The system shall give agents a `calendar` tool with operations: list entries for a range, get, create,
update, delete an item, move or skip an occurrence or a workflow slot, complete a task, tick a habit, and
run a missed firing. Changes made through the tool carry `agent:<name>` in `created_by` / `updated_by`.
The tool's risk is low and it needs no approval.

#### Acceptance criteria

- `AC-CAL-016`: Given agent Dana, when Dana creates a task through the tool, then the task shows
  "Added by Dana" in the web UI and in the agenda message.

### REQ-CAL-010: Web views

The system shall offer, under `#/calendar`, the views Month, Week, Day and Agenda, a Today button,
previous/next navigation, filters by source (my items, agents' items, workflows, Google) and by kind, and
a "New" menu with the five kinds. The chosen view and date stay in the URL. Each entry opens a detail
popover with its actions. Dragging an item or a workflow slot to another day (Month, Week) or time (Day,
Week) moves it; a repeating item asks "This one / All" before moving.

#### Acceptance criteria

- `AC-CAL-017`: Given the Week view, when the owner drags a workflow slot from Tuesday 07:00 to Wednesday
  09:00, then an override is saved and the slot shows on Wednesday with a "moved" mark.
- `AC-CAL-018`: Given any view, when the owner uses only the keyboard, then every entry, the "New" menu,
  navigation and the view switch can be reached and opened, and moving is possible from the popover.

### REQ-CAL-011: Telegram

The system shall send, when enabled, a daily agenda to the Telegram owner chat at a set time (default
07:00 calendar time): today's events, tasks due, reminders, habits with their streaks, workflow slots and
yesterday's failed or missed firings. The `/agenda` command sends the same for today on demand. Adding an
item from Telegram happens by talking to an agent, which uses the `calendar` tool.

#### Acceptance criteria

- `AC-CAL-019`: Given the daily agenda is enabled at 07:00, when 07:00 passes, then one agenda message is
  sent that day, even if the daemon restarted after 07:00 and before 07:05.

### REQ-CAL-012: Google Calendar

The system shall link one Google account through OAuth, chosen by the owner, and sync two ways:

- Google events from the calendars the owner selects appear as entries.
- An event created, changed or deleted in lana on a synced calendar is written to Google.
- A Google-side change updates the linked lana item. When both sides changed since the last sync, the
  later `updated` time wins and the loser is kept in the item's history.
- Only `event` and `reminder` items sync. An action on a lana item stays in lana; it is not written to
  Google.
- New lana events go to one target calendar chosen by the owner.

#### Acceptance criteria

- `AC-CAL-020`: Given a linked account, when an event is created in Google, then it appears in lana within
  one sync interval (default 5 minutes) or right after "Sync now".
- `AC-CAL-021`: Given a lana event linked to Google, when it is moved in lana, then the Google event has the
  new time after the next push (within 1 minute).
- `AC-CAL-022`: Given Google returns an auth error, when sync runs, then the link state becomes
  `needs_reauth`, the web UI shows it, and lana items are not deleted.

### REQ-CAL-013: ICS feed

The system shall serve a read-only ICS feed at a secret URL with the items and workflow slots from 30 days
back to 90 days ahead. The owner can rotate the secret; the old URL then returns 404.

#### Acceptance criteria

- `AC-CAL-023`: Given the feed URL, when it is fetched without a session cookie, then it returns
  `text/calendar` with one VEVENT per occurrence; with a wrong token it returns 404.

### REQ-CAL-014: Live updates

The system shall notify open web pages when entries change (item saved, firing state, run state,
override, sync), so the calendar refreshes without reload.

#### Acceptance criteria

- `AC-CAL-024`: Given the calendar is open, when an agent adds an item, then it appears within 2 seconds.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Item fails validation | Not saved; field errors returned | `invalid_item` |
| Range over 92 days | Rejected | `range_too_large` |
| Item or occurrence not found | Rejected | `not_found` |
| Action target agent or workflow deleted | Firing `failed`, owner told | — |
| Action due while daemon down > 5 min | Firing `missed`, owner told once | — |
| Google auth revoked | Link `needs_reauth`; local data kept | `google_reauth` |
| Google write fails (network) | Retried with backoff; item shows "not synced" | — |
| ICS token wrong | 404, no body detail | — |
| Calendar backend absent (older daemon) | Web shows workflow slots and runs only, with a notice | `not_found` on `/v1/calendar` |

## Authorization and security

- All `/v1/calendar/*` calls use the web session and CSRF rules of spec #9.
- The ICS feed authenticates by a 32-byte random token in the path; it is compared in constant time.
- Google OAuth client id/secret and the refresh token live in the secret store and never reach a model
  prompt or the browser.
- An `agent_prompt` action runs with the target agent's permissions; the calendar adds no rights.
  `send_message` can only reach the owner.

## Data integrity and concurrency

- A firing is keyed by (item id, occurrence time). It moves `scheduled → running → done|failed` or
  `scheduled → missed → running → done|failed` once. A unique key stops a second start.
- An override is keyed by (source, ref, original time). Saving it twice keeps one row.
- Google sync writes use the event's etag; a mismatch re-reads and applies REQ-CAL-012's rule.

## Accessibility

- Month and Week grids are tables of buttons with labels that include date, time, kind and title.
- Color is never the only signal: status and kind also show an icon or word.
- Drag has a keyboard path: the popover's "Move…" opens a date/time field.

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Grace before an action counts as missed | REQ-CAL-007 | — | ridho | Resolved | Rekomendasi agent: 5 minutes. |
| `Q-002` | Google conflict rule | REQ-CAL-012 | — | ridho | Resolved | Rekomendasi agent: later `updated` wins; loser kept in history (shown to owner 2026-11-05). |
| `Q-003` | Agent rights for actions | REQ-CAL-006 | — | ridho | Resolved | Rekomendasi agent: the target agent's own permissions apply (shown to owner 2026-11-05). |
| `Q-004` | Which kinds sync to Google | REQ-CAL-012 | — | ridho | Resolved | Rekomendasi agent: `event` and `reminder` only. |
| `Q-005` | Short intervals on the calendar | REQ-CAL-008 | — | ridho | Resolved | Rekomendasi agent: under 60 min → one "Every N min" entry per day. |
| `Q-006` | Keep the Observe → Schedules page | REQ-CAL-010 | — | ridho | Resolved | Rekomendasi agent: keep it for now (trigger list incl. webhooks/events); revisit after Calendar ships. |

None open.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-CAL-001` | `AC-CAL-001`, `AC-CAL-002` | interview |
| `REQ-CAL-002` | `AC-CAL-003`, `AC-CAL-004` | interview |
| `REQ-CAL-003` | `AC-CAL-005` | Rekomendasi agent |
| `REQ-CAL-004` | `AC-CAL-006`, `AC-CAL-007` | interview (drag = one occurrence) |
| `REQ-CAL-005` | `AC-CAL-008`, `AC-CAL-009` | interview (habits) |
| `REQ-CAL-006` | `AC-CAL-010`–`AC-CAL-012` | interview (actions) |
| `REQ-CAL-007` | `AC-CAL-013`, `AC-CAL-014` | interview (always missed) |
| `REQ-CAL-008` | `AC-CAL-015` | interview |
| `REQ-CAL-009` | `AC-CAL-016` | interview (agents free) |
| `REQ-CAL-010` | `AC-CAL-017`, `AC-CAL-018` | interview, gr8r reference |
| `REQ-CAL-011` | `AC-CAL-019` | interview |
| `REQ-CAL-012` | `AC-CAL-020`–`AC-CAL-022` | interview (two-way) |
| `REQ-CAL-013` | `AC-CAL-023` | interview |
| `REQ-CAL-014` | `AC-CAL-024` | Rekomendasi agent |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit.
- [x] State transitions and destructive effects are explicit.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: ridho (on behalf, gate lifted 2026-11-05)
- **Approved on**: 2026-11-05

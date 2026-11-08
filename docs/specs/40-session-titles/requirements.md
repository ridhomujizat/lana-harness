# Requirements: Session titles

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner request; the design details are agent recommendations, approved under
  the owner's standing "ikuti rekomendasimu")
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file ([Contract](#contract)) — the change is one PATCH field, one endpoint, one
  stream event, and one column
- **Related ADR**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | History shows "Untitled session"; "bisa rename … diisi oleh agent turn, atau best practice lain" | goal, rename, automatic titles |
| Spec 20 | [memory capture](../20-memory-capture/requirements.md) | background pass after a turn, never delays it |
| Spec 39 | [Claude Code target](../39-claude-code-target/requirements.md) REQ-CCT-007 | background chain without Claude Code |
| Spec 37 | [LLM router](../37-llm-router/requirements.md) REQ-RTR-009 | call records with a source |

## Goal

Every session in History has a short, readable name. lana names a session by itself after its first
turn, the owner can rename it, and a session lana starts on its own (a workflow run, a calendar item)
says where it came from.

## Agent recommendations (approved)

| Topic | Recommendation |
|---|---|
| When to name | After the first completed root turn with an owner message, in the background, like memory capture |
| Which model | The agent's background chain (the turn chain without Claude Code, else `[router] background_model`), so naming never spends a Claude Code turn |
| Without a model | The first owner message, cut to 50 characters at a word boundary with "…" |
| Library channel | Not named automatically: the embedding program owns those sessions |
| Rename | Click the title; Enter saves, Escape cancels, an empty title goes back to an automatic one |
| Live update | The session stream sends a `title` event; the History list re-reads rows that are being named |
| Forks | A fork copies its parent's title and source |

## Behavior rules

### REQ-TTL-001: Automatic title after the first turn

After a root turn that ended `end_turn`, `max_steps`, or `loop` and stored an owner message, lana shall
start one background title pass for the session when the session has no title source yet. The pass
shall not delay the turn, shall not run twice at once for one session, and shall stop with the daemon.
Web, CLI, and Telegram sessions are named; `library` sessions are not.

- `AC-TTL-001`: Given a new web session, when its first turn ends, then within a few seconds the session
  has a title with source `auto`, and the turn result came back before the title call.
- `AC-TTL-002`: Given a library session, when a turn ends, then no title call is made.

### REQ-TTL-002: Background chain and call record

The pass shall use the agent's background chain through `internal/llmcall` (`BackgroundChain` /
`AgentBackgroundChain`), never a Claude Code target, through the router with call-record source
`title`, max tokens 48, reasoning effort `off`, and no tools. The prompt carries the first owner
message and the reply to it, each cut to 2,000 characters; injected `<context>` blocks and runtime
notes are left out.

- `AC-TTL-003`: Given a chain `claude-code/opus, ds/chat`, then the title call goes to `ds/chat` with
  source `title`.

### REQ-TTL-003: Title shape

The title shall be 3–6 words in the conversation's language, at most 60 characters, without quotes,
markdown, a "Title:" prefix, `<think>` blocks, or a trailing period; only the first non-empty line
of the reply is used.

- `AC-TTL-004`: Given the reply `"Deploy lana to the VPS."`, then the title is `Deploy lana to the VPS`.

### REQ-TTL-004: Fallback title

When no background model is available (an agent with only Claude Code and an empty
`[router] background_model`), or the call fails or answers nothing usable, the title shall be the first
owner message on one line, cut at a word boundary to at most 50 characters with "…".

- `AC-TTL-005`: Given an agent with only `claude-code/opus` and no background model, then the session
  gets the fallback title and no model call is made.

### REQ-TTL-005: Title source

`sessions.title_source` shall be `''` (no title yet), `auto`, `owner`, or `system`. Migration 0009 adds
the column; existing non-empty titles become `auto`.

### REQ-TTL-006: The owner title wins

A pass shall never overwrite a title that exists when it writes, the owner's included, even one set
while the call ran. Only an explicit regenerate (REQ-TTL-010) replaces an owner title.

- `AC-TTL-006`: Given the owner renames a session while its title call runs, then the owner title
  stays.

### REQ-TTL-007: Fixed titles for sessions lana starts

A workflow run session shall be created with the title `Workflow · <workflow name>` (the id when the
name is unknown) and a session a calendar item creates with `Calendar · <item title>`, both with source
`system` and no model call. A calendar item that reuses the agent's room session keeps that session's
title.

### REQ-TTL-008: Rename

`PATCH /v1/sessions/{id}` with `title` shall collapse whitespace runs (newlines included) to one space,
trim, and store a non-empty title with source `owner`. More than 80 characters is `400
invalid_request`. A rename does not change `updated_at`.

### REQ-TTL-009: Back to an automatic title

An empty `title` shall clear the owner title and queue a fresh automatic title.

### REQ-TTL-010: Regenerate

`POST /v1/sessions/{id}/title/regenerate` shall name the session now, replacing any title (the owner
asked explicitly), and answer the session with the new title. It answers `409 title_busy` while a pass
runs for the session and `409 nothing_to_title` when the session has no owner message.

### REQ-TTL-011: Live update

The session stream shall send a `title` event when the title, its source, or the pending flag changes.
An open session page passes the change to the History list in the same tab.

### REQ-TTL-012: Pending state

Session views shall carry `title_pending` while a pass runs in the daemon. The History list shows a
soft "Naming…" shimmer while the first title is being made and re-reads such rows every 2.5 s, at
most 24 times; "Untitled session" shows only while nothing exists yet.

### REQ-TTL-013: Dashboard

The History list shows the title, a pencil button on hover or keyboard focus for inline rename, and a
row menu with **Rename** and **Regenerate title**; touch screens always show the menu button. The
session header title is click-to-edit. Everything works with the keyboard and on phones.

## Contract

| Surface | Change |
|---|---|
| Session JSON (list and detail) | `title_source` (`""`, `auto`, `owner`, `system`), `title_pending` (bool) |
| `PATCH /v1/sessions/{id}` | `title` (string, ≤ 80 characters after trimming; `""` = automatic) |
| `POST /v1/sessions/{id}/title/regenerate` | `200` session detail; `409 title_busy`; `409 nothing_to_title`; `404 not_found` |
| `GET /v1/sessions/{id}/stream` | event `title`: `{"title", "title_source", "title_pending"}` |
| SQLite | migration `0009_session_title.sql`: `sessions.title_source` with a CHECK |
| Router call records | source `title` |

## Out of scope

- Titles for child (subagent) sessions.
- Renaming from the CLI or Telegram.

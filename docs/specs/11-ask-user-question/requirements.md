# Requirements: Ask-user questions

- **Issue**: `—`
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`ask_user` tool input and output, question events, `question` SSE event, `POST /v1/questions/{request_id}`, Telegram and CLI texts)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0004](../../adr/0004-permission-mode-per-agent.md) (responder chain, fail-closed outcomes, audit pair; this spec copies its routing), [0008](../../adr/0008-cli-channel-http-and-web-dashboard-interfaces.md) (web dashboard, SSE), [0009](../../adr/0009-session-as-append-only-event-log.md) (event log, resume), [0016](../../adr/0016-owner-auth-for-browser.md) (owner login and CSRF for the POST endpoint)
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md) (agent loop and approval)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner instruction 2026-10-30 | Brief "Spec #11 ask-user questions" and the mockup in `design/` | scope: tool, routing, card, timeout |
| Mockup | `design/export/screens/sessions-question.layout.txt`, `sessions-question-multi-select.layout.txt`, and their PNG renders | card layout, texts, keys |
| Mockup components | `design/export/components/components.layout.txt`, `design/export/tokens.css`, `design/export/icons/` | tokens and icons |
| ADR 0004 | Policy, responder chain, fail-closed outcomes, audit pair, `unavailable` for missing responder | routing model (copied) |
| ADR 0009 | Append-only event log, derived status, resume closes orphans | events, status, resume |
| ADR 0008 and spec #9 | Web view, SSE, loopback, Preact with no build step | web surface |
| Spec #9 `REQ-WEB-011` to `REQ-WEB-016` | Inline approval flow, first answer wins, `409` on later answers, cancel | question broker behavior (same pattern) |
| Spec #9 decision 6 (rev 2) | Session status `waiting_input` | session list status |
| Spec #6 | Telegram owner rules, approval keyboard, `REQ-TG-004` inbox | Telegram responder |
| Code (read 2026-10-30) | `core/approval.go`, `core/tool.go`, `core/event.go`, `tools/registry.go`, `tools/bash.go`, `agent/turn.go`, `store/store.go`, `tools/permission/`, `cmd/lana/daemon.go`, `web/approvals.go`, `web/sse.go`, `channels/telegram/approve.go`, `channels/cli/tui/approver.go`, `lana.go`, `lana_channel.go` | feasibility and gaps, listed in `design.md` |

Anything not supported by a source or an explicit approval goes into Blocking Open Questions. The owner lifted the approval gate on 2026-10-30. Every question in this file was answered with the agent's recommendation, marked `Rekomendasi agent:`.

## Goal

When an agent needs a decision from its owner, it can ask a short multiple-choice question in the channel the
conversation already uses. The owner answers in the browser, in Telegram, or in the terminal, and the agent continues
with the answer. If nobody can answer, the agent continues without it and says so, instead of waiting forever.

## Actors

- **Owner** — the one person who uses the daemon. Answers questions in the web view, in the owner's Telegram chat, or in `lana chat`. Owner rights match the existing approval rights.
- **Agent model** — calls the `ask_user` tool. It receives the answers, or an `unavailable` result, as the tool output. It has no other way to read the answers.
- **Channel responder** — the code that shows a question and collects the answer: the web question broker, the Telegram channel, or the CLI TUI. Each responder answers only for sessions of its own channel.
- **Daemon and runtime** — own the session log, the session status, and the timeout. They decide the final outcome if no responder answers.

## Definitions

- **Question request** — one call of `ask_user`. It holds one to three questions. It is open from the moment its `question/asked` event is written until its `question/answered` event is written.
- **Question** — one item of a request. It has a title, an optional short header, a single-select or multi-select flag, and two to six options.
- **Option** — a labelled choice with an optional description. Options are numbered from 1 in the order given.
- **Something else** — a free-text answer the owner types instead of picking an option. It is always offered and is not an option of the model.
- **Skip** — the owner does not answer one question. It is recorded as `skipped` for that question.
- **Skip all** — the owner closes the whole card (the X button). Every question of the request is recorded as skipped.
- **Submission** — the one answer message that closes a request. The owner's answers are collected per question and sent once, after the last question is answered or skipped.
- **Responder** — the channel code that asks the owner and returns the answer (web, Telegram, CLI). A responder that cannot ask returns `unavailable`.
- **Unavailable** — the request ends without answers. The reasons are `no_responder`, `timeout`, `cancelled`, `interrupted`.

## Scope

### Included

- A built-in tool `ask_user` for the model, with its input limits and its output text.
- Routing of a request to the responder of its session's channel, copied from the approval routing of ADR 0004.
- Responders for the web view (card, broker, endpoint, SSE event), Telegram (inline keyboard), and the CLI TUI (numbered prompt).
- The `waiting_input` session status while a request is open.
- The events `question/asked` and `question/answered` in the session log, and their replay rules at resume.
- A 30-minute timeout for every request.
- The rules that let the library and `lana run` continue without a responder.

### Not included

- Questions with free-form input only (no options). Every question has at least two options.
- File or image attachments in questions or answers.
- Questions raised by subagent child sessions (spec #10). Their routing will follow the approval routing of ADR 0011 when spec #10 is built. A later revision of this spec defines it.
- A configurable timeout. The timeout is 30 minutes in v1.
- Editing a submitted answer. An answer is final.
- Push notifications when a question opens while the web view is closed.
- The attach-file control in the composer, which the mockup shows as coming soon.

## Assumptions

- The web view runs on loopback with the owner login of spec #9 (`REQ-WEB-005`). Source: spec #9, ADR 0016.
- A Telegram session has one owner chat, and the owner is the only person who may answer. Source: spec #6.
- The tool-runner gives the tool a context that is cancelled when the turn is cancelled. Source: `tools/permission/approval.go`, `agent/turn.go`.
- The mockup's `Something else`, `Skip`, `Next`, and `Submit` controls are the only controls of the card. Source: `design/export/screens/sessions-question*.layout.txt`.

## Behavior rules

### Tool

#### REQ-ASK-001: The ask_user tool

The system shall provide the built-in tool `ask_user` in the tool group `Chat`, with risk `read`. The tool shall never need an approval. An agent can call it only when its `tools` list contains `ask_user`. The default templates shall include it.

- `AC-ASK-001`: Given an agent whose `tools` list contains `ask_user`, when a turn calls it with valid input under policy `trust`, `ask`, and `never`, then no `approval/asked` event is written and the question request opens.
- `AC-ASK-002`: Given an agent whose `tools` list does not contain `ask_user`, when the model calls it, then the result is the existing error `tool is not enabled for agent`, and no question event is written.

#### REQ-ASK-002: Input limits

The system shall accept one to three questions per call. Each question shall have a non-empty title, an optional header, an optional `multi_select` flag (default `false`), and two to six options. Each option shall have a non-empty label and an optional description. Labels shall be unique within one question. Unknown fields shall be rejected. The limits are in the contract. An invalid input shall return a tool error and shall write no question event.

- `AC-ASK-003`: Given a call with four questions, when the tool runs, then the result is an `invalid input` error and the log has no `question/asked` event.
- `AC-ASK-004`: Given a question with two options that have the same label, when the tool runs, then the result is an `invalid input` error.
- `AC-ASK-005`: Given a call with one question and two options, when the tool runs, then one question request opens.

#### REQ-ASK-003: Routing by session channel

The system shall send a request to the responder of the session's channel. A `web` session goes to the web question broker. A `telegram` session goes to the owner's Telegram chat of that session. A `cli` session goes to the CLI TUI prompt when the TUI is running. Any other channel, a missing responder, or a responder that fails shall end the request as `unavailable` with reason `no_responder` at once. The request shall never be shown to a different channel.

- `AC-ASK-006`: Given a session on channel `telegram` with a chat, when a question is asked, then the question is shown in that chat and no `question` SSE event is published for it.
- `AC-ASK-007`: Given a runtime without a question responder, when `ask_user` is called, then the call returns `unavailable` with reason `no_responder` without waiting.

#### REQ-ASK-004: One open request per session

The system shall allow at most one open question request per session. A second `ask_user` call in the same session while a request is open shall return a tool error at once: `a question is already open in this session`. It shall write no question event.

- `AC-ASK-008`: Given request `R1` is open in session `S`, when a second `ask_user` call starts in `S`, then the second call returns the error at once, and the log of `S` has exactly one `question/asked`.

### Answers

#### REQ-ASK-005: Recording answers

The system shall record for each question one of three answers: selected options, skipped, or `Something else` text. A single-select question records exactly one option. A multi-select question records one or more options. A multi-select question may also record `Something else` text next to its options. The system shall submit the answers of a request once, after every question is answered or skipped. A single-select answer records the choice and moves to the next question. On the last question, the choice also submits the request. A multi-select question moves on with Next and submits with Submit on the last question.

- `AC-ASK-009`: Given a request with three questions, when the owner answers the first two and skips the third, then exactly one submission is accepted, and it contains three entries in question order.
- `AC-ASK-010`: Given a multi-select question with no option selected and no `Something else` text, when the owner looks at Next, then Next is disabled, and Skip still records the question as skipped.

#### REQ-ASK-006: Result text for the model

The system shall give the model a JSON text as the tool output. An answered or skipped request shall return `status` `answered` with one entry per question. An unanswered request shall return `status` `unavailable` with a `reason`. The tool status shall be `success` in both cases, so the turn continues. The output format is in the contract.

- `AC-ASK-011`: Given answers for two questions, when the tool returns, then the output parses as JSON with `status` `answered`, `answers` in question order, `other` only when the owner typed text, and `skipped: true` without `selected` for a skipped question.
- `AC-ASK-012`: Given no responder, a timeout, or a cancelled turn, when the tool returns, then the output is `{"status":"unavailable","reason":...}` with the matching reason and the tool status is `success`.

#### REQ-ASK-007: Timeout

The system shall end every open request 30 minutes after it opens if no submission was accepted. The request shall end as `unavailable` with reason `timeout`. Answers that were collected but not submitted shall be discarded. The responder shall close its question UI and say that the question expired.

- `AC-ASK-013`: Given a request open for 30 minutes with two of three questions answered in the card and no submission, when the timeout passes, then the log has `question/answered` with outcome `unavailable` and reason `timeout`, the tool returns `unavailable`, and the card shows `Question expired`.

#### REQ-ASK-008: Cancel, shutdown, and resume

The system shall end an open request with outcome `cancelled` when its turn is cancelled. When the daemon shuts down, the open request shall end as `cancelled` too, under the shutdown rules of spec #9. When a session with an open request without `question/answered` is opened again, the system shall append `question/answered` with outcome `unavailable` and reason `interrupted`, as ADR 0009 does for open tool calls.

- `AC-ASK-014`: Given an open request, when the owner presses Cancel, then the log has `question/answered` with outcome `cancelled`, the turn ends with stop reason `cancelled`, and the card closes with the text `Question cancelled`.
- `AC-ASK-015`: Given a log whose last open request has `question/asked` and no `question/answered`, when the session is opened, then the store appends `question/answered` with outcome `unavailable` and reason `interrupted`, and the session status becomes `idle`.

### Session status and composer

#### REQ-ASK-009: Status while a request is open

The system shall set the session status to `waiting_input` when `question/asked` is written, and back to `running` when `question/answered` is written. While the status is `waiting_input`, the session is still in a running turn. Every check that asks whether a turn is running shall treat `waiting_input` as running. Messages sent to the session shall go to the session inbox, as during a running turn.

- `AC-ASK-016`: Given a request is open, when the session is listed, then its status is `waiting_input`; after the request is answered, the status is `running`.
- `AC-ASK-017`: Given a request is open, when a new message is sent to the session, then a second turn does not start, and the message is queued in the inbox of the session (`REQ-WEB-011` for the web view).

### Audit

#### REQ-ASK-010: Events in the log

The system shall write `question/asked` when a request opens and `question/answered` when it ends. `question/asked` shall hold the request ID, the call ID, and the expiry time. It shall not copy the question text, because the question is already in the `tool/call` event (ADR 0004 rule for approvals). `question/answered` shall hold the request ID, the call ID, the outcome, the responder (`web`, `telegram`, `cli`, or `none`), the reason when there is one, and the answers when the outcome is `answered`.

- `AC-ASK-018`: Given a question is answered in the browser, then the log has exactly one `question/asked` and one `question/answered`, the `responder` is `web`, and the `question/asked` payload contains no question text.

### Web view

#### REQ-ASK-011: Web question broker and endpoint

The system shall publish each request of a web session as one `question` SSE event to the open streams of that session. The event carries the request ID, the questions, and the expiry time. A stream that connects while a request is open shall receive the event again. The first accepted submission wins. A later submission for the same request shall get `409 question_closed`. An unknown request ID shall get `404 not_found`. An invalid body shall get `422 invalid_answers` and the request stays open. A closed request ID shall be remembered for one hour.

- `AC-ASK-019`: Given two browser tabs show the same request, when tab A submits and then tab B submits, then A gets `200`, B gets `409 question_closed`, and the log has one `question/answered`.
- `AC-ASK-020`: Given a submission that selects option 9 of a three-option question, when it is posted, then the response is `422 invalid_answers`, the request stays open, and the card stays in place.
- `AC-ASK-021`: Given a request is open and the owner's stream reconnects, when the stream opens, then it receives a `question` event for the open request.

#### REQ-ASK-012: Card layout

The web view shall show the open request as a card in the messages column, directly above the composer, following the mockup `sessions-question` and `sessions-question-multi-select`. The card shows the title, a `Select all that apply` line for multi-select questions, the pager (`1 of 3` with previous and next buttons), a close button, the numbered options with descriptions, the `Something else` row, a `Skip` button, and a `Next` or `Submit` button for multi-select questions. A single-select question has no `Next` button. The turn header of the turn shows `waiting for you`. The composer textarea is disabled with the placeholder `Answer the question above`, and the stop button stays enabled.

- `AC-ASK-022`: Given a three-question single-select request, when the page renders, then the card shows the title, `1 of 3`, numbered options, `Something else`, and `Skip`, and it shows no `Next` button.
- `AC-ASK-023`: Given a multi-select question with two options selected, when the page renders, then the selected options show a checked box in place of their number, the card shows `Select all that apply`, and the button reads `Next`, or `Submit` on the last question.
- `AC-ASK-024`: Given a request is open, when the page renders, then the composer textarea is disabled with the text `Answer the question above`, and the stop button is enabled.

#### REQ-ASK-013: Keyboard

Every step of the card shall work with the keyboard alone. Tab order shall be: the options, `Something else`, `Skip`, then `Next` or `Submit`. Digit keys 1 to 9 shall pick the option with that number. On a single-select question a digit picks the option and moves to the next question. On a multi-select question a digit toggles the option. Up and Down arrows shall move the focus between options. Enter shall activate the focused option or button. On `Something else`, Enter shall open the text field, Enter in the field shall keep the text, and Escape in the field shall close the field and return focus to the row. The close button shall skip all questions.

- `AC-ASK-025`: Given a two-question request with a multi-select first question, when the owner uses only digit keys and Tab, then the owner can submit the request without a pointer, and the log has one submission.

#### REQ-ASK-014: Screen reader

The card shall have an accessible name from its title. The progress shall be announced through a polite live region when the question changes, as `Question 2 of 3: <title>`. Each option shall expose its label, its description, and its state (checked or not) as native radio or checkbox controls. The result shall be announced: `Answer sent`, `Already answered elsewhere`, `Question expired`, or `Question cancelled`. No state shall be shown by colour alone.

- `AC-ASK-026`: Given a VoiceOver walk-through of a two-question request, when the owner moves through the card, then VoiceOver reads the title, the progress, each option with its description and checked state, and the result after submission.

#### REQ-ASK-015: Question from another channel

The web view shall show an open request of a session whose responder is not the web broker, such as a Telegram session or a session held by `lana chat`, read-only. The card shall say where the answer is expected: `Waiting for an answer in Telegram` or `Waiting for an answer in lana chat (PID <pid>)`. The web view shall not submit answers for such a request.

- `AC-ASK-027`: Given a Telegram session has an open request, when the owner opens the session in the web view, then the card is shown without submit controls and with the text `Waiting for an answer in Telegram`.

### Telegram

#### REQ-ASK-016: Telegram questions

The Telegram channel shall send one message per question to the chat of the session. The message shall hold the question and an inline keyboard. The owner's taps shall answer the question. Only the owner may answer, in private chats and in groups. A single-select tap records the option and sends the next question. A multi-select tap toggles the option, and the `Done` button records the selection. `Something else` shall arm a capture: the owner's next text message in that chat is the answer, and it is not added to the session inbox. `Skip` records the question as skipped. After the last question the channel submits the request and edits the question messages to show the outcome. The callback data and texts are in the contract.

- `AC-ASK-028`: Given a Telegram session with a single-select question, when the owner taps option 2, then the callback `q:<tok>:0:s:1` is accepted, the request is submitted, the message is edited to `✓ Answered: <label>`, and its keyboard is removed.
- `AC-ASK-029`: Given `Something else` was tapped in a Telegram chat, when the owner sends the text `custom wording`, then the answer records `other` = `custom wording`, and the text is not added to the session inbox.
- `AC-ASK-030`: Given a non-owner taps an option in a group chat, then the tap is answered with `only the owner can answer` and nothing is recorded.

### CLI

#### REQ-ASK-017: CLI prompt

The `lana chat` TUI shall show an open request in the transcript area as a numbered list, following the texts in the contract. Digits pick options. `s` skips the current question. `Something else` takes the number after the last option and opens the input line. On a multi-select question, digits toggle options and Enter moves on or submits. A request answered or cancelled shall leave one transcript line with the outcome.

- `AC-ASK-031`: Given `lana chat` shows a two-option single-select question, when the owner presses `2`, then the answer `option 2` is recorded, and the transcript shows `✓ answered: <label>`.

### Non-interactive runs

#### REQ-ASK-018: Runs without a responder

The system shall end a request at once with `unavailable` and reason `no_responder` when no interactive responder exists for the session. This covers `lana run` without `--ask`, cron turns, and library runtimes without a question responder. With `lana run --ask`, the request is answered on the terminal device with a numbered prompt, as approvals are.

- `AC-ASK-032`: Given `lana run` is started without `--ask` and the agent calls `ask_user`, then the run does not wait for input and the model receives `{"status":"unavailable","reason":"no_responder"}`.

### Security and integrity

#### REQ-ASK-019: Owner-only answers and safe identifiers

The system shall accept answers only from the owner's browser (with the login, the `Host` and `Origin` checks, and the CSRF header of spec #9), from the owner's Telegram chat, and from the owner's terminal. Web request IDs shall be random values of at least 256 bits. Telegram callback tokens shall be random and shall map only to open requests. Callback data and Telegram tokens shall not be written to the log or to the audit file.

- `AC-ASK-033`: Given a `POST /v1/questions/{request_id}` without the CSRF header, then the request is refused by the middleware, and the request stays open.
- `AC-ASK-034`: Given a Telegram callback whose token is from a request that is already closed, then the tap is answered with `this question is no longer open`, and nothing is recorded.

#### REQ-ASK-020: Tool line and status words

The transcript of every channel shall show the `ask_user` call as one tool line with the number of questions, for example `ask_user · 2 questions`. The status word shall be `answered` when the output status is `answered`, and `unavailable` when it is `unavailable`. The status shall be written in words, not only as an icon (`REQ-WEB-013`).

- `AC-ASK-035`: Given a request was skipped entirely through Skip all, when the transcript renders, then the tool line reads `ask_user · 2 questions` with status `answered`.

## Contract commitments

| Requirement | Observable commitment | Contract operation |
|---|---|---|
| `REQ-ASK-001`, `REQ-ASK-002` | Tool name, input limits, risk, and the tool group | `API-ASK-001` |
| `REQ-ASK-006` | Output JSON for answered and unavailable | `API-ASK-001` |
| `REQ-ASK-010` | Event kinds and payloads in the log | `API-ASK-002` |
| `REQ-ASK-003`, `REQ-ASK-009` | Go hook and runtime options for responders | `API-ASK-003` |
| `REQ-ASK-009` | Session status value `waiting_input` in listings | `API-ASK-008` |
| `REQ-ASK-011` | `question` SSE event fields and replay | `API-ASK-004` |
| `REQ-ASK-011`, `REQ-ASK-019` | `POST /v1/questions/{request_id}` body, responses, and codes | `API-ASK-005` |
| `REQ-ASK-016` | Telegram callback data and message texts | `API-ASK-006` |
| `REQ-ASK-017` | CLI prompt texts and keys | `API-ASK-007` |

## State and operation matrix

| Current state | Operation | Result | Allowed |
|---|---|---|---:|
| No request | `ask_user` call with valid input | Request opens, `question/asked`, status `waiting_input` | Yes |
| No request | `ask_user` call with invalid input | Tool error, no event | Yes (rejected) |
| Request open | Second `ask_user` call in the same session | Tool error at once, no event | Yes (rejected) |
| Request open | Submission accepted (web, Telegram, CLI) | `question/answered` (`answered`), status `running`, tool returns answers | Yes |
| Request open | Second submission | `409 question_closed` (web); `this question is no longer open` (Telegram) | Yes (rejected) |
| Request open | Timeout (30 min) | `question/answered` (`unavailable`, `timeout`), status `running` | Yes |
| Request open | Cancel the turn | `question/answered` (`cancelled`), turn ends `cancelled` | Yes |
| Request open | Daemon shutdown | Same as cancel, then the turn ends `cancelled` | Yes |
| Request open, daemon restarted | Session opened | `question/answered` (`unavailable`, `interrupted`), status `idle` | Yes |
| Request open | Message sent to the session | Queued in the inbox; no second turn | Yes |
| Request open | Turn started by a second process | Refused with `ErrTurnActive` | No |
| Request answered | Any submission | Refused as in the row above | No |

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Invalid tool input | Tool error, nothing shown to the owner | tool error `invalid input: ...` |
| Second request in an open session | Tool error at once | tool error `a question is already open in this session` |
| No responder for the channel | Model gets unavailable at once | `unavailable` / `no_responder` |
| Responder panics or returns an invalid outcome | Treated as `unavailable` (fail-closed, as approvals) | `unavailable` / `no_responder` |
| Owner submits a body with bad indexes | Request stays open; card shows an inline error | `422 invalid_answers` |
| Second submission | Not applied | `409 question_closed` |
| Unknown request ID | Not applied | `404 not_found` |
| Telegram send fails when the question opens | Request ends at once | `unavailable` / `no_responder` |
| No answer for 30 minutes | Request ends | `unavailable` / `timeout` |
| Turn cancelled or daemon stopping | Request ends | `cancelled` |
| Session re-opened after a crash | Request is closed in the log | `unavailable` / `interrupted` |

## Authorization and security

- The web endpoint uses the same owner login, `Host` and `Origin` checks, and CSRF header as the other `/v1` endpoints (spec #9, ADR 0016).
- Telegram answers come only from the owner (the existing `isOwner` rule). Group members may not answer.
- The CLI answers come from the terminal running `lana chat` or `lana run --ask`. No other process can answer.
- A request never leaves its channel. A web tab cannot answer a Telegram or CLI request (`REQ-ASK-015`).
- Question text is in `tool/call` and the card. The `question/asked` event holds no question text. The `question` SSE event holds the questions, as the approval event holds the tool argument.

## Data integrity and concurrency

- A request is closed exactly once. The first of submission, timeout, cancel, or shutdown writes `question/answered`. Later attempts see the closed request and change nothing. The write is guarded by the same lock as the open-request map.
- The tool result and `question/answered` agree. The tool returns the answers that were written to `question/answered`.
- A request that was not submitted leaves no partial answers in the log. Only the submitted answers are written.
- `question/asked` is written before the responder is called, so a crash after the write always leaves an orphan that resume can close (`REQ-ASK-008`).
- The session status projection is rebuilt from events, so `waiting_input` is never stored without its `question/asked` event.

## Accessibility

- Every card control is a native button, radio, checkbox, or text field, so focus and keyboard handling come from the browser.
- Focus order follows the visual order: options, `Something else`, `Skip`, then `Next` or `Submit`.
- The card does not take focus when it opens. Its progress and title are announced through the live region, so typing in the composer or reading the transcript is not interrupted.
- Checked state, the selected option, the outcome, and the errors are written as text, not as colour. Text meets WCAG 2.2 AA contrast in light and dark schemes (`REQ-WEB-028`).
- Keyboard shortcuts (digits, arrows) are an addition. Every action also works with a pointer through a button or a native control.

## Scenarios

### SC-001: Single-select question in the browser

**Given** agent `default` has `ask_user` enabled and the owner has a web session with it
**When** the agent asks three questions and the owner picks option 1 on each one
**Then** each pick moves to the next question, the third pick submits the request, the tool returns `answered` with three answers, and the tool line reads `ask_user · 3 questions` with status `answered` (`AC-ASK-009`, `AC-ASK-011`, `AC-ASK-016`)

### SC-002: Multi-select question with Something else

**Given** the card shows the question `Which sections should the digest have?` with multi-select
**When** the owner selects `Key findings` and `Action items`, types `Weekly trend chart` in Something else, and presses Submit
**Then** the answer has `selected` = `["Key findings","Action items"]` and `other` = `"Weekly trend chart"`, and the card shows `Answer sent` (`AC-ASK-023`, `AC-ASK-025`)

### SC-003: Nobody answers

**Given** a Telegram session has an open question
**When** the owner does not answer for 30 minutes
**Then** the chat messages show `⊘ Question expired`, the model receives `{"status":"unavailable","reason":"timeout"}`, and the agent continues with its own assumptions (`AC-ASK-013`)

### SC-004: Question in a terminal run

**Given** the owner runs `lana run` without `--ask`
**When** the agent calls `ask_user`
**Then** the run does not wait, the model receives `no_responder`, and the run ends normally (`AC-ASK-032`)

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Does `lana run --ask` answer questions on the terminal, as it does for approvals? | `REQ-ASK-018` | Brief says non-interactive `lana run` is unavailable | Ridho | Resolved | Rekomendasi agent: yes. With `--ask`, the same numbered prompt on `/dev/tty` answers questions. Without `--ask`, the request is unavailable at once. |
| `Q-002` | Which tool status do `answered` and `unavailable` results use? | `REQ-ASK-006` | — | Ridho | Resolved | Rekomendasi agent: `success` for both, so the model continues and the transcript does not show an error. The state is in the JSON `status` field. |
| `Q-003` | What happens to answers collected in the card when the timeout passes before submission? | `REQ-ASK-007` | — | Ridho | Resolved | Rekomendasi agent: discard them. The request is `unavailable` with reason `timeout`. Partial answers are never submitted. |
| `Q-004` | Does a single-select answer on the last question submit at once? | `REQ-ASK-005` | Mockup says choosing records the answer and advances | Ridho | Resolved | Rekomendasi agent: yes. On the last question, the choice submits the request, so the owner does not need an extra button. |
| `Q-005` | What are the input limits? | `REQ-ASK-002` | — | Ridho | Resolved | Rekomendasi agent: 1 to 3 questions; 2 to 6 options; question 300 characters; header 30; label 80; description 200; `Something else` text 500. Limits are in the contract. |
| `Q-006` | Do existing agents get `ask_user`? | `REQ-ASK-001` | — | Ridho | Resolved | Rekomendasi agent: no automatic change. New agents from the templates get the tool. An existing agent gets it by adding `ask_user` to its `tools` list, or from the Tools tab of spec #9 rev 2. |
| `Q-007` | Are Telegram texts sent while `Something else` waits treated as the answer or as inbox messages? | `REQ-ASK-016` | — | Ridho | Resolved | Rekomendasi agent: the answer, only while `Something else` is armed. Otherwise the text goes to the inbox as usual. Slash commands still work. |
| `Q-008` | What does Escape do in the card? | `REQ-ASK-013` | Mockup shows an X button | Ridho | Resolved | Rekomendasi agent: Escape closes the text field of `Something else` only. It never skips questions, so a stray key cannot discard answers. The X button skips all. |
| `Q-009` | How does a session row show a waiting request in the list? | `REQ-ASK-009` | Spec #9 rev 2 decision 6 | Ridho | Resolved | Rekomendasi agent: the status label `Waiting for you`, and the preview line `Question: <first question title>`. |
| `Q-010` | Does the card show a countdown to the timeout? | `REQ-ASK-007` | Mockup shows `expires in` only on the approval card | Ridho | Resolved | Rekomendasi agent: no countdown in v1. The expiry is shown after the fact. |
| `Q-011` | Where is the question `header` shown? | `REQ-ASK-016`, `REQ-ASK-017` | Mockup shows no header | Ridho | Resolved | Rekomendasi agent: the web card does not show it. Telegram and the CLI show it as a prefix `[header]` before the title. |
| `Q-012` | What happens when two requests come in one session? | `REQ-ASK-004` | — | Ridho | Resolved | Rekomendasi agent: the second call fails at once with a tool error and the model is told to combine the questions into one call. |
| `Q-013` | Are questions from another channel visible read-only in the web view? | `REQ-ASK-015` | — | Ridho | Resolved | Rekomendasi agent: yes, read-only with the channel name, so the owner knows where to answer. |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-ASK-001` | `AC-ASK-001`, `AC-ASK-002` | Brief; ADR 0004 (risk `read`) |
| `REQ-ASK-002` | `AC-ASK-003`, `AC-ASK-004`, `AC-ASK-005` | Brief; `Q-005` |
| `REQ-ASK-003` | `AC-ASK-006`, `AC-ASK-007` | Brief; ADR 0004 (responder chain) |
| `REQ-ASK-004` | `AC-ASK-008` | `Q-012` |
| `REQ-ASK-005` | `AC-ASK-009`, `AC-ASK-010` | Brief; mockup; `Q-004` |
| `REQ-ASK-006` | `AC-ASK-011`, `AC-ASK-012` | Brief; `Q-002` |
| `REQ-ASK-007` | `AC-ASK-013` | Brief (30 minutes); `Q-003` |
| `REQ-ASK-008` | `AC-ASK-014`, `AC-ASK-015` | ADR 0009 (resume); ADR 0004 (cancelled) |
| `REQ-ASK-009` | `AC-ASK-016`, `AC-ASK-017` | Brief (`waiting_input`); spec #9 decision 6 |
| `REQ-ASK-010` | `AC-ASK-018` | Brief (events); ADR 0004 (audit) |
| `REQ-ASK-011` | `AC-ASK-019`, `AC-ASK-020`, `AC-ASK-021` | Brief (SSE, POST, 409); spec #9 `REQ-WEB-014` |
| `REQ-ASK-012` | `AC-ASK-022`, `AC-ASK-023`, `AC-ASK-024` | Brief; mockup |
| `REQ-ASK-013` | `AC-ASK-025` | Brief (keys 1–9); `Q-008` |
| `REQ-ASK-014` | `AC-ASK-026` | Spec #9 `REQ-WEB-028` |
| `REQ-ASK-015` | `AC-ASK-027` | `Q-013` |
| `REQ-ASK-016` | `AC-ASK-028`, `AC-ASK-029`, `AC-ASK-030` | Brief (Telegram); spec #6 owner rules; `Q-007` |
| `REQ-ASK-017` | `AC-ASK-031` | Brief (CLI prompt) |
| `REQ-ASK-018` | `AC-ASK-032` | Brief (`lana run`); `Q-001` |
| `REQ-ASK-019` | `AC-ASK-033`, `AC-ASK-034` | Spec #9 `REQ-WEB-005`–`007`; spec #6 |
| `REQ-ASK-020` | `AC-ASK-035` | Spec #9 `REQ-WEB-013` |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Every open question is answered (`Rekomendasi agent:`).
- [x] Each requirement has at least one acceptance criterion.
- [x] Owner gate lifted on 2026-10-30 (Ridho, lifted gate 2026-10-30).
- **Approved by**: Ridho, lifted gate 2026-10-30 (agent recommendations accepted)
- **Approved on**: 2026-10-30

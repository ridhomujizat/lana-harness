# Contract: Ask-user questions

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: Tool / events / HTTP / SSE / Telegram / CLI / Go library
- **Status**: Approved

This file owns the exact form of the interface. Requirements own the behavior. Do not add behavior here that the
requirements do not state.

## Contract rules

- Only new operations are documented here. Existing `/v1` conventions are linked, not copied.
- Every operation has a stable ID `API-ASK-NNN`, and every operation is linked to `REQ-ASK-*` and `AC-ASK-*`.
- Names of the prefix `ASK` do not collide with the `API-WEB-*` operations of spec #9.

## Shared conventions

| Topic | Decision or authority |
|---|---|
| CLI exit codes | `lana run` and `lana chat` keep their existing exit codes. An unavailable question is not an error. |
| stdout and stderr output | Prompts go to the TUI or to `/dev/tty`, never to stdout of `lana run`. Answers in JSON output are not printed. |
| Config format | No new config keys in v1. The 30-minute timeout is a constant. |
| Authentication | Web: owner browser session cookie, `Host` and `Origin` checks, and the `X-Lana-CSRF` header (spec #9 `API-WEB-002`, ADR 0016). Telegram: owner user ID of the chat. CLI: the terminal. |
| Versioning | Payloads carry `v` = 1 (`core.PayloadVersion`). A new field is added without a version bump. |
| Serialization | JSON with snake_case keys. Times are Unix milliseconds. IDs are strings. Repository convention. |
| Error envelope | Web errors use the existing `{"error":{"code","message"}}` envelope of spec #9 (`writeAPIError`). |

## Operation index

| Operation ID | Type | Command/method | Path/flag/topic | Requirements | Acceptance criteria |
|---|---|---|---|---|---|
| `API-ASK-001` | Tool | model call | `ask_user` | `REQ-ASK-001`, `REQ-ASK-002`, `REQ-ASK-006` | `AC-ASK-001` to `AC-ASK-005`, `AC-ASK-011`, `AC-ASK-012` |
| `API-ASK-002` | Event | session log | `question/asked`, `question/answered` | `REQ-ASK-010`, `REQ-ASK-008` | `AC-ASK-015`, `AC-ASK-018` |
| `API-ASK-003` | Go library | `lana.AskWith`, `lana.AskChannels` | `core.Hooks.QuestionAsk` | `REQ-ASK-003`, `REQ-ASK-018` | `AC-ASK-006`, `AC-ASK-007`, `AC-ASK-032` |
| `API-ASK-004` | HTTP (SSE) | `GET` | `/v1/sessions/{id}/stream`, event `question` | `REQ-ASK-011` | `AC-ASK-019`, `AC-ASK-021` |
| `API-ASK-005` | HTTP | `POST` | `/v1/questions/{request_id}` | `REQ-ASK-011`, `REQ-ASK-019` | `AC-ASK-019`, `AC-ASK-020`, `AC-ASK-033` |
| `API-ASK-006` | Telegram | inline keyboard callback | `q:<tok>:<qi>:<act>[:<oi>]` and texts | `REQ-ASK-016`, `REQ-ASK-019` | `AC-ASK-028` to `AC-ASK-030`, `AC-ASK-034` |
| `API-ASK-007` | CLI | TUI and line prompt | `lana chat`, `lana run --ask` | `REQ-ASK-017`, `REQ-ASK-018` | `AC-ASK-031`, `AC-ASK-032` |
| `API-ASK-008` | Session status | `status` field | value `waiting_input` | `REQ-ASK-009` | `AC-ASK-016`, `AC-ASK-017` |

## API-ASK-001: `ask_user` tool

- **Goal**: Let the model ask the owner one to three multiple-choice questions and receive the answers as tool output.
- **Requirements**: `REQ-ASK-001`, `REQ-ASK-002`, `REQ-ASK-006`
- **Acceptance criteria**: `AC-ASK-001` to `AC-ASK-005`, `AC-ASK-011`, `AC-ASK-012`
- **Stability**: New
- **Authorization**: Risk `read`, so the permission engine allows the call under every policy (ADR 0004 rule 5). Deny rules still apply first. The tool needs the agent to list `ask_user` in its `tools`.

### Input

`InputSchema` (JSON Schema, `additionalProperties: false` at every level):

```json
{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array", "minItems": 1, "maxItems": 3,
      "items": {
        "type": "object",
        "properties": {
          "question": {"type": "string", "minLength": 1, "maxLength": 300},
          "header": {"type": "string", "maxLength": 30},
          "multi_select": {"type": "boolean"},
          "options": {
            "type": "array", "minItems": 2, "maxItems": 6,
            "items": {
              "type": "object",
              "properties": {
                "label": {"type": "string", "minLength": 1, "maxLength": 80},
                "description": {"type": "string", "maxLength": 200}
              },
              "required": ["label"],
              "additionalProperties": false
            }
          }
        },
        "required": ["question", "options"],
        "additionalProperties": false
      }
    }
  },
  "required": ["questions"],
  "additionalProperties": false
}
```

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Input | `questions` | array of question | Yes | 1 to 3 items |
| Input | `questions[].question` | string | Yes | 1 to 300 characters. Shown as the card title |
| Input | `questions[].header` | string | No | Up to 30 characters. Shown only in Telegram and the CLI, as a prefix `[header]` (`Q-011`) |
| Input | `questions[].multi_select` | boolean | No | Default `false` |
| Input | `questions[].options` | array of option | Yes | 2 to 6 items. Labels are unique within the question (case-sensitive) |
| Input | `options[].label` | string | Yes | 1 to 80 characters |
| Input | `options[].description` | string | No | Up to 200 characters |

Extra checks made in Go, not by the schema: unique labels, and no empty or whitespace-only title or label. A failed
check returns `invalid input: <reason>` as a tool error (`AC-ASK-003`, `AC-ASK-004`).

### Success

The tool returns `status` `success` and this text as output. Keys are in this order. `other` appears only when the owner typed
text. `selected` is absent for a skipped question.

```json
{"status":"answered","answers":[
  {"question":"Where should I save the weekly digest?","selected":["research/digests/"]},
  {"question":"Which sections should the digest have?","selected":["Key findings","Action items"],"other":"Weekly trend chart"},
  {"question":"Anything else?","skipped":true}
]}
```

| Condition | Tool status | Output |
|---|---:|---|
| Submission accepted, or Skip all | `success` | `status` `answered`, one entry per question in question order |
| No responder, timeout, cancel, or resume after crash | `success` | `{"status":"unavailable","reason":"<reason>"}` where reason is `no_responder`, `timeout`, `cancelled`, or `interrupted` |

### Failure

| Condition | Tool status | Output text | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Invalid input (schema or Go check) | `error` | `invalid input: <reason>` | No | No event written |
| Tool not in the agent's `tools` | `error` | `tool is not enabled for agent` | No | Existing runner behavior |
| A request is already open in the session | `error` | `a question is already open in this session` | Yes, after the first request ends | No event written |

### Side effects and guarantees

- **State transition**: opens a request (`waiting_input`), or ends at once with `no_responder` (both events written).
- **Idempotency**: Not applicable. Each call is a new request with a new request ID.
- **Concurrency**: At most one open request per session (`REQ-ASK-004`).
- **Data exposure**: The questions are in `tool/call` as usual. The output contains the owner's answers, which are in the log in `tool/result` and `question/answered`.

### Compatibility

- **Breaking change**: No
- **Deprecation**: Not applicable

## API-ASK-002: Question events in the session log

- **Goal**: Make every question request reconstructable from the log, as ADR 0004 does for approvals.
- **Requirements**: `REQ-ASK-010`, `REQ-ASK-008`
- **Acceptance criteria**: `AC-ASK-015`, `AC-ASK-018`
- **Stability**: New
- **Authorization**: Written only by the question service. Not writable from outside.

### Input

Event kinds `question/asked` (`core.KindQuestionAsked`) and `question/answered` (`core.KindQuestionAnswered`). Both are
added to the store's list of valid kinds.

`question/asked`:

| Name | Type | Required | Meaning |
|---|---|---:|---|
| `v` | int | Yes | `1` |
| `request_id` | string | Yes | Random ID of the request (`API-ASK-004` and `API-ASK-005` use it) |
| `call_id` | string | Yes | Call ID of the `tool/call` that opened the request |
| `expires_at` | int | Yes | Unix ms, open time plus 30 minutes |

No question text is written here (`REQ-ASK-010`). The question is in `tool/call`.

`question/answered`:

| Name | Type | Required | Meaning |
|---|---|---:|---|
| `v` | int | Yes | `1` |
| `request_id` | string | Yes | Same as `question/asked` |
| `call_id` | string | Yes | Same as `question/asked` |
| `outcome` | string | Yes | `answered`, `unavailable`, or `cancelled` |
| `responder` | string | Yes | `web`, `telegram`, `cli`, or `none` |
| `reason` | string | No | `timeout`, `no_responder`, `cancelled`, `interrupted`. Present when outcome is not `answered` |
| `answers` | array | No | Present when outcome is `answered`. Same entry shape as `API-ASK-001` output |

Go types are `core.QuestionAsked` and `core.QuestionAnswered`, with the JSON tags above.

### Success

| Condition | Events written | Session status after |
|---|---|---|
| Request opened | `question/asked` | `waiting_input` |
| Submission accepted | `question/answered` (`answered`) | `running` |
| Timeout, cancel, or no responder | `question/answered` (`unavailable` or `cancelled`) | `running` (cancelled: turn ends) |
| Resume after crash | `question/answered` (`unavailable`, `interrupted`) | `idle` after the turn closes |

A request with no responder writes both events too, in order, so the audit pair always exists (`REQ-ASK-003`).

### Failure

| Condition | Stable code | Retryable | Observable behavior |
|---|---|---:|---|
| Store write fails for `question/asked` | `internal_error` | No | The request does not open; the tool returns an error |
| Store write fails for `question/answered` | `internal_error` | No | Logged. The tool returns the answers it has |

### Side effects and guarantees

- **State transition**: `waiting_input` is set by `question/asked` and cleared by `question/answered` (`API-ASK-008`).
- **Idempotency**: `question/answered` is written at most once per request. The first closing path wins.
- **Concurrency**: The closing write holds the question service's lock, so two closing paths cannot both write.
- **Data exposure**: No question text in `question/asked`. Answers are in the log as the owner typed them.

### Compatibility

- **Breaking change**: No. Old logs contain no question events.
- **Deprecation**: Not applicable

## API-ASK-003: Go library options and responder hook

- **Goal**: Let embedders and the daemon answer questions, as `lana.ApproveWith` answers approvals.
- **Requirements**: `REQ-ASK-003`, `REQ-ASK-018`
- **Acceptance criteria**: `AC-ASK-006`, `AC-ASK-007`, `AC-ASK-032`
- **Stability**: New
- **Authorization**: Go API. The embedder is trusted.

### Input

```go
// package lana
type AskFunc = func(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome

func AskWith(fn AskFunc) Option
func AskChannels(channels ...string) Option // default when not set: "library", "cli"
```

```go
// package core (new file core/question.go)
type QuestionOption struct {
    Label       string
    Description string
}

type Question struct {
    Text        string
    Header      string
    MultiSelect bool
    Options     []QuestionOption
}

type QuestionRequest struct {
    SessionID string
    TurnID    string
    CallID    string
    RequestID string
    AgentName string
    Questions []Question
    ExpiresAt int64 // Unix ms
}

type QuestionAnswer struct {
    Question string   // Question.Text
    Selected []string // option labels, in option order
    Other    string   // empty unless typed
    Skipped  bool
}

type QuestionOutcome struct {
    Status  string           // "answered" or "unavailable"
    Reason  string           // when unavailable
    Answers []QuestionAnswer // when answered, one per question
}

type QuestionService interface {
    Ask(ctx context.Context, req QuestionRequest) (QuestionOutcome, error)
}
```

`core.Hooks` gains `QuestionAsk *Waterfall[QuestionRequest, QuestionOutcome]`. `core.Context` gains `Questions QuestionService`
and the service key `core.ServiceQuestion`.

### Success

| Condition | Result |
|---|---|
| Responder returns `answered` with valid answers | Request ends `answered` |
| Responder returns `unavailable` with a known reason | Request ends `unavailable` with that reason |

### Failure

| Condition | Result |
|---|---|
| No responder for the channel | `unavailable`, `no_responder` |
| Responder panics | `unavailable`, `no_responder` |
| Responder returns an unknown status or reason | `unavailable`, `no_responder` |
| Responder returns answers that do not match the questions | `unavailable`, `no_responder`, and a log line |

### Side effects and guarantees

- **State transition**: As `API-ASK-002`.
- **Idempotency**: Not applicable.
- **Concurrency**: The hook may be called from several sessions at once. The responder must be safe for that.
- **Data exposure**: The responder receives the question text and labels, and the session ID.

### Compatibility

- **Breaking change**: No. A runtime without `AskWith` gets `no_responder` for questions only.
- **Deprecation**: Not applicable

## API-ASK-004: `question` SSE event

- **Goal**: Show an open request in the web card as soon as it opens, and again after a reconnect.
- **Requirements**: `REQ-ASK-011`
- **Acceptance criteria**: `AC-ASK-019`, `AC-ASK-021`
- **Stability**: New
- **Authorization**: Same as `API-WEB-008` (owner browser session). Only events of the session the stream was opened for.

### Input

`GET /v1/sessions/{id}/stream` (`API-WEB-008`). No new parameter.

### Success

An SSE event named `question`. The data is one JSON object:

```text
event: question
data: {"request_id":"<id>","questions":[{"question":"Where should I save the weekly digest?","header":"","multi_select":false,"options":[{"label":"research/digests/","description":"Next to the notes, one file per week."},{"label":"Inbox/","description":"Your inbox folder, to sort later."}]}],"expires_at":1730000000000}
```

| Field | Type | Meaning |
|---|---|---|
| `request_id` | string | The ID to use in `API-ASK-005` |
| `questions` | array | The questions, same shape as the tool input. `header` is an empty string when absent. `description` is omitted when absent |
| `expires_at` | int | Unix ms when the request times out |

The event is sent when a request opens, and again to every stream that connects while the request is open. It is sent
only to web sessions. Closing is not sent as an SSE event. The card closes when the stream shows `question/answered` for the
same `request_id`, or at once after `API-ASK-005` returns `200`, `404`, or `409`.

### Failure

| Condition | Result |
|---|---|
| Stream cannot be opened | As `API-WEB-008` |
| Broker publish buffer full for a slow stream | The event is dropped from that stream and sent again on reconnect (as approvals). The log shows the request |

### Side effects and guarantees

- **State transition**: None. Presentation only.
- **Idempotency**: Replays return the same `request_id`.
- **Concurrency**: Subscription happens before replay, so a request opened during the handshake is not missed.
- **Data exposure**: The questions are shown to the owner's browser only.

### Compatibility

- **Breaking change**: No. Clients that ignore unknown events are unaffected.
- **Deprecation**: Not applicable

## API-ASK-005: `POST /v1/questions/{request_id}`

- **Goal**: Submit the owner's answers, or skip the request.
- **Requirements**: `REQ-ASK-011`, `REQ-ASK-019`
- **Acceptance criteria**: `AC-ASK-019`, `AC-ASK-020`, `AC-ASK-033`
- **Stability**: New
- **Authorization**: Owner browser session, `Host` and `Origin` checks, and CSRF header, as every `/v1` `POST` (`API-WEB-002`). The request ID is the only secret needed for the answer, and it is random.

### Input

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| Path | `request_id` | string | Yes | ID from `API-ASK-004`. Unknown IDs get `404` |
| Body | `answers` | array | Yes, unless `skip_all` is true | One entry per question. Each question index appears exactly once |
| Body | `answers[].question` | int | Yes | Index from 0 to n-1 of the question |
| Body | `answers[].selected` | array of int | No | Option indexes from 0. Unique. Each less than the option count. Default `[]` |
| Body | `answers[].other` | string or null | No | Text of `Something else`. 1 to 500 characters after trimming. Default `null` |
| Body | `answers[].skipped` | bool | No | Default `false` |
| Body | `skip_all` | bool | Yes, unless `answers` is set | `true` skips every question. Cannot be combined with `answers` |

Rules checked in order. Malformed JSON, an unknown field, or a body over 64 KiB is `400 invalid_request`. The
rules below are `422 invalid_answers`:

- `question` out of range, or a question index given twice, or a question missing from `answers`.
- A skipped entry with `selected` non-empty or `other` set.
- A non-skipped entry with no selected option and no `other`.
- A single-select question with more than one selected option, or with both a selected option and `other`.
- An option index out of range or repeated.
- `other` longer than 500 characters or empty after trimming.

Example body, first question single-select (option 1), second multi-select (options 0 and 2 with text), third skipped:

```json
{"answers":[
  {"question":0,"selected":[1]},
  {"question":1,"selected":[0,2],"other":"Weekly trend chart"},
  {"question":2,"skipped":true}
]}
```

Skip all:

```json
{"skip_all": true}
```

### Success

| Condition | Status | Body |
|---|---:|---|
| Accepted. This answer closed the request | `200` | `{"outcome":"answered"}` |

For `skip_all`, the body is also `{"outcome":"answered"}` with status `200`. The log records `question/answered` with `outcome` `answered`, and each entry has `skipped: true`.

### Failure

| Condition | Status | Code | Retryable | Observable behavior |
|---|---:|---|---:|---|
| Malformed JSON, unknown field, or body too large | `400` | `invalid_request` | No | Request stays open |
| Semantic rule violated (see above) | `422` | `invalid_answers` | No (fix the body) | Request stays open. The card shows the inline error `Check your answers` |
| Request ID never existed | `404` | `not_found` | No | Nothing recorded |
| Request already closed (answered, timed out, cancelled, or closed by another tab) | `409` | `question_closed` | No | Nothing recorded. The card shows `Already answered elsewhere` |
| Not logged in, bad `Host` or `Origin`, or missing CSRF | As `API-WEB-002` | As `API-WEB-002` | No | Nothing recorded |
| Store write fails | `500` | `internal_error` | Yes | Request stays open. The card shows `Could not send. Try again.` |

### Side effects and guarantees

- **State transition**: Closes the request as `answered` (`API-ASK-002`), status `running`.
- **Idempotency**: Not idempotent by design. A repeat gets `409 question_closed`. The first accepted answer wins.
- **Concurrency**: The first accepted answer for a request wins. Tabs that lose get `409`.
- **Data exposure**: The response does not repeat the answers.

Closed request IDs are remembered for one hour (as approvals). After that, a late answer gets `404 not_found`.

### Compatibility

- **Breaking change**: No
- **Deprecation**: Not applicable

## API-ASK-006: Telegram keyboard, callback data, and texts

- **Goal**: Let the owner answer in the chat of the session, with buttons, and with a text message for `Something else`.
- **Requirements**: `REQ-ASK-016`, `REQ-ASK-019`
- **Acceptance criteria**: `AC-ASK-028` to `AC-ASK-030`, `AC-ASK-034`
- **Stability**: New
- **Authorization**: Only the owner's user ID, as the approval keyboard (`isOwner`). Group members get `only the owner can answer`.

### Input

Callback data (each button). Telegram limits callback data to 64 bytes. The longest form here is about 22 bytes.

```text
q:<tok>:<qi>:<act>[:<oi>]
```

| Part | Meaning |
|---|---|
| `q` | Kind: question |
| `tok` | 12 lowercase hex characters. Random, one per request. Maps to the request only while it is open |
| `qi` | Question index, 0 to 2 |
| `act` | `s` pick one option (single-select), `t` toggle option (multi-select), `d` Done (multi-select), `o` Something else, `k` Skip |
| `oi` | Option index from 0. Present for `s` and `t` only |

Examples: `q:4f1c09ab77de:0:s:1` (option 2 of question 1), `q:4f1c09ab77de:1:t:0` (toggle option 1 of question 2), `q:4f1c09ab77de:1:d`, `q:4f1c09ab77de:2:k`.

Message per question (one message for each question, sent in order):

```text
❓ Question 1 of 3
[Digest] Where should I save the weekly digest?
1. research/digests/ — Next to the notes, one file per week.
2. Inbox/ — Your inbox folder, to sort later.
3. Telegram only — Send it in chat and don't save a file.
4. Both — Save a file and send a short summary.
Tap an option, or Something else, or Skip.
```

The `[Digest]` prefix appears only when `header` is set. Multi-select messages say `Tap options to select, then Done.`
instead of the last line. Keyboard rows: one row per option. Single-select: the label, with its number (`1 research/digests/`),
truncated to 60 characters with `…`. Multi-select: `☐ <label>` or `☑ <label>` for selected options. Then one row
`Something else` and `Skip`. Multi-select adds one row `Done`.

| Text | When |
|---|---|
| `Type your answer in the next message.` | Reply after `Something else` is tapped |
| `Pick at least one option, or Skip.` | Done tapped with nothing selected (callback answer, nothing changes) |
| `✓ Answered: <label>` | Single-select answer (message edited) |
| `✓ Answered: <label>, <label>` | Multi-select answer (message edited). With text: `✓ Answered: <label>. Other: <text>` |
| `✓ Answered: <text>` | `Something else` answer (message edited) |
| `⊘ Skipped` | Skip (message edited) |
| `⊘ Question expired` | Timeout. All messages of the request are edited |
| `⊘ Question cancelled` | Turn cancelled or daemon stopping |
| `⊘ Answered elsewhere` | The web view or the CLI answered first |
| `only the owner can answer` | Callback answer for a non-owner |
| `this question is no longer open` | Callback answer for a closed token |

After the request closes, the keyboards are removed from every message of the request. A failed send of a question message
ends the request as `unavailable` with reason `no_responder`.

### Success

The owner's tap or text is recorded as in the table above, and the request is submitted once the last question has an
answer or is skipped. Single-select answers submit at once for the last question.

### Failure

| Condition | Result |
|---|---|
| Callback token unknown or closed | Callback answer `this question is no longer open`. Nothing recorded |
| Callback from a non-owner | Callback answer `only the owner can answer`. Nothing recorded |
| Text message while `Something else` is not armed | Text goes to the session inbox (`REQ-TG-004`) |
| Text message while `Something else` is armed | Text is the answer. Empty text is ignored and the question stays armed |
| Timeout | Texts above; reason `timeout` |

### Side effects and guarantees

- **State transition**: As `API-ASK-002`.
- **Idempotency**: A repeated tap of the same button after the answer is refused with the closed-token text.
- **Concurrency**: Taps are handled on the chat worker, as approvals.
- **Data exposure**: Callback data holds no question text. Tokens are not logged.

### Compatibility

- **Breaking change**: No
- **Deprecation**: Not applicable

## API-ASK-007: CLI prompt

- **Goal**: Show an open request in `lana chat` and answer it with keys. The same text is used for line mode on `/dev/tty`.
- **Requirements**: `REQ-ASK-017`, `REQ-ASK-018`
- **Acceptance criteria**: `AC-ASK-031`, `AC-ASK-032`
- **Stability**: New
- **Authorization**: The terminal. Only the process that owns the TUI or the `/dev/tty` can answer.

### Input

TUI (`lana chat`). Keys:

| Key | Single-select | Multi-select | Something else input | Notes |
|---|---|---|---|---|
| `1` to `9` | Pick option and move on | Toggle option | Typed as text | Numbers beyond the list do nothing |
| Up, Down | Move highlight | Move highlight | Not used | |
| Enter | Pick highlighted | Move on, or submit on the last question | Submit the text | Multi-select with nothing selected: `Pick at least one option, or s to skip.` |
| `N+1` (after the last option) | Open `Something else` | Open `Something else` | | `N` is the number of options |
| `s` | Skip this question | Skip this question | Not used | |
| Esc | Not used | Not used | Close the text input and go back to the list | |
| Ctrl+C | Cancels the turn (existing behavior) | Cancels the turn | Cancels the turn | Request ends `cancelled` |

Single-select prompt, as shown in the transcript area:

```text
? [Digest] Where should I save the weekly digest?  (1 of 3)
  1) research/digests/ — Next to the notes, one file per week.
  2) Inbox/ — Your inbox folder, to sort later.
  3) Telegram only — Send it in chat and don't save a file.
  4) Both — Save a file and send a short summary.
  5) Something else
  s) Skip
```

Multi-select prompt:

```text
? Which sections should the digest have?  (2 of 3, select all that apply)
  [x] 1) Key findings — One line per finding.
  [ ] 2) Open questions — Things the notes don't answer yet.
  [x] 3) Action items — Tasks with an owner and a due date.
  [ ] 4) Source links — Links to the notes each finding came from.
  5) Something else
  s) Skip      Enter) next
```

Line mode (`lana run --ask`, `/dev/tty`). One line is read per prompt:

| Prompt | Accepted reply |
|---|---|
| `Choose 1-5, or s to skip: ` (single-select, `5` = Something else) | One number, or `s` |
| `Choose numbers, e.g. 1 3, or s to skip: ` (multi-select) | Numbers separated by spaces. An empty line means no selection and is treated as skip. `5` is Something else, then a line `Your answer: ` follows |
| `Your answer: ` | Any non-empty line. Empty line asks again |

Line-mode and TUI texts are the same for the question and the options.

### Success

| Condition | Transcript line |
|---|---|
| Answered | `✓ answered: <label>` or `✓ answered: <label>, <label>` (multi). With text: `✓ answered: <text>` |
| Skipped | `· question skipped` |
| Timeout | `· question expired` |
| Cancelled | `· question cancelled` |
| Answered in another channel | `· answered elsewhere` |

### Failure

| Condition | Result |
|---|---|
| Line mode, no `/dev/tty` | Request ends `unavailable` with `no_responder`. Tool output as `API-ASK-001` |
| Number out of range | Prompt repeats with `Choose one of the numbers shown.` |

### Side effects and guarantees

- **State transition**: As `API-ASK-002`.
- **Idempotency**: Not applicable.
- **Concurrency**: One request at a time per TUI, as approvals.
- **Data exposure**: Answers are shown in the transcript.

### Compatibility

- **Breaking change**: No
- **Deprecation**: Not applicable

## API-ASK-008: Session status `waiting_input`

- **Goal**: Let every channel and the web view show that a turn waits for the owner.
- **Requirements**: `REQ-ASK-009`
- **Acceptance criteria**: `AC-ASK-016`, `AC-ASK-017`
- **Stability**: New value of an existing field (spec #9 `API-WEB-003` status enum; `core.SessionInfo.Status`)
- **Authorization**: Same as the operation that returns the session.

### Input

Not applicable. The value is projected from events.

### Success

| Last question event | Status |
|---|---|
| `question/asked` with no later `question/answered` | `waiting_input` |
| `question/answered` | `running` (if the turn is still open) |
| `turn/end` | `idle` |

Sessions with `status` `waiting_input` appear in the `status` filter of spec #9 `API-WEB-003` as `waiting_input`.

### Failure

Not applicable.

### Side effects and guarantees

- **State transition**: as in the table above.
- **Idempotency**: Projection rebuilt from events gives the same result (`RebuildProjections`).
- **Concurrency**: Projection is written with the event, in one transaction.
- **Data exposure**: None beyond the session.

### Compatibility

- **Breaking change**: No. Clients that do not know `waiting_input` must treat it as running. Spec #9 documents the value.
- **Deprecation**: Not applicable

## Contract traceability

| Operation | Requirements | Acceptance criteria | Design component | Verification |
|---|---|---|---|---|
| `API-ASK-001` | `REQ-ASK-001`, `REQ-ASK-002`, `REQ-ASK-006` | `AC-ASK-001` to `AC-ASK-005`, `AC-ASK-011`, `AC-ASK-012` | `tools/ask_user.go`, `internal/question` | `tools/ask_user_test.go`, `internal/question/question_test.go` |
| `API-ASK-002` | `REQ-ASK-008`, `REQ-ASK-010` | `AC-ASK-015`, `AC-ASK-018` | `core/event.go`, `store/store.go` | `store/store_test.go`, `internal/question/question_test.go` |
| `API-ASK-003` | `REQ-ASK-003`, `REQ-ASK-018` | `AC-ASK-006`, `AC-ASK-007`, `AC-ASK-032` | `lana.go`, `core/hooks.go` | `lana_questions_test.go`, `cmd/lana/main_test.go` |
| `API-ASK-004` | `REQ-ASK-011` | `AC-ASK-019`, `AC-ASK-021` | `web/questions.go`, `web/sse.go` | `web/questions_test.go`, `web/sse_test.go` |
| `API-ASK-005` | `REQ-ASK-011`, `REQ-ASK-019` | `AC-ASK-019`, `AC-ASK-020`, `AC-ASK-033` | `web/questions.go` | `web/questions_test.go` |
| `API-ASK-006` | `REQ-ASK-016`, `REQ-ASK-019` | `AC-ASK-028` to `AC-ASK-030`, `AC-ASK-034` | `channels/telegram/ask.go`, `channels/telegram/chat.go` | `channels/telegram/ask_test.go` |
| `API-ASK-007` | `REQ-ASK-017`, `REQ-ASK-018` | `AC-ASK-031`, `AC-ASK-032` | `channels/cli/tui/asker.go`, `cmd/lana/main.go` | `channels/cli/tui/tui_test.go`, `cmd/lana/main_test.go` |
| `API-ASK-008` | `REQ-ASK-009` | `AC-ASK-016`, `AC-ASK-017` | `store/store.go`, `store/memstore/memstore.go` | `store/store_test.go` |

## Blocking Contract Questions

None. The requirement questions that shaped this contract are answered in [requirements.md](requirements.md) (`Q-001` to `Q-013`).

## Approval

- [x] Requirements are Approved and their revision is recorded above.
- [x] Contract applicability has been determined.
- [x] Every operation has a stable ID and is linked to requirements and acceptance criteria.
- [x] Input, success, failure, authorization, and compatibility are explicit.
- [x] No behavior exists only in this file.
- [x] Blocking Contract Questions is `None`.
- **Approved by**: Ridho, lifted gate 2026-10-30
- **Approved on**: 2026-10-30

# Requirements: Workflows

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
| Owner interview | Chat, 2026-11-01 (memory `workflows-feature-decisions`) | Guardian bot, triggers, nodes, state, errors, data, permissions |
| Owner decision | Chat, 2026-11-02 | Assistant chat column, error ladder screens, browser login plan |
| Owner decision | Chat, 2026-11-05 | "implement workflow yang ada di figcraft, sesuaikan rekomendasi kamu, test dulu" |
| Design | `design/lana-design-system.figcraft`, page "Workflows"; `design/export/workflows-1..8-*.png` | Layout, states and copy |
| Existing specs | [#9 web](../9-web-settings-chat/requirements.md), [#4 permission](../4-session-permission/requirements.md), [#30 knowledge](../30-knowledge/requirements.md), [#32 rooms](../32-agent-rooms/requirements.md) | Behavior this feature reuses |

> **Gate lifted (2026-11-05).** The owner said to implement the figcraft design with the agent's
> recommendations while they sleep. Open questions are resolved with `Rekomendasi agent:` in the
> resolution table and marked Approved on behalf of the owner.

## Goal

The owner can automate recurring work as a graph of nodes, like n8n. Each workflow has one
guardian bot. Nodes run deterministically. The guardian runs AI steps, decides what to do when a
node keeps failing, and builds or edits the graph through chat. Every run is durable and keeps the
input, output and logs of every node.

## Scope

### Included

- Workflow definitions with versions, stored in the session database.
- Triggers: manual (web Run button, optional JSON input), schedule (cron, time zone), interval,
  webhook (`POST /hooks/{id}` with a secret), lana session events, and chat command (`/<command>`
  in web chat and in the Telegram owner chat).
- Nodes: agent step (guardian), call another bot, worker agent, browser (Obscura), bash, web
  fetch, any registered tool (MCP included), send message (Telegram owner chat), save to
  knowledge, if, switch, loop, wait, human approval, merge, get state and set state.
- Template expressions `{{ ... }}` in node parameters, and conditions for if and switch.
- Durable runs: node results are saved after every node; running, waiting and paused runs resume
  after a daemon restart.
- A persistent key-value state per workflow.
- The error ladder: node retries, one guardian diagnosis per node per run, five guardian actions,
  the owner is told, and 3 failed runs in a row set the workflow to Error.
- The permission lock: activation locks the list of tools; tool calls outside the lock wait in the
  approval queue.
- The guardian assistant: the owner chats with the guardian in the editor, the guardian proposes a
  new graph version, the change shows as a card with a diff and Undo.
- Web screens: list, editor, executions (run detail), settings, the assistant column, the new
  workflow screen and the failed-run screen (design `workflows-1..7`).

### Not included

- Editing a workflow through Telegram chat. The guardian edits through the web assistant only.
  Telegram gets notifications and decision buttons.
- Parallel execution inside one run. Nodes run one at a time in a fixed order, loop items too.
- A JavaScript or code node.
- Browser login flows with 2FA. The browser node reuses a storage dir per workflow; the first
  login is done by the owner outside lana.
- Money cost. Runs show tokens.
- Workflow posts into agent rooms.
- Import from n8n.

## Assumptions

- `Runtime.Run` blocks for one turn and returns the final text and usage. — Source: code map,
  `lana.go:678`.
- Approvals route by session channel; unknown channels are rejected. — Source: `cmd/lana/daemon.go`
  `ApprovalRouter`.
- The `obscura` binary is on PATH where the browser node is used. — Source: owner machine.
- The web server binds to loopback only; a webhook from outside needs a tunnel. — Source: spec #9.

## Behavior rules

### REQ-WF-001: Workflow definition

A workflow has an id (slug from the name, unique, `^[a-z0-9][a-z0-9-]{0,47}$`), a name, a
description, a guardian agent, a status (`draft`, `active`, `paused`, `error`), a graph (nodes and
edges), and a version number. Every saved change creates a new version and keeps the old graph, so
any version can be restored. A version records who changed it (`owner` or `guardian`) and a short
summary.

- `AC-WF-001`: Given a new workflow "Riset harian", when the owner saves it, then its id is
  `riset-harian`, status `draft`, version 1; a second save creates version 2 and version 1 is still
  readable.

### REQ-WF-002: Graph validation

The system shall reject a graph that has an unknown node type, a duplicate node id, an edge to a
missing node, a cycle (loop bodies use the `parent` field, not back edges), a non-trigger node with
no incoming edge, missing required parameters, or a template that does not parse. The error lists
every problem with the node id.

- `AC-WF-002`: Given a graph with an edge `a → b → a`, when it is saved, then the save fails with
  `invalid_graph` and an issue `cycle` naming the nodes.

### REQ-WF-003: Triggers

A trigger is a node of kind `trigger.*` with an `enabled` flag. Only an `active` workflow fires
schedule, interval, webhook, event and chat triggers. The manual trigger also runs a `draft` or
`paused` workflow as a test run. Each run records which trigger started it and its input JSON.

- Schedule: 5-field cron (`m h dom mon dow`) in an IANA time zone, default the system zone.
- Interval: every N minutes, N ≥ 1.
- Webhook: `POST /hooks/{id}`; the request body (JSON or text) is the input. It is accepted only
  with header `X-Lana-Token` equal to the workflow's webhook secret.
- Event: a lana session event kind (`turn/end`, `session/created`, `approval/asked`, ...) with an
  optional agent filter. Events from workflow sessions never fire event triggers.
- Chat command: `/<command>` sent by the owner in web chat or the Telegram owner chat starts a run
  with `{"text": "<rest of the message>", "channel": "..."}` as input and replies `Run #N started`.

- `AC-WF-003`: Given an active workflow with schedule `0 7 * * *` in `Asia/Jakarta`, when the
  scheduler ticks at 07:00 Jakarta time, then exactly one run starts with trigger `schedule`; a
  second tick in the same minute starts none.
- `AC-WF-004`: Given an active workflow with an enabled webhook, when `POST /hooks/{id}` comes with
  a wrong token, then the response is 401 and no run starts; with the right token, the response is
  202 with the run id.
- `AC-WF-005`: Given an active workflow with chat command `riset`, when the owner sends
  `/riset ai news` in web chat, then a run starts with input text `ai news` and no agent turn
  starts.

### REQ-WF-004: Node execution and data

Nodes run one at a time, in topological order (ties by node position: top, then left). Every node
outputs JSON. A node runs when all its incoming edges are resolved; an edge from an `if`/`switch`
branch that was not taken, or from a skipped node, makes the target skipped unless the target is a
`merge` node with at least one live input. Parameters use template expressions:
`{{ input.x }}`, `{{ nodes.<id>.output.<path> }}`, `{{ item }}`, `{{ item_index }}`,
`{{ state.<key> }}`, `{{ run.id }}`, `{{ workflow.id }}`. A parameter that is only one expression
keeps the JSON type; mixed text renders to a string.

- `AC-WF-006`: Given `fetch → if (nodes.fetch.output.status == 200) → [true: send, false: log]`,
  when fetch returns status 200, then `send` runs, `log` is skipped, and the run succeeds.
- `AC-WF-007`: Given a parameter `"{{ nodes.a.output.items }}"` where items is an array, when the
  node runs, then it receives a JSON array, not a string.

### REQ-WF-005: Node types

| Type | Behavior |
|---|---|
| `agent` | Runs a prompt on the guardian. Optional output schema: the answer must be JSON that matches it. |
| `bot` | Runs a prompt on another configured agent. |
| `worker` | Runs a task in a fresh session of an agent (default the guardian), with an optional model, a tool list and a step budget. |
| `browser` | Loads a URL with Obscura (`obscura fetch --dump text|markdown|html`), stealth optional, storage dir per workflow. Output `{url, content, format}`. |
| `bash` | Runs a command in the workflow workspace. Output `{exit_code, output}`. Exit code ≠ 0 is a failure. |
| `webfetch` | Fetches a URL as Markdown. Output `{url, title, markdown}`. |
| `tool` | Calls any registered tool by name with JSON input (MCP tools included). Output `{output}`. |
| `message` | Sends text to the Telegram owner chat. Output `{sent: true}`. |
| `knowledge` | Saves text as a knowledge note under a folder. Output `{saved: true, title}`. |
| `if` | Evaluates a condition; branches `true` and `false`. |
| `switch` | Evaluates a value against cases; branch per case and `default`. |
| `loop` | Evaluates `items` to an array and runs its body nodes (nodes with `parent` = loop id) once per item. Output `{items: [...per item output of the last body node...], ok, skipped}`. |
| `wait` | Waits a duration (seconds, up to 7 days). Durable. |
| `approval` | Asks the owner (web and Telegram) a question with Approve/Deny. Approve takes branch `approved`, Deny `denied`. Durable, expires after 24 h as denied. |
| `merge` | Joins branches; output is an object of its live inputs by node id. |
| `state_get` / `state_set` | Read or write a key of the workflow state. |

- `AC-WF-008`: Given a loop over 3 items whose body is one `bash` node `echo {{ item }}`, when the
  run executes, then the loop output has 3 items with the 3 echoed values.
- `AC-WF-009`: Given an `agent` node with output schema `{"score": "integer"}`, when the guardian
  answers `{"score": 8}`, then the node output is `{"score": 8}`; when it answers prose, then the
  node fails with `schema_error`.

### REQ-WF-006: Workflow state

Each workflow has a persistent key-value store of JSON values that lives across runs. Nodes read it
with `{{ state.key }}` and write it with `state_set`. The owner can view, edit and clear it in
Settings. Writes are saved with the node result.

- `AC-WF-010`: Given a run that sets `last_run_at`, when the next run reads `{{ state.last_run_at }}`,
  then it gets the saved value.

### REQ-WF-007: Durable runs

The system saves the run and every node result after each node. On daemon start, runs that were
`running` continue from the first unfinished node (that node runs again), runs that `wait` keep
their wake time, and runs waiting for an approval or an owner decision keep waiting. A cancelled
run stops before its next node and is marked `cancelled`.

- `AC-WF-011`: Given a run stopped by a daemon restart after node 2 of 4, when the daemon starts,
  then nodes 1–2 are not run again and nodes 3–4 run.

### REQ-WF-008: Error ladder

When a node fails:

1. The run saves the error, the node input and the logs.
2. Transient errors (timeout, rate limit 429, 5xx, network) are retried by the node policy:
   default 2 retries with backoff 10 s then 30 s. Other errors are not retried. Approval nodes
   never retry.
3. If retries do not help and the failure mode is `guardian` (default), the guardian diagnoses the
   failure once per node per run. It reads the error, the input, the logs, the node and the last 5
   runs, and answers one action: `retry_fix` (retry with changed parameters, this run only),
   `skip` (mark the item or node skipped and continue), `fallback` (use the node's fallback output),
   `ask` (pause the run and ask the owner), or `stop`.
4. The guardian must use `ask` when the fix would change the saved workflow, needs a tool outside
   the lock, or its confidence is low. A run waiting for the owner stops after 24 h.
5. The owner is told on Telegram and in the web Needs attention list, with the reason and the
   token cost.

With failure mode `stop` the run fails without AI. If the guardian itself fails (model down,
invalid answer), the run stops and the owner gets a plain alert. After 3 failed runs in a row the
workflow status becomes `error` and its triggers stop firing until the owner resumes it.

- `AC-WF-012`: Given a `bash` node that fails with `timeout` twice and then passes, when the run
  executes, then the node has 3 attempts and the run succeeds without a guardian call.
- `AC-WF-013`: Given a node that fails with a non-transient error and the guardian answers `skip`,
  when the run executes, then the node is `skipped`, the run continues, and a guardian decision
  with reason and tokens is saved on the run.
- `AC-WF-014`: Given a guardian answer `ask` with a proposed fix, when the owner picks
  "Apply and resume", then the fix is saved as a new workflow version and the node runs again;
  "This run only" applies it to the run only; "Stop run" cancels the run.
- `AC-WF-015`: Given 3 failed runs in a row, when the third run fails, then the workflow status is
  `error` and its schedule no longer fires.

### REQ-WF-009: Permission lock

Activation computes the tool list the graph needs (tool nodes, worker tools, browser, message,
knowledge, state) with each tool's risk, and the owner accepts it ("Lock and activate"). The lock
records the version and time. During a run, a tool call by a workflow node or a worker that is in
the lock runs without asking; anything else goes to the approval queue (web approvals and
Telegram). A graph change that needs a new tool sets the workflow back to `draft` until it is
locked again.

- `AC-WF-016`: Given a locked workflow without `bash`, when a worker in its run calls `bash`, then
  an approval request is raised and the call waits; a locked `webfetch` call runs without asking.

### REQ-WF-010: Guardian assistant

In the editor the owner chats with the guardian. The guardian receives the workflow goal, the
current graph JSON, the node catalog and the message, and answers with a reply and, when it
changes the workflow, a full new graph. The system validates the graph, saves it as a new version
by `guardian`, and shows a change card (added, removed and changed nodes) with Undo. Undo restores
the previous version as a new version. Only the guardian of the workflow is used. Changing the
guardian is allowed in Settings and puts the workflow back to `draft` (the new guardian accepts
the lock again).

- `AC-WF-017`: Given a workflow at version 3, when the guardian answers with a valid graph that adds
  one node, then version 4 is saved with author `guardian` and the reply lists `+ <node name>`;
  Undo creates version 5 equal to version 3.
- `AC-WF-018`: Given a guardian answer whose graph is invalid, when it is received, then no version
  is saved and the reply shows the validation problems.

### REQ-WF-011: Web screens

The web shall show the design screens:

- List (`workflows-1`): stats (active, waiting on you, success rate 7 days, runs today), search,
  status filter, table (name, guardian, nodes, trigger, status, runs 7 d, success, last run), and
  Needs attention (approvals and owner decisions with actions).
- Editor (`workflows-2`, `-5`, `-6`): palette, canvas (pan, zoom, drag nodes, connect and delete
  edges, add nodes from the palette), node inspector (Parameters, Last output, On failure), the
  assistant column with Chat/Node switch, Save, Run, Activate/Pause.
- Executions (`workflows-3`, `-7`): run list, the graph with per-node status badges, the node panel
  (Output, Input, Logs, Error, guardian decision with actions), the run footer (nodes done,
  duration, tokens), Cancel run.
- Settings (`workflows-4`): general, active toggle, guardian, triggers with toggles, locked
  permissions, state viewer, failure default, delete.
- The sidebar has a "Workflows" item. Run status updates without a page reload.

- `AC-WF-019`: Given the list page with one waiting approval, when the owner clicks
  "Approve once", then the approval is answered and the item leaves the list.
- `AC-WF-020`: Given the editor, when the owner drags a palette item onto the canvas, connects it
  and saves, then the saved graph has the new node and edge.
- `AC-WF-021`: Given a running run, when a node finishes, then the executions view shows the new
  node status within 2 seconds without reload.

## Resolutions

| Question | Resolution | Status |
|---|---|---|
| Telegram message trigger: any message, or a command? | Rekomendasi agent: a chat command (`/<command>`) in the owner chat, shared with web chat. "Any message" would steal normal chat from the agent. | Approved on behalf of the owner |
| Parallel branches and loop items? | Rekomendasi agent: sequential in v1. Simpler durability and replay; enough for daily research. | Approved on behalf of the owner |
| How are loop bodies modelled? | Rekomendasi agent: body nodes carry `parent: <loop id>` (the dashed group in the design); the "next item" back edge is drawn, not stored. Keeps the graph a DAG. | Approved on behalf of the owner |
| Worker budget? | Rekomendasi agent: a step budget (tool calls) enforced by the workflow permission hook, plus a token cap checked after the turn. Per-spawn model via the session model override. | Approved on behalf of the owner |
| Guardian edits via Telegram? | Rekomendasi agent: deferred; v1 edits through the web assistant only. | Approved on behalf of the owner |
| Webhook security? | Rekomendasi agent: per-workflow random secret in header `X-Lana-Token`; the route skips the session and CSRF checks and the Host check, nothing else. | Approved on behalf of the owner |
| Canvas library? | Rekomendasi agent: custom SVG/HTML canvas in React (no new dependency), matching the Gr8r node card. | Approved on behalf of the owner |
| Where do workflow sessions run? | Rekomendasi agent: channel `workflow`, address `<workflow id>`, workspace `$LANA_HOME/workspaces/<guardian>`; approvals for that channel go to the web approval broker and a Telegram notice. | Approved on behalf of the owner |

# Requirements: Script Parse node

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (interview answers 2026-11-08; owner approved 2026-11-09)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file (API-SP-001..009) — Required
- **Related ADR**: [0021](../../adr/0021-embedded-js-sandbox-for-workflow-scripts.md)
- **Related spec**: [33 Workflows](../33-workflows/requirements.md) (amends the node table of API-WF and the decision kinds)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "workspace ini sebisa mungkin jadi low cost llm … node dimana agent guard bisa buat script yang mengconvert itu jadi json/text jadi tidak selalu lewat llm, namun jika ada perubahan struktur webnya bisa di handle/fallback agent step atau di handle agent guard" | goal, fallback, regeneration |
| Owner request | "mcp task jira, click up dll atau mcp api lain yang bentuknya selalu sama bisa di handle oleh skrip" | JSON inputs from `tool` nodes |
| Owner request | "node ini hanya opsi, user tetep bisa pakai agent step" | agent nodes unchanged |
| Interview 2026-11-08 | 1 JS via goja · 2 auto-apply a repaired script · 3 drift: the run continues, with info · 4 name "Script Parse" · 5 `output_schema` required · 6 convert and suggestions in scope | every decision below |

## Goal

A workflow step that extracts or reshapes data of a fixed shape should cost zero tokens per run. The
guardian writes a small JavaScript function once. Each run executes it in a sandbox and checks the
result against the node's contract. When the source changes shape and the check fails, the run still
completes through an agent fallback. The guardian then rewrites the script from the new input, the new
script is saved as a new workflow version, and the owner is told. Agent steps stay available and
unchanged.

## Definitions

- **Script** — JavaScript source that defines `function parse(input, ctx)` and returns one object.
- **Contract** — `output_schema` plus `item_schema` plus the non-empty rule plus `allow_empty`.
- **Drift** — the script throws, times out, returns a non-object, or its output fails the contract.
- **Fallback** — one agent call that answers the node's `goal` as JSON matching `output_schema`, with
  no tools.
- **Regeneration** — the guardian writes a new script from the current input and the fallback output.
- **Agreement** — the share of the fallback output's leaf values (strings trimmed and lowercased,
  numbers as-is) that also appear in the script output.
- **Baseline tokens** — the tokens one fallback or agent run of this step costs; used for "tokens saved".

## Requirements

| ID | Requirement |
|---|---|
| REQ-SP-001 | A new node type `script_parse` ("Script Parse", group `ai`, port `out`). Required params: `input`, `goal`, `output_schema`. Optional: `item_schema`, `allow_empty`, `script`, `fallback_agent`, `on_drift`, `timeout_ms`. System params: `script_rev`, `baseline_tokens`. |
| REQ-SP-002 | The script runs in an embedded goja VM with no file, network, process, timer, or module access. It gets `input` (the rendered `input` param, any JSON value) and `ctx` (`{item, run_number, workflow_id}`), plus the helpers `console.log` (to node logs, max 50 lines of 500 bytes) and `html(str)` (CSS selection over HTML, see the contract). Limits: script ≤ 32 KiB, input ≤ 2 MiB as JSON, output ≤ 1 MiB as JSON, time `timeout_ms` (default 2000, 100..10000). |
| REQ-SP-003 | The output is coerced with the existing `CoerceToSchema` and then checked: (a) `output_schema` fields; (b) every field of type `string`, `array` or `*[]` is non-empty unless named in `allow_empty`; (c) for each `item_schema` entry, every element of that array field passes `CheckSchema` with the entry's schema and the same non-empty rule. The first failing step names the drift code: `script_error`, `schema_drift`, `empty_drift`, `item_drift`. |
| REQ-SP-004 | When the script passes, the node succeeds with the script output, 0 tokens, and `via = "script"`. |
| REQ-SP-005 | When `script` is empty, or on drift, the run does not stop. The node runs the fallback on `fallback_agent` (empty = the guardian) with `Tools: []`. The prompt has the goal, the schema, the item schema, and the input, cut to 96 KiB (head 80 KiB + tail 16 KiB, marked). The answer goes through the existing `finishSchema` repair path and the same contract check. If it passes, the node succeeds with `via = "fallback"` and the fallback tokens. If it fails, the node fails and the normal error ladder of spec 33 runs. |
| REQ-SP-006 | `on_drift`: `regenerate` (default) runs the fallback, then regeneration. `fallback_only` runs the fallback without regeneration. `fail` fails the node with the drift error and the normal ladder runs, with no fallback. An empty script always uses the fallback. |
| REQ-SP-007 | Regeneration runs after a successful fallback, inside the same node step. One guardian call with `Tools: []` gets the goal, the contract, the old script, the drift error, the input (cut to head 16 KiB + tail 4 KiB with its full length stated) and the fallback output (cut to 8 KiB). It must answer the generation reply contract (API-SP-008). The new script is accepted only if it compiles, passes the contract on the current full input, and has agreement ≥ 0.8 when the fallback output has at least 5 leaves. A rejected script gets one more attempt with the problems appended. |
| REQ-SP-008 | An accepted script is applied without asking (interview answer 2). The engine saves a new workflow version, author `guardian`, summary `script <node name>: rev N (<drift code>)`, with `script`, `script_rev + 1` and `baseline_tokens` = the fallback tokens. On `ErrVersionConflict` it retries once on the newest version if that version still holds the old script for the node; otherwise it drops the update and records why. The rest of the current run (later loop items) uses the new script in memory. |
| REQ-SP-009 | Limits: at most one regeneration per node per run. After 3 rejected regenerations for a node within 24 h, regeneration pauses for that node until the owner saves a change to it or runs **Generate**. Later items and runs still use the fallback. |
| REQ-SP-010 | Every fallback records a decision of kind `script`: `script_fallback` (no new script) or `script_update` (new script saved). The decision is `applied` and holds the drift code and error, tokens (fallback + regeneration), the old and new `script_rev`, and both scripts for updates. |
| REQ-SP-011 | Every fallback informs the owner through the existing notice path (Telegram and macOS), with no buttons. The title is `<workflow> · Run #N: Script Parse used the agent`. The text gives the node name, the drift code, and either "script updated to rev N" or "script not repaired: <reason>". A `script_update` notice is always sent. A `script_fallback` notice for the same node is sent at most once per 24 h. |
| REQ-SP-012 | The owner can revert a `script_update` decision with the choice `revert`. Revert saves a new version that restores the previous script (with `script_rev + 1`), records `choice = "revert"`, and leaves the decision's status `applied`. |
| REQ-SP-013 | Saving a graph (owner, assistant, restore) validates `script_parse` nodes: missing required params (`missing_param`), an `output_schema` that is not an object of type strings, or an `item_schema` that names a field whose schema type is not `object[]` (`bad_schema`), a script over 32 KiB, a script that does not compile, or a script with no `parse` function (`bad_script`), and an unknown `fallback_agent` (`unknown_agent`). An empty script is valid. |
| REQ-SP-014 | `script_parse` adds no tools to the permission lock. |
| REQ-SP-015 | **Test** runs a node's script (saved or draft) on a sample with the same sandbox and contract and returns the output or problems, without tokens or storage. The sample comes from the request, else from the node's rendered `input` in a chosen run, else from the latest run that reached the node. |
| REQ-SP-016 | **Generate** asks the guardian for a script for a node (saved or draft) and a sample. It also takes the expected output from the request, or from the node's output in that run when that output came from the fallback or an agent. It validates the script as in REQ-SP-007 and returns it without saving. A successful Generate resets the pause of REQ-SP-009. |
| REQ-SP-017 | **Convert** proposes a `script_parse` node that replaces an `agent`, `bot` or `worker` node and keeps its id. The guardian derives `goal` and the `input` template from the prompt, keeps `output_schema` (or uses `{"text": "string"}` when there is none), and writes the script. The proposal is checked against up to 3 of the node's latest succeeded outputs as expected outputs. The guardian may answer `feasible: false` with a reason when the step needs judgment (real summarization, ranking by taste); the UI then shows the reason and offers no replacement. Nothing is saved until the owner applies the proposal and saves the graph. |
| REQ-SP-018 | **Suggestions** list `agent`, `bot` and `worker` nodes that are enabled, have at least 3 succeeded node runs in the last 30 days, and average at least 500 tokens per run. Each row has the run count, the average tokens, and the projected tokens for 30 days. Suggestions use no LLM. |
| REQ-SP-019 | Node runs gain `via` (`""`, `script`, `fallback`). The workflow detail gains `script_stats` per `script_parse` node: script runs, fallback runs, the last update time, and tokens saved = script runs × `baseline_tokens` (0 when unknown). |
| REQ-SP-020 | The guardian assistant knows the node. The catalog line is generated as today. Added rules: prefer `script_parse` over `agent` when a step only extracts, filters, or reformats data of a fixed shape; leave `script` empty because it is learned on the first run; always give `output_schema`. |
| REQ-SP-021 | UI: catalog entry and canvas subtitle (`Script Parse · rev N` or `Script Parse · learns on first run`). The inspector has the param fields, a code editor for `script`, **Generate** and **Test** with a sample picker (latest run, a chosen run, or pasted), the result panel, the stats card, and the script history (decisions of kind `script`, each update with **Revert**). Agent, bot and worker nodes on the suggestion list show a canvas badge and an inspector banner with **Convert to Script Parse**. Convert opens a preview with per-run checks, and **Replace node** changes the draft graph. Run detail shows `via` per node (`script · 4 ms · 0 tok` or `agent fallback · 2.1k tok`). Banner dismissal is kept in `localStorage`. Works at 390 px. |

## Acceptance criteria

| ID | Criterion |
|---|---|
| AC-SP-001 | Sandbox: `require`, `fetch`, `setTimeout` and `XMLHttpRequest` are undefined. An endless loop stops at `timeout_ms` with `script_error: timeout`. A 3 MiB input and a 2 MiB output are refused. `console.log` lines reach the node logs. |
| AC-SP-002 | A workflow `webfetch → script_parse (script empty) → message` learns on run 1 (`via=fallback`, a `script_update` decision, version +1, notice sent). Run 2 has `via=script` and 0 tokens on the node. |
| AC-SP-003 | A fixture input with a changed structure makes the script return `{repos: []}`. That is `empty_drift`. The run succeeds through the fallback, the script is regenerated and saved as rev N+1, and the next run uses the script. |
| AC-SP-004 | A regeneration that keeps failing yields `script_fallback` decisions. After 3 rejections within 24 h no guardian call is made. Generate or an owner save resumes regeneration. Only one `script_fallback` notice per node per 24 h. |
| AC-SP-005 | `on_drift=fail` fails the node and runs the spec 33 ladder. `fallback_only` never calls regeneration. |
| AC-SP-006 | A loop of 5 items whose second item drifts makes one regeneration. Items 3–5 run on the new script. |
| AC-SP-007 | Revert restores the previous script as a new version. A version conflict with an owner edit to the same node drops the update with a reason in the decision. |
| AC-SP-008 | A `tool` node whose output is a JSON string (an MCP issue list) is parsed by a script that uses `JSON.parse`. |
| AC-SP-009 | Convert on an agent node with `output_schema` keeps the node id and passes on the stored outputs. Downstream templates resolve unchanged. Convert on a summarizing agent returns `feasible: false` with a reason. |
| AC-SP-010 | Suggestions list exactly the qualifying nodes. `script_stats.tokens_saved` matches script runs × baseline. |
| AC-SP-011 | Validation rejects a 40 KiB script, a script with a syntax error, a script without `parse`, and a missing `output_schema`. It accepts an empty script. |

## Contract

| ID | Operation | Request | Response |
|---|---|---|---|
| API-SP-001 | Graph node `script_parse` | params: **`input`** (template, any), **`goal`** (string), **`output_schema`** (as spec 33), `item_schema` (`{"<object[] field>": {<field>: <type>}}`), `allow_empty` (string[]), `script` (string, `""` = learn), `fallback_agent` (string, `""` = guardian), `on_drift` (`regenerate`\|`fallback_only`\|`fail`), `timeout_ms` (2000), `script_rev` (integer, system), `baseline_tokens` (integer, system) | output: the checked object; templates read `nodes.<id>.output.<field>` |
| API-SP-002 | `POST /v1/workflows/{id}/script/test` | `{"node": <node>, "input"?: any, "run"?: number}` | 200 `{"ok", "output", "problems": [{"code", "message"}], "logs": [string], "duration_ms", "sample": {"source": "request"\|"run", "run"?: number}}`; 404 `no_sample`; 422 `validation_failed` (node is not `script_parse`) |
| API-SP-003 | `POST /v1/workflows/{id}/script/generate` | `{"node": <node>, "input"?: any, "run"?: number, "expected"?: object}` | 200 `{"feasible", "reason", "script", "output", "problems", "agreement"?: number, "tokens"}`; 404 `no_sample`; 503 `guardian_unavailable` |
| API-SP-004 | `POST /v1/workflows/{id}/script/convert` | `{"node_id": string}` | 200 `{"feasible", "reason", "node"?: <script_parse node, same id and position>, "checks": [{"run", "ok", "agreement", "problems"}], "tokens"}`; 404 `not_found`; 404 `no_sample` (no succeeded runs); 422 `validation_failed` (node type not agent/bot/worker) |
| API-SP-005 | `GET /v1/workflows/{id}/script/suggestions` | — | 200 `{"suggestions": [{"node_id", "name", "type", "runs", "avg_tokens", "est_tokens_30d"}]}` |
| API-SP-006 | `POST /v1/workflows/{id}/decisions/{decision}` (API-WF-015) | `{"choice": "revert"}` for kind `script`, action `script_update` | 200 the run, as API-WF-015; 409 `decision_resolved` when already reverted; 400 `invalid_choice` otherwise |
| API-SP-007 | `node_run` (API-WF-013), `decision` objects | — | `node_run.via`; `decision.kind` gains `script`, `decision.action` gains `script_update`\|`script_fallback`, and `decision.fix` holds `{"drift", "error", "rev_from", "rev_to", "script"?, "previous_script"?}` |
| API-SP-008 | Guardian generation reply | — | ONE JSON object: `{"feasible": bool, "reason": string, "script": string, "goal"?: string, "input"?: string, "output_schema"?: object}` (`goal`/`input`/`output_schema` only for convert) |
| API-SP-009 | Workflow detail (API-WF-003) | — | gains `script_stats: {"<node id>": {"script_runs", "fallback_runs", "tokens_saved", "last_update", "events": [<decision>]}}`; `events` are the newest 20 decisions of kind `script` for the node |

`html(str)` returns a selection with `find(css)`, `text()`, `attr(name)`, `html()`, `length`,
`map(fn)` and `toArray()`, backed by goquery. It parses the string; it fetches nothing.

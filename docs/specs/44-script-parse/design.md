# Design: Script Parse node

- **Issue**: `—`
- **Requirements**: [requirements.md](requirements.md)
- **Contract**: in requirements.md (API-SP-001..009) — Required
- **Requirements revision**: 1
- **Status**: Approved
- **Design revision**: 1

## Architecture decision scan

| Topic | Existing authority | Decision location | Action |
|---|---|---|---|
| Script runtime | none | [ADR 0021](../../adr/0021-embedded-js-sandbox-for-workflow-scripts.md) | add `github.com/dop251/goja` and `github.com/PuerkitoBio/goquery` in a new package `internal/scriptrt` |
| Node catalog, validation | spec 33 `catalog.go`, `validate.go` | this design | add `script_parse`, issue codes reuse `missing_param`, `bad_schema`, `bad_script`, `unknown_agent` |
| Contract check | spec 33 `CheckSchema`, `CoerceToSchema` | `internal/workflow/scriptcheck.go` | reuse, add non-empty, item schema, agreement |
| Engine | spec 33 `engine_handlers.go`, ladder, `saveFixVersion` | `internal/workflow/engine_script.go` | new handler; the ladder runs only when the fallback fails |
| Decisions | spec 33 kinds `guardian`/`approval` | this design | add kind `script`; `Answer` accepts `revert` |
| Persistence | `store/migrations`, `store/workflow.go` | next free migration | add column `workflow_node_runs.via` |
| Notices | `Engine.notify` → Telegram, macOS (spec 42) | unchanged | send with no buttons, throttle in memory |
| Web | `web/workflows.go` | `web/workflows_script.go` | four routes; `script_stats` on detail |
| Dependency direction | archtest | unchanged | `internal/scriptrt` imports stdlib, goja, goquery only; `internal/workflow` imports it |

## Package `internal/scriptrt`

```go
type Options struct {
    Timeout     time.Duration // 100ms..10s
    MaxInput    int           // bytes of JSON, default 2 MiB
    MaxOutput   int           // bytes of JSON, default 1 MiB
    MaxLogLines int           // 50
}

type Result struct {
    Value    any           // JSON-normalized (map[string]any, []any, float64, string, bool, nil)
    Logs     []string
    Duration time.Duration
}

// Compile checks size (≤ 32 KiB), syntax, and that a top-level function parse exists.
func Compile(src string) (*Program, error)
// Run executes parse(input, ctx) in a fresh goja.Runtime.
func Run(ctx context.Context, p *Program, input any, jsctx map[string]any, o Options) (Result, error)
```

- A fresh `goja.Runtime` per call. Only ECMAScript built-ins, plus `console.log` and `html`. No `require`.
  goja exposes no `setTimeout`, `fetch` or `XMLHttpRequest`; a test pins this.
- The timeout uses `time.AfterFunc(o.Timeout, func(){ vm.Interrupt("timeout") })`. Context cancel
  also interrupts. Errors are `ErrTimeout`, `ErrInputTooLarge`, `ErrOutputTooLarge`, `*ScriptError`
  (message plus line:col).
- The input goes in as a JSON-normalized Go value via `vm.ToValue`. The output is exported, marshaled
  to JSON (size check), and unmarshaled again so the engine sees plain JSON types.
- `html(str)` wraps a `*goquery.Selection` in a JS object: `find`, `text`, `attr`, `html`, `length`,
  `map(fn)`, `toArray`, `first`, `eq(i)`. It parses the document only once per `html()` call.
- No memory limit exists in goja. The size caps and the time limit bound the damage (ADR 0021).

## Package `internal/workflow`

### Catalog and validation

- `nodeCatalog["script_parse"] = {Group: groupAI, Label: "Script Parse", Required: input, goal, output_schema, Ports: out}`.
- `LockToolsOf` returns nil for `script_parse`.
- `validate.go`: `bad_schema` when `output_schema` or an `item_schema` value is not an object of type
  strings, or when an `item_schema` key is not an `object[]` field of `output_schema`. `bad_script` comes
  from `scriptrt.Compile`. `unknown_agent` checks `fallback_agent` through `AgentExists`. Templates in
  `script` are not rendered: `Render` skips the `script` key for this type, so `{{` in JS is safe.

### Contract check (`scriptcheck.go`)

```go
type DriftError struct{ Code, Message string } // script_error|schema_drift|empty_drift|item_drift
func CheckContract(params map[string]any, v any) (any, *DriftError) // coerce, schema, non-empty, items
func Agreement(expected, got any) (score float64, leaves int)
```

The non-empty rule: a `string` is empty when it trims to `""`; `array` and `*[]` are empty at length 0.
`allow_empty` names top-level fields. For item fields it uses `"<field>.<item field>"`.

### Engine (`engine_script.go`)

`invoke` dispatches `script_parse` to `scriptParseNode`:

1. Read the node params, the per-run script override (`ex.scripts[nodeID]`, set by regeneration in this
   run), and `timeout_ms`.
2. If the script is not empty: compile it (cached per run by hash), run it, and call `CheckContract`.
   On pass, return the output, `via=script`, and 0 tokens.
3. On drift with `on_drift=fail`, return the drift error. The ladder of spec 33 takes over.
4. Fallback: `AgentRequest{Agent: fallback_agent or guardian, Prompt: buildFallbackPrompt(...), Tools: []string{}}`
   through `callAgent`, then `finishSchema`, then `CheckContract`. A failure is returned as the node
   error with the fallback tokens. The ladder takes over.
5. If `on_drift=regenerate` and `regenAllowed(node)` is true, call `regenerate`. It makes at most 2 guardian
   calls and returns `(script, tokens, problems)`.
6. Record the decision, update the version (below), set `ex.scripts[nodeID]`, send the notice, and return
   the fallback output with `via=fallback` and tokens = fallback + regeneration.

`regenAllowed` counts `script_fallback` decisions with `fix.regen_rejected=true` for the node in the
last 24 h (`ListDecisions` by workflow, kind `script`). It returns false at 3, unless a version newer
than the third rejection changed this node's params. `ex.regenDone[nodeID]` limits regeneration to one
per run.

Saving: `saveScriptVersion(ctx, wfID, nodeID, oldScript, newScript, rev, baseline)` loads the current
version and checks that the node exists and that its `script` equals `oldScript`. It then merges the
params and calls `SaveVersion(author "guardian")`. On `ErrVersionConflict` it reloads once and checks again.
It returns `"", nil` or a drop reason. The run keeps its pinned `run.Version`; only `ex.scripts` changes
the rest of the run.

Notice throttle: `Engine.scriptNoticeAt map[string]time.Time` keyed by `wfID/nodeID`, guarded by the
engine mutex. It applies to `script_fallback` only. It is lost on restart; one extra notice after a
restart is acceptable.

Revert (`Answer` with choice `revert`): allowed only for kind `script`, action `script_update`, and
`choice == ""`. It saves a version with `script = fix.previous_script`, `script_rev = current + 1`, and
sets `choice = "revert"`, `resolved_at`.

### Prompts (`script_prompts.go`)

- `BuildFallbackPrompt(goal, schema, itemSchema, input)`: role line, goal, input (cut as REQ-SP-005), and
  "Answer with ONE JSON object only matching: <schema>". The item schema appears as "each element of
  <field> has: <schema>".
- `BuildScriptPrompt(ScriptPromptInput)`: role "You write a deterministic JavaScript parser for a lana
  workflow step." Then the goal, the contract, the sandbox rules (ES2020 as goja supports it, `function parse(input, ctx)`,
  no I/O, `html()` and `console.log` available, return an object, never hardcode the sample's values,
  prefer structural anchors such as headings, link patterns, or JSON keys over line numbers), the
  input, the expected output, the old script and its error (regeneration), the problems of a previous
  attempt, and the reply contract API-SP-008. For convert, it adds the agent prompt and asks for `goal`,
  `input` (a template that uses the prompt's `{{ }}` references) and `output_schema`. It sets
  `feasible=false` when the step needs judgment.
- `ParseScriptReply(text)` uses `ExtractJSON`. It requires `script` when `feasible` is true.

### Service (`service_script.go`)

- `TestScript(ctx, wfID, node, input *any, run int) (TestResult, error)`
- `GenerateScript(ctx, wfID, node, input *any, run int, expected map[string]any) (GenerateResult, error)`
- `ConvertNode(ctx, wfID, nodeID) (ConvertResult, error)`
- `ScriptSuggestions(ctx, wfID) ([]Suggestion, error)`: reads runs of the last 30 days (`ListRuns`) and
  their node runs. It caps at the newest 200 runs.
- `ScriptStats(ctx, wfID) map[string]ScriptStat`: the same scan over all stored runs of the current
  `script_parse` nodes, capped at 1000 runs; `last_update` comes from the newest `script_update` decision.

Sample resolution: the request input, else the stored rendered `input` param of that node in run `run`,
else in the newest run that has the node, else `no_sample`. When the node did not run (new node), it uses
the rendered `input` template against the newest run's node outputs (`Render` with a scope rebuilt from
stored node runs).

### Changes found in the live check

- `SampleExcerpt(sample, expected, 24 KiB)` replaces the fixed head/tail cut in `BuildScriptPrompt`:
  the window centers on the first place where a value of the expected output occurs. On the 58 KB
  GitHub trending page the entries start after 16 KiB of navigation, so the old cut showed none.
- `normalizeConvertInput` reduces a proposed convert `input` to its `{{ }}` reference(s) before the
  per-run checks.
- The script prompt asks for readable code (one statement per line, 2-space indent, anchor comments).
- `evalScript` is shared by the engine candidate check and the Test/Generate service calls.
- The decision-answer route keeps `decision_resolved` for a second revert (API-WF-015).

## Persistence

Next free migration (`00NN_workflow_node_via.sql`):

```sql
ALTER TABLE workflow_node_runs ADD COLUMN via TEXT NOT NULL DEFAULT '';
```

`store/workflow.go` reads and writes `via`. `memstore.go` keeps it. Decision kinds are free text; there
is no migration.

## Web (`web/workflows_script.go`)

Four routes, API-SP-002..005, behind the existing workflow port. The body limit is the workflow body
limit (raised to 4 MiB for `script/test` and `script/generate` because of pasted samples). The
workflow detail handler adds `script_stats`. `handleWorkflowAnswer` passes `revert` through.

## Web UI (`web/ui`)

| File | Change |
|---|---|
| `views/workflow-catalog.ts` | `script_parse` entry: icon `code-brackets`, fields, defaults `{input:"", goal:"", output_schema:{}, script:"", on_drift:"regenerate"}`, subtitle |
| `views/workflow-inspector.tsx` | `ScriptParsePanel`: code editor (existing `wf-code` textarea, mono, line count), Generate/Test with a sample picker, result panel (output JSON, problems, logs, duration), stats card, script history with Revert; agent/bot/worker banner "Convert to Script Parse · ~N tok/run" |
| `views/workflow-script-convert.tsx` | convert preview dialog: goal, input, schema, script, per-run checks; **Replace node** patches the draft graph |
| `views/workflow-canvas.tsx` | suggestion badge on nodes; dismissal in `localStorage` (try/catch) |
| `views/workflow-runs.tsx` | `via` chip per node row; `script` decisions in the run timeline |
| `api/workflows.ts` | client calls and types |
| `views/workflow-templates.ts` | the "GitHub trending digest" template uses `script_parse` + `message` |

## Assistant

`BuildAssistantPrompt` adds three rule lines (REQ-SP-020). The catalog line already comes from
`nodeCatalog`.

## Test strategy

- `internal/scriptrt`: sandbox globals, timeout, cancel, size caps, the `html` helper on fixtures, error
  line numbers, `Compile` without `parse`.
- `internal/workflow`: `CheckContract` table tests, `Agreement`, prompt golden files, and engine tests
  with a fake `Deps` (scripted agent replies) for AC-SP-002..007. Validation for AC-SP-011.
- `store`: `via` round trip.
- `web`: route tests for API-SP-002..006 and `script_stats`.
- UI: vitest for the panel, convert dialog, badge and `via` chip; `tsc`, eslint, `make web-check`.
- Live: a temporary daemon with DeepSeek runs the GitHub trending workflow twice, then a drift fixture
  (a bash node that prints a changed page), and checks tokens and decisions (AC-SP-002, 003).

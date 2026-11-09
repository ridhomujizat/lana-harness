# Design: Script drift guard

- **Status**: Approved 2026-11-09 · **Requirements**: [requirements.md](requirements.md)

## Architecture decision scan

No new dependency, table or migration. Node outputs already live in `workflow_node_runs.output_json`,
`via` is a free TEXT column (migration 0010), and decisions already carry kind `script`. No ADR.

## Package `internal/workflow`

### Rules (`schemarules.go`)

- `ParseTypeSpec(spec string) (typ string, rules []string, note string)`: the first word is the type;
  then words in the rule vocabulary (`>0`, `>=0`, `url`, `min:N`, `max:N`, with N a non-negative
  integer) are rules until the first other word.
- `RuleProblems(schema, itemSchema map[string]any) []string` for validation (`bad_rule`).
- `CheckRules(params map[string]any, out any) *DriftError`: walks `output_schema` and `item_schema`
  like `CheckContract`. Code `rule_drift`, message `field "repos.stars_today" item 3: 0 is not >0`.
- `DropRules(params map[string]any, violated []RuleRef) map[string]any`: returns params with the
  tokens removed (deep copy). `RuleRef{Path, Token}`; `CheckRules` reports every violated ref via
  `RuleViolations(params, out) []RuleRef`, and `CheckRules` wraps the first one.
- `MergeRules(params, rules map[string]string) map[string]any` and
  `KeepHoldingRules(params, rules, outputs ...any) map[string]string` for REQ-DG-006. A rules map key
  is a field path, the value one or more tokens separated by spaces.

### Profile (`scriptprofile.go`)

- `CheckProfile(params map[string]any, baseline []any, out any) *DriftError`: nil when
  `len(baseline) < 2`. Signals and thresholds as REQ-DG-002, as named constants. Field paths come
  from `output_schema` (top level) and `item_schema` (items). Messages:
  `field "repos.stars_today": 9/9 values are 0, baseline 0/45`.
- String class: `url` = `^https?://\S+$`, `number` = `^[-+]?[\d.,]+%?$`, else `text`.
- Pure functions; table tests plus the live fixture `testdata/trending_profile.json` (AC-DG-001).

### Engine (`engine_script.go`)

Flow for a non-empty script, after `runScript` passes the contract:

1. `CheckRules` → `rule_drift`.
2. `CheckProfile(params, ex.baseline(ctx, node.ID), out)` → `profile_drift`.
3. No drift and `audit_every` > 0 and `ex.scriptRunsSinceCheck(node.ID)+1 >= N` → audit.

`ex.baseline` reads `ListRuns(WorkflowID, Limit 30)` and `ListNodeRuns`, skips the current run,
keeps good outputs at or after the newest `profile_accept` decision run number of the node (from
`ListDecisions`), newest first, up to 5. It also returns the count for the audit (script runs since
the newest fallback or audit run). It is cached per node in `runExec`.

Fallback stays as spec 44. Fallback outputs pass `CheckContract` only (no rules). After the
fallback:

- Soft drift and `confirms(fallbackOut, scriptOut)` (the spec 44 agreement rule, shared with
  `checkScriptCandidate`):
  - `profile_drift` → decision `profile_accept` (Fix: drift, error, agreement), no notice, no regen.
  - `rule_drift` → `saveRuleDrop` (new version without the violated tokens, author guardian, base
    version check as `saveScriptVersion`), decision `rule_drop` (Fix: drift, error, rules), notice.
  - The node output is the fallback output, `via=fallback`.
- Audit: the script output already passed; run the fallback. Confirmed → output the script output,
  `via=audit`, tokens = fallback tokens, log `audit ok, agreement 0.97`. Not confirmed → drift
  `audit_drift`, continue with `afterFallback` as for any drift.
- Otherwise `afterFallback` as spec 44.

Regeneration candidates must also pass `CheckRules` (in `evalScript`).

### Prompts and service

- `BuildScriptPrompt` asks for an optional `"rules": {"<field path>": "<tokens>"}` and lists the
  vocabulary with the advice "only for values that can never break the rule on real data".
  `ScriptReply.Rules map[string]string`.
- `GenerateScript` and `ConvertNode`: `KeepHoldingRules` against the expected output and the script
  output, then `MergeRules`. `GenerateResult.OutputSchema/ItemSchema` are set when rules were kept.
- `TestScript` adds rule and profile problems (`rule_drift`, `profile_drift`) using the same baseline
  reader on the service store.
- `ScriptStats.AuditRuns`; `ViaAudit = "audit"`; validation of `audit_every` (`bad_param`, 0..1000)
  and `bad_rule`.
- Assistant rules: two lines on rule tokens and `audit_every`.

## Web UI

- `api/workflows.ts`: `audit_runs`, `GenerateResult.output_schema/item_schema`, via `audit`.
- Script tab: "Audit every N runs" number field (0 = off) with "each audit costs one fallback",
  events text for `profile_accept` ("data changed, script confirmed") and `rule_drop`, drift code
  labels; Generate applies the returned schema.
- Runs: `audit` via badge.

## Test strategy

Pure tests for rules and profile (with the live fixture), engine tests with fake deps for each branch
of the flow (AC-DG-003..006), service tests for rule keeping, vitest for the UI, then the live check
on the scratch daemon (AC-DG-002).

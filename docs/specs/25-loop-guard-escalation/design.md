# Design: Loop guard escalation and tool-name recovery

- **Requirements**: revision 2 · **Contract**: revision 1
- **Status**: Approved

## 1. Result digest (REQ-LPG-003–005)

`core.ToolOutput` gains `Digest string \`json:"-"\``. `agent/toolrunner.go` sets it to the hex
SHA-256 of `status + "\x00" + output` for every call, before `spillOutput` runs. The spill preview
carries a random file name, so a digest taken after spill would never match. A blocked call's
digest is computed by the guard the same way from its synthetic result.

## 2. Loop guard (REQ-LPG-001–009, -011–013)

`agent/loopguard.go` keeps its existing chains and adds, per turn:

```go
failBySig    map[string]int            // REQ-LPG-002
sameBySig    map[string]sameResult     // REQ-LPG-003: {digest string; count int}
chainDigests []string                  // digests of the current repeat chain (REQ-LPG-004)
history      []historyEntry            // last 64 tracked calls {key, digest} (REQ-LPG-005)
strikes      int                       // REQ-LPG-007
halt         *loopHalt                 // first halt cause of the step: {kind, tool, count}
```

Functions:

```go
// before decides one call before the step runs. A blocked call gets its
// synthetic error output, and the step is marked for halt.
func (g *loopGuard) before(call core.ToolCall) (core.ToolOutput, bool)
// observe is unchanged in shape; it now also reads Digest, runs the new
// detectors, and records g.halt. It returns the reminder text.
func (g *loopGuard) observe(calls []core.ToolCall, outputs []core.ToolOutput) string
// strike records a step whose calls all had unknown names (REQ-LPG-007).
func (g *loopGuard) strike(allUnknown bool)
```

- `before` checks, for a tracked call: `failBySig[key] >= 4`; read-only and `sameBySig[key].count >= 4`;
  `key == lastKey && repeat == 8` and all `chainDigests` equal. First match wins. `before` does not
  advance any counter; `observe` treats the blocked call like any error result.
- `reset()` also clears the new state.
- A progress write in `observe` clears `failBySig` and `sameBySig`.
- Cycle check after each step: for period p in 2..4, count full laps backward from the end of
  `history` where entry `i` equals entry `i-p` (key and digest). Laps 3 → reminder (once per period
  and lap count), laps 5 → halt.
- Several reminders of one call are deduplicated by text and joined by a blank line (as now).

`agent/turn.go`, in the tool branch:

1. For each call: `out, blocked := guard.before(call)`. Blocked calls skip the pipeline; the others
   go to `rn.reg.pipeline.Run` as now. Merge the outputs back in call order.
2. Store results as now. `pendingReminder = guard.observe(calls, outputs)`; `guard.strike(...)` from
   the unknown-name flag (§3).
3. If `guard.halt != nil`: log `loop guard halt`, replace `pendingReminder` with the halt note
   (REQ-LPG-009), set `loopHalt = true` and `budgetReport = true` so the next step offers no tools.
   When the report step ends normally, the reason is `loop` (not `max_steps`). A claimed inbox
   message is pushed back as the `max_steps` path does.

No new events. The halt note is a runtime note (`UserMessage.Runtime`).

```mermaid
flowchart LR
  call[tracked call] --> before{before}
  before -- blocked --> err[error result] --> halt
  before -- run --> run[pipeline] --> observe{observe}
  observe -- reminder --> note[runtime note, next step]
  observe -- halt --> halt[halt note + step without tools] --> end[turn/end loop]
```

## 3. Tool names (REQ-LPG-020, -021)

New `agent/toolname.go`:

```go
// repairToolName returns the single offered name that name normalizes to, or "".
func repairToolName(name string, offered []string) string
// unknownToolText returns the REQ-LPG-021 error text.
func unknownToolText(name string, offered []string) string
func similarity(a, b string) float64 // 1 - levenshtein/max(len)
```

`agent/toolrunner.go` `Run`, at the `!allowed[call.Name]` check: try `repairToolName`; on a match set
`state.call.Name` to it, log `tool name repaired`, and continue to the registry lookup. Otherwise the
output is `unknownToolText`. The "not registered" path uses the same text. `Run` reports whether
every call of the step had an unknown name, through a new `ToolOutput` field
`UnknownName bool \`json:"-"\`` that the turn reads for `guard.strike`.

The assistant message and the `tool_call` event keep the original name; only execution uses the
repaired name. The guard key uses the name as the model wrote it (the turn's `calls`); a repeated
wrong name keeps the same key, so detection is unchanged.

The "Did you mean" score is the best similarity of the normalized name against the whole offered
name, its MCP `<tool>` part, and each `_`-separated word of it. The whole-name score alone gives
`read` vs `read_file` 0.44, below the 0.7 cutoff of AC-LPG-022.

## 4. Surfaces (REQ-LPG-010)

`lana.go` `StopLoop`; the six texts of API-LPG-003. The web turn card for `loop` reuses the existing
Continue path; Telegram `endLine` adds the `loop` case, which already gets Continue for abnormal ends.

## Visual Architecture Check

| Trigger | Present? | Required view |
|---|---:|---|
| Lifecycle or state transition | Yes | §2 flowchart |
| All others | No | — |

## Verification

- `agent/loopguard_test.go`: one table test per AC-LPG-001–011.
- `agent/turn_loopguard_test.go`: AC-LPG-002 and AC-LPG-012 end to end with a scripted provider
  (block, report step without tools, reason `loop`).
- `agent/toolname_test.go`: AC-LPG-020–023.
- Web: `turn.test.tsx` case for `loop`. Telegram and TUI: existing end-line tests extended.
- `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; web `npm test` in the Nix shell.

## 5. Revision 2 (REQ-LPG-014, -015)

`agent/loopguard.go`:

```go
anyArgs map[string]int // key: tool + "\x00" + digest; read-only tools only (REQ-LPG-014)
```

- In `observeResult`, for a read-only tool with a success result that is not an empty search
  result (`grep` `no matches`, `glob` `no results`) and not empty (`""`): `anyArgs[key]++`, where
  key = tool + digest, plus `path.Clean(path)` from the arguments for `read_file`. At 3 send the
  REQ-LPG-014 reminder (once per key); at 8 `setHalt("read_only_any_args", tool, n)`.
- A progress write clears `anyArgs`; `reset()` clears it.
- Several reminders for one call stay deduplicated. When REQ-LPG-003 and REQ-LPG-014 fire for the
  same call, both texts may be sent (they differ).

`agent/turn.go` report step (REQ-LPG-015, also used by REQ-LTC-040):

- Remove the `if !budgetReport` guard around `rn.reg.tools.Specs(turnTools)`: the report step
  sends the same `specs` as earlier steps.
- In the tool branch, when `budgetReport` is true, do not call `guard.before` or the pipeline:
  every call gets `ToolOutput{Status: error, Output: "Tools are disabled for this final report; answer in text."}`
  (results stored and published as usual), then `goto finished` after the step end is written,
  keeping the step's text as the turn text. The reason stays `loop` or `max_steps` per the existing
  finalization.
- Update the comment "The report step of REQ-LTC-040 offers no tools".

Tests: `agent/loopguard_test.go` AC-LPG-013–015; `agent/turn_loopguard_test.go` AC-LPG-016 (the
mock provider records requests: the report request has the same tool names as the step before) and
AC-LPG-017; update existing tests that assert the report step has no tools (search
`agent/turn_retry_test.go`, `agent/turn_loopguard_test.go`, `agent/subagent_test.go` for it).

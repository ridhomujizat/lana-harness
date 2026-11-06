# Requirements: Loop guard escalation and tool-name recovery

- **Issue**: —
- **Status**: Implemented
- **Revision**: 2
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: 0009 (session as append-only event log)
- **Related diagram**: — (flow in [design.md](design.md) §2)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "gmn caranya harness bisa deteksi/negur LLM ketika dia stuck ngeloop, gimana harness ngehandle LLM halu dalam tool calling", then "boleh" to a spec for the gaps | goal |
| Owner answers 2026-10-31 | Escalation: "ikuti evonic dan hermes". Extra detection: cycle A-B-A-B and unchanged result. Wrong tool name: nearest name plus the list of tools | `Q-001`–`Q-003` |
| Real-model run 2026-11-01 | `deepseek-flash`, polling scenario: after the first reminder the model varied `limit` (10, 100, 50, 2, 3, …) and `status.txt` / `./status.txt`, so every signature was new; 42 reads returned the same text before the block. The halt report step, sent without tools, got 128 cached of 3 738 input tokens (the step before: 9 344 of 9 605) | problem (revision 2) |
| Owner request 2026-11-01 | "oke fix itu" to: count read-only results by tool and digest across arguments (reminder 3, stop 8, reset by a progress write, empty search results excluded) | `REQ-LPG-014` |
| Spec #23 | REQ-TRL-010–014 (repeat reminders 3/5/8, failure reminders 4/8, runtime-note delivery, "a reminder never ends the turn and never blocks a call") | revised by this spec |
| Spec #19 | REQ-LTC-040 (one final step without tools that asks for a status report) | reuse |
| Spec #16 | REQ-TFB-004/005 (end card and Continue for an abnormal end) | reuse |
| Hermes Agent | `NousResearch/hermes-agent` @ `c419cf7c`: `agent/tool_guardrails.py` (signature = name + sha256 of canonical args; result digest; same-args failure warn 2 / block 5; read-only no-progress warn 2 / block 5; identical streak notice 3 / halt 5; cycle periods 2–4, window 64, notice at 3 laps / halt at 5; a block halts the turn; tool list never changed; `tool_choice` never forced); `run_agent.py:1256-1263` (halt reply, "send `continue`"); `agent/turn_tool_validation.py:101-153` (name repair, 3 invalid-name strikes end the turn); `agent/conversation_loop.py:1126-1137` (unknown-name text with available tools) | reference design |
| evonic | `anvie/evonic` @ `87834dc`: `backend/agent_runtime/llm_loop.py:3066-3111` (canonical call key, one force-stop message, then the loop ends after 3 more tool calls); `quality_monitor.py:157-166` (unknown-name text, list capped at 12 names plus a total when over 15); `llm_loop.py:1170` (tool pruning mid-turn, which breaks the prefix cache) | reference design |

## Goal

When the model repeats work without progress, lana first warns it, then refuses the useless call,
then stops the turn with a short status report and a Continue button, so the owner never waits on a
turn that burns steps in a loop. When the model calls a tool by a wrong name, lana recovers the
obvious cases and otherwise tells the model the nearest correct name.

## Definitions

- **Tracked call** — any tool call except `ask_user`, `job_output`, and `job_list` (REQ-TRL-012).
- **Signature** — tool name plus canonical arguments (REQ-TRL-010).
- **Result digest** — SHA-256 of the result status and the full result text before spill.
- **Read-only tool** — `read_file`, `glob`, `grep`, `web_fetch`, `memory_read`, `skill_load`.
- **Progress write** — a successful `write_file` or `edit_file` call.
- **Block** — lana does not run the call and returns an error result in its place.
- **Halt** — lana ends the turn through one report step in which tool calls are not run (REQ-LPG-009, REQ-LPG-015).
- **Empty search result** — a successful `grep` result `no matches` or `glob` result `no results`.
- **Offered tools** — the tool names of this turn's request (`turnTools`).

## Scope

### Included

- New loop detectors, call blocking, and turn halt, on every channel and in child turns.
- A `loop` turn end reason with an end card and Continue.
- Tool-name repair and a better unknown-name error.

### Not included

- JSON repair or type coercion of tool arguments (Hermes has both; not requested).
- Repeated text in the model's reply (not selected by the owner).
- Detection across turns (Hermes and evonic have none).
- Removing tools from the offered list or forcing `tool_choice` (breaks the prefix cache).
- A config switch or per-agent thresholds.

## Behavior rules

All counters live in one turn. They reset at turn start and on an owner message claimed mid-turn
(REQ-TRL-014). Untracked calls neither count nor reset.

### REQ-LPG-001: Existing reminders stay

The repeat reminders (3, 5, 8) and failure reminders (4, 8) of REQ-TRL-010/011 are unchanged.
REQ-TRL-014 is revised: a reminder still never blocks or halts, but REQ-LPG-002–007 may.

### REQ-LPG-002: Same-arguments failure

Each signature counts its error results in the turn, consecutive or not. A success of that
signature or any progress write resets the count of every signature. At count 2 lana sends:
`<tool> has failed <n> times with identical arguments. This looks like a loop; inspect the error and change strategy instead of retrying it unchanged.`
A call whose signature already has count 4 is blocked (its 5th attempt).

- `AC-LPG-001`: Given `bash {"command":"make"}` failed twice, then the reminder is sent.
- `AC-LPG-002`: Given it failed 4 times (other calls between), when it is called again, then it does
  not run and the turn halts.
- `AC-LPG-003`: Given 3 failures, then a successful `edit_file`, then 2 more failures, then no block.

### REQ-LPG-003: Read-only no progress

For a read-only tool, each signature counts how many results had the same digest as its previous
result, consecutive or not. A different digest, an error result, or a progress write resets it to 1
for the new result. At count 2 lana sends:
`<tool> returned the same result <n> times. Use the result already provided or change the query instead of repeating it unchanged.`
A call whose signature already has count 4 is blocked.

- `AC-LPG-004`: Given `read_file` of the same unchanged path returned the same text 4 times, when it
  is called a 5th time, then it does not run and the turn halts.
- `AC-LPG-005`: Given an `edit_file` success on that file between the reads, then the count restarts.

### REQ-LPG-004: Identical streak

When the repeat chain of REQ-TRL-010 has length 8 and all 8 results have the same digest, the next
call with that signature (the 9th) is blocked. A chain whose results differ is not blocked.

- `AC-LPG-006`: 8 identical `bash` calls with the same output, then a 9th: blocked, turn halts.
- `AC-LPG-007`: 9 identical `bash` calls whose output differs each time: no block.

### REQ-LPG-005: Cycle

lana keeps the last 64 tracked calls (signature and digest). After each step it looks for a cycle
of period 2, 3, or 4 at the end of that history, where every position repeats with the same
signature and digest. At 3 full laps it sends:
`The last <laps> rounds repeated the same cycle of <period> tool calls (ending with <tool>) with identical arguments and identical results. Do not repeat the batch; change arguments, use a different tool, or proceed with what you have.`
At 5 full laps the turn halts. A period-1 run is the repeat chain, not a cycle.

- `AC-LPG-008`: A-B-A-B-A-B with identical results sends the reminder; A-B ×5 halts.
- `AC-LPG-009`: A-B ×5 where B's result changes each time: no reminder, no halt.

### REQ-LPG-006: Failure streak halt

When the failure streak of REQ-TRL-011 reaches 12, the turn halts after that step.

- `AC-LPG-010`: 12 consecutive error results (any tools): the turn halts.

### REQ-LPG-007: Invalid-name strikes

A step in which every tool call has an unknown or not-offered name (after REQ-LPG-020) is a strike.
A step with at least one valid name resets the strikes. At 3 strikes the turn halts.

- `AC-LPG-011`: 3 steps in a row with only unknown names: the turn halts.

### REQ-LPG-008: Blocked call

A blocked call does not run, does not ask for approval, and does not call the tool hooks. Its
result has status `error` and this text (Hermes wording):
- REQ-LPG-002: `Blocked <tool>: the same tool call failed <n> times with identical arguments. Stop retrying it unchanged; change strategy or explain the blocker.`
- REQ-LPG-003: `Blocked <tool>: this read-only call returned the same result <n> times. Stop repeating it unchanged; use the result already provided or try a different query.`
- REQ-LPG-004: `Blocked <tool>: the same call with identical arguments returned the same result <n> times in a row. Stop repeating it unchanged; use the result already provided or change strategy.`

Other calls of the same step run normally. A blocked call counts as a tracked call with status
`error`. Any block halts the turn after the step.

### REQ-LPG-009: Halt

After the step's results are stored, lana appends a runtime note (REQ-TRL-013) and runs one more
report step (REQ-LPG-015), as REQ-LTC-040 does. The note is:
`lana stopped this turn because the same work repeated without progress (<detail>). Do not call tools. Tell the owner in their language what you tried, what blocked you, and what you need from them to continue.`
`<detail>` names the detector, the tool, and the count. The turn then ends with reason `loop`.
An owner message already queued stays for the next turn. If the turn also reaches `max_steps`, the
first condition reached decides the reason.

- `AC-LPG-012`: After a halt the last step runs no tool, its text is shown, and `turn/end` has
  reason `loop`.

### REQ-LPG-010: End surfaces

A `loop` end shows the same card and Continue as other abnormal ends (REQ-TFB-004/005), with the
texts in [api-contract.md](api-contract.md). Continue starts a new turn with fresh counters.

### REQ-LPG-011: Everywhere

REQ-LPG-002–009 apply on every channel and in child turns. A halted child turn returns its report
to the parent like any finished child turn.

### REQ-LPG-012: Prefix cache

The guard never changes the offered tools, never sets `tool_choice`, and never edits a stored
message. Reminders and the halt note are appended runtime notes. A blocked call's result is a normal
appended tool result.

### REQ-LPG-013: Logs

Each block and halt writes one `slog.Info` line (`loop guard block` / `loop guard halt`) with
session, turn, detector, tool, and count.

### Revision 2

### REQ-LPG-014: Read-only same result across arguments

For a read-only tool, lana counts the results of that tool per digest in the turn, whatever the
other arguments, consecutive or not. For `read_file` the count key also holds the cleaned path
(`path.Clean`, so `./status.txt` and `status.txt` are one file), so different files with the same
text are not counted together. Error results, empty search results, and empty results (output
`""`) are not counted. A progress
write clears all counts. When a count reaches 3, lana sends:
`<tool> returned the same result <n> times with different arguments. Changing the arguments does not give new information; use the result already provided or try a different approach.`
When a count reaches 8, the turn halts after that step (REQ-LPG-009). The call is not blocked
before it runs, because its result is not known yet. The detector kind in logs and in the halt
`<detail>` is `read_only_any_args`.

- `AC-LPG-013`: `read_file` of `status.txt` with `limit` 10, 100, 50, … and `./status.txt`, all
  returning the same text: reminder at the 3rd result, halt after the 8th.
- `AC-LPG-014`: 8 `grep` calls with different patterns, all `no matches`: no reminder, no halt.
- `AC-LPG-015`: 5 reads of the same text, an `edit_file` success, then 5 more reads of the (now
  same) new text: no halt.
- `AC-LPG-018`: `read_file` of 8 different files with identical text, and of 8 empty files: no
  reminder, no halt.

### REQ-LPG-015: The report step keeps the tools

The report step of a halt (REQ-LPG-009) and of a step cap (REQ-LTC-040, revised here) sends the
same offered tools as the steps before it, so the provider prefix cache stays valid; only the
runtime note asks the model not to call tools. When the model still returns tool calls in that
step, lana does not run them: each gets an error result
`Tools are disabled for this final report; answer in text.` and the turn ends after that step
with the step's text and the same reason (`loop` or `max_steps`).

- `AC-LPG-016`: The report step's request carries the same tool list as the previous step.
- `AC-LPG-017`: A report step that returns a tool call does not run it; the turn ends with reason
  `loop` (or `max_steps`), and no further step runs.

### Tool names

### REQ-LPG-020: Name repair

When a call names a tool that is not offered, lana normalizes the name: trim spaces, quotes, and
`<`/`>`; lowercase; `-` and spaces to `_`; CamelCase to snake_case; strip a trailing `_tool` or
`tool`. It also matches an unprefixed name to the `<tool>` part of an offered
`mcp__<server>__<tool>`. If exactly one offered tool matches, the call runs as that tool. The stored
assistant message keeps the original name. lana logs `tool name repaired` with both names. Fuzzy
matches are never run.

- `AC-LPG-020`: `ReadFile`, `read-file`, and `read_file_tool` run `read_file`.
- `AC-LPG-021`: `create_issue` runs `mcp__github__create_issue` when only one server offers it, and
  is an unknown name when two do.

### REQ-LPG-021: Unknown-name error

When repair finds no single match, the result is an error with this text:
`Tool '<name>' does not exist. Did you mean '<nearest>'? Available tools: <list>.`
- `<nearest>` is the offered name with the highest similarity (normalized Levenshtein) of at least
  0.7; the sentence is omitted when none qualifies.
- `<list>` is the sorted offered names; when there are more than 15, the first 12 then
  `, ... (<n> total)`.
- A registered tool that is not offered gets the same text, so hidden tools stay hidden.
- An empty name gets: `Tool call rejected: the tool name was empty. To call a tool, use a valid name from your tool list; otherwise reply in plain text.`

- `AC-LPG-022`: `read` with `read_file` offered gives `Did you mean 'read_file'?`.
- `AC-LPG-023`: 20 offered tools give 12 names and `(20 total)`.

## Open questions

None. Agent recommendations, approved by the owner on 2026-10-31:

| ID | Decision | Basis |
|---|---|---|
| `Q-004` | Rekomendasi agent: halt is on for every channel, including web and TUI | evonic always ends; Hermes ends on non-interactive channels; lana runs mostly from Telegram and the daemon |
| `Q-005` | Rekomendasi agent: a halt ends with a model-written report, not a fixed text | REQ-LTC-040 precedent; Hermes uses the same pattern on its iteration limit; owners are non-technical and need their own language |
| `Q-006` | Rekomendasi agent: "unchanged result" follows Hermes (same arguments, same result, read-only tools), not "different arguments, same result". **Revision 2:** extended by REQ-LPG-014 after the real-model run showed argument variation evading the guard | neither reference detects the latter, and it flags normal searches; empty search results are excluded for that reason |
| `Q-007` | Rekomendasi agent: name repair runs only exact normalized matches; fuzzy matches are only suggested | running a fuzzy match (Hermes, cutoff 0.7) can run the wrong tool, for example a write instead of a read |
| `Q-008` | Rekomendasi agent (revision 2): the report step keeps the offered tools and does not run calls made in it (REQ-LPG-015) | Hermes sends tools on its summary call; the real-model run showed a full prefix-cache miss on the tool-less report step; owner asked to fix cache misses on 2026-11-01 |

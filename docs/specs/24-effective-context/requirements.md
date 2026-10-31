# Requirements: Effective context

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: — (tool inputs and prompt text; listed below)
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "yang di terapkan di evonic sepertnya bagus? … menghemat yg tidak perlu tapi tetep dapat yg di perlukan memory dan toolnya", then "bukan promp diet tapi effectiv … nnti modelnya ga selalu yg model kecil tapi model frontier juga", then "ya boleh implement" | goal |
| Incident 2026-10-31 | Session `06GS4XTS85JBYH88Z947SSYT60`: the owner said "hi" and the agent answered that the DBX MCP tool was not active, while DBX was connected with 25 tools. The prompt carried an always-on "When something blocks you" section about unavailable MCP servers and the full memory, which held negative tool notes and an outdated workaround ("nohup … & disown", superseded by spec 22). | problem |
| Eval 2026-10-31 | `lana-eval` baseline: task `long-context` with `context_window = 24000` ran to 123 000 input tokens with zero compactions. `agent/compaction/compaction.go` `trigger()` returns `W − O − B` = 24000 − 32000 − 16384 < 0 for a thinking model, and `run` treats `trigger <= 0` as "compaction off". All 8 tasks passed, so the set has no headroom to show improvements. | problem |
| evonic | `backend/agent_runtime/context.py` `_build_static_prompt`: each tool definition carries its own `system_prompt` that is added only when the agent has that tool; rules for `bash` (build in tmux, scratchpad) are added only when `bash` is assigned; memory is not injected — the prompt lists memory keys and the agent calls `recall` | reference design |
| Anthropic prompting guidance for frontier models | Frontier models follow instructions precisely; emphatic wording ("MUST", "always", "CRITICAL") makes them over-apply a rule. State the reason and the scope instead. | wording rule |

## Goal

Every request carries what the model needs for the current task and little else, for small and
frontier models alike: owner facts always, environment memory when relevant, tool rules only for
tools the agent has, worded so that a precise model does not over-apply them. Context limits are
honored for any window size.

## Requirements

### Prompt wording

- **REQ-ECX-001** Every fixed prompt section lana writes (persona templates excluded) states each
  rule with its scope ("when …") and, where not obvious, its reason. It avoids emphatic words
  (`MUST`, `ALWAYS`, `NEVER` in capitals, `CRITICAL`, `IMPORTANT`) and absolute triggers such as
  "after every message".

### Tool-scoped guidance

- **REQ-ECX-010** A tool may provide prompt guidance: a short static text about how to use it with
  the other tools. The head prompt has one `# Tool notes` section that holds the guidance of the
  tools offered in the turn, in tool-name order, and is absent when no offered tool has guidance.
  Guidance of a tool that is not offered never appears.
- **REQ-ECX-011** Guidance texts (initial set):
  - `edit_file`: read the file (or the relevant range) before editing; after a failed edit, read
    again instead of guessing; prefer one edit per logical change.
  - `bash`: long-running commands go through `run_in_background` and `job_output`; check a server
    with a request after it reports ready.
  - `ask_user`: ask when a decision belongs to the owner or the task is blocked; do not ask about
    things the tools can find out.
  - `memory_read` / `memory_write`: the memory rules of REQ-ECX-021/-024 (replaces the separate
    `# Memory capture` section).
  - `skill_load`: the current skills hint ("call skill_load before following a skill"); the skill
    list itself stays in `# Skills`.
- **REQ-ECX-012** The MCP blocker rules (report an unavailable or failing MCP server; do not call
  its API by hand) appear only when the agent has at least one MCP server configured. The general
  rules (say plainly when a request cannot be finished; mention tools or their status only when
  the request needs them or the owner asks) appear always.

### Memory

- **REQ-ECX-020** The owner file (`USER.md`, target `user`) is always in the head prompt in full,
  as today.
- **REQ-ECX-021** The agent memory (`MEMORY.md`, target `memory`) is shown in full while its size
  is at most 2 000 characters. Above that it is shown as an index: one line per entry,
  `m<n>: <first line of the entry, cut at 100 characters>`, numbered from 1 in file order, at most
  80 lines (the newest entries win when there are more), followed by one line saying that the
  full text comes from `memory_read` with `query` or `ids`.
- **REQ-ECX-022** The memory size limit rises from 4 000 to 32 000 characters. The owner file limit
  stays 2 000.
- **REQ-ECX-023** `memory_read` accepts `query` (string) and `ids` (array of `m<n>` or integers) in
  addition to `target`. With `ids` it returns those entries in full. With `query` it returns the
  entries ranked by the relevance score of REQ-ECX-025, best first, at most 10, each with its
  `m<n>` id. Without either it returns all entries, as today.
- **REQ-ECX-024** When the memory is shown as an index, the first user message of a turn gets up to
  3 relevant entries in full inside its `<context>` block (`relevant memory:` then
  `- m<n>: <entry>` lines), chosen by REQ-ECX-025 against the owner's message text. An entry
  already given this way earlier in the same session is not given again. Nothing is added when no
  entry reaches the minimum score. This keeps relevant memory reachable when the model does not
  call `memory_read`, and stays append-only for the prefix cache.
- **REQ-ECX-025** Relevance score: lower-case word tokens (letters and digits, length ≥ 3) minus a
  fixed stop-word list (English and Indonesian common words); score = sum over shared tokens of
  `1 + ln(N / df)` where `N` is the entry count and `df` the number of entries holding the token;
  minimum score 2.0. No model call, no external index.
- **REQ-ECX-026** Memory hygiene: the memory guidance (REQ-ECX-011) and the memory capture pass
  instruction tell the model to replace or remove an entry that the current turn showed to be
  wrong or outdated, including workarounds that a tool now covers, instead of adding a second
  entry. The capture pass may emit such an operation based on what the turn proved, not only on
  what the owner said.

### Compaction limits

- **REQ-ECX-030** The output reservation `O` is capped at `W / 4` and the headroom `B` at `W / 8`
  before the threshold is computed, so `T = floor(min(W × 0.8, W − O − B))` is positive for any
  window of at least 4 096 tokens. A window below 4 096 is raised to 4 096. The retained tail uses
  the same capped `O`. A policy with `T <= 0` logs a warning instead of silently skipping.

### Eval

- **REQ-ECX-040** `lana-eval` tasks may seed the agent's `MEMORY.md` and the owner's `USER.md`, and
  the runner writes the turn's final answer to `.lana-eval-answer.txt` in the workspace before the
  check runs.
- **REQ-ECX-041** New tasks:
  1. `memory-recall` — memory seeded with 40 entries (index mode); one says the build needs
     `BUILD_MODE=fast`; `build.sh` fails without it with a generic error; prompt "run the build and
     tell me the result"; passes when the artifact exists.
  2. `greeting` — prompt "hi"; the agent has an MCP-free tool set and seeded memory with tool
     notes; passes when the answer has no tool, MCP, or memory talk (case-insensitive check for
     `tool`, `mcp`, `memory`, `bash`) and is under 300 characters.
  3. `edit-indent` — a Python file with mixed indentation where the obvious `old_string` misses on
     whitespace; passes when tests pass (exercises edit hints).
  4. `blocked` — the task needs a command that always fails with a permission error the model
     cannot fix; passes when the answer reports the blocker and the turn used at most 12 tool calls.
  5. `long-context` keeps `context_window = 24000` and passes only when the session has at least
     one compaction and the collected words are right.
- **REQ-ECX-042** `lana-eval -models a,b` runs the whole set per model and prints one table per
  model plus a per-model total line.

## Out of scope

- Embedding-based memory search, a memory database, memory per session.
- Moving tool descriptions out of the tool declarations.
- Changing persona templates (`AGENT.md`) written by the owner.

# Design: Effective context

- **Status**: Approved
- **Requirements**: [requirements.md](requirements.md)

## 1. Tool guidance (REQ-ECX-010–012)

- `core/tool.go`: optional interface

  ```go
  // ToolGuidance is implemented by a tool that adds a short usage note to the
  // head prompt when it is offered (REQ-ECX-010).
  type ToolGuidance interface{ PromptGuidance() string }
  ```

  MCP and custom tools do not implement it.
- `tools/`: `edit_file`, `bash`, `ask_user`, `memory_write` (memory rules; `memory_read` returns
  "" so the text is not duplicated), `skill_load` implement it with static strings.
- `internal/agenthome/prompt.go`: `PromptInput` gains `ToolNotes []ToolNote` (`Name`, `Text`) and
  `HasMCP bool`. `BuildSections` adds `Section{Name: SectionToolNotes}` with
  `# Tool notes` and one `## <tool>` block per note, placed after `# Skills` and before
  `# When something blocks you`. The `MemoryCapture` flag and `memoryCaptureRules` section are
  removed; that text becomes the `memory_write` guidance (REQ-ECX-011, rewritten per REQ-ECX-001,
  REQ-ECX-026).
- Blocker section: split `blockingRules` into general rules (always) and MCP rules (only when
  `HasMCP`). Rewrite the wording per REQ-ECX-001. Section name stays `SectionBlockers` so the
  `<system-update>` diff treats it as a changed section.
- `agent/turn.go`: where `turnTools` is final (after `filterDisabledTools` and sort), collect
  guidance: for each name, `rn.reg.tools.Get(name)`; if it implements `core.ToolGuidance` and the
  text is non-empty, append. Pass `HasMCP: len(resolved.mcp) > 0`. `buildPrompt` signature takes
  these instead of the `memoryCapture` bool.
- Tests that pin the full prompt text (`internal/agenthome/agenthome_test.go`, `prompt_test.go`,
  `agent/prompt_test.go`, others found by grep for section headings) are updated.

## 2. Memory (REQ-ECX-020–026)

- `internal/agenthome`: `MemoryLimit = 32000`. New `memory.go`:
  - `MemoryInlineLimit = 2000`, `MemoryIndexMax = 80`, `MemoryIndexLine = 100`.
  - `MemoryView(entries []string) (text string, indexed bool)` — full section text or the index
    (`m<n>: …`, newest 80 when more, trailing line
    `Full text: memory_read with query or ids.`).
  - `Score(query, entry string, df map[string]int, n int) float64`, `Tokens(s string) []string`
    with a fixed stop-word list (English and Indonesian), `Rank(query string, entries []string)
    []Ranked` (`ID int` 1-based, `Score`), minimum score constant `MemoryMinScore = 2.0`.
- `BuildSections`: the memory section uses `MemoryView`.
- `tools/memory.go`: `memory_read` input gains `query` and `ids` (strings `m3` or integers);
  outputs use `m<n>` ids; property descriptions per spec 23 style.
- `agent/turn.go`: when building the turn's first user message `<context>` block and the memory is
  indexed, rank entries against the owner's message text, drop ids in a per-session set
  `rn.memoryShown` (in memory on the Runner; lost on restart, accepted), take the top 3 with
  score ≥ minimum, add them to `PromptInput` as `RelevantMemory []string`, record the ids.
  `ContextBlock` renders `relevant memory:` lines after `date`.
- `agent/memcapture/instruction.go`: add the hygiene rule (REQ-ECX-026) and allow ops based on what
  the turn proved. In index mode the instruction still lists all entries (capture needs exact
  `old_text`).

## 3. Compaction limits (REQ-ECX-030)

`agent/compaction/compaction.go` `resolvePolicy`: after `window`, `output`, `headroom` are set,
`if window < 4096 { window = 4096 }`, `output = min(output, window/4)`,
`headroom = min(headroom, window/8)`. `run`: when `trigger <= 0`, `slog.Warn` once per session.
Tests: window 24000 thinking model → T = min(19200, 24000 − 6000 − 3000) = 15000; window 1 048 576
unchanged; window 1000 → 4096.

## 4. Eval (REQ-ECX-040–042)

`cmd/lana-eval`: `evalTask` gains `Memory []string`, `User []string` (written to
`<home>/agents/eval/MEMORY.md` and `<home>/USER.md` with the `§` separator — use
`internal/agenthome` paths and join helpers), and `Tools []string` (override of the default tool
list). After `rt.Run`, write `Result.Text` to `<workspace>/.lana-eval-answer.txt`. New tasks per
REQ-ECX-041; `long-context` check also reads the compaction count (runner passes it as env
`LANA_EVAL_COMPACTIONS` to the check). `-models` (comma list, overrides `-model`): run all tasks per
model; JSON report holds one entry per model; `-compare` matches by model and task.

## 5. Verification

- Unit: guidance collected only for offered tools; `# Tool notes` absent without guidance; MCP rules
  only with MCP; wording test scans fixed sections for the banned emphatic words; memory view
  full vs index switch at 2 000; index cap 80 newest; ranking (shared rare token beats common;
  stop words ignored; min score); `memory_read` query/ids; context block relevant lines and
  no repeat within a session; compaction caps.
- Prefix: head prompt stays byte-stable across steps; relevant memory only in the first user
  message of a turn.
- Eval: full set on `deepseek-flash`; compare against `eval-spec23.json`.

# Requirements: Tool reliability and the eval runner

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: — (tool descriptions and inputs, one dev command; listed below)
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-31 | "menurutmu apa lagi yang bisa di tingkatkan dari harnessnya", then "oke boleh implement 1,2,3" | goal |
| Session `06GRWP55TNRKSACZKTR40BCPRM` (2026-10-31) | The model edited files with `bash` `printf`/`sed -i` (seq 104, 221, 226) and read them with `cat` instead of `edit_file`/`read_file`. It retried variations of the same failing dev-server command for ~12 steps (seq 109–169). | problem |
| Code review 2026-10-31 | Tool descriptions are one line (`tools/files.go`, `tools/search.go`, `tools/webfetch.go`). `edit_file` errors are bare `not found` / `found N times; add more context`, it has no `replace_all`, and it rewrites the file mode to 0644. `read_file` returns raw lines without numbers or a "more lines" notice. No guard detects a looping model. No repeatable measurement of harness quality exists. | problem |
| deepseek-harness | `packages/guard/repeat-tool-reminder` (exact-repeat reminders at 3, 5, 8, reset by a user message, appended as a model-visible message, tool result untouched), `packages/fs/tool-fs/src/{read,edit,write}.ts` (descriptions, `replace_all`, "use read, not cat"), `packages/shell/tool-bash/src/index.ts` (bash description), at `/Users/ridho/coding/project/rnd/deepseek-harness` | reference design |

## Requirements

### Tool descriptions and file tools

- **REQ-TRL-001** Every built-in tool (`bash`, `read_file`, `write_file`, `edit_file`, `glob`,
  `grep`, `web_fetch`, `job_output`, `job_list`, `job_kill`) has a description that says what it
  does, when to use it instead of another tool, and its main limits. Every input property has a
  `description` in the JSON schema. Descriptions are static strings (prefix cache, REQ-LTC-003).
- **REQ-TRL-002** The `bash` description tells the model not to use shell commands (`cat`,
  `head`, `tail`, `sed`, `awk`, `echo >`, `printf >`, heredocs, `grep`, `find`, `ls -R`) to read,
  search, create, or edit files, and names the dedicated tools. It keeps the REQ-JOB-016 sentence.
- **REQ-TRL-003** `read_file` prefixes each line with its 1-based number and a tab
  (`%6d\t`). When lines remain after the returned range, the result ends with
  `(Showing lines A–B of N. Use offset=B+1 to read more.)`. The description says the prefix is not
  part of the file and must not be copied into `edit_file`.
- **REQ-TRL-004** `edit_file` accepts `replace_all` (boolean, default false). With `false`,
  `old_string` must occur once; with `true`, every occurrence is replaced and the count is reported.
  `old_string` equal to `new_string` is an input error.
- **REQ-TRL-005** When `old_string` is not found, the error names the file and gives a recovery
  hint:
  - if it matches after ignoring differences in leading/trailing whitespace per line, the error
    says so, gives the line range, and shows those lines numbered as in `read_file`;
  - else, if the first non-blank line of `old_string` (trimmed) occurs in the file, the error shows
    up to 3 such line numbers with 2 lines of context each;
  - else the error says to read the file with `read_file` first.
  The error never exceeds 2 000 characters.
- **REQ-TRL-006** When `old_string` occurs more than once and `replace_all` is false, the error
  lists the line numbers of the first 10 matches and suggests more context or `replace_all`.
- **REQ-TRL-007** A successful `edit_file` returns `Edited <path>: replaced <n> occurrence(s).`
  followed by the changed region of the new file, numbered as in `read_file`, with 2 lines of
  context, at most 40 lines (for `replace_all`, the first changed region only).
- **REQ-TRL-008** `edit_file` and `write_file` keep the permission bits of an existing file.
  `write_file` creates missing parent directories, and returns `Created <path> (<n> bytes).` or
  `Overwrote <path> (<n> bytes).`.

### Loop guard

- **REQ-TRL-010** Exact repeat: inside a turn, a tool call with the same name and the same
  canonical arguments (JSON with object keys sorted) as the previous tracked call extends a repeat
  chain; any other tracked call starts a new chain of 1. At chain lengths 3, 5, and 8, a reminder
  is sent to the model. The first reminder is the deepseek-harness gentle text; later ones are the
  detailed text with tool name, count, and arguments capped at 500 characters.
- **REQ-TRL-011** Failure streak: inside a turn, consecutive tool results with status `error`
  form a failure streak; a successful result resets it. At streak lengths 4 and 8 a reminder is sent:
  the last 4 calls failed, read the errors, do not repeat a variation of the same approach,
  try a different approach or ask the user (`ask_user`) when blocked. When one batch of parallel
  calls crosses a threshold, one reminder is sent.
- **REQ-TRL-012** `ask_user`, `job_output`, and `job_list` are not tracked by either guard: they
  neither count nor reset.
- **REQ-TRL-013** A reminder is a runtime note (`UserMessage.Runtime`, REQ-MEM-021): it is
  appended after the step's tool results as a user message, never edits a tool result, is stored in
  the event log, is hidden in the web/TUI/Telegram views like other runtime notes, and is
  append-only for the prefix cache. When an owner message is waiting in the inbox, the owner
  message is sent and both chains reset; the reminder is dropped.
- **REQ-TRL-014** Both chains reset at the start of each turn and on every owner message claimed
  mid-turn. A reminder never ends the turn and never blocks a call.

### Eval runner

- **REQ-TRL-020** A developer command `go run ./cmd/lana-eval` runs a fixed set of eval tasks
  against a real provider, each in its own temporary lana home and workspace, with approval
  policy `trust`. Flags: `-model` (default `deepseek-flash`), `-provider-url` (default
  `https://api.deepseek.com/v1`), `-key-env` (default `DEEPSEEK_API_KEY`), `-run <regexp>` (task
  filter), `-out <file>` (JSON report), `-compare <file>` (earlier report), `-timeout`
  (per task, default 10m), `-keep` (keep temp dirs). It is not part of the `lana` binary.
- **REQ-TRL-021** An eval task has: name, setup (files written into the workspace, optional
  setup command), prompt (may contain `{{port}}` replaced by a free TCP port), agent overrides
  (e.g. `context_window`), and a check command run in the workspace after the turn; exit 0 is pass.
  The check runs before the runtime closes, so background jobs are still alive.
- **REQ-TRL-022** The set has at least these 8 tasks, using only `sh`, `python3`, and `curl`:
  1. `edit-small` — change one value in two files;
  2. `fix-bug` — make a failing `python3 -m unittest` suite pass without changing the tests;
  3. `rename-symbol` — rename a function across 4 files, tests still pass;
  4. `serve-background` — start an HTTP server on `{{port}}` that stays up, check with `curl`;
  5. `find-definition` — find a constant among ~200 generated files and write its value;
  6. `large-output` — find one error code in a 200 000-line log produced by a script (spill path);
  7. `recover-error` — a build script fails with an actionable error; the task passes only if the
     model follows it;
  8. `long-context` — read 30 files and collect one word from each, with `context_window` 24 000
     so compaction must run.
- **REQ-TRL-023** Per task the report records: pass/fail, check output (tail 2 000 chars),
  duration, steps, tool calls by name, tool errors, `bash` calls that read/search/edit files
  (heuristic on the command's first word: `cat`, `head`, `tail`, `sed`, `awk`, `grep`, `find`,
  `printf`, `echo` with `>`, heredoc), repeat/failure reminders sent, compactions, input tokens,
  cached input tokens, cache hit %, output tokens, and the turn end reason. Totals cover all tasks.
- **REQ-TRL-024** The command prints one table row per task plus totals. With `-compare`, each
  numeric column shows the delta to the earlier report. A provider error in one task marks that
  task `error` and the run continues. Exit code 0 when every task ran (pass or fail), 1 on setup
  errors.
- **REQ-TRL-025** Prices are optional flags `-price-in`, `-price-cached`, `-price-out` (USD per 1M
  tokens). When all three are set, the report adds cost per task and in total.

## Out of scope

- A persistent bash working directory or `workdir` input, a todo/plan tool, an unattended approval
  policy, web search, browser verification (later specs).
- Near-duplicate argument detection beyond the failure streak.
- Running evals in CI (needs a paid API key).

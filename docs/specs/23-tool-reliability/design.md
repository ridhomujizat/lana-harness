# Design: Tool reliability and the eval runner

- **Status**: Approved
- **Requirements**: [requirements.md](requirements.md)

## 1. Tool descriptions (REQ-TRL-001, -002)

Descriptions stay Go string constants next to each tool (`tools/files.go`, `tools/search.go`,
`tools/bash.go`, `tools/webfetch.go`, `tools/jobs.go`). Every schema property gets a
`"description"`. Wording follows deepseek-harness `tool-fs` and `tool-bash`. Targets:

- `read_file`: "Reads a UTF-8 text file and returns numbered lines (`<n>\t<line>`). Use this, not
  `cat`/`head`/`tail` in bash, to inspect files. Default 2000 lines from `offset`; read large files
  in parts. The number prefix is not part of the file: never copy it into edit_file."
- `write_file`: "Creates a file or replaces its whole content. Creates parent directories. To change
  part of an existing file use edit_file instead; never write files with bash `echo`/`printf`/
  heredocs."
- `edit_file`: "Replaces exact text in an existing file. Read the file first and copy `old_string`
  exactly, including indentation, without the line-number prefix. `old_string` must occur once
  unless `replace_all` is true; add surrounding lines to make it unique. Use this, not `sed` in
  bash."
- `glob`, `grep`: say they replace `find`/`ls -R` and `grep`/`rg` in bash; mention result caps.
- `bash`: "Runs a shell command (`sh -c`) in the workspace and returns stdout and stderr. Each call
  starts in the workspace directory. Use it for builds, tests, git, package managers, and running
  programs. Do not use it to read, search, create, or edit files: use read_file, grep, glob,
  write_file, and edit_file. For servers, watchers, and anything that does not exit, set
  run_in_background: true; never use &, nohup, setsid, or disown for that." Plus timeout default.
- `web_fetch`, `job_*`: one or two sentences each on use and limits.

Tests assert the key phrases (not whole strings) and that every property has a description.

## 2. read_file numbering (REQ-TRL-003)

`tools/files.go` read path: format each returned line with `fmt.Sprintf("%6d\t%s", n, line)`;
when `end < len(lines)` append `\n(Showing lines A–B of N. Use offset=B+1 to read more.)`.
`truncateText(…, 30000)` stays. Spill (`agent/toolrunner.go`) already exempts `read_file`.
Update tests that compare raw output (`tools/files_test.go`, any agent test reading a file).

## 3. edit_file (REQ-TRL-004–008)

`tools/files.go` `editFileTool.Call`:

1. Decode `replace_all`. Reject `old_string == new_string`.
2. `count := strings.Count`. `count == 1` or (`count > 1 && replace_all`): replace (`strings.Replace`
   with n = 1 or -1), write with the existing file's `Mode().Perm()`.
3. `count > 1 && !replace_all`: error with up to 10 match line numbers (line of each match start).
4. `count == 0`: `notFoundHint(content, old)` in a new `tools/edithint.go`:
   - whitespace-insensitive match: split both into lines, compare `strings.TrimSpace` per line over
     a sliding window of `len(oldLines)`; first hit → message with range and the numbered lines
     (cap 30 lines);
   - else first non-blank old line trimmed; collect up to 3 file lines whose trimmed text contains
     it; show each with 2 lines of context, numbered;
   - else `Read the file with read_file and copy old_string exactly.`
   Cap the message at 2 000 chars.
5. Success output: header line plus numbered new-file lines from (first changed line − 2) to
   (last changed line of the first replacement + 2), capped at 40 lines. Compute the changed range
   from the byte offset of the first match and the line count of `new_string`.

`writeFileTool`: `os.MkdirAll(filepath.Dir(path), 0o755)` (after the guard check), keep an existing
file's mode, return Created/Overwrote messages.

## 4. Loop guard (REQ-TRL-010–014)

New `agent/loopguard.go` (no new events, no config):

```go
type loopGuard struct {
    lastKey  string // name + "\x00" + canonical args
    repeat   int
    failures int
}
func (g *loopGuard) reset()
// observe processes one step's calls and outputs in order and returns the
// reminder text for the next request, or "".
func (g *loopGuard) observe(calls []core.ToolCall, outputs []core.ToolOutput) string
func canonicalArgs(raw json.RawMessage) string // unmarshal to any, json.Marshal sorts map keys; invalid JSON → raw string
```

- Untracked names: `ask_user`, `job_output`, `job_list` (skip both counters).
- Repeat thresholds 3, 5, 8 (gentle text at 3, detailed at 5 and 8, args capped 500 chars).
  Failure thresholds 4 and 8 (status `error`; success resets to 0). Texts from requirements and
  deepseek-harness, in English. If several fire in one step, join them with a blank line.
- `agent/turn.go` `runTurn`: one `guard loopGuard` per turn and `pendingReminder string`.
  After the tool results are appended: `pendingReminder = guard.observe(calls, outputs)`.
  In the `case pendingTools:` branch: `popInbox()`; if it returned an owner message,
  `guard.reset()` and `pendingReminder = ""`; else if `pendingReminder != ""`, claim it with
  `runtimeNote = true`, `claimed = true`, then clear it. A claimed inbox message in the `default`
  branch also calls `guard.reset()`.
- Runtime notes are already stored with `UserMessage.Runtime` and hidden in the views (spec 20).
  Log `slog.Info("loop guard reminder", session_id, turn_id, kind, count)`.

## 5. Eval runner (REQ-TRL-020–025)

New `cmd/lana-eval/` (package `main`), not imported by `cmd/lana`:

- `main.go`: flags, task loop, report writing, table printing, exit code.
- `tasks.go`: `type evalTask struct { Name, Prompt string; Files map[string]string; Setup, Check string; Agent map[string]any; Timeout time.Duration }` and the 8 tasks. Setup and check
  run with `sh -c` in the workspace; generated inputs (200 files, 200 000-line log, 30 notes) come
  from small `python3` or `sh` setup commands so the Go source stays short. `{{port}}` comes from
  `net.Listen("tcp", "127.0.0.1:0")` then close.
- `run.go`: per task, `os.MkdirTemp` home + workspace; write `config.toml` with
  `[providers.eval]` (`kind = "openai_compat"`, `base_url`, `api_key` from the env var named by
  `-key-env`, `timeout = "300s"`), `[agents.eval]` (`provider = "eval"`, `model`, `workspace`, tools
  `read_file write_file edit_file glob grep bash web_fetch`, `permission = "trust"`, task overrides).
  `lana.New(ctx, lana.Options{Home: home})`, `rt.Run(ctx, lana.Request{Agent: "eval", Prompt: …})`
  with the task timeout, run the check, then read the session events through the runtime's
  existing session event API (the one the web package uses) and compute metrics, then `rt.Close()`
  (kills jobs), then remove temp dirs unless `-keep`.
- Metrics from events: `step/end` usage (input, cached, output), `tool/call` names and `bash`
  command heuristic, `tool/result` status, `user/message` with `runtime: true` whose text starts
  with the reminder prefixes, `compaction/summary` count, `turn/end` reason.
- `report.go`: `type report struct { Model string; Started time.Time; Tasks []taskResult; Total taskResult }`
  JSON; table via `text/tabwriter`; `-compare` loads an earlier report and prints `value (±delta)`
  for numeric columns matched by task name.

## 6. Verification

- `tools`: read numbering and "Showing lines" notice; edit unique/multiple/replace_all/not-found
  (all three hint kinds)/same-string; mode kept on edit and overwrite; write creates dirs; success
  snippet shape; descriptions contain the key phrases; every schema property has a description.
- `agent`: loopguard unit tests (canonical args key order, thresholds 3/5/8 and 4/8, untracked
  tools transparent, success resets failures); turn test with a scripted provider that repeats one
  failing call 4 times: the 3rd step's request ends with the runtime-note user message after the
  tool result, events contain it with `runtime: true`; owner message in inbox drops the reminder.
- Prefix: existing cache-stability tests still pass (descriptions are constants).
- Eval: `go vet ./...`; `go run ./cmd/lana-eval -h`; a dry test with a fake provider is optional;
  one real run of `-run edit-small` by the orchestrator.

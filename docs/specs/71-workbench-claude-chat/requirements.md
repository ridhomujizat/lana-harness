# Spec 71: Claude Code chat view in the Workbench

Status: Approved r1, 2026-11-23. Owner: plan and design reviewed in chat on 2026-11-23 ("oke lanjut bikin spec dan implement semua fase"). Design: FigCraft page "Workbench · Claude Chat" in `design/lana-design-system.figcraft` (screens 1–10, sections 7a–7h).

## Problem

Claude Code runs in a Workbench pane as a terminal UI. On a phone, and for reading long turns, a terminal is hard to use: tool calls scroll by as text, diffs are hard to read, and answering a permission prompt means hitting the right key. The owner wants the same running Claude Code shown as a chat, switchable back and forth with the terminal, without restarting it.

## Decisions (owner, 2026-11-23)

1. One icon-only button in the pane header, left of the state chip, switches a Claude pane between Terminal and Chat. The icon shows the target view: a chat icon in Terminal, a terminal icon in Chat. Shortcut ⇧⌘J.
2. Both views show the same `claude` process. Switching never restarts or re-launches it.
3. The chat is built from plain elements, not Lana's agent chat components (turn, trajectory, question, approval), because the owner will redesign it.
4. The composer is an outer shell with 6 px padding around an inner field; the send button is round, inside the field. The bottom toolbar has: `+` attach, the permission mode, model · effort, and a context donut. There are no "Commands" or "Files" buttons: typing `/` or `@` opens a full-width, long list above the field.
5. The context, model, mode and `+` menus open right above their own button, over the field.
6. Hovering the context donut shows a breakdown; it offers "Compact now".
7. AskUserQuestion shows as a question card with tabs, radio or checkbox options, a Recommended tag and an "Other" text input. Plan mode shows the plan as markdown with approval options.
8. All UI text is English.
9. Light mode follows the app theme (sample in section 10 of the design).
10. Subagents and background tasks show in the chat too (owner, 2026-11-23: "soal subagent dan background service muncul juga ya").

## Defaults (recommended in the plan, accepted with "implement semua fase")

- **D1** A new Claude pane opens in Terminal. The choice is remembered per pane in the browser.
- **D2** On a phone-width screen (≤ 700 px) a Claude pane opens in Chat unless the owner picked Terminal for it.
- **D3** All phases ship together: transcript chat, composer, prompts answered from the chat, and polish.
- **D4** omp panes keep the button disabled with a tooltip ("Chat is not available for omp yet"). An omp adapter is out of scope.

## How it works

The chat never parses terminal text for the conversation itself. It reads Claude Code's transcript file, `<config dir>/projects/<encoded cwd>/<session id>.jsonl`, which Claude Code appends to as the conversation goes on. Things that are only on the screen (the live spinner line, the permission mode, an open dialog and its numbered options) are read from a headless terminal mirror the daemon keeps of the pane. Everything the owner does in the chat is turned into terminal input written to the same pty: text as a bracketed paste and Enter, keys as their escape sequences, slash commands as typed text.

No core protocol change: the daemon uses the existing `Get`, `Attach(view)` and `Attach(control)` calls of the Workbench core.

## Requirements

### Finding the conversation (Go)

- **REQ-WC-001** A new daemon-side package `internal/workbench/claudechat` finds the Claude Code conversation of a Workbench session from its shell pid. It scans the `sessions/*.json` files of the known Claude config dirs: `~/.claude`, every `~/.claude-swap-backup/sessions/*/` (claude-swap profiles), and the daemon's own `CLAUDE_CONFIG_DIR` when set. The list of dirs is cached for 30 s. A file qualifies when its `pid` is alive and the session's shell pid is an ancestor of it (parent chain, at most 16 steps). With several matches the newest `startedAt` wins. The result is `{pid, session_id, cwd, config_dir}`.
- **REQ-WC-002** The transcript path is found by globbing `<config dir>/projects/*/<session id>.jsonl`; the session id must be a canonical UUID (spec 60 `isClaudeID`). The lookup is cached for 2 s per Workbench session and re-run after that, so `/clear` (a new session id in the same pid file) switches the chat to the new transcript.
- **REQ-WC-003** When the session runs no Claude Code (the core reports `agent != "claude"`) the chat answers `available: false` with `reason: "no_agent"` or `"unsupported_agent"` (another agent). When Claude runs but no transcript is found yet, `reason: "waiting"`.

### Reading the transcript (Go)

- **REQ-WC-010** The reader reads the JSONL incrementally from a byte offset. A partial last line (no trailing newline) is left for the next read. Lines over 8 MB are skipped and counted as unknown.
- **REQ-WC-011** Only lines with `type` `user` or `assistant` whose `isSidechain` is not true make chat items. `isMeta` user lines are skipped. Every other line type is ignored silently, except that `permission-mode` / `mode` lines update the transcript mode. A line that fails to parse, or a user/assistant line of an unknown content shape, increments `unknown_lines`.
- **REQ-WC-012** Items, each with a stable `id` (the line `uuid`, plus `:<block index>` when one line holds several blocks):
  - `user`: a user line whose content is a string or text blocks. Its text, image blocks as `images: [{ref, media_type}]` (bytes are not inlined), and `at`.
  - `command`: a user string of the form `<command-name>/x</command-name>…<command-args>…</command-args>` → `{name, args}`.
  - `output`: `<local-command-stdout>…</local-command-stdout>` → `{text}` with ANSI escapes removed.
  - `assistant`: a text block → `{text, model, at}`.
  - `thinking`: a thinking block → `{text}` (empty when redacted).
  - `tool`: a `tool_use` block → `{tool_use_id, name, input, summary, status}`. `summary` is `{verb, target, meta}`: Read/Write/Edit/MultiEdit/NotebookEdit → the file path, Bash → the command, Grep/Glob → the pattern and path, WebFetch → the URL, WebSearch → the query, Task/Agent → the description, MCP tools (`mcp__server__tool`) → `server · tool`, anything else → the name. Write/Edit carry `diff: {old, new}` (each cut to 200 lines), Write with `old: ""`. `input` is the raw input with strings over 4 KB cut.
  - The matching `tool_result` (in a later user line) updates that tool item: `status` becomes `done` or `error` (`is_error`), `result: {text, lines, truncated}` with the text cut to 4 KB / 60 lines. A tool item without a result is `running`.
  - `question`: an `AskUserQuestion` tool_use → `{tool_use_id, questions: [{question, header, multi_select, options: [{label, description}]}], answered, answer}`; `answered` and `answer` come from its tool_result.
  - `plan`: an `ExitPlanMode` tool_use → `{tool_use_id, plan (markdown), state}`; `state` is `pending` without a result, `approved` or `rejected` from the result (`is_error` = rejected).
  - `divider`: a compact boundary (`subtype: "compact_boundary"` system line or `isCompactSummary` user line) → `{text: "Conversation compacted"}`.
- **REQ-WC-013** Meta from the transcript: the model of the last assistant line, its `effort` field when present, the last `permissionMode`, and context use from the last main-chain assistant `usage`: `used = input_tokens + cache_read_input_tokens + cache_creation_input_tokens`, plus `output_tokens`. The window is 1,000,000 when the model id contains `[1m]`, else 200,000.
- **REQ-WC-014** Turn timing: when an assistant line is followed by a user line that is a new prompt (not a tool result), the daemon emits a `turn_end` item after the last assistant item of that turn with `{duration_ms, output_tokens}` computed from the first and last line timestamps of the turn.
- **REQ-WC-015** Snapshot size: the first read of a transcript returns at most the last 400 items; `truncated_before: true` tells the client older items exist. `GET …/chat?before=<item id>` pages back 200 items at a time.

### Subagents and background tasks (Go)

- **REQ-WC-016** Background tasks: the reader keeps `meta.tasks`, a list of `{id, kind, tool_use_id, title, agent_type, status, summary, started, ended}`:
  - a `Bash` tool_use with `run_in_background: true` whose result line has `toolUseResult.backgroundTaskId` → kind `shell`, title = the command's `description` or its first line;
  - an `Agent` / `Task` tool_use whose result has `toolUseResult.agentId` → kind `agent`, title = `description`, `agent_type` = `subagent_type`; a synchronous agent (no `isAsync`) is `completed` when its result arrives;
  - status `running` until a `<task-notification>` with that `<task-id>` arrives (in a `queue-operation` line's `content` or in a user string), which sets `status` (`completed`, `failed`, `killed`) and `summary`; a `TaskStop` / `KillShell` tool_use with a successful result marks its task `killed`;
  - at most the 50 newest tasks are kept.
- **REQ-WC-017** Agent tool items carry `agent: {agent_id, agent_type, background}`. A subagent's own transcript is `<transcript dir>/<session id>/subagents/agent-<agent id>.jsonl` (with `agent-<id>.meta.json`). `GET …/chat/agents/{agent_id}` returns its items (sidechain lines are the content there), its meta (`model`, `finished` from the meta file) and `steps` (tool_use count). The agent id must match `^[A-Za-z0-9_-]{1,64}$`.

### Screen mirror (Go)

- **REQ-WC-020** While at least one chat stream is open for a session, the daemon keeps a view attachment to it and feeds the bytes into a headless `vt` emulator (same library as `internal/workbench/screen.go`), sized from the replay's OSC 7701 size markers and from the session's cols/rows. The mirror stops 30 s after the last stream closes.
- **REQ-WC-021** From the mirror the daemon parses, as pure functions tested against real Claude Code 2.1.330 screens (`internal/workbench/claudechat/testdata/screens/`):
  - **working**: the footer contains `esc to interrupt`;
  - **status**: while working, the last spinner line above the input box (`^[·✢✳✶✻✽*]\s+\S`, e.g. `✶ Flowing…`);
  - **mode**: from the footer: `manual mode on` → `default`, `accept edits on` → `acceptEdits`, `plan mode on` → `plan`, `auto mode on` → `auto`, `bypass permissions on` → `bypassPermissions`;
  - **effort**: from the `◐ medium · /effort` line;
  - **input**: the `❯` line between the two rules of the input box; a `Try "…"` placeholder or a dim suggestion (two spaces after `❯`) counts as empty;
  - **dialog**: a list of options in the bottom 30 lines, numbered (`❯ 1. Yes`) or not (the trust prompt), with the `❯` cursor marking the selected one. Its `options` are `{key, label, selected}`, `title` is the nearest line above ending in `?`, and `lines` are the box lines above the title (the command or file). `kind` is `permission` ("Do you want…"), `plan` ("Would you like to proceed"), `question` (header chips or "Enter to select"), else `other`. A permission dialog whose first visible option is 2 gets option 1 "Yes" back (a render overlap seen on real screens).
- **REQ-WC-022** `live.state` is the core's `agent_state` (`working`, `blocked` → needs you, `idle`). `live.dialog` is only reported while the state is `blocked` or a dialog was parsed.

### Input (Go)

- **REQ-WC-030** Writes go through a control attachment the daemon opens on first use and closes after 60 s without input. The remote control lock applies: every POST route refuses with `controlAllowed(r)` false exactly like the attach route (REQ-RA-031).
- **REQ-WC-031** `say`: text up to 64 KB. The daemon first sends Ctrl+U, which clears a draft in Claude Code's input (Ctrl+Y brings it back). Then it writes `ESC[200~` + text + `ESC[201~`, waits 80 ms and writes `CR`. Attachment paths in `paths` are appended to the text, space-separated, each shell-quoted when it has spaces. While a dialog is open, `say` answers 409 `dialog_open` unless `force: true`.
- **REQ-WC-032** `keys`: a list of named keys, each one of `enter`, `esc`, `tab`, `shift+tab`, `up`, `down`, `left`, `right`, `backspace`, `ctrl+c`, `space`, a single digit `0`–`9`, or `text:<string>` (up to 200 chars, typed as-is). Keys are written in order 30 ms apart. Anything else is 400 `invalid_key`.
- **REQ-WC-033** `mode`: target one of `default`, `acceptEdits`, `plan`, `auto`, `bypassPermissions`. The daemon presses Shift+Tab, waits up to 400 ms for the mirror's mode to change, and repeats until it matches, at most 6 presses. If it never matches, 409 `mode_unavailable` with the modes it saw.
- **REQ-WC-034** `model`: `{model?, effort?}`. While the state is `working`, 409 `busy`. Model aliases `opus`, `sonnet`, `haiku`, `fable`, or a full model id (`[a-z0-9.\-\[\]]{1,64}`); effort one of `low`, `medium`, `high`, `xhigh`, `max`. The daemon types `/model <m>` + Enter and/or `/effort <e>` + Enter (as `say` does).
- **REQ-WC-035** `commands`: the slash command list for the `/` menu. Built-in commands are a static list in the package (name + description, taken from Claude Code 2.1.330). User and project skills (`~/.claude/skills/*/SKILL.md`, `<cwd>/.claude/skills/*/SKILL.md`) and commands (`~/.claude/commands/**/*.md`, `<cwd>/.claude/commands/**/*.md`) are scanned; the name comes from the folder or file (subfolders join with `:`), the description from the frontmatter `description`, else the first non-empty line, cut to 120 chars. Cached 30 s per cwd. Each entry has `source`: `built-in`, `user skill`, `project skill`, `user command`, `project command`.
- **REQ-WC-036** Images: `GET …/chat/image?item=<id>&i=<n>` re-reads that transcript line and returns the decoded image bytes with its media type, `Cache-Control: private, max-age=3600`. Only base64 image sources are served; anything else is 404.

### HTTP (Go, `web` package)

- **REQ-WC-040** Routes under `/v1/workbench/sessions/{id}/chat…`, authenticated like the other Workbench routes, listed in [api-contract.md](api-contract.md). A session that does not exist is 404 `not_found`; the Workbench off is 503 `workbench_unavailable`.
- **REQ-WC-041** The stream is Server-Sent Events. The first event carries a full snapshot (`reset: true`); later events carry upserted items (new items and items whose status changed), meta and live changes. The daemon polls the transcript file size every 300 ms while a stream is open and pushes live changes at most every 250 ms. A comment line keeps the connection alive every 15 s.

### Web UI (TypeScript)

- **REQ-WC-050** The pane header gets the view button (decision 1) for sessions whose `agent` is `claude`. For `omp` and other agents with an adapter in spec 60 but no chat, the button shows disabled with the D4 tooltip. ⇧⌘J toggles the focused pane.
- **REQ-WC-051** In Chat view the terminal stays mounted but hidden (`display: none` is not used, so xterm keeps its size: the terminal is moved off-screen with `visibility: hidden`), so switching back is instant and nothing re-attaches.
- **REQ-WC-052** The chat column is centered, max 760 px wide. Items render as in the design: a session divider, user bubbles on the right (with image thumbnails), Claude's text as markdown (the shared `lib/markdown` renderer, not the agent chat components), "Thought for…" collapsed thinking, tool calls grouped per turn in a card ("N steps", a summary of new files / edits / commands) with each row expandable to its diff or output, a turn footer with duration and tokens and a copy button.
- **REQ-WC-053** While working: the live status line under the last group, and messages sent meanwhile show as queued (dashed bubble) until they appear in the transcript; "Cancel" on a queued message sends Esc only if it is the last one typed, otherwise it is just hidden.
- **REQ-WC-054** Needs you: a permission dialog shows the yellow card with the command (from the pending tool in the transcript when there is one, else the dialog lines) and the parsed options; clicking an option moves the ❯ cursor with the arrow keys from the selected option and presses Enter. A question shows the purple card from the transcript's question with tabs per question; choosing moves to the matching screen option and presses Enter, "Submit" sends Enter, "Other" moves to "Type something.", types the text and presses Enter. A plan shows the plan card with the parsed options. When no options were parsed, the card offers "Open terminal" only. The composer is locked while a dialog is open.
- **REQ-WC-055** Composer: Enter sends, ⇧Enter new line, Esc (when the field is empty) sends Esc to Claude, ⇧Tab cycles the mode. `/` at the start opens the command menu; `@` after a space or at the start opens the file menu (spec 66 `GET /v1/workbench/git/search` on the session cwd). Both menus are full width over the messages, up to 12 rows visible, with sections and keyboard navigation (↑↓, Tab/Enter insert, Esc close).
- **REQ-WC-056** Toolbar: `+` menu (Upload file…, Paste image from clipboard, Pick from the Files panel), mode menu (five modes with descriptions, check on current), model · effort menu (Opus / Sonnet / Haiku + effort segments), context donut with hover tooltip (used / window, cache read, new input, last output, left before auto-compact at 80 %, "Compact now" sends `/compact`). Menus open right above their button.
- **REQ-WC-057** Attachments: files dropped on the chat, pasted images and uploads go through `POST /v1/workbench/drops`; chips with thumbnails / progress show in the field; on send their paths are passed in `say.paths`.
- **REQ-WC-058** States: waiting (spinner + text + "Open terminal"), unsupported agent (button disabled), unknown lines banner ("N unknown transcript lines were skipped" + "Open terminal") when `unknown_lines > 0`, unreadable dialog fallback.
- **REQ-WC-059** Phone (≤ 700 px): D2, compact composer (`+`, mode, send), large option buttons.
- **REQ-WC-060** Light and dark themes use the app's tokens.
- **REQ-WC-061** An Agent tool call shows as a subagent row in its tool group: agent type, description, status (running spinner, done, failed), and when expanded the subagent's own steps and final report (`GET …/chat/agents/{id}`, refreshed every 2 s while it runs).
- **REQ-WC-062** A "Background" chip above the composer shows the number of running tasks (shells and agents); clicking it opens a list of the tasks with kind icon, title, status, elapsed time and summary. Finished tasks stay in the list, dimmed.

## Out of scope

- An omp (or any non-Claude) transcript adapter.
- Editing or rewinding earlier messages.
- Streaming partial assistant text (the transcript holds whole blocks; the live status line covers progress).

## Risks

- The transcript format is not a public API. The reader skips what it does not know and reports a count, and the terminal is always one click away.
- Screen parsing of dialogs depends on Claude Code's layout. Options that cannot be parsed fall back to "Open terminal".

# Spec 69: Agents report the worktree they work in

Status: Draft r1, 2026-11-22. Not approved yet. Implementation comes later (owner, 2026-11-22). Extends spec 66 (Workbench files, git changes and worktrees, model A).

## Problem

Spec 66 shows each Workbench pane's worktree in a chip and lets the Files and Changes panel follow the focused pane. It finds the worktree from the working directory of the pane's foreground process (spec 55 A4, `proc_pidinfo`). A Claude Code session never changes its own process directory. When the owner tells it to work in another worktree, it edits files there with absolute paths and runs `cd <worktree> && <command>` in its Bash tool, and Claude Code resets the Bash directory afterwards ("Shell cwd was reset"). The chip and the panel stay on the folder Claude Code started in.

On 2026-11-22 three `claude` processes in the lana-harness Workbench space all had the repo root (`perf/chat-payload`) as their directory, while they worked in `../lana-harness-worktree/figcraft-viewer` and `../lana-harness-worktree/workbench-files`. The owner could not watch those changes in the panel.

Lana's own agents have the same gap. An agent whose runtime is Claude Code (spec 38) or any other runtime can work in a worktree, and the chat shows nothing about which worktree or what changed.

## Decisions (owner, 2026-11-22)

1. **The agent reports the worktree itself.** It runs `lana workbench use <path>` when it starts working in another worktree, and `lana workbench use .` when it goes back. A hook that only reads `cwd` was rejected: the directory resets to the launch folder after each command.
2. **The agent learns this on its own.** A Claude Code `SessionStart` hook adds the instruction to the session context. No per-repo CLAUDE.md is needed.
3. **The instruction appears only in Lana Workbench terminals.** That includes `claude` typed by hand in a Workbench pane. It never appears in Ghostty, herdr, VS Code, Terminal.app or other terminals. A herdr or tmux started inside a Workbench pane counts as outside. `--append-system-prompt` from the Workbench launcher was rejected because a hand-typed `claude` would miss it.
4. **The instruction text is English.**
5. **All projects.** It works for any git repo the spec 61 path policy allows (home and temp folders), not only lana-harness.
6. **Chip and panel follow; the pane stays where it is.** Panes do not move between tabs or spaces on their own.
7. **Several sessions in different worktrees:** each pane has its own chip, and the panel follows the focused pane (spec 66 REQ-WG-072).
8. **Subagents:** the chip stays on the main session's worktree. Subagent worktrees show only in the worktree picker.
9. **After the session goes idle,** the chip stays on the last reported worktree, so the owner can review it.
10. **A worktree that does not exist yet:** the agent may create it, in the repo's worktree folder (spec 66 REQ-WG-030).
11. **Lana's own agents get the same feature in phase 2.** Phase 1 is the Workbench.

## Phases

- **Phase 1** — Workbench panes: REQ-WA-001 to REQ-WA-052.
- **Phase 2** — Lana agent sessions (chat): REQ-WA-060 to REQ-WA-072. Built after phase 1 is live.

## Requirements

### Phase 1 — Workbench terminal markers (Go, core)

- **REQ-WA-001** Each Workbench terminal gets `LANA_WORKBENCH_TTY=<pty slave path>` (for example `/dev/ttys012`) next to the existing `LANA_WORKBENCH_SESSION=<id>` (spec 55). `sessionEnv` strips an inherited `LANA_WORKBENCH_TTY` like it strips `LANA_WORKBENCH_SESSION`, so a core started from a Workbench terminal does not pass the old value on.
- **REQ-WA-002** A process is "in a Workbench pane" when both variables are set and its controlling terminal is the path in `LANA_WORKBENCH_TTY`. A herdr, tmux or screen session started inside a pane opens a new terminal, so programs inside it are not in the pane.

### Phase 1 — Claude Code hook (Go, CLI)

- **REQ-WA-010** `lana workbench hook session-start` is the hook program. It reads the hook JSON from stdin (`source` is `startup`, `resume`, `clear` or `compact`). When the process is in a Workbench pane (REQ-WA-002), it writes the hook output with `hookSpecificOutput.additionalContext` set to the text in REQ-WA-011 and exits 0. In all other cases it writes nothing and exits 0. It never calls the daemon, and it ends within 100 ms.
- **REQ-WA-011** The context text, in English, with `<id>` and `<root>` filled in:

  ```
  You are running inside a Lana Workbench pane (session <id>). The Workbench
  shows the files and git changes of the worktree you work in. When you start
  working in a git worktree other than <root>, run
  `lana workbench use <worktree path>` before your first edit there. When you
  go back to <root>, run `lana workbench use .`. To create a new worktree, run
  `lana workbench use --new <branch>`; it creates it in the repo's worktree
  folder and reports it. Subagents do not need to report.
  ```

  `<root>` is the worktree root of the session's start directory, or the start directory when it is not in a repo.
- **REQ-WA-012** The hook entry is a `SessionStart` command hook with no matcher, so it runs for every source. Its command checks the variable in the shell before it starts `lana`, so outside the Workbench no `lana` process runs:

  ```
  [ -n "$LANA_WORKBENCH_SESSION" ] && exec '<lana binary>' workbench hook session-start; exit 0
  ```

  `<lana binary>` is the absolute path of the installed `lana` (spec 36), so the hook works when `lana` is not on `PATH`.
- **REQ-WA-013** Install. The daemon adds the hook entry to `settings.json` of each Claude Code config folder Lana knows: `~/.claude` (or `$CLAUDE_CONFIG_DIR` of the daemon), and the folder of each Claude Code account (spec 56, `llmcall.ClaudeConfigDir`). It merges into the existing `hooks.SessionStart` list and keeps every other key and hook as it is. Lana's entry is found by its command containing `workbench hook session-start`. The daemon checks the entry on start and after an account is added, and fixes the binary path after an update moved it. Before its first change to a file it writes a copy next to it as `settings.json.lana-bak`. A `settings.json` that is not valid JSON is left alone and reported in the daemon log and the Workbench settings.
- **REQ-WA-014** The Workbench settings get a switch "Tell Claude Code it runs in the Workbench", on by default. Off removes Lana's entry from every folder in REQ-WA-013, and the daemon stops adding it. The setting is kept in `<LANA_HOME>/workbench-git.json` under a top-level `agent_hook` key (spec 66 REQ-WG-050 keeps its per-repo keys).
- **REQ-WA-015** A Claude Code session that was already running before the hook was installed does not get the context. The panel's worktree picker shows a hint on a pane whose agent has not reported and whose agent started before the install: "Run /clear in this session, or start it again, so it can report its worktree."

### Phase 1 — `lana workbench use` (Go, CLI)

- **REQ-WA-020** `lana workbench use <path>` reports that the session in this pane now works in the worktree that holds `<path>`. It reads the session id from `LANA_WORKBENCH_SESSION` and refuses with exit 2 and "not in a Lana Workbench pane" when REQ-WA-002 does not hold. It sends the absolute path to the daemon (REQ-WA-030), authenticated like the other daemon CLI calls (`daemon.secret`, API-WLG-008). On success it prints one line, `Workbench: <short branch> (<worktree root>)`, and exits 0.
- **REQ-WA-021** `lana workbench use .` (or `--clear`) removes the report: the pane goes back to its process directory (spec 66 REQ-WG-070).
- **REQ-WA-022** `lana workbench use --new <branch> [--base <branch>] [--no-fetch]` creates a worktree through the spec 66 create action (REQ-WG-031) with the repo of the session's start directory, a new branch, the default base (D2), fetch on, the folder from REQ-WG-030, and the copy candidates checked by default (REQ-WG-032). Then it reports it as in REQ-WA-020 and prints its path. An existing branch is used as is (`new_branch: false`) when `<branch>` exists and has no worktree yet.
- **REQ-WA-023** Errors go to stderr with exit 1 and say what to do: not a repo, forbidden path (spec 61 policy), daemon not running, Workbench off, branch already has a worktree (the message gives its path, so the agent can `use` it).

### Phase 1 — Daemon (Go)

- **REQ-WA-030** `PUT /v1/workbench/sessions/{id}/worktree` with `{path}` sets the session's reported worktree. The daemon resolves the path (spec 66 REQ-WG-002), keeps the worktree root and the time, and answers with the resolved worktree. `DELETE` on the same route removes it. 404 `not_found` for an unknown session, 404 `not_a_repo`, 403 `forbidden_path`.
- **REQ-WA-031** Reports live in the daemon, not in the core, so the core protocol does not change. They are kept in `<LANA_HOME>/workbench/worktree-reports.json`, keyed by session id, with the worktree root, the agent (`claude`, …) that was in the foreground when it reported, and the time.
- **REQ-WA-032** A report ends when the program that made it leaves the pane: the session's `agent` becomes empty or another agent starts, or the session exits or is killed. A Workbench restore of the same conversation (spec 60) keeps it: the report stays while the restored session resumes that agent.
- **REQ-WA-033** The daemon adds `work_dir` (the reported worktree root, omitted when there is none) to each session it serves on `GET /v1/workbench/sessions` and in the session events of `GET /v1/workbench/events`. A change of report sends a session event with the new value.
- **REQ-WA-034** Reporting a worktree of another repo than the session's start directory is allowed (an agent can work across repos); the chip then shows that repo's worktree.

### Phase 1 — Web

- **REQ-WA-040** The session's worktree is resolved from `work_dir`, else `cwd`, else `dir` (changes spec 66 REQ-WG-070). The pane chip, the panel's follow-focus (REQ-WG-072), the tab hover card (REQ-WG-073), the shared-worktree warning (REQ-WG-075) and the `in_use` checks of remove and clean up (REQ-WG-033, 034) all use this worktree.
- **REQ-WA-041** A chip that comes from a report shows a small "reported" mark (a dot ring, tooltip "Reported by Claude Code at 14:32"). A chip from the process directory looks as in spec 66.
- **REQ-WA-042** The worktree picker's "In this space" section lists a reported worktree under the pane that reported it, with "Clear report" to go back to the process directory.
- **REQ-WA-043** When a pane's worktree changes and that pane has focus, the panel switches worktree with a short toast "Following <short branch>". A pinned panel (REQ-WG-072) does not switch.

### Phase 1 — Other agents

- **REQ-WA-050** `lana workbench use` works from any program in a pane, so other agents (omp, Codex) can call it when told to. Giving them the instruction automatically (for example through a global `AGENTS.md`) is out of scope for this spec.

### Phase 1 — Tests

- **REQ-WA-051** Go: the hook output for each source inside and outside a pane, including a nested terminal with a different tty; the settings merge (empty file, existing hooks kept, Lana's entry found and updated, invalid JSON left alone, the backup written once); `use` with a path, `.`, `--new` and each error; the report store and its end rules (agent exit, another agent, kill, restore keeps it); `work_dir` in the session list and events.
- **REQ-WA-052** Web, vitest: the worktree order `work_dir` → `cwd` → `dir`, follow-focus on a report change, the pin, and the in-use check with a reported worktree. Live check in a Workbench pane: two Claude Code sessions report two worktrees, the chips and the panel follow, Ghostty shows no context.

### Phase 2 — Lana agent sessions

- **REQ-WA-060** A new lana tool `use_worktree` with `{path}` or `{new_branch, base}` and `{clear: true}` sets the work worktree of the agent's chat session, with the same resolve, create and errors as REQ-WA-020 to REQ-WA-023. It is offered to agents that have the file or shell tools. A Claude Code runtime agent (spec 38) gets it through the lana MCP tool set like the other lana tools.
- **REQ-WA-061** The agent's prompt head (`AppendSystemPrompt` for Claude Code runtime agents, the system prompt for the others) gets a short instruction to call `use_worktree` before working in a worktree other than its workspace. The Workbench `SessionStart` hook does not fire for runtime processes, because they have no `LANA_WORKBENCH_SESSION`.
- **REQ-WA-062** The chat session keeps its work worktree in the session record (persisted with the session). It does not end when a turn ends; it ends with `clear` or when the session is deleted.
- **REQ-WA-070** The chat header shows the worktree chip (spec 66 REQ-WG-071 style) when the session has a work worktree. Clicking it opens the Files and Changes panel on that worktree, reusing the spec 66 panel components, next to the chat.
- **REQ-WA-071** The spec 66 git routes answer when the Workbench is off: they do not need the terminal core. Only the session routes keep answering 503 `workbench_unavailable`.
- **REQ-WA-072** A Lana agent that runs a Claude Code session in a Workbench pane (a future "Lana agents in the Workbench" feature) is covered by phase 1, because that session runs in a pane.

## Out of scope

Detecting the worktree from edits or commands without a report. Moving panes between tabs or spaces by worktree. Auto-instructions for agents other than Claude Code. Reports from subagents (they may still run `use`, which then moves the chip). Stage, discard and commit (spec 66 D1).

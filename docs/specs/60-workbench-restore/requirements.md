# Spec 60: Workbench restore after reboot

- **Status**: Approved (owner, 2026-11-17: "oke lanjut"; earlier: "resume workbench jika habis restart laptop bisa di implement kah?", "termasuk session claude code omp dll bisa juga ga?", "ya boleh")
- **Branch**: `feature/workbench-restore`, from `development` (0.5.0-beta.9, 894cd98)
- **Builds on**: spec 55 (Workbench), especially REQ-WB-003, REQ-WB-014, REQ-WB-015, REQ-WB-031

## Problem

Workbench sessions live only in the memory of the core process (`lana workbench serve`). When the Mac restarts, or the core stops for any other reason, every session is gone. `layout.json` survives, so the Workbench page comes back with its spaces, tabs and splits, but every pane says "This terminal is gone." The owner then has to open each terminal again, `cd` to the right directory and find the right Claude Code or omp conversation by hand.

## Decisions (owner, 2026-11-17)

1. Sessions are restored **automatically** when the core starts, under their **old ids**, so the stored layout stays valid.
2. A session that ran a coding agent is restored as a **shell in the last working directory**, with the agent's **resume command typed into that shell**. Quitting the agent leaves the owner in the shell, as before the reboot.
3. First adapters: **Claude Code** and **omp**. Every other agent, and every program that is not an agent, gets the shell in its last directory only.
4. Scrollback from before the reboot is **not** restored in this spec.
5. Running processes cannot survive a reboot. A restore brings back the command, the directory and the agent conversation, not the in-memory state of a program.

## Persisted state

- **REQ-WR-001** The core keeps `$LANA_HOME/workbench/sessions.json` (mode 0600, written atomically: temp file in the same directory, then rename). It is a JSON object `{version: 1, sessions: [...]}`, ordered by `created`. Each entry:

  | field | meaning |
  |---|---|
  | `id` | session id |
  | `title` | title given at create time |
  | `command` | argv given at create time (resolved default shell when none was given) |
  | `dir` | directory given at create time |
  | `env` | extra env entries given at create time (not the full environment) |
  | `labels` | labels |
  | `created` | creation time |
  | `cols`, `rows` | last size |
  | `last_dir` | last known working directory of the foreground job (REQ-WR-003), else `dir` |
  | `exited` | true when the program had already exited |
  | `exit_code` | exit code when `exited` |
  | `agent` | agent label from agentdetect (`claude`, `omp`, ...) of the last agent seen in the session, `""` when none |
  | `agent_session` | that agent's conversation id (REQ-WR-010), `""` when unknown |
  | `agent_cwd` | the directory the agent ran in, `""` when unknown |
  | `agent_seen` | last time the agent was seen in the foreground |

- **REQ-WR-002** The core writes the file after `create`, `kill` (removal), `restart`, exit of a program, and whenever `last_dir`, `agent`, `agent_session` or `agent_cwd` changes. Writes are coalesced: at most one write per second, and a pending write is flushed before the core exits.
- **REQ-WR-003** `last_dir` comes from the same source as the session's `cwd` (spec 55 A4: OSC 7, else the foreground process directory). It is updated when that value changes and is a directory that exists.
- **REQ-WR-004** An entry leaves the file only when the owner closes the session (`kill`, REQ-WB-014). A core shutdown — SIGTERM at logout or reboot, `lana workbench stop`, the core restart after a self-update — kills the processes but **keeps** the entries, marking running ones as interrupted (`exited: false` stays in the file; it is what tells the next core to restore them).
- **REQ-WR-005** When an agent leaves the foreground (the owner quits Claude Code and is back at the shell), `agent`, `agent_session` and `agent_cwd` stay set for 10 minutes after `agent_seen`, then clear. A reboot shortly after quitting an agent still offers it back; an hour-old agent is not resumed into a shell where the owner moved on.

## Agent conversation ids

- **REQ-WR-010** While an agent is the foreground job (agentdetect gives its label and pid), the core looks up its conversation id at most once every 5 s, until found, and again when the agent's pid changes:
  - **claude**: read `~/.claude/sessions/<pid>.json`; use `sessionId` and `cwd` when its `pid` equals the agent pid.
  - **omp**: list the session files the agent pid has open (macOS `proc_pidinfo` / `PROC_PIDLISTFDS` + `proc_pidfdinfo` vnode paths, Linux `/proc/<pid>/fd`) and pick the one under `~/.omp/agent/sessions/` named `<timestamp>_<id>.jsonl`. When none is open, read the `type: "session"` header line of each `*.jsonl` under `~/.omp/agent/sessions/*/` changed after the agent started, and take the newest whose `cwd` equals the agent's directory. The id is the header's `id`; `agent_cwd` is its `cwd`.
  - **other agents**: no lookup; `agent_session` stays empty.
- **REQ-WR-011** `$HOME` is the owner's home for both paths; `CLAUDE_CONFIG_DIR` overrides `~/.claude` when set in the session's environment. A missing or unreadable file is not an error: the id stays empty and the next lookup tries again.
- **REQ-WR-012** The lookups are table-driven (one adapter per agent: how to find the id, how to build the resume command), so later adapters (cursor-agent, codex, ...) are one table entry and one function each.

## Restore

- **REQ-WR-020** On start, after reading `layout.json`, the core reads `sessions.json`. A missing file means nothing to restore. A file it cannot parse is renamed to `sessions.json.bad-<unix time>` and logged; the core starts with no sessions.
- **REQ-WR-021** Each entry with `exited: true` comes back as an exited session (same id, title, command, dir, env, labels, exit code), so the pane shows "Process exited N" and Restart, as before the reboot.
- **REQ-WR-022** Each entry with `exited: false` is started again under its id, with its title, env, labels and size:
  - **no agent, command is a shell** (`bash`, `zsh`, `fish`, `sh`, `nu`, by argv[0] basename, with or without `-l`): the original `command` in `last_dir` (falling back to `dir`, then `$HOME`, when the directory no longer exists). What ran inside the shell (a dev server, `lazygit`) is not re-run.
  - **no agent, command is not a shell** (the session was created to run `npm run dev` directly): not started. It comes back as exited with exit code -1, so the pane offers Restart and the owner decides.
  - **agent with an adapter** (`claude`, `omp`): the user's login shell (REQ-WB-010 default) in `agent_cwd` (else `last_dir`, `dir`, `$HOME`). Once the shell has printed its first output, or after 1.5 s, whichever comes first, the core writes the resume command and a newline to the pty:
    - claude: `claude --resume <agent_session>`, or `claude --continue` when the id is empty.
    - omp: `omp --resume <agent_session>`, or `omp --continue` when the id is empty.
  - **agent without an adapter**: the login shell in `agent_cwd` / `last_dir`, with nothing typed.
  When the original `command` was not a shell (the session was created to run `claude` directly), the agent case still starts a login shell, so quitting the agent leaves a shell.
- **REQ-WR-023** Session ids in the resume command are checked before use: claude ids must be a UUID, omp ids must match `^[0-9a-fA-F-]{8,64}$`. Anything else falls back to the `--continue` form. Nothing from the session files is ever typed into the shell except an id that passed this check.
- **REQ-WR-024** A restored session carries `restored` (the restore time) in its `Session` object until its program exits or it is restarted, and the core logs one line per restored session (id, agent, the command typed). The restore runs before the core accepts connections, so the first `list` already shows every restored session.
- **REQ-WR-025** The resume command is typed only once, at restore. `restart` of a session (REQ-WB-015) keeps its existing behavior and does not type it again.

## Starting the core at boot

- **REQ-WR-030** When the Lana daemon starts and `$LANA_HOME/workbench/sessions.json` has at least one entry with `exited: false`, the daemon calls `workbench.Ensure` in the background (it does not delay daemon start; a failure is logged). Without such an entry the core still starts lazily, as today.

## Protocol and web UI

- **REQ-WR-040** `Session` gets `restored` (timestamp, omitted when not restored) and `resumed_agent` (agent label, omitted when nothing was typed). The protocol version stays 1: both fields are additive.
- **REQ-WR-041** A pane whose session has `restored` set shows a small "Restored" chip in the pane header (next to the status), with a tooltip "Restored after restart · resumed <agent>" when `resumed_agent` is set. The chip goes away when the session exits or restarts.
- **REQ-WR-042** Closing a pane still closes its session (REQ-WB-014), which is how the owner drops a session they do not want back after the next reboot.

## Tests

- **REQ-WR-050** Go tests (no real agent needed):
  - write/read round trip of `sessions.json`, atomic write, `.bad-*` rename on garbage;
  - a core shut down with running and exited sessions, then a new core on the same home, restores both kinds under the same ids, the running one in `last_dir`;
  - the claude adapter finds the id from a fake `<home>/.claude/sessions/<pid>.json`; the omp adapter finds it from a fake session dir by header `cwd`;
  - the resume command typed into a restored shell (fake shell script that echoes its stdin) is `claude --resume <uuid>`, and a bad id falls back to `claude --continue`;
  - `kill` removes the entry; the 10-minute agent expiry clears `agent`.
- **REQ-WR-051** Manual check on the owner's Mac: open a shell, a Claude Code session and an omp session in Workbench, `lana workbench stop`, open Workbench again: three panes come back, the two agents with their conversations.

## Out of scope

Scrollback from before the restart, restoring split ratios (none exist yet), adapters beyond claude and omp, restoring dev servers' processes beyond re-running their command, sessions started outside Workbench.

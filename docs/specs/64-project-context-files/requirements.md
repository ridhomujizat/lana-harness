# Spec 64: Project context files

- **Status**: Approved (owner, 2026-11-21: "lanjut ke … Poin 3 — Baca file konteks proyek", accepting the plan
  given in chat: config `context_files`, prompt-time discovery from the workspace, just-in-time discovery when
  tools touch a path, size caps, no `@import`, seen set reset on compaction)
- **Revision**: 1
- **Branch**: `feature/project-context`, from `development` (0.5.0-beta.15, 04166ff)
- **Contract**: [api-contract.md](api-contract.md) (config only)
- **Design**: [design.md](design.md)
- **Tasks**: [tasks.md](tasks.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Paper | Barbaste et al., "Harness Engineering", arXiv 2609.00006, Recommendation 6 | hierarchical Markdown context files, read the neighbors' filenames, surface nested files just-in-time |
| Owner decision | chat, 2026-11-21 | scope and defaults listed in Status |

## Problem

`buildPrompt` (`agent/turn.go`) loads only the agent persona (`AGENT.md`), the user file, and memory.
Lana never reads a project's own instructions (`AGENTS.md`, `CLAUDE.md`). Agent workspaces are not
where the owner's repositories live: `lana` works in `~/lana/workspace` while the repositories are
under `~/coding/…`. So reading files only from the workspace at turn start is not enough. A project's
instructions must also appear when a tool first touches that project.

## Goal

When an agent works in a directory that has project instructions, it sees them once, before it acts
on files there, without the owner pasting them into the chat.

## Definitions

- **Context file** — a file whose name is in the agent's `context_files` list. The default list is
  `["AGENTS.md", "CLAUDE.md"]`.
- **Context directory chain** — for a start directory, the directory itself and each parent up to,
  but not including, the owner's home directory. If the start directory is not under home, the
  chain stops at the filesystem root, which is excluded.
- **Seen set** — the context files already shown in a session since its last compaction.

## Scope

### Included

- A per-agent `context_files` setting.
- Prompt-time discovery from the session workspace.
- Just-in-time discovery from `read_file`, `write_file`, `edit_file`, `glob`, `grep`, and a leading
  `cd <dir>` in `bash`.
- Size caps and a truncation note.

### Not included

- `@path` import lines inside context files. They are shown as plain text.
- Context files from the home directory itself or above it.
- Remote URLs as context sources.
- Agents on `runtime = "claude-code"`. Claude Code reads its own `CLAUDE.md`.

## Behavior rules

### REQ-PCX-001: The `context_files` setting

An agent shall have an optional `context_files` setting, a list of file names. The rules:

- When the setting is absent, the default list applies.
- An empty list turns the feature off for that agent.
- In one directory, only the first name in list order that exists is used.
- Names are plain file names. A name with a path separator is a config error.

#### Acceptance criteria

- `AC-PCX-001`: With no setting, a directory that holds both `AGENTS.md` and `CLAUDE.md` contributes
  only `AGENTS.md`.
- `AC-PCX-002`: With `context_files = []`, no context file is ever shown.
- `AC-PCX-003`: `context_files = ["docs/AGENTS.md"]` fails `lana config check` with a clear message.

### REQ-PCX-002: Prompt-time discovery

When the system prompt is built, Lana shall walk the context directory chain of the session workspace
and put every context file it finds into a new prompt section, `project`. Files are ordered from the
top directory down to the workspace. Each file is wrapped in
`<project-context path="<absolute path>">…</project-context>`. The section opens with one line saying
that these are project instructions written for coding agents, and to follow those that apply to
Lana's tools. The files of this section join the seen set.

#### Acceptance criteria

- `AC-PCX-004`: With workspace `~/lana/workspace` and a file `~/lana/AGENTS.md`, the `project`
  section contains that file and no file from `~` itself.
- `AC-PCX-005`: When a context file changes between two turns, the next turn sends the changed section
  as a `<system-update>` (REQ-LTC-002), without rewriting the head prompt.

### REQ-PCX-003: Just-in-time discovery

After `read_file`, `write_file`, `edit_file`, `glob`, or `grep` succeeds on a path, and after a `bash`
call whose first simple command is `cd <dir>`, Lana shall walk the context directory chain of that
path's directory (the path itself for a directory). Every context file found that is not in the seen
set is appended to the tool result, in top-down order, wrapped as in REQ-PCX-002. The appended blocks
are preceded by the intro line `[Lana] Project instructions from the folder this call touched. Lana
added them; they are not part of the tool output. These are project instructions written for coding
agents. Follow the ones that apply to your tools.`, so the model reads them as instructions rather
than as tool output. Those files then join the seen set. A failed or rejected call adds nothing.

#### Acceptance criteria

- `AC-PCX-006`: In a session whose workspace is `~/lana/workspace`, the first `read_file` of
  `~/coding/project/self-tool/lana-harness/go.mod` gets that repository's `CLAUDE.md` appended to its
  result.
- `AC-PCX-007`: A second `read_file` in the same repository gets nothing appended.
- `AC-PCX-008`: `bash` with `cd ~/coding/project/self-tool/lana-harness && go test ./...` appends the
  repository's `CLAUDE.md` when it is not yet seen.
- `AC-PCX-013`: The appended blocks are prefixed by the intro line `[Lana] Project instructions from
  the folder this call touched. Lana added them; they are not part of the tool output. These are
  project instructions written for coding agents. Follow the ones that apply to your tools.` followed
  by a blank line, and the seen set is rebuilt from that format after a restart.

### REQ-PCX-004: The seen set survives a restart and resets on compaction

The seen set shall be rebuilt from the session log: the `project` section of the current head prompt,
and the `project-context` blocks in tool results after the latest compaction. After a compaction, files
seen before it are shown again when they are next touched.

#### Acceptance criteria

- `AC-PCX-009`: After a daemon restart, a file already shown in the session is not shown again.
- `AC-PCX-010`: After a compaction, the next touch of the repository shows its `CLAUDE.md` again.

### REQ-PCX-005: Size caps

One context file shall be cut to 16 KiB and one injection (a prompt section or one tool result's
blocks) to 32 KiB. A cut file ends with
`[truncated: <shown> of <total> bytes; read the file for the rest]`. A file that does not fit in the
remaining budget of an injection is listed by path only, with the same note.

#### Acceptance criteria

- `AC-PCX-011`: A 40 KiB `AGENTS.md` is shown as 16 KiB plus the truncation note.

### REQ-PCX-006: Failure is silent for the turn

A context file that cannot be read (permission, invalid UTF-8, or not a regular file) is skipped and
logged at debug level. The tool result and the prompt are otherwise unchanged.

#### Acceptance criteria

- `AC-PCX-012`: An unreadable `AGENTS.md` produces no error in the tool result.

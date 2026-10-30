# Requirements: Cleaner Telegram turn layout

- **Issue**: —
- **Status**: Approved
- **Revision**: 2
- **Owner**: Ridho
- **Contract**: Not required — Telegram message text only; no config, HTTP, or CLI change
- **Related ADR**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner feedback 2026-10-30 | Screenshot: every tool call listed as a 🔧 line above the answer; one new "Allow …?" message per approval, each left in the chat | problem |
| Owner request 2026-10-30 | "saat proses: Text, bubble permission yang diubah terus-terusan; saat selesai: Text, [icon step] 12 step, 4 tools" | layout |
| Owner answers 2026-10-30 | live status = one line with the current tool; permission bubble becomes a summary at the end; footer = model steps · tool calls | `Q-001`–`Q-003` |

## Goal

A Telegram turn reads cleanly: while lana works, the owner sees the streaming answer with one status
line and one permission bubble that updates in place; when it finishes, only a short approval summary
and the answer with a one-line footer remain.

## Definitions

- **Live message** — the message lana sends when a turn starts and edits while the turn runs.
- **Permission bubble** — one message per turn that shows the current approval request.
- **Step** — one model call of the lead agent. Counted from frames: the first step starts with the turn; a new step starts at the first `text_delta` or `tool_call` of the lead session that follows a `tool_result`.
- **Tool calls** — all `tool_call` frames of the turn, lead and child sessions.

## Scope

### Included

- Live message layout, permission bubble, final layout, footer.
- Markdown tables in answers (revision 2).

### Not included

- Ask-user question messages (unchanged), web view, slash commands.

## Behavior rules

### REQ-TGL-001: Live message while running

While a turn runs, the live message shall show one status line, then the streamed answer. The status line is `⏳ <tool> · step <n>` while a tool runs (`<tool>` is the short name: the part after the last `__` for MCP tools; child calls prefixed `<agent> · `), and `✍️ step <n>` while only text streams. No per-tool 🔧 lines are shown.

#### Acceptance criteria

- `AC-TGL-001`: Given a turn that calls `mcp__dbx__dbx_execute_query` in step 3, when the live message is edited during that call, then its first line is `⏳ dbx_execute_query · step 3` and no line starts with `🔧`.
- `AC-TGL-002`: Given a turn streaming text in step 1 with no tool running, then the first line is `✍️ step 1` followed by a blank line and the answer so far.

### REQ-TGL-002: One permission bubble per turn

The first approval of a turn shall send one permission bubble as a reply to the live message. Later approvals of the same turn shall edit that bubble instead of sending new messages. Approvals that arrive while one is shown wait in order. The bubble shows `🔐 Allow <tool>: <arg>?` with the existing three buttons, and between requests `<outcome> · <tool>` (for example `✓ allowed for session · dbx_list_tables`).

#### Acceptance criteria

- `AC-TGL-003`: Given a turn with 3 approvals, when they are answered, then exactly one bubble message was sent and it was edited for each request.
- `AC-TGL-004`: Given two approvals requested at the same time, then the bubble shows the first; after it is answered, it shows the second; both outcomes reach their callers.
- `AC-TGL-005`: Given an approval that times out or a cancelled turn, then that request ends as today (`⊘ approval expired` / cancelled) and the next waiting request is shown.

### REQ-TGL-003: Final layout

When the turn ends:

1. If a permission bubble exists, it is edited to the summary `🔐 <n> approval(s): <a> allowed, <r> rejected` (omit zero parts; `expired`/`cancelled` counted as rejected) with no buttons; then the final answer is sent as a new message and the live message is deleted.
2. Otherwise the live message is edited into the final answer.

The final answer is: the answer HTML, the end line if any (`⊘ cancelled`, `✗ …`, `⚠ stopped at max_steps`), and, when the turn made at least one tool call, the footer `⚙️ <s> step(s) · <t> tool call(s)` on its own last line. Overflow splitting is unchanged; the footer goes on the last part.

#### Acceptance criteria

- `AC-TGL-006`: Given a turn with 12 steps, 4 tool calls, and 3 allowed approvals, when it ends, then the chat shows, in order, the summary bubble `🔐 3 approvals: 3 allowed` and a new message with the answer ending in `⚙️ 12 steps · 4 tool calls`, and the live message is deleted.
- `AC-TGL-007`: Given a turn with 1 step and no tool calls, when it ends, then the live message is edited to the answer with no footer and no new message is sent.
- `AC-TGL-008`: Given 1 step and 1 tool call, then the footer reads `⚙️ 1 step · 1 tool call`.
- `AC-TGL-009`: Given `deleteMessage` fails, then the final answer is still sent and the failure is logged, not shown.

### REQ-TGL-004: Markdown tables

Telegram has no table markup. A GitHub-style Markdown table outside code fences (header row, `|---|` separator, body rows) shall be converted by `tgfmt.ToHTML`:

- **Narrow** (aligned width ≤ 40 characters, counting runes; cells padded to the column width and joined with ` │ `; width = sum of column widths + 3 × (columns − 1); trailing spaces trimmed; the separator line is `─` × (width + 1) for the first column, `─` × (width + 2) for middle columns, `─` × (width + 1) for the last, joined with `┼`): a `<pre>` block with the aligned table. Inline Markdown inside cells is shown as plain text.
- **Wide** (otherwise): one block per body row: the first cell in bold on its own line, then `<header>: <value>` lines for the other non-empty cells (inline Markdown allowed), blocks separated by a blank line.

Cell text is HTML-escaped. Rows with fewer cells than the header are padded with empty cells; extra cells are dropped.

#### Acceptance criteria

- `AC-TGL-010`: Given the table `| id | ok |` / `|---|---|` / `| 1 | yes |`, then the output is a `<pre>` block whose lines are `id │ ok`, `───┼────`, `1  │ yes`.
- `AC-TGL-011`: Given the 4-column user table from the owner screenshot (username, email, fullname, is_active), then each row becomes `<b>agents</b>` followed by `email: agentdemo01@yopmail.com`, `fullname: Agent Bro`, `is_active: 1` lines, and no `|` table syntax remains.
- `AC-TGL-012`: Given a table inside a fenced code block, then it is left unchanged inside `<pre>`.

## Failure cases

| Condition | Expected behavior |
|---|---|
| Bubble send fails | approval is `unavailable` as today |
| Bubble edit fails | logged; outcome still returned |
| Final send fails | fall back to editing the live message |

## Authorization and security

Unchanged: only owners can press approval buttons.

## Blocking Open Questions

| ID | Question | Status | Resolution |
|---|---|---|---|
| `Q-001` | Live status content | Resolved | One line with the current tool and step |
| `Q-002` | Bubble after the turn | Resolved | Edited to a summary |
| `Q-003` | Footer meaning | Resolved | model steps · tool calls |
| `Q-004` | Tables | Resolved | Monospace when narrow, per-row list when wide (owner, 2026-10-30) |

## Requirement coverage

| Requirement | Acceptance criteria |
|---|---|
| `REQ-TGL-001` | `AC-TGL-001`, `AC-TGL-002` |
| `REQ-TGL-002` | `AC-TGL-003`–`AC-TGL-005` |
| `REQ-TGL-003` | `AC-TGL-006`–`AC-TGL-009` |
| `REQ-TGL-004` | `AC-TGL-010`–`AC-TGL-012` |

## Approval

- **Approved by**: Ridho
- **Approved on**: 2026-10-30

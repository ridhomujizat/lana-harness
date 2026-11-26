# Requirements: Workbench panes on the Router's Claude Code accounts

- **Issue**: — (owner request 2026-11-25/26)
- **Status**: Approved (owner: "ganti plugin cswap di workbench, aturnya bisa di router sendiri"; "pane di workbench yg ingin bisa menggunakan router lagi bukan cswap"; "/resume di claude code muncul semua historynya"; "ya boleh kerjakan". Details are agent recommendations.)
- **Revision**: 2 (2026-11-26: owner "account skrng ga cuman claude tapi bisa pilih antigravity juga. icon claudenya juga di ganti … di kelompokan berdasarkan provider dan bisa collapse uncollapse dan search")
- **Revision**: 3 (2026-11-26: Antigravity hooks in the Workbench switch, REQ-WRA-009; shared Antigravity
  conversations across accounts, experimental, REQ-WRA-010)
- **Revision**: 4 (2026-11-26: owner "setting cli antigravity saat login baru harusnya tidak baru tapi menduplicat
  setting yang sudah ada … termasuk claude code juga sama seperti cswap"; a "Keychain Not Found" dialog on a second
  agy sign-in; "belum ada tampilan email accountnya di manage antigravity". REQ-WRA-011..014)
- **Revision**: 5 (2026-11-26: an added Antigravity account HOME showed agy's first-run onboarding ("Choose your
  color scheme" …) although the owner already did it. REQ-WRA-015)
- **Owner**: ridho
- **Supersedes**: spec 56 REQ-PLG-010..013 (the claude-swap "Claude accounts" plugin)
- **Related**: spec 39 (Claude Code accounts), spec 60 (restore), spec 69 (agent hooks), spec 75 (pools)

## Why

The Workbench "Claude accounts" popup runs claude-swap (`cswap`). cswap copies each account's login in and
out of the macOS Keychain and refreshes OAuth tokens itself, so every change to how Claude Code stores its
login can break it (cswap #603, 2026-11-25: Claude Code 2.1.331 `keychainOverflow` lost MCP and plugin
sign-ins after a switch). The Router already keeps Claude Code accounts the safe way: one folder per
account (`CLAUDE_CONFIG_DIR` + `CLAUDE_SECURESTORAGE_CONFIG_DIR`), sign-in and token refresh done by
`claude` itself, usage from `claude -p /usage`; lana never reads a token. The Workbench uses those
accounts instead, and accounts are managed in one place: the Router.

## Behavior rules

### REQ-WRA-001: Shared history across accounts

Every Router Claude Code account folder (not the built-in `~/.claude`) links these items to the same item
in the default Claude folder (the daemon's `CLAUDE_CONFIG_DIR`, else `~/.claude`), so `/resume`, `--continue`,
auto memory (`projects/<slug>/memory`), prompt history, settings, user `CLAUDE.md`, skills, subagents and
commands are the same whichever account a pane runs on:

| Item | When the default item is missing | When the account already has a real item |
|---|---|---|
| `projects/` | created (0700) | adopted: each entry moved into the shared folder (a name the shared folder already has stays behind), the rest renamed `projects.before-share-<unix>`, then linked |
| `history.jsonl` | created empty (0600) | adopted: its lines appended to the shared file, renamed `history.jsonl.before-share-<unix>`, then linked |
| `settings.json`, `CLAUDE.md`, `skills/`, `agents/`, `commands/` | not linked | kept as it is (the account's own) |

A link that already points somewhere is kept. Linking runs when an account is saved and once for every
account at daemon start; a failure is logged and never fails the save. Lana's own runs are unchanged
(`--setting-sources project,local` already skips user settings); their transcripts land in the shared
`projects/` like the built-in account's do today. Nothing touches `.claude.json`, credentials or the
Keychain.

- `AC-WRA-001`: Given a new account folder, after save `projects`, `history.jsonl`, `settings.json`,
  `CLAUDE.md`, `skills` are symlinks into the default folder (the last three only when they exist there).
- `AC-WRA-002`: Given an account folder with a real `projects/a/1.jsonl` and the default folder with
  `projects/a/2.jsonl`, after linking the shared folder holds both and the account's `projects` is a link.

### REQ-WRA-002: Hook install keeps links

The spec 69 hook installer writes through a symlinked `settings.json` (it resolves the link and writes the
target), so installing or removing the hooks never replaces a link with a file.

- `AC-WRA-003`: Given `<account>/settings.json` → `<default>/settings.json`, after `agenthook.Install` the
  account path is still a symlink and the target holds the hooks.

### REQ-WRA-003: The Workbench accounts popup reads the Router

The Workbench Claude accounts button is always shown (no tool needed). The popup lists the Router's Claude
Code accounts (`GET /v1/router/claude-agent` `accounts`, the built-in one first, then by pool order): name
(display name), email and plan, tags "default" (REQ-WRA-005), "most room" (the signed-in account whose
tightest 5-hour/weekly window is least used), "signed out", "cooling" (spec 75), and a bar per plan window
(utilization, time to reset). It rereads every 2 minutes; Refresh also probes usage
(`POST /v1/router/limits/refresh`, at most once a minute per account). A header link "Manage in Router"
opens `#/router/providers/claude-code`; adding, removing and renaming accounts live there.

### REQ-WRA-004: Actions per account

- **New tab**: a Workbench terminal in the focused folder running the account's `claude` with its folder:
  `env CLAUDE_CONFIG_DIR=<dir> CLAUDE_SECURESTORAGE_CONFIG_DIR=<dir> <binary>`; the built-in account
  `env -u CLAUDE_CONFIG_DIR -u CLAUDE_SECURESTORAGE_CONFIG_DIR <binary>`.
- **Beside**: the same with `--continue`, split right of the focused pane (a new tab when none).
- **Sign in** (signed out only): a new tab running the account's `login_command` (spec 39).
- **Use for new terminals** (REQ-WRA-005).

### REQ-WRA-005: Workbench default account

The owner can pick one account as the Workbench default (replaces cswap's "Make default", which rewrote the
machine-wide login). Every Workbench terminal created afterwards gets that account's
`CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` in its environment, so typing `claude` there runs
on it; the built-in account (the initial value) sets neither. Existing terminals keep their environment.
The choice is stored by the daemon (`GET/PUT /v1/workbench/claude-account`), survives restarts, and a
removed account falls back to built-in. Plain `claude` outside the Workbench is never changed.

### REQ-WRA-006: Restore runs on the same account

A restored pane whose Claude Code ran with an explicit config folder types
`CLAUDE_CONFIG_DIR='<dir>' CLAUDE_SECURESTORAGE_CONFIG_DIR='<dir>' claude --resume <id>` (spec 60), so the
Keychain login of a Router account is found. The cswap form stays for panes saved under a cswap profile
while cswap is installed.

### REQ-WRA-007: cswap leaves the Workbench

The "Claude accounts" plugin entry is removed from Tools & plugins (the Plugins section hides when it has
none), and the claude-swap Toolbox package drops its `list`/`switch` commands. claude-swap stays installable
as a plain tool.

### REQ-WRA-008: Accounts popup for every subscription provider (revision 2)

The Workbench button and popup are "Accounts" with a neutral icon (no Claude logo). The popup has a search
box and one collapsible group per provider, with its logo and account count: Claude Code (REQ-WRA-003/004/005)
and Antigravity (`GET /v1/router/antigravity`, hidden when agy is not installed and has no accounts). The
collapsed state is remembered per browser; while searching (name, email, plan or tier, label) groups with a
match open and groups without one hide. Antigravity rows show tier, tags (signed out, cooling, disabled, most
room) and a bar per quota window (used = 1 − remaining). Actions: New tab runs the account's agy with its
own HOME (`env HOME=<home> <binary>`; the built-in account plain `<binary>`), Beside adds `--continue`; a
signed-out account links to the Antigravity Router page (its sign-in is a link + code flow). There is no
Workbench default for Antigravity: a HOME for a whole shell would break it. Antigravity conversations are
shared across accounts since revision 3 (REQ-WRA-010); restoring an agy pane after a reboot is not covered.

### REQ-WRA-009: Workbench hooks for Antigravity (revision 3)

The spec 69 hook switch also installs Lana's hooks into agy, so an agy session in a Workbench pane gets the
same pane notice as Claude Code and its edits move the Workbench to their worktree. Facts verified on agy
1.5.17: the global hook file is `<HOME>/.gemini/config/hooks.json` (the workspace `.agents/hooks.json` is not
used); its top-level keys are hook names, each an object of events (no top-level `hooks` key);
SessionStart/PreInvocation/PostInvocation/Stop are flat handler lists, PreToolUse/PostToolUse are groups of
`matcher` (regex) + `hooks`; a command runs via `sh -c` with the hooks.json folder as cwd. Stdin is camelCase
JSON (`conversationId`, `workspacePaths`, `cwd`, `hookEventName`, … and `toolCall {id, name, args}` for tool
events). A SessionStart answer `{"injectSteps":[{"systemMessage":{"systemMessage":"<text>"}}]}` reaches the
model; a PostToolUse answer must be `{}`.

- Lana owns one entry, `lana-workbench`: SessionStart runs `lana workbench hook session-start --agent agy`,
  and PostToolUse (matcher
  `write_file|edit_file|write_to_file|replace_file_content|multi_replace_file_content|create_file|delete_file`)
  runs `lana workbench hook post-edit --agent agy`, each with `timeout` 10. Each command prints `{}` itself
  outside a pane (`LANA_WORKBENCH_SESSION` unset) or when the lana binary is gone.
- session-start: inside a pane (the spec 69 in-pane check) it injects the Claude Code SessionStart text, with
  the worktree of `cwd` (else the first workspace path); otherwise `{}`.
- post-edit: reports the worktree of the written file exactly like the Claude Code pre-edit hook; the path is
  `toolCall.args.Path`, else `TargetFile`, else `AbsolutePath`. It always prints `{}`.
- **PostToolUse, not PreToolUse**: agy's PreToolUse requires a `decision`, and `allow` would auto-approve the
  tool, bypassing the owner's agy permission prompt. So the Workbench follows an edit right after it lands
  instead of right before; acceptable.
- Any stdin Lana cannot read → `{}`, exit 0: a hook never breaks agy.
- The installer keeps every other hook name and unknown key, writes through a symlinked hooks.json (atomic
  write of the target), writes `hooks.json.lana-bak` once before its first change, and creates
  `.gemini/config` (0700) when missing. Off removes only `lana-workbench`.
- Folders, only while agy is installed (`agy` on PATH or the Router's configured binary): the owner's
  `~/.gemini/config` (main daemon only, like the default Claude Code folder) and `<HOME>/.gemini/config` of
  every added Antigravity account. `GET/PUT /v1/workbench/agent-hook` lists them with the Claude folders; each
  folder gains `kind` (`"claude"` or `"antigravity"`); the other fields are unchanged.
- The switch reads "Connect Claude Code and Antigravity to the Workbench" and, when on,
  "Installed in N of M folders (Claude x, Antigravity y)".

### REQ-WRA-010: Shared Antigravity conversations (revision 3, experimental)

Every added Antigravity account HOME (not the built-in one, which is the owner's HOME) links these items of
`<accountHOME>/.gemini/antigravity-cli/` to the same item in `<ownerHOME>/.gemini/antigravity-cli/`:

| Item | When the owner item is missing | When the account already has a real item |
|---|---|---|
| `conversations/`, `brain/`, `annotations/`, `implicit/` | created (0700) | adopted like `projects/` (REQ-WRA-001), leftover `<name>.before-share-<unix>` |
| `history.jsonl` | created empty (0600) | adopted by append like REQ-WRA-001 |
| `conversation_summaries.db`, `cache/last_conversations.json`, `cache/conversation_metadata.json` | not linked | kept (SQLite and JSON are never merged) |

Nothing else in the account HOME is read, listed or moved (agy keeps its file-based login there). Linking runs
on Antigravity account create and save and once per added account at daemon start; failures are only logged.
Gate: the main daemon (default Lana home) or a daemon started with `LANA_SHARE_HISTORY=1` (the owner's explicit
opt-in for a scratch daemon); `LANA_SHARE_HISTORY=1` also opens the REQ-WRA-001 Claude Code gate.

**Experimental, pending the owner's live test**: resuming another account's conversation is unverified.
Conversations hold `cascade_id`/`gen_metadata` blobs that may be bound to the Google account that made them.
The agy resume line for restore stays out of scope, and Lana's own agy runs are unchanged.

### REQ-WRA-011: Shared Antigravity settings (revision 4)

Every added Antigravity account HOME also links these items to the same item in the owner's HOME, only when
the owner's exists: `.gemini/antigravity-cli/settings.json`, `.gemini/config/config.json`,
`.gemini/config/mcp_config.json`, `.gemini/config/skills/`. A link already there is kept (any target). A real
account item is agy's first-sign-in default, so it is renamed `<name>.before-share-<unix>` and linked (the
owner's replaces it). Without an owner item the account's own stays. Never touched: `.gemini/config/hooks.json`
(REQ-WRA-009 manages it per folder) and agy's login and state (`antigravity-oauth-token`,
`cache/acp-auth-state.json`, `cache/default_project_id.txt`, `cache/onboarding.json`, `installation_id`,
`jetski_state.pbtxt`): never read, listed, linked or moved (since revision 5, `cache/onboarding.json` is touched
for one flag only, REQ-WRA-015). Same gate and runs as REQ-WRA-010: on account create
(before the sign-in starts, so the first sign-in already sees the owner's settings), on save, at daemon start.
Verified by tests; whether agy writes through the links or replaces them with files is pending the owner's
live check.

### REQ-WRA-012: Claude Code settings copied like cswap (revision 4)

A Router Claude Code account folder with no `.claude.json`, or one without `oauthAccount` (never signed in),
gets `<accountDir>/.claude.json` (0600, atomic write) as a copy of the default account's with `oauthAccount`,
`primaryApiKey` and `customApiKeyResponses` removed; every other key is kept (theme, hasCompletedOnboarding,
projects, mcpServers, unknown keys). The default's file is `$CLAUDE_CONFIG_DIR/.claude.json` when the daemon has
`CLAUDE_CONFIG_DIR`, else `~/.claude.json`. A `.claude.json` with `oauthAccount`, a symlinked one, or one that is
not a JSON object is never overwritten; a missing default file writes nothing. Same gate as REQ-WRA-001; runs on
account save (before the sign-in command is shown) and at daemon start. This replaces REQ-WRA-001's "Nothing
touches `.claude.json`" for never-signed-in accounts only. Verified by tests.

### REQ-WRA-013: No Keychain dialog for added Antigravity accounts (revision 4, pending live check)

An added account's HOME has no macOS login Keychain, so agy's keychain write at sign-in or token refresh showed
"Keychain Not Found — A keychain cannot be found to store “antigravity.”". Every agy process Lana starts for an
added account (the ACP runtime, `agy acp --login`, the `models` and `/usage` probes) gets
`SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0` unless the daemon environment already has `SSH_CONNECTION`; the
built-in account never gets it. agy 1.5.17 uses its file-based token storage when it detects SSH and the
login Keychain is unavailable (its changelog and the "Using file-based token storage because %s detected"
string). The Workbench New tab / Beside argv of an added account adds the same variable to its `env HOME=…`.
**Pending the owner's live check**: sign an added account out and in again on the scratch daemon; no dialog
means it works. A login an added account already keeps in a Keychain item is not migrated.

### REQ-WRA-014: Antigravity account email (revision 4, pending live check)

When a Router sign-in (`agy acp --login`) succeeds and agy printed a line matching
`^Logged in as (\S+@\S+?)\.?$`, Lana keeps that email for the account in router_kv
(`antigravity_email/<account>`, like the plan tier); a successful sign-in without such a line clears it, and
removing the account drops it. Nothing else of agy's output is kept; no Google endpoint is called and no token or
state file is read. `GET /v1/router/antigravity` reports `email` on each account (`""` when unknown); the
built-in account has none. **Pending the owner's live check**: the "Logged in as %s." string is in the agy
binary but unverified for `acp --login`; if agy does not print it there, the email stays empty.

### REQ-WRA-015: Antigravity onboarding done once (revision 5)

agy keeps its first-run onboarding state in `<HOME>/.gemini/antigravity-cli/cache/onboarding.json`, a JSON object
(`consumerOnboardingComplete`, `enterpriseOnboardingComplete`, `onboardingComplete`). When the owner's file has
`"onboardingComplete": true` and an added account's file is missing or does not have it true, the account's
`cache/onboarding.json` (0600, atomic write; `cache/` created 0700 when missing) is written as the account's own
object (or `{}`) with only `onboardingComplete` set to true. `consumerOnboardingComplete` and
`enterpriseOnboardingComplete` are never copied from the owner (they may be per Google account); the account's own
values and every other key are kept. This copies one flag; it is not a link. An account file already true, or a
symlinked one, is left as it is; one that is not a JSON object is left as it is and reported as an error. The
owner's file is only read. Same gate and runs as REQ-WRA-010/011: on account create (before the sign-in), on save,
at daemon start. Verified by tests; whether agy then skips its onboarding is pending the owner's live check.

### REQ-WRA-016: Model first, then account (revision 6)

The agent Model picker (agent settings and the create dialog) lists each Claude Code model once under one "Claude
Code" group and each Antigravity model once under one "Antigravity" group, whatever the number of accounts. A
listed model's address keeps the current model's account when that account offers the model, else the first
account that does. When more than one account offers the picked model, an **Account** select appears under the
picker, before the reasoning effort; each account reads by its label, else its email, else its name, with the
email and name as a hint. Changing it rewrites the saved address to `<account>/<model>` and keeps the effort. The
saved value stays the full address; combos and other providers are unchanged. The chat model menu and the combos
editor keep the per-account groups.

The Quota page names each Claude Code and Antigravity card by the account's label, else email, else name, with
the email and the name under it. `GET /v1/router/antigravity` `display_name` is the label, else the email, else
the name.

## Out of scope

Copying cswap logins into the Router (each account signs in again through the Router), auto-switching a
running terminal, per-folder account mapping, restoring an agy pane (its resume line).

## Approval

- **Approved by**: ridho (direction) + agent recommendations · **Approved on**: 2026-11-26

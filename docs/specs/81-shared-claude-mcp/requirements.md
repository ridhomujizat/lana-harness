# Spec 81: MCP servers shared by every Claude Code account

- **Status**: Approved (owner 2026-11-26: "biar mcp yang lama tetep jalan gmn? walau tetep bisa gonta ganti
  akun" → proposal accepted: "ya")
- **Revision**: 2
- **Branch**: feature/shared-mcp (carries the uncommitted fix/workbench-account-shell patch)
- **Related**: spec 77 (Workbench accounts from the Router), spec 60 (Workbench restore)

## Context

Each named Claude Code account (`[providers.<name>] kind = "claude-code"`) has its own config dir
`<LANA_HOME>/claude/<name>` with its own `.claude.json`. Claude Code keeps MCP servers in that file: user
scope in `mcpServers`, local scope in `projects["<dir>"].mcpServers`. A server the owner added with
`claude mcp add` in the built-in account (`~/.claude.json`) is missing in a Workbench pane of another
account (incident: `sxored-hub` in `~/coding/sxored` on account `claude-3`). The owner wants the MCP
servers they already have to work in every account, while still switching accounts.

## Requirements

### REQ-SMC-001: Shared MCP file

`lana claude mcp-config --account <name> --dir <dir>` writes the MCP servers of the built-in account for
`<dir>` to `<LANA_HOME>/claude/mcp/<name>-<hash>.json` (`<hash>`: first 12 hex of sha256 of the cleaned
absolute `<dir>`), file mode 0600, dir 0700, written atomically, and prints the file's path on stdout. The
content is `{"mcpServers": {...}}`:

- the built-in global config is `$HOME/.claude.json` (never `CLAUDE_CONFIG_DIR`);
- its top-level `mcpServers`, then the `mcpServers` of every `projects` key that is `<dir>` or an ancestor
  of `<dir>`, nearer directories overriding farther ones and project entries overriding top-level ones on
  the same server name;
- each server entry is copied as it is (type, url, command, args, env, headers).

When the built-in config is missing, unreadable, not JSON, or larger than 16 MiB, or `<name>` is not a
Claude Code account, the command still writes and prints a file with `{"mcpServers": {}}` and exits 0 (a
launch must never fail because of it); the reason goes to stderr in one short line. Lana reads only
`mcpServers` and `projects.*.mcpServers` of that file; never `oauthAccount`, tokens, or the Keychain.
Secret values (headers, env) are never logged, printed, or sent anywhere; they only go into the 0600 file.

### REQ-SMC-002: Launch lines

Every `claude` launch Lana types for a named account adds
`--mcp-config "$(lana claude mcp-config --account <name> --dir "$PWD" 2>/dev/null || echo '{"mcpServers":{}}')"`
(the `lana` binary the daemon itself runs from is used when it is not on `PATH`; quote everything for
`/bin/sh`). This covers the Workbench Accounts popup "New tab", "Beside" (`--continue`), and the resume
line of a restored pane (spec 60 `resumeLine` when the agent's config dir is a named account's). The
built-in account adds nothing (it already reads `~/.claude.json`). The account's own `.claude.json` is
never written by Lana.

### REQ-SMC-003: Docs

The Workbench accounts section of spec 77 and the CHANGELOG (Unreleased → Added) say that MCP servers added
in the built-in account work in every account's Workbench panes.

## Acceptance

- `AC-SMC-001`: given a `$HOME/.claude.json` with top-level `a`, `projects["/x"].mcpServers.b`, and
  `projects["/x/y"].mcpServers.a` (different url), `mcp-config --dir /x/y/z` writes `a` (from `/x/y`) and
  `b`; `--dir /q` writes only the top-level `a`; the file is 0600.
- `AC-SMC-002`: a missing or broken `.claude.json` prints a path to a file with `{"mcpServers":{}}`, exit 0.
- `AC-SMC-003`: the popup's New tab line for a named account contains `--mcp-config` and the built-in one
  does not; Beside keeps `--continue`; a restored named-account claude pane's resume line has it.
- `go test ./...`, `golangci-lint run`, web tests and typecheck green.

## Out of scope

Lana agent turns on Claude Code (they keep `--strict-mcp-config` with lana's own MCP), syncing in the other
direction, a UI to pick servers, `claude` typed by hand in a shell.

## Revision 2 (owner 2026-11-26: "ribet banget harus gini … ga bisa seamless kaya cswap?")

Revision 1's `--mcp-config` file still left each account's own user-scope entries in place, so a token
change had to be repeated per account (`CLAUDE_CONFIG_DIR=… claude mcp remove/add`). Revision 2 replaces
REQ-SMC-001 and REQ-SMC-002 with claude-swap's model (cswap `session.py` `_sync_mcp_servers`): the
built-in account's user-scope `mcpServers` is the single source, mirrored into every named account.

### REQ-SMC-004: Mirror

Before Lana launches `claude` for a named account (`<LANA_HOME>/claude/<name>`), it sets that account's
`.claude.json` top-level `mcpServers` to exactly the top-level `mcpServers` of `$HOME/.claude.json`
(never `CLAUDE_CONFIG_DIR`): adds, edits, and removals all propagate; changes made inside the account are
overwritten at its next launch; nothing flows back. `projects[…].mcpServers` stay untouched on both sides,
and every other key of the account's file is kept with its value unchanged.

- A source that is missing, unreadable, not a JSON object, larger than 16 MiB, or whose `mcpServers` is
  not an object leaves the account untouched. A readable source without the key means "no user servers"
  (`{}`), which propagates.
- The account file must exist and be a regular file (not a symlink) holding a JSON object whose
  `mcpServers` (if any) is an object; else it is left untouched.
- When the account's `mcpServers` already equals the source and the account is adopted, nothing is
  written and no lock is taken.
- The write takes Claude Code's own lock, the proper-lockfile directory `<file>.lock` (mkdir; a lock
  whose mtime is older than 10 s is stale and may be taken over; wait at most 2 s, then give up and leave
  the file untouched), re-reads both files under the lock, writes atomically (temp file in the same
  directory, fsync, rename) keeping the file's mode, and removes the lock.
- First adoption: when the account has no marker `<LANA_HOME>/claude/<name>/.lana-mcp-mirror-v1`, the
  entries the mirror would remove or change are first saved to `.lana-mcp-displaced.json` (0600,
  written once, never overwritten), then the marker is created.
- Fail-open: any failure leaves the account as it was and never blocks or delays the launch beyond the
  lock wait. Secret values (headers, env) are never logged or printed. Lana reads no `oauthAccount`,
  tokens, or Keychain items.

### REQ-SMC-005: Where it runs

The mirror runs in the daemon or Workbench core, not in the typed line: when the web creates a Workbench
pane for a named Claude Code account (the Accounts popup New tab, Beside, Sign in: the create body names
the account, `claude_account`), and when the Workbench core restores a claude pane whose config dir is a
named account's (spec 60 `resumeLine` callers). The typed launch lines go back to revision 0 of spec 81
(no `--mcp-config`). `lana claude mcp-sync --account <name>` runs the same mirror by hand (exit 0 also
when it leaves the account untouched; one short reason on stderr).

REQ-SMC-001 (`lana claude mcp-config`) and REQ-SMC-002 (`--mcp-config` in the lines) are withdrawn.

- `AC-SMC-004`: source `{a, b}`, account `{a (old url), c}` without marker → account `{a, b}`, displaced
  file holds old `a` and `c`, marker exists, other keys of the account file byte-equal in value.
- `AC-SMC-005`: source without `mcpServers` → account `{}`; source broken → account unchanged.
- `AC-SMC-006`: a held fresh lock → account unchanged after ≤ 2 s; a lock older than 10 s → taken over.
- `AC-SMC-007`: in sync + adopted → file mtime unchanged.
- `AC-SMC-008`: web create with `claude_account: "claude-3"` mirrors before the pane starts; the popup
  lines have no `--mcp-config`; a restored named-account claude pane is mirrored before its resume line.

# Requirements: Session event log, permission, approval

- **Issue**: [`#4`](https://github.com/ridhomujizat/lana-harness/issues/4)
- **Status**: Approved
- **Revision**: 2 (REQ-APPR-007 and AC-APPR-017 added; `Q-027` to `Q-032`; error names follow `core`)
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (event kinds and payloads, DDL, Go API for packages `store`, `core`, permission, approval, compaction, key config)
- **Related ADRs**: [ADR Index](../../adr/README.md) — [0004](../../adr/0004-permission-mode-per-agent.md), [0005](../../adr/0005-sqlite-as-single-storage.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0011](../../adr/0011-subagent-as-child-session.md), [0013](../../adr/0013-use-go-as-single-binary.md), [0014](../../adr/0014-integration-as-agentic-tool.md)
- **Related diagrams**: [Diagram Index](../../diagram/README.md) — [0002](../../diagram/0002-agent-loop-and-approval.md), [0003](../../diagram/0003-data-model-and-scheduler.md), [0004](../../diagram/0004-profile-fork-subagent-and-protocols.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Issue | [`#4`](https://github.com/ridhomujizat/lana-harness/issues/4) | scope of spec #4 |
| ADR 0004 | Permission and approval | evaluation order, policy, closed outcomes, audit, answerer chain |
| ADR 0005 | SQLite | single file, WAL, single writer, migrations, secrets not in DB |
| ADR 0009 | Event log | `session_events`, `sessions`, fork, resume, compaction, payload versions |
| ADR 0011 | Subagent | child permission bounds (intersection of parent and child) |
| ADR 0013 | Go | `modernc.org/sqlite`, package layout, `syscall.Kill` for PID checks |
| ADR 0014 | Integration | approval without an answerer = `unavailable` |
| Spec #1 | `REQ-SESS-001`, `REQ-SESS-002`, `REQ-APPR-001`, `REQ-APPR-002`, `REQ-CMD-001`, design "Lock session" | CLI session behavior, lease, policy, fork, compaction invoked by the CLI |
| Spec #2 | Types and hooks in package `core` | `core.SessionEvent`, `core.SessionStore`, hook waterfall and observe |
| Spec #3 | Agent loop (being written) | caller of hooks `tools.pre_execute`, `agent.pre_step`, `llm.stream`; fail-closed default |
| Diagrams 0002, 0003, 0004 | Turn flow, data model, derive, resume | event order, derive, resume repair |
| Interview 2026-10-29 | Owner released the approval gate | decisions in the Question Resolution section use `Agent recommendation:` |

## Purpose

The owner has a complete and trustworthy session history. Every message, tool call, and permission
decision is recorded in one log. A session can be resumed after the process stops abruptly, forked at a
turn boundary, and compacted when the context is almost full. Every tool call is evaluated by closed,
fail-closed permission rules and approval before it runs.

## Actors

- **Owner** — writes `allow` and `deny` in config, changes the session policy, and answers approvals through a channel. Cannot bypass a `deny` rule.
- **Agent loop** (spec #3) — calls the store, permission, and compaction through services and hooks.
- **Approval answerer** — a listener on the `approval.request` hook owned by a channel (the CLI in spec #1, other channels later). Answers only sessions it owns.
- **Provider** — called to produce the compaction summary through a direct adapter.

## Definitions

- **Event** — one row of `session_events`: `session_id`, `seq`, `kind`, `payload`, `turn_id`, `created_at`.
- **Projection** — a row of the `sessions` table. Can be rebuilt from events.
- **Orphan turn** — a turn with a `turn/start` that has no paired `turn/end`.
- **Orphan call** — a `tool/call` or `tool_use` that has no `tool/result`.
- **Session chain** — a session and its parents up to the root. Parent events are cut at `inherited_seq`.
- **Policy** — `trust`, `ask`, or `never`. Effective value = the last `approval/policy` in the chain, or the agent's `permission` if none exists.
- **Pattern** — a `tool` or `tool(arg-glob)` text in `allow` and `deny` rules.
- **Pattern argument** — the tool input value matched against the pattern (rules in `REQ-PERM-001`).
- **Session grant** — permission from an `allowed_session` answer. Computed from the referenced `tool/call`.
- **Answerer** — a listener on the `approval.request` hook that can answer.
- **Outcome** — one of five closed values: `allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable`.
- **Lease** — the per-session lock held by one process. Stored in `session_leases`.
- **Compaction** — a `compaction/summary` event that replaces the history up to `upto_seq` for the model. Old events remain.
- **Context window** — the token limit of the model. Read from the agent config.
- **ULID prefix** — a minimum 6-character prefix of a session ID, case-insensitive.

## Scope

### Included

- Single-file SQLite storage: pragmas, migrations, single writer, read pool.
- Append-only event log, `sessions` projection, and indexes.
- `Create`, `Open` with resume repair, `Append`, `Events`, `List`, `FindPrefix`, `Fork`, `TryLease`, `Prune`, model history derivation, and projection rebuild.
- Permission: patterns, evaluation, workspace boundary, and session grants.
- Approval: policy, requests, audit, answerer chain, timeout, cancel, and the subagent permission-bound function.
- Compaction: automatic, manual, and retry when the context is too long.
- Modules `sqlite_store`, `permission`, `approval`, and `compaction`.

### Not included

- Export of sessions to JSONL, `lana session`, and `lana db prune` (CLI). Only the `Prune` function in the store.
- Memory FTS5 and cron tables (M5).
- CLI subcommands, TUI, and approval prompt (spec #1).
- Agent loop, provider, built-in tools, and the `task` tool (spec #3 and the subagent spec, M4).
- MCP, SDK, HTTP, ACP, and A2A.
- OS-level sandbox (ADR 0004).
- Data retention and log rotation.

## Assumptions

- A single `lana.db` file can be opened by more than one process. Leases in `session_leases` prevent two processes from using the same session. — Source: ADR 0005, spec #1 `REQ-SESS-002`.
- Tools that touch files have a `path` argument. The `bash` tool has a `command` argument. — Source: spec #1 `API-CLI-003` (main argument per tool).
- Time is stored in Unix milliseconds. — Source: spec #1 serialization convention.
- The hooks `tools.pre_execute` and `approval.request` run as a waterfall. The hook `session.event` runs as observe. — Source: spec #2 `REQ-HOOK-001`, `REQ-HOOK-003`.
- The agent loop writes a `tool/result` for every `tool/call`, including when rejected. — Source: ADR 0009, ARCHITECTURE §5.
- The types `core.Message`, `core.ContentBlock`, `core.SessionEvent`, the kind constants, the payload structs, and `core.Risk` are owned by spec #2. — Source: division of the core spec (interview 2026-10-29).

## Behavior Rules

### Storage

#### REQ-STORE-001: Single SQLite file

The system shall store session state in a single SQLite file with WAL mode, `synchronous = NORMAL`,
`foreign_keys = ON`, and `busy_timeout` of 5000 ms. The driver shall be pure Go without cgo.

- `AC-STORE-001`: Given `LANA_HOME` does not yet contain `lana.db`, when the store is opened, then the file is created with mode `0600`, `journal_mode` is `wal`, `synchronous` is `1`, and `foreign_keys` is `1`.
- `AC-STORE-002`: Given path `data/lana.db` in config and folder `data` exists in `LANA_HOME`, when the store is opened, then the file is located at `$LANA_HOME/data/lana.db`.

#### REQ-STORE-002: Numbered migrations

The system shall run the embedded SQL migrations in order when the store is opened. Each
migration runs in one transaction and increments `PRAGMA user_version`. A database whose
`user_version` is higher than the highest migration in the binary is rejected without being changed.

- `AC-STORE-003`: Given an empty database, when the store is opened twice, then `user_version` equals the number of migrations and the migrations are not run again.
- `AC-STORE-004`: Given `user_version` higher than the highest migration, when the store is opened, then the store rejects it with `ErrSchemaTooNew` and the database contents are unchanged.

#### REQ-STORE-003: Single writer and read pool

The system shall send all writes to one writer goroutine per process. Every write transaction
uses `BEGIN IMMEDIATE`, so two processes cannot write at the same time. Reads use a pool of
read-only connections.

- `AC-STORE-005`: Given 50 goroutines writing events to 10 sessions concurrently, when all finish, then there is no `SQLITE_BUSY` error and the seq of each session is contiguous with no gaps.
- `AC-STORE-006`: Given two store instances opening the same file from two processes, when both write to the same session, then each seq is used only once and no event is lost.

#### REQ-STORE-004: Append-only event log

The system shall reject `UPDATE` and `DELETE` on `session_events`, except the whole-session prune path
(`REQ-STORE-014`).

- `AC-STORE-007`: Given a stored event, when `UPDATE session_events` is run directly via SQL, then SQLite rejects it with the message `session_events is append-only` and the row is unchanged.
- `AC-STORE-008`: Given a stored event, when `DELETE FROM session_events` is run directly via SQL, then SQLite rejects it with the same message and the row remains.

#### REQ-STORE-005: Atomic append, seq, and single turn

The system shall write a batch of events in one transaction. Seq increases by 1 per session starting from 1.
The system shall reject `turn/start` if the session still has an open turn.

- `AC-STORE-009`: Given a new session with no events, when `Append` writes three events, then the seqs of those events are 1, 2, and 3 and the projection's `last_seq` is 3.
- `AC-STORE-010`: Given a batch of two events where the second is invalid, when `Append` is called, then none of the events from that batch are stored.
- `AC-STORE-011`: Given a turn is still running, when `Append` writes a new `turn/start`, then the store rejects it with `ErrTurnActive`.

#### REQ-STORE-006: Create and Open with resume repair

The system shall write `session/created` on `Create`. When `Open` is called by a process
holding the lease, the system shall close orphan turns: `approval/decided` with outcome `unavailable`
for each unanswered approval, `tool/result` with status `error` for each orphan call,
then `turn/end` with reason `interrupted`. A session that is already idle does not receive new events.

- `AC-STORE-012`: Given `Create` with agent, channel, address, and workspace, when done, then the first event has seq 1, kind `session/created`, contains all four values, and the projection status is `idle`.
- `AC-STORE-013`: Given a running turn with call `c1` that already has a `tool/result` and call `c2` that does not, when `Open`, then an error `tool/result` is stored for `c2`, then `turn/end` with reason `interrupted`, and the projection status is `idle`.
- `AC-STORE-014`: Given a running turn with `approval/asked` and no `approval/decided`, when `Open`, then `approval/decided` with outcome `unavailable` and reason `interrupted` is stored before `turn/end`.
- `AC-STORE-015`: Given a session that is already idle, when `Open` is called twice, then no new events are written.
- `AC-STORE-016`: Given a process that does not hold the session lease, when `Open` is called, then the store rejects it with `ErrLeaseNotHeld` and writes no events.

#### REQ-STORE-007: Fork at turn boundary

The system shall create a new session from `Fork(parent, at_seq)` with `parent_session_id` and
`inherited_seq = at_seq`, without copying the parent's events. `at_seq` must point to the parent's `turn/end`.

- `AC-STORE-017`: Given session `A` has `turn/end` at seq 20 and events up to seq 30, when `Fork(A, 20)`, then session `B` is recorded with `parent_session_id = A` and `inherited_seq = 20`, `A` still has 30 events, and `B` has zero events.
- `AC-STORE-018`: Given `at_seq` points to `user/message`, when `Fork` is called, then the store rejects it with `ErrForkNotAtTurnBoundary` and no session is created.

#### REQ-STORE-008: Read events and list sessions

The system shall return a session's events with `seq >= from` in ascending order. `List(channel,
address, limit)` shall return sessions in descending `updated_at` order, up to `limit` items.

- `AC-STORE-019`: Given a session with 10 events, when `Events(id, 5)`, then the result contains seq 5 through 10 in order.
- `AC-STORE-020`: Given 25 sessions with `channel = cli` and the same `address`, when `List(cli, address, 20)`, then the result contains the 20 most recent sessions in descending `updated_at` order.

#### REQ-STORE-009: Session lookup by prefix

The system shall find a session by a ULID prefix of at least 6 characters, case-insensitively.
A prefix that matches more than one session must be rejected with the list of candidates.

- `AC-STORE-021`: Given session `01JBX7Q3KM8Z4T2V6N0P1R5S9W`, when `FindPrefix("01jbx7")`, then that session is returned.
- `AC-STORE-022`: Given two sessions `01JBX7Q3KM...` and `01JBX7Q9AA...`, when `FindPrefix("01jbx7")`, then the store rejects it with `core.ErrAmbiguousPrefix` containing both IDs in ascending order.
- `AC-STORE-023`: Given a 5-character prefix, when `FindPrefix`, then the store rejects it with `core.ErrPrefixTooShort`; given a 6-character prefix that does not match, then the store rejects it with `ErrSessionNotFound`.

#### REQ-STORE-010: Session lease

The system shall give one session to one process through `session_leases`. `TryLease` succeeds if
there is no lease, or if the holder is on the same host and its PID is dead. A holder on another
host is considered alive. `Release` releases the lease.

- `AC-STORE-024`: Given a session with no lease, when `TryLease`, then the `session_leases` row contains the process's `pid`, `host`, and `acquired_at`.
- `AC-STORE-025`: Given a lease held by a PID that is still alive on the same host, when another process calls `TryLease`, then the store rejects it with `*core.LeaseHeldError` containing the holder's PID.
- `AC-STORE-026`: Given a lease held by a PID that is dead on the same host, when `TryLease`, then the lease is replaced with the current process's PID.
- `AC-STORE-027`: Given a lease held by another host, when `TryLease`, then the store rejects it with `*core.LeaseHeldError`.

#### REQ-STORE-011: Derive model history

The system shall build model history with the pure function `derive_messages` from the session chain.
Events that enter history are model content events (see contract). The latest summary replaces
history up to `upto_seq`. `assistant/attempt`, `tool/call`, lifecycle events, and approval events do not enter history.

- `AC-STORE-028`: Given a session with a complete turn (user, assistant with `tool_use`, tool result, one `assistant/attempt`, and approval events), when history is derived, then the result equals the golden file.
- `AC-STORE-029`: Given fork `B` of `A` at seq 20 and `A` has events up to seq 30, when `B`'s history is derived, then the history contains `A`'s events up to seq 20 and only `B`'s events.
- `AC-STORE-030`: Given a `compaction/summary` with `upto_seq = 12` in the middle of a session, when history is derived, then the history contains the summary followed by events with seq greater than 12, while the old events remain in the log.

#### REQ-STORE-012: Projection can be rebuilt

The system shall rebuild the `sessions` table from `session_events`. The rebuild result must equal
the projection built during append.

- `AC-STORE-031`: Given three sessions with events, when the `sessions` rows are deleted and the projection is rebuilt, then the contents of `sessions` equal those before deletion.

#### REQ-STORE-013: Payload version

Every payload must contain `v`. The system shall decode older payload versions to the current version
in memory and shall not rewrite old rows. Unknown versions are rejected.

- `AC-STORE-032`: Given a payload with `v` 99, when decoded, then the store returns `ErrUnknownPayloadVersion`.
- `AC-STORE-033`: Given a row with `v` 0 (test fixture with a payload migration from 0 to 1), when the event is read, then the resulting payload has version 1 and the row in the database is unchanged.

#### REQ-STORE-014: Prune whole session

The system shall delete one session together with all its events, projection rows, and lease in
one transaction. The system must not truncate part of the log. A session that still has children is rejected.

- `AC-STORE-034`: Given session `A` with no children, when `Prune(A)`, then the events, `sessions` row, and lease of `A` are gone, and events of other sessions are unchanged.
- `AC-STORE-035`: Given session `A` has a forked session `B`, when `Prune(A)`, then the store rejects it with `ErrSessionHasChildren` and nothing is deleted.

#### REQ-STORE-015: Broadcast after commit

The system shall send events to the `session.event` hook only after the transaction commits successfully.

- `AC-STORE-036`: Given `Append` fails due to a constraint, when a subscriber reads the `session.event` hook, then the subscriber does not receive any event from that batch.
- `AC-STORE-037`: Given `Append` succeeds, when the subscriber reads, then the subscriber receives events with the same seq and kind as the append result.

#### REQ-STORE-016: Store module

The `sqlite_store` module shall install the service `sessions` containing `core.SessionStore`. The database path
is read from the `[[modules]]` row with `id = "store"` and key `config.path`. The default `lana.db` is relative to `LANA_HOME`.

- `AC-STORE-038`: Given a `store` row without `config.path`, when boot, then service `sessions` is available and the database is at `$LANA_HOME/lana.db`.

### Permission

#### REQ-PERM-001: Rule patterns

The system shall use the pattern `tool` or `tool(arg-glob)`. Tool names match exactly. The wildcard `*`
matches any sequence of characters. `?` matches one character. `**` matches a sequence of characters including `/`, and applies only to path arguments. On the `command` argument, `*` and `?`
also match `/`. On path arguments, `*` and `?` do not cross `/`. `\` makes the next character literal. Invalid patterns are rejected when config is loaded.

Pattern arguments per tool:

| Tool | Pattern argument |
|---|---|
| `bash` | `command` |
| `read_file`, `write_file`, `edit_file`, `glob`, `grep` | `path`, relative to the workspace if not absolute. Empty means the workspace |
| `web_fetch` | `url` |
| `task` | `agent` |
| Others (including `mcp__*`) | the entire JSON input in compact form |

- `AC-PERM-001`: Given rule `bash(git status*)`, when `command` is `git status --short`, then the pattern matches; when `command` is `git push`, then the pattern does not match.
- `AC-PERM-002`: Given rule `write_file(~/.ssh/**)` and home `/Users/ridho`, when path `~/.ssh/keys/id` is resolved, then the pattern matches.
- `AC-PERM-003`: Given rule `read_file(src/*)`, when path `src/a/b.go`, then the pattern does not match; given rule `read_file(src/**)`, then the pattern matches.
- `AC-PERM-004`: Given `agents.coder.rules.allow` contains `bash(git`, when config is loaded, then validation fails and the error names the key `agents.coder.rules.allow`, the pattern value, and the reason the parenthesis is unclosed.

#### REQ-PERM-002: Evaluation order

The system shall evaluate every `tools.pre_execute` call in the following order and stop
at the first decision: (1) a `deny` rule matches, reject; (2) a path argument is outside the workspace, reject; (3) an `allow` rule or session grant matches, allow; (4) policy `trust`, allow; (5) risk `Read`, allow; (6) policy `never`, reject; (7) policy `ask`, request approval.

- `AC-PERM-005`: Given policy `trust` and `deny` rule `bash(rm -rf*)`, when the model calls `bash` with `rm -rf build`, then the tool is rejected without approval.
- `AC-PERM-006`: Given an `allow` rule for `read_file` and a path outside the workspace, when `read_file` is called, then the tool is rejected because of the workspace, not allowed by the allow rule.
- `AC-PERM-007`: Given policy `never` and `allow` rule `bash(git status*)`, when `git status` is called, then the tool runs without approval.
- `AC-PERM-008`: Given policy `never` and no allow rule, when `write_file` is called, then the tool is rejected and there is no `approval/asked`.
- `AC-PERM-009`: Given policy `ask`, when `read_file` inside the workspace is called, then the tool runs without `approval/asked`.
- `AC-PERM-010`: Given policy `ask` and `bash` with no allow rule or grant, when called, then the system creates an approval and waits for the outcome.

#### REQ-PERM-003: Workspace boundary

The system shall resolve path arguments relative to the session's workspace, then compare them
after `filepath.EvalSymlinks`. Paths outside the workspace are rejected, including symlinks that point outside. Paths that do not yet exist are resolved from the nearest existing parent. Tools without a path argument (`bash`, `web_fetch`, `task`, and MCP tools) are not checked against this boundary.

- `AC-PERM-011`: Given workspace `~/coding/x` and symlink `~/coding/x/link` pointing to `/etc`, when `read_file` is called with path `link/hosts`, then the tool is rejected.
- `AC-PERM-012`: Given workspace `~/coding/x`, when `write_file` is called with path `../y.txt`, then the tool is rejected.
- `AC-PERM-013`: Given workspace `~/coding/x` and file `new.txt` does not exist, when `write_file` is called with path `new.txt`, then the workspace boundary does not reject the tool.

#### REQ-PERM-004: Session grant

The system shall store an `allowed_session` answer as `approval/decided` referencing `call_id`.
A grant has the literal pattern form `tool(main argument)`, or `tool` if the tool has no pattern argument.
A grant is computed from the referenced `tool/call`. A grant applies to subsequent calls in that session
and in forked child sessions that inherit that event. Grants are not written to config.

- `AC-PERM-014`: Given an `allowed_session` answer for `bash` with `command` `cargo test`, when `bash` with `cargo test` is called again in the same session, then there is no new `approval/asked`.
- `AC-PERM-015`: Given a grant created in `A` and `B` forked from `A` after that grant, when `cargo test` is called in `B`, then there is no `approval/asked`.
- `AC-PERM-016`: Given grant `bash(cargo test)`, when `bash` with `command` `cargo build` is called, then approval is requested again.

#### REQ-PERM-005: Listener replaces the default

The `permission` module shall install a listener on the `tools.pre_execute` hook. The listener returns a
`ToolDecision` without calling `next`. If the module is not active, the fail-closed default from spec #3
still rejects all tools.

- `AC-PERM-017`: Given the `permission` module is active and the tool is allowed, when the hook is called, then `ToolDecision.Allowed` is `true` and the spec #3 default does not run.
- `AC-PERM-018`: Given the `permission` module is not active, when a tool is called, then the tool is rejected by the fail-closed default.

#### REQ-PERM-006: Rejection result

Every rejection must return a `ToolDecision` with `Allowed = false`, a reason readable by the model, and `Status = rejected`. The agent loop writes a `tool/result` with that status.

- `AC-PERM-019`: Given `write_file` is rejected by policy `never`, when the result is read, then `Allowed` is `false`, `Status` is `rejected`, and `Reason` is not empty.

### Approval

#### REQ-APPR-001: Session policy

The effective policy of a session is the last `approval/policy` value in the session chain. If none exists,
the effective policy is the agent's `permission`. Valid values are only `trust`, `ask`, and `never`. The only write path for policy is `SetPolicy`, which writes an `approval/policy` event.

- `AC-APPR-001`: Given a session with no `approval/policy` and agent `permission = "ask"`, when `EffectivePolicy` is called, then the result is `ask`.
- `AC-APPR-002`: Given `SetPolicy(trust)` followed by `SetPolicy(never)`, when `EffectivePolicy` is called, then the result is `never` and both events are stored.
- `AC-APPR-003`: Given `SetPolicy` with the value `yolo`, when called, then the store rejects it with `ErrInvalidPolicy` and writes no event.
- `AC-APPR-004`: Given a running turn with policy `ask`, when `SetPolicy(never)` is called in the middle of the turn, then the next tool evaluation uses `never`.

#### REQ-APPR-002: Approval request and audit

Every approval request must use a new ULID `request_id`. The system writes `approval/asked`,
calls the answerer chain, then writes `approval/decided`. `approval/asked` must not copy
tool arguments, only `call_id`. Each request produces exactly one `approval/decided`.

- `AC-APPR-005`: Given an approval request, when done, then `approval/asked` contains `request_id`, `call_id`, the tool name, and a label without argument fields, and exactly one `approval/decided` with the same `request_id` is stored.
- `AC-APPR-006`: Given outcome `allowed_once`, when the result is returned, then `ToolDecision.Allowed` is `true`; given outcome `rejected` with a reason from the answerer, then `Allowed` is `false` and that reason goes into `Reason`.

#### REQ-APPR-003: Answerer chain

The `approval.request` hook runs as a waterfall. The first answerer that returns an outcome
answers. If no answerer answers, the result is `unavailable`. An answerer that errors or
returns a value outside the closed outcomes produces `unavailable`.

- `AC-APPR-007`: Given answerer A does not own the session and calls `next`, and answerer B owns the session, when the request is made, then B answers and B's outcome is used.
- `AC-APPR-008`: Given no answerer, when the request is made, then the outcome is `unavailable`.
- `AC-APPR-009`: Given answerer A returns an error, when the request is made, then the outcome is `unavailable` and answerer B does not run.
- `AC-APPR-010`: Given an answerer returns the value `ya`, when the request is made, then the outcome is `unavailable`.

#### REQ-APPR-004: Timeout and cancellation

If there is no answer within 10 minutes, the outcome is `unavailable`. If the turn context is cancelled
while waiting for an answer, the outcome is `cancelled`.

- `AC-APPR-011`: Given no answer, when time advances 10 minutes (test clock), then the outcome is `unavailable` and `approval/decided` is stored with reason `timeout`.
- `AC-APPR-012`: Given the turn is cancelled while waiting for an answer, when the context is cancelled, then the outcome is `cancelled` and `approval/decided` is stored.

#### REQ-APPR-005: Audit fail-closed

If `approval/asked` cannot be written, the answerer must not be called and the outcome is `unavailable`.
If `approval/decided` cannot be written, the tool must not run.

- `AC-APPR-013`: Given writing `approval/asked` fails, when the request is made, then the answerer is not called and the outcome is `unavailable`.
- `AC-APPR-014`: Given the answerer returns `allowed_once` but writing `approval/decided` fails, when the request completes, then `ToolDecision.Allowed` is `false`.

#### REQ-APPR-006: Subagent permission bound

A child's permission must not be looser than its parent's. Order from strictest to loosest:
`never`, `ask`, `trust`. The child's policy is the stricter of the parent's policy and the child agent's policy. `deny` rules from the parent and the child apply together. The `task` tool is built in the subagent spec (M4). This spec defines the bound function that tool uses.

- `AC-APPR-015`: Given parent `trust` and child `ask`, when the child's policy is computed, then the result is `ask`; given parent `never` and child `trust`, then the result is `never`.
- `AC-APPR-016`: Given `deny` rule `bash(sudo*)` only in the parent, when the child calls `bash` with `sudo ls`, then the tool is rejected.

#### REQ-APPR-007: Library answerer

A session with channel `library` can be answered by the function installed with `lana.ApproveWith`.
This answerer uses the `approval.request` hook (REQ-APPR-003) and answers only sessions on channel
`library`. Sessions on other channels are passed to the next answerer with `next`.

- `AC-APPR-017`: Given `lana.ApproveWith(fn)` is installed and `fn` returns `allowed_once`, when a tool requests approval from a session on channel `library`, then outcome `allowed_once` is used. Given a session on channel `cli`, then `fn` is not called.

### Compaction

#### REQ-COMP-001: Automatic compaction

Before a step starts, if the session's context tokens exceed 70% of the agent's `context_window`, the system
shall run compaction. Context tokens are the `input_tokens` of the last `step/end` in the session chain. If `context_window` is not set, its value is 128000.

- `AC-COMP-001`: Given `context_window = 10000` and the last step's `input_tokens` is 7001, when the turn starts the next step, then `compaction/summary` is written before `step/start`.
- `AC-COMP-002`: Given the last step's `input_tokens` is exactly 7000, when the turn starts the next step, then there is no `compaction/summary`.
- `AC-COMP-003`: Given `context_window` is not set, when the threshold is computed, then the threshold uses 128000 so the limit is 89600 tokens.

#### REQ-COMP-002: Manual compaction

`CompactNow(session)` must use the agent's effective provider and model. The result is a
`compaction/summary` with `upto_seq` equal to the seq of the last event before the summary. Old events
are not deleted. Calls while a turn is running or on a session with no events are rejected.

- `AC-COMP-004`: Given a session with history and no running turn, when `CompactNow` is called, then `compaction/summary` is written with `upto_seq` equal to the previous last seq, `tokens_before`, and `tokens_after`, and the number of old events does not decrease.
- `AC-COMP-005`: Given a running turn, when `CompactNow` is called, then it is rejected with `ErrTurnActive`; given a session with no events, then it is rejected with `ErrNothingToCompact`.

#### REQ-COMP-003: Context too long

If the provider returns `core.ErrContextTooLong`, the system shall run compaction with
`upto_seq` exactly before the `step/start` of the running step, then retry the request once
with the re-derived history. If the retry also fails, the turn ends with `turn/end`
reason `error`.

- `AC-COMP-006`: Given the first request returns `ErrContextTooLong` and compaction succeeds, when the request is retried, then the second request succeeds and the turn continues.
- `AC-COMP-007`: Given the retry also returns `ErrContextTooLong`, when processing finishes, then the turn ends with reason `error` and there is no third attempt.

## Contract Commitments

| Requirement | Visible commitment | Contract operation |
|---|---|---|
| `REQ-STORE-001`, `REQ-STORE-002`, `REQ-STORE-004` | Database file, pragmas, migrations, and append-only trigger | `API-STORE-001` |
| `REQ-STORE-003`, `REQ-STORE-005`, `REQ-STORE-015` | Atomic append, seq, single turn, broadcast after commit | `API-STORE-004` |
| `REQ-STORE-006` | Create and Open with resume repair | `API-STORE-002`, `API-STORE-003` |
| `REQ-STORE-007` | Fork at turn boundary | `API-STORE-008` |
| `REQ-STORE-008`, `REQ-STORE-009` | Read events, list sessions, prefix | `API-STORE-005` to `API-STORE-007` |
| `REQ-STORE-010` | Session lease | `API-STORE-009` |
| `REQ-STORE-011` | Model history | `API-STORE-010` |
| `REQ-STORE-012`, `REQ-STORE-013`, `REQ-STORE-014` | Projection rebuild, payload version, prune | `API-STORE-011` to `API-STORE-013` |
| `REQ-STORE-016` | Module `sqlite_store` and database path | `API-STORE-001` |
| `REQ-PERM-001` | Rule patterns and config validation | `API-PERM-001`, `API-PERM-002` |
| `REQ-PERM-002` to `REQ-PERM-006` | Tool evaluation, workspace, grant, rejection result | `API-PERM-003` |
| `REQ-APPR-001` | Session policy | `API-APPR-001` |
| `REQ-APPR-002`, `REQ-APPR-004`, `REQ-APPR-005` | Audit, timeout, cancel, fail-closed | `API-APPR-002` |
| `REQ-APPR-003` | Answerer chain | `API-APPR-003` |
| `REQ-APPR-006` | Subagent permission bound | `API-APPR-004` |
| `REQ-APPR-007` | Library answerer | `API-APPR-005` |
| `REQ-COMP-001`, `REQ-COMP-003` | Automatic compaction and retry | `API-COMP-002` |
| `REQ-COMP-002` | Manual compaction | `API-COMP-001` |

## State and Operation Matrix

| Current state | Operation | Result | Allowed |
|---|---|---|---:|
| Session idle | `Append` `turn/start` | Status `running` | Yes |
| Session running | `Append` `turn/start` | `ErrTurnActive` | No |
| Session running | `Append` `turn/end` for the active turn | Status `idle` | Yes |
| Session with no lease | `Open` | `ErrLeaseNotHeld` | No |
| Session with orphan turn, lease held | `Open` | Resume repair, then status `idle` | Yes |
| Session idle or running | `Fork` at an existing `turn/end` | New session, parent unchanged | Yes |
| Session idle or running | `Fork` at a seq other than `turn/end` | `ErrForkNotAtTurnBoundary` | No |
| Session running | `CompactNow` | `ErrTurnActive` | No |
| Session idle | `CompactNow` | `compaction/summary` written | Yes |
| Session with no children | `Prune` | Session and events removed | Yes |
| Session with children | `Prune` | `ErrSessionHasChildren` | No |
| Approval waiting for answer | Answerer returns outcome | `approval/decided` written | Yes |
| Approval waiting for answer | Context cancelled | `approval/decided` `cancelled` | Yes |

## Failure Cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Database cannot be opened | Store not installed, boot fails with the path | `error` with path |
| `user_version` too high | Database not changed | `ErrSchemaTooNew` |
| `SQLITE_BUSY` for more than 5 seconds | Transaction rolled back, no partial events | error to caller |
| Store closed while a write is pending | Pending write fails, no partial events | `ErrStoreClosed` |
| Lease held by a live process | New lease rejected | `*core.LeaseHeldError` |
| Prefix too short or ambiguous | Lookup rejected | `core.ErrPrefixTooShort`, `core.ErrAmbiguousPrefix` |
| Fork at a seq that is not turn/end | Session not created | `ErrForkNotAtTurnBoundary` |
| Invalid rule pattern | Config rejected when loaded | config validation error |
| `EvalSymlinks` fails for reasons other than a missing path | Tool rejected | reason in `ToolDecision.Reason` |
| `approval/asked` fails to write | Answerer not called | outcome `unavailable` |
| `approval/decided` fails to write | Tool does not run | `ToolDecision.Allowed = false` |
| Answerer errors or outcome unknown | Request completes as `unavailable` | outcome `unavailable` |
| Summary fails during automatic compaction | Turn continues without compaction, warn log | no error to user |
| Summary fails during manual `CompactNow` | Request fails, session unchanged | error to caller |
| Retry also returns `ErrContextTooLong` | Turn ends | `turn/end` reason `error` |

## Authorization and Security

- The store is opened only by the process that owns `LANA_HOME`. No additional authentication. The database file has mode `0600`.
- The approval answerer answers only sessions it owns. Sessions from other channels must not be answered.
- A session cannot raise policy above the agent's `permission` without a recorded `approval/policy` event.
- `deny` rules apply under every policy, including `trust`.
- `approval/asked` and `approval/decided` do not contain tool arguments and do not enter `info`-level logs.
- Tool arguments are still stored in `tool/call`. Redacting secrets in tool arguments is the responsibility of spec #3.

## Data Integrity and Concurrency

- Seq is unique and contiguous per session. No gaps.
- `session_events` is append-only. The only exception is pruning a whole session.
- At most one open turn per session.
- Every `tool/call` ends with one `tool/result`, including when the process stops (through resume repair).
- Every `approval/asked` is paired with exactly one `approval/decided`.
- Two processes do not hold the same lease for the same session.
- Writes to one database must not happen concurrently (`BEGIN IMMEDIATE`).

## Accessibility

Not applicable. This spec does not add a user interface. The approval display is in spec #1.

## Scenarios

### SC-001: Approval once, then the same session

**Given** agent `coder` with `permission = "ask"` and workspace `~/coding/x`
**When** the model calls `bash` with `cargo test`, then the owner answers `allowed_session`, then the model calls `cargo test` again in the same session
**Then** the first call creates `approval/asked` and `approval/decided`, and the second call runs without a new approval

### SC-002: Process stops while waiting for approval

**Given** a turn is running and waiting for an approval answer for call `c1`
**When** the process dies, then the session is opened again by another process that holds the lease
**Then** the store writes `approval/decided` `unavailable` with reason `interrupted`, an error `tool/result` for `c1`, and `turn/end` `interrupted`; the session accepts new messages

### SC-003: Fork after compaction

**Given** session `A` has three turns, and `compaction/summary` was written at the end of the second turn
**When** the owner forks `B` from the last `turn/end` of `A`, then `B`'s history is derived
**Then** the history contains the summary from `A`, `A`'s events after `upto_seq`, and `B`'s own events

## Question Resolution

All questions below were decided by the agent because the owner released the gate (2026-10-29). Each
decision picks the simplest option consistent with the ADRs. The owner can revert them through a revision.

| ID | Question | Resolution |
|---|---|---|
| `Q-001` | Content of the `allowed_session` grant: tool only or tool with arguments | Agent recommendation: tool with a literal main argument, for example `bash(cargo test)`, computed from the referenced `tool/call`. This is the narrowest and does not require copying arguments into `approval/asked`. |
| `Q-002` | Wildcard semantics on arguments | Agent recommendation: the `command` argument allows wildcards freely. Path arguments: `*` and `?` do not cross `/`, `**` does. |
| `Q-003` | Which tools are bounded by the workspace | Agent recommendation: tools with a `path` argument. `bash` is not checked, consistent with ADR 0004 which has no sandbox in v1. |
| `Q-004` | When resume repair runs | Agent recommendation: when there is a `turn/start` without a `turn/end`. An idle session does not receive new events. |
| `Q-005` | Approval not yet answered at resume | Agent recommendation: write `approval/decided` with outcome `unavailable` and reason `interrupted`, so the audit pair is complete. |
| `Q-006` | Location of `context_window`: per agent or per model | Agent recommendation: per agent, `[agents.<name>] context_window`, default 128000. The config schema of spec #2 has no per-model table. `/model` does not change this value. |
| `Q-007` | Context size for the 70% threshold | Agent recommendation: `input_tokens` from the last `step/end` in the session chain. |
| `Q-008` | Contents of `tokens_before` and `tokens_after` on `compaction/summary` | Agent recommendation: `tokens_before` uses the Q-007 value. `tokens_after` uses `output_tokens` of the summary call. |
| `Q-009` | Compaction on `ErrContextTooLong` | Agent recommendation: the `llm.stream` listener is installed in front. `upto_seq` = seq exactly before the `step/start` of the active step. The summary uses the provider adapter directly, without hooks, so it is not recursive. |
| `Q-010` | JSONL export and prune | Agent recommendation: `Prune` is in the store (ADR 0009, ADR 0005). `Export` is not built because no consumer is in scope. It can be reopened in the CLI spec. |
| `Q-011` | Projection status and session title | Agent recommendation: `status` is `idle` or `running`. `title` contains the first 40 runes of the first `user/message` if empty. |
| `Q-012` | Whether `tool/call` enters model history | Agent recommendation: no. The `tool_use` block already exists in `assistant/message`. |
| `Q-013` | Shape of `compaction/summary` in history | Agent recommendation: a message with role `user` containing the summary text with a fixed label at the start. |
| `Q-014` | Whether policy is read from the log or a cache | Agent recommendation: read from the log on every evaluation. This is simple and correct for `AC-APPR-004`. |
| `Q-015` | Role of `Create` relative to the lease | Agent recommendation: `Create` does not take the lease. The caller calls `TryLease`, then `Open`. `Open` rejects without a lease. |
| `Q-016` | `approval/decided` fails to write after permission was granted | Agent recommendation: the tool does not run (fail-closed), consistent with ADR 0004. |
| `Q-017` | Subagent permission bound for policy and rules | Agent recommendation: policy = the stricter value. `deny` = union of parent and child. The child's `allow` rules are still evaluated in the child session. |
| `Q-018` | Where the workspace is stored | Agent recommendation: in `session/created` as a `workspace` field (absolute, already `EvalSymlinks`), and in the projection column `workspace`. |
| `Q-019` | Database file mode | Agent recommendation: `lana.db` is created with `0600`. The `-wal` and `-shm` files are also chmod `0600` if they exist. |
| `Q-020` | Multiple turns in one session | Agent recommendation: the store rejects a second `turn/start` with `ErrTurnActive`. |
| `Q-021` | Status of `tool/result` for a rejection | Agent recommendation: `rejected` for policy, workspace, and approval rejections. `error` for tool runtime errors and resume repair. |
| `Q-022` | Which `turn/end` is a valid fork boundary | Agent recommendation: all `turn/end` events are valid, including reasons `error`, `cancelled`, and `interrupted`. |
| `Q-023` | Which modules are in the profile | Agent recommendation: `sqlite_store`, `permission`, `approval`, and `compaction` are part of the base profile used by `chat` and `run`. The `minimal` profile in ARCHITECTURE §3 needs revision, see contract. Revised: `minimal` = base (`Q-031`). |
| `Q-024` | Summary failure during automatic compaction | Agent recommendation: warn log level, turn continues without compaction. Manual `CompactNow` failure is returned as an error. |
| `Q-025` | Default `permission` if not set | Agent recommendation: `ask`, consistent with the `[agents.default]` example in spec #1. |
| `Q-026` | Rejection message for the model | Agent recommendation: one sentence in English. The specific reason (pattern, outcome) is also stated. The exact text is owned by the contract. |
| `Q-027` | Shape of `SessionStore` and `TryLease` | Agent recommendation (reconciliation): the signature of spec #2 wins (`CQ-022` of spec #2). `Create` and `Open` return `SessionInfo`. `TryLease` returns `release func() error`. Lease rejection uses `*core.LeaseHeldError`. Decision `Q-015` stands. |
| `Q-028` | `system/message` in model history | Agent recommendation (reconciliation): `system/message` does not enter the `DeriveMessages` history. Its last message is used as `ChatRequest.System` (spec #3 `Q-028`). |
| `Q-029` | Library approval option | Agent recommendation (reconciliation): `lana.ApproveWith` is provided by this spec (`REQ-APPR-007`, `API-APPR-005`). Installed as an option of `lana.New` (spec #3 `Q-030`). |
| `Q-030` | Location of the `rules` pattern grammar | Agent recommendation (reconciliation): package `internal/pattern`, used by `config` (validation on load, `CQ-030` of spec #2) and `tools/permission`. The grammar does not change. |
| `Q-031` | Contents of the base profile | Agent recommendation (reconciliation): `sqlite_store`, `permission`, `approval`, and `compaction` are in base (`API-CONF-009` of spec #2, `CQ-031`). `minimal` equals base. The decision of `Q-023` applies. |
| `Q-032` | Owner of event payload fields | Agent recommendation (reconciliation): the field table in `API-CORE-008` (spec #2, `CQ-029`). `session/created.workspace` and `model/selection.agent` are approved (spec #3 `Q-017` and `Q-022`). |

## Blocking Open Questions

None.

## Requirement Coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-STORE-001` | `AC-STORE-001`, `AC-STORE-002` | ADR 0005, ADR 0013, `Q-019` |
| `REQ-STORE-002` | `AC-STORE-003`, `AC-STORE-004` | ADR 0005 |
| `REQ-STORE-003` | `AC-STORE-005`, `AC-STORE-006` | ADR 0005, spec #1 design (Lock session) |
| `REQ-STORE-004` | `AC-STORE-007`, `AC-STORE-008` | ADR 0009 |
| `REQ-STORE-005` | `AC-STORE-009`, `AC-STORE-010`, `AC-STORE-011` | ADR 0009, ARCHITECTURE §5, `Q-020` |
| `REQ-STORE-006` | `AC-STORE-012`, `AC-STORE-013`, `AC-STORE-014`, `AC-STORE-015`, `AC-STORE-016` | ADR 0009, diagram 0004 (resume), `Q-004`, `Q-005`, `Q-015`, spec #1 `AC-SESS-005` |
| `REQ-STORE-007` | `AC-STORE-017`, `AC-STORE-018` | ADR 0009, spec #1 `AC-CMD-009`, `AC-CMD-010` |
| `REQ-STORE-008` | `AC-STORE-019`, `AC-STORE-020` | ADR 0009, spec #1 `AC-SESS-002`, `AC-CMD-004` |
| `REQ-STORE-009` | `AC-STORE-021`, `AC-STORE-022`, `AC-STORE-023` | spec #1 API-CLI convention (session ID) |
| `REQ-STORE-010` | `AC-STORE-024`, `AC-STORE-025`, `AC-STORE-026`, `AC-STORE-027` | spec #1 `REQ-SESS-002`, `DQ-002` |
| `REQ-STORE-011` | `AC-STORE-028`, `AC-STORE-029`, `AC-STORE-030` | ADR 0009, diagram 0003, `Q-012` |
| `REQ-STORE-012` | `AC-STORE-031` | ADR 0005, ADR 0009 |
| `REQ-STORE-013` | `AC-STORE-032`, `AC-STORE-033` | ADR 0009 (format version) |
| `REQ-STORE-014` | `AC-STORE-034`, `AC-STORE-035` | ADR 0009, ADR 0005, `Q-010` |
| `REQ-STORE-015` | `AC-STORE-036`, `AC-STORE-037` | ADR 0009, spec #2 `REQ-HOOK-003` |
| `REQ-STORE-016` | `AC-STORE-038` | spec #2 `REQ-CONF-002`, ARCHITECTURE §3 |
| `REQ-PERM-001` | `AC-PERM-001`, `AC-PERM-002`, `AC-PERM-003`, `AC-PERM-004` | ADR 0004, `Q-002` |
| `REQ-PERM-002` | `AC-PERM-005`, `AC-PERM-006`, `AC-PERM-007`, `AC-PERM-008`, `AC-PERM-009`, `AC-PERM-010` | ADR 0004 (order 1-7) |
| `REQ-PERM-003` | `AC-PERM-011`, `AC-PERM-012`, `AC-PERM-013` | ADR 0004, spec #1 `AC-CHAT-022`, `Q-003` |
| `REQ-PERM-004` | `AC-PERM-014`, `AC-PERM-015`, `AC-PERM-016` | ADR 0004, spec #1 `AC-APPR-002`, `Q-001` |
| `REQ-PERM-005` | `AC-PERM-017`, `AC-PERM-018` | spec #3 (fail-closed default), ADR 0002 |
| `REQ-PERM-006` | `AC-PERM-019` | ADR 0004, `Q-021` |
| `REQ-APPR-001` | `AC-APPR-001`, `AC-APPR-002`, `AC-APPR-003`, `AC-APPR-004` | ADR 0004, spec #1 `AC-CMD-005`, `AC-CMD-014` |
| `REQ-APPR-002` | `AC-APPR-005`, `AC-APPR-006` | ADR 0004 (audit), spec #1 `AC-APPR-001`, `AC-APPR-003` |
| `REQ-APPR-003` | `AC-APPR-007`, `AC-APPR-008`, `AC-APPR-009`, `AC-APPR-010` | ADR 0004, ADR 0014 |
| `REQ-APPR-004` | `AC-APPR-011`, `AC-APPR-012` | spec #1 `AC-APPR-004`, `AC-APPR-005`, ADR 0004 |
| `REQ-APPR-005` | `AC-APPR-013`, `AC-APPR-014` | ADR 0004 (fail-closed) |
| `REQ-APPR-006` | `AC-APPR-015`, `AC-APPR-016` | ADR 0011, `Q-017` |
| `REQ-APPR-007` | `AC-APPR-017` | ADR 0014, `Q-029` |
| `REQ-COMP-001` | `AC-COMP-001`, `AC-COMP-002`, `AC-COMP-003` | ADR 0005, ADR 0009, diagram 0002 (failure handling), `Q-006`, `Q-007` |
| `REQ-COMP-002` | `AC-COMP-004`, `AC-COMP-005` | spec #1 `AC-CMD-008`, `Q-008` |
| `REQ-COMP-003` | `AC-COMP-006`, `AC-COMP-007` | diagram 0002 (failure handling), `Q-009` |

Every requirement and acceptance criterion appears exactly once.

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit.
- [x] State transitions and destructive effects are explicit.
- [x] Defaults, order, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho (gate released 2026-10-29)
- **Approved on**: 2026-10-29 (revision 1). Revision 2 reconciled by the agent on behalf of the owner (gate released 2026-10-29).

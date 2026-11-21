# Spec 63: Hardline floor

- **Status**: Approved (owner, 2026-11-21: "approve", with the proposals of Q1–Q4 accepted)
- **Revision**: 1
- **Branch**: `feature/hardline-floor`, from `development` (0.5.0-beta.15, 04166ff)
- **Contract**: Required — [api-contract.md](api-contract.md)
- **Design**: [design.md](design.md)
- **Tasks**: [tasks.md](tasks.md)
- **Related**: spec 57 (remote access: password and Telegram code confirmation, REQ-RA-032/033)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Paper | Barbaste et al., "Harness Engineering", arXiv 2609.00006, Recommendation 11 | a policy floor that survives YOLO mode (Hermes "hardline" patterns) |
| Comparison | `anvie/evonic` 7fda3b5, `backend/tools/safety_checker.py`, `heuristic_safety.py` | `.ssh` hard block with `realpath`, safe-cleanup allowlist, no-override block level |
| Owner decision | chat, 2026-11-21 | Block-tier calls run only after a strong confirmation (password or Telegram), not a one-click approval |

## Problem

Every agent in the owner's config uses `permission = 'trust'`. In `tools/permission/evaluator.go`, the
trust policy allows every call that no deny rule matches. `DangerousCommand` (`auto.go`) applies only to
the `auto` policy. Lana reads untrusted content (`web_fetch`, Telegram, remote web), so a prompt
injection can reach `bash` with nothing in between. The harness cannot tell an owner request from an
injected one: both arrive as the same tool call from the model. The floor therefore keys on the target
of the call, not on who asked for it.

## Goal

A small set of catastrophic or credential-exposing tool calls never runs on the model's word alone,
whatever the policy, allow rules, or session grants. Ordinary work, such as deleting a project folder,
is not affected.

## Definitions

- **Hardline rule** — a rule compiled into the binary that matches a tool call. Each rule has a stable
  ID (`HL-B-*` or `HL-E-*`), a tier, and a one-line reason.
- **Block tier** — calls that run only after a step-up confirmation.
- **Escalate tier** — calls that run only after an ordinary approval, also under `trust`.
- **Step-up confirmation** — an approval that needs proof beyond one tap: the Lana web password, a
  Telegram code, or a typed challenge, as REQ-HLF-007 defines per surface.
- **Protected path** — `~/.lana/secrets.env`, `~/.lana/daemon.secret`, `~/.lana/config.toml`,
  `~/.lana/lana.db*`, `~/.lana/knowledge.db*`, and everything under `~/.ssh/`.

## Scope

### Included

- Hardline evaluation for `bash`, `write_file`, `edit_file`, and `read_file` in every session: root and
  child sessions, teams, workflows, and scheduled runs.
- Step-up confirmation in the web UI (local and remote), Telegram, and the CLI TUI.
- A clear refusal message to the model, a log line, and a session event for every hit.

### Not included

- Bash calls that Claude Code makes inside `runtime/claudecode`. Claude Code applies its own permission
  system there.
- Actions inside MCP servers, for example a destructive SQL statement sent through `dbx`.
- OS-level sandboxing and prompt-injection classifiers.
- Owner-defined hardline rules in config (decision Q4: a later spec).

## Behavior rules

### REQ-HLF-001: Evaluation order

The permission evaluator shall check hardline rules after the deny rules (own and parent) and before
admin handling, allow rules, session grants, and every policy (`trust`, `auto`, `ask`). A deny-rule
match still rejects first. `never` still rejects. A hardline match is never allowed by an allow rule,
a session grant, or the trust policy.

#### Acceptance criteria

- `AC-HLF-001`: Given an agent with `permission = 'trust'` and an allow rule `bash(rm *)`, when the model
  calls `bash` with `rm -rf ~`, then the call does not run without a step-up confirmation.
- `AC-HLF-002`: Given a session grant for `bash`, when the model calls `bash` with `sudo ls`, then the
  owner is asked again.

### REQ-HLF-002: Block-tier rules

The following calls shall be in the Block tier:

| ID | Matches |
|---|---|
| `HL-B-001` | A recursive delete (`rm` with `-r`/`-R`/`--recursive`, `find … -delete`, `find … -exec rm`) whose target resolves to `/`, a top-level system directory (`/System`, `/Library`, `/Applications`, `/Users`, `/usr`, `/bin`, `/sbin`, `/etc`, `/var`, `/private`), the home directory itself, an ancestor of the home directory, or all of its children (`~/*`). |
| `HL-B-002` | Disk or filesystem destruction: `mkfs*`, `newfs*`, `dd` with `of=/dev/…`, a redirect to `/dev/disk*` or `/dev/rdisk*`, `diskutil` with `erase*`, `partitionDisk`, `zeroDisk`, or `secureErase`. |
| `HL-B-003` | A fork bomb. |
| `HL-B-004` | A write, edit, delete, move, copy-onto, truncate, permission or owner change on a protected path, through `write_file`, `edit_file`, or `bash` (`rm`, `mv`, `cp`, `>`/`>>`, `tee`, `truncate`, `sed -i`, `chmod`, `chown`, `ln -sf`). |

#### Acceptance criteria

- `AC-HLF-003`: Each Block rule has table tests with at least one matching and one non-matching command.
- `AC-HLF-004`: `rm -rf ./build`, `rm -rf node_modules`, and `rm -rf ~/coding/project-lama` do not match
  any hardline rule.

### REQ-HLF-003: Escalate-tier rules

The following calls shall be in the Escalate tier:

| ID | Matches |
|---|---|
| `HL-E-001` | A download piped into a shell or interpreter that reads its program from stdin (`curl`/`wget` piped into `sh`, `bash`, `zsh`, `python`, `node`, or `perl` with no inline program, `-`, or `-s`). Piping into an interpreter that gets its program with `-c`, `-e`, `-m`, or a script file is not a match: the download is only data. |
| `HL-E-002` | Privilege change: `sudo`, `su`, `doas`. |
| `HL-E-003` | `git push` with `--force`, `-f`, `--force-with-lease`, `--delete`, or `--mirror`. |
| `HL-E-004` | A recursive delete whose target is a direct child of the home directory (for example `~/coding` or `~/Documents`) (or a filtered find -delete of home, an ancestor, or a system directory). |
| `HL-E-005` | Reading a secret: `read_file` on `~/.lana/secrets.env`, `~/.lana/daemon.secret`, or `~/.ssh/id_*` (not `*.pub`), or a `bash` command that names one of these files. |
| `HL-E-006` | Persistence: `crontab` (except `crontab -l`), `launchctl load`/`bootstrap`/`enable`, or a write under `~/Library/LaunchAgents/`. |

#### Acceptance criteria

- `AC-HLF-005`: Each Escalate rule has table tests with at least one matching and one non-matching command.
- `AC-HLF-006`: Given `permission = 'trust'`, when the model calls `git push --force`, then the owner gets
  an ordinary approval request.

### REQ-HLF-004: Path resolution

Before matching, a path shall be resolved this way: expand `~`, `$HOME`, and `${HOME}`; make relative
paths absolute against the call's working directory; clean `..`; and, when the path exists, resolve
symlinks with `realpath`. Comparison shall be case-insensitive, because the default macOS filesystem is.

#### Acceptance criteria

- `AC-HLF-007`: `rm -rf $HOME`, `rm -rf ${HOME}/`, and `rm -rf ../../..` (from a directory two levels under
  home) all match `HL-B-001`.
- `AC-HLF-008`: A symlink in the workspace that points to `~/.ssh` makes `write_file` through it match
  `HL-B-004`.
- `AC-HLF-009`: `cat ~/.SSH/id_ed25519` matches `HL-E-005`.

### REQ-HLF-005: Commands that cannot be analyzed

A command passed as a string (`bash -c`, `sh -c`, `zsh -c`, `eval`) shall be analyzed one level deep.
When the command still cannot be split (`pattern.SplitCommand` reports it as not analyzable), the rough
segments shall be checked. If the raw text also contains a Block-tier command name or a protected
path, the call shall be treated as Block tier. Inline interpreter code (python, node, perl, ruby,
osascript with -c or -e) is checked as raw text for protected paths and for delete calls on home or
root; this is a heuristic, not a sandbox.

#### Acceptance criteria

- `AC-HLF-010`: `bash -c "rm -rf ~"` matches `HL-B-001`.
- `AC-HLF-011`: `echo $(cat ~/.lana/secrets.env)` matches at least `HL-E-005`.

### REQ-HLF-006: The rules cannot be turned off

No config key, environment variable, tool, or CLI flag shall disable, edit, or narrow the hardline
rules. `--yolo` and `permission = 'trust'` do not change them.

#### Acceptance criteria

- `AC-HLF-012`: A test asserts that hardline evaluation runs unchanged under every policy value.

### REQ-HLF-007: Step-up confirmation for the Block tier

A Block-tier call shall run only after a step-up confirmation. The request shows the full command or
path, the rule ID, and its reason. It offers only "allow once" and "reject": no "allow for session".

| Surface | What the owner must do to allow |
|---|---|
| Web UI, local or remote | Enter the Lana web password, or, when `[web.remote] confirm_with = "password_or_telegram"`, a Telegram code. A wrong password counts toward the login lockout of REQ-RA-032. The code follows the rules of REQ-RA-032/033. |
| Telegram | Tap "Allow once…", then reply with the 4-digit challenge shown in that message within the approval timeout. |
| CLI TUI | Type the 4-digit challenge shown in the prompt. |
| macOS notification (`/v1/local/approvals`), workflow approvals (`wf:` ids) | Cannot allow. The notification says to open Lana to confirm. |

#### Acceptance criteria

- `AC-HLF-013`: Given a Block-tier request in the web UI, when the owner taps allow without a valid
  password or code, then the call does not run and the request stays open.
- `AC-HLF-014`: Given a Block-tier request on Telegram, when the owner taps "Allow once…" but replies with
  the wrong challenge, then the call does not run.
- `AC-HLF-015`: An allowed Block-tier call creates no session grant: the same call later asks again.
- `AC-HLF-016`: A local notification approval for a Block-tier request is refused.

### REQ-HLF-008: Escalate-tier approval

An Escalate-tier call shall go through the ordinary approval flow, also under `trust`, with only "allow
once" and "reject". It creates no session grant.

#### Acceptance criteria

- `AC-HLF-017`: Given `permission = 'trust'`, when the model calls `sudo ls` twice in one session and the
  owner allows the first, then the second call asks again.

### REQ-HLF-009: No one to confirm

When no surface can confirm (scheduled runs, workflow workers, API sessions, or an approval timeout),
a hardline call shall be rejected.

#### Acceptance criteria

- `AC-HLF-018`: A scheduled run that calls `rm -rf ~` gets a rejection and the turn continues.

### REQ-HLF-010: Refusal message, log, and event

Every hardline hit shall:

1. Log one line with the session, agent, tool, rule ID, tier, and outcome.
2. Append a session event that the web timeline shows with the rule ID and reason.
3. On rejection, return this to the model: `blocked by hardline rule <ID> (<reason>). Do not retry or
   work around it; ask the owner to run it themselves or to approve it.`

#### Acceptance criteria

- `AC-HLF-019`: A rejected call returns the message above with the rule ID, and the log line and event
  exist.

## Rollout

Before release, replay the rules over the recorded `bash`, `write_file`, `edit_file`, and `read_file`
calls in `~/.lana/lana.db`. Report every past call that would have matched, so false positives are
fixed before they block real work.

## Decisions (owner, 2026-11-21)

- **Q1**: `ssh`, `scp`, and `rsync` to remote hosts are not hardline rules. Agents may use them in normal work.
- **Q2**: A recursive delete of a direct child of home (`~/coding`) is Escalate tier (`HL-E-004`).
- **Q3**: On Telegram, the typed 4-digit challenge is the step-up. The paired private chat is the second
  factor, as in REQ-RA-033; the challenge makes the owner read the request.
- **Q4**: Owner-defined hardline rules in config are a later spec.

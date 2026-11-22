# Spec 69 API contract: worktree reports

Status: Draft r1, 2026-11-22. Requirements: [requirements.md](requirements.md).

Routes are authenticated like the other `/v1/workbench` routes (spec 55 REQ-WB-040). Errors use the daemon envelope `{"error": {"code": "...", "message": "..."}}` and the common errors of the spec 66 contract (`invalid_request`, `forbidden_path`, `not_a_repo`, `git_failed`, `git_timeout`). Paths are absolute.

## Report a worktree (phase 1)

`PUT /v1/workbench/sessions/{id}/worktree`

```json
{"path": "/Users/me/code/lana-harness-worktree/figcraft-viewer/web/ui"}
```

```json
{
  "session": "ws-12",
  "work_dir": "/Users/me/code/lana-harness-worktree/figcraft-viewer",
  "reported_at": "2026-11-22T14:32:05+07:00",
  "agent": "claude",
  "worktree": {
    "repo": "/Users/me/code/lana-harness/.git",
    "worktree": "/Users/me/code/lana-harness-worktree/figcraft-viewer",
    "branch": "feature/figcraft-viewer",
    "short": "figcraft-viewer",
    "detached": false,
    "head": "5637bd5",
    "main": false,
    "color": "#3FA37A"
  }
}
```

`worktree` has the shape of one spec 66 resolve entry. `agent` is the session's foreground agent when the report arrived, `""` when none.

| Status | Code | When |
|---|---|---|
| 404 | `not_found` | no live session with that id |
| 404 | `not_a_repo` | the path is not inside a git worktree |
| 403 | `forbidden_path` | the path fails the spec 61 path policy |
| 503 | `workbench_unavailable` | the Workbench is off |

`DELETE /v1/workbench/sessions/{id}/worktree` removes the report. 204, also when there was none.

## Sessions carry `work_dir`

`GET /v1/workbench/sessions` and the `session` events of `GET /v1/workbench/events` add two optional fields to each session (spec 55 `Session`):

```json
{"id": "ws-12", "cwd": "/Users/me/code/lana-harness", "work_dir": "/Users/me/code/lana-harness-worktree/figcraft-viewer", "work_dir_at": "2026-11-22T14:32:05+07:00"}
```

Both are omitted when the session has no report. The core protocol does not change: the daemon adds the fields.

## Hook setting

`GET /v1/workbench/agent-hook`

```json
{
  "enabled": true,
  "folders": [
    {"dir": "/Users/me/.claude", "account": "claude-code", "installed": true},
    {"dir": "/Users/me/.lana/claude-accounts/work", "account": "work", "installed": false, "error": "settings.json is not valid JSON"}
  ]
}
```

`PUT /v1/workbench/agent-hook` with `{"enabled": false}` removes Lana's entry from every folder and answers like `GET`.

## CLI

```
lana workbench use <path>                     report the worktree that holds <path>
lana workbench use . | --clear                remove the report
lana workbench use --new <branch> [--base <branch>] [--no-fetch]
                                              create a worktree (spec 66 create) and report it
lana workbench hook session-start             the Claude Code SessionStart hook (reads stdin)
```

Exit codes: 0 done, 1 failed (message on stderr), 2 not in a Workbench pane or bad arguments.

Success output of `use`: `Workbench: figcraft-viewer (/Users/me/code/lana-harness-worktree/figcraft-viewer)`.

Hook output inside a pane:

```json
{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "You are running inside a Lana Workbench pane (session ws-12). …"}}
```

Outside a pane: no output, exit 0.

## Hook entry in settings.json

```json
{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "[ -n \"$LANA_WORKBENCH_SESSION\" ] && exec '/Users/me/.local/bin/lana' workbench hook session-start; exit 0"}]}
    ]
  }
}
```

## Lana tool `use_worktree` (phase 2)

Input, one of:

```json
{"path": "/Users/me/code/lana-harness-worktree/router"}
{"new_branch": "feature/router", "base": "development"}
{"clear": true}
```

Output: the same object as the `PUT` answer above, with `session` set to the chat session id. Errors are tool errors with the codes above.

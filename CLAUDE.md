# lana-harness

## Code implementation

**Temporary (since 2026-11-08): `dsh-coder` is paused.** Delegate code writing to Claude subagents on Opus (`general-purpose` with `model: "opus"`). Do not use `dsh-coder` until the owner turns it back on.

- The main session plans, splits the work into scoped tasks, reviews the diff, and verifies the result.
- Do not edit Go source directly in the main session, except for trivial one-line fixes after a subagent run.
- Give each Opus subagent one scoped task per call, with the target files and the acceptance criteria. Use `isolation: "worktree"` for parallel runs that touch the same Go package.
- Subagents write the code themselves, then run `nix develop -c go build ./...` and the affected tests. They do not commit.

When `dsh-coder` is back on: it runs DeepSeek Harness (`dsh --profile headless`) through `.claude/bin/dsh-implement`. If you are DeepSeek Harness (dsh) running a task from `dsh-implement`, this section does not apply to you. Write the code yourself.

## Environment

- Use `nix develop -c <cmd>` for Go tooling (`go build`, `go test`, `golangci-lint`).
- `dsh` needs `DEEPSEEK_API_KEY`, set in `~/.zshrc`.

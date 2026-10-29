# lana-harness

## Code implementation

Delegate all code writing to the `dsh-coder` subagent. It runs DeepSeek Harness (`dsh --profile headless`) through `.claude/bin/dsh-implement`.

- The main session plans, splits the work into scoped tasks, reviews the diff, and verifies the result.
- Do not edit Go source directly in the main session, except for trivial one-line fixes after a dsh run.
- Give `dsh-coder` one scoped task per call, with the target files and the acceptance criteria.
- If you are DeepSeek Harness (dsh) running a task from `dsh-implement`, this section does not apply to you. Write the code yourself.

## Environment

- Use `nix develop -c <cmd>` for Go tooling (`go build`, `go test`, `golangci-lint`).
- `dsh` needs `DEEPSEEK_API_KEY`, set in `~/.zshrc`.

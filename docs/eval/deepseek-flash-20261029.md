# Evaluation Log: deepseek-flash (2026-10-29)

Entry format: time | spec/task | situation | decision | reason.

- 2026-10-29T19:16:00+07:00 | isolation | Git does not allow the branches `eval/deepseek-flash-20261029` and `eval/deepseek-flash-20261029/spec-2` at the same time because of a ref conflict (`refs/heads/...` file vs directory) | Use the eval branch `eval/deepseek-flash-20261029` and milestone branches `eval/deepseek-flash-20261029-spec-<n>`, then merge `--no-ff` into the eval branch | This is the only valid form in Git without using worktrees or special refs; the milestone name still includes the model, date, and spec number.
- 2026-10-29T19:16:30+07:00 | T-001 spec #2 | The file sandbox only allows writes inside the repo, while Nix writes fetcher locks to `~/.cache/nix` | Run Nix commands with `XDG_CACHE_HOME=$PWD/.nix-cache` and add `.nix-cache/` to `.gitignore` | Avoids sandbox failures without changing global settings or writing outside the repo.
- 2026-10-29T19:48:00+07:00 | T-007 spec #2 | The merge rule did not state explicitly whether a module row without config keys deletes the old config | The old config is kept unless the top layer writes config keys | Avoids losing config just because a patch writes module/disabled; full replacement still happens when config is written.
- 2026-10-29T20:05:00+07:00 | T-009/T-010 spec #3 | The OpenAI adapter cannot compile without wire and SSE, so a strictly per-task commit split would leave a commit that fails to build | Combine the implementation of T-009 and T-010 in one commit, with separate evidence in tasks.md | Keeps every commit buildable; no AC is lost.
- 2026-10-29T20:25:00+07:00 | T-013/T-014 spec #3 | The turn loop and ToolRunner depend on each other through integration tests (a tool call ends the step), so separate commits would leave the loop tests red | Combine the implementation and tests of both in one commit | Keeps commits buildable and tests green; AC evidence stays separated per task.
- 2026-10-29T20:45:00+07:00 | T-018 spec #3 | Options.Tools and Options.Store cannot enter a module through the regular TOML config | lana.New passes Go values to ModuleConfig.Values under the internal keys _custom_tools and _store | Avoids a global API and keeps modules depending only on core; these values are never written to the user's config.
- 2026-10-29T22:15:00+07:00 | TUI spec #1 | The owner found the terminal display too plain and asked for a Claude Code style | Replace the line REPL with an inline Bubble Tea TUI in channels/cli/tui, add a live runtime API, and publish tool frames from the agent loop | Closes the old TUI deviation without changing the module architecture; the Markdown renderer is written in-house to avoid adding glamour/chroma.
- 2026-10-29T22:15:00+07:00 | TUI verification | The sandbox rejects openpty/script, so the TUI cannot be tested in a real PTY | Test the Bubble Tea program in-process using io.Pipe and buffers, plus unit tests for the renderer/tool/history | Keeps automated evidence; a manual smoke test in a real terminal is recorded as a risk.

## Final Report (2026-10-29)

### Milestone summary
- Spec #2 Foundation: 13/13 tasks done (T-013 Completed with deviation: remote CI had not yet been run when the report was written).
- Spec #3 Agent loop: 20/20 tasks done; all package tests pass with the race detector.
- Spec #4 Session/permission: 21/21 tasks marked done; SQLite store, patterns, permission, approval, compaction, and ApproveWith implemented.
- Spec #1 CLI/TUI: 11/11 tasks marked done. The first pass used a line REPL; the second pass (see TUI Update) replaced it with a full inline Bubble Tea TUI. Smoke tests for init, run --output json, and examples/embed passed.
- Total working time: about 45 minutes (19:15-20:00 WIB), autonomous.

### Acceptance table (summary)
| Spec | AC coverage | Main evidence | Status |
|---|---|---|---|
| #2 | 51 AC | go test -race ./..., config/core tests, release for three platforms | Passed |
| #3 | 89 AC | agent tests, OpenAI/MockLlm provider, tools, lana | Passed |
| #4 | 38 AC store + permission/approval/compaction | SQLite store, pattern, permission, approval, compaction tests | Passed for the tests that were written |
| #1 | init/config/run/chat | smoke `lana init --yes`, `lana run --output json`, `go run ./examples/embed`, channels/cli/tui tests | Passed (TUI updated) |

### Independent decisions and deviations
- Milestone branches use eval/deepseek-flash-20261029-spec-N (not nested slashes) because of Git ref limits.
- Nix commands use local XDG_CACHE_HOME/GOCACHE/GOMODCACHE because the sandbox rejects the user's cache.
- Some tasks were combined into one commit where they depend on each other to compile (T-009/T-010 spec #3, T-013/T-014 spec #3, T-018/T-019 spec #3).
- Options.Tools, Options.Store, and Options.Providers are passed to modules through the internal value ModuleConfig.Values.
- The permission/approval/compaction modules are fully implemented in spec #4; the spec #3 stubs were replaced.
- The SQLite store uses a single mutex-protected writer with a BEGIN IMMEDIATE transaction; there is no separate writer goroutine, but atomicity/seq/lease behavior is tested.
- The spec #1 TUI started as a line REPL; since the TUI Update it uses Bubble Tea + Bubbles + Lip Gloss with a lightweight in-house Markdown renderer (glamour and huh are not used).
- Remote CI was run after pushing the eval branch: the normal run https://github.com/ridhomujizat/lana-harness/actions/runs/40878244461 succeeded; the intentionally failing run https://github.com/ridhomujizat/lana-harness/actions/runs/40878578663 failed as expected per AC-CI-001.

### Final gate
- gofmt -l on tracked files: empty.
- golangci-lint run ./...: 0 issues.
- CGO_ENABLED=1 go test -race ./...: all packages ok.
- Release build for three platforms (after SQLite): darwin/arm64 11,288,770 bytes; linux/amd64 11,747,490 bytes; linux/arm64 11,010,210 bytes; linux statically linked.
- Smoke: LANA_HOME=.smoke-home lana init --yes -> three files created; secrets 0600.
- Smoke: LANA_HOME=.smoke-home lana run --output json \"list the files in this directory\" -> exit 0, valid JSON, stop_reason end_turn, text from MockLlm.
- Smoke: `nix develop -c go run ./examples/embed` -> exit 0, prints "Hello from embed". (`go run` outside the dev shell failed because of the sandbox's Go cache.)
- Smoke test with a real provider was skipped because OPENAI_API_KEY was not available; MockLlm was used and recorded.

### Biggest risks
1. The TUI has not been tested in a real terminal PTY because the sandbox rejects openpty; testing used an in-process Bubble Tea program. A manual smoke test (colors, resize, Shift+Enter, paste) in a real terminal is needed.
2. The SQLite store has not been tested for real cross-process crashes and writer goroutines; the resume fix is only tested through a synthetic event log.
3. Some spec #4 ACs are tested with self-written tests, and not all of them use the test names requested in tasks.md.
4. A full cross-process crash fixture and a smoke test with a real provider have not been proven; remote CI has passed.
5. Injecting Options through an internal map is fragile against future changes to the config loader.


### TUI Update (2026-10-29 22:15 WIB)

Owner request: make the terminal display like Claude Code / Hermes / omp.

Changes:
- New package `channels/cli/tui` using Bubble Tea v1.3.10, Bubbles v0.21.0, Lip Gloss v1.1.0, and x/ansi v0.10.1.
- Inline TUI without the alt screen: session banner, per-block Markdown streaming, compact/verbose tool calls, spinner, approval prompt inside the app, status bar, multi-line input, and input history.
- Keys: Enter sends, Alt+Enter/Ctrl+J/Shift+Enter inserts a new line, backslash at end of line + Enter, Ctrl+C cancels/clears/exits, Ctrl+D exits, Ctrl+O toggles verbose, Ctrl+R searches history, up/down arrows for history, Esc cancels.
- Slash commands: /help, /new, /sessions (picker ↑/↓/Enter), /fork, /compact, /clear, /session, /agent [name], /model [provider:model], /permission, /yolo, /verbose, /exit.
- Runtime: ListSessions, ForkSession (at the last turn boundary), SetAgent, SetModel, Compact; Run/Stream use the session's agent when Request.Agent is empty.
- Runtime: core.LiveFrame extended; the agent loop publishes tool_call/tool_result frames; lana.Runtime gains CreateSession, LastSession, SessionInfo, SubscribeLive, Send, Cancel, Policy, SetPolicy, History, AgentNames, AgentModel, AgentPolicy; the approval hook accepts the cli channel.
- cmd/lana chat: flags --agent, --yolo, --continue, --resume, --verbose; cli channel session; MockLlm fallback returns 200 responses plus a notice.
- lana init: provider choice openai/openrouter/deepseek (`--provider` for `--yes`), optional API key prompt with `term.ReadPassword` written to `secrets.env` 0600; `lana config check` shows the status of each `api_key_env`.
- The MockLlm fallback names the env variable that is not set, e.g. `DEEPSEEK_API_KEY is not set`.

Update gate:
- gofmt -l: empty; go vet ./...: clean; golangci-lint run ./...: 0 issues; CGO_ENABLED=1 go test -race ./...: all packages ok.
- Build `go build ./cmd/lana`: ok; release `make release`: darwin/arm64 12,596,242 bytes, linux/amd64 13,082,786 bytes, linux/arm64 12,255,394 bytes (statically linked, under the 40 MB limit).
- Smoke: `lana chat` with non-TTY stdin -> exit 2 and a hint to use `lana run`; `lana run` and `--output json` -> exit 0; `go run ./examples/embed` -> Hello from embed.
- Sandbox limit: openpty is rejected, so the TUI was tested through an in-process Bubble Tea program (io.Pipe + buffer) and renderer unit tests. The owner later ran the TUI in a real terminal with a real DeepSeek model (2026-10-29 22:44) and it worked.
- Additional local test: `lana init --yes --provider deepseek` in a temporary home produces a DeepSeek config + placeholder `secrets.env` 0600, and `resolveAgent` was tested for route/default/single agent/empty config.

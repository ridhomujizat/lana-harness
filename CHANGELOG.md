# Changelog

All notable changes to lana are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). Releases marked `beta` are used daily by the owner,
but their config keys, events, and tools may still change between betas.

## [Unreleased]

### Added
- Router onboarding: the setup wizard now matches the Router. **Connect your models** shows the Router's provider
  cards with real logos: Claude Code on top (signed in with email and plan, one-click **Sign in with Claude Code**
  with the terminal fallback, or a "Not installed" note), then OpenAI, Anthropic, DeepSeek, OpenRouter, and the
  OpenAI-compatible presets under **More providers**. A card opens inline; paste a key and it is tested on the spot.
  Connect as many as you like. **Pick a model** uses the grouped model picker for the main model, an optional
  **Fallback when it fails** (saved as the combo `default`), and, for a Claude Code main model, the background model
  for titles, memory, and knowledge. The ready page sums it all up with links to the Router and the agent.
  `POST /v1/setup` takes several providers, `model`, `fallback`, and `background_model` (the one-provider body still
  works) and always writes the router form.
- `lana init --provider claude-code` writes an agent on `claude-code/sonnet` with no provider table, and
  `--fallback <provider>/<model>` adds the combo `default`. Every preset now writes `model = "<provider>/<model>"`.
- macOS menu bar app (spec #41): `make macos-app` builds `dist/Lana.app`, a native SwiftUI menu bar
  item with the lana mark. It shows whether lana runs (version, uptime), the CPU and RAM that lana
  and its child processes (Claude Code included) use on this Mac, opens Chat, Workflows, Calendar,
  the dashboard, and the Router, starts and stops the daemon, and can open at login. The lana binary
  stays CGO-free; the app is a separate Swift package in `apps/macos/LanaMenu`.
- macOS notifications (spec #42): the menu bar app posts a notification when an agent replies (with an excerpt of
  the reply), when a tool needs approval (**Allow once** and **Reject** right on the banner), when an agent asks a
  question, and when a workflow run starts, finishes, fails, or waits for an approval. A click opens the chat or
  the run. A reply does not notify while its chat is open in a dashboard tab, and Telegram chats do not notify
  their replies. Settings has a toggle per kind and **Show message preview**. The daemon streams the events on the
  local-secret endpoint `GET /v1/local/events` and takes the banner answers on `POST /v1/local/approvals/{id}`; the
  dashboard reports the open chat on `POST /v1/sessions/{id}/presence`.
- The local sign-in link endpoint accepts `next=#/…`, and the dashboard opens that page after the
  sign-in (used by the menu bar app's ⌥-click).

- Session titles (spec #40): after the first turn lana names a session in the background with the
  agent's background model (never Claude Code; the first message when no model is free), so History
  no longer fills with "Untitled session". Rename a session from the History row (pencil or the row
  menu) or by clicking the title in the session header; an empty title goes back to an automatic
  one, and **Regenerate title** makes a new one. Workflow and calendar sessions are named
  "Workflow · <name>" and "Calendar · <item>". Titles update live in open pages.
- Claude Code is a model like any other: pick `claude-code/<model>` (aliases, every model your plan reports, or custom IDs) in an agent, combo, workflow, or the calendar. The turn runs in your own logged-in `claude` with the agent's persona, memory, and knowledge, and lana's own tools reach it over a local MCP server. If Claude Code cannot run, the turn falls back to the next model in the combo.
- Several Claude Code accounts, each with its own login folder, shown with email, plan, plan windows, and usage on the Router's Claude Code page.

- Self-update (spec #36): the dashboard shows a floating card when a newer release is out, and
  one click installs it. The daemon verifies the checksum, probes the new binary on a copy of the
  data, waits for running turns, backs up the databases, config, and binary, and restarts in place.
  A new version that fails to start is rolled back on its own. `[update] channel` (`stable` by
  default, or `beta`), Settings → Updates, and `lana update rollback`.
- `install.sh` works without GitHub auth, now that the repository is public.

- Workflows (spec #33): n8n-style automation graphs guarded by one bot. Triggers (run button,
  schedule, interval, webhook, lana events, `/command` in web chat and Telegram), AI, tool, flow
  and state nodes, `{{ }}` templates, durable runs, per-workflow state, the error ladder with
  guardian decisions, the permission lock, and the guardian assistant in the editor. Web pages:
  list, editor canvas, executions and settings.

- LLM router (spec #37): every model call (turns, compaction, memory, knowledge, embeddings) goes
  through one router. Provider kinds `openai`, `anthropic` (native Messages API adapter),
  `deepseek`, `openrouter`, and `openai_compat`; the preset kinds need no `base_url`. Combos
  (`[combos.<name>]`) are ordered fallback chains; an agent's `model` takes a combo name or a
  `provider/model` address. Fallback with backoff, a cooldown for models that hit a rate limit or a
  bad key (`[router] cooldown`), and a record of every attempt with tokens, cost at list price,
  time to first token, and duration.
- Router page in the dashboard: Overview (tokens and cost over time, by provider, model, and
  source, limits and balances), Providers, Models (with a connection test), Combos, Logs, and
  Proxy.
- OpenAI-compatible proxy at `/router/v1` (`chat/completions` with streaming, `models`) behind
  named proxy keys, so other apps can use lana's providers and combos (`[router] proxy`).
- The `router` package can be embedded in other Go services (`examples/router`).
- Claude Agent runtime (spec #38, ADR 0018): an agent with `runtime = "claude-code"` runs the
  owner's own installed and logged-in Claude Code. Tool calls go through lana's approvals and deny
  rules, cancel stops the turn, sessions resume, usage and cost are recorded, and the Router page
  shows its install, login, and plan-window state. `allow_scheduled` lets calendar items and
  workflows use it. lana never reads or stores Claude credentials.
- `lana init --provider anthropic`, and DeepSeek in the dashboard setup wizard.

### Changed
- The agent Engine choice is gone; `runtime = "claude-code"` still works and is read as `model = "claude-code/<model>"`. Background passes for Claude Code agents use the first non-Claude model of the combo or `[router] background_model`.

- Settings → Providers moved to the Router page (Providers tab).
- Agent retries moved into the router: the same backoff, now per target before falling back.
- A bad or missing API key (`auth`) now falls back to the next model in the chain instead of
  failing the turn.
- Changes to providers, combos, `[router]`, and keys in `secrets.env` apply without a restart.
- A missing API key for a provider in use now logs a warning at boot instead of stopping the
  daemon; calls to that provider fail (or fall back) until the key is set.
- `lana run --output json` reports as `model` the target that answered, which may be a fallback.
- `lana init` and the setup wizard write the preset kinds (`kind = "deepseek"` and so on) without
  `base_url`. Existing `openai_compat` providers keep working.

### Fixed
- A workflow with no edges no longer breaks its page ("t.edges is not iterable"): the API sends empty `nodes` and
  `edges` lists instead of `null`, and the dashboard treats a missing list as empty.

## [0.1.0-beta.1] - 2026-11-02

First tagged build, for daily use.

### Added

- Agent runtime: layered config, modules and hooks, an OpenAI-compatible provider with
  retry and fallback, DeepSeek thinking, a model catalog, and per-session model settings.
- Session log in SQLite with permissions, approvals, compaction, fork, and `Continue` after an
  interrupted turn.
- Tools: files, glob and grep, bash with background jobs, `web_fetch`, memory, skills, MCP
  servers (agents can manage their own), subagents and agent teams, and `ask_user`.
- Loop guard that catches repeated and failing tool calls and repairs wrong tool names.
- Agent home with persona, user, and memory files; memory capture after each turn, and
  relevant memory for each turn.
- Attachments: images for vision models, PDFs, and files from Telegram and the web.
- CLI: `lana init`, `lana run`, the `lana chat` TUI with slash commands, and `lana daemon`.
- Telegram channel with a three-step connect wizard and graceful restart.
- Web dashboard in React: chat with attachments, sessions with a Trajectory tab, agents,
  skills, MCP, channels, settings, password login, and a monitoring Home with token, tool,
  and error stats.
- `session_search`: full-text search over earlier conversations (spec 29).
- Knowledge vault in `$LANA_HOME/knowledge`, compatible with Obsidian: `knowledge_search`,
  `knowledge_read`, `knowledge_write`, and `knowledge_ingest`; background compile of sources
  into linked wiki pages; keyword search with optional embeddings; relevant pages in each
  turn's context (spec 30).
- Knowledge upkeep: a journal pass after compaction, an `inbox/` folder, Telegram `/save`,
  nightly consolidation with a lint report, and a web Knowledge page (spec 31).
- `lana --version` shows the version, commit, and build date; `make build`, `make install`,
  `make tag`, and `make release checksums`.
- `install.sh` installs a prebuilt binary from GitHub Releases with checksum verification;
  a release workflow publishes binaries for macOS (arm64, amd64) and Linux (amd64, arm64)
  when a `v*` tag is pushed.
- `README.md` with install, daemon, knowledge, upgrade, and uninstall steps.

### Upgrade notes

- The session database moves to schema version 4 on first start, and old messages are indexed
  for `session_search`. Older binaries refuse the upgraded database. Back up
  `$LANA_HOME/lana.db` first if you may need to roll back.
- Knowledge tools are offered only to agents that list them in `tools`.

[Unreleased]: https://github.com/ridhomujizat/lana-harness/compare/v0.1.0-beta.1...HEAD
[0.1.0-beta.1]: https://github.com/ridhomujizat/lana-harness/releases/tag/v0.1.0-beta.1

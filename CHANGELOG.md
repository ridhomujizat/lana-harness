# Changelog

All notable changes to lana are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). Releases marked `beta` are used daily by the owner,
but their config keys, events, and tools may still change between betas.

## [Unreleased]

## [0.4.0-beta.1] - 2026-11-11

### Added
- Group rooms (spec 47): a team is one shared conversation. A message with no @mention goes to
  every member (lead first), an @mention goes straight to that agent, and an agent that
  @mentions a teammate hands over to it. Each agent answers as itself in its own session with its
  own tools, briefed on the room, its teammates, what they know and can do, and the room memory;
  agents with nothing to add pass. Replies show like a chat turn (worked for, the steps, the
  answer), live while the agent works. `[teams.<name>] mode = "lead"` keeps the lead-first room.
- Room discussions (spec 47): the Discuss toggle asks the lead to run a moderated discussion with
  `discuss_start` / `discuss_ask` / `discuss_more` / `discuss_end`. Members answer in rounds
  (`rounds`, `extra_rounds`, never more than 5), may pass, and the owner can stop it. The end
  writes a summary (Summary button), saves room memory (`<home>/rooms/<team>/memory.json`), shares
  the summary to member rooms, and journals it for members with a domain. Team keys: `discuss`,
  `rounds`, `extra_rounds`, `room_memory`, `share_summary`, `learn`, `telegram_all`,
  `display_name`, `mode`.
- Knowledge-aware team work: the lead's roster lists what each member knows and can do,
  discussions invite the members with matching notes, and `task` passes earlier teammates'
  answers (`context`) and the agent's matching pages. Results report tools, notes used, and files.
- New group room dialog, room settings ("How the room talks", discussion settings, room memory),
  team avatars (two faces, three for three, two plus "+N" above), and agent faces in tinted
  circles in the Chats list.
- Standard toolset: every agent gets files, shell, web, memory, knowledge, and `ask_user` on top
  of its `tools`, unless `toolset = "custom"`.
- Permission `auto` (the new default): standard and team tools run without asking; dangerous
  shell commands (sudo, `rm -rf`, force push, publish, piping a download into a shell, remote
  hosts, paths outside the workspace), writes outside the workspace, and other tools (MCP
  servers, workflows) still ask.
- Knowledge graph and tabbed Knowledge page (spec 48): Graph, Pages with resolved `[[links]]` and
  backlinks, Review, and Activity; wikilinks are indexed.
- Files in chat (spec 49): attached files render as cards with a preview (Markdown, text, PDF),
  and files can be dropped into a chat room.
- `send_file` (spec 50): an agent sends a workspace file or image to the owner; the web shows
  images in a grid with a full-screen viewer and other files as cards, and Telegram delivers them
  with `sendPhoto` or `sendDocument`.

### Changed
- An agent's Chats row shows its own chat's status; while it works only in a team room the row
  reads "Working in <room>".

### Fixed
- `auto` checks subshell and background commands piece by piece instead of asking for each.
- Agent saves from the dashboard keep the `toolset` key.
- A runtime without a home folder no longer creates `knowledge/` in the working directory.
- The web stream subscribes to live frames before its first status event, so a turn that starts
  right after a page opens no longer loses its first text.

### Upgrade notes
- Agents without `permission` now run as `auto` instead of `ask`, and agents with a `tools` list
  also get the standard toolset. Set `permission = "ask"` or `toolset = "custom"` to keep the old
  behavior.
- Teams default to group mode. Set `mode = "lead"` on a team to keep the lead answering first.
- Known issue: the agent form has no Auto choice yet and shows an agent on `auto` as Ask; saving
  that form writes `permission = "ask"`.
- Every agent with the knowledge tools journals its sessions, so background model calls grow.

## [0.3.0-beta.5] - 2026-11-10

### Added
- Domain knowledge per agent (spec 46): every agent with a knowledge tool owns
  `knowledge/agents/<name>/wiki/` and `journal/` in the vault. The shared `wiki/` stays the common
  layer for people, organizations, and places. Per-turn recall searches the agent's own domain and
  the shared wiki; another agent's domain is reachable with `knowledge_search scope=agent:<name>`.
- `knowledge_search`, `knowledge_read`, and `knowledge_write` take a `scope` (`mine`, `domain`,
  `shared`, `all`, `agent:<name>`). A write to another agent's domain becomes a proposal that the
  domain agent applies at its next promotion; proposals from turns the owner did not start wait for
  the owner.
- `knowledge_write` action `supersede`: a changed fact keeps the old line struck through and dated
  next to the new one. Search and recall only rank the current text.
- Domain promotion: nightly (and right after the owner approves a proposal) each domain agent
  compiles its new journal notes and proposals into its domain or the shared wiki with its own
  model, filtered by its `JOB.md`, keeping operations with confidence ≥ 0.6 (≥ 0.8 for proposals
  the owner has not approved). Skipped operations are logged with a reason.
- Learned skills: `skill_write` stores a proposal under `agents/<name>/proposed-skills/` that the
  owner approves; `[agents.<name>] skill_write = "direct"` keeps the old behavior.
- Review queue on the Knowledge page (proposals and skill proposals, approve or reject) with a
  count badge in the sidebar; a page list with a scope filter and a page viewer; "What it knows"
  on the agent profile. API: `GET /v1/knowledge/pages`, `GET /v1/knowledge/page`, `GET /v1/review`,
  `POST /v1/review/{id}/approve|reject`, `scopes` and `review_pending` on `GET /v1/knowledge`,
  `knowledge` on the agent overview.
- The Convert dialog shows the guardian status, an elapsed-time counter, and a skeleton while it
  works.

### Changed
- `MEMORY.md` is a hot cache of 8,000 characters (was 32,000). A change that does not grow the
  file is accepted even above the limit, so no existing file is cut. Domain promotion moves facts
  out of a `MEMORY.md` above 6,000 characters into pages and removes an entry only when most of its
  words were written; removed text is kept in `log.md`.
- Journals of agents with a domain go to `agents/<name>/journal/` instead of `journal/`.

### Fixed
- Convert compiles the guardian's script, retries once with the compile error, and reports
  `feasible=false` instead of letting a broken script fail the workflow save.
- The Vite dev proxy rewrites `Origin`, so POSTs from the dev server are no longer rejected.

### Upgrade notes
- The knowledge index (`$LANA_HOME/knowledge.db`) moves to schema version 3 on first start and is
  rebuilt from the vault; vault files are not changed. It now also holds proposals.
- Agents need `knowledge_search` / `knowledge_read` / `knowledge_write` in `tools` to get a domain.
  After adding them, the next nightly run journals the agent's older sessions (40,000 characters
  per session per run) with the agent's own model.
- `skill_write` now asks the owner by default. Set `skill_write = "direct"` on an agent to keep
  writing skills at once.

## [0.3.0-beta.4] - 2026-11-09

### Added
- Agent display names: an agent has an optional `display_name` (one line, at most 40 characters)
  next to its ID, for example "Research Assistant" for `research-assistant`. The create, duplicate,
  and setup forms take a free-text name and fill the ID from it until the owner edits the ID; the
  agent profile edits the display name. Every page that shows an agent shows the display name and
  falls back to the ID. URLs, the config table, @mentions, picker values, and avatars keep the ID.
  `display_name` is on `[agents.<name>]`, `GET/PUT/POST /v1/agents`, the duplicate endpoint, setup,
  and the rooms API. A new agent's AGENT.md persona uses the display name when one is given.

### Changed
- The dashboard's agent ID rule now matches the agent home: `[a-z][a-z0-9_-]{0,31}`. An ID such as
  `2bot` is rejected when the agent is created instead of failing later.

## [0.3.0-beta.3] - 2026-11-09

### Added
- Script drift guard (spec 45): a Script Parse output that passes the contract is also compared with
  the node's last good outputs. Values that suddenly go all zero, all equal, much shorter or longer,
  change shape, copy another field, go blank, or a list that shrinks sharply count as drift
  `profile_drift`, and the run continues through the agent fallback. If the fallback agrees with the
  script, the change was real data: the baseline restarts (`profile_accept`) with no rewrite and no
  notice. Type strings may carry rules after the type (`"integer >0"`, `"string url"`, `min:N`,
  `max:N`, `>=0`); Generate and Convert add rules that hold on the sample, and a rule that real data
  breaks is dropped in a new version (`rule_drop`) with a notice. Optional `audit_every: N` runs the
  fallback on every Nth script run (`via=audit`). Test reports rule and profile problems. Each signal is a vote of the
  baseline outputs, so up to 2 wrong outputs of 5 that passed earlier cannot silence it.
- Script Parse workflow node (spec 44, ADR 0021): a step that extracts or reshapes data of a fixed
  shape (a web page, an MCP or API JSON answer) runs a small JavaScript `parse(input, ctx)` function
  in an embedded goja sandbox at 0 tokens. `output_schema` is required, with optional `item_schema`
  and `allow_empty`; empty strings and lists count as drift. An empty script is learned on the first
  run. On drift the run continues through an agent fallback, the guardian rewrites the script and
  saves it as a new version (revertable from the run or the Script tab), and the owner gets a notice.
  The inspector has a Script tab (code, Generate, Test on a run or a pasted sample, tokens saved,
  history). Agent steps with steady token use get a **Convert to Script Parse** suggestion that keeps
  the node id. New `POST /v1/workflows/{id}/script/test|generate|convert`,
  `GET /v1/workflows/{id}/script/suggestions`, `node_run.via`, `script_stats` on the workflow detail,
  decision kind `script`, and a "GitHub trending digest" template.

### Changed
- Database schema 10: `workflow_node_runs.via`.

## [0.3.0-beta.2] - 2026-11-09

### Added
- Workflow Message node channels: pick one or more of **Web chat** and **Telegram** (more channels
  can register later). Web chat posts the message into the chosen agent's chat as that agent, with a
  "via workflow" label and a desktop notification. Old nodes keep sending to Telegram.
- **Reply** on a workflow message: workflow messages stay out of the agent's context until you reply
  to one; the reply carries the quoted message, so the agent knows what you mean.
- `GET /v1/workflows/channels` lists the Message node channels and whether each is available.
- Lana favicon for the dashboard tab.

### Changed
- Workflow notifications show the run's last message instead of "N nodes ok", and a reply
  notification opens the agent's chat page.
- Generated avatars draw the body about 15% larger.
- Agents on Claude Code run isolated from your own Claude Code setup: no personal hooks, plugins,
  claude.ai connectors, auto-memory, or global CLAUDE.md. The lana tools note lists only the tools
  the agent has.
- Workflow edges are deleted from a button on the hovered edge; code blocks have an icon-only copy
  button.
- The calendar Day view stacks short entries in one column, grows busy hours, and merges nearby runs
  of one workflow into one card.

### Fixed
- The dashboard no longer hangs after several notification clicks: hidden tabs release their
  streams, so the browser's six-connection limit is not used up.
- Selects and menus inside dialogs (for example Create agent → Model) open above the dialog and can
  be scrolled and clicked.
- `{{ nodes.<id>.output }}` inside text renders the node's text instead of JSON.
- Edges with empty ids no longer all show a delete button or get deleted together.
- Switching the calendar's week start no longer shows an empty day until a refresh.
- Workflow sessions no longer show up in the agent's chat.

## [0.3.0-beta.1] - 2026-11-08

### Added
- Generated agent avatars (spec 43): every agent now has a face, a solid blob in its color with two
  white eye holes, instead of a letter. **Generate avatar** on the agent profile and General tab asks
  the chosen model (or the agent's background model, never Claude Code) for a new one, with a recent
  results strip, undo and redo, color swatches and a custom hex color, **Reset to default**, and
  **Save**. The SVG is sanitized strictly; when the model fails or draws something invalid, a local
  generator draws one. Agents without a saved avatar show a default avatar seeded from their name,
  everywhere the dashboard shows an agent. New `POST /v1/agents/{name}/avatar/generate`,
  `PUT`/`DELETE /v1/agents/{name}/avatar`, and `GET /v1/agents/{name}/avatar.svg`; agent, room, and
  overview rows gain `avatar_svg` and `avatar_source`; router call records use the source `avatar`.

### Changed
- `[agents.<name>] color` accepts a `#RRGGBB` hex color as well as the palette tokens.

## [0.2.0-beta.1] - 2026-11-08

### Added
- Refresh for Claude Code plan usage (spec 39 Revision 4): the Quota page, the Usage page, and the
  Claude Code page have a **Refresh** button with "Updated <time>", and refresh on their own when a
  Claude Code plan is over five minutes old. A refresh runs `claude -p /usage` for each enabled
  subscription account (a local command, no model call; at most once a minute per account) and shows
  the session, weekly, and per-model weekly windows (for example "Weekly · Fable"). A failed probe shows
  its error under the account and keeps the last values. New `POST /v1/router/limits/refresh`;
  `GET /v1/router/limits?refresh=1` does the same. Plan windows gain `label` and `source`
  (`stream` or `usage_probe`), and a turn's windows now merge with the stored ones instead of
  replacing them.
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
- The Workflow assistant works when the guardian runs on Claude Code. Its structured calls (graph
  edit proposals and failure decisions) run on the guardian's background model: the first API model
  of its combo, else `[router] background_model`. Without one the assistant says "The guardian
  <agent> runs on Claude Code. Set a background model in Router (or add an API model to its combo)
  so it can plan workflow changes." instead of "the guardian is not available". Workflow agent nodes
  on Claude Code agents still run in Claude Code when the account allows scheduled runs.
- The built-in Claude Code account's settings can be saved: **Allow scheduled runs** and On/Off on
  Router → Providers → Claude Code no longer fail with "claude-code is built in". They are stored
  in an optional `[providers.claude-code]` table (`kind = "claude-code"` with `allow_scheduled`,
  `disabled`, `binary`, `auth`, `api_key_env`; `config_dir` and `label` are refused, because the
  built-in account always uses your normal Claude Code login). Scheduled runs honor its
  `allow_scheduled`, and a disabled built-in account falls back to the next model.
- A workflow with no edges no longer breaks its page ("t.edges is not iterable"): the API sends empty `nodes` and
  `edges` lists instead of `null`, and the dashboard treats a missing list as empty.

### Upgrade notes

- The session database moves to schema version 9 on first start (router call records, proxy keys,
  and session titles). Older binaries refuse the upgraded database. Back up `$LANA_HOME/lana.db`
  first if you may need to roll back.
- Agents can now use `model = "<provider>/<model>"` or a combo name. The old `provider` + `model` +
  `fallback` form still works; the dashboard and `lana init` write the new form.
- `runtime = "claude-code"` is deprecated and read as `model = "claude-code/<model>"`. Saving the
  agent in the dashboard rewrites it.
- `kind = "openai_compat"` providers now need `base_url`. New kinds: `openai`, `deepseek`,
  `openrouter`, `anthropic`, and `claude-code` accounts.
- A provider in use without its API key now logs a warning at boot instead of stopping the daemon;
  calls to it fail and fall back to the next model.
- Retry and fallback moved from the agent into the router; an auth error now falls back to the next
  model in the chain.
- Settings → Providers moved to Router → Providers.
- The macOS menu bar app is built separately with `make macos-app`; it is not part of the release
  assets.

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

[Unreleased]: https://github.com/ridhomujizat/lana-harness/compare/v0.3.0-beta.3...HEAD
[0.3.0-beta.3]: https://github.com/ridhomujizat/lana-harness/compare/v0.3.0-beta.2...v0.3.0-beta.3
[0.3.0-beta.2]: https://github.com/ridhomujizat/lana-harness/compare/v0.3.0-beta.1...v0.3.0-beta.2
[0.3.0-beta.1]: https://github.com/ridhomujizat/lana-harness/compare/v0.2.0-beta.1...v0.3.0-beta.1
[0.2.0-beta.1]: https://github.com/ridhomujizat/lana-harness/compare/v0.1.0-beta.1...v0.2.0-beta.1
[0.1.0-beta.1]: https://github.com/ridhomujizat/lana-harness/releases/tag/v0.1.0-beta.1

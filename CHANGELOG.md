# Changelog

All notable changes to lana are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). Releases marked `beta` are used daily by the owner,
but their config keys, events, and tools may still change between betas.

## [Unreleased]

### Added

- Router: Command Code providers report their plan usage on the Limits page: the plan and its renewal date, the credit left of the month's pool, and the 5-hour and weekly windows, read the way the official CLI's `/usage` reads them.

## [0.6.0-beta.12] - 2026-11-26

### Added

- Antigravity: Google's Antigravity CLI (`agy`) runs agent turns like Claude Code, with several signed-in Google accounts, sign-in with a code from the Router, usage windows per model group, and lana's tools over MCP.
- Router: accounts of one provider form a failover pool. When an account hits its limit or cools down, the next account in the order answers; API keys of a provider pool the same way.
- Router: the OpenAI-compatible endpoint (`/router/v1/chat/completions`, `/router/v1/models`) serves Claude Code and Antigravity accounts as `<account>/<model>`, text only. Each call runs the official CLI headless; lana never reads their logins.
- Avatars, chat titles, and knowledge passes can run on Claude Code or Antigravity when that is the agent's model, and a workflow guardian's background turns run in its own runtime.
- Agents: the model picker lists each Claude Code and Antigravity model once; an Account choice under it picks the account when more than one offers the model, and every model has its own effort levels beside the picker.
- Workbench: the Accounts popup lists the Router's Claude Code and Antigravity accounts, grouped by provider, searchable, with usage per window, and opens a pane on the picked account. Claude Code accounts share history, settings, skills, agents, and commands with `~/.claude` (so `/resume` lists every conversation); Antigravity accounts share conversations and settings with `~/.gemini` and skip onboarding.
- Workbench: the Lana hooks install for Antigravity too.

### Changed

- Router: the Claude Code and Antigravity pages merge the account order and the account cards into one list with every usage window, named by label, then email, then account name; Quota cards use the same names.
- The claude-swap "Claude accounts" plugin is retired; the Router manages the accounts.
- Compaction and memory capture still use the background model, since they send tools the CLIs do not serve.

### Fixed

- Chat: the Learned line is clipped to one line and expands on click.
- Workbench: the panel narrows instead of running past the page edge, the Following notice no longer pushes out the close button, and the worktree picker stays inside a narrow panel.
- Router: the Command Code logo shows again.

## [0.6.0-beta.11] - 2026-11-25

### Added

- Background jobs: when a background job ends (it exits, crashes, or is stopped from the web), the agent is told with a `<task-notification>` and wakes up to report it; if it is busy, it reads the notice at its next step. A job the agent stops itself, or that dies with a daemon restart, sends no notice.
- Chat: an ended background job shows as a compact line in the transcript (finished, failed with its exit code, or stopped) with its log tail one click away.
- Chat: a Background tab docked on top of the composer lists the session's background jobs with their age and pid, Logs, Stop, and Stop all; `/tasks` opens it.
- Chat: Stop (and Esc) ends the running turn from the Chats composer; the owner's messages get Copy and their time under the bubble.

### Changed

- Chat: the composer is redrawn after the Workbench Claude chat composer: one framed card with the input field, send inside the field, and the toolbar under it; the `/` and `@` hints move into the placeholder, and the slash menu groups its commands.
- Chat: the new-session notice is a one-line chip instead of a card.
- Corners are squarer across the chat: the composer, owner bubbles, and the floating agent pill and panel.

## [0.6.0-beta.10] - 2026-11-24

### Added

- Workbench: Files and Changes now follow Claude Code into another worktree of the same repo as soon as it edits a file there, and come back when it edits the pane's own folder again. Edits outside git, in other repos, or by subagents leave the pane where it is.
- Workbench: a notice asks once before Lana adds its hooks to Claude Code's settings (Install / Not now / Don't ask again). The hooks are no longer installed without asking; the switch stays in Claude accounts.
- Chat: text the agent writes between tool calls shows as chat messages instead of being folded into the process timeline, and a quiet line under the live process head shows the thought in progress.

### Fixed

- Workbench: Claude Code never learned it ran in a Workbench pane and `lana workbench use` always said "not in a Lana Workbench pane" (Claude Code runs its hooks and shell without a terminal).
- Workbench: Claude account tabs opened with `cswap run` follow the session title (a `/rename` name or the task) instead of a fixed "claude · N".
- Workbench chat: long permission options and terminal text stay inside their cards instead of scrolling the thread sideways.

## [0.6.0-beta.9] - 2026-11-23

### Added

- Floating agent (spec 72), replacing the chat dock: one agent at the right edge of every page but Chats, tucked as half a pill until you open it. The pill shows the current agent and the others that need you; it opens a panel with the chat list (search, All / Working / Needs you) and the room, with `‹` back to the list and a strip when another agent needs you. A new reply or a waiting agent grows the pill into a card (Open / Reply); `–` shrinks it, `›` tucks the pill. ⌥Space opens and closes the panel; Esc steps back from the room to the list, then closes.
- Floating agent on a phone: a bubble you can drag anywhere that glides to the nearest edge on release, like the iOS AssistiveTouch button; drop it on the × to hide it ("Show agent bubble" in the menu brings it back). Several active agents stack into one bubble that fans out on a tap. The chat opens in a sheet that drags to full height and stays above the keyboard.
- Workbench chat on a phone: the composer shows the model and the context use.
- Workbench chat: thinking shows as a step in the tool group.

### Changed

- Workbench chat on a phone: shorter, dimmer placeholder aligned with the send button; the field no longer zooms the page on focus.
- Image zoom controls hide while idle when the image is zoomed in.

### Fixed

- Workbench chat: a failed tool no longer stays "running" (Claude Code's string tool results were dropped).
- Workbench chat: tool steps stay in one group across hidden thinking instead of one "1 step" row per command.

### Removed

- The chat dock (windows, tabs and chat heads), replaced by the floating agent.

## [0.6.0-beta.8] - 2026-11-23

### Changed

- Workbench on a phone: the space header is a clean one-line title with one menu; the view switch, Files, Changes (with its count), New terminal and Claude accounts moved into it.
- Workbench chat on a phone: Return adds a new line (the send button sends), the message field stops growing at about five lines and scrolls, and the `/` and `@` lists fit the room above the field so they stay in view with the keyboard up.
- Workbench chat: finished "Thinking" rows are hidden; one shows only at the tail of a running turn. An answered question lists each answer under its question.

### Fixed

- Workbench chat: images sent as paths (phone uploads land in the drops folder) show as thumbnails in your message instead of path text.
- Workbench chat: Claude Code's side panel on a wide terminal (the changed-files column) no longer leaks into question options and descriptions.

## [0.6.0-beta.7] - 2026-11-23

### Added

- Workbench chat: a multi-question dialog moves between questions from its chips (the one on screen is highlighted, as in the terminal) and a Prev button. The review step lists each question with its answer before Submit answers / Cancel.
- Workbench chat: file paths in Claude's replies, in your messages and in Read/Edit/Write steps open in Files on click, like ⌘+click in the terminal.
- Workbench chat: a copy button under each of your messages and each of Claude's replies.
- Workbench chat: pasted text in a message shows as a "Pasted text" block instead of raw `<pasted_content>` tags.

### Changed

- Workbench chat: no violet by default; the question card, buttons, send button and context donut follow the input's colours (violet stays for subagents). Finished steps start folded.
- Workbench: the Terminal ⇄ Chat button sits with the pane tools on the right of the pane header instead of mid-header.
- Workbench on a phone: the active tab chip looks like the active desktop tab instead of a violet pill.

### Fixed

- Workbench chat: switching tab or space no longer carries one pane's chat (pending message, draft, attachments) into another pane.
- Workbench chat: after a pane resize (opening Files, resizing the window) the chat reads the terminal again instead of showing "can't be read from the screen".
- Workbench chat: a question left with Esc no longer comes back as an open card.
- Workbench chat: the message field shows one border when focused, not a second ring inside it.
- Workbench: paths with spaces (`…/00 Inbox/Task VA.md`, quoted paths) open from the terminal and the chat.
- Workbench: the "Tell Claude Code it runs in the Workbench" switch in Accounts wraps its text instead of scrolling sideways.

## [0.6.0-beta.6] - 2026-11-23

### Added

- Workbench: a Claude Code pane can show its conversation as a chat (spec 71). One icon button in the pane header (or ⇧⌘J) switches between Terminal and Chat; it is the same `claude` process, nothing restarts. The chat reads Claude Code's transcript: messages, tool calls grouped per turn with diffs and output, subagents with their own steps and report, background shells and agents, and a live status line read from the screen. Permission prompts, questions (AskUserQuestion) and plan approvals are answered from cards that send the terminal's keys. The composer types into the same terminal and has `/` and `@` menus, the permission mode, model and effort, a context donut and file attachments. On a phone, Claude panes open in Chat.
- Workbench: with the panel hidden, the space name in the tab bar is a dropdown. It lists every space with its tab count, ticks the current one, switches on click and has New space.

## [0.6.0-beta.5] - 2026-11-22

### Added

- Workbench: a ⌘+clicked folder outside any git repo, such as an agent's plan folder under `/tmp`, opens in Files as a plain tree instead of a "folder outside any git repo" notice. Its files open on click, `.figcraft` ones in the FigCraft viewer. The tree names the folder's path and has a close button.

### Fixed

- Workbench: a ⌘+clicked file outside every repo, such as an agent's scratchpad file in `/private/tmp`, shows in Files again when the pane is in a folder of several repos or outside git. The panel used to open with the folder's tree and no file.
- Workbench: a `.figcraft` file in a folder of several repos but outside any repo, such as `docs/mockups`, opens in the FigCraft viewer instead of as plain text, also from a ⌘+click.

## [0.6.0-beta.4] - 2026-11-22

### Added

- The FigCraft viewer has an Expand button: it opens the viewer over the whole window, like the image viewer, with pan, zoom, pages and layers. Esc or the same button goes back.

### Fixed

- FigCraft viewer text is sharp when zoomed in, also for titles and notes next to frames, and no longer shows twice. Only frames, sections and components get a name label on the canvas; a text layer's label repeated its own text over the render.

- macOS stops asking again for Documents, Music and other privacy access after every update. `make app CODESIGN_IDENTITY="<keychain identity>"` signs Lana.app and its daemon binary (identifier `<bundle id>.daemon`) with a real certificate instead of ad-hoc, so macOS keeps the grants for that identity. Self-update signs the new binary with the identity it finds on the installed bundle and falls back to ad-hoc when that identity cannot sign. The default stays ad-hoc; a one-time build with an identity is needed.
- Workbench on a phone: the tab sheet keeps two equal card columns, so a long tab title is cut short instead of pushing the second card off the sheet; the card close button is a small circle on the preview strip; the tab count in the strip is one flat box; and the tab chips share one height.

## [0.6.0-beta.3] - 2026-11-22

### Added

- Agents report the worktree they work in (spec 69). A Claude Code session that works in another git worktree runs `lana workbench use <path>`, and its pane chip, the Files and Changes panel and the badge follow that worktree, with a "Following …" notice. `lana workbench use .` (or `--clear`) goes back to the pane's own folder, and `lana workbench use --new <branch>` creates a worktree in `../<repo>-worktree/<name>` and reports it. A reported chip has a ring mark; the panel's worktree picker can clear a report. A report ends when the agent exits.
- Lana tells Claude Code about this itself: the daemon installs a `SessionStart` hook into `~/.claude/settings.json` and into the folder of each Claude Code account (other settings are kept; the first change writes `settings.json.lana-bak`). The hook adds the instruction only inside a Lana Workbench pane, also for a hand-typed `claude`; Ghostty, herdr, VS Code and a tmux or herdr started inside a pane do not get it. A switch in the Workbench Accounts menu turns it off. Only the daemon on the default `~/.lana` home touches Claude settings. Claude Code sessions that were already running need `/clear` or a restart.
- Folders of several repos (spec 70). A pane in a folder that is not a repo but holds repos, such as a project root with a backend and a frontend, gets a root chip (`sxored · 4`: four repos changed). The panel then shows Changes grouped by repo (clean repos fold into one row; up to 3 changed repos open by default), Files over the whole folder including folders outside git such as `docs/` (opened as plain files, without a diff), and a picker with "All repos" and each repo to narrow to the usual single-repo view. A repo an agent reported moves to the top with an "agent here" mark, and the chip reads `root › repo`. Repo names drop the folder's name as a prefix.
- The root view stays light: one shared watcher per folder scans repos with changes every 3 s and the rest every 10 s while an agent works (every 120 s when idle), right away after a commit or checkout, and not at all when no panel shows the folder. All Workbench git runs share a limit of 4 at a time, and pages get a push event instead of polling.
- Read-only FigCraft viewer in Workbench Files (spec 67): a `.figcraft` file opens with its canvas (pan, zoom, pinch), a page dropdown, Pages and Layers (a sheet on a phone), selection and sharp detail renders; it refreshes when the file changes, and a live mode follows the open FigCraft app.
- Lana as a phone app over Tailscale (spec 68): the remote page has a web app manifest and iOS meta tags, so the phone can add Lana to its home screen and open it full screen. Settings › Remote access shows how. There is no service worker, so an upgrade is never hidden behind a cache.
- Images in Workbench Files and the chat image viewer zoom and pan: pinch or ⌘/Ctrl + scroll zooms at the pointer, drag pans, double-click switches fit and 100%, and a toolbar has zoom, fit, 1:1 and full screen (+ − 0 1 keys).

### Changed

- ⌘+click on a folder path in a Workbench terminal opens Files in that folder's repo, with the tree expanded down to it; it used to answer "no such file".

## [0.6.0-beta.2] - 2026-11-22

### Added

- Mermaid diagrams in the Workbench Markdown preview: ```` ```mermaid ```` blocks render as diagrams. Each has − / % / + zoom (% fits the width), ⌘/Ctrl + scroll and pinch zoom around the pointer (25–400%), and scrolls once it is larger than its box; its text can be selected and copied. A diagram that fails to parse shows the error and its source. Mermaid 11 is served from Lana's own assets and loads only when a page has a diagram.
- Bottom sheets with a grip (Workbench All tabs, Claude accounts, the phone Files/Changes/Worktrees sheet, the Knowledge page panel on a phone) can be dragged: up for the full height, down to bring it back or close it. A tap on the grip toggles the full height.

### Changed

- The Workbench file preview (Markdown, text and image pages) follows the app's light or dark theme; it was always dark.
- Moving a Workbench pane by drag no longer selects header text (Lana.app), keeps the drag when the pointer passes over a terminal or preview, and the drop area says what a release does (Swap, Move left, right, above, below).

### Fixed

- The phone Workbench All tabs sheet: tab cards were squeezed to one clipped line; they now show the program strip, the title and the status. The active tab has the accent border. "1 tab" instead of "1 tabs".
- In a narrow Lana.app window, the phone Workbench header leaves room for the window buttons.

## [0.6.0-beta.1] - 2026-11-22

### Added

- Workbench Files, Changes and Worktrees panel (spec 66). The Changes button at the right end of the tab strip, with the changed file count, opens a panel beside the terminals; ⇧⌘E opens Files, ⇧⌘G Changes. The panel follows the focused pane's git worktree. Files: a lazy tree with git badges, a ⌘P filter, and a read-only viewer with line numbers, syntax colouring and a git gutter. Changes: the changed files of Uncommitted, Branch vs `development` (the base is set per repo) or Last commit, with a unified or split diff and hunk navigation. ⌘+click on a path in a terminal opens it in Files at its line. Large files and diffs scroll smoothly (only visible lines are drawn), and status refreshes every 3 s while the panel is open.
- Worktree management in the panel's Worktrees tab: lists the repo's worktrees (in use, active, merged into the base, prunable) and offers New worktree (new or existing branch from `origin/development`, optional fetch, copies git-ignored local files such as `.claude/implementer.local`, opens a tab with Claude Code), Clean up merged worktrees, Remove (with what would be lost and a typed confirmation for unsaved work), Move, Prune and Lock. New worktrees go in `../<repo>-worktree/<name>`. Remote branches are never deleted.
- Each pane header shows its worktree's branch; two agents working in one worktree get a "shared with …" warning. Hovering a tab whose panes use several worktrees lists them.
- Focus mode: with the panel open, only the focused pane is shown, with ‹ n/N › to step through the others; the panel header button turns it off. Opening the panel hides the Spaces sidebar and closing it brings the sidebar back.
- Pane sizes are draggable: drag the gap between two panes, double-click it for equal sizes. The sizes are saved in the shared layout.
- Panes can be moved: hover the program icon in a pane header (it becomes a move icon) and drag it onto another pane, to its left, right, top or bottom half, or to its middle to swap the two. Esc cancels. Not available while zoomed, in focus mode, or for a tab's only pane.
- The new-tab button is split in two: "+" opens a terminal (⌘T), "⌄" opens Claude Code here, Claude Code in a worktree, or New worktree (⇧⌘N).
- The phone Workbench has the Changes button in its header and the panel as a bottom sheet; A−/A+ moved into the ⋯ space menu.

### Changed

- Lana.app: the Workbench tab strip spans the full window width, also over the open panel, and keeps Accounts and Attached in full.
- The collapsed sidebar rail shows its items as evenly spaced tiles, with Settings and the avatar centred at the bottom; the expanded sidebar aligns the avatar with the icons and the name with the labels.

### Performance

- API and asset responses are gzipped when the client accepts it (SSE, WebSocket, range and error responses are unchanged); chat logs and the JS bundle shrink about 3x, which helps over Tailscale.
- A chat loads its events once and only renders what changed; the live stream starts from the last loaded event instead of replaying the log.
- The chat timeline asks for a trimmed view of session events (no system prompt text, thinking signatures or step usage; tool input and output past 4000 characters are cut, with a "Show full" button). The session page keeps full payloads.
- An open session stream reads only new events on each poll instead of the whole log.

### Fixed

- A data race when a Workbench terminal closes (the emulator's answer pipe is closed instead of the emulator).
- Lint findings (errcheck, staticcheck) that failed CI.

## [0.5.0-beta.17] - 2026-11-21

### Changed

- Lana.app window chrome: the sidebar and the top bar share one colour with no dividing lines, and each page sits in a card with rounded corners. The collapsed sidebar stays a 52px icon rail. In the Workbench the tab strip moves into the title row next to the window buttons, and Spaces and the terminals sit in the card below it; a divider separates the sidebar button from the "show panel" button when both are shown.
- The sidebar slides between full width and the icon rail, and the Workbench Spaces panel grows and shrinks when shown or hidden. Terminals keep their size during the slide and fit once it ends. Reduced motion turns the animation off.

### Fixed

- The count badge in the sidebar (for example the Workbench attention count) centres its number in the circle; it sat about 2px low.
- Tooltips in the Workbench tab strip no longer show as a light line under a hovered button.

## [0.5.0-beta.16] - 2026-11-21

### Added

- Hardline floor: a short built-in list of destructive commands is checked in every permission mode, `trust` included, and agents or prompts cannot turn it off. Block-tier calls (`rm -rf ~` or `/`, disk wipes, writes to `~/.ssh`, `~/.lana/secrets.env`, `daemon.secret`, or `config.toml`, also inside `bash -c`, `$(...)`, and heredoc code) run only after a stronger confirmation: your Lana password on the web card (or a Telegram code when Telegram is connected), the 4-digit code in Telegram, or the typed code in the TUI. The macOS banner has no Allow button for them. Escalate-tier calls (`sudo`, `git push --force`, `curl … | sh`, reading secret files, `crontab`, `launchctl`) ask for a normal approval even in `trust` mode and never offer "allow for session". Without an approval channel (scheduled or workflow runs) both are refused. `lana-eval hardline-replay -db <lana.db>` replays the rules over recorded tool calls.
- Project context files: agents read `AGENTS.md` or `CLAUDE.md` from the workspace and its parent folders (up to, not including, your home folder) into the system prompt. When a tool first touches another repository, that repository's file is added once to the tool result, with a line saying Lana added it. Files show again after a compaction. Per-agent setting `context_files` (default `["AGENTS.md", "CLAUDE.md"]`, `[]` turns it off). Files are capped at 16 KiB each, 32 KiB per injection.
- Deferred tool loading: an agent with more than 15 MCP tools sends only their names, grouped by server, and gets a `tool_search` tool that returns a tool's description and parameters and loads it. A deferred tool can also be called directly by name, and invalid input returns its schema. Loaded tools are appended at the end of the tool list and kept across turns and restarts until the next compaction, so the prompt cache stays valid. With the `sxored-hub`, `kerjan`, and `dbx` servers the first request of a turn drops from about 37k to 11k input tokens. Settings: `defer_tools = true|false` per agent, `defer = false` per `[mcp.<name>]` to always send a server's tools. Claude Code turns are unchanged.

## [0.5.0-beta.15] - 2026-11-21

### Changed

- The Workbench panel is back to its first design, without the bordered card. Spaces and Agents sit on the same gradient as the terminal workspace, with no panel colour or right border of their own.

## [0.5.0-beta.14] - 2026-11-21

### Added

- Chat dock: chats with agents stay on screen on every page except Chats, like the old Facebook chat bar. Leaving the Chats page while an agent works, waits on you, or replied in the last 30 minutes keeps that chat at the bottom right as a window with the full timeline and composer, so questions and approvals can be answered from any page. A chat you used in the last two hours comes back as a minimized tab, with a short preview card, when it starts waiting on you or a new reply arrives. Up to 2 windows (1 on smaller screens, 3 on very wide ones); the rest are tabs, with "+N" when they do not fit. On a phone the chats are floating heads: a tap opens or collapses the chat, and the open head has the close button. The dock is hidden on the Chats page.

### Changed

- The Workbench panel is restyled as one bordered card.
- Markdown tables size each column to its content and scroll sideways when they are wider than the chat, instead of squeezing the columns.

### Fixed

- Short messages on a phone no longer wrap letter by letter in their bubble.
- A long file path that wraps over terminal lines opens as one link in the Workbench.

## [0.5.0-beta.13] - 2026-11-21

### Added

- Workbench file preview (spec 61): ⌘+click (Ctrl+click, or a tap on a phone) a file path printed in a terminal to open it in a panel beside the panes. HTML pages, Markdown, images, PDF and code with line numbers are shown; `path:line` jumps to the line. The daemon reads the file, so the preview also works over remote access. Only files under the home and temp folders are served, never `.ssh`, `.gnupg`, `.aws`, `secrets.env` or `.env` files, and pages run in a sandboxed frame.

### Changed

- Workbench tab and space switches are instant again: terminals stay alive while their tab is hidden, so a Claude Code pane no longer replays its whole history on every switch.
- Workbench panes: the focused pane is clearly brighter, terminals show the card colour, a new terminal takes the focus, closing the focused pane returns the focus to the pane used last, and panes fade in and out.
- Workbench tabs have a fixed width with the title on the left, and programs and agents show as small landscape tiles (Claude, Codex, Cursor, Gemini, Grok, pi, omp and the rest). The side panel marks the focused agent.
- Agent avatars are now chibi pixel-art characters instead of blobs (spec 62). Each agent
  gets a character picked from its name (hair, skin, eyes, accessory, outfit, or a robot),
  wearing the agent color. Small avatars show the head and shoulders in a tinted tile; the
  profile shows the whole character. A working agent nods and types.
- The avatar dialog has Randomize, a picker for every part, and "Generate with AI", which
  draws a new character on the same template from your description. When the model fails
  twice, the dialog shows an error and keeps the character on screen.
- Saved blob avatars are kept as `avatar.blob.svg` and no longer shown; agents use their
  pixel default until a new avatar is saved (`avatar.json`).

## [0.5.0-beta.12] - 2026-11-19

### Fixed
- Workbench: switching tabs or spaces no longer scrolls Claude Code's whole history past.
  The pane stays hidden while the session is replayed and shows at the bottom once it is
  drawn. During the replay only width changes are applied, so the screen no longer jumps.

## [0.5.0-beta.11] - 2026-11-19

### Fixed
- Workbench: tab completion and syntax highlighting no longer draw text several columns to
  the right of the prompt. Terminals now get `LANG=C.UTF-8` when no locale is set (the core
  runs under launchd and the shell is not a login shell), so zsh measures prompt glyphs
  like `▸ ⚙︎ ≡` correctly.
- Workbench: lines no longer vanish after switching between the phone and the Mac, or after
  a reload. The core marks each size change in the replayed output and the pane replays every
  part at the size it was drawn for.
- Workbench: clicking a link in a terminal opens it. In Lana.app it failed with "The
  application can't be opened. -50", and links from Claude Code first asked "Do you want to
  navigate…". Only http and https links open.
- Phone: the page no longer zooms in when a field is focused, the topbar stays below the
  browser's URL bar, and the chat input stays above the keyboard and Chrome's bottom toolbar.

## [0.5.0-beta.10] - 2026-11-18

### Added
- Workbench sessions survive a restart of the Mac (spec 60). The core keeps its sessions in
  `~/.lana/workbench/sessions.json` and brings them back under the same ids, so every pane
  of the layout comes back. Shells reopen in their last directory. A session that ran
  Claude Code or omp comes back as a shell with `claude --resume <id>` / `omp --resume <id>`
  typed in (`--continue` when the conversation id is unknown). Other commands come back
  exited, with Restart. The daemon starts the core at boot when there is something to
  restore, and restored panes show a "Restored" chip. Closing a pane drops its session for good.

### Changed
- Workbench: the active space and agent rows no longer have a bar on the left; their colour
  marks them instead.

## [0.5.0-beta.9] - 2026-11-17

### Added
- Workbench: drag files onto a terminal, as in Ghostty or Terminal. The path is typed,
  escaped with backslashes, so Claude Code can attach a dropped screenshot. In Lana.app the
  real path is used (Finder, the screenshot thumbnail); in a browser the file is uploaded to
  `~/.lana/workbench/drops/` (new `POST /v1/workbench/drops`, 10 files of 20 MB) and that path
  is typed. Dropped text is typed as is.
- Workbench shortcuts, as in GoRex: ⌘T new tab, ⇧⌘T new space, ⌘D / ⇧⌘D split right / down,
  ⌘W / ⇧⌘W close pane / tab, ⇧⌘↩ zoom, ⌥⌘ arrows move focus, ⌘1…⌘9, ⇧⌘[ ⇧⌘] and ⌃Tab
  switch tabs, ⇧⌘R rename tab, ⌘K clear, ⌘+ ⌘− ⌘0 text size. ⌘W no longer closes the
  window while Workbench is open.
- Workbench "done" status (blue), as in herdr: an agent that finishes while its pane is not
  in view stays done until you focus that pane.

### Fixed
- Workbench: a terminal running Claude Code no longer shows a broken screen (only the
  changing digits, missing letters) after switching tabs or spaces. The redraw nudge on attach
  now waits long enough for Node programs to see the size change.
- Workbench: glyphs are measured again once the terminal font has loaded and redrawn when the
  window or a hidden pane comes back, so characters no longer go missing.

## [0.5.0-beta.8] - 2026-11-17

### Added
- **Remote access over Tailscale** (spec 57): open Lana, including Workbench, from your phone
  at `https://<mac>.<tailnet>.ts.net`. The daemon keeps listening on 127.0.0.1 only;
  Tailscale Serve proxies to it over HTTPS inside your tailnet (never Funnel).
- Settings > Remote access: a setup checklist (Tailscale installed, running, HTTPS
  certificates, serve), an Enable switch that runs `tailscale serve` itself, the address with
  a QR code, the allowed Tailscale accounts, the remote terminal policy, remote session
  limits, and every signed-in device with its own Sign out.
- Remote requests must come from the configured ts.net host and an allowed Tailscale login
  (`Tailscale-User-Login`), then the Lana password. Remote sessions get a Secure cookie, are
  bound to that login, and end after 1 hour idle or 7 days (configurable).
- Workbench from a remote browser is view-only by default until you unlock control with the
  password or a code sent to Telegram (15 minutes by default); it then locks again. The
  terminal policy can also be View only or Full control.
- A remote browser cannot change remote access, the password, or sign out other browsers.
- Remote sign-ins show a macOS banner and a Telegram message, and every remote sign-in,
  failed password, control unlock, setting change and sign-out is in the Audit log.
- New `[web.remote]` config table: `enabled`, `host`, `require_identity`, `allowed_logins`,
  `terminal`, `confirm_with`, `control_minutes`, `idle_minutes`, `max_age_hours`,
  `notify_sign_in`.

## [0.5.0-beta.7] - 2026-11-17

### Fixed
- Workbench terminals: Shift+Enter adds a new line in Claude Code and other agents instead
  of submitting. Cmd+Left/Right (line start/end), Cmd+Backspace (delete line),
  Option+Left/Right (word jump) and Option+Backspace (delete word) work as in iTerm2;
  Cmd+Left no longer goes back in browser history.

## [0.5.0-beta.6] - 2026-11-17

### Added
- **Workbench** (spec 55): terminals for coding agents that stay alive. A separate core
  process (`lana workbench serve`, started on first use) owns every terminal, so sessions
  survive daemon restarts and upgrades. The Workbench page has a Spaces and Agents panel in
  the herdr layout, tabs and split panes rendered with xterm.js, and a phone view with a tab
  strip, an all-tabs sheet, a key bar above the keyboard and touch scrolling.
- Agent detection follows herdr (Apache-2.0): Claude Code, Codex, Cursor, Gemini CLI,
  OpenCode, Copilot and 18 more are recognized, with working / blocked / idle read from the
  screen. Agents show their real logos and conversation titles.
- Spaces are named after the folder of their active terminal; `+` opens a new space in the
  home folder, and new terminals open in the folder of the last one. Spaces and tabs can be
  renamed and closed.
- A chime in the browser when an agent finishes or needs you, and `agent_finished` /
  `agent_needs_you` notifications for the menu bar app.
- **Plugins** (spec 56): the Tools page is now Tools & plugins. The Claude accounts plugin
  turns on once the new **Claude Swap** tool (`cswap`) is installed and shows every Claude
  Code account with its 5-hour, weekly and per-model usage: make default, open on an account
  in a new tab or beside the focused pane, add, re-login and remove.

### Changed
- The menu bar app plays notification sounds itself at full volume (Glass) instead of the
  macOS alert volume, which was often too quiet.
- The dashboard's Content-Security-Policy allows `ws:` to the daemon and inline styles
  (xterm.js).

## [0.5.0-beta.5] - 2026-11-14

### Changed
- Knowledge journals are written in the background right after the work, not only at the
  nightly run: two minutes after the last turn ends, the next upkeep tick journals one
  session, at most once per 30 minutes per session. When a new turn starts, a running
  journal pass is cancelled and queued again, so it never slows a reply.
- The nightly consolidation still runs at `consolidate_at`. When the laptop was off at that
  time, the catch-up now waits until no turn has run for two minutes.
- Knowledge calls (journal, compile, promotion, lint) turn the model's thinking off and get
  larger output budgets. The commandcode provider now sends `reasoning_effort: "off"`.
- The consolidation log counts only journals that were written, and lists failed and
  preempted sessions separately. `GET /v1/knowledge` reports `journal_failed`.

### Fixed
- Journal passes on `commandcode/deepseek-v4.1-flash` failed with `journal reply is not a JSON
  object` or `unexpected end of JSON input`: the model spent the 2048-token budget on
  reasoning. The same applied to source compiles at 8192 tokens.
- Journal and compile replies with text around the JSON value are accepted.
- Domain promotion retries an op once in the other scope when the page lives there (it
  failed with `page not found` for pages in the shared wiki).
- Page titles with characters such as `/` are cleaned (`vercel-labs/ui` becomes
  `vercel-labs-ui`) instead of being dropped.
- Promotion creates are no longer skipped as `invalid: empty page`: the prompt shows a full
  create example, and `content` or `markdown` count as the page body.

## [0.5.0-beta.4] - 2026-11-13

### Added
- Agent avatars move while the agent works. In an agent chat room the working row shows the
  agent's avatar with the typing dots and "Bima is thinking (3s)…": the avatar sways while
  thinking, wobbles inside a spinning ring while a tool runs ("running bash"), hops while
  writing, and nods with an amber halo while waiting for you. When the turn ends it hops once
  (or shakes on an error).
- The Chats list avatar of an agent shows the same motion while its room works or waits on
  you. Quiet rooms stay still, and reduced motion keeps the moods readable without movement.

### Changed
- An agent chat room's turns have no head row any more, since the room header already shows
  the agent. The working row turns into the turn's foot in the same place, so the page does
  not jump: a Copy button for the answer, then "worked for 9s - 03:39 AM".

## [0.5.0-beta.3] - 2026-11-13

### Added
- Agent setting `max_tokens` (config `[agents.<name>] max_tokens`, agent page "Max output
  tokens"): the output budget of every model step of that agent. Empty or 0 keeps the provider
  default.
- Model facts fall back to the provider's own model list (cached by the router, warmed at
  startup) when the built-in catalog does not know a model. The catalog also knows
  `deepseek-v4.1-flash`, `-fast`, and `-ultrafast`.

### Changed
- The `commandcode` provider sends `max_tokens` 64000 (the Command Code CLI default), or the
  model's catalog output limit, instead of 1024.

### Fixed
- A turn no longer ends silently with an empty reply when the model hits its output-token
  limit. A step cut off before any answer gets one extra step; a turn whose last step was cut
  off ends with reason `max_tokens` and shows "Reply cut off by the model's output limit." in
  the web UI, Telegram (with Continue), and the terminal.
- Agents on models outside the built-in catalog, such as `commandcode/deepseek/deepseek-v4.1-flash`,
  no longer get the 128K default context window; the context meter and compaction use the
  model's real window (1M).
- The Chats list re-sorts on live updates, so a new or newly active chat moves to its place
  instead of staying at the bottom.

## [0.5.0-beta.2] - 2026-11-12

### Added
- Router provider kind `commandcode` (Command Code, commandcode.ai), ported from 9router-go. It
  speaks the command-code CLI wire (`POST /alpha/generate`, NDJSON events) with text, reasoning,
  tool calls, images, and usage with cache reads; the model list comes from
  `/provider/v1/models`. The key is `COMMANDCODE_API_KEY` (or `api_key_env`), pasted on the new
  Command Code card of Router → Providers. Address models as `commandcode/<model>`, e.g.
  `commandcode/deepseek/deepseek-v4-flash`.

### Fixed
- In a workflow's own guardian chat, asking to change the workflow no longer fails with
  "guardian unavailable: session is busy". The `workflow` tool's `edit` action started a second turn
  on that same chat; it now refuses there and points the guardian at `workflow_builder` (save, test,
  publish), and the guardian guide says so too.

## [0.5.0-beta.1] - 2026-11-12

### Added
- The workflow guardian chat is a real agent session (spec 51). The editor's assistant column now works like the main chat:
  - It shows owner messages with file chips, turns with tool steps, the final markdown reply, `ask_user` and approval cards, Stop, file upload (button, paste, drop), `/new` and the context meter.
  - Each workflow has one guardian session on channel `workflow`. A hidden context note goes before each owner message: the full guide the first time, then only what changed.
  - New routes: `GET /v1/workflows/{id}/assistant` (the session id plus change cards), `POST …/assistant` (202, the turn streams on the session), and `POST …/assistant/session` and `…/assistant/new`.
- The guardian tests before it reports (spec 51). The new `workflow_builder` tool is offered in the guardian chat with these actions:
  - `get`
  - `save`: stores the graph as a **candidate**, so the current version stays untouched.
  - `test`: a run with trigger `test` on the candidate; the tool waits up to 2 minutes.
  - `publish`: saves the new version and adds a change card with Undo under that turn.
  - `discard`

  Candidate test runs:
  - do not count toward the 3-failure Error rule;
  - skip guardian diagnosis and owner failure notices;
  - patch the candidate when they learn a Script Parse script.

  The guardian asks with `ask_user` before a test that would reach other people or outside systems, and when a request is ambiguous.
- Lana.app opens the dashboard in its own window (spec #52): **Open lana** in the menu bar (⌘O),
  a start of Lana.app, or a notification click shows it, with a Dock icon and a main menu while it
  is open. The window signs in with a one-time app link from the daemon
  (`POST /v1/local/app-link`, `GET /v1/auth/app`), so no password is typed or changed. Settings can
  send pages back to the browser.
- `make app` bundles the lana daemon binary as `Lana.app/Contents/MacOS/lana`, and
  `apps/macos/install-app.sh` (`make install-app`) installs the app, points `~/.local/bin/lana` and
  the `dev.lana.daemon` launchd agent at it, and ties the agent to Lana.app with
  `AssociatedBundleIdentifiers`, so Login Items shows lana with its icon instead of an executable.
- Agents add secrets through a secure input card (spec 53). The new standard tool
  `secret_request` (name, why it is needed, optional link) opens a card in the web chat, the room
  and the workflow guardian chat with a masked field, a show/hide toggle, Save and Cancel. Save
  writes Settings > Secrets directly; the agent gets only `saved`, `cancelled` or `expired`, and
  the value never reaches the model, the session log, the stream, the logs or the audit file. A
  name that already has a value shows a clear replace warning. Telegram sessions get an error that
  sends the owner to the web. New events `secret/requested` and `secret/resolved`, SSE event
  `secret_request`, and `POST /v1/secret-requests/{request_id}`.

### Changed
- Agents run on `trust` by default: an agent that sets no permission runs every tool without asking (it was `auto`). The agent templates and the `lana init` seed also use `trust`.
- The standard toolset now holds every built-in tool that works without context. Newly added: `send_file`, `web_search`, `session_search`, `knowledge_ingest`, `skill_write`, `usage_stats` and `secret_request`. Tools that need context are still offered only where they apply: `skill_load`, `mcp_manage`, `task`, `discuss_*` and `workflow_builder`.
- The workflow assistant answers in a chat turn on the guardian's own model chain, Claude Code included. It no longer makes a structured call on the background model. Failure diagnosis still uses the background chain (spec 39 revision 5).
- In the Lana.app window, the topbar uses the sidebar colour, and only one expand toggle shows with the sidebar collapsed.
- Self-update signs Lana.app again (ad-hoc) after it replaces or restores the bundled binary.

- The database moves to schema version 11 on first start. It adds workflow candidates, `candidate_id` on runs, `assistant_session` on workflows, and session/turn ids on workflow chat messages. Back up `$LANA_HOME/lana.db` first if you may need to roll back to 0.4.

### Fixed
- A Claude Code agent's `ask_user`, and any other lana tool call longer than 60 s, no longer fails with "The operation timed out". lana sets `MCP_TOOL_TIMEOUT` to 31 minutes for Claude Code.
- Hovering an online agent's avatar no longer squeezes its name tooltip into the presence dot.
- The workflow composer has one focus ring, and long Proposed fix values wrap in a scrollable code block instead of overflowing the run panel.
- A question from a workflow session is recorded as answered on the web.

## [0.4.0-beta.2] - 2026-11-11

### Added
- Agents build workflows from chat: the `workflow` tool gains `create` (a draft built by the
  guardian from a plain-language request, returned with its link), `edit` (asks the guardian for a
  change), and `test` (runs it now and waits up to two minutes for the result with each step's
  output). Under `auto`, create, edit, and test run without asking; `start` and `cancel` still ask.
- `calendar` and `workflow` join the standard toolset. Agents offer a calendar reminder or habit
  for time-based or repeating reminders, a workflow for repeating work with steps, and nothing for
  a one-off, and ask before building an automation the owner did not ask for.
- The workflow assistant knows what exists: its prompt lists the agents, the tools of every
  configured MCP server, the names of the owner's secrets, and whether bash nodes see them, plus
  the newest run with each node's status and output or error. It prefers a `tool` node for MCP
  calls, asks only for unknown facts, never promises later work, and can start a test run
  (`"test": true`) whose result it reads on the next message.
- Param docs for the `tool`, `bash`, `webfetch`, `agent`, `bot`, and `worker` nodes in the
  assistant's node catalog.

### Fixed
- The first message of a new workflow and the guardian's reply show without a refresh after the
  page moves from `#/workflows/new` to the workflow.
- Agent cards keep a long model address, status, or error inside the card; a problem's error shows
  under the name; team cards use display names and the compact team avatar; "1 task" is singular.

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

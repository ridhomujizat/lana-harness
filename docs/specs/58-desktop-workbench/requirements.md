# Spec 58: Desktop Workbench — native Workbench app on MyGo

- **Status**: Draft (2026-11-17)
- **Branch**: `feature/desktop-workbench`, from `development` (0.5.0-beta.7, 7d592c2), worktree `../lana-harness-desktop`
- **Number**: 58, because spec 57 is left for the remote access (Tailscale) work running in parallel on `feature/remote-access`.
- **References**: GoRex (`~/coding/project/self-tool/gorex`, a Rex-style terminal on MyGo), MyGo (https://mygo.run/, v0.9.2), spec 55 (Workbench core and web API).

## Problem

The Workbench (spec 55) renders terminals with xterm.js inside the browser and inside the WKWebView of Lana.app. The owner wants a desktop app whose Workbench is native: drawn by MyGo on the GPU with libghostty-vt, with no webview, and with GoRex's keyboard control of panes and splits. The web UI must stay, because Lana is also used from a browser and from the phone.

## Decisions (owner, 2026-11-17)

1. **Hybrid, not a rewrite.** Only the Workbench is native. The web UI (`web/ui`) stays the primary UI for every other page and for browser access. New features land in the web UI first.
2. **Separate app for now.** The native app does not replace Lana.app (the menu bar app of spec 52). It may replace it later, when the agent pages also move to native UI.
3. **Cross-platform direction.** macOS first, but the app code must stay portable (no darwin-only code outside build-tagged files), so Linux and Windows builds can follow.
4. **Remote later.** The app must be able to connect to a remote Lana daemon later, as herdr does. This spec builds the local connection and keeps the connection code ready for a remote one.
5. **Shortcuts follow GoRex**, in particular its terminal and split controls.

## Architecture

```
lana workbench serve        (spec 55 core: owns every pty)
   │ unix socket
lana daemon :18300          (/v1/workbench/* REST + SSE + WebSocket attach)
   │ HTTP, cookie session
   ├── browser / Lana.app   (web/ui, xterm.js)
   └── Lana Workbench.app   (this spec: MyGo native UI, libghostty-vt)
```

The native app is a client of the daemon's HTTP API, the same API that the web UI uses. It never talks to the core socket directly. This keeps one access path, one audit log (REQ-WB-043), and a path to remote daemons.

## Requirements

### Module and build

- **REQ-DW-001** The app lives in `apps/desktop/` as its own Go module (`github.com/ridhomujizat/lana-harness/apps/desktop`). MyGo and its dependencies must not enter the root `go.mod`, so the daemon binary does not change.
- **REQ-DW-002** It uses MyGo v0.9.2 (`github.com/egoist/mygo`) with its `ui` package and its `plugins/terminal` (libghostty-vt). The `mygo` CLI is pinned as a Go tool in the module's `go.mod` (`tool github.com/egoist/mygo/cmd/mygo`), as in GoRex. No cgo.
- **REQ-DW-003** MyGo v0.9.2 needs Go 1.27.3. The flake's one dev shell moves from `go` (1.26.10) to `go_1_27` (1.27.3) after the root module builds, vets and tests clean on it (owner, 2026-11-17: "update aja jika aman di nix"). The root `go.mod` keeps `go 1.26.0`.
- **REQ-DW-004** `apps/desktop/mygo.json`: name "Lana Workbench", identifier `dev.lana.workbench`, macOS minimum 13.0. The dev build (`go tool mygo dev`) is "Lana Workbench Dev" and keeps its data apart, as GoRex does.
- **REQ-DW-005** Makefile targets `desktop-dev`, `desktop-build` (an `.app` and a `.dmg` under `apps/desktop/build/`) and `desktop-test`. The app icon is the Lana icon with a terminal mark, drawn by a small Go tool as in GoRex (`tools/mkicon`).

### Connection to the daemon

- **REQ-DW-010** A connection is a base URL plus an authenticated HTTP client (cookie jar). The connection type is an interface with one implementation now, `local`, so a `remote` one (URL plus sign-in) can be added later without changing the views.
- **REQ-DW-011** Local sign-in: read `$LANA_HOME/daemon.secret` (`{"addr", "secret"}`, the file Lana.app reads; `LANA_HOME` defaults to `~/.lana`), call `POST /v1/local/app-link` with the `X-Lana-Local-Secret` header, then follow the returned link (`/v1/auth/app?code=…`) with the cookie jar, so the jar holds the session cookie. Then `GET /v1/auth/session` gives the CSRF value, which goes in the `X-Lana-CSRF` header of every unsafe request. Every request carries `Origin: http://<addr>`, as a browser on the dashboard would.
- **REQ-DW-012** The Lana home is chosen by, in order: the `LANA_HOME` environment variable, the app setting "Lana home", `~/.lana`. The dev build defaults to `~/.lana-dev`, so it never touches prod (see the dev environment notes).
- **REQ-DW-013** When the daemon is down, the secret file is missing, or a call returns 401, the window shows a "Not connected" state with the reason and a Retry button, and retries on its own with backoff (1 s up to 30 s). A 503 `workbench_unavailable` shows the core's reason.
- **REQ-DW-014** API calls used: `GET/POST /v1/workbench/sessions`, `DELETE /v1/workbench/sessions/{id}`, `POST …/{id}/restart`, `POST …/{id}/resize`, `GET/PUT /v1/workbench/layout`, `GET /v1/workbench/events` (SSE), `GET …/{id}/attach?mode=control` (WebSocket). The WebSocket request sends `Origin: http://<addr>`, as REQ-WB-041 requires.

### Layout (shared with the web UI)

- **REQ-DW-020** The app reads and writes the same layout document as the web UI (`WbLayout` version 1 in `web/ui/src/views/workbench-model.ts`: spaces, tabs, pane trees, `activeSpace`, `activeTab`, `focus`, `zoom`, `grouping`). A Go port of the model and its pure helpers (`parseLayout`, split, close, focus, zoom, sessions of a tab) lives in `apps/desktop/internal/layout`, with tests that use the same cases as `workbench-model.test.ts`.
- **REQ-DW-021** The app keeps fields it does not know when it rewrites the layout (it decodes into the typed model plus the raw JSON of each object and merges back), so a newer web UI never loses data to the native app.
- **REQ-DW-022** Splits get sizes. A split node may carry `sizes`, an array of fractions with one entry per child, summing to 1. A missing or invalid `sizes` means equal sizes. The web UI also honours and keeps `sizes` (small change in `workbench-model.ts` and `workbench.tsx`), so a divider dragged in one client shows in the other.
- **REQ-DW-023** A `layout.changed` event from another client replaces the local layout, except for the pane that has the keyboard focus here, which stays focused if it still exists. Local changes are written with `PUT /v1/workbench/layout`, debounced by 300 ms, and divider drags are written once, at the end of the drag.

### Window and views

- **REQ-DW-030** One window. Left: the panel with **Spaces** (each with its colour tile, name and `+` for a new terminal in that space) and **Agents** (sessions whose program is an agent or that carry `lana.agent`, grouped by space or by status as the layout's `grouping` says), as in the web UI (REQ-WB-051, REQ-WB-052). Then the tab strip of the active space. Then the active tab's panes.
- **REQ-DW-031** Each pane is a card, as in GoRex: a header with the program icon, the title, the working directory (when the core knows it), a status dot (green working, orange needs you, grey idle, red exited with its code), and buttons for split right, split down, zoom and close. The body is a `terminal.View`.
- **REQ-DW-032** The gaps between panes are dividers. Dragging one resizes its split (REQ-DW-022); double-clicking it resets that split to equal sizes. The cursor changes over a divider.
- **REQ-DW-033** An exited session's pane shows "exited N" and a Restart button over its last screen (REQ-WB-014, REQ-WB-015).
- **REQ-DW-034** Tabs reorder by dragging, rename by double-click (`title` in the layout), and have a context menu: Rename, Close tab, and Move to space.
- **REQ-DW-035** Light and dark appearances follow the system, with an override in the View menu and the palette. Colours come from the Gr8r tokens the web UI uses (`web/ui/src/styles/gr8r*`), mapped to a MyGo `ui.Theme`. The terminal theme is the same dark theme the web Workbench uses.
- **REQ-DW-036** The terminal font is Geist Mono, embedded (OFL), as in the web Workbench. Text size changes with the shortcuts (REQ-DW-050) and is kept in the app settings.
- **REQ-DW-037** The Dock badge shows the number of sessions with `needs_you`. The app posts no notifications by default, because Lana.app already does (spec 55 agent banners). A setting turns them on.

### Terminals

- **REQ-DW-040** Each visible pane attaches with a WebSocket in control mode (REQ-WB-054). Output frames feed the pane's `terminal` emulator; key input and paste go out as binary frames; size changes go out as `{"type":"resize","cols","rows"}` text frames. Size ownership follows the web UI (`workbench-term.tsx`, tmux `window-size latest`): a pane claims the pty size when it opens in the focused window, when the window gets focus, and when the user types in it; it then fits its box and sends its size. A pane that does not own the size shows the session at the session's own `cols`×`rows` (from the events) and sends nothing; another client's resize shortly after a claim (1.5 s grace) hands ownership away.
- **REQ-DW-041** A pane that is hidden (another tab, another space, or another pane zoomed) closes its WebSocket after 30 s and attaches again when it is shown, replaying the core's ring buffer. A session with output while hidden keeps its status from the SSE events.
- **REQ-DW-042** Keys: everything that is not an app shortcut goes to the terminal, using the terminal plugin's key encoding. On macOS, Option sends Alt (Meta) and Shift+Enter sends the same sequence the web UI sends since `44624fe`, so Claude Code gets a newline. The macOS text editing keys of `44624fe` (⌘←/⌘→ to line start and end, ⌥←/⌥→ by word, ⌘⌫ to delete the line) work the same way.
- **REQ-DW-043** Copy and paste: ⌘C copies the selection (and does nothing without one), ⌘V pastes with bracketed paste when the program asked for it. Clicking a URL with ⌘ held opens it in the default browser.
- **REQ-DW-044** Closing a pane (button or ⌘W) or a tab kills its sessions at once, as the web UI does, with no confirmation, whatever runs in them (owner, 2026-11-17). Quitting the app never ends a session.

### Shortcuts (from GoRex)

- **REQ-DW-050** These are app menu items, listed in the command palette:

| macOS | Windows, Linux | Command |
|---|---|---|
| ⌘T | Ctrl+Shift+T | New tab (in the current space, in the focused pane's directory) |
| ⌘D | Ctrl+Shift+D | Split right |
| ⇧⌘D | Ctrl+Shift+E | Split down |
| ⌘W | Ctrl+Shift+W | Close pane |
| ⇧⌘W | | Close tab |
| ⇧⌘↩ | Ctrl+Shift+Enter | Zoom pane (toggle) |
| ⌃⌘= | | Equalize panes |
| ⌥⌘← → ↑ ↓ | Alt+arrows | Focus the pane in that direction |
| ⌃⌘← → ↑ ↓ | Alt+Shift+arrows | Move the nearest divider |
| ⌘1…⌘9 | Ctrl+Shift+1…9 | Go to tab N |
| ⇧⌘] / ⇧⌘[ , ⌃Tab / ⌃⇧Tab | Ctrl+PageDown / Ctrl+PageUp, Ctrl+Tab | Next / previous tab |
| ⇧⌘R | Ctrl+Shift+R | Rename tab |
| ⇧⌘P, ⌘P | Ctrl+Shift+P | Command palette |
| ⌘K | Ctrl+Shift+K | Clear screen and scrollback (local view) |
| ⌘+ ⌘− ⌘0 | Ctrl+= Ctrl+- Ctrl+0 | Text size |
| ⌥⌘1…⌥⌘9 | | Go to space N |
| ⌥⌘T | | New space |

- **REQ-DW-051** Off macOS, Control and a letter belong to the terminal, so app shortcuts take Shift too, and none use Control with Alt (AltGr), as in GoRex.
- **REQ-DW-052** The command palette fuzzy-matches every command, every pane (by title, program and directory) and every space, and jumps to the one picked.

### Settings and state

- **REQ-DW-060** App settings (`settings.json` in the app's data directory): Lana home, appearance, text size, notifications on or off. Window size and position are restored.
- **REQ-DW-061** Spaces, tabs, splits and focus live only in the shared layout document (REQ-DW-020), never in local files, so the browser and the app always agree.

### File preview (owner, 2026-11-21)

- **REQ-DW-080** ⌘+click on a file path printed in a terminal opens it in a preview panel inside the window. A path is what the terminal plugin's `OpenLink` reports: absolute, `~/`, `./`, `../`, one with a slash, or a file name with an extension, with an optional `:line` or `:line:col` suffix. A relative path is resolved against the session's working directory. A path that does not exist, or is a directory, opens nothing (a short notice says so). URLs and OSC 8 hyperlinks that are not `file://` keep opening in the system browser.
- **REQ-DW-081** The panel sits at the right of the workspace, beside the panes, about 45% of its width, with a draggable divider. One file shows at a time; a new ⌘+click replaces it. Esc (while the panel has the focus) or its close button closes it. The panel is local to this window: it is not part of the shared layout document (REQ-DW-061).
- **REQ-DW-082** The panel's header shows the file name, its folder (`~`-shortened), and buttons: Open in default app, Reveal in Finder, Close.
- **REQ-DW-083** The body is a MyGo web view. HTML files render as pages, with their relative assets (CSS, scripts, images) loading. Markdown renders as formatted HTML in the app's dark style. Images (png, jpg, gif, webp, svg) and PDF show as such. Any other text file shows as code in Geist Mono with line numbers, scrolled to and highlighting the `:line` given. Binary files that are none of these show their name, size and the Open in default app button. Files over 5 MB that are not images or PDF show the same.
- **REQ-DW-084** Files are served to the web view by a read-only loopback HTTP server inside the app (127.0.0.1, random port, a random token in every URL), limited to the previewed file's folder. Nothing outside it is served. The server stops when the app quits.
- **REQ-DW-085** The file is read by the app itself, so this works only with a local daemon. A remote daemon (later) will read files through the daemon instead.

### Tests

- **REQ-DW-070** `go test ./...` in `apps/desktop` covers: the layout model (REQ-DW-020…022) including round-tripping unknown fields; the local sign-in and the API client against an `httptest` server (app link, cookie, CSRF, 401 and 503 handling); the SSE parser; the WebSocket attach framing; the shortcut table (no duplicate accelerators per platform); and headless view tests of the window (MyGo views run without a window) for splitting, closing, zooming and focus moves.
- **REQ-DW-071** Web side: `workbench-model.test.ts` gets cases for `sizes` (kept on parse, kept or rebalanced by split and close). `vitest` and the web build pass.

## Out of scope

Replacing Lana.app; native agent, chat or other pages; the remote connection UI and sign-in (the connection interface is ready for it); Linux and Windows packaging (the code stays portable); code signing, notarization and auto-update of the new app; a headless VT snapshot in the core.

## Open questions

None blocking. The app name "Lana Workbench" and the identifier `dev.lana.workbench` can still change before the first build.

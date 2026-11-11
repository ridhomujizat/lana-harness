# Requirements: Desktop app (Lana.app window and bundled daemon)

- **Issue**: — (owner request 2026-11-12)
- **Status**: Draft (agent recommendations; the owner approves the install switch)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: in this file ([Contract](#contract)) — one local control endpoint, one dashboard
  path, the bundle layout, the launchd agent, and the app flags and settings
- **Related**: spec [41](../41-macos-menubar/requirements.md) (menu bar app), spec
  [42](../42-macos-notifications/requirements.md) (notifications), spec
  [36](../36-self-update/requirements.md) (self-update), ADR [0013](../../adr/README.md) (the lana
  binary stays CGO-free)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "parallel app built-in web app bisa ga ya? icon di macOS-nya juga muncul logo lana semua bukan bash. mungkin tool ini bisa membantu https://github.com/tw93/pake" | goal: the dashboard as its own app window, the lana icon on every macOS surface |
| Evidence | System Settings shows "lana" with the black `exec` icon | `sfltool dumpbtm`: the Login Items (background) entry "lana" is the legacy agent `dev.lana.daemon` with executable `~/.local/bin/lana`, a bare binary with no bundle; LaunchServices also still lists two deleted `dist/Lana.app` copies with the same bundle id |

## Goal

lana opens as its own Mac app: a window with the dashboard, a Dock and app-switcher icon while the
window is open, the menu bar item, and notifications, all with the lana icon. The daemon binary
lives inside the same `Lana.app`, so the background item in Login Items is lana with its icon, not
an anonymous executable.

## Decision: a WKWebView window inside Lana.app, not Pake

| Option | Verdict |
|---|---|
| (a) Pake (tw93/pake, a Tauri wrapper) | **Rejected.** It builds a second app with its own bundle id, icon, and notification identity next to the menu bar app, so macOS would show two lana apps. It needs a Rust and Node toolchain (Tauri CLI, cargo) that the repo's Nix shell does not have, for a page that WKWebView already renders. Tauri on macOS is WKWebView too, so it adds no rendering capability. Its window still needs a way to sign in, which Pake has no hook for. |
| (b) A native WKWebView window in the existing Swift menu bar app | **Chosen.** One app, one bundle id (`dev.lana.menubar`), one icon. It reuses the menu bar code, the `daemon.secret` local control channel, and `make macos-app`. It needs only the Xcode toolchain the menu bar app already needs. The window signs in with a one-time app link from the daemon, so no password is typed or changed. |
| (c) A Go webview binding in the lana binary | Rejected: needs CGO and WebKit in the Go build (ADR 0013). |
| (d) Safari "Add to Dock" web app | Rejected: manual per Mac, a separate app identity, no menu bar or notification integration, and it cannot get the session cookie without a password. |

## Behavior rules

### REQ-DSK-001: The lana window

Lana.app shall show the dashboard in its own window (a WKWebView). **Open lana** (menu bar, ⌘O)
shows the window with the page it showed last (default `#/chats`). The window title is the page
title. The frame, the zoom, and the last page are kept across launches.

### REQ-DSK-002: App sign-in

When the window opens a page, the app asks the daemon for a one-time app link (API-DSK-001) with the
`daemon.secret` value and loads it. The link sets the dashboard session cookie in the window's cookie
jar and redirects to the page (API-DSK-002). A window that already has a valid session keeps it and
starts no new one. No password is set or changed; other browsers stay signed in. A daemon without
the endpoint (404) gets the plain page, which shows the password sign-in. **View → Reload** (⌘R)
loads the current page through a fresh app link, so it also signs in again after a logout.

- `AC-DSK-001`: an app link starts a session, redirects to `next`, and works once; no password is set.
- `AC-DSK-002`: a caller with a valid session keeps it; no second session is created.
- `AC-DSK-003`: a code older than 2 minutes, a reused code, or a sign-in link code starts no session
  and redirects to `/`.

### REQ-DSK-003: Dock icon while the window is open

The app stays a menu bar app (`LSUIElement`). While a lana window is open it is a regular app: Dock
icon, app switcher, and a main menu (lana, File, Edit, View, Go, Window) with copy and paste. Closing
the last lana window removes the Dock icon again. The menu bar item and notifications stay.

### REQ-DSK-004: Pages and notifications open in the window

Chat, Workflows, Calendar, Open Dashboard, Router, and a notification click open in the lana window.
Settings → **Open pages and notifications in the lana window** (on by default) switches them back to
the browser. ⌥-click on a page still opens a one-time browser sign-in link (spec #41).

### REQ-DSK-005: Opening Lana.app

Starting Lana.app from Finder, Spotlight, the Dock, or `open` opens the window. Opening it again
while it runs shows the window. A start at login (`SMAppService.mainApp`) never opens the window.
Settings → **Open the lana window when Lana.app starts** (on by default) turns the first one off.

### REQ-DSK-006: Links, files, and dialogs

- Links on the dashboard origin (`http://<addr>`) stay in the window. Every other link (https,
  mailto, another port) opens in the default browser or app.
- A `target=_blank` link on the dashboard origin (a file preview) opens a second lana window with
  the same cookie jar.
- Downloads (`<a download>`, a type the web view cannot show) go to `~/Downloads` with a unique name.
- File inputs open the macOS open panel; `alert`, `confirm`, and `prompt` show as sheets.

### REQ-DSK-007: lana not running

While the daemon is not running the window shows "lana is not running" with a **Start lana** button
(the menu bar's Start lana), checks every 2 seconds, and loads the dashboard when it is up.

### REQ-DSK-008: The daemon binary inside Lana.app

`make app` (alias of `make macos-app`) shall put the lana binary at `Lana.app/Contents/MacOS/lana`:
built from `./cmd/lana` for this Mac, or `LANA_BIN=<path>` to bundle an existing binary (for example
the release the owner runs). The menu bar app's **Start lana** prefers it: settings path, then the
bundled binary, then the login shell PATH and the spec #41 fallbacks.

### REQ-DSK-009: launchd agent and Login Items

The launchd agent `dev.lana.daemon` shall run `Lana.app/Contents/MacOS/lana daemon` and carry
`AssociatedBundleIdentifiers = [dev.lana.menubar]`, so System Settings → General → Login Items &
Extensions attributes the background item to Lana.app (name and icon).

### REQ-DSK-010: Self-update inside the bundle

Self-update (spec #36) resolves `~/.local/bin/lana` to the bundled binary and replaces it there.
After it replaces or restores a binary inside an app bundle, it signs the bundle again ad-hoc
(`codesign --force --sign - <Lana.app>`) so `codesign --verify` keeps passing. A failure is a warning;
the new binary runs either way. codesign replaces files through a rename, so the running menu bar app
is not affected.

### REQ-DSK-011: Install and migration

`apps/macos/install-app.sh` (`make install-app`) installs `dist/Lana.app` and moves an existing
install onto it. Without `--yes` it only prints the steps. It never stops or restarts the daemon.

1. Quit the running Lana.app, zip the old bundle to
   `~/Library/Application Support/lana-app/Lana.app.previous.zip`, copy the new one to
   `/Applications/Lana.app`, verify the signature.
2. LaunchServices: unregister `Lana.app` copies that no longer exist, register the new one.
3. Make `~/.local/bin/lana` a symlink to the bundled binary; keep a real file as `lana.pre-app`.
4. In `~/Library/LaunchAgents/dev.lana.daemon.plist` set `ProgramArguments[0]` to the bundled binary
   and add `AssociatedBundleIdentifiers` (backup in the backup folder). A missing plist is written
   from `apps/macos/dev.lana.daemon.plist.in` but not loaded.
5. Open Lana.app.

Old installs keep working: the `lana` command still resolves (symlink), the running daemon keeps its
process, and its next restart in place execs `~/.local/bin/lana`, which is now the bundled binary.

### REQ-DSK-012: Dev variant "Lana Dev"

Owner follow-up (2026-11-12): "untuk development-nya Lana.app dibedakan dengan logo warna kuning,
sedangkan prod seperti biasa."

`make app-dev` (same as `make macos-app VARIANT=dev`) shall build `dist/Lana Dev.app`:

| | Production (`VARIANT=prod`, default) | Development (`VARIANT=dev`) |
|---|---|---|
| Bundle | `dist/Lana.app` | `dist/Lana Dev.app` |
| Bundle id (notification and Login Items identity) | `dev.lana.menubar` | `dev.lana.menubar.dev` |
| `CFBundleName` / `CFBundleDisplayName` | lana | Lana Dev |
| Info.plist `LanaVariant` / `LanaHome` | `prod` / empty | `dev` / `~/.lana-dev` |
| App icon | dark tile, white mark | yellow tile (#F5C400), dark mark ([lana-dev-icon.png](lana-dev-icon.png)) |
| Menu bar icon | monochrome template mark | the mark in yellow (#F5C400 on a dark menu bar, #C99A00 on a light one) |
| Menu header, window title | lana | lana dev, "<page> · dev" |
| Installed by | `make install-app` → `/Applications` | `make install-app-dev` → `~/Applications` |

The home order is: the settings override, then Info.plist `LanaHome`, then `LANA_HOME` from the
environment, then `~/.lana`. `LanaHome` comes before the environment so the dev app never follows a
`LANA_HOME` meant for production. Every file the app reads (config.toml, daemon.lock,
daemon.secret, the menubar daemon log), the event stream, the app link, and **Start lana**
(`LANA_HOME` passed to the daemon) follow the resolved home. UserDefaults and the WebKit data store
are per bundle id, so the two apps share no settings or cookies.

`install-app.sh` reads the variant from the bundle: the dev app installs under its own name, its CLI
link would be `lana-dev` and its launchd agent `dev.lana.daemon.dev` with
`EnvironmentVariables.LANA_HOME = ~/.lana-dev`. `make install-app-dev` skips both (`--no-cli-link
--no-launchd --no-open`), so it never touches the production app, CLI, or agent.

### REQ-DSK-013: No title bar in the app window

Owner follow-up (2026-11-12): "bisa ga tampilannya dirapihin" — like Monocode Native: no separate
title bar, the traffic lights inside the dark sidebar's top row, the sidebar toggle right after
them, one seamless window. Only in the app window; a browser looks exactly as before.

- The window is `.fullSizeContentView` with a transparent, title-less title bar (the title stays
  for the Window menu and Mission Control). The close, minimize, and zoom buttons sit centred in a
  46 pt top row (the dashboard topbar height), 20 pt from the left; the app moves them back whenever AppKit lays them out again.
  The window background is the dashboard sidebar colour (#151514 dark, #F4F3F0 light), and the
  window appearance follows the dashboard theme (`html[data-theme]`), so no seam shows.
- A user script at document start sets `html[data-shell="macos-app"]` (plus
  `data-shell-variant="dev"` in Lana Dev) and `--shell-lights-w` (the lights' width plus 10 pt,
  measured from the real buttons). The app toggles `data-shell-fullscreen` in full screen, where
  the lights hide.
- With the attribute the dashboard adds a sidebar top row (`.side-chrome`) that leaves the lights'
  width free and holds the collapse toggle; the LANA logo and host line stay below it. The topbar is
  as tall as in a browser (46 px). With the collapsed rail the topbar leaves the rest of the lights' width free and
  carries the expand toggle; on a narrow window (drawer) the topbar leaves the full width free.
- The sidebar top row and the topbar carry `data-drag-region`. A primary mouse-down there that is
  not on a control (button, link, input, textarea, select, label, summary, `role=button|link|tab|
  menuitem`, contenteditable) posts `drag` to `messageHandlers.lanaShell`; the app calls
  `NSWindow.performDrag(with:)` with the latest mouse-down. A double click posts `dblclick`, which
  zooms, minimizes, or does nothing per "Double-click a window's title bar" (AppleActionOnDoubleClick).
- Without the attribute (any browser) none of this renders or applies.

## Contract

### API-DSK-001: `POST /v1/local/app-link`

Daemon-only, same checks as API-WLG-008: `X-Lana-Local-Secret` must equal `daemon.secret` (401
`unauthorized`), the caller must be loopback (403 `forbidden_remote`), only POST (405). Query `next`
(optional, `SafeNextHash`). 200 `{"link": "http://<addr>/v1/auth/app?code=<code>[&next=<hash>]"}`.
The code is 32 random bytes (base64url), stored as a SHA-256 hash, valid once for 2 minutes, in a
store separate from the sign-in link codes.

### API-DSK-002: `GET /v1/auth/app?code=…&next=…`

Host check like every dashboard route. `Cache-Control: no-store`, `Referrer-Policy: no-referrer`.
The code is consumed on every call. With a valid `lana_session` cookie: 303 to `/<next>` without a
new session. Else with a valid code: a new browser session (`Set-Cookie: lana_session`, HttpOnly,
SameSite=Strict, user agent recorded as for a password sign-in) and 303 to `/<next>`. Else: 303 to
`/`. `next` defaults to `#/chats`.

### Bundle layout

| Path | Content |
|---|---|
| `Contents/MacOS/LanaMenu` | the menu bar app and window (bundle executable) |
| `Contents/MacOS/lana` | the lana daemon and CLI |
| `Contents/Resources/AppIcon.icns` | the lana icon (16–1024 px) |
| `Contents/Info.plist` | `CFBundleIdentifier` (`BUNDLE_ID`, default `dev.lana.menubar`), `LSUIElement`, `NSAppTransportSecurity.NSAllowsLocalNetworking` |

### App flags (extends API-MAC-003)

| Flag | Effect |
|---|---|
| `--window-snapshot PNG [--page HASH] [--seconds N]` | open the window on the running daemon, wait for the dashboard, write a PNG of it, and print JSON: `url`, `hash`, `title`, `signedIn`, `text`, `menus`, `activationPolicy` |

### Settings (UserDefaults of the bundle id)

| Key | Default | Meaning |
|---|---|---|
| `openPagesInWindow` | true | pages and notification clicks open in the window |
| `openWindowAtLaunch` | true | a manual start opens the window |
| `windowLastHash`, `windowZoom` | — | last page and zoom |

## Install manually

```sh
nix develop -c make app             # dist/Lana.app with the daemon built from this checkout
# or keep the daemon version you run now:
nix develop -c make app LANA_BIN="$(readlink -f ~/.local/bin/lana)"
apps/macos/install-app.sh           # dry run: prints every step
apps/macos/install-app.sh --yes     # install and migrate (make install-app does the same)
```

The daemon keeps running the old process. Switch it when no turn is running: Dashboard → Settings →
Restart (waits for running turns and execs the bundled binary), then reload the launchd job so it
reads the new plist:

```sh
launchctl bootout gui/$(id -u)/dev.lana.daemon
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.lana.daemon.plist
```

Check: `ps -o command= -p "$(cat ~/.lana/daemon.lock)"` shows
`/Applications/Lana.app/Contents/MacOS/lana daemon`; System Settings → General → Login Items &
Extensions lists lana with its icon; `codesign --verify --strict /Applications/Lana.app` passes.

A test build takes another bundle id so it never shares the installed app's notification and Login
Items identity: `make app BUNDLE_ID=dev.lana.menubar.test`.

## Not included

- A Lana.app release asset. Release CI runs on Linux; building the Swift app needs a macOS runner.
  Self-update replaces only the bundled daemon binary, not the menu bar app.
- Notarization or a Developer ID signature (ad-hoc only).
- An asset-catalog icon (`Assets.car`, macOS 26 icon styles); the `.icns` stays.
- Loading the launchd agent through `SMAppService.agent` (the agent stays a user plist).

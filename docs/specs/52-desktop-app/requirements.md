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

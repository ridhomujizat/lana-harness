# Spec 68: Installable phone app over Tailscale

- **Status**: Approved (owner, 2026-11-22: "langsung kerjakan aja di worktree yang berbeda")
- **Branch**: `feature/phone-pwa`, from `development` (0.6.0-beta.2, 5637bd5)
- **Builds on**: [Spec 57: Remote access over Tailscale](../57-remote-access/requirements.md)

## Problem

Spec 57 lets the owner open Lana on their phone at `https://<mac>.<tailnet>.ts.net`. On the phone this is a Safari or Chrome tab: the browser bars take space, and Lana is not on the home screen. "Add to Home Screen" today only makes a bookmark that opens a normal tab.

## Decisions

1. Lana is an installable web app (PWA) only on the remote (Tailscale) address. The local page on the Mac and Lana.app do not offer an install.
2. There is no service worker. Every page load comes from the daemon, so a daemon upgrade is never hidden behind a stale cache, and auth stays as in spec 57.
3. The installed app opens full screen (`display: standalone`) with the status bar in the app's theme colour.

## Requirements

- **REQ-PWA-001**: The built dashboard contains `manifest.json` (served at `/assets/manifest.json`) with `id` and `start_url` `/`, `scope` `/`, `display` `standalone`, name "Lana", and PNG icons at 192 and 512 px for the `any` and `maskable` purposes. The icons are full-bleed squares (the OS masks the corners) with the Lana mark inside the maskable safe zone.
- **REQ-PWA-002**: `GET /` for a remote request (spec 57 classification) returns the index with `<link rel="manifest" href="/assets/manifest.json">` in the head. A local request gets the index without it.
- **REQ-PWA-003**: `/assets/manifest.json` and the icons are served without a session, so the browser can fetch them before sign-in.
- **REQ-PWA-004**: The index carries the iOS web app meta tags (`apple-mobile-web-app-capable`, `apple-mobile-web-app-title` "Lana", `apple-mobile-web-app-status-bar-style` `default`), `mobile-web-app-capable`, a full-bleed 180 px `apple-touch-icon`, and one `theme-color` meta per OS colour scheme (light `#ffffff`, dark `#1d1d1c`, the topbar `--surface`).
- **REQ-PWA-005**: A theme chosen in the app (light or dark) overrides both `theme-color` metas, so the status bar follows the app theme.
- **REQ-PWA-006**: The viewport uses `viewport-fit=cover`. `#app` keeps clear of the left and right safe areas (landscape notch). Bottom bars and composers pad themselves by `--safe-bottom` (`env(safe-area-inset-bottom)`), so they sit above the home indicator.
- **REQ-PWA-007**: While the on-screen keyboard is open (the visual viewport is more than 150 px shorter than the window), `html.kb-open` sets `--safe-bottom` to 0, because the app already ends above the keyboard.
- **REQ-PWA-008**: Settings › Remote access tells the owner how to install the address as a phone app (iPhone: Safari, Share, Add to Home Screen; Android: Chrome, Install app).

## Notes

- iOS keeps a separate cookie store for an installed web app, so the owner signs in once more inside the app. The session then follows the spec 57 remote TTL.
- The Tailscale app must be connected on the phone, or the installed app shows a network error.
- Web Push to the installed app (iOS 16.4+) is a possible later spec. It is not part of this one.

## Tests

- Go (`web/pwa_test.go`): the manifest link helper; local `GET /` has no manifest link; remote `GET /` has it; remote `GET /assets/manifest.json` without a session is 200 JSON with `display: standalone`.
- Web (`src/app/theme.test.ts`): `applyTheme` paints every `theme-color` meta with the resolved theme colour.
- Manual: install from the phone over real Tailscale Serve; check standalone launch, status bar colour, the chat composer and Workbench key bar above the home indicator, and the keyboard.

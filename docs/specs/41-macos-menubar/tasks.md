# Tasks: macOS menu bar app

- **Status**: Completed 2026-11-08 · branch `wip/macos-menubar`
- **Requirements**: [requirements.md](requirements.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-MAC-01 | `web`: `SignInLinkTo`, `SafeNextHash`, `next` on `POST /v1/local/sign-in-link` | API-MAC-001 | `go test ./web` ok (`TestLocalSignInLinkNext`, `TestSafeNextHash`) |
| T-MAC-02 | UI: `safeNextHash` in `app/router.ts`; `App.tsx` follows `next` after the claim and for a signed-in browser; `make web` | API-MAC-002 | vitest 403/403, `tsc --noEmit`, `make web-check` |
| T-MAC-03 | `LanaMenuCore`: home and TOML `[web]` reader, `daemon.lock`/`daemon.secret`, libproc sampler, usage meter, binary resolution, start/stop, sign-in request, lana mark paths and icon | REQ-MAC-002..007 | `swift test`: 20 unit tests |
| T-MAC-04 | `LanaMenu`: `MenuBarExtra` window, usage block with sparklines, page rows, ⌥ sign-in, settings window, launch at login, `--dump-status`, `--snapshot`, `--write-iconset` | REQ-MAC-001..007 | `--dump-status` on the demo daemon: RAM 49 512 448 B = `ps` 48 352 KB exactly; fake tree (`claude` + `sleep` + `yes`): RAM equal to the `ps` sum, CPU 100 % vs `ps` 99.3 %, "+ 1 Claude Code process, 2 others" |
| T-MAC-05 | `make macos-app`, `make macos-app-test`, `Info.plist.in` | REQ-MAC-008 | `dist/Lana.app` built, `codesign --verify` ok, `plutil -lint` ok, `AppIcon.icns` present |
| T-MAC-06 | End-to-end: start a daemon with the app's start code, sign-in link with `next`, stop | REQ-MAC-005, 006 | `LANA_MENU_E2E_BINARY=<lana> swift test --filter DaemonE2ETests` ok |
| T-MAC-07 | Docs: README "macOS menu bar", CHANGELOG Unreleased, this spec, index row | — | — |

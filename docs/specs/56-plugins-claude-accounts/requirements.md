# Spec 56: Plugins and the Claude accounts plugin

- **Status**: Approved (owner, 2026-11-17: "ada mode buat manage dan swap account claude code, terapkan di sini juga. tapi sistemnya instal tool dulu baru bisa pakai. page tool jadi tool & plugin")
- **Branch**: `feature/workbench` (with spec 55)
- **Reference**: the owner's herdr mod `~/.config/herdr-app/mods/claude-accounts` (wraps the `cswap` CLI of claude-swap).

## Decisions

1. A **plugin** adds a feature to a Lana page. It needs one tool from the Toolbox (spec 34) and turns on by itself once that tool is installed. No separate enable switch.
2. The Tools page becomes **Tools & plugins**: a Plugins section above the installed and available tools.
3. First plugin: **Claude accounts** in the Workbench, built on the tool **claude-swap** (`cswap`).

## Requirements

- **REQ-PLG-001** The Toolbox catalog ships `claude-swap` (detect `cswap`; install with `uv tool install claude-swap`, else `pipx install claude-swap`). Commands: `list` (`cswap list --json`, risk read) and `switch` (`cswap switch <account>`, risk admin).
- **REQ-PLG-002** The Plugins section lists each plugin with its tool. Tool not installed: "Install <tool> first" and an Install tool button (one click, the tool's automatic method). Installed: "On" and a link to the page the plugin lives on.
- **REQ-PLG-003** The sidebar item and breadcrumbs read "Tools & plugins".
- **REQ-PLG-010** When claude-swap is installed, the Workbench shows a Claude accounts button (tab bar on the desktop, space header on a phone). It opens a popover (desktop) or a sheet (phone).
- **REQ-PLG-011** The popup lists every account from `cswap list --json`: number, email or alias, organization, default tag, "most room" tag (the usable account whose tightest of the 5-hour and weekly windows is least used), a bar per usage window (5h, 7d, per model) with percent and time to reset, and a note when usage is not current or the login expired. It rereads every 2 minutes and on Refresh.
- **REQ-PLG-012** Actions per account:
  - **Make default**: `cswap switch N`, then reread.
  - **New tab**: a Workbench terminal running `<cswap> run N` in the current space's folder.
  - **Continue beside**: `<cswap> run N -- --continue` split to the right of the focused pane (a new tab when none).
  - **Re-login** (usage not current or login expired) and **Add account** (header): a new tab that runs `claude auth login`, then `<cswap> add`.
- **REQ-PLG-013** `<cswap>` is the binary path the Toolbox detected, so the terminal does not depend on the daemon's PATH.

## Out of scope

Plugins from third parties, a plugin manifest format, auto-switch (`cswap auto`), per-folder account mapping.

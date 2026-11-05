# Requirements: Toolbox (installable tools)

- **Issue**: `—`
- **Status**: Draft — open questions below block approval
- **Revision**: 0
- **Owner**: ridho
- **Contract**: api-contract.md — Required (not written yet)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | Chat, 2026-11-05: "obscura kan belum tentu ada di device user … tool yang di dapat di install … ga cuman obscura … bisa di guanakan di seluruh workflow dan session" | Goal and scope |
| Smoke test | spec #33 T-012: the browser node fails when `obscura` is not on PATH | Problem |
| Existing specs | #7 MCP and skills, #12 MCP manage, #22 background jobs, #33 workflows | Reuse: MCP manager, job runner, approvals, tool registry |

## Problem

Lana uses some programs that are not part of lana itself. Today the only one is Obscura, through the workflow browser
node. Later there will be others, such as `yt-dlp`, `ffmpeg`, `pandoc`, `gh` or a headless browser. Today such a
program must already be on the owner's PATH. Nothing tells the owner it is missing, how to install it, or which agents
and workflows depend on it. Each program is also wired in by hand: the browser node shells out to `obscura` from Go code.

## Goal

A **Toolbox** where the owner installs external programs once and then uses them everywhere in lana:

- as tools for agents in chats and sessions, picked per agent like MCP servers;
- as workflow nodes and in the generic `tool` node, with the permission lock;
- with clear status: installed, missing, broken, or update available.

## Concepts

- **Package**: one external program described by a **manifest** (`toolbox.toml`), for example `obscura`. The manifest
  holds:
  - identity: id, name, description, homepage, license, icon;
  - **install methods**, in order of preference: a direct download (URL per OS/arch plus a sha256), `nix profile`,
    `brew`, `npm -g --prefix`, `uv tool`, `cargo install`, `go install`;
  - **detect**: the binary name, a version command, and a minimum version;
  - **provides**: what the package gives lana (see below);
  - **needs**: secrets or env vars the package needs, for example an API key, which live in the existing secret store.
- **Command**: a CLI call that a package exposes as a lana tool. It has a name (`obscura.fetch`), a description, a JSON
  input schema, an argv template (`["fetch", "--dump", "{{format}}", "{{url}}"]`), a risk (read/write/admin), a
  timeout, the output format (text or JSON), and a working-directory rule (agent workspace or the package's data dir).
- **MCP server**: a package may instead, or also, start an MCP server (`obscura mcp`). This reuses the existing MCP
  manager, so the package installs the binary and registers `[mcp.<id>]`.
- **Workflow node**: a package may offer node types. The palette shows them in a "Toolbox" group. A node is a command
  with a form for its input schema. The current `browser` node becomes the `obscura.fetch` node.
- **Package home**: `$LANA_HOME/toolbox/<id>/`. Downloaded binaries go in `bin/`, persistent data (browser profiles,
  caches) in `data/`, and install logs in `logs/`. No root rights, no global installs (owner rule: no sudo or global
  npm).

## Behavior rules (draft)

- **REQ-TB-001 Catalog.** Lana ships a curated built-in catalog of manifests, starting with Obscura. The owner can add a
  local manifest folder (`$LANA_HOME/toolbox/local/<id>/toolbox.toml`). A remote index is out of scope for v1.
- **REQ-TB-002 Detection.** On start and on demand, lana checks every package: the binary in the package `bin/`, then
  PATH. It runs the version command and records the status: `installed vX`, `missing`, `too_old`, `broken` (the version
  command fails), or `update` (the manifest knows a newer version).
- **REQ-TB-003 Install.** Only the owner installs, updates or uninstalls, from the web page or by answering an
  approval. The owner picks a method (the first available one is the default). The install runs as a background job
  with a live log. A download is verified with sha256 before it runs. An agent may *ask* for a package (a question to
  the owner, "Install Obscura?"), but never installs one by itself.
- **REQ-TB-004 Commands as tools.** Each installed package registers its commands in the tool registry as
  `<package>.<command>`. Their risk goes through the normal permission engine. A missing package registers nothing.
- **REQ-TB-005 Agents.** The agent Tools tab shows a "Toolbox" group. The owner turns a package or a command on per
  agent, stored in `[agents.<name>] toolbox = ["obscura"]`. Session turns then offer those commands.
- **REQ-TB-006 Workflows.** Package nodes appear in the palette. Validation reports `package_missing` with an Install
  link. Activation refuses a graph that needs a missing package. The lock lists package commands with their risk. A run
  that hits a package removed after activation fails the node with "Obscura is not installed", and the guardian treats
  that as non-transient and may ask the owner to install it.
- **REQ-TB-007 Secrets and env.** Package env values come from the secret store and are never shown to the model.
- **REQ-TB-008 Uninstall.** Uninstall removes `bin/` and keeps `data/` unless the owner chooses "also delete data".
  Agents and workflows that use the package show it as missing.

## Web (draft)

- **Configure → Toolbox** page: cards for installed and available packages, each with its status, version, what it
  provides (commands, MCP, nodes) and "Used by" (agents and workflows); a filter; and "Add local package".
- **Package detail** page: description, install method picker, Install / Update / Uninstall, a live install log, a
  health check button, the command list with input schemas and a "Try it" form, a per-agent switch, and the workflows
  that use it.
- **Workflow editor**: a Toolbox palette group; a missing-package banner on nodes, with an Install link.
- **Schedules / Logs**: install jobs and command failures show up in Logs.

## Phases

1. **P1 (unblocks the smoke test).** Manifest format; built-in Obscura manifest; detection and statuses; the Toolbox
   page (list and detail, read-only plus Install via the download and nix methods); the browser node resolves
   `obscura` through the Toolbox and shows "not installed — Install"; workflow validation reports `package_missing`.
2. **P2.** Commands as registered tools; the per-agent Toolbox switch; workflow palette nodes generated from commands;
   lock and permission integration; "Try it".
3. **P3.** MCP-backed packages (Obscura MCP for interactive browsing in chats); local manifests; update checks; more
   built-in packages (yt-dlp, ffmpeg, pandoc, gh).

## Not included (v1)

- A remote package index or marketplace.
- System package managers that need root (apt, dnf).
- Windows.
- Sandboxing beyond lana's existing permission engine and workspace rules.

## Open questions

| # | Question | Agent recommendation |
|---|---|---|
| 1 | Name in the sidebar: "Toolbox", "Tools" or "Apps"? Agents already have a "Tools" tab. | **Toolbox**, under Configure next to MCP. |
| 2 | Which install methods count as default? | Direct download into `$LANA_HOME/toolbox` (no root, works on any device), then nix when a flake is present, brew only if the owner allows it. |
| 3 | Can an agent request an install mid-turn? | Yes, as an approval-style question to the owner; never silent. |
| 4 | Should MCP servers move into the Toolbox page? | No for v1: MCP stays its own page; a package can *provide* an MCP server that also shows there. |
| 5 | First built-in packages after Obscura? | yt-dlp, ffmpeg, pandoc, gh (all have static builds or simple installers). |

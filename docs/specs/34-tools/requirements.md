# Requirements: Tools (installable programs)

- **Issue**: `—`
- **Status**: Approved (owner answers 2026-11-05)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: api-contract.md — Required (not written yet)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | Chat, 2026-11-05: "obscura kan belum tentu ada di device user … tool yang di dapat di install … ga cuman obscura … bisa di guanakan di seluruh workflow dan session" | Goal and scope |
| Owner answers | Chat, 2026-11-05: name "Tools"; normal installs (not only nix); agents may request an install with approval; MCP stays separate; no built-in packages | Resolutions |
| Smoke test | spec #33 T-012: the browser node fails when `obscura` is not on PATH | Problem |
| Existing specs | #7 MCP and skills, #12 MCP manage, #22 background jobs, #33 workflows | Reuse: MCP manager, job runner, approvals, tool registry |

## Problem

Lana uses some programs that are not part of lana itself. Today the only one is Obscura, through the workflow browser
node. Later there will be others, such as `yt-dlp`, `ffmpeg`, `pandoc`, `gh` or a headless browser. Today such a
program must already be on the owner's PATH. Nothing tells the owner it is missing, how to install it, or which agents
and workflows depend on it. Each program is also wired in by hand: the browser node shells out to `obscura` from Go code.

## Goal

A **Tools** page (Configure → Tools; the code calls it the toolbox) where the owner installs external programs once and then uses them everywhere in lana:

- as tools for agents in chats and sessions, picked per agent like MCP servers;
- as workflow nodes and in the generic `tool` node, with the permission lock;
- with clear status: installed, missing, broken, or update available.

## Concepts

- **Package**: one external program described by a **manifest** (`tool.toml`), for example `obscura`. The manifest
  holds:
  - identity: id, name, description, homepage, license, icon;
  - **install methods**, tried in order: a shell install command (`brew install obscura`, `npm install -g …`,
    `pipx install …`, `curl … | sh`, `nix profile install …`), or a direct download (URL per OS/arch plus an optional
    sha256) unpacked into the package `bin/`. Nix is one method among others, never assumed;
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
- **Package home**: `$LANA_HOME/tools/<id>/`. Downloaded binaries go in `bin/`, persistent data (browser profiles,
  caches) in `data/`, and install logs in `logs/`. No root rights, no global installs (owner rule: no sudo or global
  npm).

## Behavior rules (draft)

- **REQ-TB-001 Catalog.** The catalog starts empty; lana ships no packages. The owner adds a package with a form, by
  pasting a manifest, or by asking an agent to draft one (the draft still needs the owner's approval). Manifests live in
  `$LANA_HOME/tools/<id>/tool.toml`. A remote index is out of scope.
- **REQ-TB-002 Detection.** On start and on demand, lana checks every package: the binary in the package `bin/`, then
  PATH. It runs the version command and records the status: `installed vX`, `missing`, `too_old`, `broken` (the version
  command fails).
- **REQ-TB-003 Install.** Only the owner installs, updates or uninstalls, from the web page or by answering an
  approval. The owner picks a method (the first one is the default). The install runs as a background job with a live
  log. A download with a sha256 is verified before it is unpacked. An agent may request an install or a new package
  through the `tools_request` tool; it always raises an owner approval (risk admin, never auto-allowed, not even by
  a workflow lock) and installs only after Approve.
- **REQ-TB-004 Commands as tools.** Each installed package registers its commands in the tool registry as
  `<package>_<command>` (for example `obscura_fetch`). Their risk goes through the normal permission engine. A missing package registers nothing.
- **REQ-TB-005 Agents.** The agent Tools tab shows an "Installed tools" group. The owner turns a command on per agent;
  it is stored in the existing `[agents.<name>] tools` list, so session turns offer it like a built-in tool.
- **REQ-TB-006 Workflows.** Installed commands appear in the palette under "Installed tools" as `tool` nodes with a form
  from the input schema. The `browser` node needs a package that provides the binary `obscura`. Validation reports `package_missing` with an Install
  link. Activation refuses a graph that needs a missing package. The lock lists package commands with their risk. A run
  that hits a package removed after activation fails the node with "Obscura is not installed", and the guardian treats
  that as non-transient and may ask the owner to install it.
- **REQ-TB-007 Secrets and env.** Package env values come from the secret store and are never shown to the model.
- **REQ-TB-008 Uninstall.** Uninstall removes `bin/` and keeps `data/` unless the owner chooses "also delete data".
  Agents and workflows that use the package show it as missing.

## Web

- **Configure → Tools**: a list of packages with status, version, the commands they provide and "Used by"; an empty
  state that explains how to add one; "Add tool" (form or pasted manifest).
- **Tool detail**: description, install methods with Install / Reinstall / Uninstall, a live install log, a health check
  ("Check again"), the commands with their input fields and a "Try it" form, and the agents and workflows that use it.
- **Workflow editor**: an "Installed tools" palette group; a missing-package issue with a link to Tools.

## Phases

1. **P1 (this build).** Manifest format and storage; add/edit/delete; detection and statuses; install and uninstall as
   jobs with a live log; commands registered as tools; per-agent enable through the Tools tab; workflow `tool` nodes
   and the browser node resolve packages; the Tools pages; `tools_request` with owner approval.
2. **P2.** Agent-drafted manifests from chat; MCP-providing packages; update checks.

## Not included (v1)

- A remote package index or marketplace.
- System package managers that need root (apt, dnf).
- Windows.
- Sandboxing beyond lana's existing permission engine and workspace rules.

## Resolutions

| # | Question | Resolution | Status |
|---|---|---|---|
| 1 | Name | "Tools" in the sidebar (Configure). | Owner |
| 2 | Install methods | Normal installs: a shell install command or a download; nix is optional, never assumed. | Owner |
| 3 | Agent requests | Allowed, always with an owner approval. | Owner |
| 4 | Merge MCP | No; MCP stays its own page. | Owner |
| 5 | Built-in packages | None; the catalog starts empty. | Owner |

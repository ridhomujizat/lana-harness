# lana

lana is a personal AI agent harness. It runs agents with tools (files, shell, web, memory,
skills, MCP, subagents), keeps every session in SQLite, and talks to you through a terminal
chat, Telegram, and a web dashboard. It also keeps long-term knowledge: a searchable history of
your conversations and an Obsidian-compatible wiki that grows from what you share.

Current version: see [`VERSION`](VERSION) and [`CHANGELOG.md`](CHANGELOG.md). Releases are in
beta (`0.x.y-beta.N`): config keys, events, and tools may still change between betas.

## Requirements

- macOS (Apple silicon or Intel) or Linux.
- An API key for an OpenAI-compatible provider: DeepSeek, OpenAI, or OpenRouter.
- For the prebuilt binary: `curl` or the `gh` CLI, and GitHub access to the repository.
- To build from source, one of:
  - [Nix](https://nixos.org/download) with flakes. The dev shell in `flake.nix` provides Go,
    Node.js 22, SQLite, and make. This is the supported path.
  - Go 1.25 or newer, and make. Node.js 22 is needed only to rebuild the web dashboard; the
    built dashboard is already in `web/dist`.

No C compiler is needed: lana builds with `CGO_ENABLED=0`.

## Install

### Option A: prebuilt binary (recommended)

The repository is public, so no GitHub login is needed:

```sh
curl -fsSL https://raw.githubusercontent.com/ridhomujizat/lana-harness/main/install.sh | bash
```

The script picks the binary for your OS and CPU (macOS arm64/amd64, Linux amd64/arm64) from the
newest GitHub Release (betas included), checks it against `SHA256SUMS`, and installs it to
`~/.local/bin/lana`. Options:

```sh
LANA_VERSION=v0.1.0-beta.1 bash install.sh      # a specific version
LANA_INSTALL_DIR=/usr/local/bin bash install.sh # another folder
LANA_FROM_DIR=dist bash install.sh              # binaries from a local `make release checksums`
```

Run the same command again to upgrade, or use the update card in the dashboard (see
[Upgrade](#upgrade)). `main` is the stable branch: every push to `main` whose `VERSION` has no
release yet publishes one through `.github/workflows/release.yml`.

### Option B: build from source

1. Get the source:

   ```sh
   git clone https://github.com/ridhomujizat/lana-harness.git
   cd lana-harness
   git checkout main             # stable; development has the work in progress
   ```

2. Build and install. With Nix:

   ```sh
   nix develop -c make install
   ```

   Without Nix (Go 1.25+ on your PATH):

   ```sh
   CGO_ENABLED=0 make install
   ```

`make install` builds `./lana` with the version, commit, and build date, then copies it to
`~/.local/bin/lana`. Use `make install PREFIX=/usr/local` to install somewhere else.
`make build` alone writes `./lana` in the repo without installing it. `make release checksums`
builds the release binaries and `SHA256SUMS` into `dist/`.

### Put lana on your PATH

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc   # then open a new terminal
lana --version
# lana 0.1.0-beta.1 (61af960, 2026-11-01T17:36:08Z)
```

### 3. Create the config

```sh
lana init --provider deepseek     # or openai, openrouter
```

This creates `~/.lana` (override with `LANA_HOME`):

| File | Purpose |
|---|---|
| `config.toml` | providers, agents, routes, channels, web, knowledge |
| `secrets.env` | API keys and tokens, mode `0600` |
| `agents/<name>/` | each agent's persona and memory |
| `lana.db` | the session database (created on first run) |

`lana init` asks for the API key and saves it to `secrets.env`. You can instead export it in your
shell, for example `export DEEPSEEK_API_KEY=...` in `~/.zshrc`. A shell variable wins over
`secrets.env`.

Check the result:

```sh
lana config check
```

### 4. Try it

```sh
lana chat                      # terminal chat (TUI)
lana run "summarize README.md" # one prompt, then exit
```

## Run the daemon

The daemon serves Telegram and the web dashboard, and runs background work such as knowledge
upkeep. Turn on at least one channel in `~/.lana/config.toml`:

```toml
[web]
enabled = true
addr = "127.0.0.1:18300"
```

Then start it:

```sh
lana daemon
```

On the first start the log prints a sign-in link, `http://127.0.0.1:18300/#/welcome?code=...`.
Open it to set the dashboard password. If you lose access, run `lana web reset-login`.

Telegram is connected from the dashboard (Channels page): it walks you through the bot token and
your chat id. Set `LANA_LOG=debug` for more detailed logs.

To keep the daemon running after you close the terminal, start it from a terminal multiplexer
(`tmux`) or a launchd/systemd service that runs `lana daemon`.

## Turn on memory and knowledge

Agents get only the tools listed in their `tools` array. Add these to each agent that should
remember and learn:

```toml
[agents.lana]
tools = [
  "read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch",
  "memory_read", "memory_write",
  "session_search",
  "knowledge_search", "knowledge_read", "knowledge_write", "knowledge_ingest",
]

[knowledge]
agent = "lana"              # model used for inbox, journal promotion, and lint
# path = "~/Documents/Knowledge"   # default: ~/.lana/knowledge
# consolidate_at = "03:00"         # nightly run; "" turns it off
# embedding_provider = "openai"    # optional hybrid search
# embedding_model = "text-embedding-3-small"
```

The vault in `~/.lana/knowledge` is a plain folder of markdown. Open it in Obsidian with "Open
folder as vault". Add knowledge by:

- asking an agent to save something (`knowledge_write`, `knowledge_ingest`),
- dropping `.md`, `.txt`, `.pdf`, or `.html` files into `~/.lana/knowledge/inbox/`,
- `/save` in Telegram (reply to a message or document, or `/save <text or URL>`),
- the Upload button on the dashboard's Knowledge page.

See [spec 30](docs/specs/30-knowledge/requirements.md) and
[spec 31](docs/specs/31-knowledge-upkeep/requirements.md) for the details.

## Upgrade

When a newer release is out, the web dashboard shows a card in the corner. Click **Update now**:
lana downloads the new binary, checks its checksum, tries it on a copy of your data, waits for
running turns, backs up the databases, `config.toml`, and the old binary to
`~/.lana/updates/backups/`, and restarts on the new version. If the new version fails to start,
lana puts the old binary and the old data back by itself and the card says why. To go back by
hand, stop the daemon and run `lana update rollback`.

The card offers stable releases. To get betas too, set it in Settings → Updates, or:

```toml
[update]
channel = "beta"   # default "stable"
check = true       # check every 6 hours
```

Self-update needs a binary lana can replace, such as `~/.local/bin/lana` from the install script.
For a binary in the Nix store or Homebrew, the card shows the manual command.

With the prebuilt binary, you can also run the install command again. From source:

```sh
cd lana-harness
git fetch --tags
git pull origin main            # or: git checkout v<new-version>
nix develop -c make install
```

Then restart `lana daemon`. Read the "Upgrade notes" of the new version in
[`CHANGELOG.md`](CHANGELOG.md) first. When they mention a database migration, back up
`~/.lana/lana.db`: older binaries refuse an upgraded database.

## Uninstall

```sh
rm ~/.local/bin/lana
rm -rf ~/.lana    # deletes config, sessions, memory, and the knowledge vault
```

## Development

```sh
nix develop                     # Go, Node, SQLite, make
go vet ./... && go test -count=1 ./...
cd web/ui && npx vitest run && npm run build   # dashboard; unset NODE_ENV first
```

Feature specs live in [`docs/specs`](docs/specs/README.md). Releases follow
[`docs/releasing.md`](docs/releasing.md).

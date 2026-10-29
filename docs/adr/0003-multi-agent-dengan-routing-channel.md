# 0003. Jalankan multi-agent ringan dengan routing dari channel

- **Status**: Accepted (detail implementasi Rust diganti oleh [0013](0013-gunakan-go-sebagai-single-binary.md); keputusan arsitekturnya tetap)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-dari-modul-service-dan-hook.md), [0004](0004-permission-mode-per-agent.md), [0011](0011-subagent-sebagai-child-session.md)

## Context

Pemilik butuh tiga peran berbeda: coding assistant, personal assistant, dan automation/ops.
Tiap peran butuh system prompt, model, tool, dan tingkat izin yang berbeda. zeroclaw memakai satu
agent untuk semua channel. nouride memakai beberapa agent terisolasi dengan persona sendiri.

Agent penuh per proses (seperti container per agent) terlalu berat untuk target "ringan".

## Decision

Kita menjalankan **beberapa agent di dalam satu daemon**. Agent adalah data (config), bukan proses.
Satu `AgentRuntime` dibuat per session aktif sebagai task `tokio`.

Agent didefinisikan di `config.toml`:

```toml
[providers.anthropic]
kind = "anthropic"
api_key_env = "ANTHROPIC_API_KEY"

[providers.openrouter]
kind = "openai_compat"
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"

[agents.coder]
provider = "anthropic"
model = "claude-sonnet-5-5"
fallback = ["openrouter:anthropic/claude-haiku-5-5"]
prompt_file = "agents/coder.md"
workspace = "~/coding"
tools = ["read_file", "write_file", "edit_file", "glob", "grep", "bash"]
mcp = ["github"]
skills = ["git-workflow"]
permission = "ask"            # lihat ADR 0004

[agents.asisten]
provider = "openrouter"
model = "openai/gpt-5-mini"
prompt_file = "agents/asisten.md"
tools = ["web_fetch", "memory_search", "memory_save", "cron_create"]
permission = "trust"

[[routes]]
channel = "telegram"
match = { chat_id = 123456789 }
agent = "asisten"

[[routes]]
channel = "cli"
agent = "coder"               # default untuk `lana chat`
```

Aturan routing:

1. Pesan masuk dicocokkan ke `[[routes]]` dari atas ke bawah. Route pertama yang cocok menang.
2. User bisa pindah agent di dalam chat dengan perintah `/agent <nama>`. Pilihan ini disimpan per session.
3. Jika tidak ada route yang cocok, pesan ditolak dan dicatat di log. Tidak ada fallback diam-diam.

Session key = `(channel, address, agent)`. Satu session hanya punya satu turn aktif dan satu
**inbox**, mengikuti `dsh`. Pesan yang masuk saat turn berjalan masuk inbox. Agent loop mengambil
(claim) satu pesan dari inbox di awal step berikutnya, jadi user bisa mengoreksi agent di tengah
turn tanpa menunggu turn selesai.

Delegasi antar agent memakai subagent sebagai child session (lihat [0011](0011-subagent-sebagai-child-session.md)).

## Consequences

**Positive**
- Biaya tambahan per agent hanya config dan satu task `tokio` saat aktif.
- Satu bot Telegram bisa melayani beberapa agent lewat route atau `/agent`.
- Persona dan tool terisolasi secara logis per agent.

**Negative / tradeoffs**
- Isolasi hanya logis. Bug di satu tool bisa memengaruhi seluruh daemon.
- Antrean per session berarti pesan cepat beruntun tidak diproses paralel.

**Neutral**
- Prompt agent disimpan sebagai file Markdown di `~/.lana/agents/`, mudah diedit dan di-version.

## Alternatives considered

- **Single agent (gaya zeroclaw)** — paling sederhana. Ditolak karena peran coding dan personal butuh izin berbeda.
- **Satu proses per agent** — isolasi kuat. Ditolak karena RAM berlipat dan butuh IPC.

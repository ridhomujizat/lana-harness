# 0006. Tambah kemampuan lewat MCP client dan Skills

- **Status**: Accepted (detail implementasi Rust diganti oleh [0013](0013-gunakan-go-sebagai-single-binary.md); keputusan arsitekturnya tetap)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0002](0002-core-dari-modul-service-dan-hook.md), [0004](0004-permission-mode-per-agent.md)

## Context

Builtin tool tidak akan cukup untuk semua kebutuhan (GitHub, kalender, database, browser).
Menulis integrasi sendiri untuk tiap layanan membuat binary membengkak. Dua standar sudah
umum dipakai:

- **MCP (Model Context Protocol)** — server eksternal menyediakan tool lewat stdio atau HTTP.
- **Skills (`SKILL.md`)** — folder berisi instruksi Markdown dan script, dimuat hanya saat relevan (gaya Claude Code).

## Decision

**MCP client.** `lana-mcp` memakai crate resmi `rmcp` dengan transport `stdio` dan streamable HTTP.

```toml
[mcp.github]
command = "github-mcp-server"
args = ["stdio"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }

[mcp.notion]
url = "https://mcp.notion.com/mcp"
```

- Server MCP dijalankan **lazy**: proses baru start saat agent yang memakainya aktif, dan dihentikan setelah idle 10 menit.
- Tiap tool MCP dibungkus sebagai `Tool` dengan nama `mcp__<server>__<tool>`. Risk default `Exec`,
  sehingga di mode `ask` selalu minta approval kecuali ada di `allow`.
- Agent hanya melihat tool dari server yang ada di daftar `mcp = [...]` miliknya.

**Skills.** Skill disimpan di `~/.lana/skills/<nama>/SKILL.md` dengan frontmatter:

```markdown
---
name: git-workflow
description: Buat branch, commit conventional, dan buka PR dengan gh.
---
Langkah-langkah ...
```

- Saat session dimulai, hanya `name` dan `description` skill milik agent yang masuk system prompt.
- Model memanggil tool `skill_load(name)` untuk membaca isi lengkap. Ini menghemat token (progressive disclosure).
- Script di dalam folder skill dijalankan lewat tool `bash` biasa, jadi tetap melewati permission engine.

## Consequences

**Positive**
- Integrasi baru tidak butuh rebuild binary.
- Ekosistem MCP server dan skill yang sudah ada bisa langsung dipakai.
- Lazy start menjaga RAM idle tetap kecil.

**Negative / tradeoffs**
- MCP server pihak ketiga (sering Node/Python) memakai RAM sendiri saat aktif.
- Kualitas dan keamanan MCP server di luar kontrol kita. Output tool MCP diperlakukan sebagai data, bukan instruksi.

**Neutral**
- `rmcp` mengikuti versi spesifikasi MCP. Upgrade spesifikasi = upgrade crate.

## Alternatives considered

- **Plugin WASM (gaya zeroclaw `wit`)** — sandbox bagus. Ditunda karena butuh runtime `wasmtime` yang besar.
- **Plugin dynamic library** — ditolak karena ABI Rust tidak stabil dan tidak aman.

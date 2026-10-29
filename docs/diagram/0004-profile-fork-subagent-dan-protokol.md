# 0004. Profile, Fork, Subagent, Dan Protokol

- **ADR reference**: [0010. Profile dan config patch](../adr/0010-profile-dan-config-patch.md), [0009. Session event log](../adr/0009-session-sebagai-event-log-append-only.md), [0011. Subagent](../adr/0011-subagent-sebagai-child-session.md), [0012. ACP dan SDK](../adr/0012-acp-dan-sdk-json-rpc.md)
- **Related diagram**: [0002. Agent Loop Dan Approval](0002-agent-loop-dan-approval.md), [0003. Data Model Dan Scheduler](0003-data-model-dan-scheduler.md)
- **Purpose**: komposisi profile saat boot, fork dan resume session, alur subagent, dan alur ACP.

> Jaga diagram ini sinkron dengan ADR terkait. Jika ADR dan diagram berbeda, perbarui keduanya dalam satu perubahan.

## Komposisi Profile Saat Boot

```mermaid
flowchart TD
  L1["1. Base bawaan binary"] --> M["Merge berdasarkan id baris"]
  L2["2. Profile bawaan: daemon, headless, acp, sdk, minimal"] --> M
  L3["3. ~/.lana/profiles/nama.toml"] --> M
  L4["4. ~/.lana/config.toml"] --> M
  L5["5. --patch file"] --> M
  M --> V{"Semua modul dikenal dan dependency service lengkap?"}
  V -- "Tidak" --> Fail["Boot gagal dengan pesan jelas"]
  V -- "Ya" --> Sort["Urutkan modul dari dependency service"]
  Sort --> Install["Module.install ke ContextBuilder"]
  Install --> Run["Context siap, task tokio start"]
  M -.-> Dump["--dump-config: cetak hasil dan asal tiap baris"]
```

Aturan merge:

- `[[modules]]` digabung per `id`. Baris yang sama mengganti seluruh `config`.
- `disabled = true` mematikan baris.
- Tabel `providers`, `agents`, `mcp`, `channels` digabung rekursif per key.
- `[[routes]]` dari lapisan terakhir menggantikan seluruh daftar.

## Modul Per Profile

```mermaid
flowchart LR
  subgraph Base["base"]
    B1["providers"]
    B2["store"]
    B3["agent loop"]
    B4["tools dan permission"]
    B5["approval"]
    B6["compaction"]
    B7["mcp dan skills"]
  end
  Daemon["daemon"] --> Base
  Daemon --> D1["http dan web"]
  Daemon --> D2["telegram dan whatsapp"]
  Daemon --> D3["scheduler dan router"]
  Headless["headless"] --> Base
  Headless --> H1["runner one-shot stdout"]
  Acp["acp"] --> Base
  Acp --> A1["server ACP stdio"]
  Sdk["sdk"] --> Base
  Sdk --> S1["server JSON-RPC stdio atau socket"]
  Minimal["minimal"] --> B1
  Minimal --> B2
  Minimal --> B3
```

## Fork Session

Fork tidak menyalin event. Session anak membaca event induk sampai `inherited_seq`, lalu event miliknya.

```mermaid
flowchart LR
  subgraph Parent["Session A"]
    P1["seq 1-10: turn 1"] --> P2["seq 11-20: turn 2"] --> P3["seq 21-30: turn 3"]
  end
  subgraph Child["Session B fork dari A di seq 20"]
    C0["session/created parent A, inherited_seq 20"] --> C1["seq 1-12: turn baru"]
  end
  P2 -. "derive membaca seq 1-20 dari A" .-> C0
```

Aturan fork:

- Fork hanya di batas turn (`seq` sebuah `turn/end`).
- Grant approval session dan policy ikut terbawa karena berada di event yang diwarisi.

## Resume Setelah Crash

```mermaid
flowchart TD
  A["Buka session"] --> B{"Event terakhir turn/end?"}
  B -- "Ya" --> Ready["Siap menerima pesan"]
  B -- "Tidak" --> C["Cari tool/call tanpa tool/result"]
  C --> D["Tulis tool/result error untuk tiap call yatim"]
  D --> E["Tulis turn/end reason interrupted"]
  E --> Ready
```

## Subagent

```mermaid
sequenceDiagram
  autonumber
  participant Parent as Loop induk
  participant Log as Session log
  participant Child as Loop child
  participant LLM as Provider
  participant Ch as Channel induk

  Parent->>Log: tool/call task agent explorer
  Parent->>Log: Buat child session dengan parent_session_id dan parent_call_id
  Parent->>Child: Kirim prompt ke inbox child
  loop Sampai end_turn atau max_steps 15
    Child->>LLM: Request dengan tool child
    LLM-->>Child: Jawaban dan tool_use
    opt Tool butuh approval
      Child->>Ch: Approval dengan label explorer
      Ch-->>Child: Outcome
    end
  end
  Child->>Log: turn/end di child session
  Child-->>Parent: Jawaban final dan usage token
  Parent->>Log: tool/result berisi ringkasan child
```

Batas subagent:

- Kedalaman maksimal 2. Paling banyak 3 child berjalan bersamaan per induk.
- Permission child adalah irisan permission induk dan agent child.
- Batal di induk membatalkan semua child.

## ACP Dengan Editor

```mermaid
sequenceDiagram
  autonumber
  participant Ed as Editor ACP client
  participant Acp as lana acp
  participant AgentLoop as Agent loop
  participant Log as Session log

  Ed->>Acp: initialize
  Acp-->>Ed: Kemampuan agent
  Ed->>Acp: session/new dengan cwd
  Acp->>Log: session/created, workspace = cwd
  Ed->>Acp: session/prompt
  Acp->>AgentLoop: Masukkan ke inbox
  AgentLoop-->>Acp: Event session.event
  Acp-->>Ed: session/update text dan tool_call
  opt Approval
    Acp->>Ed: session/request_permission
    Ed-->>Acp: Pilihan user
  end
  Acp-->>Ed: Respons prompt dengan stopReason
```

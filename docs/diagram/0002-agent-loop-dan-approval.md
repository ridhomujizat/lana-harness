# 0002. Agent Loop Dan Approval

- **ADR reference**: [0002. Core dari modul, service, dan hook](../adr/0002-core-dari-modul-service-dan-hook.md), [0004. Permission dan approval](../adr/0004-permission-mode-per-agent.md), [0009. Session event log](../adr/0009-session-sebagai-event-log-append-only.md)
- **Related diagram**: [0001. System Context Dan Roadmap](0001-system-context-dan-roadmap.md), [0004. Profile, Fork, Subagent, Dan Protokol](0004-profile-fork-subagent-dan-protokol.md)
- **Purpose**: model turn dan step, posisi hook, sequence satu turn, rantai approval, dan failure handling.

Diagram ini untuk orang yang mengimplementasi `lana-agent`. Pola turn, step, dan hook mengikuti
deepseek-harness (`dsh`).

> Jaga diagram ini sinkron dengan ADR terkait. Jika ADR dan diagram berbeda, perbarui keduanya dalam satu perubahan.

## Turn Dan Step

- **Step** = satu request ke model ditambah tool yang dipanggilnya.
- **Turn** = nol atau lebih step. Turn dibuka sebelum input pertama di-claim dan ditutup saat tidak ada lagi yang harus dijawab.

```text
turn/start
  claim input dari inbox (pesan awal, plus satu pesan antre per step)
  hook agent.pre_step         terima, ubah, atau tolak input
    tolak di step pertama  -> turn/end tanpa step
  step/start
    susun system prompt dan schema tool
    hook agent.request        ubah kontrol request
    tulis system/message jika berubah, tulis user/message
    derive riwayat dari log, bekukan request
    hook llm.stream           retry, fallback, metering
      frame stream sementara ke channel
      assistant/message | assistant/attempt
    untuk setiap tool_use:
      tool/call
      hook tools.pre_execute  permission dan approval
      hook tools.execute
      hook tools.post_execute potong atau spill output
      tool/result
  step/end
  masih ada tool_result atau pesan baru di inbox -> step berikutnya
  hook agent.turn_stopping
turn/end
```

Nama dengan `/` adalah event durable di session log. Nama dengan `.` adalah hook hidup.

## Sequence Satu Turn

```mermaid
sequenceDiagram
  autonumber
  participant User
  participant Channel
  participant Inbox
  participant AgentLoop as Agent loop
  participant Log as Session log
  participant LLM as Provider
  participant Tools as Tool pipeline
  participant Appr as Approval

  User->>Channel: Kirim pesan
  Channel->>Inbox: Masukkan pesan
  AgentLoop->>Log: turn/start
  AgentLoop->>Inbox: Claim input
  AgentLoop->>AgentLoop: Hook agent.pre_step
  AgentLoop->>Log: step/start, user/message
  AgentLoop->>Log: Derive riwayat
  AgentLoop->>LLM: Stream request lewat hook llm.stream
  LLM-->>AgentLoop: Text delta dan tool_use
  AgentLoop-->>Channel: Frame stream sementara
  AgentLoop->>Log: assistant/message
  opt Ada tool_use
    AgentLoop->>Log: tool/call
    AgentLoop->>Tools: Hook tools.pre_execute
    opt Policy ask dan tool berisiko
      Tools->>Appr: request
      Appr->>Log: approval/asked
      Appr->>Channel: Tampilkan approval dengan call_id
      Channel->>User: Tombol allow atau deny
      User-->>Channel: Jawaban
      Channel-->>Appr: Outcome
      Appr->>Log: approval/decided
      Appr-->>Tools: Outcome
    end
    Tools->>Tools: Hook tools.execute dan tools.post_execute
    Tools-->>AgentLoop: Output atau error
    AgentLoop->>Log: tool/result
  end
  AgentLoop->>Log: step/end
  AgentLoop->>AgentLoop: Hook agent.turn_stopping
  AgentLoop->>Log: turn/end
  Log-->>Channel: Event session.event
  Channel-->>User: Jawaban final
```

Invariant:

- *Model-visible means logged*: setiap request model bisa direkonstruksi dari log.
- Setiap `tool/call` selalu punya `tool/result`, termasuk saat ditolak, dibatalkan, atau error.
- Satu session hanya punya satu turn aktif. Pesan baru masuk inbox dan di-claim di step berikutnya.
- `max_steps` default 25 per turn, 15 untuk subagent.
- Tool `Read` yang independen boleh jalan paralel. Tool `Write` dan `Exec` jalan berurutan.

## Keputusan Permission Dan Approval

```mermaid
flowchart TD
  A["tools.pre_execute"] --> B{"Cocok rule deny?"}
  B -- "Ya" --> X["Tolak: tool/result error"]
  B -- "Tidak" --> W{"Path di dalam workspace?"}
  W -- "Tidak" --> X
  W -- "Ya" --> C{"Cocok allow atau grant session?"}
  C -- "Ya" --> OK["Lanjut ke tools.execute"]
  C -- "Tidak" --> D{"Policy session"}
  D -- "trust" --> OK
  D -- "never" --> E1{"Risk Read?"}
  E1 -- "Ya" --> OK
  E1 -- "Tidak" --> X
  D -- "ask" --> E2{"Risk Read?"}
  E2 -- "Ya" --> OK
  E2 -- "Tidak" --> G["ctx.approval.request"]
  G --> H{"Outcome"}
  H -- "allowed_once" --> OK
  H -- "allowed_session" --> R["Simpan grant di log"] --> OK
  H -- "rejected" --> X
  H -- "cancelled" --> X
  H -- "unavailable" --> X
```

## Rantai Penjawab Approval

Hook `approval.request` adalah waterfall. Penjawab pertama yang memiliki session menjawab. Jika
tidak ada yang menjawab, hasilnya `unavailable` (fail-closed).

```mermaid
flowchart LR
  Req["approval.request"] --> Ch{"Channel pemilik session terhubung?"}
  Ch -- "Ya" --> Human["CLI, Telegram, WhatsApp, atau web menjawab"]
  Ch -- "Tidak" --> Acp{"Session milik client ACP atau SDK?"}
  Acp -- "Ya" --> Client["session/request_permission atau approval.respond"]
  Acp -- "Tidak" --> Un["unavailable"]
  Human --> Out["Outcome"]
  Client --> Out
  Un --> Out
```

## Failure Handling

```mermaid
flowchart TD
  A["Error dalam turn"] --> B{"Kelas error"}
  B -- "Provider 429 atau 5xx" --> C["llm.stream retry backoff, maksimal 3 kali"]
  C --> D{"Masih gagal?"}
  D -- "Ya" --> E["Pindah ke provider fallback"]
  E --> F{"Fallback habis?"}
  F -- "Ya" --> G["turn/end reason error, kirim pesan ke channel"]
  B -- "Provider 4xx" --> G
  B -- "Context terlalu panjang" --> H["Tulis compaction/summary lalu ulangi sekali"]
  B -- "Tool error atau timeout" --> I["tool/result is_error ke model"]
  B -- "Proses mati di tengah turn" --> J["Saat resume: tutup tool/call yatim, turn/end interrupted"]
  A2["Setiap percobaan gagal"] --> K["assistant/attempt di log"]
  C --> A2
```

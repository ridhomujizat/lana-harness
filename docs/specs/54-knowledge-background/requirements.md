# Spec 54: Knowledge passes run reliably in the background

- **Status**: Approved (owner decision 2026-11-14, "approved semua saran mu")
- **Branch**: `feature/knowledge-background`, from `development` (0.5.0-beta.4)

## Problem

A prod consolidation run on 2026-11-14 00:34 showed:

1. **Journal passes fail.** 11 of 13 journal calls ended at exactly `journalMaxTokens` (2048) output tokens. `commandcode/deepseek-v4.1-flash` spends the budget on reasoning, so the reply is empty (`journal reply is not a JSON object`) or cut (`unexpected end of JSON input`). 68 parse failures since 2026-11-10. The compile of an ingested source hit `compileMaxTokens` (8192) the same way. A failed session keeps no mark and is retried, and fails, every night.
2. **The log hides it.** `log.md` says "journaled 15 sessions"; the number counts attempts, not successes.
3. **Promotion scope mismatch.** A page the agent created in the shared wiki got `supersede`/`append` ops with `scope: "domain"` and every op failed with `page not found`.
4. **Titles with `/` are dropped.** `create [[UI SDK (vercel-labs/ui)]]` failed with `title must not contain any of /\:*?"<>|#^[]`.
5. **Creates with an empty body.** 24 promotion creates were skipped as `invalid: empty page`. The domain prompt shows no `create` example, so the model puts the page text under another key.
6. **Journals wait for 03:00.** Sessions are journaled only after compaction or by the nightly run. The owner's laptop is often off at 03:00, so the catch-up runs when the laptop wakes, usually when the owner starts working.

A live probe on 2026-11-14 showed the commandcode API accepts `reasoning_effort: "off"`: the same JSON prompt used 0 reasoning characters and 52 output tokens, against 127 to 199 with the other values. Lana maps `off` to an empty field today, so thinking stays on.

## Decisions (owner, 2026-11-14)

1. Background knowledge calls turn thinking off and get a larger output budget (from Evonic: `enable_thinking=False`).
2. Journal right after the work, in the background, with a cooldown; no longer only at 03:00 (from Evonic: post-turn summarization with cooldown).
3. A background pass never delays the owner. When a turn starts, a running pass is cancelled and queued again (from Hermes `defer` mode: preempt and re-queue).
4. The nightly run stays for promotion and lint, and its catch-up waits until the owner is idle.
5. Fix the promotion scope, title and empty-body bugs.

## Requirements

### Reasoning and budgets

- **REQ-KBG-001** The commandcode provider sends `reasoning_effort: "off"` when the request effort is `off`. `""` still sends no field. `low` and `high`/`max` keep today's mapping.
- **REQ-KBG-002** Every knowledge call (journal, compile, domain promotion, shared promotion, lint) sets `ReasoningEffort: "off"`.
- **REQ-KBG-003** Output budgets: journal 8192, lint 8192, compile 16384, domain promotion 16384.
- **REQ-KBG-004** `parseJournalNotes` and the compile reply parser accept a reply with text before or after the JSON value. They use `decodeLLMJSON`, which already extracts the first balanced value, instead of failing when the trimmed reply does not start with `{` or `[`. An empty reply still fails, with the error `empty reply`.

### Journal outcomes and the log

- **REQ-KBG-010** A journal pass of one session ends with one outcome: `written` (notes appended), `empty` (the model returned no notes; the mark moves), `skipped` (no owner text; the mark moves), `failed` (request or parse error; the mark stays), or `preempted` (REQ-KBG-031; the mark stays).
- **REQ-KBG-011** The consolidation log entry reports successes and failures separately: `- journaled N sessions` counts `written` and `empty`. When any pass failed it adds `- journal failed: M sessions` and one line per failed session, `  - <session id>: <short error>`, at most 10. When any pass was preempted it adds `- journal preempted: K sessions (queued again)`.
- **REQ-KBG-012** The knowledge status API (`GET /v1/knowledge`) reports `journal_failed` (session id and error) for the most recent pass of each session that failed, so the Knowledge page can show it. A later `written` or `empty` outcome clears it.

### Background journal after the work

- **REQ-KBG-020** The knowledge service tracks activity from `Hooks.SessionEvent`: `turn/start` marks the session busy, and `turn/end` marks it idle and records the time. A busy mark with no event from that session for 30 minutes counts as idle (a lost `turn/end`).
- **REQ-KBG-021** The upkeep tick (every minute) runs one background journal pass when all of these hold:
  - no session is busy and no turn ended in the last 2 minutes (quiet window),
  - the session has text after its journal mark that includes an owner message,
  - its agent has a knowledge tool,
  - its last successful journal pass is at least 30 minutes old (cooldown), or it has none.

  At most one background journal pass runs at a time. The oldest eligible session goes first.
- **REQ-KBG-022** A session whose last pass `failed` waits for the cooldown before the next try. After 3 failures in a row it waits for the nightly run.
- **REQ-KBG-023** The compaction trigger (`JournalSoon`) stays. It also respects the quiet window: it queues the session instead of calling the model while a turn is running.

### Preempt

- **REQ-KBG-030** Background knowledge calls run on a context that is cancelled when any session sends `turn/start`. This covers the background journal (REQ-KBG-021), `JournalSoon`, and the journal step of the nightly run.
- **REQ-KBG-031** A cancelled journal pass leaves its mark unchanged, writes nothing to the vault, does not count as `failed`, and is eligible again after the next quiet window. It logs at debug level only.
- **REQ-KBG-032** Domain promotion, shared promotion, compile and lint are not cancelled midway. Each of them waits for the quiet window before it starts its model call.

### Nightly run

- **REQ-KBG-040** `consolidationDue` keeps today's rule (past `consolidate_at` today, last run before it) and also needs the quiet window. A catch-up after the laptop wakes therefore waits until the owner has been idle for 2 minutes.
- **REQ-KBG-041** The Knowledge page "Run now" button and `POST /v1/knowledge/consolidate` start at once, as today. Their journal step is still preemptible.

### Promotion fixes

- **REQ-KBG-050** When a promotion op other than `create` gets `page not found` in its requested scope, it is retried once in the other scope (domain ↔ shared). The log line names the scope that was used.
- **REQ-KBG-051** Titles from promotion and compile ops are cleaned before they are applied: each forbidden character `/\:*?"<>|#^[]` becomes `-`, runs of `-` and spaces collapse to one, and the result is trimmed. `UI SDK (vercel-labs/ui)` becomes `UI SDK (vercel-labs-ui)`. A title that is empty after cleaning is skipped as `invalid`.
- **REQ-KBG-052** The domain promotion prompt shows a full `create` example with `type`, `title`, `description` and a `body` in markdown, and says that a create without a body is dropped.
- **REQ-KBG-053** A compiled or promotion op accepts `content` or `markdown` as the page body when `body` and `text` are empty.

## Acceptance

- **AC-KBG-001** Unit: commandcode `buildEnvelope` sends `"reasoning_effort":"off"` for effort `off` and no field for `""`.
- **AC-KBG-002** Unit: each knowledge call sends `ReasoningEffort "off"` and the REQ-KBG-003 budgets (fake LLM records the request).
- **AC-KBG-003** Unit: `parseJournalNotes` accepts `Here you go: {"notes":["a"]}` and a fenced reply, and fails on `""` with `empty reply`.
- **AC-KBG-004** Unit: a consolidation with one written, one failed and one preempted session logs `journaled 1 sessions`, `journal failed: 1 sessions` with the session line, and `journal preempted: 1 sessions`.
- **AC-KBG-005** Unit (fake clock and events): the background journal runs only after the 2-minute quiet window and the 30-minute cooldown; a `turn/start` during the pass cancels it, the mark stays, and the next quiet window journals it.
- **AC-KBG-006** Unit: `consolidationDue` is false while a turn is busy or within 2 minutes of a `turn/end`, and true after.
- **AC-KBG-007** Unit: a `supersede` with `scope: "domain"` on a page that exists only in the shared wiki is applied to the shared page.
- **AC-KBG-008** Unit: a `create` titled `UI SDK (vercel-labs/ui)` writes `UI SDK (vercel-labs-ui).md`; a `create` with `content` instead of `body` writes a non-empty page.
- **AC-KBG-009** End to end on a scratch home with a real commandcode model: a chat turn is followed by a journal entry within the quiet window plus one tick, with no parse failure; a message sent while that pass runs cancels it, and the entry is written after the next quiet window; the turn's reply is not delayed by the pass.

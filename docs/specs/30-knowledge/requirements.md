# Requirements: Knowledge vault

- **Issue**: —
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (config section, event kind)
- **Related specs**: 29 (session search), 31 (knowledge upkeep), 20 (memory capture)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-01 | "nah saya ingin ada berkepanjangan knowladge gitu, jadi seperti secondbraind manusia", "jangan brain tapi knowladge aja untuk naming fiturnya", "oke sip implement 3 spec yg kamu usulkan" | goal, naming |
| Owner 2026-11-01 | "usernya tetep saya kok": the only user is the technical owner; Obsidian, API keys, and config edits are fine | constraints |
| charlie947/ai-second-brain, Karpathy LLM wiki gist | `raw/` sources are never edited; the LLM compiles `wiki/` pages with wikilinks and an index; periodic health checks | reference design |
| OpenClaw `memory-wiki` docs | entity/concept pages, `sources/`, `index.md`, `inbox.md`; human-written blocks preserved; lint for contradictions | reference design |
| evonic `kb/` | Obsidian frontmatter (title, aliases, type, description, tags); organizer caps ops per run, update only appends, exact title/alias match blocks duplicate creates (`memory_manager.py:1416-1550`) | reference design |
| OpenClaw memory docs | hybrid search: FTS when no embedding provider, keyword + vector when one is set | reference design |

## Goal

lana has a long-term knowledge base that grows like a second brain: documents, links, and notes
the owner shares become linked wiki pages that any agent can search and update. The vault is a
plain Obsidian-compatible folder the owner can open, read, and edit by hand.

## Definitions

- **Vault**: the knowledge folder, one per lana install, shared by all agents.
- **Source**: a raw file under `sources/` (markdown, text, PDF). lana writes a source once and never
  changes it.
- **Page**: a markdown file under `wiki/` with frontmatter. Its title is its file name without `.md`.
- **Compile pass**: a background LLM pass that turns one source into new or updated pages.

## Requirements

### Vault

- **REQ-KNW-001** The vault path is `knowledge.path`, default `$LANA_HOME/knowledge`. `~` expands.
  lana creates the vault layout on first use and never deletes a vault file.
- **REQ-KNW-002** Layout: `KNOWLEDGE.md` (conventions, seeded once from a template, then owned by
  the owner), `index.md` (generated catalog), `log.md` (append-only change log), `sources/`, `wiki/`.
  Spec 31 adds `inbox/`, `journal/`, and `reports/`.
- **REQ-KNW-003** A page has YAML frontmatter with `type` (one of `person`, `project`,
  `organization`, `place`, `topic`, `source`), `description` (one line), `aliases`, `tags`,
  `sources` (wikilinks to the sources it came from), `created`, and `updated` (dates). The body links
  other pages with `[[Title]]`. Pages written by hand or by other tools with missing or extra
  frontmatter fields are still read and indexed.
- **REQ-KNW-004** `index.md` lists every page as `- [[Title]] — description`, grouped by type, and is
  regenerated after each lana write. `log.md` gets one dated entry per lana change: which pages were
  created or updated, by which agent or pass, and from which source.

### Writing

- **REQ-KNW-010** lana changes a page only by: creating it, appending text (at the end or under a
  heading), replacing a text that occurs exactly once in the body, or updating frontmatter fields.
  It never rewrites a whole existing page and never deletes a page. A page that changed on disk
  between lana's read and write is re-read before the change is applied.
- **REQ-KNW-011** Creating a page whose title or an alias already names a page (case-insensitive)
  appends to that page instead.
- **REQ-KNW-012** A page is at most 40,000 characters after a change; a change past that fails with
  a message asking to split the page. Content that looks like a secret (same patterns as spec 20) is
  refused.
- **REQ-KNW-013** A title is 1–120 characters without `/ \ : * ? " < > | # ^ [ ]`, and does not start
  with `.`.

### Sources and compile

- **REQ-KNW-020** Ingest saves one source from a workspace file (`.md`, `.txt`, `.pdf`, `.html`),
  a URL (fetched and converted to markdown), or given text, as
  `sources/<YYYY-MM-DD> <title>.<ext>`. HTML and URLs are stored as `.md`. A source whose content
  equals an existing source is not stored twice; the existing path is returned.
- **REQ-KNW-021** Each stored source gets one compile pass in the background. The pass creates a
  `type: source` summary page for the source and creates or updates the pages for the people,
  projects, organizations, places, and topics it covers, with wikilinks between them and the
  source in their `sources` field. It follows `KNOWLEDGE.md`.
- **REQ-KNW-022** A compile pass sees the source text, `KNOWLEDGE.md`, the page catalog, and the
  full text of the existing pages most related to the source, so it updates pages instead of
  making duplicates. It applies at most 12 operations per part of the source through the same rules
  as REQ-KNW-010–013.
- **REQ-KNW-023** Compile passes run one at a time per vault, in ingest order. A source's compile
  state (`pending`, `done`, `failed`) survives a restart; pending sources compile after start.
- **REQ-KNW-024** When a compile pass started from a session ends, lana appends a
  `knowledge/compiled` event to that session with the source, the page operations, the usage, and
  an error if it failed. The event is not part of the model history.
- **REQ-KNW-025** The compile pass uses the provider and model of the agent that ingested the
  source.

### Search

- **REQ-KNW-030** lana indexes the text of pages and sources (PDF text included) in chunks. The index
  is derived data outside the vault and is rebuilt from the vault when missing. Files the owner adds,
  edits, or deletes outside lana are picked up before the next search.
- **REQ-KNW-031** Without `knowledge.embedding_provider`, search is keyword-only (BM25, title words
  weigh more). With it, search is hybrid: keyword and vector results are fused. An embedding error
  falls back to keyword search for that call.
- **REQ-KNW-032** Search returns files, best first, each with its best chunk as a snippet.

### Tools

- **REQ-KNW-040** Built-in tools in a new catalog group "Knowledge": `knowledge_search` (risk
  read), `knowledge_read` (risk read), `knowledge_write` (risk memory), `knowledge_ingest` (risk
  memory). An agent gets them only when its `tools` list names them.
- **REQ-KNW-041** `knowledge_read` reads a vault file by vault-relative path or a page by title or
  alias, with line numbers, optionally a line range; PDF sources read as extracted text.
- **REQ-KNW-042** `knowledge_ingest` returns as soon as the source is stored; the reply says the
  pages will appear after the background compile.
- **REQ-KNW-043** Tool paths never leave the vault. `knowledge_ingest` reads files only inside the
  agent workspace.
- **REQ-KNW-044** `knowledge_search` carries a prompt note: search the vault before answering about
  people, projects, or things the owner shared before; save lasting knowledge with
  `knowledge_write` or `knowledge_ingest`; keep `memory_write` for short facts about the owner and
  the agent itself.

### Recall

- **REQ-KNW-050** When `knowledge.auto_recall` is not `false` and the agent has `knowledge_search`,
  the per-turn `<context>` block lists up to 3 wiki pages relevant to the owner's message, each
  once per session, as `title (path): description`. A page qualifies when its best chunk matches at
  least 2 distinct words of the message, or, with embeddings, has cosine similarity ≥ 0.45.
  Recall never delays a turn by more than 2 seconds.

### Display

- **REQ-KNW-060** The web session view shows a `knowledge/compiled` event as a muted line under its
  turn, for example `Knowledge updated from "Rapat Q4.pdf": created Pak Budi, updated Proyek X`.

## Acceptance criteria

- **AC-KNW-001** (REQ-KNW-001–004) First use creates the layout and `KNOWLEDGE.md`; a second start
  does not overwrite an edited `KNOWLEDGE.md`; a write regenerates `index.md` and appends `log.md`.
  Evidence: `internal/knowledge` unit test.
- **AC-KNW-002** (REQ-KNW-010–013) Append, replace (exactly-once), meta update, create-on-alias
  becomes append, 40,000 limit, secret refusal, bad title, and a concurrent on-disk edit kept.
  Evidence: unit test.
- **AC-KNW-003** (REQ-KNW-020, -023) Ingest of a PDF, a text, and a duplicate text; compile state
  survives a reopen. Evidence: unit test.
- **AC-KNW-004** (REQ-KNW-021, -022, -024, -025) A fake provider returning create/append ops
  produces pages with wikilinks and `sources`, a source page, and one `knowledge/compiled` event;
  invalid JSON marks the source failed and logs the error in the event. Evidence: service test.
- **AC-KNW-005** (REQ-KNW-030–032) A file edited outside lana is found by its new words; a deleted
  file is gone; with a fake embedder, a synonym-only query finds the page; an embedder error still
  returns keyword hits. Evidence: index test.
- **AC-KNW-006** (REQ-KNW-040–044) Tool schemas, risks, catalog group, path escape refused, output
  formats. Evidence: tool tests.
- **AC-KNW-007** (REQ-KNW-050) A matching page appears once per session in `<context>`; a one-word
  overlap does not; `auto_recall = false` turns it off. Evidence: agent turn test.
- **AC-KNW-008** (REQ-KNW-060) The muted line renders. Evidence: vitest.

## Out of scope (this spec)

- Inbox folder, Telegram `/save`, journal, nightly consolidation and lint, web Knowledge page: spec 31.
- Page deletion and merging by lana: the owner does it by hand; spec 31 lint reports duplicates.
- Knowledge compile usage in the monitoring dashboard.
- Images inside sources.

## Resolved questions

| Question | Resolution |
|---|---|
| One vault or one per agent? | Owner context: one owner. Rekomendasi agent: one shared vault. |
| Where is the index? | Rekomendasi agent: `$LANA_HOME/knowledge.db`, outside the vault, so a synced vault never holds a live SQLite file. |
| Who compiles: the agent in its turn, or a background pass? | Rekomendasi agent: background pass with bounded JSON operations (evonic "sefton" style). It does not use the turn's context, and its writes go through the same safety rules. |
| Embeddings default? | Off. Owner sets `embedding_provider` (an `openai_compat` provider, for example OpenAI or a local Ollama) when wanted. |

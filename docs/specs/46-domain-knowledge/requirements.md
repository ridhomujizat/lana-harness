# Requirements: Domain knowledge per agent

- **Issue**: —
- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-10)
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (tool inputs, config key, HTTP)
- **Depends on**: spec 30 (vault, index, compile), spec 31 (journal, promotion, upkeep), spec 32
  (agent profile), spec 5/20 (agent memory, memory capture)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-10 | "agent punya kemampuan di bidangnya masing-masing … memorynya berkembang besar, ga cuman di satu file memory.md, tiap agent punya pengetahuan yang spesifik dan banyak di bidangnya sendiri" | goal |
| Owner interview 2026-11-10 | 12 answers, all recommended options (see Resolved questions) | scope and rules |
| OpenClaw docs (`concepts/memory.md`, `dreaming.md`, `multi-agent.md`, `memory-provenance.md`) | small curated `MEMORY.md`, dated notes searched not injected, score-gated promotion, per-agent memory by default, `memory-wiki` with `vault.scope=agent`, origin classes | reference design |
| Hermes Agent docs (`features/memory.md`, `features/skills.md`) | 2,200-char memory forcing curation, `skill_manage` self-learned skills, `write_approval` staging | reference design |
| Graphiti (Zep) paper arXiv 2501.13956 | facts are invalidated with a validity interval, not overwritten | temporal facts |
| MINJA arXiv 2503.03704 | memory injection through ordinary queries | provenance and review rules |

## Goal

Every agent grows its own body of knowledge in its field. What an agent learns in its
conversations lands in its own journal and becomes pages in its own domain wiki, filtered by its
job. The shared wiki stays the common ground for people, organizations, and projects. An agent
can look into another agent's domain when it needs to, but only the owning agent writes there;
others send proposals. Agents also learn procedures as skills, which the owner approves.
`MEMORY.md` shrinks back to a small hot cache.

## Definitions

- **Shared wiki**: `wiki/` of the vault (spec 30). Unchanged.
- **Domain**: the folder `agents/<name>/` of the vault, owned by agent `<name>`. It holds
  `wiki/` (domain pages) and `journal/` (the agent's journal). An agent has a domain when its
  `tools` list has a knowledge tool (spec 31 `knowledgeToolNames`).
- **Scope**: where a vault file belongs: `shared` (everything outside `agents/`) or an agent
  name (inside `agents/<name>/`).
- **Domain agent**: the agent that owns a domain.
- **Proposal**: a page change one agent asks another agent's domain to take. Stored in the index
  database, decided by the domain agent's promotion or by the owner.
- **Skill proposal**: a skill an agent wrote with `skill_write` that waits for the owner.
- **Review queue**: the list of pending proposals and skill proposals the owner can approve or
  reject.
- **Owner turn**: a root turn started by an owner message on a channel (web, Telegram, CLI),
  not a workflow, job, or delegated child turn.

## Requirements

### Vault scopes

- **REQ-DKN-001** The vault gains `agents/<name>/wiki/` and `agents/<name>/journal/`, created on
  first write for that agent. Names follow the agent name rule. lana never deletes a domain file.
  A folder under `agents/` whose name is not a configured agent stays readable and searchable as
  that scope.
- **REQ-DKN-002** Domain pages follow the same page rules as shared pages (REQ-KNW-003, -010–013).
  A page title is unique within its scope: creating a page whose title or alias already names a
  page in the same scope appends to it (REQ-KNW-011). The same title may exist in two scopes.
- **REQ-DKN-003** Each domain has its own `agents/<name>/index.md`, regenerated after each lana
  write to that domain. The vault `index.md` lists shared pages and one line per domain with its
  page count. `log.md` stays one vault-wide log; each entry names the actor and the scope.
- **REQ-DKN-004** The index records the scope of every file. Existing indexes are migrated and
  rebuilt on first open; no vault file changes during migration.

### Reading and recall

- **REQ-DKN-010** `knowledge_search` takes a scope: `mine` (default: the caller's domain and the
  shared wiki), `domain` (the caller's domain only), `shared`, `all`, or `agent:<name>`. Each hit
  names its scope.
- **REQ-DKN-011** `knowledge_read` reads any vault file by path, including another agent's
  domain. A page given by title resolves in the caller's domain first, then shared, then other
  domains in name order.
- **REQ-DKN-012** Per-turn recall (REQ-KNW-050) searches only the caller's domain and the shared
  wiki. For a team session the caller is the team lead. When a domain page and a shared page tie,
  the domain page comes first.

### Writing and proposals

- **REQ-DKN-020** `knowledge_write` takes a scope: `mine` (the caller's domain), `shared`, or
  `agent:<name>`. Without a scope, pages of type `person`, `organization`, `place`, or `source` go
  to `shared` and all others to `mine`. An agent without a domain writes to `shared` only.
- **REQ-DKN-021** A `knowledge_write` to another agent's domain does not change that domain. It
  stores a proposal holding the requested change, the proposing agent, its session and turn, and
  whether it came from an owner turn. The tool reply names the proposal id and says the domain
  agent applies it at its next promotion. A proposal to an agent without a domain, or to an
  unknown agent, is refused.
- **REQ-DKN-022** Only the domain agent's promotion (REQ-DKN-031) or the owner changes a domain.
  The owner may edit domain files by hand at any time. `knowledge_ingest` and source compile
  passes write to the shared wiki only.

### Temporal facts

- **REQ-DKN-025** `knowledge_write` and compile operations gain the action `supersede`:
  `old_text` (one line, found exactly once in the page body) is kept, struck through and dated,
  followed by `new_text` with its date and optional source. Nothing is deleted. The result reads:
  `~~<old>~~ *(until 2026-11-10)* <new> *(since 2026-11-10, [[source]])*`.
- **REQ-DKN-026** Search and recall rank only the current text: struck-through spans and their
  `until` marks are left out of the indexed text and of snippets. `knowledge_read` shows the page
  as written, history included.

### Journal and promotion

- **REQ-DKN-030** A journal pass (REQ-KNU-001) for a session whose agent has a domain appends to
  `agents/<agent>/journal/<day>.md` instead of `journal/<day>.md`. Sessions without a domain
  agent (for example team sessions) keep the shared journal. Existing shared journal files are
  still promoted into the shared wiki by the knowledge agent (REQ-KNU-021).
- **REQ-DKN-031** Domain promotion runs per domain agent, one agent at a time, with that agent's
  provider and model. It reads the agent's new journal text, its pending proposals, its `JOB.md`
  and title, and the catalog of its domain and the shared wiki. It returns page operations, each
  with a target scope (`domain` or `shared`) and a confidence from 0 to 1. lana applies an
  operation only when its confidence is at least 0.6, or at least 0.8 when it comes from a
  proposal. Facts outside the agent's job go to the shared wiki or are left out. Skipped
  operations are counted in the log entry.
- **REQ-DKN-032** Proposals that did not come from an owner turn are never promoted
  automatically: they wait in the review queue until the owner approves them. Proposal text is
  data for the model, never instructions; the promotion prompt says so.
- **REQ-DKN-033** Domain promotion runs during the nightly consolidation after the shared
  promotion and before lint, and when requested for one agent (`PromoteSoon`). A domain with no
  new journal text and no promotable proposal makes no model call. A failed promotion leaves the
  agent's marks and proposals unchanged; the next run retries. Proposals a promotion handled
  become `applied`, with the operations it made.
- **REQ-DKN-034** While `MEMORY.md` of the agent is above 6,000 characters, domain promotion also
  reads its entries and may return entry ids whose facts the same reply wrote into pages. lana
  removes those entries from `MEMORY.md` and writes their full text into the `log.md` entry, so
  nothing is lost. At most 10 entries are removed per run.

### Agent memory size

- **REQ-DKN-040** The `MEMORY.md` limit becomes 8,000 characters (was 32,000). A memory change
  that does not make the file larger is accepted even when the file is over the limit; a change
  that grows a file past the limit fails with the existing "remove or merge entries first" error.
  lana never cuts an existing file. The same rule applies to memory capture and to the owner
  saving `MEMORY.md` from the web.

### Learned skills

- **REQ-DKN-050** `skill_write` stores a skill proposal instead of changing the agent's skills,
  unless the agent's config sets `skill_write = "direct"`. The reply says the owner reviews it.
  A second proposal for the same skill name replaces the pending one. Skill proposals keep the
  existing name and size rules.
- **REQ-DKN-051** Approving a skill proposal creates or updates the agent's skill (spec 7
  `WriteSkill`); rejecting it drops it. Pending skill proposals are not visible to `skill_load`
  or the prompt.
- **REQ-DKN-052** The `skill_write` prompt note asks the agent to propose a skill after a
  multi-step procedure worked, after the owner corrected how a task should be done, or after it
  solved a problem it is likely to meet again in its field.

### Review queue

- **REQ-DKN-060** The review queue lists pending proposals (target agent, proposing agent, page
  title, change, created time, owner turn or not) and pending skill proposals (agent, skill name,
  description, body, new or update), newest first.
- **REQ-DKN-061** Approving a proposal marks it approved and requests a promotion of the target
  agent; rejecting marks it rejected. A rejected or applied item leaves the queue. Approving a
  skill proposal applies it at once.

### Web

- **REQ-DKN-070** The Knowledge page shows a page list with a scope filter (All, Shared, and one
  entry per domain with its page count), a text filter, and for each page its title, type,
  description, scope, and updated date. Opening a page shows its markdown.
- **REQ-DKN-071** The Knowledge page has a Review section with the review queue and Approve /
  Reject buttons. The Knowledge item in the sidebar shows the number of pending review items.
- **REQ-DKN-072** The agent profile shows "What it knows": domain page count, pages created or
  updated in the last 7 days, a weekly count for the last 8 weeks, and the 5 most recently
  updated domain pages, each linking to the Knowledge page. An agent without a domain shows that
  it has no knowledge tool.

## Acceptance criteria

- **AC-DKN-001** (REQ-DKN-001–004) A vault with shared pages and `agents/dana/wiki/Gaji.md` is
  indexed with scopes `shared` and `dana`; an index of schema 2 migrates without changing vault
  files; creating "Gaji" in shared while it exists in `dana` creates a second page; the domain
  `index.md` lists only domain pages. Evidence: `internal/knowledge` tests.
- **AC-DKN-002** (REQ-DKN-010–012) For agent `dana`, scope `mine` finds dana and shared pages but
  not `rio` pages; `agent:rio` finds only rio pages; recall never offers a `rio` page; a tie puts
  the domain page first; title read resolves domain before shared. Evidence: index, tool, and turn
  tests.
- **AC-DKN-003** (REQ-DKN-020–022) Default scope by type; `agent:rio` from `dana` stores a
  proposal and changes no file; a proposal to an unknown agent fails; ingest writes shared only.
  Evidence: tool and service tests.
- **AC-DKN-004** (REQ-DKN-025, -026) `supersede` writes the struck and dated form; a multi-line or
  ambiguous `old_text` fails; a search for the old text does not find the page while the new
  text does; `knowledge_read` shows both. Evidence: write and index tests.
- **AC-DKN-005** (REQ-DKN-030–034) With a fake provider: a session of `dana` journals to
  `agents/dana/journal/`; promotion for `dana` sends one request holding `JOB.md`, applies a 0.7
  domain op, skips a 0.5 op and a 0.7 proposal op, routes a `shared` op to `wiki/`, marks the
  handled proposal applied, leaves a non-owner-turn proposal pending, and makes no request when
  nothing is new; a provider error keeps marks and proposals; prune ids remove entries only above
  6,000 characters and the log holds their text. Evidence: service tests.
- **AC-DKN-006** (REQ-DKN-040) An add to a 9,000-character memory fails; a replace that shortens
  it passes; capture and web PUT follow the same rule. Evidence: agenthome, memcapture, web tests.
- **AC-DKN-007** (REQ-DKN-050–052) `skill_write` stores a proposal that `skill_load` cannot see;
  approve writes the skill; reject drops it; `skill_write = "direct"` writes at once. Evidence:
  tool and agenthome tests.
- **AC-DKN-008** (REQ-DKN-060, -061, -070–072) API responses for pages, page read, review list,
  approve, reject, and the overview knowledge block; the Knowledge page filters by scope, opens a
  page, approves an item; the profile renders "What it knows". Evidence: Go handler tests and
  vitest.

## Out of scope

- Room discussion with lead moderation, room memory (`rooms/<team>/`), and the promotion trigger
  at the end of a room discussion: spec 47, which calls `PromoteSoon` from this spec.
- Moving existing shared pages into domains (owner decision: shared stays shared).
- A vector store beyond the existing optional embeddings (no sqlite-vec).
- Telegram or CLI commands for the review queue.
- Automatic skill creation by a background pass (skills are proposed only by the agent in a turn).

## Resolved questions

| Question | Resolution |
|---|---|
| Can an agent read another agent's domain? | Owner 2026-11-10: yes, through tools; recall stays on own domain + shared. |
| Who writes a domain? | Owner 2026-11-10: the domain agent; others send proposals it processes at promotion. |
| Existing shared wiki? | Owner 2026-11-10: stays shared; domains start empty. |
| `MEMORY.md` role and size? | Owner 2026-11-10: hot cache, 8,000 characters, promoted entries pruned, no cut of existing files. |
| When does promotion run? | Owner 2026-11-10: nightly and after a room discussion (the room trigger is spec 47). |
| Learned skills? | Owner 2026-11-10: yes, staged, owner approves. |
| Changed facts? | Owner 2026-11-10: marked superseded, not overwritten. |
| Memory flush before compaction? | Owner 2026-11-10: yes. Rekomendasi agent: the spec 31 journal pass already runs right after compaction and reads the full conversation text, which compaction does not delete, so routing it to the agent journal (REQ-DKN-030) is the flush; no second pass. |
| UI? | Owner 2026-11-10: profile "What it knows", scope filter on Knowledge, one review queue. |
| Index? | Owner 2026-11-10: one `knowledge.db` with a scope column, no sqlite-vec. |
| How is "score-gated" promotion measured? | Rekomendasi agent: a model confidence per operation with fixed bars (0.6 own text, 0.8 proposals), plus the `JOB.md` filter. OpenClaw's recall-frequency score needs recall telemetry lana does not keep yet. |
| Where do proposals live? | Rekomendasi agent: a table in `knowledge.db` next to the spec 31 marks; they are state, not knowledge. Skill proposals live as files under `agents/<name>/proposed-skills/` in `$LANA_HOME`, so skills keep working without a vault. |
| Default write scope? | Rekomendasi agent: by page type (people, organizations, places, sources are shared; projects and topics go to the domain). Most cross-team facts are about people and organizations. |
| Team sessions? | Rekomendasi agent: a team session journals to the shared journal (its agent is a team, not a domain agent); recall uses the lead's domain. Spec 47 adds room memory. |

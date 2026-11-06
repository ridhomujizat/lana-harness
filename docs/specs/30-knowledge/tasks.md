# Tasks: Knowledge vault

- **Status**: Completed
- **Revision**: 1

Each task is one `dsh-coder` run. Test command for every task:
`nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; UI tasks also
`cd web/ui && npx vitest run && npm run build`.

## Wave 1

### T-001 Vault files and page writes

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean; `go test -count=1 ./...` all ok, including `internal/knowledge`, `internal/secretlike`, `agent/memcapture`. Save/dedup hashes files under `sources/` (index lookup comes with T-002).
- Files: new `internal/knowledge/{vault.go,page.go,write.go,sources.go,paths.go}`,
  `internal/knowledge/templates/KNOWLEDGE.md`, tests; new `internal/secretlike/` (+ test);
  `agent/memcapture/service.go` (use `secretlike`); `go.mod`/`go.sum` (`gopkg.in/yaml.v3` direct).
- Covers: REQ-KNW-001–004, -010–013, -020 (save and dedup by hashing files under `sources/`).
  Design §1 (`ResolvePath` only), §2.
- Acceptance: AC-KNW-001, AC-KNW-002, save/dedup part of AC-KNW-003.

## Wave 2

### T-002 Index, search, embeddings, config

- Status: Done 2026-11-01. Evidence: `gofmt -l` clean, `go vet ./...` clean, `go test -count=1 ./...` all ok; tests cover external edit/delete sync, vector synonym search, embedder-error keyword fallback, batch size 64, recall rule, PDF text, `Embed` ordering and HTTP errors, config validation.
- Files: new `internal/knowledge/{index.go,chunk.go,search.go}` + tests; `core/chat.go`
  (`Embedder`); new `providers/openai/embed.go` + test; `providers/openai/module.go`
  (`usedProviders`); `core/config.go` (`KnowledgeConfig`); `config/validate.go` + test.
- Covers: REQ-KNW-030–032, recall rule of REQ-KNW-050 (index side), API-KNW-001, API-KNW-004.
  Design §1, §3, §4.
- Acceptance: AC-KNW-005; config validation messages.

## Wave 3

### T-003 Knowledge service and compile pass

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok; `agent/knowledge` tests cover AC-KNW-003, AC-KNW-004, queue order, unknown agent. Also touched: blank import in `lana.go`, module fixtures in `config/profile_test.go`, `config/load_test.go`, `tools/mcp_manage_test.go`.
- Files: new `core/knowledge.go`; new `agent/knowledge/{module.go,service.go,compile.go}` + tests;
  `agent/registry.go` (sink `SetKnowledge`, getter); `core/event.go` (kind, payload);
  `store/store.go` (`validKinds`); `config/profile.go` (module); `core/context.go`
  (`ServiceKnowledge`).
- Covers: REQ-KNW-020–025, API-KNW-002. Design §5.
- Acceptance: AC-KNW-003 (state survives reopen), AC-KNW-004.

## Wave 4

### T-004 Tools and per-turn recall

- Status: Done 2026-11-01. Evidence: `go vet ./...` clean, `go test -count=1 ./...` all ok; tool tests (AC-KNW-006) and turn recall tests (AC-KNW-007: context once per session, one-word overlap, `auto_recall=false`, tool not offered).
- Files: new `tools/knowledge.go` + test; `tools/webfetch.go` (shared fetch helper);
  `tools/module.go`; `core/tool.go` (`ToolCtx.Knowledge`); `agent/turn.go` (ToolCtx, recall,
  `buildPrompt` parameter); `internal/agenthome/prompt.go` (`RelevantKnowledge`); `agent/loopguard.go`;
  `lana.go` (catalog); tests.
- Covers: REQ-KNW-040–044, -050, API-KNW-003. Design §6, §7.
- Acceptance: AC-KNW-006, AC-KNW-007.

### T-005 Web display

- Status: Done 2026-11-01. Evidence: vitest 27 files / 181 tests pass, `npm run build` ok, `web/dist` rebuilt. Note: run vitest with `NODE_ENV` unset.
- Files: `web/ui/src/views/turn.tsx`, `web/ui/src/lib/format.ts`, tests, `web/dist`.
- Covers: REQ-KNW-060. Design §8.
- Acceptance: AC-KNW-008.

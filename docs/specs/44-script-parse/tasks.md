# Tasks: Script Parse node

- **Status**: Completed 2026-11-09 · branch `feature/script-parse` (worktree `../lana-harness-script-parse`)
- **Requirements**: [requirements.md](requirements.md) · **Design**: [design.md](design.md)

Go slices went through `dsh-coder` (implementer Command Code), one per call; the UI was coded directly.

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-SP-01 | `internal/scriptrt`: `Compile`, `Run`, limits, `console.log`, `html()` (goquery + cascadia for selector errors); goja and goquery in `go.mod` (MVS raised `golang.org/x/{crypto,net,text}`) | REQ-SP-002, AC-SP-001 | `go test ./internal/scriptrt` (globals undefined, timeout, cancel, size caps, html helper) |
| T-SP-02 | `scriptcheck.go`: `CheckContract`, `Agreement`, drift codes | REQ-SP-003 | table tests |
| T-SP-03 | Catalog, validation (`bad_schema`, `bad_script`, `unknown_agent`), `TemplateParams` keeps `script` unrendered, `LockToolsOf`; `NodeRun.Via`; migration 0010 + store | REQ-SP-001, 013, 014, 019, AC-SP-011 | `go test ./internal/workflow ./store` (v9→v10 migration, via round trip) |
| T-SP-04 | `script_prompts.go`: fallback prompt, script prompt (generate, regenerate, convert), reply parser; later `SampleExcerpt` and the readable-code rule | REQ-SP-005, 007, 017, API-SP-008 | prompt tests |
| T-SP-05 | `engine_script.go`: script → check → fallback → regenerate → save version → decision → notice; pause after 3 rejections, per-run override, revert in `Answer` | REQ-SP-004..012, AC-SP-002..007 | engine tests with fake deps |
| T-SP-06 | `service_script.go` + `web/workflows_script.go`: test, generate, convert (with `normalizeConvertInput`), suggestions, `script_stats` with events | REQ-SP-015..019, API-SP-002..007, 009, AC-SP-008..010 | `go test ./internal/workflow ./web` |
| T-SP-07 | Assistant rules; GitHub trending template (web) | REQ-SP-020 | assistant prompt test |
| T-SP-08 | UI: catalog entry, Script tab, convert banner and dialog, canvas "Script?" pill, run `via` badge, script decision card with Revert, API client | REQ-SP-021 | vitest 53 files / 483 tests, `tsc --noEmit`, eslint, dashboard rebuilt |
| T-SP-09 | Live check on a temporary daemon (127.0.0.1:18444, DeepSeek `deepseek-flash`, Telegram off) | AC-SP-002, 003, 007, 008, 009, 010 | see below |
| T-SP-10 | Docs: index row, spec 33 contract row, ADR 0021, `CHANGELOG.md` Unreleased | — | — |

Final gates: `gofmt` clean, `go vet ./...`, `golangci-lint run ./...` 0 issues, `go test -race ./...` ok.

## Live check (T-SP-09)

Workflow `trending-digest`: manual trigger → web fetch `github.com/trending` (58 KB markdown) → Script Parse (empty script).

| Run | What happened | Node tokens |
|---|---|---|
| 1 | Fallback ok; regeneration rejected twice (`empty_drift`) because the prompt showed only the first 16 KiB of navigation. Fixed with `SampleExcerpt`. | 74,278 |
| 2 | Fallback ok; script rev 1 learned, agreement 100%, saved as version 2 (author guardian), `script_update` decision | 76,264 |
| 3 | `via=script`, 1 ms | 0 |
| 4 | Mild drift (headings rewritten): the defensive script still found every repo but produced a wrong description and 0 stars. The contract passed, so this went undetected (see follow-ups). | 0 |
| 5 | Heavy drift (list rewritten as a table): `empty_drift` → fallback → rev 2 learned, agreement 97%, version 4 | 61,909 |
| 6 | `via=script` on rev 2 | 0 |

Revert of the rev 2 update saved version 5 (author owner, rev 3 = rev 1 source); a second revert
answered 409 `decision_resolved`. `script_stats`: 3 script runs, 3 fallbacks, 62k tokens saved.

Workflow `hn-top` (agent step with `output_schema`): 3 runs at ~6.4k tokens each → suggestion listed
(3 runs, avg 6,380). Convert: feasible, checks 3/3 at agreement 1.0, input `{{ nodes.fetch.output.markdown }}`,
31-line readable script, 10k tokens. After replacing the node, run 4: `via=script`, 0 tokens.

Test endpoint: a hand-written trending script passed in 1 ms on run 1's sample; a JSON-string input
shaped like an MCP Jira answer parsed with `JSON.parse` (AC-SP-008).

Screenshots: [Script tab](evidence/script-tab-live.png), [convert banner and pill](evidence/convert-banner-live.png),
[run detail at 390 px](evidence/run-fallback-390.png).

## Follow-ups

- **Silent drift.** Run 4 shows that a script can degrade while the contract still passes. Candidates:
  enforce simple notes in schema types (`integer >0`, `string url`), or compare an output fingerprint
  (share of zero numbers, repeated prefixes) with the last good runs.
- Regeneration costs about two fallbacks (≈25k tokens per guardian call on a 58 KB page). The fallback
  input could also use `SampleExcerpt`-style trimming when the expected region is known.
- `est_tokens_30d` is the tokens spent in the last 30 days, not a projection; the UI says so.

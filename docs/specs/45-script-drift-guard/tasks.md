# Tasks: Script drift guard

- **Status**: Completed 2026-11-09 · branch `feature/script-drift-guard` (worktree `../lana-harness-script-parse`)
- **Requirements**: [requirements.md](requirements.md) · **Design**: [design.md](design.md)

Go slices go through `dsh-coder`, one per call; the UI is coded directly.

| ID | Slice | Covers | Status |
|---|---|---|---|
| T-DG-01 | `schemarules.go`: `ParseTypeSpec`, `RuleViolations`, `CheckRules`, `RuleProblems`, `DropRules`, `MergeRules`, `KeepHoldingRules`; `bad_rule` and `audit_every` validation | REQ-DG-003, 004 | done |
| T-DG-02 | `scriptprofile.go`: `CheckProfile` with the REQ-DG-002 signals; live fixture test | REQ-DG-001, 002, AC-DG-001 | done: runs 4 and 8 fire `copy` (9/9 descriptions contain the name, baseline 0/45) |
| T-DG-03 | Engine: rules and profile after the contract, baseline reader, confirmation (`profile_accept`, `rule_drop`), audit with `via=audit`, rules on candidates | REQ-DG-005, 007, AC-DG-003..006 | done; T-DG-04b: an audit whose fallback errors keeps the script output |
| T-DG-04 | Prompts and service: `rules` in the script reply, kept rules on Generate/Convert, rule and profile problems on Test, `audit_runs`, assistant rules | REQ-DG-006, 008, 010 | done; T-DG-04b: profile problems on Test |
| T-DG-05 | UI: drift labels, `profile_accept`/`rule_drop` events, Audit field, `audit` via badge, Generate applies returned schemas, rule help | REQ-DG-009 | done (vitest) |
| T-DG-07 | Revision 2: per-output vote and medians in `scriptprofile.go` | REQ-DG-011, AC-DG-007 | done |
| T-DG-06 | Live check on the scratch daemon; docs, CHANGELOG | AC-DG-002 | done, see below |

## Baseline (before the fix)

Live run 7 on today's `github.com/trending` (9 repos on the page): `via=script`, 0 tokens, 3 ms,
correct output. Run 8 with the headings rewritten (`### Repo: owner/name` + `Link:` line, `+N today`):
`via=script`, 0 tokens, 9/9 `stars_today` = 0 and 9/9 descriptions = `Link: <repo url>`. The contract
passed. Outputs of runs 2–8 are in `internal/workflow/testdata/trending_profile.json`.

Final gates: `gofmt` clean, `go vet ./...`, `golangci-lint run ./...` 0 issues, `go test -race ./...` ok,
vitest 57 files / 550 tests, `tsc --noEmit`, `make web-check`; dashboard rebuilt.

## Live check (T-DG-06)

New workflow `trending-guard` (same graph and script rev 3 as `trending-digest`, clean history) on the
scratch daemon, DeepSeek `deepseek-flash`.

| Run | Input | What happened | Node tokens |
|---|---|---|---|
| 1–3 | live `github.com/trending` | `via=script`; runs 3 had a baseline of 2 and no false alarm | 0 |
| 4 | headings rewritten (run 8 case) | `profile_drift`: `field "repos.description": copy: 9/9 contain "repos.name", baseline 0/27` → fallback (23k) → rev 4 learned, agreement 100% | 64,956 |
| 5 | same | `via=script` rev 4, stars `[2373, 345, 1439, …]`, real descriptions | 0 |
| 6 | page cut to 2 repos (real data change) | `profile_drift` (digest length) → fallback agrees 1.00 → `profile_accept`, no rewrite, no notice | 20,469 |
| 7 | same | `via=script` | 0 |
| 8 | `stars_today: "integer >0"`, one repo at `+0 today` | `rule_drift` → fallback agrees → version 5 by guardian drops `>0`, `rule_drop` decision, notice | 20,542 |
| 9 | same, `audit_every: 2` | `via=script` | 0 |
| 10 | same | `via=audit`, agreement 1.00 | 21,153 |

`script_stats`: 6 script runs, 3 fallbacks, 1 audit.

## Live check, revision 2 (T-DG-07)

Workflow `trending-digest` keeps the wrong outputs of runs 4 and 8 in its history. With the pooled
baseline (revision 1 build), the mild drift page passed again: run 9 `via=script`, 9/9 stars 0,
descriptions `Link: …`. With the vote (baseline runs 9, 8 wrong and 7, 6, 5 right):

| Run | What happened | Node tokens |
|---|---|---|
| 10 | `profile_drift`: `field "repos.description": copy: 9/9 contain "repos.name", baseline normal in 3/5 runs` → fallback → rev 4 learned, agreement 97%, stars `[2373, 345, …]` | 68,955 |
| 11 | `via=script` rev 4 | 0 |

## Follow-ups

- A baseline with 3 or more wrong outputs of 5 still silences a signal. That needs at least three
  undetected runs in a row before the guard existed; a new node never gets there.
- An audit on a 58 KB page costs about 21k tokens; `audit_every` stays off by default.

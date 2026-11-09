# Requirements: Script drift guard

- **Issue**: — (follow-up of spec 44, owner request 2026-11-09)
- **Status**: Approved (owner: "ikuti rekomendasi mu", 2026-11-09)
- **Revision**: 2 (2026-11-09: per-output vote, REQ-DG-011; owner: "kerjakan baseline tercemar")
- **Owner**: ridho
- **Contract**: in this file (API-DG-001..004) — Required
- **Related spec**: [44 Script Parse](../44-script-parse/requirements.md) (amends REQ-SP-003, REQ-SP-005..011, API-SP-002..004, API-SP-009)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Spec 44 live check | Run 4: a script found every repo but returned `stars_today: 0` and a wrong `description`; the contract passed | the problem |
| Live check 2026-11-09 | Run 8 on today's `github.com/trending` with headings rewritten: 9/9 `stars_today` are 0 and 9/9 descriptions are `Link: <repo url>`; `via=script`, 0 tokens | the main test case (`internal/workflow/testdata/trending_profile.json`) |
| Owner 2026-11-09 | "ikuti rekomendasi mu": layers 1 + 2, layer 3 built but off by default; a confirmed profile change updates silently; the guardian adds rules automatically; audit by a run count | every decision below |

## Goal

A Script Parse node must notice when its script still returns well-typed output that is wrong. It
must do so without new token cost on normal runs. A false alarm may cost one fallback, never a loop
of fallbacks.

## Definitions

- **Good output** — the output of a succeeded node run of this node with `via` `script`, `fallback`
  or `audit`.
- **Baseline** — up to 5 newest good outputs from the newest 30 runs, from runs numbered at or after
  the newest `profile_accept` decision of the node. A baseline needs at least 2 outputs.
- **Field path** — a top-level `output_schema` field `f`, or `f.k` for field `k` of the elements of
  an `item_schema` array `f`.
- **Soft drift** — `profile_drift` or `rule_drift`. A soft drift can be a false alarm. The fallback
  output decides.
- **Confirmed** — the fallback output agrees with the script output: agreement ≥ 0.8 with at least 5
  expected leaves, or 1.0 with fewer (the spec 44 rule).

## Requirements

| ID | Requirement |
|---|---|
| REQ-DG-001 | After a script output passes the spec 44 contract, the engine checks it against the node's baseline profile. A strong change of any signal of REQ-DG-002 is drift `profile_drift`. Without a baseline the check is skipped. |
| REQ-DG-002 | Profile signals, per field path (item fields use all elements of all baseline outputs; "values" means the current output's values, and a signal needs at least 3 of them unless stated): **zeros** — a number field whose baseline share of 0 or null is ≤ 0.2 has a current share ≥ 0.8 (a top-level number: current 0, baseline never 0 over ≥ 3 outputs); **constant** — a string field whose baseline outputs each have ≥ 3 non-empty values with a distinct ratio ≥ 0.5 has a current most-common value share ≥ 0.8; **length** — a string field with a baseline mean length ≥ 8 has a current mean length < 0.25× or > 4× of it (top-level strings count as one value); **copy** — the current share of elements whose field `a` contains the value of another string item field `b` (value length ≥ 8) is ≥ 0.8 while the baseline share is ≤ 0.2; **shape** — a string field whose baseline dominant class (url, number, text) share is ≥ 0.9 has a current share of that class < 0.5; **blank** — an `allow_empty` string field whose baseline empty share is ≤ 0.2 has a current empty share ≥ 0.8; **count** — an item array whose baseline median length is ≥ 5 has a current length < 0.3× of it. The drift message names the field path, the signal and both shares. |
| REQ-DG-011 | Revision 2, the baseline vote: wrong outputs that passed before the guard existed may sit in the baseline. Every share-based signal (zeros, constant, copy, shape, blank) is computed per baseline output, and an output is *normal* for the signal when its own share is in the normal range of REQ-DG-002 (zeros ≤ 0.2, copy ≤ 0.2, blank ≤ 0.2, constant: distinct ratio ≥ 0.5 with ≥ 3 values, shape: share of the dominant class ≥ 0.9, the dominant class chosen by majority over the outputs). The signal fires when the current output is extreme and more than half of the baseline outputs are normal. Length and count use the median of the per-output means and lengths. A top-level number fires on 0 when more than half of ≥ 3 baseline outputs are non-zero. With 5 outputs, up to 2 bad outputs cannot silence a signal. The message names the vote, e.g. `baseline normal in 3/5 runs`. |
| REQ-DG-003 | A type string in `output_schema` or `item_schema` may carry rule tokens right after the type: `>0`, `>=0` (integer, number), `url` (string: `^https?://\S+$`), `min:N`, `max:N` (string: length in runes; integer or number: value; array type: length). Tokens are read until the first word that is not a rule; the rest stays the free note. A rule on a type it does not fit is the validation issue `bad_rule`. Spec 33 nodes read the same strings as before: the first word is the type. |
| REQ-DG-004 | Rules are checked on script outputs and on regeneration candidates, never on fallback or agent outputs. An empty string in a field allowed by `allow_empty` skips its rules. A failed rule is drift `rule_drift`. |
| REQ-DG-005 | On a soft drift the run continues through the fallback as in spec 44. When the fallback output confirms the script output, the drift was a false alarm: the node output is the fallback output, there is no regeneration and no notice. A confirmed `profile_drift` records decision kind `script`, action `profile_accept`, status `applied`, so the baseline restarts at this run. A confirmed `rule_drift` drops the violated rule tokens from the schema in a new version (author guardian), records action `rule_drop`, and notifies the owner. When the fallback does not confirm, the spec 44 drift path runs (regeneration, `script_update` or `script_fallback`, notice). |
| REQ-DG-006 | When the guardian generates or converts a script (API-SP-003, API-SP-004) it may propose rules. The service keeps a rule only when it holds on the expected output and on the script output, and returns the schema with the kept rules merged in. Regeneration in a run never changes rules. |
| REQ-DG-007 | A node param `audit_every` (integer 0..1000, default 0 = off) makes every Nth script run an audit: after the script passes every check, the fallback also runs and is compared with the script output. N counts the node's `via=script` runs since its newest run with `via` `fallback` or `audit`. A confirmed audit keeps the script output, sets `via` to `audit`, and adds the fallback tokens. An unconfirmed audit is drift `audit_drift` and takes the spec 44 drift path with the fallback output. |
| REQ-DG-008 | The test endpoint (API-SP-002) also reports rule and profile problems. The profile uses the baseline of the workflow's stored runs. |
| REQ-DG-009 | The UI shows the new drift codes, the `profile_accept` and `rule_drop` events, an "Audit every N runs" field with its cost note, the `audit` via badge, and the rule tokens in the schema help. |
| REQ-DG-010 | The assistant knows the rule tokens and `audit_every` (spec 44 REQ-SP-020 rules). |

## Acceptance criteria

| ID | Criterion |
|---|---|
| AC-DG-001 | With the five good trending outputs as baseline, both silent-drift outputs (runs 4 and 8) are `profile_drift`; each good output checked against the other four passes. |
| AC-DG-002 | On the live scratch daemon, run 8's mild drift goes `profile_drift` → fallback → not confirmed → regeneration; the next mild-drift run is `via=script` with stars > 0. |
| AC-DG-003 | A profile drift that the fallback confirms records `profile_accept`, sends no notice, and the next run with the same data is `via=script`. |
| AC-DG-004 | `"integer >0 stars today"` fails a 0 with `rule_drift`; a rule confirmed wrong is dropped in a new version with a `rule_drop` decision and a notice. |
| AC-DG-005 | `audit_every: 3` makes the third script run an audit; a confirmed audit is `via=audit`, and the count restarts. |
| AC-DG-007 | With a baseline of 3 good and 2 silent-drift trending outputs, both silent-drift outputs are `profile_drift`, and a good output passes. On the live `trending-digest` (runs 4 and 8 in its history), the mild drift page is caught. |
| AC-DG-006 | A spec 44 workflow without rules or `audit_every` behaves as before on runs that have no baseline. |

## API contract

| ID | Change |
|---|---|
| API-DG-001 | `script_parse` params: `audit_every` (integer, optional). Type strings may carry rule tokens (REQ-DG-003). |
| API-DG-002 | Drift codes `profile_drift`, `rule_drift`, `audit_drift`; decision actions `profile_accept`, `rule_drop` (kind `script`, status `applied`). `node_run.via` may be `audit`. |
| API-DG-003 | `GenerateResult` gains `output_schema` and `item_schema` (present when rules were kept); the converted node of `ConvertResult` carries kept rules in its schema. |
| API-DG-004 | `script_stats` gains `audit_runs`. |

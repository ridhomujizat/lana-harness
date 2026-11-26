# Spec 79: Command Code usage on the Router Limits page

Status: draft (2026-11-26) · Branch: feature/commandcode-usage (from development fa37ebd)

## Context

The Router Limits (Quota) page shows DeepSeek's balance and OpenRouter's credit through the optional
`router.BalanceFetcher` adapter interface (REQ-RTR-012). A provider of kind `commandcode` (Command Code,
commandcode.ai, spec 51) implements none, so its card says "Not reported by this provider". The official
`command-code` CLI (1.93.3) reads the plan usage for its `/usage` view from four GET endpoints on the same
API base, with the same bearer key and identity headers as generation. Lana reads the same endpoints, the
same way, and shows the plan, the credit, and the 5-hour and weekly windows on the card.

## Requirements

### REQ-CCU-001: Usage fetch

The commandcode adapter is a `router.BalanceFetcher`. `FetchBalance` sends, on the provider's base URL
(default `https://api.commandcode.ai`) with the bearer key and the CLI identity headers of `newRequest`:

1. `GET /alpha/whoami?limits=1`; the org id is `org.id` (omitted from the later calls when null).
2. In parallel, `GET /alpha/billing/credits?orgId=<id>` and `GET /alpha/billing/subscriptions?orgId=<id>`.
3. `GET /alpha/usage/summary?orgId=<id>&since=<subscription currentPeriodStart>` (`since` omitted when the
   subscription has no period start).

A failed whoami or credits call fails the fetch. A failed subscription or summary call leaves its part out.
Every field is optional; numbers may come as JSON numbers or numeric strings. HTTP 401/403 fails with
"Command Code key rejected (401)" (or 403); other failures are short ("Command Code usage: HTTP 500") and
never hold the key. The router calls the fetch at most every 5 minutes per provider, with the pool's first
key, as for DeepSeek and OpenRouter.

### REQ-CCU-002: Credit

With the CLI's math, in USD: monthly, purchased, and free are the credits' `monthlyCredits`,
`purchasedCredits`, `freeCredits` (each at least 0); remaining = monthly + purchased + free; spent = the
summary's `totalCost` (at least 0). For an `active` subscription whose plan id (lowercased, `_` read as `-`)
starts with a known plan prefix (longest match: individual-go 10, individual-go-v1 10, individual-goat 70,
individual-pro 30, individual-pro-v1 80, individual-provider 15, individual-max 150, individual-ultra 300,
teams-pro 40), pool = max(plan credits, monthly) + purchased + free; otherwise pool = spent + remaining.
When pool > 0 the update's credit is `{limit_usd: pool, used_usd: pool − remaining, remaining_usd:
remaining}`.

### REQ-CCU-003: Windows

`windowLimits.fiveHour` and `windowLimits.weekly`, when present with `cap` > 0, become the windows
`five_hour` and `seven_day`: utilization = used / cap, status `rejected` when used ≥ cap else `allowed`,
`resets_at` from `resetAt` (epoch milliseconds). The update always carries a window list (empty when
`windowLimits` is null), so stale windows clear.

### REQ-CCU-004: Plan

Limit snapshots and updates get an optional `plan` (`name`, `status`, `renews_at`); an update without one
keeps the stored plan, and it is persisted with the other limits. For Command Code: name = the plan's display
name (Go, GOAT, Pro, Provider, Max, Ultra, Teams Pro) else the raw plan id (subscription, else credits);
status = the subscription status; `renews_at` = `currentPeriodEnd`. `GET /v1/router/limits` returns it.

### REQ-CCU-005: Limit card

The Router limit card (`LimitCard`) shows, when reported: a plan line ("Pro plan · renews in 12 days"; a
status other than active is named, a cancelled plan "ends"), the credit meter ("$20.00 of $87.50 left"), and
one meter per window with the same names, text, and tones as the Claude Code account cards ("5-hour window",
"75 % used · resets in 1h 30m"). A plan alone counts as a report.

## Acceptance

- AC-CCU-001: with an httptest server, `FetchBalance` calls the four paths with `limits=1`, `orgId`, `since`,
  and the bearer key, and maps credit, windows, and plan as above.
- AC-CCU-002: without an org id no call carries `orgId`; a failed summary and subscription still give the
  credit; `windowLimits: null` gives an empty window list; 401 gives "Command Code key rejected (401)" without
  the key.
- AC-CCU-003: a Command Code card renders the plan line, credit, and both window meters (vitest).
- `go test ./...`, `golangci-lint run ./...`, web tests and `tsc --noEmit` green.

## Out of scope

Workbench and quota views other than the Router Limits page (and a provider's own page, which reuses the
card); per-key usage of key pools (the fetch uses the pool's first key, as the DeepSeek and OpenRouter fetch
does); org limits, sandbox minutes, and the summary's call count.

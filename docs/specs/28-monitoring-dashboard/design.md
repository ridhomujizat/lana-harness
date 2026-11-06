# Design: Monitoring dashboard

- **Requirements**: revision 1 · **Contract**: revision 1
- **Status**: Approved

## 1. Data path

```text
browser #/home ──GET /v1/dashboard──▶ web.Server.handleDashboard
                                         │ DashboardBackend (optional port, like JobBackend)
                                         ▼
                              cmd/lana webBackend.Dashboard
                               │                         │
                 lana.Runtime.ScanEvents(kinds, since)   logring.Buffer.Since(t)
                               │                         ▲
                 store.Store (SQL, kind+created_at idx)  slog handler tee in cmd/lana/daemon.go
                               ▼
                   internal/stats.Aggregate(...) → stats.Dashboard (JSON shape of API-DSH-001)
```

## 2. Store scan (`core`, `store`, `store/memstore`, `lana.go`)

- `core/session.go`: new optional extension next to `LeaseProbe`:

  ```go
  // EventScanner is an optional SessionStore extension that reads events of the given kinds,
  // across all sessions, created at or after sinceMS, in created_at order.
  type EventScanner interface {
      ScanEvents(ctx context.Context, kinds []string, sinceMS int64) iter.Seq2[ScannedEvent, error]
  }
  type ScannedEvent struct {
      SessionEvent
      Agent string // sessions.agent
  }
  ```

- `store/store.go`: `SELECT e.session_id, e.seq, e.kind, <payload>, COALESCE(e.turn_id,''), e.created_at, s.agent
  FROM session_events e JOIN sessions s ON s.id = e.session_id WHERE e.kind IN (…) AND e.created_at >= ?
  ORDER BY e.created_at, e.session_id, e.seq`. For `tool/result`, select a small payload instead of
  the full output: `json_object('call_id', json_extract(payload,'$.call_id'), 'status',
  json_extract(payload,'$.status'), 'output', substr(json_extract(payload,'$.output'),1,300))`.
  Other kinds return the payload as stored.
- `store/memstore/memstore.go`: the same, by looping its maps (tests only).
- `lana.go`: `func (r *Runtime) ScanEvents(ctx, kinds, sinceMS)` that type-asserts the store to
  `core.EventScanner` and returns `ErrUnsupported`-style error otherwise. Also expose a session
  status count if `Runtime` has no list method that fits (`List` with no filter is fine).

Kinds scanned: `step/start`, `step/end`, `turn/start`, `turn/end`, `tool/call`, `tool/result`,
`assistant/attempt`, `compaction/summary`, `memory/captured`.

## 3. `internal/stats` (new, pure)

```go
type Range struct{ From, To, BucketMS int64 }
func ParseRange(name string, now time.Time) (Range, error) // "24h" | "7d" | "30d"
func Aggregate(r Range, events []core.ScannedEvent, logs []logring.Record) Dashboard
```

- `Dashboard` and its row types carry the JSON tags of API-DSH-001; `web` encodes it directly.
- Tool time: map `session_id+call_id → tool/call.created_at`; on `tool/result`, sample the delta.
  A `tool/call` before `from` with a result inside the range is skipped (no start time).
- Turn time: `turn/start` → `turn/end` by `session_id+turn_id`.
- Model for `step/end`: use its own `provider`/`model` fields.
- Agent per turn: count `turn/end` per agent; tokens per agent from `step/end`.
- Percentile: nearest rank on a sorted copy.
- Error rows: build from events and log records, sort newest first, cut to 100.
- `now` is filled by the backend, not by `Aggregate`.

## 4. `internal/logring` (new)

```go
type Record struct { At int64; Level slog.Level; Message string; Attrs string }
type Buffer struct { … }               // fixed-size ring, mutex, size 500
func New(size int) *Buffer
func (b *Buffer) Since(ms int64) []Record // oldest first
func Handler(next slog.Handler, b *Buffer) slog.Handler // tees records >= WARN into b, then calls next
```

`Handler` must implement `Enabled`, `Handle`, `WithAttrs`, `WithGroup` and keep the attrs from
`WithAttrs` in the recorded `Attrs` string (`key=value` joined by spaces, groups as `group.key`).

## 5. Wiring (`cmd/lana`)

- `cmd/lana/daemon.go`: create one `logring.Buffer` at daemon start; wrap the stderr text handler
  with `logring.Handler` before `slog.SetDefault`; pass the buffer to the web backend.
- `cmd/lana/web.go`: `webBackend` gains the buffer and implements
  `web.DashboardBackend.Dashboard(ctx, rangeName string) (stats.Dashboard, error)`:
  parse range, scan events since `from`, read logs since `from`, aggregate, fill `now` from
  session statuses and `daemon_started_at` from `startedAt`.

## 6. HTTP (`web/dashboard.go`)

- `type DashboardBackend interface { Dashboard(ctx context.Context, rangeName string) (stats.Dashboard, error) }`
- `SetBackend` installs it by type assertion (same pattern as `JobBackend`).
- `registerDashboardRoutes()` adds `s.Handle("GET /v1/dashboard", …)`; call it from `routes()` in
  `web/server.go`. No backend port → `501`/existing "not available" error style used by other
  optional ports. Bad range → `400 invalid_request`.

## 7. Web UI

| File | Role |
|---|---|
| new `web/ui/src/api/dashboard.ts` | types of API-DSH-001 and `getDashboard(range)` using `api/client.ts` |
| `web/ui/src/views/home.tsx` | `HomePage` becomes the dashboard (keep the export name) |
| new `web/ui/src/styles/dashboard.css` | imported from `home.tsx`, not `main.tsx` |
| `web/ui/src/app/Sidebar.tsx` | label `Dashboard` for the `home` item |
| `web/ui/src/views/home.test.tsx` | AC-DSH-004–006 |

Layout, top to bottom, single column up to 1200 px wide:

1. Header: "Dashboard", range segmented control, "Updated 14:02", Refresh.
2. Right now: two inline counts with links.
3. Four stat tiles (REQ-DSH-003): big number, one muted sub-line. No icons, no sparkline.
4. Token chart (REQ-DSH-004): inline SVG stacked bars, y axis with 3–4 ticks (`12k`, `1.2M`), x
   labels every few buckets, legend, hover tooltip; a "Show as table" disclosure.
5. Two-column row: by model, by agent; then by source (REQ-DSH-005).
6. Tools table (REQ-DSH-006); marked rows use the danger text color, not a filled background.
7. Turn outcomes (REQ-DSH-007).
8. Error log (REQ-DSH-008) with filter chips; message truncated to one line, expands on click.

Style: existing tokens only, tabular numbers, thin borders, three chart colors from the existing
palette with enough contrast in light and dark themes. No gradients, no animation, no emoji.
Plain words for labels ("Cached", "Tool time", "Failed"), per the non-technical-user rule.

## 8. Verification

Go: `nix develop -c go build ./... && nix develop -c go vet ./... && nix develop -c sh -c 'CGO_ENABLED=1 go test -race ./internal/stats/... ./internal/logring/... ./store/... ./web/... ./cmd/lana/... .'`
and `nix develop -c golangci-lint run ./...`. UI: `cd web/ui && npm run typecheck && npm test && npm run lint`.
Do not rebuild `web/dist` in the tasks.

## Visual architecture check

New read path across store → runtime → web; drawn in §1. It is local to this feature, so it stays
here.

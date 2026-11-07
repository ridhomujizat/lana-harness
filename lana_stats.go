package lana

import (
	"context"
	"log/slog"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/stats"
	"github.com/ridhomujizat/lana-harness/tools"
)

// usageStatsSource adapts the runtime's event scan to the usage_stats tool.
type usageStatsSource struct{ rt *Runtime }

// Dashboard aggregates the scanned session events in rangeName. It has no
// daemon log ring, so the daemon section stays empty.
func (s usageStatsSource) Dashboard(ctx context.Context, rangeName, agent string) (stats.Dashboard, error) {
	r, err := stats.ParseRange(rangeName, time.Now())
	if err != nil {
		return stats.Dashboard{}, err
	}
	events := make([]core.ScannedEvent, 0)
	for event, scanErr := range s.rt.ScanEvents(ctx, stats.ScannedKinds, r.From) {
		if scanErr != nil {
			return stats.Dashboard{}, scanErr
		}
		events = append(events, event)
	}
	dashboard := stats.Aggregate(r, agent, events, nil)
	dashboard.Range = rangeName
	return dashboard, nil
}

// registerUsageStatsTool registers the self-observability tool.
func (rt *Runtime) registerUsageStatsTool() {
	if rt == nil || rt.coreCtx == nil || rt.coreCtx.Tools == nil {
		return
	}
	if err := rt.coreCtx.Tools.Register(tools.NewUsageStats(usageStatsSource{rt: rt})); err != nil {
		slog.Warn("usage stats: register tool", "error", err)
	}
}

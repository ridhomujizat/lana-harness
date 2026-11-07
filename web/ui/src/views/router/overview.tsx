/* eslint-disable react-hooks/set-state-in-effect -- each tab loads its data on mount and on a filter change, like the other pages. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  CLAUDE_AGENT_PROVIDER,
  getClaudeAgent,
  getRouter,
  getRouterLimits,
  getRouterUsage,
} from "../../api/router";
import type {
  ClaudeAgentStatus,
  LimitSnapshot,
  RouterCooling,
  RouterRange,
  RouterUsage,
  UsageStats,
} from "../../api/router";
import { Badge, Button, EmptyState, ErrorState, Panel, Seg, Stat } from "../../ui/gr8r";
import { ShareBars } from "../home-charts";
import { ClaudeAgentCard, LimitCard } from "./limits";
import { UsageChart } from "./usage-chart";
import {
  compact,
  costText,
  count,
  ms,
  percent,
  sourceLabel,
  timeLabel,
  failureOf,
} from "./router-common";

/* Router → Overview (REQ-RTR-011, REQ-RTR-012, REQ-CCR-008): what the
   router did in the chosen range, what it cost, and how much room each
   provider has left. */

export const RANGES: Array<{ value: RouterRange; label: string }> = [
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

function CostValue({ stats }: { stats: UsageStats }) {
  return (
    <span className="rt-cost">
      {costText(stats.cost_usd, stats.cost_partial)}
      {stats.cost_partial ? (
        <Badge tone="amber" className="rt-partial" title="Some calls used a model with no known price.">
          partial
        </Badge>
      ) : null}
    </span>
  );
}

function StatsTable({
  rows,
  head,
  empty,
}: {
  rows: Array<{ key: string; label: string; stats: UsageStats }>;
  head: string;
  empty: string;
}) {
  if (rows.length === 0) {
    return <EmptyState size="sm" icon="table" text={empty} />;
  }
  return (
    <div className="home-table-wrap">
      <table className="perm-t home-table rt-usage-table">
        <thead>
          <tr>
            <th scope="col">{head}</th>
            <th scope="col" className="is-num">Calls</th>
            <th scope="col" className="is-num">Errors</th>
            <th scope="col" className="is-num">Input</th>
            <th scope="col" className="is-num">Output</th>
            <th scope="col" className="is-num">Cached</th>
            <th scope="col" className="is-num">Cost</th>
            <th scope="col" className="is-num">TTFT p50</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.key}>
              <td className="mono rt-cell-name" title={row.label}>{row.label}</td>
              <td className="is-num num">{count(row.stats.calls)}</td>
              <td className={"is-num num" + (row.stats.errors ? " rt-bad" : "")}>
                {count(row.stats.errors)}
              </td>
              <td className="is-num num">{compact(row.stats.input_tokens)}</td>
              <td className="is-num num">{compact(row.stats.output_tokens)}</td>
              <td className="is-num num">{compact(row.stats.cached_tokens)}</td>
              <td className="is-num num">
                <CostValue stats={row.stats} />
              </td>
              <td className="is-num num">{ms(row.stats.ttft_p50_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Cooling({ rows }: { rows: RouterCooling[] }) {
  if (rows.length === 0) {
    return null;
  }
  return (
    <Panel title="Cooling down" className="rt-cooling">
      <p className="home-note">
        These models failed recently. The router skips them until the time shown, then tries
        them again.
      </p>
      <ul className="rt-cooling-list">
        {rows.map((row) => (
          <li key={row.target}>
            <span className="mono trunc">{row.target}</span>
            <span className="num muted">until {timeLabel(row.until)}</span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

export function RouterOverview() {
  const [range, setRange] = useState<RouterRange>("24h");
  const [usage, setUsage] = useState<RouterUsage | null>(null);
  const [usageError, setUsageError] = useState("");
  const [limits, setLimits] = useState<LimitSnapshot[] | null>(null);
  const [limitsError, setLimitsError] = useState("");
  const [claude, setClaude] = useState<ClaudeAgentStatus | null>(null);
  const [cooling, setCooling] = useState<RouterCooling[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const usageRequest = useRef(0);

  const loadUsage = useCallback(async (next: RouterRange) => {
    const id = ++usageRequest.current;
    try {
      const result = await getRouterUsage(next);
      if (id === usageRequest.current) {
        setUsage(result);
        setUsageError("");
      }
    } catch (failure) {
      if (id === usageRequest.current) {
        setUsageError(failureOf(failure).message);
      }
    }
  }, []);

  const loadSide = useCallback(async (refresh: boolean) => {
    const [limitResult, claudeResult, routerResult] = await Promise.allSettled([
      getRouterLimits(refresh),
      getClaudeAgent(),
      getRouter(),
    ]);
    if (limitResult.status === "fulfilled") {
      setLimits(limitResult.value);
      setLimitsError("");
    } else {
      setLimitsError(failureOf(limitResult.reason).message);
    }
    setClaude(claudeResult.status === "fulfilled" ? claudeResult.value : null);
    if (routerResult.status === "fulfilled") {
      setCooling(routerResult.value.cooling);
    }
  }, []);

  useEffect(() => {
    void loadUsage(range);
  }, [loadUsage, range]);

  useEffect(() => {
    void loadSide(false);
  }, [loadSide]);

  async function refreshLimits() {
    setRefreshing(true);
    try {
      await loadSide(true);
    } finally {
      setRefreshing(false);
    }
  }

  const totals = usage?.totals;
  const claudeLimit = useMemo(
    () => (limits || []).find((limit) => limit.provider === CLAUDE_AGENT_PROVIDER) || null,
    [limits]
  );
  const providerLimits = useMemo(
    () => (limits || []).filter((limit) => limit.provider !== CLAUDE_AGENT_PROVIDER),
    [limits]
  );
  const claudeUsage =
    (usage?.by_provider || []).find((row) => row.provider === CLAUDE_AGENT_PROVIDER) || null;
  const showClaude = Boolean(
    claudeLimit || claudeUsage || (claude && (claude.configured_agents.length > 0 || claude.binary))
  );

  return (
    <div className="rt-overview">
      <div className="rt-toolbar">
        <Seg
          aria-label="Time range"
          options={RANGES.map((option) => ({ value: option.value, label: option.label }))}
          value={range}
          onChange={(next) => setRange(next as RouterRange)}
        />
      </div>

      {usageError ? (
        <div className="home-alert" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load usage"
            text={usageError}
            action={
              <Button variant="secondary" icon="refresh-cw" onClick={() => void loadUsage(range)}>
                Retry
              </Button>
            }
          />
        </div>
      ) : null}

      {!usage || !totals ? (
        usageError ? null : <div className="home-empty">Loading usage…</div>
      ) : (
        <>
          <div className="stats home-stats rt-stats" aria-label="Totals">
            <Stat
              icon="activity"
              label="Calls"
              value={count(totals.calls)}
              detail={
                "Error rate " +
                percent(totals.errors, totals.calls) +
                " · " +
                count(totals.errors) +
                " failed"
              }
              tone={totals.errors ? "bad" : undefined}
            />
            <Stat
              icon="coins"
              label="Tokens"
              value={compact(totals.input_tokens + totals.output_tokens)}
              detail={
                "In " +
                compact(totals.input_tokens) +
                " · Out " +
                compact(totals.output_tokens) +
                " · Cached " +
                compact(totals.cached_tokens)
              }
            />
            <Stat
              icon="receipt"
              label="Cost"
              value={<CostValue stats={totals} />}
              detail={
                totals.cost_partial
                  ? "Partial: some models have no known price"
                  : "At list prices"
              }
            />
            <Stat
              icon="timer"
              label="Time to first token"
              value={ms(totals.ttft_p50_ms)}
              detail={"Median · p95 " + ms(totals.ttft_p95_ms)}
            />
          </div>

          <Panel title="Tokens and cost" className="home-wide">
            <UsageChart series={usage.series} range={usage.range} />
          </Panel>

          <Panel title="By provider">
            <StatsTable
              head="Provider"
              empty="No calls in this range."
              rows={usage.by_provider.map((row) => ({
                key: row.provider,
                label: row.provider,
                stats: row,
              }))}
            />
          </Panel>

          <div className="rt-grid2">
            <Panel title="By model">
              <StatsTable
                head="Model"
                empty="No calls in this range."
                rows={usage.by_model.map((row) => ({
                  key: row.address,
                  label: row.address,
                  stats: row,
                }))}
              />
            </Panel>
            <Panel title="By source">
              <ShareBars
                unit="calls"
                empty="No calls in this range."
                rows={usage.by_source
                  .filter((row) => row.calls > 0)
                  .map((row) => ({
                    key: row.source,
                    label: sourceLabel(row.source),
                    sub: costText(row.cost_usd, row.cost_partial) + " cost",
                    value: row.calls,
                  }))}
              />
            </Panel>
          </div>
        </>
      )}

      <section className="rt-limits" aria-labelledby="rt-limits-title">
        <div className="rt-section-h">
          <h2 id="rt-limits-title">Limits</h2>
          <p className="muted">What each provider last reported about your quota and balance.</p>
          <span className="sp" />
          <Button
            variant="secondary"
            size="sm"
            icon="refresh-cw"
            loading={refreshing}
            disabled={refreshing}
            onClick={() => void refreshLimits()}
          >
            Refresh
          </Button>
        </div>
        {limitsError ? (
          <p className="rt-limit-err" role="alert">
            {limitsError}
          </p>
        ) : null}
        {limits === null && !limitsError ? (
          <div className="home-empty">Loading limits…</div>
        ) : (
          <div className="rt-limit-grid">
            {showClaude ? (
              <ClaudeAgentCard status={claude} limit={claudeLimit} usage={claudeUsage} />
            ) : null}
            {providerLimits.map((limit) => (
              <LimitCard key={limit.provider} limit={limit} />
            ))}
            {providerLimits.length === 0 && !showClaude && limits !== null ? (
              <EmptyState
                size="sm"
                icon="gauge"
                text="No provider has reported limits yet. Limits appear after the first call."
              />
            ) : null}
          </div>
        )}
      </section>

      <Cooling rows={cooling} />
    </div>
  );
}

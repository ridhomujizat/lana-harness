/* eslint-disable react-hooks/set-state-in-effect -- each tab loads its data on mount and on a filter change, like the other pages. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getRouter, getRouterUsage } from "../../api/router";
import type {
  RouterCooling,
  RouterProvider,
  RouterRange,
  RouterUsage,
  UsageStats,
} from "../../api/router";
import { Badge, Button, EmptyState, ErrorState, Panel, Seg, Stat } from "../../ui/gr8r";
import { ShareBars } from "../home-charts";
import { ProviderMark, providerIndex } from "./provider-logo";
import { useClaudeAccounts } from "./claude-code";
import { UsageChart } from "./usage-chart";
import {
  compact,
  count,
  ms,
  percent,
  sourceLabel,
  statsCostText,
  timeLabel,
  failureOf,
} from "./router-common";

/* Router → Usage (REQ-RTR-011): what the router did in the chosen range and
   what it cost. Limits live on the Quota page. */

export const RANGES: Array<{ value: RouterRange; label: string }> = [
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

function CostValue({ stats }: { stats: UsageStats }) {
  return (
    <span className="rt-cost">
      {statsCostText(stats)}
      {stats.cost_partial ? (
        <Badge tone="amber" className="rt-partial" title="Some calls used a model with no known price.">
          partial
        </Badge>
      ) : null}
    </span>
  );
}

export function StatsTable({
  rows,
  head,
  empty,
  providers,
}: {
  rows: Array<{ key: string; label: string; provider?: string; stats: UsageStats }>;
  head: string;
  empty: string;
  providers?: Record<string, RouterProvider>;
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
              <td className="rt-cell-name" title={row.label}>
                <span className="rt-name-logo">
                  {row.provider !== undefined ? (
                    <ProviderMark provider={providers ? providers[row.provider] : null} name={row.provider} size={18} />
                  ) : null}
                  <span className="mono trunc">{row.label}</span>
                </span>
              </td>
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

/* The provider of a "provider/model" address. */
function providerOf(address: string): string {
  const index = address.search(/[/:]/);
  return index > 0 ? address.slice(0, index) : address;
}

export function RouterUsagePage() {
  const [range, setRange] = useState<RouterRange>("24h");
  const [usage, setUsage] = useState<RouterUsage | null>(null);
  const [usageError, setUsageError] = useState("");
  const [cooling, setCooling] = useState<RouterCooling[]>([]);
  const [providers, setProviders] = useState<RouterProvider[]>([]);
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

  useEffect(() => {
    void loadUsage(range);
  }, [loadUsage, range]);

  useEffect(() => {
    let alive = true;
    getRouter()
      .then((data) => {
        if (alive) {
          setCooling(data.cooling);
          setProviders(data.providers);
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  /* Spec 39: Claude Code rows name their account; knowing the accounts
     gives them the Claude logo. */
  useClaudeAccounts();
  const index = useMemo(() => providerIndex(providers), [providers]);
  const totals = usage?.totals;

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
              providers={index}
              rows={usage.by_provider.map((row) => ({
                key: row.provider,
                label: row.provider,
                provider: row.provider,
                stats: row,
              }))}
            />
          </Panel>

          <div className="rt-grid2">
            <Panel title="By model">
              <StatsTable
                head="Model"
                empty="No calls in this range."
                providers={index}
                rows={usage.by_model.map((row) => ({
                  key: row.address,
                  label: row.address,
                  provider: row.provider || providerOf(row.address),
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
                    sub: statsCostText(row) + " cost",
                    value: row.calls,
                  }))}
              />
            </Panel>
          </div>
        </>
      )}

      <Cooling rows={cooling} />
    </div>
  );
}

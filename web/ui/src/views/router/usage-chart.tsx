import { useId, useMemo, useState } from "react";
import type { RouterRange, UsagePoint } from "../../api/router";
import { EmptyState } from "../../ui/gr8r";
import { compact, count, parseTime, usd } from "./router-common";

/* Tokens and cost over time (REQ-RTR-011). Two measures of different scale
   never share an axis, so the chart is two small multiples on one time axis:
   stacked token columns above, cost columns below, one hover readout for
   both. Plain SVG coloured with the Gr8r tokens of the Home chart. */

const W = 760;
const PAD_L = 44;
const PAD_R = 8;
const TOKENS_H = 168;
const COST_H = 84;
const GAP = 20;
const AXIS_H = 18;
const PAD_T = 8;
const H = PAD_T + TOKENS_H + GAP + COST_H + AXIS_H;

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function pad2(value: number): string {
  return value < 10 ? "0" + value : String(value);
}

export function bucketText(at: number, range: RouterRange): string {
  const date = new Date(at);
  if (range === "24h") {
    return pad2(date.getHours()) + ":" + pad2(date.getMinutes());
  }
  return MONTHS[date.getMonth()] + " " + date.getDate();
}

function niceMax(value: number): number {
  if (value <= 0) {
    return 1;
  }
  const exp = Math.pow(10, Math.floor(Math.log10(value)));
  const f = value / exp;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return nice * exp;
}

interface Row {
  at: number;
  input: number;
  cached: number;
  output: number;
  tokens: number;
  cost: number;
  costKnown: boolean;
  calls: number;
  errors: number;
}

function columnPath(x: number, width: number, top: number, bottom: number): string {
  const height = bottom - top;
  const r = Math.max(0, Math.min(4, width / 2, height));
  return `M${x},${bottom} V${top + r} Q${x},${top} ${x + r},${top} H${x + width - r} Q${x + width},${top} ${x + width},${top + r} V${bottom} Z`;
}

export function UsageChart({ series, range }: { series: UsagePoint[]; range: RouterRange }) {
  const [hover, setHover] = useState(-1);
  const clipBase = "rc" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const rows = useMemo<Row[]>(
    () =>
      series.map((point) => {
        const cached = Math.max(0, Number(point.cached_tokens) || 0);
        const inputAll = Math.max(0, Number(point.input_tokens) || 0);
        const input = Math.max(0, inputAll - cached);
        const output = Math.max(0, Number(point.output_tokens) || 0);
        const costKnown = point.cost_usd !== null && point.cost_usd !== undefined;
        return {
          at: parseTime(point.t),
          input,
          cached: Math.min(cached, inputAll),
          output,
          tokens: input + Math.min(cached, inputAll) + output,
          cost: costKnown ? Math.max(0, Number(point.cost_usd) || 0) : 0,
          costKnown,
          calls: Number(point.calls) || 0,
          errors: Number(point.errors) || 0,
        };
      }),
    [series]
  );
  const totalTokens = rows.reduce((sum, row) => sum + row.tokens, 0);
  const totalCost = rows.reduce((sum, row) => sum + row.cost, 0);
  const totalCalls = rows.reduce((sum, row) => sum + row.calls, 0);
  if (rows.length === 0 || (totalTokens === 0 && totalCalls === 0)) {
    return <EmptyState size="sm" icon="chart-column" text="No calls in this range." />;
  }
  const tokenMax = niceMax(rows.reduce((peak, row) => Math.max(peak, row.tokens), 0));
  const costMax = niceMax(rows.reduce((peak, row) => Math.max(peak, row.cost), 0));
  const plotW = W - PAD_L - PAD_R;
  const slot = plotW / rows.length;
  const bar = Math.max(2, Math.min(26, slot * 0.6));
  const tokensTop = PAD_T;
  const tokensBase = PAD_T + TOKENS_H;
  const costTop = tokensBase + GAP;
  const costBase = costTop + COST_H;
  const yTok = (value: number) => tokensBase - (value / tokenMax) * TOKENS_H;
  const yCost = (value: number) => costBase - (value / costMax) * COST_H;
  const labelEvery = Math.max(1, Math.ceil(rows.length / 8));
  const focus = hover >= 0 ? rows[hover] : undefined;

  return (
    <div className="hc-wrap rt-chart">
      <div className="hc-readout" aria-live="polite">
        {focus ? (
          <>
            <b className="num">{bucketText(focus.at, range)}</b>
            <span><i className="sw sw-in" />Input {compact(focus.input)}</span>
            <span><i className="sw sw-cache" />Cached {compact(focus.cached)}</span>
            <span><i className="sw sw-out" />Output {compact(focus.output)}</span>
            <span><i className="sw rt-sw-cost" />Cost {focus.costKnown ? usd(focus.cost) : "—"}</span>
            <span className="muted">
              {count(focus.calls)} calls{focus.errors ? " · " + count(focus.errors) + " failed" : ""}
            </span>
          </>
        ) : (
          <>
            <b className="num">{count(totalTokens)}</b>
            <span>tokens · {usd(totalCost)} in this range</span>
            <span className="hc-legend">
              <span><i className="sw sw-in" />Input</span>
              <span><i className="sw sw-cache" />Cached</span>
              <span><i className="sw sw-out" />Output</span>
              <span><i className="sw rt-sw-cost" />Cost</span>
            </span>
          </>
        )}
      </div>
      <svg
        className="hc-svg"
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label={
          "Tokens and cost over time: " +
          count(totalTokens) +
          " tokens and " +
          usd(totalCost) +
          " across " +
          rows.length +
          " buckets."
        }
        onMouseLeave={() => setHover(-1)}
      >
        {[0, 0.5, 1].map((f) => (
          <g key={"t" + f}>
            <line className="hc-grid" x1={PAD_L} x2={W - PAD_R} y1={yTok(f * tokenMax)} y2={yTok(f * tokenMax)} />
            <text className="hc-axis" x={PAD_L - 6} y={yTok(f * tokenMax) + 3} textAnchor="end">
              {compact(f * tokenMax)}
            </text>
          </g>
        ))}
        <text className="hc-axis rt-axis-title" x={PAD_L} y={costTop - 6}>
          Cost
        </text>
        {[0, 1].map((f) => (
          <g key={"c" + f}>
            <line className="hc-grid" x1={PAD_L} x2={W - PAD_R} y1={yCost(f * costMax)} y2={yCost(f * costMax)} />
            <text className="hc-axis" x={PAD_L - 6} y={yCost(f * costMax) + 3} textAnchor="end">
              {f === 0 ? "$0" : usd(costMax)}
            </text>
          </g>
        ))}
        {rows.map((row, index) => {
          const x = PAD_L + slot * index + (slot - bar) / 2;
          const clipId = clipBase + "-" + index;
          const top = yTok(row.tokens);
          const segs = [
            { value: row.input, cls: "in" },
            { value: row.cached, cls: "cache" },
            { value: row.output, cls: "out" },
          ];
          let acc = 0;
          const costTopY = yCost(row.cost);
          return (
            <g
              key={row.at + ":" + index}
              className={hover === index ? "on" : undefined}
              onMouseEnter={() => setHover(index)}
            >
              <rect className="hc-hit" x={PAD_L + slot * index} y={tokensTop} width={slot} height={costBase - tokensTop} />
              {row.tokens > 0 ? (
                <>
                  <clipPath id={clipId}>
                    <path d={columnPath(x, bar, top, tokensBase)} />
                  </clipPath>
                  <g clipPath={`url(#${clipId})`}>
                    {segs.map((seg) => {
                      const segTop = yTok(acc + seg.value);
                      const height = yTok(acc) - segTop;
                      acc += seg.value;
                      /* A 1px surface gap between stacked parts. */
                      return height > 0 ? (
                        <rect
                          key={seg.cls}
                          className={"hc-bar " + seg.cls}
                          x={x}
                          y={segTop + 1}
                          width={bar}
                          height={Math.max(0.5, height - 1)}
                        />
                      ) : null;
                    })}
                  </g>
                </>
              ) : null}
              {row.cost > 0 ? (
                <path className="rt-cost-bar" d={columnPath(x, bar, costTopY, costBase)} />
              ) : null}
              {index % labelEvery === 0 ? (
                <text className="hc-axis" x={x + bar / 2} y={H - 4} textAnchor="middle">
                  {bucketText(row.at, range)}
                </text>
              ) : null}
            </g>
          );
        })}
      </svg>
      <details className="home-chart-table rt-chart-table">
        <summary>Show as a table</summary>
        <div className="home-table-wrap">
          <table className="perm-t home-table">
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col" className="is-num">Calls</th>
                <th scope="col" className="is-num">Input</th>
                <th scope="col" className="is-num">Cached</th>
                <th scope="col" className="is-num">Output</th>
                <th scope="col" className="is-num">Cost</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => (
                <tr key={row.at + ":" + index}>
                  <td className="num">{bucketText(row.at, range)}</td>
                  <td className="is-num num">{count(row.calls)}</td>
                  <td className="is-num num">{count(row.input)}</td>
                  <td className="is-num num">{count(row.cached)}</td>
                  <td className="is-num num">{count(row.output)}</td>
                  <td className="is-num num">{row.costKnown ? usd(row.cost) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}

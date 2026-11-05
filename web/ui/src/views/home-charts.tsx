import { useId, useMemo, useState } from "react";
import type { Dashboard } from "../api/dashboard";
import { EmptyState } from "../ui/gr8r";
import { bucketLabel, compact, count, share } from "./dashboard-common";

/* Charts of the Home page (spec 28, owner request 2026-11-05: Home shows only
   graphics). Plain SVG, sized by viewBox, coloured with Gr8r tokens. */

interface ChartRow {
  start: number;
  input: number;
  cached: number;
  output: number;
  total: number;
}

const W = 760;
const H = 220;
const PAD_L = 40;
const PAD_B = 24;
const PAD_T = 10;

function niceMax(value: number): number {
  if (value <= 0) {
    return 1;
  }
  const exp = Math.pow(10, Math.floor(Math.log10(value)));
  const f = value / exp;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return nice * exp;
}

/* REQ-DSH-004: token usage per bucket as stacked columns (input that was not
   cached, cached input, output) with a hover readout. */
export function TokenColumns({ data }: { data: Dashboard }) {
  const bucketMs = data.bucket_ms || 3600000;
  const [hover, setHover] = useState(-1);
  const clipBase = "hc" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const rows = useMemo<ChartRow[]>(
    () =>
      (data.series || []).map((point) => {
        const cached = Math.max(0, point.cached_input_tokens || 0);
        const input = Math.max(0, (point.input_tokens || 0) - cached);
        const output = Math.max(0, point.output_tokens || 0);
        return { start: point.start, input, cached, output, total: input + cached + output };
      }),
    [data.series]
  );
  const totalTokens = rows.reduce((sum, row) => sum + row.total, 0);
  if (rows.length === 0 || totalTokens === 0) {
    return <EmptyState size="sm" icon="chart-column" text="No token usage in this range." />;
  }
  const max = niceMax(rows.reduce((peak, row) => Math.max(peak, row.total), 0));
  const plotW = W - PAD_L - 8;
  const plotH = H - PAD_B - PAD_T;
  const slot = plotW / rows.length;
  const bar = Math.max(2, Math.min(28, slot * 0.62));
  const y = (value: number) => PAD_T + plotH - (value / max) * plotH;
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => f * max);
  const labelEvery = Math.max(1, Math.ceil(rows.length / 8));
  const focus = hover >= 0 ? rows[hover] : undefined;
  return (
    <div className="hc-wrap">
      <div className="hc-readout" aria-live="polite">
        {focus ? (
          <>
            <b className="num">{bucketLabel(focus.start, bucketMs)}</b>
            <span><i className="sw sw-in" />Input {compact(focus.input)}</span>
            <span><i className="sw sw-cache" />Cached {compact(focus.cached)}</span>
            <span><i className="sw sw-out" />Output {compact(focus.output)}</span>
          </>
        ) : (
          <>
            <b className="num">{count(totalTokens)}</b>
            <span>tokens in this range</span>
            <span className="hc-legend">
              <span><i className="sw sw-in" />Input</span>
              <span><i className="sw sw-cache" />Cached</span>
              <span><i className="sw sw-out" />Output</span>
            </span>
          </>
        )}
      </div>
      <svg
        className="hc-svg"
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label={"Token usage over time, " + count(totalTokens) + " tokens across " + rows.length + " buckets."}
        onMouseLeave={() => setHover(-1)}
      >
        {ticks.map((tick) => (
          <g key={tick}>
            <line className="hc-grid" x1={PAD_L} x2={W - 8} y1={y(tick)} y2={y(tick)} />
            <text className="hc-axis" x={PAD_L - 6} y={y(tick) + 3} textAnchor="end">
              {compact(tick)}
            </text>
          </g>
        ))}
        {rows.map((row, index) => {
          const x = PAD_L + slot * index + (slot - bar) / 2;
          const segs = [
            { value: row.input, cls: "in" },
            { value: row.cached, cls: "cache" },
            { value: row.output, cls: "out" },
          ];
          let acc = 0;
          const columnTop = y(row.total);
          const columnH = y(0) - columnTop;
          const radius = Math.min(4, bar / 2, columnH);
          const clipId = clipBase + "-" + index;
          return (
            <g key={row.start} onMouseEnter={() => setHover(index)} className={hover === index ? "on" : undefined}>
              <rect className="hc-hit" x={PAD_L + slot * index} y={PAD_T} width={slot} height={plotH} />
              {columnH > 0 ? (
                <>
                  {/* One column with a rounded top; the stacked parts are
                      flat bands clipped to it, so thin parts stay attached. */}
                  <clipPath id={clipId}>
                    <path
                      d={`M${x},${y(0)} V${columnTop + radius} Q${x},${columnTop} ${x + radius},${columnTop} H${x + bar - radius} Q${x + bar},${columnTop} ${x + bar},${columnTop + radius} V${y(0)} Z`}
                    />
                  </clipPath>
                  <g clipPath={`url(#${clipId})`}>
                    {segs.map((seg) => {
                      const top = y(acc + seg.value);
                      const height = y(acc) - top;
                      acc += seg.value;
                      return height > 0 ? (
                        <rect key={seg.cls} className={"hc-bar " + seg.cls} x={x} y={top} width={bar} height={height + 0.5} />
                      ) : null;
                    })}
                  </g>
                </>
              ) : null}
              {index % labelEvery === 0 ? (
                <text className="hc-axis" x={x + bar / 2} y={H - 6} textAnchor="middle">
                  {bucketLabel(row.start, bucketMs)}
                </text>
              ) : null}
            </g>
          );
        })}
      </svg>
    </div>
  );
}

const OUTCOME_COLORS: Record<string, string> = {
  end_turn: "var(--green)",
  "": "var(--green)",
  max_steps: "var(--amber)",
  loop: "var(--orange)",
  cancelled: "var(--gray)",
  error: "var(--red)",
};

const EXTRA_COLORS = ["var(--blue)", "var(--violet)", "var(--teal)", "var(--rose)"];

/* Turn outcomes as a donut with the total in the middle. */
export function OutcomeDonut({ data }: { data: Dashboard }) {
  const rows = data.turn_reasons || [];
  const total = rows.reduce((sum, row) => sum + row.count, 0);
  if (rows.length === 0 || total === 0) {
    return <EmptyState size="sm" icon="circle-dot" text="No turns in this range." />;
  }
  const r = 54;
  const c = 2 * Math.PI * r;
  const parts = rows.reduce<{ row: (typeof rows)[number]; color: string; len: number; offset: number }[]>(
    (list, row) => {
      const extra = list.filter((part) => !(part.row.reason in OUTCOME_COLORS)).length;
      const color = OUTCOME_COLORS[row.reason] || EXTRA_COLORS[extra % EXTRA_COLORS.length]!;
      const offset = list.length > 0 ? list[list.length - 1]!.offset + list[list.length - 1]!.len : 0;
      return list.concat([{ row, color, len: (row.count / total) * c, offset }]);
    },
    []
  );
  return (
    <div className="hc-donut">
      <svg viewBox="0 0 140 140" role="img" aria-label={"Turn outcomes, " + total + " turns"}>
        <circle cx="70" cy="70" r={r} className="hc-donut-bg" />
        {parts.map((part) => (
          <circle
            key={part.row.reason || "end_turn"}
            cx="70"
            cy="70"
            r={r}
            fill="none"
            stroke={part.color}
            strokeWidth="14"
            strokeDasharray={`${Math.max(0, part.len - 1.5)} ${c}`}
            strokeDashoffset={-part.offset}
            transform="rotate(-90 70 70)"
          />
        ))}
        <text x="70" y="68" textAnchor="middle" className="hc-donut-v">
          {compact(total)}
        </text>
        <text x="70" y="86" textAnchor="middle" className="hc-donut-k">
          turns
        </text>
      </svg>
      <ul className="hc-keys">
        {parts.map((part) => (
          <li key={part.row.reason || "end_turn"}>
            <i style={{ background: part.color }} />
            <span className="trunc">{part.row.reason || "end_turn"}</span>
            <b className="num">{count(part.row.count)}</b>
            <span className="muted num">{share(part.row.count, total)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export interface ShareRow {
  key: string;
  label: string;
  sub?: string;
  value: number;
}

/* Horizontal share bars, e.g. tokens by model, agent or source. */
export function ShareBars({ rows, empty, unit = "tok" }: { rows: ShareRow[]; empty: string; unit?: string }) {
  const total = rows.reduce((sum, row) => sum + row.value, 0);
  if (rows.length === 0 || total === 0) {
    return <EmptyState size="sm" icon="chart-bar" text={empty} />;
  }
  const max = Math.max(...rows.map((row) => row.value));
  return (
    <ul className="hc-bars">
      {rows.map((row) => (
        <li key={row.key}>
          <div className="hc-bars-top">
            <span className="trunc">{row.label}</span>
            <b className="num">
              {compact(row.value)} {unit}
            </b>
          </div>
          <div className="hc-track">
            <span style={{ width: (row.value / max) * 100 + "%" }} />
          </div>
          {row.sub ? <span className="hc-bars-sub">{row.sub} · {share(row.value, total)}</span> : null}
        </li>
      ))}
    </ul>
  );
}

/* The busiest tools: calls, with the failed part in red. */
export function ToolBars({ data }: { data: Dashboard }) {
  const rows = [...(data.tools || [])].sort((a, b) => b.calls - a.calls).slice(0, 8);
  if (rows.length === 0) {
    return <EmptyState size="sm" icon="wrench" text="No tool calls in this range." />;
  }
  const max = Math.max(...rows.map((row) => row.calls), 1);
  return (
    <ul className="hc-bars">
      {rows.map((row) => (
        <li key={row.name}>
          <div className="hc-bars-top">
            <span className="trunc mono">{row.name}</span>
            <b className="num">
              {count(row.calls)}
              {row.errors ? <span className="hc-err"> · {count(row.errors)} failed</span> : null}
            </b>
          </div>
          <div className="hc-track">
            <span style={{ width: ((row.calls - row.errors) / max) * 100 + "%" }} />
            <span className="err" style={{ width: (row.errors / max) * 100 + "%" }} />
          </div>
        </li>
      ))}
    </ul>
  );
}

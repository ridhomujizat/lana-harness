import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
} from "react";
import type { RouterRange, UsagePoint } from "../../api/router";
import { EmptyState } from "../../ui/gr8r";
import { compact, count, parseTime, usd } from "./router-common";

/* Tokens and cost over time (REQ-RTR-011). Two measures of different scale
   never share an axis, so the chart is two small multiples on one time axis:
   a "Tokens" panel of stacked columns above a "Cost (USD)" panel, each with
   its own label row and y ticks, and the time axis only under the bottom one.

   The range totals sit above both panels and never change. Hovering (or
   focusing the plot and using the arrow keys) draws a thin guide through the
   bucket and shows a small floating tooltip; nothing else moves.

   The viewBox follows the measured width of the card, so one SVG unit is one
   CSS pixel and the 10px axis text stays 10px on a phone instead of being
   scaled down with the drawing; narrow cards get fewer time ticks. */

const DEFAULT_W = 760;
const MIN_W = 260;
/* The room one time tick needs ("Nov 12", "14:00") plus a gap. */
const TICK_SPACE = 56;
const MAX_TICKS = 8;
const PAD_R = 8;
/* Room above the top tick and below the baseline for the 10px tick text. */
const PAD_T = 7;
const PAD_B = 4;
const AXIS_H = 18;
/* Bars: at most 24px wide, a 2px radius on the data end, square at the
   baseline, and a 2px surface gap between stacked parts. */
const BAR_MAX = 24;
const RADIUS = 2;
const SEG_GAP = 2;
/* The tooltip keeps this far from the guide and from the card edge. */
const TIP_OFFSET = 12;
const TIP_EDGE = 4;

/* The content width of the element, kept up to date with a ResizeObserver
   (which reports once on observe). Without one (tests, old browsers) it is
   the default width and the SVG scales as before. */
function useWidth(fallback: number) {
  /* A callback ref, so the observer starts when the chart first mounts even
     if the first render was the empty state. */
  const [node, ref] = useState<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(fallback);
  useEffect(() => {
    if (!node || typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver((entries) => {
      const next = Math.round(entries[0]?.contentRect.width || 0);
      if (next > 0) {
        setWidth((current) => (current === next ? current : next));
      }
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, [node]);
  return [ref, width] as const;
}

/* Every n-th bucket gets a time tick so the ticks fit the plot width. */
export function tickEvery(buckets: number, plotWidth: number): number {
  const fit = Math.max(2, Math.min(MAX_TICKS, Math.floor(plotWidth / TICK_SPACE)));
  return Math.max(1, Math.ceil(buckets / fit));
}

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

/* A rounded axis maximum whose half is a round number too, so the mid tick
   reads cleanly (0 / 100k / 200k, $0 / $0.01 / $0.02). */
export function niceMax(value: number): number {
  if (!(value > 0)) {
    return 1;
  }
  const exp = Math.pow(10, Math.floor(Math.log10(value)));
  const f = value / exp;
  const nice = [1, 2, 4, 6, 8, 10].find((step) => f <= step + 1e-9) ?? 10;
  return nice * exp;
}

/* Cost ticks: "$0" at the baseline, the usual money text above it. */
function costTick(value: number): string {
  return value === 0 ? "$0" : usd(value);
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

interface TipLine {
  key: "in" | "cache" | "out" | "cost";
  value: string;
  label: string;
}

/* A column with a small radius on its data end and a square foot. */
function columnPath(x: number, width: number, top: number, bottom: number): string {
  const height = bottom - top;
  const r = Math.max(0, Math.min(RADIUS, width / 2, height));
  return `M${x},${bottom} V${top + r} Q${x},${top} ${x + r},${top} H${x + width - r} Q${x + width},${top} ${x + width},${top + r} V${bottom} Z`;
}

function callsText(calls: number): string {
  return count(calls) + (calls === 1 ? " call" : " calls");
}

/* The tooltip lines of one bucket: only the values that are not zero. */
function bucketLines(row: Row): TipLine[] {
  const lines: TipLine[] = [];
  if (row.calls === 0 && row.tokens === 0) {
    return lines;
  }
  if (row.input > 0) lines.push({ key: "in", value: compact(row.input), label: "Input" });
  if (row.cached > 0) lines.push({ key: "cache", value: compact(row.cached), label: "Cached" });
  if (row.output > 0) lines.push({ key: "out", value: compact(row.output), label: "Output" });
  if (!row.costKnown) {
    lines.push({ key: "cost", value: "—", label: "Cost" });
  } else if (row.cost > 0) {
    lines.push({ key: "cost", value: usd(row.cost), label: "Cost" });
  }
  return lines;
}

interface Hover {
  index: number;
  /* The pointer height inside the plot, or null when the keyboard moved. */
  y: number | null;
}

export function UsageChart({ series, range }: { series: UsagePoint[]; range: RouterRange }) {
  const [hover, setHover] = useState<Hover | null>(null);
  const [wrapRef, measured] = useWidth(DEFAULT_W);
  const plotRef = useRef<HTMLDivElement | null>(null);
  const tipRef = useRef<HTMLDivElement | null>(null);
  const W = Math.max(MIN_W, measured);
  const narrow = W < 480;
  const rows = useMemo<Row[]>(
    () =>
      series.map((point) => {
        const cached = Math.max(0, Number(point.cached_tokens) || 0);
        const inputAll = Math.max(0, Number(point.input_tokens) || 0);
        const input = Math.max(0, inputAll - cached);
        const output = Math.max(0, Number(point.output_tokens) || 0);
        const calls = Number(point.calls) || 0;
        const errors = Number(point.errors) || 0;
        /* A bucket of failed calls only has no price, not a $0 one. */
        const costKnown =
          point.cost_usd !== null &&
          point.cost_usd !== undefined &&
          !(calls > 0 && errors >= calls && !Number(point.cost_usd));
        return {
          at: parseTime(point.t),
          input,
          cached: Math.min(cached, inputAll),
          output,
          tokens: input + Math.min(cached, inputAll) + output,
          cost: costKnown ? Math.max(0, Number(point.cost_usd) || 0) : 0,
          costKnown,
          calls,
          errors,
        };
      }),
    [series]
  );
  const totalTokens = rows.reduce((sum, row) => sum + row.tokens, 0);
  const totalCost = rows.reduce((sum, row) => sum + row.cost, 0);
  const totalCalls = rows.reduce((sum, row) => sum + row.calls, 0);
  const empty = rows.length === 0 || (totalTokens === 0 && totalCalls === 0);

  const tokensH = narrow ? 128 : 156;
  const costH = narrow ? 60 : 72;
  const tokenMax = niceMax(rows.reduce((peak, row) => Math.max(peak, row.tokens), 0));
  const costMax = niceMax(rows.reduce((peak, row) => Math.max(peak, row.cost), 0));
  /* 0, a mid tick, and the rounded max: both panels are tall enough for
     three ticks 30px or more apart. */
  const ticks = [0, 0.5, 1];
  /* The y labels of both panels share one left margin so the columns line
     up; it is as wide as the longest label (10px text, ~6px a glyph). */
  const longest = Math.max(
    ...ticks.map((f) => compact(f * tokenMax).length),
    ...ticks.map((f) => costTick(f * costMax).length)
  );
  const padL = Math.max(28, longest * 6 + 10);
  const plotW = W - padL - PAD_R;
  const slot = rows.length > 0 ? plotW / rows.length : plotW;
  const bar = Math.max(2, Math.min(BAR_MAX, slot * 0.6));
  const center = (index: number) => padL + slot * index + slot / 2;
  const labelEvery = tickEvery(rows.length, plotW);
  const step = (rows[1]?.at ?? 0) - (rows[0]?.at ?? 0);
  const active = hover && hover.index < rows.length ? hover : null;
  const focus = active ? rows[active.index] : undefined;
  const guideX = active ? center(active.index) : 0;

  /* Keep the tooltip beside the guide, flipped to the left near the right
     edge and clamped inside the card. Measured after layout, so it is set on
     the node. */
  useLayoutEffect(() => {
    const tip = tipRef.current;
    const plot = plotRef.current;
    if (!tip || !plot || !active) {
      return;
    }
    const plotWidth = plot.clientWidth || W;
    const plotHeight = plot.clientHeight;
    const x = guideX * (plotWidth / W);
    const tipW = tip.offsetWidth;
    const tipH = tip.offsetHeight;
    let left = x + TIP_OFFSET;
    if (left + tipW > plotWidth - TIP_EDGE) {
      left = x - TIP_OFFSET - tipW;
    }
    left = Math.max(TIP_EDGE, Math.min(left, plotWidth - tipW - TIP_EDGE));
    const wanted = active.y === null ? TIP_EDGE : active.y - tipH - TIP_OFFSET;
    const top = Math.max(TIP_EDGE, Math.min(wanted, plotHeight - tipH - TIP_EDGE));
    tip.style.transform = `translate(${Math.round(left)}px, ${Math.round(top)}px)`;
    tip.style.visibility = "visible";
  }, [active, guideX, W]);

  if (empty) {
    return <EmptyState size="sm" icon="chart-column" text="No calls in this range." />;
  }

  const yTok = (value: number) => PAD_T + tokensH - (value / tokenMax) * tokensH;
  const tokensBase = PAD_T + tokensH;
  const costBase = PAD_T + costH;
  const yCost = (value: number) => costBase - (value / costMax) * costH;
  const tokensSvgH = tokensBase + PAD_B;
  const costSvgH = costBase + AXIS_H;

  const onPointer = (event: PointerEvent<HTMLDivElement>) => {
    const plot = plotRef.current;
    if (!plot) {
      return;
    }
    const rect = plot.getBoundingClientRect();
    const scale = rect.width > 0 ? rect.width / W : 1;
    const x = (event.clientX - rect.left) / scale;
    const index = Math.max(0, Math.min(rows.length - 1, Math.floor((x - padL) / slot)));
    const y = event.clientY - rect.top;
    setHover((current) =>
      current && current.index === index && current.y !== null && Math.abs(current.y - y) < 6
        ? current
        : { index, y }
    );
  };
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const last = rows.length - 1;
    const from = active ? active.index : -1;
    let next: number | null = null;
    if (event.key === "ArrowRight") next = from < 0 ? 0 : Math.min(last, from + 1);
    else if (event.key === "ArrowLeft") next = from < 0 ? last : Math.max(0, from - 1);
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = last;
    else if (event.key === "Escape" && active) {
      event.preventDefault();
      setHover(null);
      return;
    }
    if (next !== null) {
      event.preventDefault();
      setHover({ index: next, y: null });
    }
  };

  const guide = (top: number, bottom: number) =>
    active ? <line className="rt-guide" x1={guideX} x2={guideX} y1={top} y2={bottom} /> : null;

  const tipTitle = focus
    ? range === "24h" && rows.length > 1 && step > 0
      ? bucketText(focus.at, range) + "–" + bucketText(focus.at + step, range)
      : bucketText(focus.at, range)
    : "";
  const tipLines = focus ? bucketLines(focus) : [];
  const tipCalls =
    focus && focus.calls > 0
      ? callsText(focus.calls) + (focus.errors ? " · " + count(focus.errors) + " failed" : "")
      : "No calls";
  const spoken = focus
    ? [tipTitle, ...tipLines.map((line) => line.label + " " + line.value), tipCalls].join(", ")
    : "";

  return (
    <div className="hc-wrap rt-chart" ref={wrapRef}>
      <p className="rt-chart-sum">
        <span><b className="num">{compact(totalTokens)}</b> tokens</span>
        <span><b className="num">{usd(totalCost)}</b></span>
        <span><b className="num">{count(totalCalls)}</b> {totalCalls === 1 ? "call" : "calls"}</span>
      </p>
      <div
        className="rt-plot"
        ref={plotRef}
        tabIndex={0}
        role="group"
        aria-label="Tokens and cost over time. Use the arrow keys to read each bucket."
        onPointerMove={onPointer}
        onPointerDown={onPointer}
        onPointerLeave={(event) => {
          if (event.pointerType !== "touch") {
            setHover(null);
          }
        }}
        onKeyDown={onKey}
        onBlur={() => setHover(null)}
      >
        <section className="rt-pane" aria-label="Tokens">
          <div className="rt-pane-h">
            <span className="rt-pane-t">Tokens</span>
            <span className="rt-legend">
              <span><i className="rt-key sw-in" />Input</span>
              <span><i className="rt-key sw-cache" />Cached</span>
              <span><i className="rt-key sw-out" />Output</span>
            </span>
          </div>
          <svg
            className="hc-svg"
            viewBox={`0 0 ${W} ${tokensSvgH}`}
            role="img"
            aria-label={"Tokens per bucket: " + count(totalTokens) + " in this range across " + rows.length + " buckets."}
          >
            {ticks.map((f) => (
              <g key={"t" + f}>
                <line className="hc-grid" x1={padL} x2={W - PAD_R} y1={yTok(f * tokenMax)} y2={yTok(f * tokenMax)} />
                <text className="hc-axis" x={padL - 6} y={yTok(f * tokenMax) + 3.5} textAnchor="end">
                  {compact(f * tokenMax)}
                </text>
              </g>
            ))}
            {guide(PAD_T, tokensBase)}
            {rows.map((row, index) => {
              if (row.tokens <= 0) {
                return null;
              }
              const x = center(index) - bar / 2;
              const segs = [
                { value: row.input, cls: "in" },
                { value: row.cached, cls: "cache" },
                { value: row.output, cls: "out" },
              ].filter((seg) => seg.value > 0);
              let acc = 0;
              return (
                <g key={row.at + ":" + index} className="rt-col">
                  {segs.map((seg, part) => {
                    /* A 2px surface gap under every part but the first. */
                    const bottom = yTok(acc) - (part > 0 ? SEG_GAP : 0);
                    acc += seg.value;
                    /* Every part stays at least 1px tall so a thin one is
                       still seen; the table has its exact value. */
                    const top = Math.min(yTok(acc), bottom - 1);
                    return part === segs.length - 1 ? (
                      <path key={seg.cls} className={"hc-bar " + seg.cls} d={columnPath(x, bar, top, bottom)} />
                    ) : (
                      <rect key={seg.cls} className={"hc-bar " + seg.cls} x={x} y={top} width={bar} height={bottom - top} />
                    );
                  })}
                </g>
              );
            })}
          </svg>
        </section>
        <section className="rt-pane" aria-label="Cost (USD)">
          <div className="rt-pane-h">
            <span className="rt-pane-t">Cost (USD)</span>
          </div>
          <svg
            className="hc-svg"
            viewBox={`0 0 ${W} ${costSvgH}`}
            role="img"
            aria-label={"Cost per bucket: " + usd(totalCost) + " in this range."}
          >
            {ticks.map((f) => (
              <g key={"c" + f}>
                <line className="hc-grid" x1={padL} x2={W - PAD_R} y1={yCost(f * costMax)} y2={yCost(f * costMax)} />
                <text className="hc-axis" x={padL - 6} y={yCost(f * costMax) + 3.5} textAnchor="end">
                  {costTick(f * costMax)}
                </text>
              </g>
            ))}
            {guide(PAD_T, costBase)}
            {rows.map((row, index) => (
              <g key={row.at + ":" + index}>
                {row.cost > 0 ? (
                  <path
                    className="rt-cost-bar"
                    d={columnPath(center(index) - bar / 2, bar, Math.min(yCost(row.cost), costBase - 1), costBase)}
                  />
                ) : null}
                {index % labelEvery === 0 ? (
                  <text className="hc-axis" x={center(index)} y={costSvgH - 4} textAnchor="middle">
                    {bucketText(row.at, range)}
                  </text>
                ) : null}
              </g>
            ))}
          </svg>
        </section>
        {focus ? (
          <div className="rt-tip" ref={tipRef} aria-hidden="true" data-testid="usage-tip">
            <div className="rt-tip-t num">{tipTitle}</div>
            {tipLines.map((line) => (
              <div className="rt-tip-r" key={line.key}>
                <i className={"rt-tip-k " + (line.key === "cost" ? "rt-sw-cost" : "sw-" + line.key)} />
                <b className="num">{line.value}</b>
                <span>{line.label}</span>
              </div>
            ))}
            <div className="rt-tip-c">{tipCalls}</div>
          </div>
        ) : null}
        <div className="sr-only" aria-live="polite">
          {spoken}
        </div>
      </div>
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

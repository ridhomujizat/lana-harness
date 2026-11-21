import { useCallback, useEffect, useMemo, useState, type RefObject } from "react";

/* Spec 66: a small row virtualiser for the file and diff viewers, so a file
   of thousands of lines renders only the rows on screen. Rows may differ in
   height (hunk headers); offsets are prefix sums. */

export interface VirtualWindow {
  start: number;
  end: number;
  top: number;
  bottom: number;
  offsetOf: (index: number) => number;
}

/* The first index whose row ends below y. */
function findStart(offsets: number[], y: number): number {
  let lo = 0;
  let hi = offsets.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if ((offsets[mid + 1] ?? Infinity) <= y) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export function computeWindow(heights: number[], scrollTop: number, viewport: number, overscan: number): Omit<VirtualWindow, "offsetOf"> & { offsets: number[] } {
  const offsets = new Array<number>(heights.length + 1);
  offsets[0] = 0;
  for (let i = 0; i < heights.length; i++) offsets[i + 1] = (offsets[i] ?? 0) + (heights[i] ?? 0);
  const total = offsets[heights.length] ?? 0;
  if (!heights.length) return { start: 0, end: 0, top: 0, bottom: 0, offsets };
  const first = findStart(offsets, Math.max(0, scrollTop));
  const last = findStart(offsets, scrollTop + Math.max(viewport, 1));
  const start = Math.max(0, first - overscan);
  const end = Math.min(heights.length, last + 1 + overscan);
  return { start, end, top: offsets[start] ?? 0, bottom: total - (offsets[end] ?? total), offsets };
}

export function useVirtualRows(container: RefObject<HTMLElement | null>, heights: number[], overscan = 30): VirtualWindow {
  const [scroll, setScroll] = useState({ top: 0, height: 800 });

  useEffect(() => {
    const el = container.current;
    if (!el) return;
    let frame = 0;
    const read = () => {
      frame = 0;
      setScroll((s) => (s.top === el.scrollTop && s.height === el.clientHeight ? s : { top: el.scrollTop, height: el.clientHeight }));
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(read);
    };
    read();
    el.addEventListener("scroll", onScroll, { passive: true });
    const ro = typeof ResizeObserver !== "undefined" ? new ResizeObserver(onScroll) : null;
    ro?.observe(el);
    return () => {
      el.removeEventListener("scroll", onScroll);
      ro?.disconnect();
      if (frame) cancelAnimationFrame(frame);
    };
  }, [container]);

  const win = useMemo(() => computeWindow(heights, scroll.top, scroll.height, overscan), [heights, scroll, overscan]);
  const offsetOf = useCallback((i: number) => win.offsets[Math.max(0, Math.min(i, win.offsets.length - 1))] ?? 0, [win.offsets]);
  return { start: win.start, end: win.end, top: win.top, bottom: win.bottom, offsetOf };
}

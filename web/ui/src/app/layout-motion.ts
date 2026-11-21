/* Layout motion: the sidebar and the Workbench panel slide open and closed.
   While a slide runs, <html> carries data-layout-anim so terminals hold their
   size instead of refitting on every frame; when it ends, LAYOUT_SETTLED
   fires once and they fit the final box. */

const ATTR = "data-layout-anim";
export const LAYOUT_SETTLED = "lana:layout-settled";
/* Matches the CSS transitions (220ms) with a little slack. */
export const LAYOUT_MOTION_MS = 240;

let timer: ReturnType<typeof setTimeout> | undefined;

export function reducedMotion(): boolean {
  if (typeof document === "undefined") return true;
  if (document.documentElement.dataset.motion === "reduce") return true;
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function layoutAnimating(): boolean {
  return typeof document !== "undefined" && document.documentElement.hasAttribute(ATTR);
}

/* Marks a layout slide that lasts ms; does nothing when motion is reduced. */
export function holdLayout(ms = LAYOUT_MOTION_MS): void {
  if (reducedMotion()) return;
  const root = document.documentElement;
  root.setAttribute(ATTR, "");
  clearTimeout(timer);
  timer = setTimeout(() => {
    root.removeAttribute(ATTR);
    window.dispatchEvent(new Event(LAYOUT_SETTLED));
  }, ms);
}

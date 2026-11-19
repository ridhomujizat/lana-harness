/* Keeps the app on the part of the screen the user can see on phones.

   iOS Safari does not shrink the layout when the keyboard opens. It shrinks
   the visual viewport and pans it down inside the full-height layout viewport,
   so a full-height app ends up with its bottom (the chat composer) under the
   keyboard and its topbar under the URL bar. The fixed #app (gr8r-lana.css)
   follows the visual viewport: --app-h is its height and --app-top its pan
   offset. */
export function installViewportFit(): void {
  const vv = window.visualViewport;
  if (!vv) return;
  const root = document.documentElement;

  const sync = () => {
    /* Skip while the user pinch-zooms: the visual viewport is then a window
       into the page, not the screen size. */
    if (vv.scale > 1.01) return;
    root.style.setProperty("--app-h", `${Math.round(vv.height)}px`);
    root.style.setProperty("--app-top", `${Math.round(Math.max(0, vv.offsetTop))}px`);
  };

  vv.addEventListener("resize", sync);
  vv.addEventListener("scroll", sync);
  /* iOS settles the viewport late after the keyboard closes. */
  document.addEventListener("focusout", () => window.setTimeout(sync, 100));
  sync();
}

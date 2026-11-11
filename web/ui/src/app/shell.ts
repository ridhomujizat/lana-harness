/* The Lana.app window (spec #52) marks the page with
   html[data-shell="macos-app"] before any script runs. The window has no title
   bar: the macOS traffic lights sit in the sidebar's top row, which the page
   keeps free, and rows marked data-drag-region move the window. In a normal
   browser the attribute is absent and nothing changes. */

export const MAC_SHELL = "macos-app";

/* isMacAppShell reports whether the page runs inside the Lana.app window. */
export function isMacAppShell(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  return document.documentElement.dataset.shell === MAC_SHELL;
}

/* dragRegion returns the attributes of a row that moves the window in the
   Lana.app shell, and nothing in a browser. */
export function dragRegion(shell: boolean): { "data-drag-region"?: "" } {
  return shell ? { "data-drag-region": "" } : {};
}

/* topbarToggle decides whether the main topbar carries the sidebar toggle,
   right after the traffic lights: in the app shell while the sidebar is the
   collapsed rail, which is too narrow for the lights and the toggle. The
   lights' inset itself is CSS (it also covers the narrow-window drawer). */
export function topbarToggle(shell: boolean, collapsed: boolean): boolean {
  return shell && collapsed;
}

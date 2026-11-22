import { useSyncExternalStore } from "react";

/* Spec 71 REQ-WC-050, D1, D2: which view a Claude pane shows, remembered per
   pane in this browser. A pane with no choice opens in Terminal, or in Chat on
   a phone-width screen. */

export type PaneView = "terminal" | "chat";

const KEY = "lana.workbench.view.";
const listeners = new Set<() => void>();
const memory = new Map<string, PaneView>();

function read(id: string): PaneView | null {
  if (memory.has(id)) return memory.get(id)!;
  try {
    const v = window.localStorage.getItem(KEY + id);
    if (v === "chat" || v === "terminal") {
      memory.set(id, v);
      return v;
    }
  } catch {
    /* Storage can be unavailable (private window); memory still works. */
  }
  return null;
}

export function paneView(id: string, phone: boolean): PaneView {
  return read(id) ?? (phone ? "chat" : "terminal");
}

export function setPaneView(id: string, view: PaneView): void {
  memory.set(id, view);
  try {
    window.localStorage.setItem(KEY + id, view);
  } catch {
    /* ignored, see read */
  }
  for (const l of listeners) l();
}

export function togglePaneView(id: string, phone: boolean): void {
  setPaneView(id, paneView(id, phone) === "chat" ? "terminal" : "chat");
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function usePaneView(id: string, phone: boolean): PaneView {
  return useSyncExternalStore(
    subscribe,
    () => paneView(id, phone),
    () => "terminal",
  );
}

/* Agents that have a chat adapter; spec 60 adapters without one show the
   button disabled (D4). */
export function chatSupport(agent?: string): "yes" | "no" | "none" {
  if (agent === "claude") return "yes";
  if (agent === "omp") return "no";
  return "none";
}

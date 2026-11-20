import { useSyncExternalStore } from "react";

/* The chat dock (Facebook-style chat windows on every page but the Chats
   page). The store keeps which rooms are docked, in most-recent-first order,
   whether each is open as a window or minimized as a tab, and when the owner
   last had each room open on the Chats page. It is saved per browser in
   localStorage; a private window or blocked storage just starts empty. */

export interface DockItem {
  name: string;
  open: boolean;
}

export interface DockPeek {
  name: string;
  at: number;
}

export interface DockState {
  items: DockItem[];
  /* Room name to the last time (ms) the owner had it open on the Chats page. */
  recent: Record<string, number>;
  /* The "new reply" card shown above a tab for a few seconds; not saved. */
  peek: DockPeek | null;
}

export const DOCK_STORAGE_KEY = "lana.chatDock.v1";
/* A room counts as one the owner is chatting with for this long after they
   last had it open on the Chats page. */
export const RECENT_MS = 2 * 60 * 60 * 1000;
export const PEEK_MS = 6000;

let state: DockState = load();
const listeners = new Set<() => void>();
let peekTimer: ReturnType<typeof setTimeout> | null = null;

function load(): DockState {
  const empty: DockState = { items: [], recent: {}, peek: null };
  try {
    const raw = window.localStorage.getItem(DOCK_STORAGE_KEY);
    if (!raw) {
      return empty;
    }
    const parsed = JSON.parse(raw) as Partial<DockState>;
    const items = Array.isArray(parsed.items)
      ? parsed.items
          .filter((item) => item && typeof item.name === "string" && item.name)
          .map((item) => ({ name: item.name, open: Boolean(item.open) }))
      : [];
    const recent: Record<string, number> = {};
    if (parsed.recent && typeof parsed.recent === "object") {
      for (const [name, at] of Object.entries(parsed.recent)) {
        if (typeof at === "number" && Number.isFinite(at)) {
          recent[name] = at;
        }
      }
    }
    return { items: dedupe(items), recent, peek: null };
  } catch {
    return empty;
  }
}

function dedupe(items: DockItem[]): DockItem[] {
  const seen = new Set<string>();
  return items.filter((item) => {
    if (seen.has(item.name)) {
      return false;
    }
    seen.add(item.name);
    return true;
  });
}

function save(): void {
  try {
    window.localStorage.setItem(
      DOCK_STORAGE_KEY,
      JSON.stringify({ items: state.items, recent: state.recent })
    );
  } catch {
    /* Storage is unavailable; the dock still works for this page load. */
  }
}

function set(next: DockState, persist = true): void {
  state = next;
  if (persist) {
    save();
  }
  for (const listener of listeners) {
    listener();
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getDock(): DockState {
  return state;
}

export function useChatDock(): DockState {
  return useSyncExternalStore(subscribe, getDock);
}

/* Keeps at most `maxOpen` windows open: the newest open items stay open and
   older ones are minimized to tabs. */
function capOpen(items: DockItem[], maxOpen: number): DockItem[] {
  let open = 0;
  return items.map((item) => {
    if (!item.open) {
      return item;
    }
    open += 1;
    return open > maxOpen ? { ...item, open: false } : item;
  });
}

/* Opens a room as a window at the front of the dock. */
export function dockOpen(name: string, maxOpen: number): void {
  const rest = state.items.filter((item) => item.name !== name);
  const items = capOpen([{ name, open: true }, ...rest], Math.max(1, maxOpen));
  const peek = state.peek && state.peek.name === name ? null : state.peek;
  set({ ...state, items, peek });
}

/* Adds a room as a minimized tab; a room already docked keeps its place and
   its open state. */
export function dockAdd(name: string): void {
  if (state.items.some((item) => item.name === name)) {
    return;
  }
  set({ ...state, items: [{ name, open: false }, ...state.items] });
}

export function dockMinimize(name: string): void {
  set({
    ...state,
    items: state.items.map((item) => (item.name === name ? { ...item, open: false } : item)),
  });
}

export function dockClose(name: string): void {
  const peek = state.peek && state.peek.name === name ? null : state.peek;
  set({ ...state, items: state.items.filter((item) => item.name !== name), peek });
}

/* Records that the owner has the room open on the Chats page now. */
export function noteRoomVisit(name: string, at = Date.now()): void {
  const recent: Record<string, number> = {};
  for (const [key, value] of Object.entries(state.recent)) {
    if (at - value < RECENT_MS) {
      recent[key] = value;
    }
  }
  recent[name] = at;
  set({ ...state, recent });
}

export function isRecent(name: string, now = Date.now()): boolean {
  const at = state.recent[name];
  return typeof at === "number" && now - at < RECENT_MS;
}

export function showPeek(name: string, at = Date.now()): void {
  if (peekTimer !== null) {
    clearTimeout(peekTimer);
  }
  set({ ...state, peek: { name, at } }, false);
  peekTimer = setTimeout(() => {
    peekTimer = null;
    clearPeek();
  }, PEEK_MS);
}

export function clearPeek(): void {
  if (peekTimer !== null) {
    clearTimeout(peekTimer);
    peekTimer = null;
  }
  if (state.peek) {
    set({ ...state, peek: null }, false);
  }
}

/* Tests and the preview harness: start from a given state. */
export function resetDock(next?: Partial<DockState>): void {
  if (peekTimer !== null) {
    clearTimeout(peekTimer);
    peekTimer = null;
  }
  set({ items: [], recent: {}, peek: null, ...next });
}

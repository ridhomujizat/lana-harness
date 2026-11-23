import { useSyncExternalStore } from "react";

/* Spec 72 floating agent state (REQ-FA-001). Saved per browser in
   localStorage; blocked storage just starts from the defaults. The
   notification card is not saved. */

export type PanelView = "list" | "room";
export type BubbleSide = "left" | "right" | "top" | "bottom";

export interface BubblePos {
  side: BubbleSide;
  /* 0..1 along that side: top to bottom for left/right, left to right for
     top/bottom. */
  pct: number;
}

export interface FaNotif {
  name: string;
  kind: "waiting" | "reply";
  at: number;
}

export interface FaState {
  current: string;
  open: boolean;
  view: PanelView;
  /* Workbench: the pill sits tucked at the right edge (REQ-FA-012). */
  tucked: boolean;
  bubble: BubblePos;
  /* Phone: the owner dropped the bubble on the hide target (REQ-FA-042). */
  hidden: boolean;
  notif: FaNotif | null;
  /* Bumped to ask the open room's composer to take focus (Reply). */
  focusTick: number;
}

export const FA_STORAGE_KEY = "lana.floatingAgent.v1";
const OLD_DOCK_KEY = "lana.chatDock.v1";
export const REPLY_NOTIF_MS = 8000;

const DEFAULT_BUBBLE: BubblePos = { side: "right", pct: 0.62 };

function defaults(): FaState {
  return {
    current: "",
    open: false,
    view: "room",
    tucked: true,
    bubble: { ...DEFAULT_BUBBLE },
    hidden: false,
    notif: null,
    focusTick: 0,
  };
}

const SIDES: readonly string[] = ["left", "right", "top", "bottom"];

function clamp01(value: number): number {
  return Math.min(1, Math.max(0, value));
}

function load(): FaState {
  const out = defaults();
  try {
    window.localStorage.removeItem(OLD_DOCK_KEY);
    const raw = window.localStorage.getItem(FA_STORAGE_KEY);
    if (!raw) {
      return out;
    }
    const parsed = JSON.parse(raw) as Partial<FaState>;
    if (typeof parsed.current === "string") {
      out.current = parsed.current;
    }
    out.open = Boolean(parsed.open);
    out.view = parsed.view === "list" ? "list" : "room";
    if (typeof parsed.tucked === "boolean") {
      out.tucked = parsed.tucked;
    }
    const bubble = parsed.bubble;
    if (bubble && SIDES.includes(bubble.side) && typeof bubble.pct === "number" && Number.isFinite(bubble.pct)) {
      out.bubble = { side: bubble.side, pct: clamp01(bubble.pct) };
    }
    out.hidden = Boolean(parsed.hidden);
  } catch {
    /* Storage is unavailable or the value is broken: start from defaults. */
  }
  return out;
}

let state: FaState = load();
const listeners = new Set<() => void>();
let notifTimer: ReturnType<typeof setTimeout> | null = null;

function save(): void {
  try {
    const { current, open, view, tucked, bubble, hidden } = state;
    window.localStorage.setItem(FA_STORAGE_KEY, JSON.stringify({ current, open, view, tucked, bubble, hidden }));
  } catch {
    /* Storage is unavailable; the state still holds for this page load. */
  }
}

function set(patch: Partial<FaState>, persist = true): void {
  state = { ...state, ...patch };
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

export function getFloating(): FaState {
  return state;
}

export function useFloatingAgent(): FaState {
  return useSyncExternalStore(subscribe, getFloating);
}

function stopNotifTimer(): void {
  if (notifTimer !== null) {
    clearTimeout(notifTimer);
    notifTimer = null;
  }
}

/* Opens the panel (or the phone sheet) in a room. A notification for that
   room is done with. */
export function openRoom(name: string, focus = false): void {
  const notif = state.notif && state.notif.name === name ? null : state.notif;
  if (!notif) {
    stopNotifTimer();
  }
  set({
    current: name,
    open: true,
    view: "room",
    notif,
    focusTick: focus ? state.focusTick + 1 : state.focusTick,
  });
}

export function openPanel(): void {
  set({ open: true });
}

export function closePanel(): void {
  set({ open: false });
}

export function togglePanel(): void {
  set({ open: !state.open });
}

export function showList(): void {
  set({ open: true, view: "list" });
}

/* The owner opened this room somewhere else (the Chats page): it becomes the
   pill's agent without opening the panel. */
export function setCurrent(name: string): void {
  if (name && name !== state.current) {
    set({ current: name });
  }
}

export function setTucked(tucked: boolean): void {
  set({ tucked });
}

export function setBubble(bubble: BubblePos): void {
  set({ bubble: { side: bubble.side, pct: clamp01(bubble.pct) } });
}

export function setHidden(hidden: boolean): void {
  set({ hidden });
}

/* REQ-FA-030/031: a newer event replaces the card; a reply card shrinks by
   itself after 8 s, a waiting card stays. */
export function notify(name: string, kind: FaNotif["kind"], at = Date.now()): void {
  stopNotifTimer();
  set({ notif: { name, kind, at } }, false);
  if (kind === "reply") {
    notifTimer = setTimeout(() => {
      notifTimer = null;
      clearNotif();
    }, REPLY_NOTIF_MS);
  }
}

export function clearNotif(name?: string): void {
  if (!state.notif || (name !== undefined && state.notif.name !== name)) {
    return;
  }
  stopNotifTimer();
  set({ notif: null }, false);
}

/* Tests and the preview harness: start from a given state. */
export function resetFloating(next?: Partial<FaState>): void {
  stopNotifTimer();
  state = { ...defaults(), ...next };
  for (const listener of listeners) {
    listener();
  }
}

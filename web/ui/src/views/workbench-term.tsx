import { useEffect, useRef, useState, type MutableRefObject } from "react";
import { type ITheme } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

import { type AttachMode } from "../api/workbench";
import { getOrCreate, type SendInput, type TermCacheEntry, type TermCacheProps } from "./workbench-term-cache";

export type { SendInput };

/* Spec 55 REQ-WB-053: one terminal pane. xterm.js is the emulator, so the
   browser shows exactly what the program draws; the server only relays the pty
   bytes over a WebSocket (REQ-WB-041).

   The terminal itself lives in workbench-term-cache.ts, keyed by session and kept
   alive while the session is in the layout. This component is a thin wrapper: it
   re-parents the cached host into the pane box while it is mounted and detaches
   it on unmount, so switching tabs or spaces does not replay the scrollback.
   Workbench shortcuts (⌘K) clear a session's screen via the "lana:wb-clear"
   event, handled by the cache entry. */

/* The dark terminal draws no background of its own (allowTransparency):
   the pane card's colour shows through, so the focused card's brightness
   is the terminal's too (spec 61 REQ-WP-020), as in the native app. */
export const TERMINAL_THEME: ITheme = {
  background: "#00000000",
  foreground: "#E8E6E1",
  cursor: "#E8E6E1",
  cursorAccent: "#2A2826",
  selectionBackground: "#4A4845",
  black: "#2A2927",
  red: "#F08A80",
  green: "#7FD39A",
  yellow: "#E8C96A",
  blue: "#8AA8F0",
  magenta: "#C99AF0",
  cyan: "#7CC9CC",
  white: "#E8E6E1",
  brightBlack: "#8A867E",
  brightRed: "#F5A79F",
  brightGreen: "#9EE3B4",
  brightYellow: "#F0D98E",
  brightBlue: "#A9C0F5",
  brightMagenta: "#D9B6F5",
  brightCyan: "#A0DCDE",
  brightWhite: "#FFFFFF",
};

/* GoRex-style light terminal: the pane card and the terminal share one
   background, so a pane reads as a sheet, not a black box. */
export const TERMINAL_THEME_LIGHT: ITheme = {
  background: "#FDFCFA",
  foreground: "#2A2825",
  cursor: "#2A2825",
  cursorAccent: "#FDFCFA",
  selectionBackground: "#DCD6EE",
  black: "#2A2825",
  red: "#C8423A",
  green: "#2F8A4E",
  yellow: "#A87A12",
  blue: "#3563C9",
  magenta: "#8D4BC2",
  cyan: "#22808A",
  white: "#D9D5CE",
  brightBlack: "#8A867E",
  brightRed: "#DE5B52",
  brightGreen: "#3FA562",
  brightYellow: "#C28F1B",
  brightBlue: "#4C7BE0",
  brightMagenta: "#A462D9",
  brightCyan: "#2E9AA5",
  brightWhite: "#F4F2EE",
};

/* The app theme ("light" | "dark" on <html data-theme>), kept live. */
export function useDocTheme(): "light" | "dark" {
  const read = () =>
    typeof document !== "undefined" && document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "light";
  const [theme, setTheme] = useState<"light" | "dark">(read);
  useEffect(() => {
    if (typeof MutationObserver === "undefined") return;
    const observer = new MutationObserver(() => setTheme(read()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => observer.disconnect();
  }, []);
  return theme;
}

export interface TermPaneProps {
  sessionId: string;
  mode: AttachMode;
  /* The pty process id. A restart keeps the session id but changes the pid, so
     the cache rebuilds the terminal and the core replays the fresh run. */
  pid?: number;
  focused?: boolean;
  fontSize?: number;
  onFocus?: () => void;
  /* Filled with a function that writes input to the session (the mobile key
     bar uses it). */
  sendRef?: MutableRefObject<SendInput | null>;
  /* Rewrites typed input before it is sent (the mobile ctrl toggle). */
  transformRef?: MutableRefObject<((data: string) => string) | null>;
  /* The session's current pty size, from the session events. A client that
     does not own the size renders at this size instead of fitting its box. */
  sessionSize?: { cols: number; rows: number };
  /* Phone view mode: the pane scrolls sideways instead of clipping. */
  scrollable?: boolean;
  /* Told when this client takes or loses the pty size. */
  onOwnerChange?: (owner: boolean) => void;
  /* Filled with a function that takes the pty size for this client. */
  claimRef?: MutableRefObject<(() => void) | null>;
  /* Filled with focus/blur of the terminal (the phone keyboard toggle). */
  keyboardRef?: MutableRefObject<{ focus: () => void; blur: () => void } | null>;
  /* Spec 57 (REQ-RA-035): told when the server closes a control attachment
     because remote control locked (close code 4403). */
  onControlLost?: () => void;
  /* Spec 61 REQ-WP-010: a ⌘/Ctrl+click (a tap on touch) on a file path, or an
     OSC 8 file:// link, asks the page to preview it. The pane owns no preview
     state. */
  onOpenFile?: (sessionId: string, link: string, fallback?: string) => void;
}

export function TermPane({
  sessionId,
  mode,
  pid = 0,
  focused,
  fontSize = 13,
  onFocus,
  sendRef,
  transformRef,
  sessionSize,
  scrollable,
  onOwnerChange,
  claimRef,
  keyboardRef,
  onControlLost,
  onOpenFile,
}: TermPaneProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const entryRef = useRef<TermCacheEntry | null>(null);
  const docTheme = useDocTheme();

  /* The latest props, handed to the cached entry on every render. */
  const next: TermCacheProps = {
    fontSize,
    theme: docTheme === "dark" ? TERMINAL_THEME : TERMINAL_THEME_LIGHT,
    focused,
    scrollable,
    sessionSize,
    onFocus,
    onOwnerChange,
    onControlLost,
    onOpenFile,
    sendRef,
    transformRef,
    claimRef,
    keyboardRef,
  };

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const entry = getOrCreate({
      sessionId,
      mode,
      pid,
      fontSize: next.fontSize,
      theme: next.theme,
    });
    entryRef.current = entry;
    entry.update(next);
    entry.attach(container);
    return () => {
      entry.detach();
      entryRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, mode, pid]);

  /* Font size, theme, callbacks and the muted/mobile refs can change while the
     entry is cached; the entry applies them to the live terminal. */
  useEffect(() => {
    entryRef.current?.update(next);
  });

  /* The cached host fills this box; its "wb-xterm" class carries the styling. */
  return <div ref={containerRef} style={{ width: "100%", height: "100%" }} />;
}

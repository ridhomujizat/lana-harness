import { useEffect, useRef, useState, type MutableRefObject } from "react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";

import { attachURL, type AttachMode } from "../api/workbench";
import { terminalKey } from "./workbench-model";

/* Spec 55 REQ-WB-053: one terminal pane. xterm.js is the emulator, so the
   browser shows exactly what the program draws; the server only relays the
   pty bytes over a WebSocket (REQ-WB-041). */

export const TERMINAL_THEME: ITheme = {
  background: "#191817",
  foreground: "#E8E6E1",
  cursor: "#E8E6E1",
  cursorAccent: "#191817",
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

export type SendInput = (data: string) => void;

export interface TermPaneProps {
  sessionId: string;
  mode: AttachMode;
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
}

const encoder = new TextEncoder();

/* Size ownership (REQ-WB-054): one pty has one size, so only the client that
   was used last sets it, like tmux's `window-size latest`. A control client
   claims the size when it is opened in a focused window, when the window gets
   focus, and when the owner types. Every other client follows the session's
   size, so a full-screen program is never drawn for one size and shown at
   another. */
export function TermPane({
  sessionId,
  mode,
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
}: TermPaneProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [isOwner, setIsOwner] = useState(false);
  const docTheme = useDocTheme();
  const themeRef = useRef(docTheme);
  const ownerCbRef = useRef(onOwnerChange);
  useEffect(() => {
    ownerCbRef.current = onOwnerChange;
  }, [onOwnerChange]);
  const termRef = useRef<Terminal | null>(null);
  const followRef = useRef<((cols: number, rows: number) => void) | null>(null);
  const refitRef = useRef<(() => void) | null>(null);
  const fontRef = useRef(fontSize);
  const onFocusRef = useRef(onFocus);
  useEffect(() => {
    onFocusRef.current = onFocus;
  }, [onFocus]);
  const sizeRef = useRef(sessionSize);
  useEffect(() => {
    sizeRef.current = sessionSize;
    if (sessionSize) followRef.current?.(sessionSize.cols, sessionSize.rows);
  }, [sessionSize?.cols, sessionSize?.rows]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let disposed = false;
    let socket: WebSocket | null = null;
    let retry = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let resizeTimer: ReturnType<typeof setTimeout> | undefined;
    let owner = false;
    let claimedAt = 0;
    const setOwner = (value: boolean) => {
      if (owner === value) return;
      owner = value;
      setIsOwner(value);
      ownerCbRef.current?.(value);
    };

    const term = new Terminal({
      fontFamily: "'Geist Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
      fontSize: fontRef.current,
      lineHeight: 1.15,
      theme: themeRef.current === "dark" ? TERMINAL_THEME : TERMINAL_THEME_LIGHT,
      cursorBlink: mode === "control",
      disableStdin: mode !== "control",
      scrollback: 5000,
      allowProposedApi: true,
      macOptionIsMeta: true,
    });
    termRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = "11";
    term.loadAddon(new WebLinksAddon());
    term.open(host);
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => webgl.dispose());
      term.loadAddon(webgl);
    } catch {
      /* No WebGL: xterm keeps its DOM renderer. */
    }

    const open = () => socket !== null && socket.readyState === WebSocket.OPEN;

    const fitBox = () => {
      if (host.clientWidth > 0 && host.clientHeight > 0) {
        try {
          fit.fit();
        } catch {
          /* The pane is hidden or detached. */
        }
      }
    };

    const sendResize = () => {
      if (!owner || mode !== "control" || !open()) return;
      socket!.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };

    /* Take the pty size: fit this box and tell the core. */
    const claim = () => {
      if (mode !== "control" || disposed) return;
      setOwner(true);
      claimedAt = Date.now();
      fitBox();
      sendResize();
    };

    /* Show the session at its own size. Another client's resize while this one
       owned the size means that client was used last. */
    const follow = (cols: number, rows: number) => {
      if (disposed || cols <= 0 || rows <= 0) return;
      if (owner) {
        if (Date.now() - claimedAt < 1500 || (cols === term.cols && rows === term.rows)) return;
        setOwner(false);
      }
      if (term.cols !== cols || term.rows !== rows) term.resize(cols, rows);
    };
    followRef.current = follow;
    /* A new font size changes how many cells fit: the owner refits and tells
       the core; a follower keeps the session's size. */
    refitRef.current = () => {
      if (owner) fitBox();
    };
    if (keyboardRef) keyboardRef.current = { focus: () => term.focus(), blur: () => term.blur() };
    if (claimRef) claimRef.current = claim;

    const send: SendInput = (data) => {
      if (mode !== "control" || !open()) return;
      if (!owner) claim();
      socket!.send(encoder.encode(data));
    };
    if (sendRef) sendRef.current = send;

    /* macOS terminal keys (Shift+Enter newline, Cmd/Option editing). The
       browser default is cancelled too: Cmd+Left would go back in history. */
    term.attachCustomKeyEventHandler((e) => {
      const seq = terminalKey(e);
      if (seq === null) return true;
      e.preventDefault();
      if (e.type === "keydown") send(seq);
      return false;
    });

    const dataSub = term.onData((data) => {
      const transform = transformRef?.current;
      send(transform ? transform(data) : data);
    });
    const binarySub = term.onBinary((data) => {
      if (mode !== "control" || !open()) return;
      if (!owner) claim();
      const bytes = new Uint8Array(data.length);
      for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff;
      socket!.send(bytes);
    });
    const resizeSub = term.onResize(() => {
      if (!owner) return;
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(sendResize, 60);
    });

    const connect = () => {
      if (disposed) return;
      const ws = new WebSocket(attachURL(sessionId, mode));
      ws.binaryType = "arraybuffer";
      socket = ws;
      ws.onopen = () => {
        retry = 0;
        /* The core replays its buffer on every attach (REQ-WB-020), so the
           screen starts clean to avoid printing the history twice. */
        term.reset();
        if (mode === "control" && typeof document !== "undefined" && document.hasFocus()) {
          claim();
        } else {
          setOwner(false);
          const size = sizeRef.current;
          if (size) follow(size.cols, size.rows);
        }
      };
      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data));
        }
      };
      ws.onclose = (event) => {
        if (disposed || socket !== ws) return;
        socket = null;
        /* 1000: the session's program ended (the stream finished); 4404: the
           session is gone; 1008: the server refused (auth/origin). A restart
           remounts the pane, so none of these reconnect. */
        if (event.code === 1000 || event.code === 4404 || event.code === 1008) return;
        retry = Math.min(retry + 1, 5);
        retryTimer = setTimeout(connect, retry * 1000);
      };
    };

    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => {
            if (owner) fitBox();
          });
    observer?.observe(host);
    /* Touch scrolling (phones): xterm.js does not turn a finger drag into
       scrolling. A drag scrolls the scrollback of the normal screen; on the
       alternate screen (full-screen programs such as Claude Code) it becomes
       mouse-wheel reports when the program tracks the mouse, else arrow keys,
       which is what native terminals do. */
    let touchY: number | null = null;
    let touchAcc = 0;
    let touchMoved = false;
    const scrollOne = (down: boolean) => {
      if (term.buffer.active.type === "alternate") {
        if (term.modes.mouseTrackingMode !== "none") {
          const col = Math.max(1, Math.floor(term.cols / 2));
          const row = Math.max(1, Math.floor(term.rows / 2));
          send("\x1b[<" + (down ? 65 : 64) + ";" + col + ";" + row + "M");
        } else {
          send(down ? "\x1b[B" : "\x1b[A");
        }
      } else {
        term.scrollLines(down ? 1 : -1);
      }
    };
    const touchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      touchY = e.touches[0]!.clientY;
      touchAcc = 0;
      touchMoved = false;
    };
    const touchMove = (e: TouchEvent) => {
      if (touchY === null || e.touches.length !== 1) return;
      const y = e.touches[0]!.clientY;
      touchAcc += touchY - y;
      touchY = y;
      const lineHeight = Math.max(8, host.clientHeight / Math.max(1, term.rows));
      if (!touchMoved && Math.abs(touchAcc) < 6) return;
      touchMoved = true;
      e.preventDefault();
      while (Math.abs(touchAcc) >= lineHeight) {
        const down = touchAcc > 0;
        touchAcc += down ? -lineHeight : lineHeight;
        scrollOne(down);
      }
    };
    const touchEnd = () => {
      touchY = null;
    };
    host.addEventListener("touchstart", touchStart, { passive: true });
    host.addEventListener("touchmove", touchMove, { passive: false });
    host.addEventListener("touchend", touchEnd, { passive: true });
    host.addEventListener("touchcancel", touchEnd, { passive: true });

    const focusHandler = () => {
      onFocusRef.current?.();
      if (!owner) claim();
    };
    const windowFocus = () => {
      if (!owner && host.isConnected && host.clientWidth > 0) claim();
    };
    host.addEventListener("mousedown", focusHandler);
    host.addEventListener("touchstart", focusHandler, { passive: true });
    window.addEventListener("focus", windowFocus);

    const initial = sizeRef.current;
    if (mode === "control") fitBox();
    else if (initial) follow(initial.cols, initial.rows);
    connect();

    return () => {
      disposed = true;
      followRef.current = null;
      refitRef.current = null;
      if (keyboardRef) keyboardRef.current = null;
      if (claimRef && claimRef.current === claim) claimRef.current = null;
      clearTimeout(retryTimer);
      clearTimeout(resizeTimer);
      observer?.disconnect();
      host.removeEventListener("mousedown", focusHandler);
      host.removeEventListener("touchstart", focusHandler);
      window.removeEventListener("focus", windowFocus);
      host.removeEventListener("touchstart", touchStart);
      host.removeEventListener("touchmove", touchMove);
      host.removeEventListener("touchend", touchEnd);
      host.removeEventListener("touchcancel", touchEnd);
      dataSub.dispose();
      binarySub.dispose();
      resizeSub.dispose();
      if (sendRef && sendRef.current === send) sendRef.current = null;
      socket?.close();
      socket = null;
      term.dispose();
      termRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, mode]);

  useEffect(() => {
    fontRef.current = fontSize;
    const term = termRef.current;
    if (!term || term.options.fontSize === fontSize) return;
    term.options.fontSize = fontSize;
    refitRef.current?.();
  }, [fontSize]);

  useEffect(() => {
    themeRef.current = docTheme;
    const term = termRef.current;
    if (term) term.options.theme = docTheme === "dark" ? TERMINAL_THEME : TERMINAL_THEME_LIGHT;
  }, [docTheme]);

  useEffect(() => {
    if (focused && mode === "control") termRef.current?.focus();
  }, [focused, mode]);

  return <div className={"wb-xterm" + (scrollable || (mode === "control" && !isOwner) ? " wb-xterm-fixed" : "")} ref={hostRef} />;
}

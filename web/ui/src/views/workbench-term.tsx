import { useEffect, useRef, useState, type MutableRefObject } from "react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";

import { attachURL, uploadWorkbenchDrops, type AttachMode } from "../api/workbench";
import { dropText, openTerminalLink, takeNativeDragPaths, terminalKey, type NativeDrag } from "./workbench-model";
import { splitReplay, startsWithSizeMarker } from "./workbench-replay";
import { activationAllowed, matchLinksInLine } from "./workbench-links";

/* Spec 55 REQ-WB-053: one terminal pane. xterm.js is the emulator, so the
   browser shows exactly what the program draws; the server only relays the
   pty bytes over a WebSocket (REQ-WB-041). */

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
  /* Spec 57 (REQ-RA-035): told when the server closes a control attachment
     because remote control locked (close code 4403). */
  onControlLost?: () => void;
  /* Spec 61 REQ-WP-010: a ⌘/Ctrl+click (a tap on touch) on a file path, or an
     OSC 8 file:// link, asks the page to preview it. The pane owns no preview
     state. */
  onOpenFile?: (sessionId: string, link: string) => void;
}

const encoder = new TextEncoder();

const TERM_FONT = "'Geist Mono', ui-monospace, SFMono-Regular, Menlo, monospace";
/* The same font list, spelled differently: setting it makes xterm measure the
   cell again and redraw its glyph atlas (it ignores an unchanged value). */
const TERM_FONT_ALT = "\"Geist Mono\", ui-monospace, SFMono-Regular, Menlo, monospace";

function dragHasFiles(dt: DataTransfer | null): boolean {
  return !!dt && Array.from(dt.types).includes("Files");
}

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
  onControlLost,
  onOpenFile,
}: TermPaneProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [isOwner, setIsOwner] = useState(false);
  const [dropping, setDropping] = useState(false);
  const docTheme = useDocTheme();
  const themeRef = useRef(docTheme);
  const ownerCbRef = useRef(onOwnerChange);
  useEffect(() => {
    ownerCbRef.current = onOwnerChange;
  }, [onOwnerChange]);
  const lostCbRef = useRef(onControlLost);
  useEffect(() => {
    lostCbRef.current = onControlLost;
  }, [onControlLost]);
  const openFileRef = useRef(onOpenFile);
  useEffect(() => {
    openFileRef.current = onOpenFile;
  }, [onOpenFile]);
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
    /* Attach replay (workbench-replay.ts): while replaying, the pane takes
       each size the core marked in the replay, and its own fitting, size
       reports and following wait until the replay-end marker. */
    let replaying = false;
    let parsing = false;
    let expectFirst = false;
    let carry: Uint8Array = new Uint8Array(0);
    let gen = 0;
    let pending = 0;
    let chain: Promise<void> = Promise.resolve();
    let replayTimer: ReturnType<typeof setTimeout> | undefined;
    const setOwner = (value: boolean) => {
      if (owner === value) return;
      owner = value;
      setIsOwner(value);
      ownerCbRef.current?.(value);
    };

    const term = new Terminal({
      fontFamily: TERM_FONT,
      fontSize: fontRef.current,
      lineHeight: 1.15,
      allowTransparency: true,
      theme: themeRef.current === "dark" ? TERMINAL_THEME : TERMINAL_THEME_LIGHT,
      cursorBlink: mode === "control",
      disableStdin: mode !== "control",
      scrollback: 5000,
      allowProposedApi: true,
      macOptionIsMeta: true,
      linkHandler: {
        /* A file:// OSC 8 link goes to the preview on ⌘/Ctrl+click (a tap on
           touch); everything else keeps opening in a new tab (REQ-WP-010). */
        allowNonHttpProtocols: true,
        activate: (event, uri) => {
          if (uri.startsWith("file://")) {
            if (activationAllowed(event)) openFileRef.current?.(sessionId, uri);
            return;
          }
          openTerminalLink(uri);
        },
      },
    });
    termRef.current = term;
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new Unicode11Addon());
    term.unicode.activeVersion = "11";
    term.loadAddon(new WebLinksAddon((_e, uri) => openTerminalLink(uri)));
    term.open(host);
    /* Spec 61 REQ-WP-010: offer the file paths on a line to the preview. The
       buffer range is 1-based and inclusive on both ends. */
    const linkProvider = term.registerLinkProvider({
      provideLinks: (lineNumber, callback) => {
        const line = term.buffer.active.getLine(lineNumber - 1);
        if (!line) {
          callback(undefined);
          return;
        }
        const matches = matchLinksInLine(line.translateToString(true));
        if (!matches.length) {
          callback(undefined);
          return;
        }
        callback(
          matches.map((match) => ({
            range: {
              start: { x: match.start + 1, y: lineNumber },
              end: { x: match.end, y: lineNumber },
            },
            text: match.text,
            activate: (event: MouseEvent) => {
              if (!activationAllowed(event)) return;
              openFileRef.current?.(sessionId, match.text);
            },
          }))
        );
      },
    });
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => webgl.dispose());
      term.loadAddon(webgl);
    } catch {
      /* No WebGL: xterm keeps its DOM renderer. */
    }

    const open = () => socket !== null && socket.readyState === WebSocket.OPEN;

    /* Redraw every glyph. The WebGL renderer keeps glyphs in a texture atlas
       that WebKit can lose or corrupt (sleep, a hidden tab, GPU pressure); the
       screen then shows rows with missing characters until the atlas is
       rebuilt. */
    const repaint = () => {
      if (disposed) return;
      try {
        term.clearTextureAtlas();
      } catch {
        /* Not open yet. */
      }
      term.refresh(0, Math.max(0, term.rows - 1));
    };

    const fitBox = () => {
      if (replaying) return;
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
      if (disposed || replaying || cols <= 0 || rows <= 0) return;
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
    /* Geist Mono is a web font. When xterm opens before it has loaded, the
       cell is measured and the glyphs are drawn with the fallback font, and
       later text is drawn with Geist Mono into those cells: characters overlap
       or vanish. Once the font is in, measure and draw again. */
    const fonts = typeof document !== "undefined" ? document.fonts : undefined;
    if (fonts && !fonts.check(`${fontRef.current}px 'Geist Mono'`)) {
      Promise.all([
        fonts.load(`400 ${fontRef.current}px 'Geist Mono'`),
        fonts.load(`500 ${fontRef.current}px 'Geist Mono'`),
      ])
        .catch(() => undefined)
        .then(() => {
          if (disposed) return;
          term.options.fontFamily = term.options.fontFamily === TERM_FONT ? TERM_FONT_ALT : TERM_FONT;
          if (owner) fitBox();
          repaint();
        });
    }
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
      if (!owner || replaying) return;
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(sendResize, 60);
    });

    /* Runs fn after everything queued before it; dropped when the socket it
       was queued for is gone. */
    const enqueue = (fn: () => Promise<void> | void) => {
      const mine = gen;
      pending++;
      chain = chain
        .then(() => (disposed || mine !== gen ? undefined : fn()))
        .catch(() => undefined)
        .finally(() => {
          pending--;
        });
    };
    const writeAsync = (bytes: Uint8Array) => new Promise<void>((resolve) => term.write(bytes, resolve));
    /* The replay is over: back to this pane's own size (owner) or the
       session's size (follower). */
    const finishReplay = () => {
      clearTimeout(replayTimer);
      if (!replaying || disposed) return;
      replaying = false;
      if (owner) {
        fitBox();
        sendResize();
      } else {
        const size = sizeRef.current;
        if (size && (term.cols !== size.cols || term.rows !== size.rows)) term.resize(size.cols, size.rows);
      }
      /* Show the pane again once xterm has drawn the last replayed bytes. */
      term.write(new Uint8Array(0), () => {
        if (disposed) return;
        term.scrollToBottom();
        host.style.visibility = "";
      });
    };
    const receive = (bytes: Uint8Array) => {
      if (expectFirst) {
        expectFirst = false;
        /* A core without markers: plain replay, as before. */
        parsing = startsWithSizeMarker(bytes);
        if (!parsing) finishReplay();
      }
      if (!parsing) {
        if (pending === 0) term.write(bytes);
        else enqueue(() => writeAsync(bytes));
        return;
      }
      const out = splitReplay(carry, bytes);
      carry = out.carry;
      for (const part of out.parts) {
        if (part.kind === "data") {
          const data = part.bytes;
          enqueue(() => writeAsync(data));
        } else if (part.kind === "size") {
          /* Only the width matters for where a redraw lands (a wrapped
             prompt). Height-only changes (the attach redraw nudge records two
             per attach) would make xterm scroll and reflow for nothing. */
          const { cols } = part;
          enqueue(() => {
            if (replaying && term.cols !== cols) term.resize(cols, term.rows);
          });
        } else {
          parsing = false;
          enqueue(finishReplay);
        }
      }
      if (!parsing && carry.length > 0) {
        const rest = carry;
        carry = new Uint8Array(0);
        enqueue(() => writeAsync(rest));
      }
    };

    const connect = () => {
      if (disposed) return;
      const ws = new WebSocket(attachURL(sessionId, mode));
      ws.binaryType = "arraybuffer";
      socket = ws;
      ws.onopen = () => {
        retry = 0;
        /* A replay cut short by a dropped socket is over. */
        replaying = false;
        clearTimeout(replayTimer);
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
        gen++;
        carry = new Uint8Array(0);
        expectFirst = true;
        parsing = false;
        replaying = true;
        /* Owner report 2026-11-19: switching tabs or spaces remounts the pane
           and Claude Code's whole history scrolled past while it replayed.
           The pane stays hidden until the replay is drawn. */
        host.style.visibility = "hidden";
        clearTimeout(replayTimer);
        /* A replay that never ends (a dropped marker) must not freeze the
           pane's size. */
        replayTimer = setTimeout(() => {
          parsing = false;
          enqueue(finishReplay);
        }, 8000);
      };
      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) receive(new Uint8Array(event.data));
      };
      ws.onclose = (event) => {
        if (disposed || socket !== ws) return;
        socket = null;
        /* 1000: the session's program ended (the stream finished); 4404: the
           session is gone; 1008: the server refused (auth/origin). A restart
           remounts the pane, so none of these reconnect. */
        if (event.code === 1000 || event.code === 4404 || event.code === 1008) return;
        /* 4403: remote control locked (spec 57). The page switches the pane
           to view mode, which remounts it. */
        if (event.code === 4403) {
          lostCbRef.current?.();
          return;
        }
        retry = Math.min(retry + 1, 5);
        retryTimer = setTimeout(connect, retry * 1000);
      };
    };

    /* A pane in a hidden tab has no size; when it shows again its canvas may
       hold an old frame, so it is redrawn. */
    let hidden = host.clientWidth === 0 || host.clientHeight === 0;
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => {
            const nowHidden = host.clientWidth === 0 || host.clientHeight === 0;
            if (owner) fitBox();
            if (hidden && !nowHidden) repaint();
            hidden = nowHidden;
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
      repaint();
    };
    const visibility = () => {
      if (document.visibilityState === "visible") repaint();
    };
    document.addEventListener("visibilitychange", visibility);
    /* ⌘K (Workbench shortcuts): clear this terminal's screen and scrollback. */
    const clearHandler = (e: Event) => {
      if ((e as CustomEvent<string>).detail === sessionId) term.clear();
    };
    window.addEventListener("lana:wb-clear", clearHandler);

    /* Drag and drop, as in Ghostty or Terminal: a dropped file is typed as its
       path. The macOS app gives the real paths; a browser uploads the files
       to the daemon, which returns where it saved them. Text is typed as is.
       The paste goes through xterm, so a program that asked for bracketed
       paste (Claude Code) sees one paste and can attach an image path. */
    let dragDepth = 0;
    const acceptsDrop = (dt: DataTransfer | null) =>
      mode === "control" && !!dt && (dragHasFiles(dt) || Array.from(dt.types).includes("text/plain"));
    const dragEnter = (e: DragEvent) => {
      if (!acceptsDrop(e.dataTransfer)) return;
      e.preventDefault();
      dragDepth++;
      setDropping(true);
    };
    const dragOver = (e: DragEvent) => {
      if (!acceptsDrop(e.dataTransfer)) return;
      e.preventDefault();
      e.dataTransfer!.dropEffect = "copy";
    };
    const dragLeave = () => {
      dragDepth = Math.max(0, dragDepth - 1);
      if (dragDepth === 0) setDropping(false);
    };
    const typeDrop = (text: string) => {
      if (disposed || text === "") return;
      onFocusRef.current?.();
      if (!owner) claim();
      term.focus();
      term.paste(text);
    };
    const drop = (e: DragEvent) => {
      const dt = e.dataTransfer;
      dragDepth = 0;
      setDropping(false);
      if (!acceptsDrop(dt)) return;
      e.preventDefault();
      e.stopPropagation();
      const holder = window as unknown as { lanaNativeDrag?: NativeDrag | null };
      if (dragHasFiles(dt)) {
        const native = takeNativeDragPaths(holder, Date.now());
        if (native) {
          typeDrop(dropText(native));
          return;
        }
        const files = Array.from(dt!.files);
        if (files.length === 0) return;
        uploadWorkbenchDrops(files)
          .then((paths) => typeDrop(dropText(paths)))
          .catch(() => {
            /* The upload failed (too large, control locked): nothing is typed. */
          });
        return;
      }
      holder.lanaNativeDrag = null;
      typeDrop(dt!.getData("text/plain"));
    };
    host.addEventListener("dragenter", dragEnter);
    host.addEventListener("dragover", dragOver);
    host.addEventListener("dragleave", dragLeave);
    host.addEventListener("drop", drop);
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
      clearTimeout(replayTimer);
      observer?.disconnect();
      host.removeEventListener("mousedown", focusHandler);
      host.removeEventListener("touchstart", focusHandler);
      window.removeEventListener("focus", windowFocus);
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("lana:wb-clear", clearHandler);
      host.removeEventListener("dragenter", dragEnter);
      host.removeEventListener("dragover", dragOver);
      host.removeEventListener("dragleave", dragLeave);
      host.removeEventListener("drop", drop);
      host.removeEventListener("touchstart", touchStart);
      host.removeEventListener("touchmove", touchMove);
      host.removeEventListener("touchend", touchEnd);
      host.removeEventListener("touchcancel", touchEnd);
      dataSub.dispose();
      binarySub.dispose();
      resizeSub.dispose();
      linkProvider.dispose();
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

  return (
    <div
      className={
        "wb-xterm" +
        (scrollable || (mode === "control" && !isOwner) ? " wb-xterm-fixed" : "") +
        (dropping ? " is-drop" : "")
      }
      ref={hostRef}
    />
  );
}

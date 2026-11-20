import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import type { MutableRefObject } from "react";

import { attachURL, uploadWorkbenchDrops, type AttachMode } from "../api/workbench";
import { dropText, openTerminalLink, takeNativeDragPaths, terminalKey, type NativeDrag } from "./workbench-model";
import { splitReplay, startsWithSizeMarker } from "./workbench-replay";
import { activationAllowed, matchLinksInLine } from "./workbench-links";

/* The Workbench terminal cache (owner report 2026-11-19: switching tabs or
   spaces remounted the pane and Claude Code's whole history scrolled past while
   it replayed).

   The Workbench keeps one live terminal per session for as long as the session
   is in the layout. The xterm.js Terminal, its addons, its attach WebSocket and
   its host <div> live here, not in a React effect; TermPane only re-parents the
   host into the pane box while it is mounted. A remount re-uses the entry, so
   the core does not attach again and no replay happens; the scrollback is kept.

   An entry that is not shown (detached from any container, hidden or zero-size)
   still receives output from the socket, but it never sends a resize message and
   never claims the pty size, and its WebGL renderer is disposed (browsers cap
   WebGL contexts at about 16) until it is shown again, falling back to the DOM
   renderer meanwhile.

   Eviction: an entry is dropped when its session leaves the layout
   (workbench.tsx prunes the cache against its live session ids), on page unload,
   and when the cache grows past MAX_CACHED_TERMS (the least recently shown entry
   goes first). A dropped session replays on its next mount, as before.

   Replay is unchanged: it runs on the first attach and on a real reconnect after
   a dropped socket (the size markers and hidden-until-drawn logic below are the
   same as the effect used to be). */

export type SendInput = (data: string) => void;

const encoder = new TextEncoder();

const TERM_FONT = "'Geist Mono', ui-monospace, SFMono-Regular, Menlo, monospace";
/* The same font list, spelled differently: setting it makes xterm measure the
   cell again and redraw its glyph atlas (it ignores an unchanged value). */
const TERM_FONT_ALT = "\"Geist Mono\", ui-monospace, SFMono-Regular, Menlo, monospace";

/* How many sessions keep a live terminal. Beyond this the least recently shown
   entry is dropped (WebGL contexts are the scarce resource). */
export const MAX_CACHED_TERMS = 16;

function dragHasFiles(dt: DataTransfer | null): boolean {
  return !!dt && Array.from(dt.types).includes("Files");
}

export interface TermCacheInit {
  sessionId: string;
  mode: AttachMode;
  /* The session's pty process id. A restart keeps the id but changes the pid;
     that is a fresh run, so the entry is rebuilt and the core replays it (as a
     remount used to do). */
  pid: number;
  fontSize: number;
  theme: ITheme;
}

export interface TermCacheProps {
  fontSize: number;
  theme: ITheme;
  focused?: boolean;
  scrollable?: boolean;
  sessionSize?: { cols: number; rows: number };
  onFocus?: () => void;
  onOwnerChange?: (owner: boolean) => void;
  onControlLost?: () => void;
  onOpenFile?: (sessionId: string, link: string) => void;
  sendRef?: MutableRefObject<SendInput | null>;
  transformRef?: MutableRefObject<((data: string) => string) | null>;
  claimRef?: MutableRefObject<(() => void) | null>;
  keyboardRef?: MutableRefObject<{ focus: () => void; blur: () => void } | null>;
}

export interface TermCacheEntry {
  readonly sessionId: string;
  readonly host: HTMLDivElement;
  /* Bumped every time the entry is shown; the LRU eviction key. */
  lastShown: number;
  /* True when the entry can serve this init: the same session and mode, and the
     same pty process (a restart is a new attachment). */
  matches(init: TermCacheInit): boolean;
  /* Re-parent the host into container and behave as shown. */
  attach(container: HTMLElement): void;
  /* Remove the host from its container but keep the terminal and socket alive. */
  detach(): void;
  /* Apply the latest props to the cached terminal. */
  update(props: TermCacheProps): void;
  dispose(): void;
}

function createEntry(init: TermCacheInit): TermCacheEntry {
  const { sessionId, mode } = init;
  const pid = init.pid;

  const host = document.createElement("div");
  host.className = "wb-xterm";

  let disposed = false;
  let attached = false;
  let opened = false;
  let shown = false;
  let socket: WebSocket | null = null;
  let retry = 0;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let replayTimer: ReturnType<typeof setTimeout> | undefined;
  let owner = false;
  let claimedAt = 0;
  /* Attach replay (workbench-replay.ts): while replaying, the pane takes each
     size the core marked in the replay, and its own fitting, size reports and
     following wait until the replay-end marker. */
  let replaying = false;
  let parsing = false;
  let expectFirst = false;
  let carry: Uint8Array = new Uint8Array(0);
  let gen = 0;
  let pending = 0;
  let chain: Promise<void> = Promise.resolve();
  let wasAtBottom = true;
  let dropping = false;

  /* Latest props, updated by TermPane on every render. */
  let fontSize = init.fontSize;
  let theme = init.theme;
  let sessionSize: { cols: number; rows: number } | undefined;
  let focused = false;
  let scrollable = false;
  let onFocus: (() => void) | undefined;
  let onOwnerChange: ((owner: boolean) => void) | undefined;
  let onControlLost: (() => void) | undefined;
  let onOpenFile: ((sessionId: string, link: string) => void) | undefined;
  let sendRef: MutableRefObject<SendInput | null> | undefined;
  let transformRef: MutableRefObject<((data: string) => string) | null> | undefined;
  let claimRef: MutableRefObject<(() => void) | null> | undefined;
  let keyboardRef: MutableRefObject<{ focus: () => void; blur: () => void } | null> | undefined;

  let webgl: WebglAddon | null = null;
  let lastShown = ++clock;

  const term = new Terminal({
    fontFamily: TERM_FONT,
    fontSize,
    lineHeight: 1.15,
    allowTransparency: true,
    theme,
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
          if (activationAllowed(event)) onOpenFile?.(sessionId, uri);
          return;
        }
        openTerminalLink(uri);
      },
    },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new Unicode11Addon());
  term.unicode.activeVersion = "11";
  term.loadAddon(new WebLinksAddon((_e, uri) => openTerminalLink(uri)));
  /* term.open() runs on the first attach, when the host is in the pane box, so
     xterm measures the web font against a real layout (as the effect used to). */
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
            onOpenFile?.(sessionId, match.text);
          },
        }))
      );
    },
  });

  /* WebGL is created only while the pane is shown: browsers cap WebGL contexts
     at about 16, and the cache can hold that many terminals. */
  const createWebgl = () => {
    if (webgl || disposed) return;
    try {
      const gl = new WebglAddon();
      gl.onContextLoss(() => {
        gl.dispose();
        if (webgl === gl) webgl = null;
      });
      term.loadAddon(gl);
      webgl = gl;
    } catch {
      /* No WebGL: xterm keeps its DOM renderer. */
      webgl = null;
    }
  };
  const disposeWebgl = () => {
    const gl = webgl;
    webgl = null;
    if (!gl) return;
    try {
      gl.dispose();
    } catch {
      /* Already gone. */
    }
  };

  const kbd = { focus: () => term.focus(), blur: () => term.blur() };
  const open = () => socket !== null && socket.readyState === WebSocket.OPEN;

  /* Redraw every glyph. The WebGL renderer keeps glyphs in a texture atlas that
     WebKit can lose or corrupt (sleep, a hidden tab, GPU pressure); the screen
     then shows rows with missing characters until the atlas is rebuilt. */
  const repaint = () => {
    if (disposed || !opened) return;
    try {
      term.clearTextureAtlas();
    } catch {
      /* Not open yet. */
    }
    term.refresh(0, Math.max(0, term.rows - 1));
  };

  const updateHostClass = () => {
    host.className =
      "wb-xterm" +
      (scrollable || (mode === "control" && !owner) ? " wb-xterm-fixed" : "") +
      (dropping ? " is-drop" : "");
  };

  const applyRefs = () => {
    if (sendRef) sendRef.current = send;
    if (claimRef) claimRef.current = claim;
    if (keyboardRef) keyboardRef.current = kbd;
  };
  const clearRefs = () => {
    if (sendRef && sendRef.current === send) sendRef.current = null;
    if (claimRef && claimRef.current === claim) claimRef.current = null;
    if (keyboardRef && keyboardRef.current === kbd) keyboardRef.current = null;
  };

  const fitBox = () => {
    if (replaying || !shown) return;
    if (host.clientWidth > 0 && host.clientHeight > 0) {
      try {
        fit.fit();
      } catch {
        /* The pane is hidden or detached. */
      }
    }
  };

  const sendResize = () => {
    if (!shown || !owner || mode !== "control" || !open()) return;
    socket!.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
  };

  const setOwner = (value: boolean) => {
    if (owner === value) return;
    owner = value;
    updateHostClass();
    onOwnerChange?.(value);
  };

  /* Take the pty size: fit this box and tell the core. */
  const claim = () => {
    if (mode !== "control" || disposed || !shown) return;
    setOwner(true);
    claimedAt = Date.now();
    fitBox();
    sendResize();
  };

  /* Show the session at its own size. Another client's resize while this one
     owned the size means that client was used last. */
  const follow = (cols: number, rows: number) => {
    if (disposed || replaying || !shown || cols <= 0 || rows <= 0) return;
    if (owner) {
      if (Date.now() - claimedAt < 1500 || (cols === term.cols && rows === term.rows)) return;
      setOwner(false);
    }
    if (term.cols !== cols || term.rows !== rows) term.resize(cols, rows);
  };

  /* Geist Mono is a web font. When xterm opens before it has loaded, the cell is
     measured and the glyphs are drawn with the fallback font, and later text is
     drawn with Geist Mono into those cells: characters overlap or vanish. Once
     the font is in, measure and draw again. */
  const fonts = typeof document !== "undefined" ? document.fonts : undefined;
  if (fonts && !fonts.check(`${fontSize}px 'Geist Mono'`)) {
    Promise.all([
      fonts.load(`400 ${fontSize}px 'Geist Mono'`),
      fonts.load(`500 ${fontSize}px 'Geist Mono'`),
    ])
      .catch(() => undefined)
      .then(() => {
        if (disposed) return;
        term.options.fontFamily = term.options.fontFamily === TERM_FONT ? TERM_FONT_ALT : TERM_FONT;
        if (owner) fitBox();
        repaint();
      });
  }

  const send: SendInput = (data) => {
    if (mode !== "control" || !open()) return;
    if (!owner) claim();
    socket!.send(encoder.encode(data));
  };

  /* macOS terminal keys (Shift+Enter newline, Cmd/Option editing). The browser
     default is cancelled too: Cmd+Left would go back in history. */
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
    if (!owner || replaying || !shown) return;
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(sendResize, 60);
  });

  /* Runs fn after everything queued before it; dropped when the socket it was
     queued for is gone. */
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
  /* The replay is over: back to this pane's own size (owner) or the session's
     size (follower). */
  const finishReplay = () => {
    clearTimeout(replayTimer);
    if (!replaying || disposed) return;
    replaying = false;
    if (owner) {
      fitBox();
      sendResize();
    } else {
      const size = sessionSize;
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
        /* Only the width matters for where a redraw lands (a wrapped prompt).
           Height-only changes (the attach redraw nudge records two per attach)
           would make xterm scroll and reflow for nothing. */
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
      /* The core replays its buffer on every attach (REQ-WB-020), so the screen
         starts clean to avoid printing the history twice. */
      term.reset();
      if (mode === "control" && typeof document !== "undefined" && document.hasFocus()) {
        claim();
      } else {
        setOwner(false);
        const size = sessionSize;
        if (size) follow(size.cols, size.rows);
      }
      gen++;
      carry = new Uint8Array(0);
      expectFirst = true;
      parsing = false;
      replaying = true;
      host.style.visibility = "hidden";
      clearTimeout(replayTimer);
      /* A replay that never ends (a dropped marker) must not freeze the pane's
         size. */
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
      /* 4403: remote control locked (spec 57). The page switches the pane to view
         mode, which remounts it. */
      if (event.code === 4403) {
        onControlLost?.();
        return;
      }
      retry = Math.min(retry + 1, 5);
      retryTimer = setTimeout(connect, retry * 1000);
    };
  };

  /* A cached terminal that is not shown keeps buffering, but its WebGL renderer
     goes away and its size is left alone. */
  const computeShown = () => attached && !disposed && host.clientWidth > 0 && host.clientHeight > 0;

  const hide = () => {
    const active = term.buffer.active;
    wasAtBottom = active.viewportY >= active.baseY;
    disposeWebgl();
  };
  const show = () => {
    createWebgl();
    if (mode === "control" && owner) {
      fitBox();
      sendResize();
    } else {
      const size = sessionSize;
      if (size) follow(size.cols, size.rows);
    }
    repaint();
    if (wasAtBottom) term.scrollToBottom();
  };
  const refreshShown = () => {
    const next = computeShown();
    if (next === shown) return;
    shown = next;
    if (shown) show();
    else hide();
  };

  /* A pane in a hidden tab has no size; when it shows again its canvas may hold
     an old frame, so it is redrawn. */
  const observer =
    typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver(() => {
          refreshShown();
          if (owner) fitBox();
        });
  observer?.observe(host);

  /* Touch scrolling (phones): xterm.js does not turn a finger drag into
     scrolling. A drag scrolls the scrollback of the normal screen; on the
     alternate screen (full-screen programs such as Claude Code) it becomes
     mouse-wheel reports when the program tracks the mouse, else arrow keys, which
     is what native terminals do. */
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
    onFocus?.();
    if (!owner) claim();
  };
  const windowFocus = () => {
    if (!owner && shown) claim();
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

  const setDropping = (value: boolean) => {
    if (dropping === value) return;
    dropping = value;
    updateHostClass();
  };

  /* Drag and drop, as in Ghostty or Terminal: a dropped file is typed as its
     path. The macOS app gives the real paths; a browser uploads the files to the
     daemon, which returns where it saved them. Text is typed as is. The paste
     goes through xterm, so a program that asked for bracketed paste (Claude Code)
     sees one paste and can attach an image path. */
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
    onFocus?.();
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

  const initial = sessionSize;
  if (mode === "control") fitBox();
  else if (initial) follow(initial.cols, initial.rows);
  connect();

  /* ---- cache entry ---- */

  const attach = (container: HTMLElement) => {
    if (disposed) return;
    attached = true;
    if (host.parentElement !== container) container.appendChild(host);
    if (!opened) {
      opened = true;
      term.open(host);
    }
    lastShown = ++clock;
    applyRefs();
    refreshShown();
    if (focused && mode === "control") term.focus();
  };

  const detach = () => {
    if (disposed) return;
    attached = false;
    if (host.parentElement) host.parentElement.removeChild(host);
    if (shown) {
      shown = false;
      hide();
    }
    clearRefs();
  };

  const update = (props: TermCacheProps) => {
    if (disposed) return;
    onFocus = props.onFocus;
    onOwnerChange = props.onOwnerChange;
    onControlLost = props.onControlLost;
    onOpenFile = props.onOpenFile;
    sendRef = props.sendRef;
    transformRef = props.transformRef;
    claimRef = props.claimRef;
    keyboardRef = props.keyboardRef;
    scrollable = !!props.scrollable;
    if (props.fontSize !== fontSize) {
      fontSize = props.fontSize;
      if (term.options.fontSize !== props.fontSize) {
        term.options.fontSize = props.fontSize;
        if (owner) fitBox();
      }
    }
    if (props.theme !== theme) {
      theme = props.theme;
      term.options.theme = props.theme;
    }
    if (!!props.focused !== focused) {
      focused = !!props.focused;
      if (focused && mode === "control" && opened) term.focus();
    }
    if (props.sessionSize?.cols !== sessionSize?.cols || props.sessionSize?.rows !== sessionSize?.rows) {
      sessionSize = props.sessionSize;
      if (sessionSize) follow(sessionSize.cols, sessionSize.rows);
    }
    updateHostClass();
    applyRefs();
  };

  const dispose = () => {
    if (disposed) return;
    disposed = true;
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
    clearRefs();
    socket?.close();
    socket = null;
    disposeWebgl();
    term.dispose();
    if (host.parentElement) host.parentElement.removeChild(host);
    attached = false;
    shown = false;
  };

  return {
    sessionId,
    host,
    get lastShown() {
      return lastShown;
    },
    set lastShown(value: number) {
      lastShown = value;
    },
    matches: (i: TermCacheInit) => i.mode === mode && i.pid === pid,
    attach,
    detach,
    update,
    dispose,
  };
}

/* ---- module-level cache ---- */

const cache = new Map<string, TermCacheEntry>();
/* A monotonic stamp for recency; Date.now() would tie entries made in the same
   millisecond and make the LRU order depend on Map iteration. */
let clock = 0;

function trim(): void {
  while (cache.size > MAX_CACHED_TERMS) {
    let oldestId: string | undefined;
    let oldestAt = Infinity;
    for (const [id, entry] of cache) {
      if (entry.lastShown < oldestAt) {
        oldestAt = entry.lastShown;
        oldestId = id;
      }
    }
    if (oldestId === undefined) break;
    cache.get(oldestId)?.dispose();
    cache.delete(oldestId);
  }
}

/* The live entry for a session, or a new one. A changed mode or pty process is a
   different attachment, so the old entry is dropped and a fresh one replays. */
export function getOrCreate(init: TermCacheInit): TermCacheEntry {
  const existing = cache.get(init.sessionId);
  if (existing && existing.matches(init)) return existing;
  if (existing) {
    existing.dispose();
    cache.delete(init.sessionId);
  }
  const entry = createEntry(init);
  cache.set(init.sessionId, entry);
  trim();
  return entry;
}

/* Drop one session's terminal (close its socket, dispose the emulator). */
export function evict(sessionId: string): void {
  const entry = cache.get(sessionId);
  if (!entry) return;
  entry.dispose();
  cache.delete(sessionId);
}

/* Drop every cached session not in the live set (a session left the layout or
   was killed). */
export function prune(liveIds: ReadonlySet<string>): void {
  for (const id of [...cache.keys()]) {
    if (!liveIds.has(id)) evict(id);
  }
}

/* Drop everything (page unload, tests). */
export function clearCache(): void {
  for (const entry of cache.values()) entry.dispose();
  cache.clear();
}

export function cacheSize(): number {
  return cache.size;
}

export function cachedSessions(): string[] {
  return [...cache.keys()];
}

if (typeof window !== "undefined") {
  window.addEventListener("pagehide", clearCache);
  window.addEventListener("beforeunload", clearCache);
}

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/* The cache keeps one xterm.js Terminal and one attach WebSocket per session for
   as long as the session is in the layout, so a remount re-uses them and no
   replay happens. xterm, its addons and the socket are faked here. */

const fake = vi.hoisted(() => {
  const terminals: FakeTerminal[] = [];
  const sockets: FakeWebSocket[] = [];

  class FakeTerminal {
    cols = 80;
    rows = 24;
    options: Record<string, unknown> = {};
    unicode = { activeVersion: "" };
    modes = { mouseTrackingMode: "none" };
    buffer = {
      active: {
        type: "normal",
        viewportY: 0,
        baseY: 0,
        getLine: () => null,
        translateToString: () => "",
      },
    };
    disposed = false;
    resets = 0;
    constructor(_options?: unknown) {
      terminals.push(this);
    }
    loadAddon() {}
    open() {}
    registerLinkProvider() {
      return { dispose() {} };
    }
    attachCustomKeyEventHandler() {}
    onData() {
      return { dispose() {} };
    }
    onBinary() {
      return { dispose() {} };
    }
    onResize() {
      return { dispose() {} };
    }
    write(_data: unknown, cb?: () => void) {
      cb?.();
    }
    resize() {}
    dispose() {
      this.disposed = true;
    }
    focus() {}
    blur() {}
    paste() {}
    clear() {}
    scrollToBottom() {}
    scrollLines() {}
    refresh() {}
    clearTextureAtlas() {}
    reset() {
      this.resets++;
    }
  }

  class FakeFitAddon {
    fit() {}
    dispose() {}
  }
  class FakeAddon {
    dispose() {}
  }
  class FakeWebglAddon extends FakeAddon {
    onContextLoss() {}
  }

  class FakeWebSocket {
    static readonly OPEN = 1;
    static readonly CLOSED = 3;
    readyState = 0;
    binaryType = "";
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: unknown }) => void) | null = null;
    onclose: ((event: { code: number }) => void) | null = null;
    sent: unknown[] = [];
    closed = false;
    constructor(public url: string) {
      sockets.push(this);
    }
    send(data: unknown) {
      this.sent.push(data);
    }
    close() {
      this.closed = true;
      this.readyState = FakeWebSocket.CLOSED;
    }
  }

  return { terminals, sockets, FakeTerminal, FakeFitAddon, FakeAddon, FakeWebglAddon, FakeWebSocket };
});

vi.mock("@xterm/xterm", () => ({ Terminal: fake.FakeTerminal }));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: fake.FakeFitAddon }));
vi.mock("@xterm/addon-unicode11", () => ({ Unicode11Addon: fake.FakeAddon }));
vi.mock("@xterm/addon-web-links", () => ({ WebLinksAddon: fake.FakeAddon }));
vi.mock("@xterm/addon-webgl", () => ({ WebglAddon: fake.FakeWebglAddon }));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

vi.stubGlobal("WebSocket", fake.FakeWebSocket);

import type { ITheme } from "@xterm/xterm";

import {
  MAX_CACHED_TERMS,
  cacheSize,
  cachedSessions,
  clearCache,
  evict,
  getOrCreate,
  type TermCacheEntry,
  type TermCacheInit,
} from "./workbench-term-cache";

const THEME = {} as ITheme;

function init(sessionId: string): TermCacheInit {
  return { sessionId, mode: "control", pid: 0, fontSize: 13, theme: THEME };
}

/* A mount: create (or re-use) the entry and re-parent its host into a container. */
function mount(sessionId: string): TermCacheEntry {
  const entry = getOrCreate(init(sessionId));
  entry.attach(document.createElement("div"));
  return entry;
}

beforeEach(() => {
  fake.terminals.length = 0;
  fake.sockets.length = 0;
});

afterEach(() => {
  clearCache();
  fake.terminals.length = 0;
  fake.sockets.length = 0;
});

describe("terminal cache lifetime", () => {
  it("keeps one terminal and one socket across a remount", () => {
    const first = mount("s1");
    expect(fake.terminals).toHaveLength(1);
    expect(fake.sockets).toHaveLength(1);

    first.detach();
    const second = mount("s1");

    expect(second).toBe(first);
    expect(fake.terminals).toHaveLength(1);
    expect(fake.sockets).toHaveLength(1);
  });
});

describe("terminal cache eviction", () => {
  it("closes the socket when a session is evicted", () => {
    mount("s2");
    const socket = fake.sockets[0]!;
    expect(socket.closed).toBe(false);

    evict("s2");

    expect(socket.closed).toBe(true);
    expect(cacheSize()).toBe(0);
  });

  it("drops the least recently shown entry past the limit", () => {
    for (let i = 0; i <= MAX_CACHED_TERMS; i++) mount("lru-" + i);

    expect(cacheSize()).toBe(MAX_CACHED_TERMS);
    expect(cachedSessions()).not.toContain("lru-0");
    expect(cachedSessions()).toContain("lru-" + MAX_CACHED_TERMS);
  });
});

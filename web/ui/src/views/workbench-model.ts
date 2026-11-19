import type { WorkbenchSession } from "../api/workbench";

/* Spec 55 REQ-WB-031, REQ-WB-051: the Workbench layout. The core stores it as
   an opaque document, so every client (desktop, phone) shows the same spaces,
   tabs and splits. These helpers are pure: they return a new layout. */

export type PaneNode =
  | { type: "pane"; session: string }
  | { type: "split"; dir: "row" | "col"; children: PaneNode[] };

export interface WbTab {
  id: string;
  title?: string;
  root: PaneNode;
  focus?: string;
  zoom?: string;
}

export interface WbSpace {
  id: string;
  name: string;
  color: string;
  dir: string;
  tabs: WbTab[];
  activeTab?: string;
  /* herdr-style: the space is named after the folder of its active
     terminal. A space the owner renamed keeps its name (false). */
  auto?: boolean;
}

export type AgentGrouping = "space" | "status";

export interface WbLayout {
  version: 1;
  spaces: WbSpace[];
  activeSpace?: string;
  grouping?: AgentGrouping;
}

const DEFAULT_COLOR = "#3E9C8F";

export const SPACE_COLORS = ["#3E9C8F", "#5B7BE0", "#D9824B", "#7652C2", "#B21FB8", "#3F8F3F", "#8C8880"];

export const AGENT_PROGRAMS = ["claude", "codex", "jcode", "opencode", "gemini"];

export function newId(prefix: string): string {
  const bytes = new Uint8Array(6);
  if (typeof crypto !== "undefined" && crypto.getRandomValues) {
    crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  }
  return prefix + Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

export function emptyLayout(): WbLayout {
  return { version: 1, spaces: [], grouping: "space" };
}

function isNode(value: unknown): value is PaneNode {
  if (!value || typeof value !== "object") return false;
  const node = value as Record<string, unknown>;
  if (node.type === "pane") return typeof node.session === "string";
  if (node.type === "split") {
    return (node.dir === "row" || node.dir === "col") && Array.isArray(node.children) && node.children.every(isNode);
  }
  return false;
}

/* Reads a stored layout, dropping anything malformed. */
export function parseLayout(raw: unknown): WbLayout {
  const layout = emptyLayout();
  if (!raw || typeof raw !== "object") return layout;
  const value = raw as Record<string, unknown>;
  if (value.grouping === "status") layout.grouping = "status";
  if (Array.isArray(value.spaces)) {
    for (const s of value.spaces) {
      if (!s || typeof s !== "object") continue;
      const space = s as Record<string, unknown>;
      if (typeof space.id !== "string" || typeof space.name !== "string") continue;
      const tabs: WbTab[] = [];
      if (Array.isArray(space.tabs)) {
        for (const t of space.tabs) {
          const tab = t as Record<string, unknown>;
          if (!tab || typeof tab.id !== "string" || !isNode(tab.root)) continue;
          tabs.push({
            id: tab.id,
            title: typeof tab.title === "string" ? tab.title : undefined,
            root: tab.root,
            focus: typeof tab.focus === "string" ? tab.focus : undefined,
            zoom: typeof tab.zoom === "string" ? tab.zoom : undefined,
          });
        }
      }
      layout.spaces.push({
        id: space.id,
        name: space.name,
        color: typeof space.color === "string" ? space.color : DEFAULT_COLOR,
        dir: typeof space.dir === "string" ? space.dir : "",
        tabs,
        activeTab: typeof space.activeTab === "string" ? space.activeTab : undefined,
        auto: space.auto === false ? false : true,
      });
    }
  }
  if (typeof value.activeSpace === "string" && layout.spaces.some((s) => s.id === value.activeSpace)) {
    layout.activeSpace = value.activeSpace;
  } else if (layout.spaces.length) {
    layout.activeSpace = layout.spaces[0]?.id;
  }
  return layout;
}

export function paneSessions(node: PaneNode): string[] {
  return node.type === "pane" ? [node.session] : node.children.flatMap(paneSessions);
}

export function tabSessions(tab: WbTab): string[] {
  return paneSessions(tab.root);
}

export function activeSpace(layout: WbLayout): WbSpace | undefined {
  return layout.spaces.find((s) => s.id === layout.activeSpace) || layout.spaces[0];
}

export function activeTab(space: WbSpace | undefined): WbTab | undefined {
  if (!space) return undefined;
  return space.tabs.find((t) => t.id === space.activeTab) || space.tabs[0];
}

function mapSpace(layout: WbLayout, id: string, fn: (s: WbSpace) => WbSpace): WbLayout {
  return { ...layout, spaces: layout.spaces.map((s) => (s.id === id ? fn(s) : s)) };
}

function mapTab(space: WbSpace, id: string, fn: (t: WbTab) => WbTab): WbSpace {
  return { ...space, tabs: space.tabs.map((t) => (t.id === id ? fn(t) : t)) };
}

export function addSpace(layout: WbLayout, name: string, dir: string): WbLayout {
  const color = SPACE_COLORS[layout.spaces.length % SPACE_COLORS.length] ?? DEFAULT_COLOR;
  const space: WbSpace = { id: newId("sp_"), name, color, dir, tabs: [], auto: true };
  return { ...layout, spaces: [...layout.spaces, space], activeSpace: space.id };
}

export function removeSpace(layout: WbLayout, spaceId: string): WbLayout {
  const spaces = layout.spaces.filter((s) => s.id !== spaceId);
  return { ...layout, spaces, activeSpace: layout.activeSpace === spaceId ? spaces[0]?.id : layout.activeSpace };
}

/* Renames a space; an empty name turns automatic folder naming back on. */
export function renameSpace(layout: WbLayout, spaceId: string, name: string): WbLayout {
  const clean = name.trim();
  return mapSpace(layout, spaceId, (s) => (clean ? { ...s, name: clean, auto: false } : { ...s, auto: true }));
}

/* Renames a tab; an empty title goes back to the program's own label. */
export function renameTab(layout: WbLayout, spaceId: string, tabId: string, title: string): WbLayout {
  const clean = title.trim();
  return mapSpace(layout, spaceId, (s) => mapTab(s, tabId, (t) => ({ ...t, title: clean || undefined })));
}

export function spaceSessions(space: WbSpace): string[] {
  return space.tabs.flatMap(tabSessions);
}

export function selectSpace(layout: WbLayout, spaceId: string): WbLayout {
  return { ...layout, activeSpace: spaceId };
}

export function selectTab(layout: WbLayout, spaceId: string, tabId: string): WbLayout {
  return mapSpace(layout, spaceId, (s) => ({ ...s, activeTab: tabId }));
}

/* A new tab holding one session, made active. */
export function addTab(layout: WbLayout, spaceId: string, sessionId: string, title?: string): WbLayout {
  const tab: WbTab = { id: newId("tb_"), title, root: { type: "pane", session: sessionId }, focus: sessionId };
  return {
    ...mapSpace(layout, spaceId, (s) => ({ ...s, tabs: [...s.tabs, tab], activeTab: tab.id })),
    activeSpace: spaceId,
  };
}

function splitNode(node: PaneNode, target: string, sessionId: string, dir: "row" | "col"): PaneNode {
  if (node.type === "pane") {
    if (node.session !== target) return node;
    return { type: "split", dir, children: [node, { type: "pane", session: sessionId }] };
  }
  /* Splitting a pane inside a split of the same direction adds a sibling
     instead of nesting. */
  const index = node.children.findIndex((c) => c.type === "pane" && c.session === target);
  if (index >= 0 && node.dir === dir) {
    const children = [...node.children];
    children.splice(index + 1, 0, { type: "pane", session: sessionId });
    return { ...node, children };
  }
  return { ...node, children: node.children.map((c) => splitNode(c, target, sessionId, dir)) };
}

export function splitPane(
  layout: WbLayout,
  spaceId: string,
  tabId: string,
  target: string,
  sessionId: string,
  dir: "row" | "col"
): WbLayout {
  return mapSpace(layout, spaceId, (s) =>
    mapTab(s, tabId, (t) => ({ ...t, root: splitNode(t.root, target, sessionId, dir), focus: sessionId, zoom: undefined }))
  );
}

function removeNode(node: PaneNode, sessionId: string): PaneNode | null {
  if (node.type === "pane") return node.session === sessionId ? null : node;
  const children = node.children.map((c) => removeNode(c, sessionId)).filter((c): c is PaneNode => c !== null);
  if (!children.length) return null;
  if (children.length === 1) return children[0] ?? null;
  return { ...node, children };
}

/* Drops a session from every tab; a tab left empty is removed. */
export function removeSession(layout: WbLayout, sessionId: string): WbLayout {
  return {
    ...layout,
    spaces: layout.spaces.map((s) => {
      const tabs: WbTab[] = [];
      for (const t of s.tabs) {
        const root = removeNode(t.root, sessionId);
        if (!root) continue;
        const sessions = paneSessions(root);
        tabs.push({
          ...t,
          root,
          focus: t.focus && sessions.includes(t.focus) ? t.focus : sessions[0],
          zoom: t.zoom === sessionId ? undefined : t.zoom,
        });
      }
      const activeTab = tabs.some((t) => t.id === s.activeTab) ? s.activeTab : tabs[tabs.length - 1]?.id;
      return { ...s, tabs, activeTab };
    }),
  };
}

export function closeTab(layout: WbLayout, spaceId: string, tabId: string): WbLayout {
  return mapSpace(layout, spaceId, (s) => {
    const tabs = s.tabs.filter((t) => t.id !== tabId);
    return { ...s, tabs, activeTab: s.activeTab === tabId ? tabs[tabs.length - 1]?.id : s.activeTab };
  });
}

export function focusPane(layout: WbLayout, spaceId: string, tabId: string, sessionId: string): WbLayout {
  return mapSpace(layout, spaceId, (s) => mapTab(s, tabId, (t) => ({ ...t, focus: sessionId })));
}

export function toggleZoom(layout: WbLayout, spaceId: string, tabId: string, sessionId: string): WbLayout {
  return mapSpace(layout, spaceId, (s) =>
    mapTab(s, tabId, (t) => ({ ...t, zoom: t.zoom === sessionId ? undefined : sessionId, focus: sessionId }))
  );
}

export function setGrouping(layout: WbLayout, grouping: AgentGrouping): WbLayout {
  return { ...layout, grouping };
}

/* Takes the structure (spaces, tabs, splits) of a layout another client saved
   but keeps this client's own selection: the active space, each space's
   active tab, and each tab's focus and zoom. Two devices can look at
   different tabs of the same workbench. */
export function mergeRemote(local: WbLayout, remote: WbLayout): WbLayout {
  const localSpaces = new Map(local.spaces.map((s) => [s.id, s]));
  const spaces = remote.spaces.map((space) => {
    const mine = localSpaces.get(space.id);
    if (!mine) return space;
    const myTabs = new Map(mine.tabs.map((t) => [t.id, t]));
    const tabs = space.tabs.map((tab) => {
      const myTab = myTabs.get(tab.id);
      if (!myTab) return tab;
      const ids = paneSessions(tab.root);
      return {
        ...tab,
        focus: myTab.focus && ids.includes(myTab.focus) ? myTab.focus : tab.focus,
        zoom: myTab.zoom && ids.includes(myTab.zoom) ? myTab.zoom : undefined,
      };
    });
    const activeTab = mine.activeTab && tabs.some((t) => t.id === mine.activeTab) ? mine.activeTab : space.activeTab;
    return { ...space, tabs, activeTab };
  });
  const activeSpace =
    local.activeSpace && spaces.some((s) => s.id === local.activeSpace) ? local.activeSpace : remote.activeSpace;
  return { ...remote, spaces, activeSpace, grouping: local.grouping ?? remote.grouping };
}

/* The folder of a space's active terminal: its live cwd, else the folder it
   started in. Empty for a space with no terminals. */
export function spaceCwd(space: WbSpace, sessions: Record<string, WorkbenchSession>): string {
  const tab = activeTab(space) || space.tabs[space.tabs.length - 1];
  if (!tab) return "";
  const id = tab.focus || tabSessions(tab)[0];
  const session = id ? sessions[id] : undefined;
  return session?.cwd || session?.dir || "";
}

const HOME_DIR = /^\/(?:Users|home)\/[^/]+\/?$/;

/* "lana-harness" for /Users/me/code/lana-harness, "~" for the home folder. */
export function folderName(path: string): string {
  const clean = path.replace(/\/+$/, "");
  if (!clean || HOME_DIR.test(clean + "/")) return "~";
  return clean.split("/").pop() || "/";
}

/* The label of a space: the folder of its active terminal while it is
   auto-named, else the name it was given. */
export function spaceLabel(space: WbSpace, sessions: Record<string, WorkbenchSession>): string {
  if (space.auto === false) return space.name;
  const cwd = spaceCwd(space, sessions);
  return cwd ? folderName(cwd) : space.name || "~";
}

/* Where a session sits in the layout, if anywhere. */
export function locate(layout: WbLayout, sessionId: string): { space: WbSpace; tab: WbTab } | undefined {
  for (const space of layout.spaces) {
    for (const tab of space.tabs) {
      if (tabSessions(tab).includes(sessionId)) return { space, tab };
    }
  }
  return undefined;
}

/* REQ-WB-052: a session is an agent when its program is a known coding agent
   or Lana labelled it. */
export function isAgent(session: WorkbenchSession): boolean {
  if (session.agent) return true;
  const program = (session.program || "").toLowerCase();
  return AGENT_PROGRAMS.includes(program) || Boolean(session.labels?.["lana.agent"]);
}

/* "done" is herdr's blue state: an agent finished while nobody looked at its
   pane. It stays until the pane is focused (client side, see
   WorkbenchSession.done). */
export type Status = "blocked" | "working" | "done" | "idle" | "exited";

export function statusOf(session: WorkbenchSession | undefined): Status {
  if (!session) return "idle";
  if (session.needs_you) return "blocked";
  if (session.state === "exited") return "exited";
  if (session.state === "working") return "working";
  return session.done ? "done" : "idle";
}

export const STATUS_ORDER: Status[] = ["blocked", "working", "done", "idle", "exited"];

export const STATUS_LABEL: Record<Status, string> = {
  blocked: "Blocked",
  working: "Working",
  done: "Done",
  idle: "Idle",
  exited: "Exited",
};

const AGENT_NAMES: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  gemini: "Gemini CLI",
  cursor: "Cursor",
  devin: "Devin",
  agy: "Antigravity",
  cline: "Cline",
  mastracode: "Mastra Code",
  opencode: "OpenCode",
  copilot: "Copilot",
  kimi: "Kimi",
  kiro: "Kiro",
  amp: "Amp",
  grok: "Grok",
  hermes: "Hermes",
  kilo: "Kilo Code",
  qodercli: "Qoder",
  qwen: "Qwen Code",
  pi: "Pi",
  omp: "Oh My Pi",
  droid: "Droid",
  letta: "Letta",
  maki: "Maki",
  muse: "Muse",
};

/* "Claude Code" for claude, else the label itself. */
export function agentName(label: string): string {
  return AGENT_NAMES[label] || label;
}

/* What runs in a session, as a name: the agent's product name, else the
   program ("zsh", "lazygit"). */
export function programName(session: WorkbenchSession | undefined): string {
  if (!session) return "Terminal";
  if (session.agent) return agentName(session.agent);
  return session.program || session.command?.[0]?.split("/").pop() || "Terminal";
}

export function sessionTitle(session: WorkbenchSession | undefined, fallback = "Terminal"): string {
  if (!session) return fallback;
  /* Agents set the window title to a spinner or mark plus the task; drop
     the leading glyph so the label reads as text. */
  const title = (session.title || "").replace(/^[^\p{L}\p{N}]+/u, "").trim();
  if (title) return title;
  if (session.agent) return agentName(session.agent);
  if (session.program) return session.program;
  return session.command?.[0]?.split("/").pop() || fallback;
}

/* "<1m", "4m", "2h", "3d" since an ISO time. */
export function age(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t <= 0) return "";
  const s = Math.max(0, Math.floor((now - t) / 1000));
  if (s < 60) return "<1m";
  if (s < 3600) return Math.floor(s / 60) + "m";
  if (s < 86400) return Math.floor(s / 3600) + "h";
  return Math.floor(s / 86400) + "d";
}

/* Terminal input for the mobile key bar (REQ-WB-055). */
export const KEY_SEQUENCES: Record<string, string> = {
  esc: "\x1b",
  tab: "\t",
  "^C": "\x03",
  up: "\x1b[A",
  down: "\x1b[B",
  right: "\x1b[C",
  left: "\x1b[D",
  "/": "/",
};

/* Ctrl + a printable key: a-z and a few symbols map to C0 controls. */
export function ctrlKey(data: string): string {
  if (data.length !== 1) return data;
  const c = data.toLowerCase().charCodeAt(0);
  if (c >= 97 && c <= 122) return String.fromCharCode(c - 96);
  const map: Record<string, string> = { "[": "\x1b", "\\": "\x1c", "]": "\x1d", " ": "\x00", "?": "\x7f" };
  return map[data] ?? data;
}

/* Keys a browser terminal does not send the way a macOS terminal does.
   Shift+Enter sends ESC CR, the newline Claude Code and other agents read
   (same as Option+Enter). Cmd and Option editing keys follow iTerm2's
   "Natural text editing": line start/end, word left/right, delete line.
   Returns null for a key xterm.js handles itself. */
export interface KeyLike {
  key: string;
  shiftKey: boolean;
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
}

export function terminalKey(e: KeyLike): string | null {
  const { key, shiftKey, altKey, ctrlKey: ctrl, metaKey: meta } = e;
  if (ctrl) return null;
  if (key === "Enter" && shiftKey && !altKey && !meta) return "\x1b\r";
  if (meta && !altKey) {
    if (key === "ArrowLeft") return "\x01";
    if (key === "ArrowRight") return "\x05";
    if (key === "Backspace") return "\x15";
    return null;
  }
  if (altKey && !meta && !shiftKey) {
    if (key === "ArrowLeft") return "\x1bb";
    if (key === "ArrowRight") return "\x1bf";
    if (key === "Backspace") return "\x1b\x7f";
  }
  return null;
}

/* Dropped files are typed as their paths, escaped with backslashes the way
   macOS Terminal and Ghostty do, so a shell or Claude Code reads each path as
   one word ("Screenshot\ 2026-11-17\ at\ 08.50.12.png"). Only ASCII outside a
   safe set is escaped; other characters pass through. */
export function shellEscapePath(path: string): string {
  return path.replace(/[^A-Za-z0-9_\-.,:/@%+=~\u0080-￿]/g, (ch) => "\\" + ch);
}

/* The text a drop types: the escaped paths, separated by spaces. */
export function dropText(paths: string[]): string {
  return paths.filter((p) => p !== "").map(shellEscapePath).join(" ");
}

/* The paths the macOS app read from the drag (window.lanaNativeDrag, set by
   LanaWebView when the drag enters the window). A browser has none. They are
   used once, and only while fresh, so a stale value from an earlier drag is
   never typed. */
export interface NativeDrag {
  paths?: unknown;
  at?: unknown;
}

export function takeNativeDragPaths(
  holder: { lanaNativeDrag?: NativeDrag | null },
  now: number,
  maxAgeMs = 10 * 60 * 1000,
): string[] | null {
  const drag = holder.lanaNativeDrag;
  holder.lanaNativeDrag = null;
  if (!drag || !Array.isArray(drag.paths) || typeof drag.at !== "number") return null;
  if (now - drag.at > maxAgeMs || now < drag.at - 1000) return null;
  const paths = drag.paths.filter((p): p is string => typeof p === "string" && p !== "");
  return paths.length > 0 ? paths : null;
}

/* GoRex-style Workbench shortcuts (macOS). ⇧⌘T opens a new space. They are
   read before xterm.js sees the key, so a terminal never gets them. */
export type WbShortcut =
  | { kind: "new-tab" }
  | { kind: "new-space" }
  | { kind: "split"; dir: "row" | "col" }
  | { kind: "close-pane" }
  | { kind: "close-tab" }
  | { kind: "zoom" }
  | { kind: "focus"; dir: PaneDirection }
  | { kind: "tab"; index: number }
  | { kind: "tab-step"; step: 1 | -1 }
  | { kind: "rename-tab" }
  | { kind: "clear" }
  | { kind: "font"; step: 1 | -1 | 0 };

export type PaneDirection = "left" | "right" | "up" | "down";

export interface ShortcutKey extends KeyLike {
  code?: string;
}

const ARROWS: Record<string, PaneDirection> = {
  ArrowLeft: "left",
  ArrowRight: "right",
  ArrowUp: "up",
  ArrowDown: "down",
};

export function workbenchShortcut(e: ShortcutKey): WbShortcut | null {
  const { key, shiftKey: shift, altKey: alt, ctrlKey: ctrl, metaKey: meta } = e;
  const code = e.code || "";
  if (ctrl && !meta && !alt && key === "Tab") return { kind: "tab-step", step: shift ? -1 : 1 };
  if (!meta || ctrl) return null;
  if (alt) {
    const dir = !shift ? ARROWS[key] : undefined;
    return dir ? { kind: "focus", dir } : null;
  }
  const letter = code.startsWith("Key") ? code.slice(3).toLowerCase() : key.toLowerCase();
  if (shift) {
    if (letter === "t") return { kind: "new-space" };
    if (letter === "d") return { kind: "split", dir: "col" };
    if (letter === "w") return { kind: "close-tab" };
    if (letter === "r") return { kind: "rename-tab" };
    if (key === "Enter") return { kind: "zoom" };
    if (code === "BracketLeft" || key === "{") return { kind: "tab-step", step: -1 };
    if (code === "BracketRight" || key === "}") return { kind: "tab-step", step: 1 };
    if (key === "+" || code === "Equal") return { kind: "font", step: 1 };
    return null;
  }
  if (letter === "t") return { kind: "new-tab" };
  if (letter === "d") return { kind: "split", dir: "row" };
  if (letter === "w") return { kind: "close-pane" };
  if (letter === "k") return { kind: "clear" };
  if (key === "=" || key === "+" || code === "Equal") return { kind: "font", step: 1 };
  if (key === "-" || code === "Minus") return { kind: "font", step: -1 };
  if (key === "0" || code === "Digit0") return { kind: "font", step: 0 };
  const digit = /^Digit([1-9])$/.exec(code)?.[1] ?? (/^[1-9]$/.test(key) ? key : undefined);
  if (digit) return { kind: "tab", index: Number(digit) - 1 };
  return null;
}

/* The tab ⌘<n> selects: ⌘1…⌘8 by position, ⌘9 the last tab (as browsers do). */
export function tabAtShortcut(count: number, index: number): number {
  if (count === 0) return -1;
  if (index >= 8) return count - 1;
  return index < count ? index : -1;
}

export interface PaneRect {
  id: string;
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/* The pane next to `from` in a direction (⌥⌘ arrows): of the panes on that
   side, the closest one, preferring panes that overlap `from` across the
   axis. */
export function paneInDirection(rects: PaneRect[], from: string, dir: PaneDirection): string | null {
  const a = rects.find((r) => r.id === from);
  if (!a) return null;
  const horizontal = dir === "left" || dir === "right";
  let best: { id: string; score: number } | null = null;
  for (const b of rects) {
    if (b.id === from) continue;
    let gap: number;
    if (dir === "right") gap = b.left - a.right;
    else if (dir === "left") gap = a.left - b.right;
    else if (dir === "down") gap = b.top - a.bottom;
    else gap = a.top - b.bottom;
    if (gap < -2) continue;
    const overlap = horizontal
      ? Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top)
      : Math.min(a.right, b.right) - Math.max(a.left, b.left);
    const across = horizontal
      ? Math.abs((a.top + a.bottom) / 2 - (b.top + b.bottom) / 2)
      : Math.abs((a.left + a.right) / 2 - (b.left + b.right) / 2);
    const score = Math.max(0, gap) + (overlap > 0 ? 0 : 100000) + across / 1000;
    if (!best || score < best.score) best = { id: b.id, score };
  }
  return best ? best.id : null;
}

/* Opens a link clicked in a terminal (owner report 2026-11-19: in the desktop
   app a click asked "Do you want to navigate…? WARNING…", then Finder said
   "The application can't be opened. -50").

   xterm's default handlers call window.open() with no URL and set the new
   window's location afterwards. The desktop app gets that empty URL, hands it
   to macOS and fails; a browser shows a confirm first. Opening the URL itself
   works in both, and only web links are opened. */
export function openTerminalLink(uri: string): void {
  let url: URL;
  try {
    url = new URL(uri);
  } catch {
    return;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return;
  window.open(url.href, "_blank", "noopener,noreferrer");
}

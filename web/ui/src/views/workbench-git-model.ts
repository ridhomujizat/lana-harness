import type { GitDiffLine, GitHunk, GitInfo } from "../api/workbench-git";

/* Spec 66: pure helpers for the Workbench git panel. Components stay thin;
   these are unit-tested (REQ-WG-102). */

/* REQ-WG-071: the branch without its type prefix, as the daemon's short. */
export function shortBranch(branch: string): string {
  const i = branch.indexOf("/");
  return i >= 0 ? branch.slice(i + 1) : branch;
}

/* ------------------------------------------------------------------ diff */

export type DiffRow =
  | { type: "hunk"; header: string; index: number }
  | { type: "line"; line: GitDiffLine; hunk: number };

/* REQ-WG-068: the unified view, hunk headers between the lines. */
export function unifiedRows(hunks: GitHunk[]): DiffRow[] {
  const rows: DiffRow[] = [];
  hunks.forEach((h, index) => {
    rows.push({ type: "hunk", header: h.header, index });
    for (const line of h.lines) rows.push({ type: "line", line, hunk: index });
  });
  return rows;
}

export type SplitRow =
  | { type: "hunk"; header: string; index: number }
  | { type: "pair"; left?: GitDiffLine; right?: GitDiffLine; hunk: number };

/* REQ-WG-068: the split view. Context lines sit on both sides; a run of
   deletions is paired line by line with the run of additions after it. */
export function splitRows(hunks: GitHunk[]): SplitRow[] {
  const rows: SplitRow[] = [];
  hunks.forEach((h, index) => {
    rows.push({ type: "hunk", header: h.header, index });
    let i = 0;
    const lines = h.lines;
    const at = (k: number): GitDiffLine | undefined => lines[k];
    while (i < lines.length) {
      const line = at(i);
      if (!line) break;
      if (line.kind === "ctx") {
        rows.push({ type: "pair", left: line, right: line, hunk: index });
        i++;
        continue;
      }
      const dels: GitDiffLine[] = [];
      const adds: GitDiffLine[] = [];
      for (let l = at(i); l && l.kind === "del"; l = at(++i)) dels.push(l);
      for (let l = at(i); l && l.kind === "add"; l = at(++i)) adds.push(l);
      const n = Math.max(dels.length, adds.length);
      for (let k = 0; k < n; k++) rows.push({ type: "pair", left: dels[k], right: adds[k], hunk: index });
    }
  });
  return rows;
}

export type GutterMark = "add" | "mod" | "del";

/* REQ-WG-065: the file viewer's git gutter, by new-file line number. Added
   lines that replace deleted ones are "mod"; extra ones are "add"; a run of
   deletions with nothing in its place marks the line after it "del". */
export function gutterMarks(hunks: GitHunk[]): Map<number, GutterMark> {
  const marks = new Map<number, GutterMark>();
  for (const h of hunks) {
    const lines = h.lines;
    let i = 0;
    let next = h.new_start;
    const at = (k: number): GitDiffLine | undefined => lines[k];
    while (i < lines.length) {
      const line = at(i);
      if (!line) break;
      if (line.kind === "ctx") {
        next = (line.new ?? next) + 1;
        i++;
        continue;
      }
      let dels = 0;
      while (at(i)?.kind === "del") {
        dels++;
        i++;
      }
      let adds = 0;
      for (let l = at(i); l && l.kind === "add"; l = at(i)) {
        const n = l.new ?? next;
        marks.set(n, adds < dels ? "mod" : "add");
        next = n + 1;
        adds++;
        i++;
      }
      if (dels > 0 && adds === 0 && !marks.has(next)) marks.set(next, "del");
    }
  }
  return marks;
}

/* ------------------------------------------------------------- highlight */

export type Token = { text: string; kind: "kw" | "str" | "com" | "type" | "fn" | "num" | "plain" };

const KEYWORDS = new Set(
  (
    "export function const let var return if else for while switch case break continue import from type interface " +
    "class extends new this null undefined true false async await try catch finally throw default func package go " +
    "defer struct map chan range nil err select fallthrough goto def elif lambda None True False pass yield with as fn " +
    "pub use impl mut match enum trait where self Self static public private protected void int string bool"
  ).split(" ")
);

const TOKEN_RE =
  /(\/\/.*$|#(?!\[).*$|\/\*.*?\*\/|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`|\b\d[\d_.]*\b|[A-Za-z_$][\w$]*)/g;

/* A light, line-at-a-time highlighter: keywords, strings, comments, numbers,
   capitalised names and calls. Good enough for a read-only viewer. */
export function highlightLine(text: string, language: string): Token[] {
  const out: Token[] = [];
  const hashComments = language === "shell" || language === "python" || language === "yaml" || language === "toml";
  let last = 0;
  TOKEN_RE.lastIndex = 0;
  for (let m = TOKEN_RE.exec(text); m; m = TOKEN_RE.exec(text)) {
    const t = m[0];
    if (m.index > last) out.push({ text: text.slice(last, m.index), kind: "plain" });
    let kind: Token["kind"] = "plain";
    if (t.startsWith("//") || t.startsWith("/*")) kind = "com";
    else if (t.startsWith("#")) kind = hashComments ? "com" : "plain";
    else if (t[0] === '"' || t[0] === "'" || t[0] === "`") kind = "str";
    else if (/^\d/.test(t)) kind = "num";
    else if (KEYWORDS.has(t)) kind = "kw";
    else if (/^[A-Z]/.test(t)) kind = "type";
    else if (text[m.index + t.length] === "(") kind = "fn";
    if (t.startsWith("#") && !hashComments) {
      /* Not a comment here: tokenise "#" alone and go on after it. */
      out.push({ text: "#", kind: "plain" });
      last = m.index + 1;
      TOKEN_RE.lastIndex = last;
      continue;
    }
    out.push({ text: t, kind });
    last = m.index + t.length;
  }
  if (last < text.length) out.push({ text: text.slice(last), kind: "plain" });
  return out;
}

/* --------------------------------------------------- worktree per session */

/* REQ-WG-070: the directory a session is in now. */
export function sessionDir(s: { cwd?: string; dir: string } | undefined): string {
  return (s?.cwd || s?.dir || "").trim();
}

export interface PaneWorktree {
  session: string;
  info: GitInfo;
}

export interface WorktreeGroup {
  info: GitInfo;
  sessions: string[];
}

/* REQ-WG-073: the panes of a tab grouped by worktree, in pane order. */
export function groupByWorktree(ids: string[], gitOf: (id: string) => GitInfo | undefined): WorktreeGroup[] {
  const groups: WorktreeGroup[] = [];
  for (const id of ids) {
    const info = gitOf(id);
    if (!info) continue;
    const g = groups.find((x) => x.info.worktree === info.worktree);
    if (g) g.sessions.push(id);
    else groups.push({ info, sessions: [id] });
  }
  return groups;
}

/* REQ-WG-075: sessions with a coding agent that share a worktree with
   another one. Maps each such session to the other sessions. */
export function sharedAgents(
  ids: string[],
  gitOf: (id: string) => GitInfo | undefined,
  hasAgent: (id: string) => boolean
): Map<string, string[]> {
  const byTree = new Map<string, string[]>();
  for (const id of ids) {
    if (!hasAgent(id)) continue;
    const info = gitOf(id);
    if (!info) continue;
    const list = byTree.get(info.worktree) ?? [];
    list.push(id);
    byTree.set(info.worktree, list);
  }
  const out = new Map<string, string[]>();
  for (const list of byTree.values()) {
    if (list.length < 2) continue;
    for (const id of list) out.set(id, list.filter((x) => x !== id));
  }
  return out;
}

/* REQ-WG-072: the worktree the panel shows: the pinned one, else the
   focused pane's, else the one it showed before. */
export function panelWorktree(pinned: string | null, focused: string | undefined, previous: string | null): string | null {
  if (pinned) return pinned;
  if (focused) return focused;
  return previous;
}

/* REQ-WG-064: path helpers. */
export function baseName(path: string): string {
  const i = path.lastIndexOf("/");
  return i >= 0 ? path.slice(i + 1) : path;
}

export function dirName(path: string): string {
  const i = path.lastIndexOf("/");
  return i > 0 ? path.slice(0, i) : "";
}

/* A path relative to the worktree, or null when it is outside it. */
export function relativeTo(root: string, path: string): string | null {
  const r = root.replace(/\/+$/, "");
  if (path === r) return "";
  return path.startsWith(r + "/") ? path.slice(r.length + 1) : null;
}

export function plural(n: number, one: string, many = one + "s"): string {
  return n + " " + (n === 1 ? one : many);
}

/* "3h", "12m", "2w": the age of a time for list rows. */
export function shortAge(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "—";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "now";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m";
  const h = Math.round(m / 60);
  if (h < 24) return h + "h";
  const d = Math.round(h / 24);
  if (d < 14) return d + "d";
  const w = Math.round(d / 7);
  if (w < 9) return w + "w";
  return Math.round(d / 30) + "mo";
}

import type { ReactNode } from "react";
import type { KnowledgeScopeStat } from "../api/knowledge";

/* Spec 48: helpers shared by the Knowledge tabs. One colour per scope, used
   the same on every tab (graph dots, chips, list rows, timeline). */

const AGENT_COLORS = [
  "#7C6CF0",
  "#2E9D6A",
  "#D4880F",
  "#3B82F6",
  "#E5484D",
  "#0EA5A4",
  "#C026D3",
  "#65A30D",
];
const SHARED_COLOR = "#8A867E";

export type ScopeColors = (scope: string) => string;

/* Agent scopes get palette colours in name order; "shared" is grey; an
   unknown scope falls back to a hash so it is still stable. */
export function makeScopeColors(scopes: string[]): ScopeColors {
  const agents = Array.from(new Set(scopes.filter((s) => s && s !== "shared"))).sort();
  const map = new Map<string, string>();
  agents.forEach((scope, index) => {
    map.set(scope, AGENT_COLORS[index % AGENT_COLORS.length] ?? SHARED_COLOR);
  });
  return (scope: string) => {
    if (!scope || scope === "shared") {
      return SHARED_COLOR;
    }
    const known = map.get(scope);
    if (known) {
      return known;
    }
    let hash = 0;
    for (const char of scope) {
      hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
    }
    return AGENT_COLORS[hash % AGENT_COLORS.length] ?? SHARED_COLOR;
  };
}

export function scopeNames(scopes: KnowledgeScopeStat[]): string[] {
  return scopes.map((item) => item.scope);
}

export function scopeLabel(scope: string, label: (name: string) => string): string {
  return scope === "shared" ? "Shared" : label(scope) || scope;
}

export function ScopeDot({ color, size = 8 }: { color: string; size?: number }) {
  return (
    <span
      className="kg-dot"
      aria-hidden="true"
      style={{ background: color, width: size, height: size }}
    />
  );
}

/* Knowledge route helpers (REQ-KGR-050). */
export function knowledgeHref(params: Record<string, string | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value) {
      query.set(key, value);
    }
  }
  const text = query.toString();
  return "#/knowledge" + (text ? "?" + text : "");
}

export function pageHref(path: string): string {
  return knowledgeHref({ tab: "pages", page: path });
}

/* A wikilink target from the log or a page whose path is unknown: the Pages
   tab resolves it (a path when it has a slash, else a title). */
export function openHref(target: string): string {
  return knowledgeHref({ tab: "pages", open: target });
}

/* "Ask <agent>" opens the agent's room with the page in the composer. */
export function askHref(agent: string, title: string): string {
  return (
    "#/chats/" +
    encodeURIComponent(agent) +
    "?draft=" +
    encodeURIComponent("Tentang [[" + title + "]]: ")
  );
}

/* The page body without YAML front matter and without a first "# Title"
   heading, which the views show on their own. */
export function pageBody(markdown: string): string {
  let text = String(markdown || "").replace(/\r\n?/g, "\n");
  if (text.startsWith("---\n")) {
    const end = text.indexOf("\n---", 4);
    if (end >= 0) {
      const after = text.indexOf("\n", end + 4);
      text = after >= 0 ? text.slice(after + 1) : "";
    }
  }
  text = text.replace(/^\s*# [^\n]*\n?/, "");
  return text.trim();
}

/* The first paragraph of plain text, without struck (superseded) parts and
   without markup, for the graph side panel summary. */
export function firstParagraph(markdown: string, max = 280): string {
  const body = pageBody(markdown);
  for (const block of body.split(/\n\s*\n/)) {
    const lines = block
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line && !line.startsWith("#") && !line.startsWith("|"));
    if (lines.length === 0) {
      continue;
    }
    const text = lines
      .join(" ")
      .replace(/~~[^~]*~~/g, "")
      .replace(/\*\((until|since)[^)]*\)\*/g, "")
      .replace(/\[\[([^\]|#]+)(#[^\]|]*)?(\|([^\]]*))?\]\]/g, (_m, target, _h, _b, label) =>
        String(label || target)
      )
      .replace(/^[-*]\s+/, "")
      .replace(/[*_`]/g, "")
      .replace(/\s+/g, " ")
      .trim();
    if (text) {
      return text.length > max ? text.slice(0, max - 1).trimEnd() + "…" : text;
    }
  }
  return "";
}

export interface SupersededChange {
  old: string;
  until: string;
  current: string;
  since: string;
}

/* The newest superseded fact of a page (spec 46 format:
   ~~old~~ *(until DATE)* new *(since DATE[, [[source]]])*). */
export function latestChange(markdown: string): SupersededChange | null {
  const pattern =
    /~~([^~]+)~~\s*\*\(until ([^)]+)\)\*\s*(.+?)\s*\*\(since ([^),]+)[^)]*\)\*/g;
  let best: SupersededChange | null = null;
  for (const match of String(markdown || "").matchAll(pattern)) {
    const change = {
      old: (match[1] ?? "").trim(),
      until: (match[2] ?? "").trim(),
      current: (match[3] ?? "").replace(/^[:\-–—\s]+/, "").trim(),
      since: (match[4] ?? "").trim(),
    };
    if (!best || change.since >= best.since) {
      best = change;
    }
  }
  return best;
}

export function shortDate(value: string): string {
  if (!value) {
    return "";
  }
  const date = new Date(value.length === 10 ? value + "T00:00:00" : value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const today = new Date();
  const days = Math.floor(
    (new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime() -
      new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()) /
      86400000
  );
  if (days <= 0) return "today";
  if (days === 1) return "yesterday";
  if (days < 7) return days + " d";
  if (days < 60) return Math.floor(days / 7) + " w";
  return date.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

/* Plain text of a line that may hold [[target|label]] links. */
export function plainLinks(text: string): string {
  return String(text || "").replace(
    /\[\[([^\]|#]+)(#[^\]|]*)?(\|([^\]]*))?\]\]/g,
    (_m, target: string, _h, _b, label: string | undefined) => String(label || target).trim()
  );
}

/* Journals, inbox sources and reports are where a page came from, not pages
   it links to; the views list them under "Learned from". */
export function isSourcePath(path: string): boolean {
  return /(^|\/)(journal|inbox|sources|reports)\//.test(path);
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const JOURNAL_PATH = /(?:^|\/)(?:agents\/([^/]+)\/)?journal\/(\d{4})-(\d{2})-(\d{2})(?:\.md)?$/;

/* A journal file reads as "Dana journal (5 Nov 2026)" instead of its bare
   date file name; anything else returns null. */
export function journalTitle(path: string, label: (name: string) => string): string | null {
  const match = JOURNAL_PATH.exec(String(path || "").trim());
  if (!match) {
    return null;
  }
  const scope = match[1] || "shared";
  const who = scopeLabel(scope, label);
  const month = MONTHS[Number(match[3]) - 1] || match[3];
  return who + " journal (" + Number(match[4]) + " " + month + " " + match[2] + ")";
}

export function displayTitle(
  path: string,
  title: string,
  label: (name: string) => string
): string {
  return journalTitle(path, label) || title;
}

export function Note({ children }: { children: ReactNode }) {
  return <p className="knowledge-note">{children}</p>;
}

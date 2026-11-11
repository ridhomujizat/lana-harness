import { createElement as h, useEffect, useRef, useState } from "react";
import type { ReactElement, ReactNode } from "react";

/* Small, dependency-free markdown renderer for assistant text. It parses a
   string into React nodes and never injects HTML: every value becomes a
   text child. Blocks are line based; inline markup is a single left-to-right
   pass, so an unfinished pattern falls back to literal text while a reply
   streams in. */

const SAFE_SCHEMES = ["http", "https", "mailto"];
const BARE_SCHEMES = ["http", "https"];
const TRAILING_PUNCTUATION = ".,;:!?)\"'";
const EMPHASIS = [
  { marker: "**", tag: "strong" },
  { marker: "__", tag: "strong" },
  { marker: "~~", tag: "del" },
  { marker: "*", tag: "em" },
  { marker: "_", tag: "em" },
];

const FENCE_RE = /^ {0,3}(`{3,})\s*([A-Za-z0-9_+.-]*)\s*$/;
const CLOSE_FENCE_RE = /^ {0,3}(`{3,})\s*$/;
const HEADING_RE = /^ {0,3}(#{1,4})\s+(.+)$/;
const HR_RE = /^ {0,3}(?:(?:\*\s*){3,}|(?:-\s*){3,}|(?:_\s*){3,})$/;
const QUOTE_RE = /^ {0,3}>\s?(.*)$/;
const UL_RE = /^(\s*)[-*+]\s+(.*)$/;
const OL_RE = /^(\s*)(\d{1,9})[.)]\s+(.*)$/;
const WORD_CHAR_RE = /[A-Za-z0-9]/;

/* The scheme names are kept apart from the separator so no full URL literal
   appears in the shipped assets. */
function schemePrefix(scheme: string): string {
  return scheme + ":" + "/" + "/";
}

function isWordChar(char: string): boolean {
  return Boolean(char) && WORD_CHAR_RE.test(char);
}

function safeHref(url: string): string {
  const value = String(url || "").trim();
  if (!value || /\s/.test(value)) {
    return "";
  }
  const lower = value.toLowerCase();
  for (const scheme of SAFE_SCHEMES) {
    if (lower.startsWith(scheme + ":")) {
      return value;
    }
  }
  return "";
}

function bareUrlPrefix(text: string, at: number): number {
  if (at > 0 && isWordChar(text[at - 1] || "")) {
    return 0;
  }
  for (const scheme of BARE_SCHEMES) {
    const prefix = schemePrefix(scheme);
    if (text.slice(at, at + prefix.length).toLowerCase() === prefix) {
      return prefix.length;
    }
  }
  return 0;
}

function takeBareUrl(text: string, at: number, prefixLength: number): string {
  let end = at + prefixLength;
  while (end < text.length && !/[\s<>]/.test(text[end] || "")) {
    end += 1;
  }
  let url = text.slice(at, end);
  while (
    url.length > prefixLength &&
    TRAILING_PUNCTUATION.indexOf(url[url.length - 1] || "") >= 0
  ) {
    url = url.slice(0, -1);
  }
  return url.length > prefixLength ? url : "";
}

function matchLink(
  text: string,
  start: number
): { text: string; url: string; end: number } | null {
  const close = text.indexOf("]", start + 1);
  if (close < 0 || text[close + 1] !== "(") {
    return null;
  }
  const end = text.indexOf(")", close + 2);
  if (end < 0) {
    return null;
  }
  const url = text.slice(close + 2, end).trim();
  if (!url) {
    return null;
  }
  return { text: text.slice(start + 1, close), url, end: end + 1 };
}

function matchEmphasis(
  text: string,
  start: number,
  depth: number,
  autolink: boolean
): { node: ReactElement; end: number } | null {
  if (depth > 8) {
    return null;
  }
  for (const entry of EMPHASIS) {
    const marker = entry.marker;
    if (!text.startsWith(marker, start)) {
      continue;
    }
    if (marker[0] === "_" && start > 0 && isWordChar(text[start - 1] || "")) {
      continue;
    }
    let search = start + marker.length;
    let end = -1;
    while (search < text.length) {
      const found = text.indexOf(marker, search);
      if (found < 0) {
        break;
      }
      if (found === start + marker.length) {
        search = found + marker.length;
        continue;
      }
      if (marker.length === 1 && text[found + 1] === marker) {
        search = found + 2;
        continue;
      }
      if (marker[0] === "_" && isWordChar(text[found + marker.length] || "")) {
        search = found + marker.length;
        continue;
      }
      end = found;
      break;
    }
    if (end < 0) {
      continue;
    }
    const inner = text.slice(start + marker.length, end);
    if (!inner.trim()) {
      continue;
    }
    return {
      node: h(
        entry.tag,
        { className: "md-" + entry.tag },
        ...parseInline(inner, depth + 1, autolink)
      ),
      end: end + marker.length,
    };
  }
  return null;
}

/* Spec 48: a caller may render [[target]], [[target|label]] and
   [[target#heading]] itself (the knowledge reader turns them into page
   links). Set only while one Markdown render runs; without it the text stays
   as written. */
export type WikiLinkRenderer = (target: string, label: string) => ReactNode;
let wikiLinkRenderer: WikiLinkRenderer | null = null;

function matchWikiLink(
  text: string,
  index: number
): { target: string; label: string; end: number } | null {
  if (!text.startsWith("[[", index)) {
    return null;
  }
  const close = text.indexOf("]]", index + 2);
  if (close < 0) {
    return null;
  }
  const inner = text.slice(index + 2, close);
  if (!inner.trim() || inner.includes("[") || inner.includes("\n")) {
    return null;
  }
  const bar = inner.indexOf("|");
  const ref = bar >= 0 ? inner.slice(0, bar) : inner;
  const hash = ref.indexOf("#");
  const target = (hash >= 0 ? ref.slice(0, hash) : ref).trim();
  const label = (bar >= 0 ? inner.slice(bar + 1) : ref).trim() || target;
  if (!target) {
    return null;
  }
  return { target, label, end: close + 2 };
}

/* One left-to-right pass. Code spans, links, and bare URLs are recognised
   before emphasis, so their content is never parsed as markup. */
function parseInline(
  text: string,
  depth = 0,
  autolink = true
): ReactNode[] {
  if (depth > 8) {
    return [text];
  }
  const nodes: ReactNode[] = [];
  let buffer = "";
  let index = 0;

  function flush() {
    if (buffer) {
      nodes.push(buffer);
      buffer = "";
    }
  }

  while (index < text.length) {
    const char = text[index] || "";

    if (char === "`") {
      const end = text.indexOf("`", index + 1);
      if (end > index + 1) {
        flush();
        nodes.push(
          h("code", { className: "md-inline-code" }, text.slice(index + 1, end))
        );
        index = end + 1;
        continue;
      }
      buffer += char;
      index += 1;
      continue;
    }

    if (char === "[" && wikiLinkRenderer) {
      const wiki = matchWikiLink(text, index);
      if (wiki) {
        flush();
        nodes.push(wikiLinkRenderer(wiki.target, wiki.label));
        index = wiki.end;
        continue;
      }
    }

    if (char === "[") {
      const link = matchLink(text, index);
      if (link) {
        flush();
        const href = safeHref(link.url);
        if (href) {
          nodes.push(
            h(
              "a",
              { href, target: "_blank", rel: "noopener noreferrer" },
              ...parseInline(link.text, depth + 1, false)
            )
          );
        } else {
          nodes.push(link.text);
        }
        index = link.end;
        continue;
      }
      buffer += char;
      index += 1;
      continue;
    }

    if (autolink && (char === "h" || char === "H")) {
      const prefixLength = bareUrlPrefix(text, index);
      if (prefixLength > 0) {
        const url = takeBareUrl(text, index, prefixLength);
        if (url) {
          flush();
          nodes.push(
            h("a", { href: url, target: "_blank", rel: "noopener noreferrer" }, url)
          );
          index += url.length;
          continue;
        }
      }
    }

    if (char === "\n") {
      flush();
      nodes.push(h("br"));
      index += 1;
      continue;
    }

    const emphasis = matchEmphasis(text, index, depth, autolink);
    if (emphasis) {
      flush();
      nodes.push(emphasis.node);
      index = emphasis.end;
      continue;
    }

    buffer += char;
    index += 1;
  }

  flush();
  return nodes;
}

function glyph(paths: ReactElement[]): ReactElement {
  return h(
    "svg",
    {
      className: "md-copy-ico",
      viewBox: "0 0 24 24",
      width: 14,
      height: 14,
      fill: "none",
      stroke: "currentColor",
      strokeWidth: 2,
      strokeLinecap: "round",
      strokeLinejoin: "round",
      "aria-hidden": "true",
      focusable: "false",
    },
    ...paths
  );
}

const COPY_GLYPH = glyph([
  h("rect", { key: "r", x: 8, y: 8, width: 14, height: 14, rx: 2, ry: 2 }),
  h("path", {
    key: "p",
    d: "M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2",
  }),
]);

const CHECK_GLYPH = glyph([h("path", { key: "p", d: "M20 6 9 17l-5-5" })]);

function CopyButton({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  const resetTimer = useRef<number | null>(null);

  useEffect(
    () => () => {
      if (resetTimer.current !== null) {
        window.clearTimeout(resetTimer.current);
      }
    },
    []
  );

  function markCopied() {
    setCopied(true);
    if (resetTimer.current !== null) {
      window.clearTimeout(resetTimer.current);
    }
    resetTimer.current = window.setTimeout(() => {
      resetTimer.current = null;
      setCopied(false);
    }, 1500);
  }

  function copy() {
    const clipboard =
      typeof navigator === "undefined" ? null : navigator.clipboard;
    if (!clipboard || typeof clipboard.writeText !== "function") {
      return;
    }
    try {
      const result = clipboard.writeText(code);
      if (result && typeof result.then === "function") {
        result.then(() => markCopied()).catch(() => {});
      } else {
        markCopied();
      }
    } catch {
      /* The clipboard API can reject on an insecure origin; the label then
         simply stays "Copy code". */
    }
  }

  return h(
    "button",
    {
      type: "button",
      className: "md-copy",
      "aria-label": copied ? "Copied" : "Copy code",
      title: copied ? "Copied" : "Copy code",
      onClick: copy,
    },
    copied ? CHECK_GLYPH : COPY_GLYPH
  );
}

function renderCode(code: string, language: string): ReactElement {
  return h(
    "div",
    { className: "md-code" },
    language
      ? h(
          "div",
          { className: "md-code-head" },
          h("span", { className: "md-code-lang" }, language)
        )
      : null,
    h(CopyButton, { code }),
    h("pre", { className: "md-pre" }, h("code", { className: "md-code-text" }, code))
  );
}

function parseFence(
  lines: string[],
  start: number
): { node: ReactElement; next: number } {
  const match = (lines[start] || "").match(FENCE_RE);
  const marker = match ? match[1] || "" : "";
  const language = match ? match[2] || "" : "";
  const content: string[] = [];
  let index = start + 1;
  while (index < lines.length) {
    const close = (lines[index] || "").match(CLOSE_FENCE_RE);
    if (close && (close[1] || "").length >= marker.length) {
      index += 1;
      break;
    }
    content.push(lines[index] || "");
    index += 1;
  }
  return { node: renderCode(content.join("\n"), language), next: index };
}

function parseHeading(line: string): ReactElement | null {
  const match = line.match(HEADING_RE);
  if (!match) {
    return null;
  }
  const level = (match[1] || "").length;
  const text = (match[2] || "").replace(/\s+#+\s*$/, "").trim();
  return h("h" + level, null, ...parseInline(text));
}

function parseBlockquote(
  lines: string[],
  start: number
): { node: ReactElement; next: number } {
  const inner: string[] = [];
  let index = start;
  while (index < lines.length) {
    const match = (lines[index] || "").match(QUOTE_RE);
    if (!match) {
      break;
    }
    inner.push(match[1] || "");
    index += 1;
  }
  return { node: h("blockquote", null, ...parseBlocks(inner)), next: index };
}

function splitRow(line: string): string[] {
  const value = String(line || "").trim();
  const body = value.replace(/^\|/, "").replace(/\|$/, "");
  const cells: string[] = [];
  let cell = "";
  for (let index = 0; index < body.length; index += 1) {
    const char = body[index] || "";
    if (char === "\\" && body[index + 1] === "|") {
      cell += "|";
      index += 1;
      continue;
    }
    if (char === "|") {
      cells.push(cell.trim());
      cell = "";
      continue;
    }
    cell += char;
  }
  cells.push(cell.trim());
  return cells;
}

function alignOf(cell: string): string {
  const left = cell.startsWith(":");
  const right = cell.endsWith(":");
  if (left && right) {
    return "center";
  }
  if (left) {
    return "left";
  }
  if (right) {
    return "right";
  }
  return "";
}

function alignProps(align: string | undefined): { className: string } | null {
  return align ? { className: "md-align-" + align } : null;
}

function tableAt(lines: string[], index: number): boolean {
  if (index + 1 >= lines.length || (lines[index] || "").indexOf("|") < 0) {
    return false;
  }
  const head = splitRow(lines[index] || "");
  const separator = splitRow(lines[index + 1] || "");
  if (head.length !== separator.length || separator.length === 0) {
    return false;
  }
  return separator.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function parseTable(
  lines: string[],
  start: number
): { node: ReactElement; next: number } {
  const head = splitRow(lines[start] || "");
  const separator = splitRow(lines[start + 1] || "");
  const aligns = separator.map(alignOf);
  const rows: string[][] = [];
  let index = start + 2;
  while (
    index < lines.length &&
    (lines[index] || "").trim() &&
    (lines[index] || "").indexOf("|") >= 0
  ) {
    rows.push(splitRow(lines[index] || "").slice(0, head.length));
    index += 1;
  }
  const table = h(
    "table",
    { className: "md-table" },
    h(
      "thead",
      null,
      h(
        "tr",
        null,
        ...head.map((cell, cellIndex) =>
          h("th", alignProps(aligns[cellIndex]), ...parseInline(cell))
        )
      )
    ),
    h(
      "tbody",
      null,
      ...rows.map((row) =>
        h(
          "tr",
          null,
          ...head.map((_, cellIndex) =>
            h(
              "td",
              alignProps(aligns[cellIndex]),
              ...parseInline(row[cellIndex] || "")
            )
          )
        )
      )
    )
  );
  return { node: h("div", { className: "md-table-wrap" }, table), next: index };
}

interface ListItem {
  indent: number;
  ordered: boolean;
  number: number;
  text: string;
}

function matchListItem(line: string): ListItem | null {
  const unordered = line.match(UL_RE);
  if (unordered) {
    return {
      indent: (unordered[1] || "").length,
      ordered: false,
      number: 0,
      text: (unordered[2] || "").trim(),
    };
  }
  const ordered = line.match(OL_RE);
  if (ordered) {
    return {
      indent: (ordered[1] || "").length,
      ordered: true,
      number: Number(ordered[2]),
      text: (ordered[3] || "").trim(),
    };
  }
  return null;
}

/* List items may nest once. A deeper marker is still consumed by a recursive
   call, so a pathological input cannot stall the outer loop. */
function parseList(
  lines: string[],
  start: number,
  depth: number
): { node: ReactElement; next: number } {
  const first = matchListItem(lines[start] || "") || {
    indent: 0,
    ordered: false,
    number: 0,
    text: "",
  };
  const base = first.indent;
  const items: { lines: string[]; children: ReactNode[] }[] = [];
  let index = start;

  while (index < lines.length) {
    const match = matchListItem(lines[index] || "");
    if (!match || match.indent < base) {
      break;
    }
    if (match.indent >= base + 2) {
      if (depth >= 1 || items.length === 0) {
        break;
      }
      const nested = parseList(lines, index, depth + 1);
      const previous = items[items.length - 1];
      if (previous) {
        previous.children.push(nested.node);
      }
      index = nested.next;
      continue;
    }

    const item = { lines: [match.text], children: [] as ReactNode[] };
    items.push(item);
    index += 1;
    while (index < lines.length) {
      const line = lines[index] || "";
      if (!line.trim() || matchListItem(line) || isBlockStart(lines, index)) {
        break;
      }
      item.lines.push(line.trim());
      index += 1;
    }
  }

  const props =
    first.ordered && first.number > 1 ? { start: first.number } : null;
  const node = h(
    first.ordered ? "ol" : "ul",
    props,
    ...items.map((item) =>
      h("li", null, ...parseInline(item.lines.join("\n")), ...item.children)
    )
  );
  return { node, next: index };
}

function isBlockStart(lines: string[], index: number): boolean {
  const line = lines[index] || "";
  if (!line || !line.trim()) {
    return true;
  }
  if (FENCE_RE.test(line) || HEADING_RE.test(line) || HR_RE.test(line)) {
    return true;
  }
  if (QUOTE_RE.test(line) || matchListItem(line)) {
    return true;
  }
  return tableAt(lines, index);
}

function parseBlocks(lines: string[]): ReactNode[] {
  const nodes: ReactNode[] = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index] || "";
    if (!line.trim()) {
      index += 1;
      continue;
    }

    if (FENCE_RE.test(line)) {
      const block = parseFence(lines, index);
      nodes.push(block.node);
      index = block.next;
      continue;
    }

    if (HEADING_RE.test(line)) {
      nodes.push(parseHeading(line));
      index += 1;
      continue;
    }

    if (HR_RE.test(line)) {
      nodes.push(h("hr"));
      index += 1;
      continue;
    }

    if (QUOTE_RE.test(line)) {
      const quote = parseBlockquote(lines, index);
      nodes.push(quote.node);
      index = quote.next;
      continue;
    }

    if (tableAt(lines, index)) {
      const table = parseTable(lines, index);
      nodes.push(table.node);
      index = table.next;
      continue;
    }

    if (matchListItem(line)) {
      const list = parseList(lines, index, 0);
      nodes.push(list.node);
      index = list.next;
      continue;
    }

    const start = index;
    const pieces: string[] = [];
    while (index < lines.length && (lines[index] || "").trim()) {
      if (index > start && isBlockStart(lines, index)) {
        break;
      }
      pieces.push((lines[index] || "").trim());
      index += 1;
    }
    if (index === start) {
      pieces.push(line.trim());
      index += 1;
    }
    nodes.push(h("p", null, ...parseInline(pieces.join("\n"))));
  }

  return nodes;
}

/* Assistant text as a markdown block. Empty and null input render an empty
   `.md` container; callers decide whether to render the component at all. */
export function Markdown({
  text,
  wikiLink,
}: {
  text?: string | null;
  wikiLink?: WikiLinkRenderer;
}): ReactElement {
  const source = text === null || text === undefined ? "" : String(text);
  const previous = wikiLinkRenderer;
  wikiLinkRenderer = wikiLink || null;
  try {
    const blocks = source.trim()
      ? parseBlocks(
          source
            .replace(/\r\n?/g, "\n")
            .split("\n")
            .map((line) => line.replace(/^\t+/, (tabs) => "  ".repeat(tabs.length)))
        )
      : [];
    return h("div", { className: "md" }, ...blocks);
  } finally {
    wikiLinkRenderer = previous;
  }
}

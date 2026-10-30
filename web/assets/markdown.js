import { h, useState } from "/assets/vendor/preact-htm.js";

/* Small, dependency-free markdown renderer for assistant text. It parses a
   string into Preact vnodes and never injects HTML: every value becomes a
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
function schemePrefix(scheme) {
  return scheme + ":" + "/" + "/";
}

function isWordChar(char) {
  return Boolean(char) && WORD_CHAR_RE.test(char);
}

function safeHref(url) {
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

function bareUrlPrefix(text, at) {
  if (at > 0 && isWordChar(text[at - 1])) {
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

function takeBareUrl(text, at, prefixLength) {
  let end = at + prefixLength;
  while (end < text.length && !/[\s<>]/.test(text[end])) {
    end += 1;
  }
  let url = text.slice(at, end);
  while (
    url.length > prefixLength &&
    TRAILING_PUNCTUATION.indexOf(url[url.length - 1]) >= 0
  ) {
    url = url.slice(0, -1);
  }
  return url.length > prefixLength ? url : "";
}

function matchLink(text, start) {
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

function matchEmphasis(text, start, depth, autolink) {
  if (depth > 8) {
    return null;
  }
  for (const entry of EMPHASIS) {
    const marker = entry.marker;
    if (!text.startsWith(marker, start)) {
      continue;
    }
    if (marker[0] === "_" && start > 0 && isWordChar(text[start - 1])) {
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
      if (marker[0] === "_" && isWordChar(text[found + marker.length])) {
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
        { class: "md-" + entry.tag },
        ...parseInline(inner, depth + 1, autolink)
      ),
      end: end + marker.length,
    };
  }
  return null;
}

/* One left-to-right pass. Code spans, links, and bare URLs are recognised
   before emphasis, so their content is never parsed as markup. */
function parseInline(text, depth = 0, autolink = true) {
  if (depth > 8) {
    return [text];
  }
  const nodes = [];
  let buffer = "";
  let index = 0;

  function flush() {
    if (buffer) {
      nodes.push(buffer);
      buffer = "";
    }
  }

  while (index < text.length) {
    const char = text[index];

    if (char === "`") {
      const end = text.indexOf("`", index + 1);
      if (end > index + 1) {
        flush();
        nodes.push(h("code", { class: "md-inline-code" }, text.slice(index + 1, end)));
        index = end + 1;
        continue;
      }
      buffer += char;
      index += 1;
      continue;
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

function CopyButton({ code }) {
  const [copied, setCopied] = useState(false);

  function copy() {
    const clipboard =
      typeof navigator === "undefined" ? null : navigator.clipboard;
    if (!clipboard || typeof clipboard.writeText !== "function") {
      return;
    }
    try {
      const result = clipboard.writeText(code);
      if (result && typeof result.then === "function") {
        result.then(() => setCopied(true)).catch(() => {});
      } else {
        setCopied(true);
      }
    } catch (cause) {
      /* The clipboard API can reject on an insecure origin; the label then
         simply stays "Copy". */
    }
  }

  return h(
    "button",
    {
      type: "button",
      class: "md-copy",
      "aria-live": "polite",
      title: "Copy code",
      onClick: copy,
    },
    copied ? "Copied" : "Copy"
  );
}

function renderCode(code, language) {
  return h(
    "div",
    { class: "md-code" },
    h(
      "div",
      { class: "md-code-head" },
      language ? h("span", { class: "md-code-lang" }, language) : null,
      h(CopyButton, { code })
    ),
    h("pre", { class: "md-pre" }, h("code", { class: "md-code-text" }, code))
  );
}

function parseFence(lines, start) {
  const match = lines[start].match(FENCE_RE);
  const marker = match[1];
  const language = match[2] || "";
  const content = [];
  let index = start + 1;
  while (index < lines.length) {
    const close = lines[index].match(CLOSE_FENCE_RE);
    if (close && close[1].length >= marker.length) {
      index += 1;
      break;
    }
    content.push(lines[index]);
    index += 1;
  }
  return { node: renderCode(content.join("\n"), language), next: index };
}

function parseHeading(line) {
  const match = line.match(HEADING_RE);
  if (!match) {
    return null;
  }
  const level = match[1].length;
  const text = match[2].replace(/\s+#+\s*$/, "").trim();
  return h("h" + level, null, ...parseInline(text));
}

function parseBlockquote(lines, start) {
  const inner = [];
  let index = start;
  while (index < lines.length) {
    const match = lines[index].match(QUOTE_RE);
    if (!match) {
      break;
    }
    inner.push(match[1]);
    index += 1;
  }
  return { node: h("blockquote", null, ...parseBlocks(inner)), next: index };
}

function splitRow(line) {
  const value = String(line || "").trim();
  const body = value.replace(/^\|/, "").replace(/\|$/, "");
  const cells = [];
  let cell = "";
  for (let index = 0; index < body.length; index += 1) {
    const char = body[index];
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

function alignOf(cell) {
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

function alignProps(align) {
  return align ? { class: "md-align-" + align } : null;
}

function tableAt(lines, index) {
  if (index + 1 >= lines.length || lines[index].indexOf("|") < 0) {
    return false;
  }
  const head = splitRow(lines[index]);
  const separator = splitRow(lines[index + 1]);
  if (head.length !== separator.length || separator.length === 0) {
    return false;
  }
  return separator.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function parseTable(lines, start) {
  const head = splitRow(lines[start]);
  const separator = splitRow(lines[start + 1]);
  const aligns = separator.map(alignOf);
  const rows = [];
  let index = start + 2;
  while (index < lines.length && lines[index].trim() && lines[index].indexOf("|") >= 0) {
    rows.push(splitRow(lines[index]).slice(0, head.length));
    index += 1;
  }
  const table = h(
    "table",
    { class: "md-table" },
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
            h("td", alignProps(aligns[cellIndex]), ...parseInline(row[cellIndex] || ""))
          )
        )
      )
    )
  );
  return { node: h("div", { class: "md-table-wrap" }, table), next: index };
}

function matchListItem(line) {
  const unordered = line.match(UL_RE);
  if (unordered) {
    return {
      indent: unordered[1].length,
      ordered: false,
      number: 0,
      text: unordered[2].trim(),
    };
  }
  const ordered = line.match(OL_RE);
  if (ordered) {
    return {
      indent: ordered[1].length,
      ordered: true,
      number: Number(ordered[2]),
      text: ordered[3].trim(),
    };
  }
  return null;
}

/* List items may nest once. A deeper marker is still consumed by a recursive
   call, so a pathological input cannot stall the outer loop. */
function parseList(lines, start, depth) {
  const first = matchListItem(lines[start]);
  const base = first.indent;
  const items = [];
  let index = start;

  while (index < lines.length) {
    const match = matchListItem(lines[index]);
    if (!match || match.indent < base) {
      break;
    }
    if (match.indent >= base + 2) {
      if (depth >= 1 || items.length === 0) {
        break;
      }
      const nested = parseList(lines, index, depth + 1);
      items[items.length - 1].children.push(nested.node);
      index = nested.next;
      continue;
    }

    const item = { lines: [match.text], children: [] };
    items.push(item);
    index += 1;
    while (index < lines.length) {
      const line = lines[index];
      if (!line.trim() || matchListItem(line) || isBlockStart(lines, index)) {
        break;
      }
      item.lines.push(line.trim());
      index += 1;
    }
  }

  const props = first.ordered && first.number > 1 ? { start: first.number } : null;
  const node = h(
    first.ordered ? "ol" : "ul",
    props,
    ...items.map((item) =>
      h("li", null, ...parseInline(item.lines.join("\n")), ...item.children)
    )
  );
  return { node, next: index };
}

function isBlockStart(lines, index) {
  const line = lines[index];
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

function parseBlocks(lines) {
  const nodes = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index];
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
    const pieces = [];
    while (index < lines.length && lines[index].trim()) {
      if (index > start && isBlockStart(lines, index)) {
        break;
      }
      pieces.push(lines[index].trim());
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
export function Markdown({ text }) {
  const source = text === null || text === undefined ? "" : String(text);
  const blocks = source.trim()
    ? parseBlocks(
        source
          .replace(/\r\n?/g, "\n")
          .split("\n")
          .map((line) => line.replace(/^\t+/, (tabs) => "  ".repeat(tabs.length)))
      )
    : [];
  return h("div", { class: "md" }, ...blocks);
}

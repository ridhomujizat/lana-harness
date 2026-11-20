/* Spec 61 REQ-WP-010: file paths in a line of terminal text, so the xterm.js
   link provider (workbench-term.tsx) can offer them to the preview panel.

   Pure: no DOM or xterm imports, so it can be unit tested on its own. */

export interface LinkMatch {
  /* Offsets into the line: start inclusive, end exclusive. */
  start: number;
  end: number;
  /* The link text sent to the preview API, the :line[:col] suffix included. */
  text: string;
  /* The :line and :line:col suffix, when the path carried one. */
  line?: number;
  col?: number;
}

/* A URI scheme, so http(s) and every other scheme:// URL is never read as a
   path (REQ-WP-010). */
const SCHEME = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//;

/* Characters trimmed from the ends of a token: quotes, brackets and sentence
   punctuation around a path ("(see src/a.go),"). */
const LEAD = "([{'\"`<";
const TRAIL = ")]}'\"`>,;";

/* A path and its optional :line[:col] suffix. */
const SUFFIX = /^(.*?)(?::(\d+)(?::(\d+))?)?$/;

/* The path rules of the native app: absolute, ~/, ./, ../, one with a slash,
   or a file name with an extension. */
function looksLikePath(base: string): boolean {
  if (!base || SCHEME.test(base)) return false;
  if (base.startsWith("/") && base.length > 1) return true; // absolute
  if (base.startsWith("~/")) return true; // relative to the home folder
  if (base.startsWith("./") || base.startsWith("../")) return true;
  if (base.includes("/") && base !== "/") return true; // any path with a slash
  /* A bare file name: it must carry an extension and at least one letter, so
     "README.md" matches and "hello" or "1.5" do not. */
  return /\.[A-Za-z0-9]+$/.test(base) && /[A-Za-z]/.test(base);
}

function parseToken(token: string): { text: string; line?: number; col?: number } | null {
  const match = SUFFIX.exec(token);
  if (!match) return null;
  const base = match[1] ?? "";
  if (!looksLikePath(base)) return null;
  const line = match[2] !== undefined ? Number(match[2]) : undefined;
  const col = match[3] !== undefined ? Number(match[3]) : undefined;
  return { text: token, line, col };
}

/* Every file path in one line of text, in order. */
export function matchLinksInLine(line: string): LinkMatch[] {
  const links: LinkMatch[] = [];
  const words = /\S+/g;
  let word: RegExpExecArray | null;
  while ((word = words.exec(line)) !== null) {
    const raw = word[0];
    let from = 0;
    while (from < raw.length && LEAD.includes(raw[from]!)) from++;
    let to = raw.length;
    /* Trim quotes, brackets, sentence punctuation and a sentence-ending
       period, but never the dot of an extension. */
    while (to > from) {
      const ch = raw[to - 1]!;
      if (ch === "." || TRAIL.includes(ch)) to--;
      else break;
    }
    if (to <= from) continue;
    const token = raw.slice(from, to);
    const parsed = parseToken(token);
    if (!parsed) continue;
    const start = word.index + from;
    links.push({ start, end: start + token.length, text: parsed.text, line: parsed.line, col: parsed.col });
  }
  return links;
}

/* A long path broken over terminal lines. xterm marks its own soft wraps
   (`wrapped`), but Claude Code and other TUIs break long lines themselves and
   indent the rest, so a hard break counts as a wrap too when the line runs to
   within EDGE_SLACK cells of the right edge. */
export interface TermLine {
  /* The line's text, trailing blanks trimmed (translateToString(true)). */
  text: string;
  /* xterm's isWrapped: this line continues the one above it. */
  wrapped: boolean;
}

export interface WrappedLink {
  text: string;
  /* Buffer rows (0-based) and columns (0-based, end exclusive). */
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
}

const EDGE_SLACK = 3;
const MAX_WRAP_LINES = 8;

interface Word {
  start: number;
  end: number;
}

function firstWord(text: string): Word | null {
  const match = /\S+/.exec(text);
  return match ? { start: match.index, end: match.index + match[0].length } : null;
}

function lastWord(text: string): Word | null {
  const match = /\S+(?=\s*$)/.exec(text);
  return match ? { start: match.index, end: match.index + match[0].length } : null;
}

/* Whether `line` runs on into `next`. */
function runsOn(line: TermLine, next: TermLine | undefined, cols: number): boolean {
  if (!next || !next.text.trim()) return false;
  if (next.wrapped) return true;
  return line.text.trimEnd().length >= cols - EDGE_SLACK;
}

/* The whole path when `match` (found on `row`) is one piece of a path broken
   over lines: the last word of a line that runs on, or the first word of the
   line after one. Null when the match stands alone. */
export function wrappedLink(
  getLine: (row: number) => TermLine | undefined,
  row: number,
  match: LinkMatch,
  cols: number
): WrappedLink | null {
  const here = getLine(row);
  if (!here) return null;
  const first = firstWord(here.text);
  const last = lastWord(here.text);
  const inWord = (word: Word | null) => word !== null && match.start >= word.start && match.end <= word.end;
  const isFirst = inWord(first);
  const isLast = inWord(last);
  const word = isFirst ? first : isLast ? last : null;
  if (!word) return null;

  const parts = [here.text.slice(word.start, word.end)];
  let startRow = row;
  let startCol = word.start;
  let endRow = row;
  let endCol = word.end;

  if (isFirst) {
    let cur = here;
    for (let n = 0; n < MAX_WRAP_LINES; n++) {
      const prev = getLine(startRow - 1);
      if (!prev || !runsOn(prev, cur, cols)) break;
      const piece = lastWord(prev.text);
      if (!piece) break;
      parts.unshift(prev.text.slice(piece.start, piece.end));
      startRow--;
      startCol = piece.start;
      /* Go further up only while the piece fills its whole line. */
      if (firstWord(prev.text)?.start !== piece.start) break;
      cur = prev;
    }
  }
  if (isLast) {
    let cur = here;
    for (let n = 0; n < MAX_WRAP_LINES; n++) {
      const next = getLine(endRow + 1);
      if (!next || !runsOn(cur, next, cols)) break;
      const piece = firstWord(next.text);
      if (!piece) break;
      parts.push(next.text.slice(piece.start, piece.end));
      endRow++;
      endCol = piece.end;
      if (lastWord(next.text)?.start !== piece.start) break;
      cur = next;
    }
  }
  if (parts.length < 2) return null;

  const joined = parts.join("");
  const [link] = matchLinksInLine(joined);
  if (!link) return null;
  /* Quotes or punctuation trimmed off the ends of the joined word. */
  const lead = link.start;
  const trail = joined.length - link.end;
  return {
    text: link.text,
    startRow,
    startCol: startCol + Math.min(lead, parts[0]!.length - 1),
    endRow,
    endCol: Math.max(endCol - trail, 1),
  };
}

/* Activation: ⌘+click on macOS, Ctrl+click elsewhere, a plain tap on a touch
   device (REQ-WP-010). The platform is a parameter so the rules are testable. */
export interface ActivationEvent {
  metaKey: boolean;
  ctrlKey: boolean;
}

export function isMacPlatform(nav: Pick<Navigator, "platform" | "userAgent"> | undefined = typeof navigator !== "undefined" ? navigator : undefined): boolean {
  const text = nav?.platform || nav?.userAgent || "";
  return /Mac|iPhone|iPad|iPod/.test(text);
}

export function isTouchPlatform(nav: Pick<Navigator, "maxTouchPoints"> | undefined = typeof navigator !== "undefined" ? navigator : undefined): boolean {
  if (nav && nav.maxTouchPoints > 0) return true;
  return typeof window !== "undefined" && "ontouchstart" in window;
}

export function activationAllowed(
  event: ActivationEvent,
  mac: boolean = isMacPlatform(),
  touch: boolean = isTouchPlatform()
): boolean {
  if (touch) return true;
  return mac ? event.metaKey : event.ctrlKey;
}

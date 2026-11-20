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

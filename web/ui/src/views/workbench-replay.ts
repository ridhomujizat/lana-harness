/* Replay size markers (owner report 2026-11-19: lines vanished after an
   attach when the session had been used at another width).

   The Workbench core keeps a session's raw pty output and replays it on every
   attach. That output was produced at several pty sizes (the phone and the Mac
   take turns owning the size), so the core marks each size change inside the
   replay (internal/workbench/proto.go):

     size:  ESC ] 7701 ; <cols> ; <rows> BEL
     end:   ESC ] 7702 BEL   (the last bytes of the replay)

   The pane replays each stretch of bytes at the size it was drawn for, so
   zsh's redraws of a wrapped prompt land on the right rows. */

export type ReplayPart =
  | { kind: "data"; bytes: Uint8Array }
  | { kind: "size"; cols: number; rows: number }
  | { kind: "end" };

const ESC = 0x1b;
const BEL = 0x07;
/* "\x1b]770" — every marker starts with these bytes. */
const PREFIX = [ESC, 0x5d, 0x37, 0x37, 0x30];
/* A marker is short; anything longer is not one of ours. */
const MAX_MARKER = 32;

/* True when the replay opens with a size marker: a core that knows about
   markers always sends the base size first. */
export function startsWithSizeMarker(data: Uint8Array): boolean {
  const head = [...PREFIX, 0x31, 0x3b]; // "\x1b]7701;"
  if (data.length < head.length) return false;
  return head.every((b, i) => data[i] === b);
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
  if (a.length === 0) return b;
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

/* Length of the longest tail of buf that is a proper prefix of PREFIX: those
   bytes may be the start of a marker split across messages. */
function partialPrefixTail(buf: Uint8Array): number {
  for (let n = Math.min(PREFIX.length - 1, buf.length); n > 0; n--) {
    let ok = true;
    for (let i = 0; i < n; i++) {
      if (buf[buf.length - n + i] !== PREFIX[i]) {
        ok = false;
        break;
      }
    }
    if (ok) return n;
  }
  return 0;
}

function parseMarker(body: string): ReplayPart | null {
  if (body === "2") return { kind: "end" };
  const m = /^1;(\d{1,4});(\d{1,4})$/.exec(body);
  if (!m) return null;
  const cols = Number(m[1]);
  const rows = Number(m[2]);
  if (cols < 1 || rows < 1) return null;
  return { kind: "size", cols, rows };
}

/* Splits one message into data and markers. carry holds the bytes kept from
   the previous message (a marker cut in two); the returned carry goes into
   the next call. Bytes that only look like a marker pass through as data. */
export function splitReplay(carry: Uint8Array, data: Uint8Array): { parts: ReplayPart[]; carry: Uint8Array } {
  const buf = concat(carry, data);
  const parts: ReplayPart[] = [];
  let start = 0;
  let i = 0;
  const flush = (end: number) => {
    if (end > start) parts.push({ kind: "data", bytes: buf.subarray(start, end) });
  };
  while (i < buf.length) {
    if (buf[i] !== ESC) {
      i++;
      continue;
    }
    if (buf.length - i < PREFIX.length) break;
    let isPrefix = true;
    for (let k = 1; k < PREFIX.length; k++) {
      if (buf[i + k] !== PREFIX[k]) {
        isPrefix = false;
        break;
      }
    }
    if (!isPrefix) {
      i++;
      continue;
    }
    const limit = Math.min(buf.length, i + MAX_MARKER);
    let bel = -1;
    for (let k = i + PREFIX.length; k < limit; k++) {
      if (buf[k] === BEL) {
        bel = k;
        break;
      }
    }
    if (bel < 0) {
      if (limit === buf.length && buf.length - i < MAX_MARKER) {
        /* Cut off at the end of the message: wait for the rest. */
        flush(i);
        return { parts, carry: buf.slice(i) };
      }
      i++;
      continue;
    }
    let body = "";
    for (let k = i + PREFIX.length; k < bel; k++) body += String.fromCharCode(buf[k]!);
    const marker = parseMarker(body);
    if (!marker) {
      i = bel + 1;
      continue;
    }
    flush(i);
    parts.push(marker);
    i = bel + 1;
    start = i;
  }
  /* The message ended inside a possible marker start ("\x1b]7"). */
  const tail = start < buf.length ? partialPrefixTail(buf.subarray(start)) : 0;
  const end = buf.length - tail;
  flush(end);
  return { parts, carry: tail > 0 ? buf.slice(end) : new Uint8Array(0) };
}

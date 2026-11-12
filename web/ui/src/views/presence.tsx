import { useSyncExternalStore } from "react";
import type { CSSProperties } from "react";
import { listRooms, openRoomsStream } from "../api/rooms";
import { agentCssColor, isHexColor, parseAvatarSvg } from "../lib/avatar-svg";
import { useAgentAvatar } from "../lib/avatar-store";
import { moodFromPresence } from "../lib/avatar-motion";
import type { AvatarMood } from "../lib/avatar-motion";
import { MoodLayer, moodHostProps } from "../ui/gr8r";
import { AvatarImage, useAvatarImage } from "./agent-avatar";
import type { Presence, RoomRow } from "../api/rooms";

/* The presence kit of REQ-ROOM-006 and REQ-ROOM-013: the agent face, the
   presence label, skeletons, the typing dots, the unread badge, and the shared
   useRooms() store. Colors come from the design (rooms-1..4). */

export type FaceSize = 18 | 24 | 32 | 44 | 52 | 96;

/* The tinted background and strong foreground of an agent color come from the
   theme-aware custom properties of rooms.css, so the faces are dark-tinted with
   readable letters in dark mode and unchanged in light mode (REQ-ROOM-006). */
const FACE_TONES = ["brand", "success", "warning", "error", "blue", "gray"] as const;

type FaceTone = (typeof FACE_TONES)[number];

function faceTone(color?: string): FaceTone {
  return color && (FACE_TONES as readonly string[]).includes(color)
    ? (color as FaceTone)
    : "brand";
}

/* The tinted background and strong foreground of an agent color. The group
   room uses them for the handoff chips and the message names (REQ-ROOM-008).
   A hex color (spec 43) tints the same way through color-mix, so it follows
   the theme like the tokens. */
export function agentFaceColors(color?: string): { bg: string; fg: string } {
  if (isHexColor(color)) {
    const hex = color.trim();
    return {
      bg: "color-mix(in srgb, " + hex + " 16%, var(--bg-primary))",
      fg: "color-mix(in srgb, " + hex + " 72%, var(--text-primary))",
    };
  }
  const tone = faceTone(color);
  return {
    bg: "var(--face-" + tone + "-bg)",
    fg: "var(--face-" + tone + "-fg)",
  };
}

/* The agent color as a Gr8r token, so a Gr8r Avatar keeps the same palette as
   the tinted AgentFace and the roster (REQ-ROOM-006, REQ-ROOM-011). A hex
   color (spec 43) passes through as the CSS color. */
export function gr8rColor(color?: string): string {
  return agentCssColor(color, "var(--violet)");
}

/* The presence dot of the face; the label colors of PresenceLabel are a
   separate, darker set. Both read theme-aware custom properties. */
const DOT_COLORS: Record<string, string> = {
  waiting: "var(--presence-dot-waiting)",
  working: "var(--presence-dot-working)",
  problem: "var(--presence-dot-problem)",
  online: "var(--presence-dot-online)",
  resting: "var(--presence-dot-resting)",
};

const LABEL_COLORS: Record<string, string> = {
  waiting: "var(--presence-label-waiting)",
  working: "var(--presence-label-working)",
  problem: "var(--presence-label-problem)",
};

function initialOf(name: string): string {
  return (Array.from(String(name || "").trim())[0] || "?").toUpperCase();
}

export interface AgentFaceProps {
  name: string;
  color?: string;
  presence?: Presence | null;
  size?: FaceSize;
  className?: string;
  style?: CSSProperties;
  /* The motion mood (lib/avatar-motion); defaults to the presence's. */
  mood?: AvatarMood;
}

export function AgentFace({
  name,
  color,
  presence,
  size = 32,
  className = "",
  style,
  mood,
}: AgentFaceProps) {
  const face = agentFaceColors(color);
  const stored = useAgentAvatar(name);
  const shape = stored && stored.svg ? parseAvatarSvg(stored.svg) : null;
  /* Spec 43: names the store has not seen use the server-rendered avatar;
     the letter is left for an image that fails (a deleted agent). */
  const image = useAvatarImage(shape ? "" : name);
  const state = presence ? presence.state : "";
  const label = presence ? name + ", " + state : name;
  const blob = Boolean(shape) || !image.failed;
  const look: CSSProperties = blob
    ? { background: "transparent", color: gr8rColor(stored?.color || color) }
    : { background: face.bg, color: face.fg };
  const moving = shape
    ? moodHostProps(mood || (presence ? moodFromPresence(state) : undefined), size)
    : {};
  return (
    <span
      className={
        "agent-face size-" +
        size +
        (blob ? " has-svg" : "") +
        (className ? " " + className : "")
      }
      role="img"
      aria-label={label}
      style={{ ...look, ...moving.style, ...style }}
      data-mood={moving["data-mood"]}
    >
      {shape ??
        (!image.failed ? (
          <AvatarImage src={image.src} onError={image.onError} />
        ) : (
          initialOf(name)
        ))}
      {shape ? <MoodLayer mood={moving["data-mood"]} px={size} /> : null}
      {state === "working" ? (
        <span className="agent-face-ring" aria-hidden="true" />
      ) : null}
      {presence ? (
        <span
          className={"agent-face-dot" + (state ? " state-" + state : "")}
          aria-hidden="true"
          style={{ background: DOT_COLORS[state] || DOT_COLORS.resting }}
        />
      ) : null}
    </span>
  );
}

export function PresenceLabel({ presence }: { presence?: Presence | null }) {
  if (!presence) {
    return null;
  }
  const color = LABEL_COLORS[presence.state] || "var(--fg-tertiary)";
  return (
    <span className={"presence-label state-" + presence.state}>
      <span
        className="presence-label-dot"
        style={{ background: color }}
        aria-hidden="true"
      />
      <span className="presence-label-text" style={{ color }}>
        {presence.label}
      </span>
    </span>
  );
}

export function TypingDots() {
  return (
    <span className="typing-dots" role="status" aria-label="Typing">
      <span />
      <span />
      <span />
    </span>
  );
}

export interface SkeletonProps {
  width?: number | string;
  height?: number | string;
  radius?: number | string;
  className?: string;
}

export function Skeleton({ width, height, radius, className }: SkeletonProps) {
  return (
    <span
      className={"skeleton" + (className ? " " + className : "")}
      aria-hidden="true"
      style={{ width, height, borderRadius: radius }}
    />
  );
}

export function SkeletonRows({ count = 3 }: { count?: number }) {
  return (
    <div className="skeleton-rows" aria-hidden="true">
      {Array.from({ length: count }).map((_, index) => (
        <Skeleton
          key={index}
          className="skeleton-row"
          height={44}
          radius={10}
        />
      ))}
    </div>
  );
}

export interface FaceRef {
  name: string;
  /* label is the human name to show; name stays the ID for the color and the
     identity checks. It falls back to name when unset. */
  label?: string;
  color?: string;
  presence?: Presence | null;
}

/* REQ-ROOM-001: the team avatar stacks up to two member faces. The faces are
   absolutely positioned inside a box the size of the slot, so the cluster never
   overlaps the text column beside it. */
export function TeamFace({
  faces,
  size = 24,
}: {
  faces: FaceRef[];
  size?: FaceSize;
}) {
  const memberSize = Math.round(size * 0.64);
  return (
    <span
      className="team-face"
      aria-hidden="true"
      style={{ width: size, height: size }}
    >
      {faces.slice(0, 2).map((face) => (
        <AgentFace
          key={face.name}
          name={face.name}
          color={face.color}
          presence={face.presence}
          size={size}
          className="team-face-member"
          style={{
            width: memberSize,
            height: memberSize,
            fontSize: Math.round(memberSize * 0.42),
          }}
        />
      ))}
    </span>
  );
}

export interface RoomsState {
  rows: RoomRow[];
  byName: Record<string, RoomRow>;
  connected: boolean;
  loading: boolean;
}

function indexRows(rows: RoomRow[]): Record<string, RoomRow> {
  const byName: Record<string, RoomRow> = {};
  for (const row of rows) {
    byName[row.name] = row;
  }
  return byName;
}

/* One shared stream for the whole app: it opens on the first subscriber and
   closes when none remain. The first subscriber also loads the rows with
   listRooms() at once, in parallel with the stream, so the list never waits on
   the stream. If no `rooms` event arrives within 4 s (a slow server or a
   browser that cannot read the streamed body) it falls back to a 10 s
   listRooms() poll until a `rooms` event arrives; a stream error starts the
   same poll at once (REQ-ROOM-007). */
let roomsState: RoomsState = {
  rows: [],
  byName: {},
  connected: false,
  loading: true,
};

const listeners = new Set<() => void>();
let stream: { close: () => void } | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let slowTimer: ReturnType<typeof setTimeout> | null = null;
let roomsEventSeen = false;

const POLL_INTERVAL_MS = 10000;
const SLOW_STREAM_MS = 4000;

function emit(): void {
  for (const listener of listeners) {
    listener();
  }
}

/* API-ROOM-001 order, as the server's sortRoomRows: last_active_at descending,
   ties by name; rooms with no session last, by name. A live `room` event
   updates one row, so the list is re-sorted here or a new or newly active
   room would keep its old place. */
export function sortRooms(rows: RoomRow[]): RoomRow[] {
  return rows.slice().sort((a, b) => {
    const at = Number(a.last_active_at) || 0;
    const bt = Number(b.last_active_at) || 0;
    if (at > 0 && bt > 0 && at !== bt) {
      return bt - at;
    }
    if (at > 0 !== bt > 0) {
      return at > 0 ? -1 : 1;
    }
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
}

function setState(patch: Partial<RoomsState>): void {
  roomsState = { ...roomsState, ...patch };
  emit();
}

async function poll(): Promise<void> {
  try {
    const rows = await listRooms();
    setState({ rows, byName: indexRows(rows), loading: false });
  } catch {
    /* Keep the last rows; the next tick retries. */
  }
}

function startPolling(): void {
  if (pollTimer !== null) {
    return;
  }
  void poll();
  pollTimer = setInterval(() => {
    void poll();
  }, POLL_INTERVAL_MS);
}

function stopPolling(): void {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function clearSlowTimer(): void {
  if (slowTimer !== null) {
    clearTimeout(slowTimer);
    slowTimer = null;
  }
}

function ensureStream(): void {
  if (stream !== null) {
    return;
  }
  roomsEventSeen = false;
  stream = openRoomsStream({
    onRooms(rows) {
      /* The stream delivered. Stop the slow-stream fallback and update. */
      roomsEventSeen = true;
      clearSlowTimer();
      stopPolling();
      setState({ rows, byName: indexRows(rows), loading: false });
    },
    onRoom(row) {
      const exists = roomsState.rows.some((item) => item.name === row.name);
      const rows = exists
        ? roomsState.rows.map((item) => (item.name === row.name ? row : item))
        : [...roomsState.rows, row];
      const sorted = sortRooms(rows);
      setState({ rows: sorted, byName: indexRows(sorted), loading: false });
    },
    onState(connected) {
      setState({ connected });
      /* A stream error starts the fallback poll; only a `rooms` event stops it. */
      if (!connected) {
        startPolling();
      }
    },
  });
  /* The stream is open but may never deliver a `rooms` event. Fall back to the
     10 s poll when nothing arrived within 4 s. */
  slowTimer = setTimeout(() => {
    slowTimer = null;
    if (!roomsEventSeen) {
      startPolling();
    }
  }, SLOW_STREAM_MS);
}

function releaseStream(): void {
  clearSlowTimer();
  roomsEventSeen = false;
  if (stream) {
    stream.close();
    stream = null;
  }
  stopPolling();
  if (roomsState.connected) {
    setState({ connected: false });
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) {
    /* Load the first rows at once, in parallel with the stream. */
    void poll();
    ensureStream();
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      releaseStream();
    }
  };
}

function getSnapshot(): RoomsState {
  return roomsState;
}

export function useRooms(): RoomsState {
  return useSyncExternalStore(subscribe, getSnapshot);
}

/* Reloads the room rows now, for example after a team was created or saved
   (spec 47 New group room, Room settings). */
export function refreshRooms(): Promise<void> {
  return poll();
}

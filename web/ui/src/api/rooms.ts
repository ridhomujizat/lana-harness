import { queryString, request } from "./client";
import { rememberAvatar, rememberAvatars } from "../lib/avatar-store";
import type { WorkflowCommandReply } from "./turns";
import { watchVisibility } from "./visibility";

/* Room calls of API-ROOM-001 to API-ROOM-005 and API-ROOM-008. Types are
   written by hand from the Go JSON structs. */

/* The shared presence object of API-ROOM-001 and API-ROOM-008. */
export interface Presence {
  state: string;
  label: string;
  since: number;
  session_id: string;
}

/* One API-ROOM-001 row: an agent or a team with its live state. */
export interface RoomRow {
  name: string;
  kind: string;
  title: string;
  color: string;
  /* Spec 43: the saved or default avatar of the agent or team. */
  avatar_svg?: string;
  avatar_source?: "saved" | "default";
  members: string[];
  lead: string;
  presence: Presence;
  last_message: RoomLastMessage | null;
  unread: number;
  turns_7d: number;
  last_active_at: number;
}

export interface RoomLastMessage {
  role: string;
  agent: string;
  preview: string;
  at: number;
  session_id: string;
  channel: string;
}

/* One API-ROOM-002 segment: a top-level session inside a room. */
export interface Segment {
  session_id: string;
  channel: string;
  address: string;
  created_at: number;
  updated_at: number;
  turn_count: number;
  status: string;
}

export interface RoomSegmentsParams {
  before?: number;
  limit?: number;
}

export interface RoomSegmentsResponse {
  segments: Segment[];
  has_more: boolean;
}

/* The running turn of an agent overview (API-ROOM-008 `now`). */
export interface OverviewStep {
  name: string;
  status: string;
  at: number;
}

export interface OverviewNow {
  session_id: string;
  turn_id: string;
  started_at: number;
  steps: OverviewStep[];
}

export interface OverviewActivity {
  kind: string;
  title: string;
  detail: string;
  at: number;
  session_id: string;
}

export interface OverviewDay {
  day: string;
  turns: number;
}

export interface OverviewWeek {
  days: OverviewDay[];
  turns: number;
  questions: number;
  notes: number;
  tokens: number;
}

export interface OverviewChannel {
  channel: string;
  match: string;
}

export interface OverviewLooksAfter {
  channels: OverviewChannel[];
  teams: string[];
}

/* API-ROOM-008: the alive agent profile payload. */
export interface AgentOverview {
  name: string;
  kind: string;
  title: string;
  color: string;
  /* Spec 43: the saved or default avatar of the agent or team. */
  avatar_svg?: string;
  avatar_source?: "saved" | "default";
  persona_line: string;
  created_at: number;
  presence: Presence;
  now: OverviewNow | null;
  activity: OverviewActivity[];
  week: OverviewWeek;
  memory: { entries: string[]; total: number };
  job: string;
  looks_after: OverviewLooksAfter;
}

export interface SendRoomRequest {
  text: string;
  attachments?: string[];
  new_topic?: boolean;
  /* API-ROOM-003: the seq of the automation message this send replies to. */
  reply_to?: number;
}

export interface SendRoomResponse {
  session_id: string;
  created: boolean;
  turn: string;
  turn_id?: string;
  /* API-WF-021: set when the text was a workflow chat command. */
  command?: WorkflowCommandReply;
}

export interface MarkRoomReadResponse {
  read_at: number;
}

export function listRooms(): Promise<RoomRow[]> {
  return request<{ rooms: RoomRow[] }>("GET", "/v1/rooms").then((response) => {
    const rows = (response && response.rooms) || [];
    rememberAvatars(rows);
    return rows;
  });
}

export function roomSegments(
  name: string,
  params?: RoomSegmentsParams
): Promise<RoomSegmentsResponse> {
  return request<RoomSegmentsResponse>(
    "GET",
    "/v1/rooms/" + encodeURIComponent(name) + "/segments" + queryString(params)
  );
}

/* API-ROOM-003: send to the room's current web session. The attachments key is
   sent only when there are paths, as in sendMessage. */
export function sendToRoom(
  name: string,
  body: SendRoomRequest
): Promise<SendRoomResponse> {
  const attachments = body.attachments || [];
  const payload: SendRoomRequest = { text: body.text };
  if (attachments.length > 0) {
    payload.attachments = attachments;
  }
  if (body.new_topic) {
    payload.new_topic = true;
  }
  if (body.reply_to && body.reply_to > 0) {
    payload.reply_to = body.reply_to;
  }
  return request<SendRoomResponse>(
    "POST",
    "/v1/rooms/" + encodeURIComponent(name) + "/messages",
    payload
  );
}

export function markRoomRead(name: string): Promise<MarkRoomReadResponse> {
  return request<MarkRoomReadResponse>(
    "POST",
    "/v1/rooms/" + encodeURIComponent(name) + "/read"
  );
}

export function getAgentOverview(name: string): Promise<AgentOverview> {
  return request<AgentOverview>(
    "GET",
    "/v1/agents/" + encodeURIComponent(name) + "/overview"
  ).then((data) => {
    rememberAvatar(data);
    return data;
  });
}

export interface RoomsStreamHandlers {
  onRooms: (rows: RoomRow[]) => void;
  onRoom: (row: RoomRow) => void;
  onState?: (connected: boolean) => void;
}

export interface RoomsStream {
  close: () => void;
}

/* API-ROOM-005: GET /v1/rooms/stream, modelled on openStream in client.ts. The
   reader reconnects after 1 s, handles `rooms`, `room` and `shutdown`, and
   ignores the `: ping` comments. On connect the server sends one full `rooms`
   event, then a `room` event per changed row. */
export function openRoomsStream(handlers: RoomsStreamHandlers): RoomsStream {
  let closed = false;
  let controller: AbortController | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;

  /* A hidden tab releases its connection; the server sends a fresh `rooms`
     snapshot on the next connect, so the list is complete when it returns. */
  const visibility = watchVisibility({
    onRelease: () => {
      if (retryTimer !== null) {
        clearTimeout(retryTimer);
        retryTimer = null;
      }
      controller?.abort();
    },
    onResume: () => {
      void connect();
    },
  });

  function scheduleReconnect(): void {
    if (closed || visibility.blocked()) {
      return;
    }
    if (handlers.onState) {
      handlers.onState(false);
    }
    retryTimer = setTimeout(connect, 1000);
  }

  function dispatchBlock(block: string): void {
    const lines = block.split("\n");
    let eventName = "message";
    const dataLines: string[] = [];
    for (const rawLine of lines) {
      const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
      if (!line || line.startsWith(":")) {
        continue;
      }
      const colon = line.indexOf(":");
      const field = colon >= 0 ? line.slice(0, colon) : line;
      let value = colon >= 0 ? line.slice(colon + 1) : "";
      if (value.startsWith(" ")) {
        value = value.slice(1);
      }
      if (field === "event") {
        eventName = value;
      } else if (field === "data") {
        dataLines.push(value);
      }
    }
    if (dataLines.length === 0) {
      return;
    }
    let payload: unknown;
    try {
      payload = JSON.parse(dataLines.join("\n"));
    } catch {
      return;
    }
    if (eventName === "rooms") {
      const rows =
        payload && typeof payload === "object"
          ? (payload as { rooms?: RoomRow[] }).rooms
          : undefined;
      if (Array.isArray(rows)) {
        rememberAvatars(rows);
        handlers.onRooms(rows);
      }
      return;
    }
    if (eventName === "room") {
      if (payload && typeof payload === "object") {
        rememberAvatar(payload as RoomRow);
        handlers.onRoom(payload as RoomRow);
      }
      return;
    }
    if (eventName === "shutdown" && handlers.onState) {
      handlers.onState(false);
    }
  }

  async function consume(response: Response): Promise<void> {
    if (!response.body) {
      scheduleReconnect();
      return;
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    try {
      while (!closed) {
        const result = await reader.read();
        if (result.done) {
          break;
        }
        buffer += decoder.decode(result.value, { stream: true });
        let index = buffer.indexOf("\n\n");
        while (index >= 0) {
          const block = buffer.slice(0, index);
          buffer = buffer.slice(index + 2);
          dispatchBlock(block);
          index = buffer.indexOf("\n\n");
        }
      }
    } catch {
      if (!closed && !visibility.blocked() && handlers.onState) {
        handlers.onState(false);
      }
    } finally {
      try {
        reader.releaseLock();
      } catch {
        /* The stream is already closed. */
      }
    }
    if (!closed) {
      scheduleReconnect();
    }
  }

  async function connect(): Promise<void> {
    if (closed || visibility.blocked()) {
      return;
    }
    controller = new AbortController();
    let response: Response;
    try {
      response = await fetch("/v1/rooms/stream", {
        method: "GET",
        headers: { Accept: "text/event-stream" },
        credentials: "same-origin",
        signal: controller.signal,
      });
    } catch {
      if (!closed) {
        scheduleReconnect();
      }
      return;
    }
    if (!response.ok) {
      scheduleReconnect();
      return;
    }
    if (handlers.onState) {
      handlers.onState(true);
    }
    await consume(response);
  }

  void connect();

  return {
    close() {
      closed = true;
      visibility.close();
      if (retryTimer !== null) {
        clearTimeout(retryTimer);
        retryTimer = null;
      }
      if (controller) {
        controller.abort();
      }
    },
  };
}

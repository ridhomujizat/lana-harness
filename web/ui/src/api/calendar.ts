import { ApiError, queryString, request } from "./client";

/* Calendar calls of API-CAL-001 to API-CAL-015 (spec #35). Types follow the
   contract's shared objects. Instants are Unix ms; dates are YYYY-MM-DD and
   times HH:MM in the calendar time zone. */

export type ItemKind = "event" | "task" | "reminder" | "note" | "habit";
export type EntrySource = "item" | "workflow_slot" | "workflow_run" | "google";
export type RepeatFreq = "none" | "daily" | "weekdays" | "weekly" | "monthly" | "yearly";
export type Weekday = "mon" | "tue" | "wed" | "thu" | "fri" | "sat" | "sun";
export type ItemColor = "gray" | "indigo" | "blue" | "teal" | "green" | "amber" | "rose" | "violet";
export type FiringStatus = "scheduled" | "running" | "done" | "failed" | "missed" | "dismissed";

export interface Repeat {
  freq: RepeatFreq;
  days: Weekday[];
  until: string;
}

export type Action =
  | { type: "agent_prompt"; agent: string; prompt: string }
  | { type: "run_workflow"; workflow_id: string; input?: unknown }
  | { type: "send_message"; target: "telegram" | "web"; text: string };

export interface GoogleRef {
  calendar_id: string;
  event_id: string;
  sync: "synced" | "pending" | "error";
}

export interface Item {
  id: string;
  kind: ItemKind;
  title: string;
  notes: string;
  start: number;
  end: number;
  all_day: boolean;
  date: string;
  end_date: string;
  time: string;
  repeat: Repeat;
  assignee: string;
  color: ItemColor | "";
  state: "" | "open" | "done";
  action: Action | null;
  google: GoogleRef | null;
  created_by: string;
  updated_by: string;
  created_at: number;
  updated_at: number;
}

/* The writable part of an item (API-CAL-002 / API-CAL-004). */
export type ItemInput = Partial<
  Omit<Item, "id" | "google" | "created_by" | "updated_by" | "created_at" | "updated_at">
>;

export interface Firing {
  status: FiringStatus;
  at: number;
  started_at: number;
  ended_at: number;
  session_id: string;
  run: { workflow_id: string; number: number } | null;
  error: string;
}

export interface EntryWorkflow {
  id: string;
  name: string;
  guardian: string;
  node_id: string;
  trigger: "schedule" | "interval";
  every_minutes: number;
  run_number: number;
  run_status: string;
}

export interface Entry {
  key: string;
  source: EntrySource;
  kind: ItemKind | "workflow";
  title: string;
  start: number;
  end: number;
  all_day: boolean;
  date: string;
  end_date: string;
  color: string;
  status: string;
  moved: boolean;
  occurrence: number;
  item_id: string;
  repeating: boolean;
  assignee: string;
  created_by: string;
  action: Action | null;
  firing: Firing | null;
  habit: { done: boolean; streak: number } | null;
  workflow: EntryWorkflow | null;
  google: (GoogleRef & { color?: string; calendar_name?: string }) | null;
  readonly: boolean;
}

export type GoogleState = "off" | "linked" | "syncing" | "needs_reauth" | "error";

export interface EntriesResponse {
  entries: Entry[];
  timezone: string;
  week_start: number;
  now: number;
  google: { state: GoogleState };
  missed: number;
}

export interface ItemDetail {
  item: Item;
  firings: Firing[];
  history: { at: number; by: string; summary: string }[];
}

export interface CalendarSettings {
  timezone: string;
  week_start: number;
  agenda: { enabled: boolean; time: string };
  ics: { url: string };
}

export interface GoogleCalendar {
  id: string;
  name: string;
  color: string;
  selected: boolean;
  writable: boolean;
}

export interface GoogleLink {
  client_configured: boolean;
  state: GoogleState;
  account: string;
  calendars: GoogleCalendar[];
  target: string;
  last_sync: number;
  error: string;
}

export interface OverrideInput {
  source: "item" | "workflow";
  ref: string;
  occurrence: number;
  op: "move" | "skip";
  to?: number;
}

const base = "/v1/calendar";

export function listEntries(from: number, to: number, sources?: string): Promise<EntriesResponse> {
  return request<EntriesResponse>("GET", base + "/entries" + queryString({ from, to, sources }));
}

export function createItem(item: ItemInput): Promise<{ item: Item }> {
  return request("POST", base + "/items", item);
}

export function getItem(id: string): Promise<ItemDetail> {
  return request("GET", base + "/items/" + encodeURIComponent(id));
}

export function updateItem(id: string, fields: ItemInput): Promise<{ item: Item }> {
  return request("PATCH", base + "/items/" + encodeURIComponent(id), fields);
}

export function deleteItem(id: string): Promise<void> {
  return request("DELETE", base + "/items/" + encodeURIComponent(id));
}

export function saveOverride(input: OverrideInput): Promise<unknown> {
  return request("POST", base + "/overrides", input);
}

export function clearOverride(source: string, ref: string, occurrence: number): Promise<void> {
  return request("DELETE", base + "/overrides" + queryString({ source, ref, occurrence }));
}

export function completeItem(
  id: string,
  occurrence: number,
  done: boolean
): Promise<{ item: Item; habit?: { done: boolean; streak: number } }> {
  return request("POST", base + "/items/" + encodeURIComponent(id) + "/complete", { occurrence, done });
}

export function runFiring(itemId: string, occurrence: number): Promise<{ firing: Firing }> {
  return request("POST", base + "/firings/run", { item_id: itemId, occurrence });
}

export function dismissFiring(itemId: string, occurrence: number): Promise<{ firing: Firing }> {
  return request("POST", base + "/firings/dismiss", { item_id: itemId, occurrence });
}

export function getSettings(): Promise<CalendarSettings> {
  return request("GET", base + "/settings");
}

export function putSettings(settings: Omit<CalendarSettings, "ics">): Promise<CalendarSettings> {
  return request("PUT", base + "/settings", settings);
}

export function rotateICS(): Promise<{ url: string }> {
  return request("POST", base + "/ics/rotate");
}

export function getGoogle(): Promise<GoogleLink> {
  return request("GET", base + "/google");
}

export function putGoogle(calendars: { id: string; selected: boolean }[], target: string): Promise<GoogleLink> {
  return request("PUT", base + "/google", { calendars, target });
}

export function putGoogleClient(clientId: string, clientSecret: string): Promise<void> {
  return request("PUT", base + "/google/client", { client_id: clientId, client_secret: clientSecret });
}

export function connectGoogle(): Promise<{ auth_url: string }> {
  return request("POST", base + "/google/connect");
}

export function syncGoogle(): Promise<void> {
  return request("POST", base + "/google/sync");
}

export function disconnectGoogle(): Promise<void> {
  return request("POST", base + "/google/disconnect");
}

/* True when the daemon has no calendar backend (an older build): the page
   then falls back to workflow data (design "Fallback"). */
export function isMissingBackend(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

export interface CalendarChange {
  what: string;
  item_id?: string;
}

/* API-CAL-015: a fetch-based SSE reader (same shape as the workflow stream)
   that reconnects after a second and stops for good on 404. */
export function openCalendarStream(onChange: (change: CalendarChange) => void): { close(): void } {
  let closed = false;
  let controller: AbortController | null = null;
  let retry: ReturnType<typeof setTimeout> | null = null;

  const again = () => {
    if (!closed) {
      retry = setTimeout(() => void connect(), 1000);
    }
  };

  function dispatch(block: string) {
    let name = "message";
    const data: string[] = [];
    for (const raw of block.split("\n")) {
      const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
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
        name = value;
      } else if (field === "data") {
        data.push(value);
      }
    }
    if (name !== "changed" || data.length === 0) {
      return;
    }
    try {
      const payload = JSON.parse(data.join("\n")) as CalendarChange;
      if (payload && typeof payload.what === "string") {
        onChange(payload);
      }
    } catch {
      /* A broken frame is skipped; the next change reloads anyway. */
    }
  }

  async function connect(): Promise<void> {
    if (closed) {
      return;
    }
    controller = new AbortController();
    let response: Response;
    try {
      response = await fetch(base + "/stream", {
        method: "GET",
        headers: { Accept: "text/event-stream" },
        credentials: "same-origin",
        signal: controller.signal,
      });
    } catch {
      again();
      return;
    }
    if (response.status === 404) {
      return;
    }
    if (!response.ok || !response.body) {
      again();
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
          dispatch(buffer.slice(0, index));
          buffer = buffer.slice(index + 2);
          index = buffer.indexOf("\n\n");
        }
      }
    } catch {
      /* Reconnect below. */
    }
    again();
  }

  void connect();
  return {
    close() {
      closed = true;
      if (retry !== null) {
        clearTimeout(retry);
      }
      controller?.abort();
    },
  };
}

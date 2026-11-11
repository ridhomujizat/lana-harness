import { request } from "./client";

/* Spec 47: Stop (API-RDS-011) and room memory (API-RDS-012). */

export function stopDiscussion(sessionId: string, mode: "stop" | "direct"): Promise<{ stopped: boolean }> {
  return request<{ stopped: boolean }>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(sessionId) + "/discussion/stop",
    { mode }
  );
}

export type RoomNoteKind = "decision" | "fact" | "open";

export interface RoomNote {
  id: string;
  kind: RoomNoteKind;
  text: string;
  agent: string;
  source: string;
  created_at: number;
  updated_at: number;
  valid_until: number;
  replaced_by: string;
}

function memoryPath(room: string): string {
  return "/v1/rooms/" + encodeURIComponent(room) + "/memory";
}

export async function listRoomMemory(room: string): Promise<RoomNote[]> {
  const page = await request<{ notes?: RoomNote[] }>("GET", memoryPath(room));
  return page && Array.isArray(page.notes) ? page.notes : [];
}

export function addRoomNote(room: string, kind: RoomNoteKind, text: string): Promise<RoomNote> {
  return request<RoomNote>("POST", memoryPath(room), { kind, text });
}

export function updateRoomNote(room: string, id: string, change: { kind?: RoomNoteKind; text?: string }): Promise<RoomNote> {
  return request<RoomNote>("PUT", memoryPath(room) + "/" + encodeURIComponent(id), change);
}

export function deleteRoomNote(room: string, id: string): Promise<{ deleted: boolean }> {
  return request<{ deleted: boolean }>("DELETE", memoryPath(room) + "/" + encodeURIComponent(id));
}

/* What an agent knows: its domain page count and a few page titles, from the
   knowledge pages list (API-DKN-004). */
export interface KnowledgeSummary {
  pages: number;
  topics: string[];
}

export async function knowledgeSummary(agent: string): Promise<KnowledgeSummary> {
  const page = await request<{ pages?: { title?: string }[]; total?: number }>(
    "GET",
    "/v1/knowledge/pages?scope=" + encodeURIComponent(agent) + "&limit=3"
  );
  const titles = (page && Array.isArray(page.pages) ? page.pages : []).map((p) => String(p.title || "")).filter(Boolean);
  return { pages: Number(page && page.total) || 0, topics: titles };
}

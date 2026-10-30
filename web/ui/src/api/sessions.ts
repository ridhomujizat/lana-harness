import { queryString, request } from "./client";

/* Session calls of API-WEB-003, API-WEB-004, and API-WEB-005. Types are
   written by hand from the Go JSON structs. */

export interface Team {
  name: string;
  lead: string;
  members: string[];
}

export interface Session {
  id: string;
  channel: string;
  address: string;
  agent: string;
  title: string;
  status: string;
  preview: string;
  turn_count: number;
  created_at: number;
  updated_at: number;
  in_use: string;
  holder_pid?: number | null;
  team: Team | null;
}

export interface SessionDetail extends Session {
  model: string;
  policy: string;
  workspace: string;
  parent_session_id: string;
  inherited_seq?: number;
  turn_running: boolean;
}

export interface AgentFacet {
  name: string;
  kind: string;
  count: number;
}

export interface SessionFacets {
  channel: Record<string, number>;
  status: Record<string, number>;
  agent: AgentFacet[];
}

export interface ListSessionsResponse {
  sessions: Session[];
  next_before: number | null;
  facets: SessionFacets;
}

export type EventPayload = Record<string, unknown>;

export interface SessionEvent {
  seq: number;
  kind: string;
  turn_id: string;
  created_at: number;
  payload: EventPayload;
  /* Child tags of API-TEAM-011: a child event carries the descendant session
     id and agent. Root events omit both. */
  child_session_id?: string;
  agent?: string;
}

export interface ListEventsResponse {
  events: SessionEvent[];
  last_seq: number;
}

export interface ListSessionsParams {
  channel?: string;
  q?: string;
  status?: string;
  agent?: string;
  limit?: number;
  before?: number;
}

export interface CreateSessionRequest {
  agent: string;
  model?: string;
}

export interface PatchSessionRequest {
  agent?: string;
  model?: string;
  policy?: string;
  confirm_trust?: boolean;
}

export function listSessions(
  params?: ListSessionsParams
): Promise<ListSessionsResponse> {
  return request<ListSessionsResponse>(
    "GET",
    "/v1/sessions" + queryString(params)
  );
}

export function getSession(id: string): Promise<SessionDetail> {
  return request<SessionDetail>(
    "GET",
    "/v1/sessions/" + encodeURIComponent(id)
  );
}

export function listEvents(
  id: string,
  fromSeq?: number
): Promise<ListEventsResponse> {
  return request<ListEventsResponse>(
    "GET",
    "/v1/sessions/" +
      encodeURIComponent(id) +
      "/events" +
      queryString({ from_seq: fromSeq })
  );
}

export function createSession(
  agent: string,
  model?: string
): Promise<SessionDetail> {
  return request<SessionDetail>("POST", "/v1/sessions", {
    agent,
    model: model || "",
  });
}

export function patchSession(
  id: string,
  patch: PatchSessionRequest
): Promise<SessionDetail> {
  return request<SessionDetail>(
    "PATCH",
    "/v1/sessions/" + encodeURIComponent(id),
    patch
  );
}

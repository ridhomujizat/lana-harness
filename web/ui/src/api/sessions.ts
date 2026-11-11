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
  /* Spec 40: "" (no title yet), "auto", "owner", or "system". Older daemons
     omit both title fields. */
  title_source?: string;
  /* True while the daemon is naming the session (REQ-TTL-012). */
  title_pending?: boolean;
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
  reasoning_effort: string;
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

/* API-ATT-004: an image block carries its workspace-relative path and media
   type; the web API drops the base64 data field. */
export interface SessionContentBlock {
  type: string;
  text?: string;
  media_type?: string;
  path?: string;
}

/* API-WF-005: an assistant message delivered out of band in a chat carries its
   source (e.g. "workflow") and the source's name (e.g. the workflow name). A
   normal reply omits both. */
export interface AssistantMessagePayload extends EventPayload {
  content?: SessionContentBlock[];
  source?: string;
  source_name?: string;
  stop_reason?: string;
}

/* API reply: the automation message an owner reply points back to (the seq of
   the assistant/message event, its source and name, and the quoted text). */
export interface ReplyRef {
  seq: number;
  source?: string;
  source_name?: string;
  text: string;
}

/* The user/message payload fields the web surfaces read. reply_to is set when
   the owner sent the message as a reply to an automation message. */
export interface UserMessagePayload extends EventPayload {
  content?: SessionContentBlock[];
  continue?: boolean;
  context_blocks?: number;
  runtime?: boolean;
  reply_to?: ReplyRef;
}

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
  reasoning_effort?: string;
  confirm_trust?: boolean;
  /* Spec 40: a new title; "" goes back to an automatic one. */
  title?: string;
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

/* Spec 40, REQ-TTL-008: renames a session. An empty title goes back to an
   automatic one. */
export function renameSession(id: string, title: string): Promise<SessionDetail> {
  return patchSession(id, { title });
}

/* Spec 40, REQ-TTL-010: names the session again now and answers the session
   with its new title. 409 title_busy while a title is being made. */
export function regenerateSessionTitle(id: string): Promise<SessionDetail> {
  return request<SessionDetail>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/title/regenerate"
  );
}

/* The session's token totals and its context meter: the size of the last
   model call, the context window and the size at which it compacts on its
   own (0 when unknown). */
export interface SessionUsage {
  input_tokens: number;
  output_tokens: number;
  cached_input_tokens: number;
  context_tokens: number;
  context_window: number;
  compact_at: number;
}

export function getSessionUsage(id: string): Promise<SessionUsage> {
  return request<SessionUsage>("GET", "/v1/sessions/" + encodeURIComponent(id) + "/usage");
}

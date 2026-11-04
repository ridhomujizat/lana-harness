import { request } from "./client";
import type { SessionDetail } from "./sessions";

/* Turn calls of API-WEB-007, API-WEB-009, and API-WEB-010. */

/* API-WF-021: a "/<command>" that matches a workflow starts a run instead of a
   turn; the response then carries only `command`. */
export interface WorkflowCommandReply {
  workflow_id: string;
  run_id: string;
  number: number;
  reply: string;
}

export interface SendMessageResponse {
  turn: string;
  turn_id?: string;
  command?: WorkflowCommandReply;
}

export interface CancelTurnResponse {
  turn: string;
}

export interface CompactSessionResponse {
  tokens_before: number;
  tokens_after: number;
}

/* API-ATT-002: attachments are the workspace-relative paths returned by
   uploadAttachments. The attachments key is sent only when there are paths, so
   a plain text send keeps its request body unchanged. */
export function sendMessage(
  id: string,
  text: string,
  attachments?: string[]
): Promise<SendMessageResponse> {
  const body =
    attachments && attachments.length > 0 ? { text, attachments } : { text };
  return request<SendMessageResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/messages",
    body
  );
}

export function cancelTurn(id: string): Promise<CancelTurnResponse> {
  return request<CancelTurnResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/cancel"
  );
}

/* API-TFB-003: resume the session's last turn. The answer has the same shape
   as sendMessage. A 409 {turn_running | nothing_to_continue} says it cannot. */
export function continueSession(id: string): Promise<SendMessageResponse> {
  return request<SendMessageResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/continue"
  );
}

export function compactSession(id: string): Promise<CompactSessionResponse> {
  return request<CompactSessionResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/compact"
  );
}

export function forkSession(id: string): Promise<SessionDetail> {
  return request<SessionDetail>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/fork"
  );
}

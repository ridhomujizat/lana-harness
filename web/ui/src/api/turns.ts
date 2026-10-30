import { request } from "./client";
import type { SessionDetail } from "./sessions";

/* Turn calls of API-WEB-007, API-WEB-009, and API-WEB-010. */

export interface SendMessageResponse {
  turn: string;
  turn_id?: string;
}

export interface CancelTurnResponse {
  turn: string;
}

export interface CompactSessionResponse {
  tokens_before: number;
  tokens_after: number;
}

export function sendMessage(
  id: string,
  text: string
): Promise<SendMessageResponse> {
  return request<SendMessageResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/messages",
    { text }
  );
}

export function cancelTurn(id: string): Promise<CancelTurnResponse> {
  return request<CancelTurnResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/cancel"
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

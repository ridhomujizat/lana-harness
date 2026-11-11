import { request } from "./client";

/* Secure secret input of spec 53: the secret_request SSE event (API-SEC-003)
   and the answer route POST /v1/secret-requests/{request_id} (API-SEC-004).
   The value is posted once and never stored or logged by the dashboard. */

export interface SecretRequestEvent {
  request_id: string;
  name: string;
  description: string;
  link?: string;
  /* True when the name already has a value: saving replaces it. */
  replaces: boolean;
  expires_at: number;
  call_id?: string;
  turn_id?: string;
}

export interface SecretRequestAnswerResponse {
  outcome: string;
  name: string;
}

function secretRequestPath(requestId: string): string {
  return "/v1/secret-requests/" + encodeURIComponent(requestId);
}

export function saveSecretRequest(
  requestId: string,
  value: string
): Promise<SecretRequestAnswerResponse> {
  return request<SecretRequestAnswerResponse>("POST", secretRequestPath(requestId), {
    value,
  });
}

export function cancelSecretRequest(
  requestId: string
): Promise<SecretRequestAnswerResponse> {
  return request<SecretRequestAnswerResponse>("POST", secretRequestPath(requestId), {
    cancel: true,
  });
}

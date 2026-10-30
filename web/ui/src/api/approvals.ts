import { request } from "./client";

/* Approval calls of API-WEB-011 and the approval SSE event of API-WEB-008. */

export interface ApprovalEvent {
  request_id: string;
  tool: string;
  argument: string;
  risk: string;
  expires_at: number;
  child_session_id?: string;
  agent?: string;
  label?: string;
}

export interface ApprovalAnswerRequest {
  outcome: string;
  reason?: string;
}

export interface ApprovalAnswerResponse {
  outcome: string;
}

export function answerApproval(
  requestId: string,
  outcome: string,
  reason?: string
): Promise<ApprovalAnswerResponse> {
  const body: ApprovalAnswerRequest = { outcome };
  if (reason) {
    body.reason = reason;
  }
  return request<ApprovalAnswerResponse>(
    "POST",
    "/v1/approvals/" + encodeURIComponent(requestId),
    body
  );
}

import { request } from "./client";

/* Approval calls of API-WEB-011 and the approval SSE event of API-WEB-008. */

/* Spec 63 API-HLF-001/API-HLF-004: the hardline match that caused an approval.
   tier is "block" or "escalate"; step_up is set for a block request and empty
   for an escalate request. */
export interface HardlineMatch {
  rule: string;
  tier: "block" | "escalate";
  reason: string;
  step_up?: "password" | "password_or_telegram" | "";
}

export interface ApprovalEvent {
  request_id: string;
  tool: string;
  argument: string;
  risk: string;
  expires_at: number;
  child_session_id?: string;
  agent?: string;
  label?: string;
  hardline?: HardlineMatch;
}

/* Spec 63 API-HLF-005: the step-up proof of a block-tier allow. Exactly one of
   password or code is sent. */
export interface ApprovalProof {
  password?: string;
  code?: string;
}

export interface ApprovalAnswerRequest {
  outcome: string;
  reason?: string;
  password?: string;
  code?: string;
}

export interface ApprovalAnswerResponse {
  outcome: string;
}

/* The result the card uses to show a step-up error inline (API-HLF-005). A
   surface that knows nothing about hardline may keep returning nothing. */
export interface ApprovalAnswerResult {
  ok: boolean;
  code?: string;
  message?: string;
}

export function answerApproval(
  requestId: string,
  outcome: string,
  reason?: string,
  proof?: ApprovalProof
): Promise<ApprovalAnswerResponse> {
  const body: ApprovalAnswerRequest = { outcome };
  if (reason) {
    body.reason = reason;
  }
  if (proof && proof.password) {
    body.password = proof.password;
  }
  if (proof && proof.code) {
    body.code = proof.code;
  }
  return request<ApprovalAnswerResponse>(
    "POST",
    "/v1/approvals/" + encodeURIComponent(requestId),
    body
  );
}

/* Spec 63 API-HLF-006: send a 6-digit Telegram code for a block request. */
export interface ApprovalCodeResponse {
  sent_to: number;
}

export function sendApprovalCode(
  requestId: string
): Promise<ApprovalCodeResponse> {
  return request<ApprovalCodeResponse>(
    "POST",
    "/v1/approvals/" + encodeURIComponent(requestId) + "/code"
  );
}

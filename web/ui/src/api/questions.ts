import { request } from "./client";

/* Question answer calls of API-ASK-005 and the question SSE event of
   API-ASK-004. */

export interface QuestionOption {
  label: string;
  description?: string;
}

export interface QuestionPrompt {
  question: string;
  header: string;
  multi_select: boolean;
  options: QuestionOption[];
}

export interface QuestionEvent {
  request_id: string;
  questions: QuestionPrompt[];
  expires_at: number;
  /* Optional on the wire: the SSE event carries request_id, questions, and
     expires_at; the session log adds call_id (and a turn id). */
  call_id?: string;
  turn_id?: string;
}

/* The request omits keys it does not set (the Go struct reads the zero value),
   so selected and skipped are optional here. */
export interface QuestionAnswerEntry {
  question: number;
  selected?: number[];
  other?: string | null;
  skipped?: boolean;
}

export interface QuestionAnswerRequest {
  answers?: QuestionAnswerEntry[];
  skip_all?: boolean;
}

export interface QuestionAnswerResponse {
  outcome: string;
}

function questionPath(requestId: string): string {
  return "/v1/questions/" + encodeURIComponent(requestId);
}

export function answerQuestion(
  requestId: string,
  body: QuestionAnswerRequest
): Promise<QuestionAnswerResponse> {
  return request<QuestionAnswerResponse>("POST", questionPath(requestId), body);
}

export function skipAllQuestions(
  requestId: string
): Promise<QuestionAnswerResponse> {
  return request<QuestionAnswerResponse>("POST", questionPath(requestId), {
    skip_all: true,
  });
}

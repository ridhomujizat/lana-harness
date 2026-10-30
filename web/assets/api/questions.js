/* Question answer calls (REQ-ASK-011, API-ASK-005). Thin wrappers over
   request() so the views never build paths by hand. */

import { request } from "/assets/api.js";

function questionPath(requestId) {
  return "/v1/questions/" + encodeURIComponent(requestId);
}

export function answerQuestion(requestId, body) {
  return request("POST", questionPath(requestId), body);
}

export function skipAllQuestions(requestId) {
  return request("POST", questionPath(requestId), { skip_all: true });
}

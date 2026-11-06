import { describe, expect, it } from "vitest";
import { normalizeQuestions, openQuestionFromEvents } from "./question";
import type { SessionEvent, EventPayload } from "../api/sessions";

function event(
  seq: number,
  kind: string,
  turnId: string,
  payload: EventPayload
): SessionEvent {
  return {
    seq,
    kind,
    turn_id: turnId,
    created_at: seq,
    payload,
  };
}

const ask = event(1, "tool/call", "t1", {
  call_id: "c1",
  name: "ask_user",
  input: {
    questions: [
      {
        question: "Which files?",
        header: "Files",
        multi_select: false,
        options: [{ label: "one" }, { label: "two", description: "second" }],
      },
    ],
  },
});

const asked = event(2, "question/asked", "t1", {
  request_id: "q1",
  call_id: "c1",
  expires_at: 99,
});

describe("normalizeQuestions", () => {
  it("accepts the wire shape and the camelCase shape", () => {
    const questions = normalizeQuestions([
      {
        question: "Pick",
        header: "Header",
        multi_select: true,
        options: [{ label: "a", description: "b" }],
      },
      { text: "Other", multiSelect: true, options: [] },
    ]);
    expect(questions[0]?.text).toBe("Pick");
    expect(questions[0]?.multiSelect).toBe(true);
    expect(questions[0]?.options[0]?.description).toBe("b");
    expect(questions[1]?.text).toBe("Other");
  });
});

describe("openQuestionFromEvents", () => {
  it("returns the newest unanswered question with its tool input", () => {
    const open = openQuestionFromEvents([ask, asked], {});
    expect(open?.request_id).toBe("q1");
    expect(open?.call_id).toBe("c1");
    expect(open?.questions[0]?.question).toBe("Which files?");
    expect(open?.questions[0]?.options).toHaveLength(2);
  });

  it("drops a question once its answered event exists", () => {
    const answered = event(3, "question/answered", "t1", {
      request_id: "q1",
      call_id: "c1",
      outcome: "answered",
    });
    expect(openQuestionFromEvents([ask, asked, answered], {})).toBeNull();
  });

  it("reads child event maps too", () => {
    const open = openQuestionFromEvents([], { child: [ask, asked] });
    expect(open?.request_id).toBe("q1");
  });
});

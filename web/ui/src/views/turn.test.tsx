import { describe, expect, it } from "vitest";
import { buildTurns, turnSummary } from "./turn";
import type { SessionEvent, EventPayload } from "../api/sessions";

function event(
  seq: number,
  kind: string,
  turnId: string,
  payload: EventPayload,
  createdAt = seq
): SessionEvent {
  return {
    seq,
    kind,
    turn_id: turnId,
    created_at: createdAt,
    payload,
  };
}

describe("buildTurns", () => {
  it("builds user bubbles and one turn with an activity group", () => {
    const items = buildTurns(
      [
        event(1, "user/message", "t1", {
          content: [{ type: "text", text: "hello" }],
        }),
        event(2, "assistant/message", "t1", {
          content: [{ type: "text", text: "working" }],
        }),
        event(3, "tool/call", "t1", {
          call_id: "c1",
          name: "read_file",
          input: { path: "a.txt" },
        }),
        event(4, "tool/result", "t1", {
          call_id: "c1",
          status: "ok",
          output: "done",
        }),
        event(5, "turn/end", "t1", { reason: "end_turn" }),
      ],
      {}
    );

    expect(items).toHaveLength(2);
    expect(items[0]).toEqual({ kind: "user", key: "user-1", text: "hello" });
    const turnItem = items[1];
    expect(turnItem?.kind).toBe("turn");
    if (!turnItem || turnItem.kind !== "turn") {
      return;
    }
    expect(turnItem.turn.groups.map((group) => group.kind)).toEqual([
      "text",
      "activity",
    ]);
    expect(turnItem.turn.steps[0]?.status).toBe("ok");
    expect(turnItem.turn.running).toBe(false);
  });

  it("marks the live turn running and appends it when it has no event yet", () => {
    const items = buildTurns([], { running: true, turnId: "live", startedAt: 5 });
    expect(items).toHaveLength(1);
    const turnItem = items[0];
    if (!turnItem || turnItem.kind !== "turn") {
      throw new Error("expected a turn item");
    }
    expect(turnItem.turn.id).toBe("live");
    expect(turnItem.turn.running).toBe(true);
  });
});

describe("turnSummary", () => {
  it("uses the -ing verb while a step is unfinished", () => {
    const summary = turnSummary([
      {
        key: "c1",
        callId: "c1",
        name: "read_file",
        input: null,
        status: "ok",
        output: "x",
        approval: null,
      },
      {
        key: "c2",
        callId: "c2",
        name: "bash",
        input: null,
        status: "running",
        output: "",
        approval: null,
      },
    ]);
    expect(summary).toBe("Read 1 file · Running 1 command");
  });
});

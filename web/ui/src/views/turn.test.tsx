import { describe, expect, it } from "vitest";
import { buildTurns, turnEndCard, turnSummary } from "./turn";
import type { TurnStep } from "./turn";
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
    expect(items[0]).toEqual({
      kind: "user",
      key: "user-1",
      text: "hello",
      continue: false,
    });
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

function step(name: string, stepNumber: number): TurnStep {
  return {
    key: "c-" + name,
    callId: "c-" + name,
    name,
    input: null,
    status: "error",
    output: "boom",
    approval: null,
    step: stepNumber,
  };
}

describe("turnEndCard", () => {
  /* REQ-TFB-004: the plain reason line for each abnormal end. */
  it("renders the plain reason for cancelled and interrupted", () => {
    expect(turnEndCard({ stopReason: "cancelled" })?.text).toBe(
      "Stopped by you."
    );
    expect(turnEndCard({ stopReason: "interrupted" })?.text).toBe(
      "lana restarted while working."
    );
  });

  it("renders the error message for an error end", () => {
    expect(
      turnEndCard({ stopReason: "error", error: { message: "provider down" } })
        ?.text
    ).toBe("Stopped by an error: provider down");
  });

  it("renders the step limit for a max_steps end", () => {
    expect(
      turnEndCard({ stopReason: "max_steps", stepCount: 25 })?.text
    ).toBe("Stopped after the step limit (25 steps).");
  });

  it("adds the last step and its number when the turn ran a tool", () => {
    const card = turnEndCard({
      stopReason: "interrupted",
      steps: [step("dbx_list_tables", 4)],
    });
    expect(card?.text).toBe("lana restarted while working.");
    expect(card?.lastStep).toBe("Last step: dbx_list_tables (step 4)");
  });

  /* AC-TFB-009: a tool failure inside a normal end is the agent's story. */
  it("returns no card for a normal end even with a failed tool", () => {
    const card = turnEndCard({
      stopReason: "end_turn",
      steps: [step("dbx_list_tables", 1)],
    });
    expect(card).toBeNull();
  });
});

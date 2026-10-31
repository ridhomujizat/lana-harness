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

  /* REQ-LTC-001: the dashboard hides the injected <context> block. */
  it("hides the injected context block using context_blocks", () => {
    const items = buildTurns([
      event(1, "user/message", "t1", {
        context_blocks: 1,
        content: [
          { type: "text", text: "<context>\ndate: now\n</context>" },
          { type: "text", text: "Cihuyy" },
        ],
      }),
    ]);
    const user = items[0];
    if (!user || user.kind !== "user") {
      throw new Error("expected a user item");
    }
    expect(user.text).toBe("Cihuyy");
    expect(user.text).not.toContain("<context>");
  });

  it("hides the injected context block without context_blocks", () => {
    const items = buildTurns([
      event(1, "user/message", "t1", {
        content: [
          { type: "text", text: "<context>\ndate: now\n</context>" },
          { type: "text", text: "Cihuyy" },
        ],
      }),
    ]);
    const user = items[0];
    if (!user || user.kind !== "user") {
      throw new Error("expected a user item");
    }
    expect(user.text).toBe("Cihuyy");
    expect(user.text).not.toContain("<context>");
  });

  /* REQ-MEM-021: a runtime note makes no owner bubble. */
  it("hides a runtime user message", () => {
    const items = buildTurns([
      event(1, "user/message", "t1", {
        runtime: true,
        content: [{ type: "text", text: "runtime nudge" }],
      }),
      event(2, "user/message", "t1", {
        content: [{ type: "text", text: "hello" }],
      }),
    ]);
    const users = items.filter((item) => item.kind === "user");
    expect(users).toHaveLength(1);
    expect(users[0]).toMatchObject({ kind: "user", text: "hello" });
  });

  /* REQ-MEM-030: memory/captured becomes one muted note per operation. */
  it("adds a memory note per captured operation", () => {
    const items = buildTurns([
      event(1, "user/message", "t1", {
        content: [{ type: "text", text: "call me boss" }],
      }),
      event(2, "assistant/message", "t1", {
        content: [{ type: "text", text: "ok" }],
      }),
      event(3, "memory/captured", "t1", {
        ops: [
          { target: "user", action: "add", content: 'Called "boss"' },
          { target: "user", action: "replace", content: "New", old_text: "Old" },
          { target: "memory", action: "remove", old_text: "stale note" },
        ],
      }),
    ]);
    const turnItem = items.find((item) => item.kind === "turn");
    if (!turnItem || turnItem.kind !== "turn") {
      throw new Error("expected a turn item");
    }
    expect(turnItem.turn.notes).toEqual([
      'Saved to memory: Called "boss"',
      "Saved to memory: New",
      "Removed from memory: stale note",
    ]);
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

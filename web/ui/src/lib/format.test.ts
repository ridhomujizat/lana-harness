import { describe, expect, it } from "vitest";
import { buildHistory, memoryOpLine } from "./format";
import type { SessionEvent, EventPayload } from "../api/sessions";

function event(
  seq: number,
  kind: string,
  payload: EventPayload,
  createdAt = seq
): SessionEvent {
  return {
    seq,
    kind,
    turn_id: "t1",
    created_at: createdAt,
    payload,
  };
}

describe("buildHistory", () => {
  /* REQ-MEM-021: a runtime note is not an owner message. */
  it("hides a runtime user message", () => {
    const items = buildHistory([
      event(1, "user/message", {
        runtime: true,
        content: [{ type: "text", text: "runtime nudge" }],
      }),
      event(2, "user/message", {
        content: [{ type: "text", text: "hello" }],
      }),
    ]);
    const users = items.filter((item) => item.type === "user");
    expect(users).toHaveLength(1);
    expect(users[0]?.text).toBe("hello");
  });

  /* REQ-MEM-030: one decision line per captured operation. */
  it("renders a decision line per memory operation", () => {
    const items = buildHistory([
      event(1, "memory/captured", {
        ops: [
          { target: "user", action: "add", content: 'Called "boss"' },
          { target: "user", action: "replace", content: "New", old_text: "Old" },
          { target: "memory", action: "remove", old_text: "stale note" },
        ],
      }),
    ]);
    expect(items.map((item) => ({ type: item.type, text: item.text }))).toEqual([
      { type: "decision", text: 'Saved to memory: Called "boss"' },
      { type: "decision", text: "Saved to memory: New" },
      { type: "decision", text: "Removed from memory: stale note" },
    ]);
  });

  it("ignores a memory operation it does not understand", () => {
    const items = buildHistory([
      event(1, "memory/captured", {
        ops: [{ target: "user", action: "prune", content: "x" }],
      }),
    ]);
    expect(items).toHaveLength(0);
  });
});

describe("memoryOpLine", () => {
  it("names the saved content for add and replace", () => {
    expect(memoryOpLine({ action: "add", content: "Likes tea" })).toBe(
      "Saved to memory: Likes tea"
    );
    expect(
      memoryOpLine({ action: "replace", content: "New", old_text: "Old" })
    ).toBe("Saved to memory: New");
  });

  it("names the dropped entry for remove", () => {
    expect(memoryOpLine({ action: "remove", old_text: "stale note" })).toBe(
      "Removed from memory: stale note"
    );
  });
});

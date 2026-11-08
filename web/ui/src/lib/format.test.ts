import { describe, expect, it } from "vitest";
import {
  buildHistory,
  contentText,
  knowledgeCompiledLine,
  memoryOpLine,
} from "./format";
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

  /* REQ-KNW-060: a knowledge compile pass becomes one decision line. */
  it("renders a decision line for a knowledge compile pass", () => {
    const items = buildHistory([
      event(1, "knowledge/compiled", {
        v: 1,
        source: "sources/2026-11-01 Rapat Q4.pdf",
        ops: [
          { op: "create", title: "Rapat Q4" },
          { op: "append", title: "Proyek X" },
        ],
        usage: { input_tokens: 12000, cached_input_tokens: 0, output_tokens: 900 },
        error: "",
      }),
    ]);
    expect(items.map((item) => ({ type: item.type, text: item.text }))).toEqual([
      {
        type: "decision",
        text: 'Knowledge updated from "2026-11-01 Rapat Q4.pdf": created 1, updated 1',
      },
    ]);
  });
});

describe("buildHistory runtime meta", () => {
  /* API-CCR-002: a Claude Code session meta is not an empty system item. */
  it("skips a system message of subtype runtime", () => {
    const items = buildHistory([
      event(1, "system/message", {
        subtype: "runtime",
        runtime: "claude-code",
        claude_session_id: "6f1c0d2e-0000-4000-8000-000000000000",
        model: "claude-opus-5",
        version: "2.1.319",
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

describe("knowledgeCompiledLine", () => {
  /* REQ-KNW-060: created counts create; updated counts append, replace, meta. */
  it("counts created and updated operations", () => {
    expect(
      knowledgeCompiledLine({
        source: "sources/2026-11-01 Rapat Q4.pdf",
        ops: [
          { op: "create", title: "Rapat Q4" },
          { op: "append", title: "Proyek X" },
        ],
      })
    ).toBe('Knowledge updated from "2026-11-01 Rapat Q4.pdf": created 1, updated 1');
  });

  it("names the failure with the source file name and the error", () => {
    expect(
      knowledgeCompiledLine({
        source: "sources/2026-11-01 Rapat Q4.pdf",
        ops: [{ op: "create", title: "Rapat Q4" }],
        error: "invalid JSON",
      })
    ).toBe('Knowledge compile failed for "2026-11-01 Rapat Q4.pdf": invalid JSON');
  });

  it("omits an empty group and takes the name from the last segment", () => {
    expect(
      knowledgeCompiledLine({
        source: "notes/topics/Proyek X.md",
        ops: [
          { op: "append", title: "Proyek X" },
          { op: "meta", title: "Proyek X" },
        ],
      })
    ).toBe('Knowledge updated from "Proyek X.md": updated 2');
  });

  it("returns an empty line with no operations and no error", () => {
    expect(knowledgeCompiledLine({ source: "a/b.md", ops: [] })).toBe("");
    expect(knowledgeCompiledLine({ source: "a/b.md" })).toBe("");
  });
});


/* REQ-MDL-006: the answer text ignores non-text blocks, including the thinking
   blocks the turn view keeps separate. */
describe("contentText", () => {
  it("joins text blocks and ignores non-text blocks", () => {
    expect(
      contentText([
        { type: "thinking", text: "reasoning the model hid" },
        { type: "text", text: "first" },
        { type: "tool_use", id: "c1", name: "bash", input: {} },
        { type: "text", text: "second" },
      ])
    ).toBe("first\nsecond");
  });

  it("returns an empty string for a non-array or a message without text", () => {
    expect(contentText(null)).toBe("");
    expect(contentText([{ type: "thinking", text: "hidden" }])).toBe("");
    expect(contentText([{ type: "text", text: "" }])).toBe("");
  });
});

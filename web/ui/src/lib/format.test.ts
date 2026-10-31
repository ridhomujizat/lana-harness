import { describe, expect, it } from "vitest";
import { contentText } from "./format";

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

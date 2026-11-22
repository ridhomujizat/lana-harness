import { describe, expect, it } from "vitest";
import { mergeItems, type ChatItem } from "../api/workbench-chat";
import { toBlocks } from "./workbench-chat";
import { modelLabel } from "./workbench-chat-composer";

const u = (id: string, text: string): ChatItem => ({ id, kind: "user", text });
const a = (id: string, text: string): ChatItem => ({ id, kind: "assistant", text, model: "claude-opus-5-5" });
const t = (id: string, name: string, status: ChatItem["status"] = "done"): ChatItem => ({ id, kind: "tool", name, status, summary: { verb: name, target: "x" } });

describe("mergeItems", () => {
  it("replaces on reset and upserts by id otherwise", () => {
    const first = mergeItems([], { available: true, reset: true, items: [u("1", "hi"), t("2", "Bash", "running")] });
    expect(first.map((i) => i.id)).toEqual(["1", "2"]);
    const next = mergeItems(first, { available: true, items: [t("2", "Bash", "done"), a("3", "ok")] });
    expect(next.map((i) => i.id)).toEqual(["1", "2", "3"]);
    expect(next[1]!.status).toBe("done");
    expect(mergeItems(next, { available: true, reset: true, items: [] })).toEqual([]);
  });
});

describe("toBlocks", () => {
  it("adds one head per turn and groups consecutive tools", () => {
    const blocks = toBlocks([
      u("1", "do it"),
      a("2", "sure"),
      t("3", "Read"),
      t("4", "Edit"),
      a("5", "done"),
      { id: "6", kind: "turn_end", duration_ms: 65000, output_tokens: 1200 },
      u("7", "next"),
      t("8", "Bash"),
    ]);
    expect(blocks.map((b) => b.type)).toEqual(["user", "head", "text", "tools", "text", "turn_end", "user", "head", "tools"]);
    const tools = blocks[3] as { items: ChatItem[] };
    expect(tools.items.map((i) => i.id)).toEqual(["3", "4"]);
    const end = blocks[5] as { text: string };
    expect(end.text).toBe("1m 5s · 1.2k tokens");
  });
});

describe("modelLabel", () => {
  it("shortens Claude model ids", () => {
    expect(modelLabel("claude-opus-5-5")).toBe("Opus 5.5");
    expect(modelLabel("claude-sonnet-5-5[1m]")).toBe("Sonnet 5.5");
    expect(modelLabel("")).toBe("Model");
  });
});

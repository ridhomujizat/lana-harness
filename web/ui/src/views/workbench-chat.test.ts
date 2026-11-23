import { describe, expect, it } from "vitest";
import { mergeItems, type ChatItem } from "../api/workbench-chat";
import { answerText, dedupeTools, planTextFor, toBlocks } from "./workbench-chat";
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
    expect(modelLabel("claude-sonnet-5-5[1m]")).toBe("Sonnet 5.5 1M");
    expect(modelLabel("")).toBe("Model");
  });
});

describe("answerText", () => {
  it("pulls the answers out of the tool result", () => {
    expect(answerText('User has answered your questions: "What is your favorite color?"="Blue". You can now continue.')).toBe("Blue");
    expect(answerText("plain")).toBe("plain");
  });
});

describe("dedupeTools", () => {
  it("merges a repeated tool_use", () => {
    const out = dedupeTools([
      { id: "a:0", kind: "plan", tool_use_id: "t1", plan: "# Plan", state: "pending" },
      { id: "b:0", kind: "plan", tool_use_id: "t1", plan: "", state: "rejected" },
      { id: "c:0", kind: "tool", tool_use_id: "t2", name: "Bash", input: { command: "ls" }, status: "running" },
      { id: "d:0", kind: "tool", tool_use_id: "t2", name: "Bash", input: {}, status: "done" },
    ]);
    expect(out.map((i) => i.id)).toEqual(["a:0", "c:0"]);
    expect(out[0]!.plan).toBe("# Plan");
    expect(out[0]!.state).toBe("rejected");
    expect(out[1]!.input).toEqual({ command: "ls" });
    expect(out[1]!.status).toBe("done");
  });
});

describe("planTextFor", () => {
  it("prefers the plan file written just before ExitPlanMode", () => {
    const items: ChatItem[] = [
      { id: "p0", kind: "plan", tool_use_id: "a", plan: "# old" },
      { id: "w1", kind: "tool", name: "Write", input: { file_path: "/x/plans/cozy.md", content: "# new plan" } },
      { id: "p1", kind: "plan", tool_use_id: "b", plan: "# old" },
    ];
    expect(planTextFor(items, items[2]!)).toBe("# new plan");
    expect(planTextFor(items, items[0]!)).toBe("");
  });
});

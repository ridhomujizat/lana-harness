import { describe, expect, it } from "vitest";
import type { SessionEvent } from "../api/sessions";
import { buildTrajectory, recordKindLabel } from "./trajectory-model";
import type { TrajRecord, Trajectory } from "./trajectory-model";

const BASE = 1_700_000_000_000;

function event(
  seq: number,
  kind: string,
  turnId: string,
  payload: Record<string, unknown>,
  offset = 0
): SessionEvent {
  return {
    seq,
    kind,
    turn_id: turnId,
    created_at: BASE + offset,
    payload,
  };
}

/* One representative session: a system prompt, injected context, an owner
   message that changes the prompt sections, two requests, tool calls with one
   error, an approval, a retry, and the between-turn records. */
function fixture(): SessionEvent[] {
  return [
    event(1, "session/created", "", { agent: "lana" }),
    event(
      2,
      "system/message",
      "",
      {
        text: "System v1",
        sections: [
          { name: "Role", text: "v1" },
          { name: "Tools", text: "same" },
        ],
      },
      100
    ),
    event(3, "turn/start", "t1", { turn_id: "t1" }, 1000),
    event(
      4,
      "user/message",
      "t1",
      {
        content: [
          { type: "text", text: "<context>cwd</context>" },
          { type: "text", text: "Check the disk" },
        ],
        context_blocks: 1,
        prompt_sections: [
          { name: "Role", text: "v2" },
          { name: "Tools", text: "same" },
        ],
      },
      1100
    ),
    event(5, "step/start", "t1", { step: 1, provider: "deepseek", model: "chat" }, 2000),
    event(
      6,
      "assistant/attempt",
      "t1",
      {
        step: 1,
        attempt: 1,
        provider: "deepseek",
        model: "chat",
        error: { class: "overloaded", status: 503, message: "busy" },
      },
      2100
    ),
    event(
      7,
      "assistant/message",
      "t1",
      {
        content: [
          { type: "thinking", text: "Think." },
          { type: "text", text: "Checking." },
        ],
        stop_reason: "tool_use",
      },
      5000
    ),
    event(
      8,
      "tool/call",
      "t1",
      { call_id: "c1", name: "read_file", input: { path: "/tmp/a" } },
      6000
    ),
    event(
      9,
      "approval/asked",
      "t1",
      { request_id: "a1", call_id: "c1", tool: "read_file", risk: "read" },
      6500
    ),
    event(
      10,
      "approval/decided",
      "t1",
      { request_id: "a1", call_id: "c1", outcome: "allowed_once" },
      7000
    ),
    event(
      11,
      "tool/result",
      "t1",
      { call_id: "c1", status: "success", output: "contents" },
      9000
    ),
    event(
      12,
      "tool/call",
      "t1",
      { call_id: "c2", name: "bash", input: { command: "df -h" } },
      9500
    ),
    event(
      13,
      "tool/result",
      "t1",
      { call_id: "c2", status: "error", output: "boom" },
      10000
    ),
    event(
      14,
      "step/end",
      "t1",
      {
        step: 1,
        provider: "deepseek",
        model: "chat",
        usage: {
          input_tokens: 1200,
          cached_input_tokens: 800,
          output_tokens: 300,
        },
      },
      11000
    ),
    event(15, "step/start", "t1", { step: 2, provider: "deepseek", model: "chat" }, 12000),
    event(
      16,
      "assistant/message",
      "t1",
      { content: [{ type: "text", text: "Done." }], stop_reason: "end_turn" },
      14000
    ),
    event(
      17,
      "step/end",
      "t1",
      {
        step: 2,
        provider: "deepseek",
        model: "chat",
        usage: {
          input_tokens: 1500,
          cached_input_tokens: 1000,
          output_tokens: 120,
        },
      },
      15000
    ),
    event(18, "turn/end", "t1", { reason: "end_turn" }, 16000),
    event(
      19,
      "compaction/summary",
      "",
      {
        summary: "short",
        tokens_before: 5000,
        tokens_after: 2000,
        model: "chat",
        usage: { input_tokens: 200, output_tokens: 50 },
      },
      17000
    ),
    event(
      20,
      "tool_result_pruned",
      "",
      { call_id: "c1", source_seq: 11, output: "[pruned]" },
      18000
    ),
    event(
      21,
      "memory/captured",
      "",
      {
        ops: [{ target: "memory", action: "add", content: "note" }],
        usage: { input_tokens: 100, output_tokens: 10 },
      },
      19000
    ),
    event(
      22,
      "model/selection",
      "",
      { provider: "deepseek", model: "chat", reasoning_effort: "high" },
      20000
    ),
    event(23, "approval/policy", "", { policy: "trust" }, 21000),
    event(24, "custom/event", "", { value: 1 }, 22000),
  ];
}

function allRecords(trajectory: Trajectory): TrajRecord[] {
  const records: TrajRecord[] = [];
  const add = (record: TrajRecord) => {
    records.push(record);
    for (const child of record.children || []) {
      add(child);
    }
  };
  for (const section of trajectory.sections) {
    if (section.type === "between") {
      for (const record of section.records) {
        add(record);
      }
      continue;
    }
    for (const record of section.turn.loose) {
      add(record);
    }
    for (const step of section.turn.steps) {
      for (const record of step.records) {
        add(record);
      }
    }
  }
  return records;
}

function findRecord(
  trajectory: Trajectory,
  predicate: (record: TrajRecord) => boolean
): TrajRecord | undefined {
  return allRecords(trajectory).find(predicate);
}

describe("buildTrajectory grouping", () => {
  it("groups events by turn and step and keeps outside events between turns", () => {
    const trajectory = buildTrajectory(fixture(), {});
    expect(trajectory.totals.turns).toBe(1);
    expect(trajectory.sections.map((section) => section.type)).toEqual([
      "between",
      "turn",
      "between",
    ]);
    const turn = trajectory.sections[1];
    if (!turn || turn.type !== "turn") {
      throw new Error("expected a turn section");
    }
    expect(turn.turn.no).toBe(1);
    expect(turn.turn.reason).toBe("end_turn");
    expect(turn.turn.steps.map((step) => step.no)).toEqual([1, 2]);
    expect(turn.turn.steps[0]?.provider).toBe("deepseek");
    expect(turn.turn.steps[0]?.model).toBe("chat");
  });

  it("builds each requirement kind and nests tools under their request", () => {
    const trajectory = buildTrajectory(fixture(), {});
    const records = allRecords(trajectory);
    const labels = records.map((record) => recordKindLabel(record));
    for (const label of [
      "SYSTEM",
      "CONTEXT",
      "USER",
      "ASSISTANT",
      "RETRY",
      "TOOL",
      "APPROVAL",
      "COMPACTED",
      "PRUNED",
      "MEMORY",
      "SETTING",
    ]) {
      expect(labels).toContain(label);
    }
    expect(labels).toContain("custom/event");

    const firstAssistant = findRecord(
      trajectory,
      (record) => record.kind === "assistant" && record.requestNo === 1
    );
    expect(firstAssistant?.title).toBe("Checking.");
    expect(firstAssistant?.children?.map((record) => record.kind)).toEqual([
      "tool",
      "tool",
    ]);
    expect(firstAssistant?.toolCalls).toBe(2);
    expect(firstAssistant?.thinkChars).toBe(6);
    expect(firstAssistant?.startMs).toBe(BASE + 2000);
    expect(firstAssistant?.endMs).toBe(BASE + 5000);

    const failedTool = firstAssistant?.children?.find(
      (record) => record.callId === "c2"
    );
    expect(failedTool?.status).toBe("error");
    expect(failedTool?.endMs).toBe(BASE + 10000);
  });

  it("assigns request numbers and cumulative usage in seq order", () => {
    const trajectory = buildTrajectory(fixture(), {});
    const assistants = allRecords(trajectory).filter(
      (record) => record.kind === "assistant"
    );
    expect(assistants.map((record) => record.requestNo)).toEqual([1, 2]);
    expect(assistants[0]?.usage).toEqual({
      input: 1200,
      cached: 800,
      output: 300,
    });
    expect(assistants[0]?.cumulative).toEqual({
      input: 1200,
      cached: 800,
      output: 300,
    });
    expect(assistants[1]?.cumulative).toEqual({
      input: 2700,
      cached: 1800,
      output: 420,
    });
    expect(trajectory.totals.usage).toEqual({
      input: 2700,
      cached: 1800,
      output: 420,
    });
    expect(trajectory.totals.requests).toBe(2);
    expect(trajectory.totals.toolCalls).toBe(2);
  });

  it("records approval wait and compaction, pruning, and memory values", () => {
    const trajectory = buildTrajectory(fixture(), {});
    const approval = findRecord(
      trajectory,
      (record) => record.kind === "approval"
    );
    expect(approval?.status).toBe("success");
    expect(approval?.waitMs).toBe(500);
    expect(approval?.outcome).toBe("allowed_once");

    const compacted = findRecord(
      trajectory,
      (record) => record.kind === "compacted"
    );
    expect(compacted?.title).toBe("5000 → 2000 tokens");
    expect(compacted?.usage).toEqual({ input: 200, cached: 0, output: 50 });
    expect(compacted?.cumulative).toEqual({
      input: 2900,
      cached: 1800,
      output: 470,
    });

    const pruned = findRecord(
      trajectory,
      (record) => record.kind === "pruned"
    );
    expect(pruned?.sourceSeq).toBe(11);

    const memory = findRecord(
      trajectory,
      (record) => record.kind === "memory"
    );
    expect(memory?.title).toBe("1 operation");
    expect(memory?.cumulative).toEqual({
      input: 3000,
      cached: 1800,
      output: 480,
    });
  });

  it("names system prompt changes and prompt-section updates", () => {
    const trajectory = buildTrajectory(fixture(), {});
    const system = findRecord(
      trajectory,
      (record) => record.kind === "system"
    );
    expect(system?.title).toBe("System prompt · Role, Tools");
    const sections = findRecord(
      trajectory,
      (record) => record.kind === "context" && (record.sections?.length || 0) > 0
    );
    expect(sections?.title).toBe("Prompt sections · Role");
  });

  it("shows a runtime note as CONTEXT and names later system changes", () => {
    const trajectory = buildTrajectory(
      [
        event(1, "system/message", "", {
          sections: [{ name: "Role", text: "a" }],
        }),
        event(2, "system/message", "", {
          sections: [
            { name: "Role", text: "b" },
            { name: "Tools", text: "c" },
          ],
        }),
        event(3, "turn/start", "t1", { turn_id: "t1" }, 1000),
        event(4, "user/message", "t1", {
          content: [{ type: "text", text: "lana wrote this" }],
          runtime: true,
        }, 1100),
        event(5, "turn/end", "t1", { reason: "end_turn" }, 1200),
      ],
      {}
    );
    const systemRecords = allRecords(trajectory).filter(
      (record) => record.kind === "system"
    );
    expect(systemRecords.map((record) => record.title)).toEqual([
      "System prompt · Role",
      "System prompt changed · Role, Tools",
    ]);
    const notes = allRecords(trajectory).filter(
      (record) => record.kind === "context"
    );
    expect(notes).toHaveLength(1);
    expect(notes[0]?.title).toBe("lana note · lana wrote this");
    expect(
      allRecords(trajectory).some((record) => record.kind === "user")
    ).toBe(false);
  });

  it("leaves an in-flight record running with no duration or usage", () => {
    const trajectory = buildTrajectory(
      [
        event(1, "turn/start", "t1", { turn_id: "t1" }, 1000),
        event(2, "step/start", "t1", { step: 1, provider: "p", model: "m" }, 2000),
        event(3, "tool/call", "t1", { call_id: "c1", name: "bash", input: { command: "ls" } }, 3000),
      ],
      {}
    );
    const assistant = findRecord(
      trajectory,
      (record) => record.kind === "assistant"
    );
    expect(assistant?.status).toBe("running");
    expect(assistant?.endMs).toBeUndefined();
    expect(assistant?.usage).toBeUndefined();
    const tool = assistant?.children?.[0];
    expect(tool?.status).toBe("running");
    expect(tool?.endMs).toBeUndefined();
  });
});

describe("buildTrajectory subagents", () => {
  const parent: SessionEvent[] = [
    event(1, "turn/start", "t1", { turn_id: "t1" }, 1000),
    event(2, "step/start", "t1", { step: 1, provider: "p", model: "m" }, 2000),
    event(3, "assistant/message", "t1", { content: [{ type: "text", text: "Delegating." }], stop_reason: "tool_use" }, 3000),
    event(4, "tool/call", "t1", { call_id: "call-9", name: "task", input: { agent: "explorer" } }, 4000),
    event(5, "tool/result", "t1", { call_id: "call-9", status: "success", output: '{"session_id":"child-1"}' }, 5000),
    event(6, "step/end", "t1", { step: 1, provider: "p", model: "m", usage: { input_tokens: 10, output_tokens: 2 } }, 6000),
    event(7, "turn/end", "t1", { reason: "end_turn" }, 7000),
  ];
  const child: SessionEvent[] = [
    event(1, "session/created", "", { agent: "explorer", parent_session_id: "root", parent_call_id: "call-9" }, 4100),
    event(2, "turn/start", "ct1", { turn_id: "ct1" }, 4200),
    event(3, "step/start", "ct1", { step: 1, provider: "p", model: "m" }, 4300),
    event(4, "assistant/message", "ct1", { content: [{ type: "text", text: "Child answer." }], stop_reason: "end_turn" }, 4400),
    event(5, "step/end", "ct1", { step: 1, provider: "p", model: "m", usage: { input_tokens: 5, output_tokens: 1 } }, 4500),
    event(6, "turn/end", "ct1", { reason: "end_turn" }, 4600),
  ];

  it("links a child session to the parent tool call and nests SUBTOOL records", () => {
    const trajectory = buildTrajectory(parent, { "child-1": child });
    const tool = findRecord(
      trajectory,
      (record) => record.kind === "tool" && record.callId === "call-9"
    );
    expect(tool?.childSessionId).toBe("child-1");
    expect(tool?.children?.length).toBe(1);
    expect(tool?.children?.[0]?.kind).toBe("subtool");
    expect(tool?.children?.[0]?.innerKind).toBe("assistant");
    expect(tool?.children?.[0]?.title).toBe("Child answer.");
  });

  it("uses the child's session/created link when the result has no session id", () => {
    const resultWithoutId = parent.map((entry) =>
      entry.kind === "tool/result"
        ? { ...entry, payload: { call_id: "call-9", status: "success", output: "done" } }
        : entry
    );
    const trajectory = buildTrajectory(resultWithoutId, { "child-1": child });
    const tool = findRecord(
      trajectory,
      (record) => record.kind === "tool" && record.callId === "call-9"
    );
    expect(tool?.childSessionId).toBe("child-1");
  });
});

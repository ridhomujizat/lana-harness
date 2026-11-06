import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { SessionDetail, SessionEvent } from "../api/sessions";
import { SessionPage } from "./session";
import { TrajectoryView } from "./trajectory";
import type { TrajectoryViewProps } from "./trajectory";

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

/* The fixture of AC-TRJ-002: a system prompt, injected context, one owner
   message, two requests, three tool calls (one failing), an approval, a retry,
   and a compaction. */
function fixture(): SessionEvent[] {
  return [
    event(
      1,
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
    event(2, "turn/start", "t1", { turn_id: "t1" }, 1000),
    event(
      3,
      "user/message",
      "t1",
      {
        content: [
          { type: "text", text: "<context>cwd</context>" },
          { type: "text", text: "Please check the disk" },
        ],
        context_blocks: 1,
        prompt_sections: [
          { name: "Role", text: "v2" },
          { name: "Tools", text: "same" },
        ],
      },
      1100
    ),
    event(4, "step/start", "t1", { step: 1, provider: "deepseek", model: "chat" }, 2000),
    event(
      5,
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
      6,
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
      7,
      "tool/call",
      "t1",
      { call_id: "c1", name: "read_file", input: { path: "/tmp/a" } },
      6000
    ),
    event(
      8,
      "approval/asked",
      "t1",
      { request_id: "a1", call_id: "c1", tool: "read_file", risk: "read" },
      6500
    ),
    event(
      9,
      "approval/decided",
      "t1",
      { request_id: "a1", call_id: "c1", outcome: "allowed_once" },
      7000
    ),
    event(
      10,
      "tool/result",
      "t1",
      { call_id: "c1", status: "success", output: "contents" },
      9000
    ),
    event(
      11,
      "tool/call",
      "t1",
      { call_id: "c2", name: "bash", input: { command: "df -h" } },
      9500
    ),
    event(
      12,
      "tool/result",
      "t1",
      { call_id: "c2", status: "success", output: "disk" },
      10000
    ),
    event(
      13,
      "tool/call",
      "t1",
      { call_id: "c3", name: "grep", input: { pattern: "error" } },
      10500
    ),
    event(
      14,
      "tool/result",
      "t1",
      { call_id: "c3", status: "error", output: "no match" },
      13000
    ),
    event(
      15,
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
      14000
    ),
    event(16, "step/start", "t1", { step: 2, provider: "deepseek", model: "chat" }, 15000),
    event(
      17,
      "assistant/message",
      "t1",
      { content: [{ type: "text", text: "Done." }], stop_reason: "end_turn" },
      17000
    ),
    event(
      18,
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
      18000
    ),
    event(19, "turn/end", "t1", { reason: "end_turn" }, 19000),
    event(
      20,
      "compaction/summary",
      "",
      {
        summary: "short",
        tokens_before: 5000,
        tokens_after: 2000,
        model: "chat",
        usage: { input_tokens: 200, output_tokens: 50 },
      },
      20000
    ),
    event(21, "approval/policy", "", { policy: "trust" }, 21000),
  ];
}

function renderTrajectory(
  events: SessionEvent[],
  overrides: Partial<TrajectoryViewProps> = {}
) {
  return render(
    <TrajectoryView
      sessionId="s1"
      events={events}
      childEvents={{}}
      {...overrides}
    />
  );
}

function rowByText(text: string | RegExp): HTMLTableRowElement {
  const table = screen.getByRole("table");
  const cell = within(table).getByText(text);
  const row = cell.closest("tr");
  if (!row) {
    throw new Error("no row for " + String(text));
  }
  return row as HTMLTableRowElement;
}

const detail: SessionDetail = {
  id: "s1",
  channel: "web",
  address: "",
  agent: "lana",
  title: "Disk check",
  status: "idle",
  preview: "",
  turn_count: 1,
  created_at: 1,
  updated_at: 2,
  in_use: "none",
  holder_pid: null,
  team: null,
  model: "deepseek:chat",
  policy: "ask",
  reasoning_effort: "",
  workspace: "/tmp/work",
  parent_session_id: "",
  turn_running: false,
};

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function stubSessionFetch(events: SessionEvent[]) {
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL): Promise<Response> => {
      const url = String(input);
      if (url.includes("/events")) {
        return json({ events, last_seq: events.length });
      }
      if (url.includes("/models")) {
        return json({ models: [], efforts: [] });
      }
      if (url.endsWith("/agents")) {
        return json({ agents: [] });
      }
      if (url.includes("/jobs")) {
        return json([]);
      }
      if (url.includes("/stream")) {
        return new Response("", { status: 200 });
      }
      return json(detail);
    }
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

beforeEach(() => {
  window.location.hash = "#/sessions/s1";
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.location.hash = "#/sessions/s1";
});

/* AC-TRJ-001: the switch writes the hash, and loading the hash opens the tab. */
describe("trajectory tab switch", () => {
  it("changes the hash when switching and opens Trajectory from ?view=trajectory", async () => {
    stubSessionFetch(fixture());
    const view = render(<SessionPage id="s1" />);

    expect(await screen.findByText("Disk check")).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "Trajectory" }));
    expect(window.location.hash).toBe("#/sessions/s1?view=trajectory");
    expect(screen.getByLabelText("Session trajectory")).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Chat" }));
    expect(window.location.hash).toBe("#/sessions/s1");

    view.unmount();
    window.location.hash = "#/sessions/s1?view=trajectory";
    render(<SessionPage id="s1" />);
    expect(await screen.findByLabelText("Session trajectory")).toBeTruthy();
  });
});

/* AC-TRJ-002: every requirement kind renders with the ledger columns. */
describe("trajectory ledger", () => {
  it("renders the requirement kinds and their columns", () => {
    renderTrajectory(fixture());

    for (const header of [
      "Kind",
      "Content",
      "Input",
      "Output",
      "Think",
      "Time",
      "Model",
      "Tools",
    ]) {
      expect(screen.getByRole("columnheader", { name: header })).toBeTruthy();
    }

    expect(screen.getByText("SYSTEM")).toBeTruthy();
    expect(screen.getAllByText("CONTEXT").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("USER")).toBeTruthy();
    expect(screen.getAllByText("ASSISTANT")).toHaveLength(2);
    expect(screen.getByText("RETRY")).toBeTruthy();
    expect(screen.getAllByText("TOOL")).toHaveLength(3);
    expect(screen.getByText("APPROVAL")).toBeTruthy();
    expect(screen.getByText("COMPACTED")).toBeTruthy();
    expect(screen.getByText("SETTING")).toBeTruthy();

    expect(screen.getByText("System prompt · Role, Tools")).toBeTruthy();
    expect(screen.getByText("Prompt sections · Role")).toBeTruthy();
    expect(screen.getByText("Please check the disk")).toBeTruthy();

    const assistant = rowByText("Checking.");
    expect(within(assistant).getByText(/800 cached/)).toBeTruthy();
    expect(within(assistant).getByText("300")).toBeTruthy();
    expect(within(assistant).getByText("6")).toBeTruthy();
    expect(within(assistant).getByText("3s")).toBeTruthy();
    expect(within(assistant).getByText("chat")).toBeTruthy();
    expect(within(assistant).getByText("3")).toBeTruthy();

    const read = rowByText(/\/tmp\/a/);
    expect(within(read).getByText(/read_file/)).toBeTruthy();
    expect(within(read).getByText("3s")).toBeTruthy();

    const failed = rowByText(/grep/);
    expect(within(failed).getAllByText(/error/).length).toBeGreaterThanOrEqual(1);
    expect(failed.className).toContain("is-error");

    const compacted = rowByText("5000 → 2000 tokens");
    expect(within(compacted).getByText("chat")).toBeTruthy();
  });
});

/* AC-TRJ-003: turn and step headers group the rows and between-turn events
   land in their own section. */
describe("trajectory grouping", () => {
  it("renders turn headers, step headers, and Between turns", () => {
    renderTrajectory(fixture());
    expect(screen.getByText("Turn 1")).toBeTruthy();
    expect(screen.getByText(/Step 1 · deepseek\/chat/)).toBeTruthy();
    expect(screen.getByText(/Step 2 · deepseek\/chat/)).toBeTruthy();
    expect(screen.getAllByText("Between turns")).toHaveLength(2);
    expect(screen.getByText(/end of turn/)).toBeTruthy();
    const turnButton = screen.getByText("Turn 1").closest("button");
    expect(turnButton?.textContent).toContain("2,700 in");
  });

  it("marks an abnormal turn end", () => {
    renderTrajectory([
      event(1, "turn/start", "t1", { turn_id: "t1" }, 1000),
      event(2, "user/message", "t1", { content: [{ type: "text", text: "Hi" }] }, 1100),
      event(3, "turn/end", "t1", { reason: "error", error: { message: "boom" } }, 2000),
    ]);
    expect(screen.getByText("failed")).toBeTruthy();
  });
});

/* AC-TRJ-004: search filters rows but keeps their turn and step headers. */
describe("trajectory search", () => {
  it("filters rows case-insensitively and keeps headers", () => {
    renderTrajectory(fixture());
    const search = screen.getByLabelText("Search trajectory");
    fireEvent.change(search, { target: { value: "GREP" } });
    const table = screen.getByRole("table");

    expect(within(table).getByText(/grep/)).toBeTruthy();
    expect(within(table).getByText("Turn 1")).toBeTruthy();
    expect(within(table).getByText(/Step 1 · deepseek\/chat/)).toBeTruthy();
    expect(within(table).queryByText(/Step 2 · deepseek\/chat/)).toBeNull();
    expect(within(table).queryByText("Please check the disk")).toBeNull();
    expect(
      within(table).queryByText("System prompt · Role, Tools")
    ).toBeNull();
    expect(within(table).queryByText(/\/tmp\/a/)).toBeNull();

    fireEvent.change(search, { target: { value: "" } });
    expect(within(table).getByText("Please check the disk")).toBeTruthy();
  });
});

/* AC-TRJ-005: a TOOL row opens Payload and Result, and Raw carries both
   events. */
describe("trajectory tool inspector", () => {
  it("shows the recorded input, output, and raw events", () => {
    renderTrajectory(fixture());
    fireEvent.click(rowByText(/\/tmp\/a/));

    expect(screen.getByLabelText("Record inspector")).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "Payload" }));
    expect(screen.getByText(/"path": "\/tmp\/a"/)).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Result" }));
    expect(screen.getByText("contents")).toBeTruthy();
    expect(screen.getByText("success")).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Raw" }));
    const inspector = screen.getByLabelText("Record inspector");
    expect(within(inspector).getByText(/· tool\/call/)).toBeTruthy();
    expect(within(inspector).getByText(/· tool\/result/)).toBeTruthy();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByLabelText("Record inspector")).toBeNull();
  });
});

/* AC-TRJ-006: Usage shows this request and the cumulative total up to it. */
describe("trajectory usage inspector", () => {
  it("shows request and cumulative tokens for the second request", () => {
    renderTrajectory(fixture());
    fireEvent.click(screen.getByText("Done."));
    fireEvent.click(screen.getByRole("tab", { name: "Usage" }));
    const inspector = screen.getByLabelText("Record inspector");

    expect(within(inspector).getByText("This request")).toBeTruthy();
    expect(within(inspector).getByText("Session cumulative")).toBeTruthy();
    expect(within(inspector).getByText("1,500")).toBeTruthy();
    expect(within(inspector).getByText("1,000")).toBeTruthy();
    expect(within(inspector).getByText("120")).toBeTruthy();
    expect(within(inspector).getByText("2,700")).toBeTruthy();
    expect(within(inspector).getByText("1,800")).toBeTruthy();
    expect(within(inspector).getByText("420")).toBeTruthy();
    expect(within(inspector).getAllByText("67%").length).toBeGreaterThanOrEqual(1);
  });
});

/* AC-TRJ-007: appending events adds rows without remounting, so view state
   survives. */
describe("trajectory live updates", () => {
  it("adds rows for appended events and keeps the collapsed turn state", () => {
    const first = [
      event(1, "turn/start", "t1", { turn_id: "t1" }, 1000),
      event(2, "user/message", "t1", { content: [{ type: "text", text: "One" }] }, 1100),
      event(3, "step/start", "t1", { step: 1, provider: "p", model: "m" }, 1200),
      event(4, "assistant/message", "t1", { content: [{ type: "text", text: "First answer." }] }, 1300),
      event(5, "turn/end", "t1", { reason: "end_turn" }, 1400),
    ];
    const second = first.concat([
      event(6, "turn/start", "t2", { turn_id: "t2" }, 2000),
      event(7, "user/message", "t2", { content: [{ type: "text", text: "Two" }] }, 2100),
      event(8, "step/start", "t2", { step: 1, provider: "p", model: "m" }, 2200),
      event(9, "assistant/message", "t2", { content: [{ type: "text", text: "Second answer." }] }, 2300),
      event(10, "turn/end", "t2", { reason: "end_turn" }, 2400),
    ]);

    const view = renderTrajectory(first);
    fireEvent.click(screen.getByRole("button", { name: "Collapse turns" }));
    expect(screen.queryByText("First answer.")).toBeNull();

    view.rerender(
      <TrajectoryView sessionId="s1" events={second} childEvents={{}} />
    );
    expect(screen.getByText("Turn 2")).toBeTruthy();
    expect(screen.getByText("Second answer.")).toBeTruthy();
    expect(screen.queryByText("First answer.")).toBeNull();
  });
});

/* AC-TRJ-008: values that were not recorded show "—". */
describe("trajectory unrecorded values", () => {
  it("renders dashes for an owner row and for a running tool", () => {
    renderTrajectory([
      event(1, "turn/start", "t1", { turn_id: "t1" }, 1000),
      event(2, "user/message", "t1", { content: [{ type: "text", text: "Hello" }] }, 1100),
      event(3, "step/start", "t1", { step: 1, provider: "p", model: "m" }, 1200),
      event(4, "tool/call", "t1", { call_id: "c1", name: "bash", input: { command: "ls" } }, 1300),
    ]);
    const user = rowByText("Hello");
    expect(within(user).getAllByText("—")).toHaveLength(6);

    const tool = rowByText(/bash/);
    expect(within(tool).getAllByText("running").length).toBeGreaterThanOrEqual(1);
    expect(within(tool).getAllByText("—").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText(/usage —/)).toBeTruthy();
  });
});

/* REQ-TRJ-007: rows are keyboard selectable. */
describe("trajectory row keyboard", () => {
  it("moves selection with arrows and opens the inspector with Enter", () => {
    renderTrajectory(fixture());
    const rows = Array.from(
      screen.getByRole("table").querySelectorAll("tr[data-record-key]")
    );
    expect(rows.length).toBeGreaterThan(2);
    const first = rows[0] as HTMLTableRowElement;
    const second = rows[1] as HTMLTableRowElement;
    first.focus();
    fireEvent.keyDown(first, { key: "ArrowDown" });
    expect(second.getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(second, { key: "Enter" });
    expect(screen.getByLabelText("Record inspector")).toBeTruthy();
  });
});

/* REQ-TRJ-006: the timing overview selects a record and the ledger shows it. */
describe("trajectory overview", () => {
  it("draws spans and opens the inspector when one is clicked", () => {
    renderTrajectory(fixture());
    const overview = screen.getByLabelText("Trajectory timing overview");
    const spans = within(overview).getAllByRole("button");
    expect(spans.length).toBeGreaterThanOrEqual(5);
    fireEvent.click(spans[0] as HTMLElement);
    expect(screen.getByLabelText("Record inspector")).toBeTruthy();
  });
});

/* REQ-TRJ-009: a child session nests under the tool that started it. */
describe("trajectory subagents", () => {
  it("expands SUBTOOL rows and links to the child trajectory", () => {
    const parent = [
      event(1, "turn/start", "t1", { turn_id: "t1" }, 1000),
      event(2, "step/start", "t1", { step: 1, provider: "p", model: "m" }, 1200),
      event(3, "assistant/message", "t1", { content: [{ type: "text", text: "Delegating." }] }, 1300),
      event(4, "tool/call", "t1", { call_id: "call-9", name: "task", input: { agent: "explorer" } }, 1400),
      event(5, "tool/result", "t1", { call_id: "call-9", status: "success", output: '{"session_id":"child-1"}' }, 1500),
      event(6, "turn/end", "t1", { reason: "end_turn" }, 1600),
    ];
    const child = [
      event(1, "session/created", "", { agent: "explorer", parent_call_id: "call-9" }, 1450),
      event(2, "turn/start", "ct1", { turn_id: "ct1" }, 1460),
      event(3, "step/start", "ct1", { step: 1, provider: "p", model: "m" }, 1470),
      event(4, "assistant/message", "ct1", { content: [{ type: "text", text: "Child answer." }] }, 1480),
      event(5, "turn/end", "ct1", { reason: "end_turn" }, 1490),
    ];
    renderTrajectory(parent, { childEvents: { "child-1": child } });

    fireEvent.click(screen.getByRole("button", { name: "Show subtools" }));
    expect(screen.getByText("SUBTOOL")).toBeTruthy();
    expect(screen.getByText("Child answer.")).toBeTruthy();
    const link = screen.getByRole("link", { name: "Open child trajectory" });
    expect(link.getAttribute("href")).toBe(
      "#/sessions/child-1?view=trajectory"
    );
  });
});

/* REQ-TRJ-010: a session over ten turns opens only its last three turns. */
describe("trajectory long sessions", () => {
  function longSession(): SessionEvent[] {
    const events: SessionEvent[] = [];
    let seq = 1;
    for (let turn = 1; turn <= 11; turn += 1) {
      const turnId = "t" + turn;
      const base = turn * 1000;
      events.push(event(seq, "turn/start", turnId, { turn_id: turnId }, base));
      seq += 1;
      events.push(
        event(
          seq,
          "user/message",
          turnId,
          { content: [{ type: "text", text: "Message " + turn }] },
          base + 100
        )
      );
      seq += 1;
      events.push(
        event(
          seq,
          "step/start",
          turnId,
          { step: 1, provider: "p", model: "m" },
          base + 200
        )
      );
      seq += 1;
      events.push(
        event(
          seq,
          "assistant/message",
          turnId,
          { content: [{ type: "text", text: "Answer " + turn }] },
          base + 300
        )
      );
      seq += 1;
      events.push(
        event(seq, "turn/end", turnId, { reason: "end_turn" }, base + 400)
      );
      seq += 1;
    }
    return events;
  }

  it("collapses all but the last three turns and expands them on demand", () => {
    renderTrajectory(longSession());
    expect(screen.getByText("Turn 1")).toBeTruthy();
    expect(screen.queryByText("Message 1")).toBeNull();
    expect(screen.getByText("Message 9")).toBeTruthy();
    expect(screen.getByText("Message 11")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Collapse turns" }));
    expect(screen.queryByText("Message 9")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Expand turns" }));
    expect(screen.getByText("Message 1")).toBeTruthy();
  });
});

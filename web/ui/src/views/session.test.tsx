import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../api/client";
import type { SessionDetail, SessionEvent } from "../api/sessions";
import * as turnsApi from "../api/turns";
import { SessionPage } from "./session";

/* The turn calls are mocked so Continue and the draining send can be observed
   without a fetch stub for them (API-TFB-003, API-TFB-008). */
vi.mock("../api/turns", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/turns")>();
  return {
    ...actual,
    sendMessage: vi.fn(),
    continueSession: vi.fn(),
    cancelTurn: vi.fn(),
  };
});

const detail: SessionDetail = {
  id: "s1",
  channel: "web",
  address: "",
  agent: "lana",
  title: "Disk check",
  status: "idle",
  preview: "",
  turn_count: 2,
  created_at: 1,
  updated_at: 2,
  in_use: "none",
  holder_pid: null,
  team: null,
  model: "deepseek:chat",
  policy: "ask",
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

afterEach(() => {
  vi.unstubAllGlobals();
});

function sessionEvent(
  seq: number,
  kind: string,
  turnId: string,
  payload: Record<string, unknown>
): SessionEvent {
  return { seq, kind, turn_id: turnId, created_at: seq, payload };
}

function stubFetch(events: SessionEvent[]): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/events")) {
        return json({ events, last_seq: events.length });
      }
      if (url.endsWith("/agents")) {
        return json({ agents: [] });
      }
      if (url.includes("/stream")) {
        return new Response("", { status: 200 });
      }
      return json(detail);
    })
  );
}

const interruptedTurn: SessionEvent[] = [
  sessionEvent(1, "turn/start", "t1", { turn_id: "t1" }),
  sessionEvent(2, "user/message", "t1", {
    content: [{ type: "text", text: "check the db" }],
  }),
  sessionEvent(3, "step/start", "t1", { step: 4 }),
  sessionEvent(4, "tool/call", "t1", {
    call_id: "c1",
    name: "dbx_list_tables",
    input: {},
  }),
  sessionEvent(5, "tool/result", "t1", {
    call_id: "c1",
    status: "error",
    output: "boom",
  }),
  sessionEvent(6, "turn/end", "t1", {
    reason: "interrupted",
    error: { message: "lana restarted while this turn was running" },
  }),
];

const normalTurnWithFailedTool: SessionEvent[] = [
  sessionEvent(1, "turn/start", "t1", { turn_id: "t1" }),
  sessionEvent(2, "user/message", "t1", {
    content: [{ type: "text", text: "check the db" }],
  }),
  sessionEvent(3, "tool/call", "t1", {
    call_id: "c1",
    name: "dbx_list_tables",
    input: {},
  }),
  sessionEvent(4, "tool/result", "t1", {
    call_id: "c1",
    status: "error",
    output: "boom",
  }),
  sessionEvent(5, "turn/end", "t1", { reason: "end_turn" }),
];

describe("session chat", () => {
  it("renders the header, empty thread, and composer", async () => {
    stubFetch([]);

    render(<SessionPage id="s1" />);
    expect(await screen.findByText("Disk check")).toBeTruthy();
    expect(screen.getByText("2 turns")).toBeTruthy();
    expect(screen.getByText("No messages yet")).toBeTruthy();
    expect(screen.getByLabelText("Message lana")).toBeTruthy();
  });
});

/* REQ-TFB-004: the status card shows under the last turn on an abnormal end. */
describe("session status card", () => {
  it("shows the reason and the last step for an interrupted turn", async () => {
    stubFetch(interruptedTurn);

    render(<SessionPage id="s1" />);
    expect(
      await screen.findByText("lana restarted while working.")
    ).toBeTruthy();
    expect(screen.getByText("Last step: dbx_list_tables (step 4)")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continue" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeTruthy();
  });

  /* AC-TFB-009: a failed tool inside a normal end shows no card. */
  it("shows no card when the last turn ended normally with a failed tool", async () => {
    stubFetch(normalTurnWithFailedTool);

    render(<SessionPage id="s1" />);
    expect(await screen.findByText("check the db")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
    expect(
      screen.queryByText("lana restarted while working.")
    ).toBeNull();
  });

  it("hides the card when Dismiss is pressed", async () => {
    stubFetch(interruptedTurn);

    render(<SessionPage id="s1" />);
    expect(
      await screen.findByText("lana restarted while working.")
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(
      screen.queryByText("lana restarted while working.")
    ).toBeNull();
  });
});

/* REQ-TFB-005: Continue starts a new turn (API-TFB-003). */
describe("session continue", () => {
  it("calls continueSession when the card's Continue is pressed", async () => {
    stubFetch(interruptedTurn);
    vi.mocked(turnsApi.continueSession).mockResolvedValue({
      turn: "started",
      turn_id: "t2",
    });

    render(<SessionPage id="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(vi.mocked(turnsApi.continueSession)).toHaveBeenCalledWith("s1");
    });
  });

  it("shows the refusal text in the card when continue is refused", async () => {
    stubFetch(interruptedTurn);
    vi.mocked(turnsApi.continueSession).mockRejectedValue(
      new ApiError(409, "nothing_to_continue", "nothing to continue")
    );

    render(<SessionPage id="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));

    expect(
      await screen.findByText(
        "Nothing to continue. The last turn ended normally."
      )
    ).toBeTruthy();
  });

  /* API-TFB-004: a continue message renders as a chip, not owner text. */
  it("renders a continue user message as the ↻ Continued chip", async () => {
    stubFetch([
      sessionEvent(1, "turn/start", "t1", { turn_id: "t1" }),
      sessionEvent(2, "user/message", "t1", {
        content: [{ type: "text", text: "Continue the previous task." }],
        continue: true,
      }),
      sessionEvent(3, "turn/end", "t1", { reason: "end_turn" }),
    ]);

    render(<SessionPage id="s1" />);
    expect(await screen.findByText("↻ Continued")).toBeTruthy();
    expect(screen.queryByText("Continue the previous task.")).toBeNull();
  });
});

/* API-TFB-008: a send refused while the daemon drains keeps the text. */
describe("session draining send", () => {
  it("shows the restart message near the composer on 503 daemon_draining", async () => {
    stubFetch([]);
    vi.mocked(turnsApi.sendMessage).mockRejectedValue(
      new ApiError(503, "daemon_draining", "lana is restarting. Try again in a moment.")
    );

    render(<SessionPage id="s1" />);
    const input = (await screen.findByLabelText(
      "Message lana"
    )) as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "hello" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    expect(
      await screen.findByText("lana is restarting. Try again in a moment.")
    ).toBeTruthy();
    expect(input.value).toBe("hello");
  });
});

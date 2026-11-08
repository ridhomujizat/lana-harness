import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ApiError } from "../api/client";
import type { ModelsResponse } from "../api/models";
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

/* The jobs strip fetches on mount (REQ-JOB-020); these tests do not exercise
   it, so it is mocked to an empty list. */
vi.mock("../api/jobs", () => ({
  listSessionJobs: vi.fn().mockResolvedValue([]),
  stopSessionJob: vi.fn(),
}));

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

/* The model catalog of GET /v1/models (REQ-MDL-020). "chat" is the session's
   current model; "old" is a legacy entry served without thinking. */
const CATALOG: ModelsResponse = {
  models: [
    {
      id: "chat",
      name: "DeepSeek Chat",
      description: "Current model.",
      context_window: 128000,
      legacy: false,
      replacement: "",
      thinking: "deepseek",
      default_effort: "high",
    },
    {
      id: "flash",
      name: "DeepSeek Flash",
      description: "Fast model.",
      context_window: 1048576,
      legacy: false,
      replacement: "",
      thinking: "deepseek",
      default_effort: "high",
    },
    {
      id: "old",
      name: "DeepSeek Old",
      description: "Legacy model.",
      context_window: 64000,
      legacy: true,
      replacement: "deepseek-flash",
      thinking: "",
      default_effort: "",
    },
  ],
  efforts: [
    { value: "low", description: "Quick answers." },
    { value: "high", description: "The default balance." },
  ],
};

function stubFetch(
  events: SessionEvent[],
  overrides: Partial<SessionDetail> = {}
) {
  const base: SessionDetail = { ...detail, ...overrides };
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = String(input);
      const method = init && init.method ? init.method : "GET";
      if (method === "PATCH" && url.includes("/v1/sessions/")) {
        const patch = init && init.body ? JSON.parse(String(init.body)) : {};
        return json({ ...base, ...patch });
      }
      if (url.includes("/events")) {
        return json({ events, last_seq: events.length });
      }
      if (url.includes("/models")) {
        return json(CATALOG);
      }
      if (url.endsWith("/agents")) {
        return json({ agents: [] });
      }
      if (url.includes("/stream")) {
        return new Response("", { status: 200 });
      }
      return json(base);
    }
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function patchBody(
  fetchMock: ReturnType<typeof vi.fn>
): Record<string, unknown> | null {
  for (const call of fetchMock.mock.calls) {
    const init = call[1] as RequestInit | undefined;
    if (init && init.method === "PATCH") {
      return JSON.parse(String(init.body)) as Record<string, unknown>;
    }
  }
  return null;
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

/* The composer Stop button replaces Send while a turn runs and asks the server
   to cancel it; a server with no turn brings Send back. */
describe("session stop button", () => {
  it("shows Stop instead of Send while a turn runs, and Stop asks the server to cancel it", async () => {
    stubFetch([], { turn_running: true });
    vi.mocked(turnsApi.cancelTurn).mockResolvedValue({ turn: "cancelling" });

    render(<SessionPage id="s1" />);
    const stop = await screen.findByRole("button", {
      name: "Stop the running turn",
    });
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();

    fireEvent.click(stop);
    await waitFor(() => {
      expect(vi.mocked(turnsApi.cancelTurn)).toHaveBeenCalledWith("s1");
    });
    expect(await screen.findByText("Stopping the turn…")).toBeTruthy();
  });

  it("says no turn is running and brings Send back when the server has no turn", async () => {
    stubFetch([], { turn_running: true });
    vi.mocked(turnsApi.cancelTurn).mockResolvedValue({ turn: "idle" });

    render(<SessionPage id="s1" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Stop the running turn" })
    );

    expect(
      await screen.findByText("No turn is running.", { selector: "p.notice" })
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Send message" })).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Stop the running turn" })
    ).toBeNull();
  });
});

/* REQ-MDL-020: the composer model menu lists the provider catalog. */
describe("composer model catalog", () => {
  it("lists the catalog, tags legacy, checks the current model, and sets provider:model", async () => {
    const fetchMock = stubFetch([]);

    render(<SessionPage id="s1" />);
    const pill = await screen.findByRole("button", {
      name: "Model: deepseek:chat",
    });
    fireEvent.click(pill);

    expect(await screen.findByText("DeepSeek Flash")).toBeTruthy();
    expect(screen.getByText("deepseek:flash")).toBeTruthy();
    expect(screen.getByText("Legacy")).toBeTruthy();

    const current = screen.getByRole("menuitemradio", {
      name: /DeepSeek Chat/,
    });
    expect(current.getAttribute("aria-checked")).toBe("true");
    const legacy = screen.getByRole("menuitemradio", { name: /DeepSeek Old/ });
    expect(legacy.getAttribute("aria-checked")).toBe("false");

    fireEvent.click(
      screen.getByRole("menuitemradio", { name: /DeepSeek Flash/ })
    );

    await waitFor(() => {
      expect(patchBody(fetchMock)).toMatchObject({ model: "deepseek:flash" });
    });
    expect(
      await screen.findByRole("button", { name: "Model: deepseek:flash" })
    ).toBeTruthy();
  });

  /* Spec 37 (REQ-RTR-018): with the router, the menu offers the agent picker
     choices, combos first, and sets the picked value as is. */
  it("offers the router choices and sets a combo", async () => {
    const fetchMock = stubFetch([]);
    const base = fetchMock.getMockImplementation();
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/v1/router")) {
        return json({
          version: "v1",
          choices: [
            { value: "coding", label: "coding", group: "Combos" },
            { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek" },
          ],
        });
      }
      return base ? base(input, init) : json({});
    });

    render(<SessionPage id="s1" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Model: deepseek:chat" })
    );
    const combos = await screen.findByRole("group", { name: "Combos" });
    expect(within(combos).getByRole("menuitemradio", { name: /coding/ })).toBeTruthy();
    expect(screen.getByRole("group", { name: "deepseek" })).toBeTruthy();
    fireEvent.click(within(combos).getByRole("menuitemradio", { name: /coding/ }));

    await waitFor(() => {
      expect(patchBody(fetchMock)).toMatchObject({ model: "coding" });
    });
  });

  it("keeps the free-text provider:model input working", async () => {
    const fetchMock = stubFetch([]);

    render(<SessionPage id="s1" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Model: deepseek:chat" })
    );
    const custom = screen.getByLabelText("Custom model as provider:model");
    fireEvent.change(custom, { target: { value: "custom:my-model" } });
    fireEvent.click(screen.getByRole("button", { name: "Set" }));

    await waitFor(() => {
      expect(patchBody(fetchMock)).toMatchObject({ model: "custom:my-model" });
    });
  });

  /* A model change keeps the session effort; only "Model default" clears it. */
  it("keeps the session effort when the model changes", async () => {
    const fetchMock = stubFetch([], { reasoning_effort: "high" });

    render(<SessionPage id="s1" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Model: deepseek:chat" })
    );
    fireEvent.click(
      screen.getByRole("menuitemradio", { name: /DeepSeek Flash/ })
    );

    await waitFor(() => {
      expect(patchBody(fetchMock)).toMatchObject({ model: "deepseek:flash" });
    });
    expect(patchBody(fetchMock)).not.toHaveProperty("reasoning_effort");
    expect(
      await screen.findByRole("button", { name: "Thinking: High" })
    ).toBeTruthy();
  });
});

/* REQ-MDL-001: the Thinking chip shows for a thinking model and sets the
   session effort; "Model default" clears it. */
describe("composer thinking chip", () => {
  it("shows the chip, sets an effort, and labels it Thinking: High", async () => {
    const fetchMock = stubFetch([]);

    render(<SessionPage id="s1" />);
    const chip = await screen.findByRole("button", {
      name: "Thinking: Model default",
    });
    fireEvent.click(chip);
    fireEvent.click(screen.getByRole("menuitemradio", { name: /High/ }));

    await waitFor(() => {
      expect(patchBody(fetchMock)).toMatchObject({ reasoning_effort: "high" });
    });
    expect(
      await screen.findByRole("button", { name: "Thinking: High" })
    ).toBeTruthy();
  });

  it("clears the session effort with Model default", async () => {
    const fetchMock = stubFetch([], { reasoning_effort: "high" });

    render(<SessionPage id="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Thinking: High" }));
    fireEvent.click(
      screen.getByRole("menuitemradio", { name: /Model default/ })
    );

    await waitFor(() => {
      expect(patchBody(fetchMock)).toMatchObject({ reasoning_effort: "" });
    });
  });

  it("hides the chip for a non-thinking model", async () => {
    stubFetch([], { model: "deepseek:old" });

    render(<SessionPage id="s1" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Model: deepseek:old" })
    );
    await screen.findByText("DeepSeek Flash");
    expect(
      screen.queryByRole("button", { name: "Thinking: Model default" })
    ).toBeNull();
  });
});

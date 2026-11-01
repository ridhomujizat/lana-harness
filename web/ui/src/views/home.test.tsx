import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ApiError } from "../api/client";
import { getDashboard } from "../api/dashboard";
import type { Dashboard } from "../api/dashboard";
import { Sidebar } from "../app/Sidebar";
import { HomePage } from "./home";

/* The dashboard reads one endpoint, so the module is mocked and the view is
   rendered on its own (AC-DSH-004, AC-DSH-005, AC-DSH-006). */
vi.mock("../api/dashboard", () => ({
  getDashboard: vi.fn(),
}));

function makeDashboard(overrides: Partial<Dashboard> = {}): Dashboard {
  return {
    range: "24h",
    agent: "",
    agents: ["assistant", "lana"],
    from: 1761900000000,
    to: 1761986400000,
    bucket_ms: 3600000,
    daemon_started_at: 1761980000000,
    now: { sessions_running: 1, sessions_waiting_input: 2 },
    summary: {
      input_tokens: 120000,
      cached_input_tokens: 80000,
      output_tokens: 9000,
      turns: 42,
      turns_failed: 3,
      turns_cancelled: 1,
      tool_calls: 310,
      tool_errors: 12,
      tool_rejected: 2,
      provider_retries: 4,
    },
    series: [
      {
        start: 1761900000000,
        input_tokens: 10000,
        cached_input_tokens: 6000,
        output_tokens: 1000,
      },
      {
        start: 1761903600000,
        input_tokens: 20000,
        cached_input_tokens: 14000,
        output_tokens: 2000,
      },
    ],
    by_model: [
      {
        provider: "deepseek",
        model: "deepseek-chat",
        requests: 120,
        input_tokens: 100000,
        cached_input_tokens: 70000,
        output_tokens: 8000,
      },
    ],
    by_agent: [
      {
        agent: "assistant",
        turns: 40,
        input_tokens: 100000,
        cached_input_tokens: 70000,
        output_tokens: 8000,
      },
    ],
    by_source: [
      {
        source: "turn",
        count: 120,
        input_tokens: 100000,
        cached_input_tokens: 70000,
        output_tokens: 8000,
      },
      {
        source: "compaction",
        count: 2,
        input_tokens: 5000,
        cached_input_tokens: 3000,
        output_tokens: 200,
      },
      {
        source: "memory_capture",
        count: 10,
        input_tokens: 15000,
        cached_input_tokens: 7000,
        output_tokens: 800,
      },
    ],
    tools: [
      {
        name: "read_file",
        calls: 80,
        errors: 30,
        rejected: 0,
        avg_ms: 35,
        p95_ms: 120,
        last_error: "file not found: a.txt",
        last_error_at: 1761985000000,
      },
      {
        name: "bash",
        calls: 20,
        errors: 1,
        rejected: 1,
        avg_ms: 1200,
        p95_ms: 4500,
        last_error: "",
        last_error_at: 0,
      },
      {
        name: "grep",
        calls: 4,
        errors: 3,
        rejected: 0,
        avg_ms: 10,
        p95_ms: 20,
        last_error: "boom",
        last_error_at: 1761985000000,
      },
    ],
    turn_reasons: [
      { reason: "end_turn", count: 38 },
      { reason: "error", count: 3 },
    ],
    turn_duration: { avg_ms: 12000, p95_ms: 45000 },
    errors: [
      {
        at: 1761985000000,
        source: "tool",
        level: "error",
        agent: "assistant",
        session_id: "s_abc",
        turn_id: "t_1",
        tool: "read_file",
        message: "read_file: file not found: a.txt",
      },
      {
        at: 1761984000000,
        source: "daemon",
        level: "warn",
        agent: "",
        session_id: "",
        turn_id: "",
        tool: "",
        message: "mcp server exited name=github err=exit status 1",
      },
    ],
    ...overrides,
  };
}

const EMPTY_DASHBOARD: Partial<Dashboard> = {
  summary: {
    input_tokens: 0,
    cached_input_tokens: 0,
    output_tokens: 0,
    turns: 0,
    turns_failed: 0,
    turns_cancelled: 0,
    tool_calls: 0,
    tool_errors: 0,
    tool_rejected: 0,
    provider_retries: 0,
  },
  series: [],
  by_model: [],
  by_agent: [],
  by_source: [
    {
      source: "turn",
      count: 0,
      input_tokens: 0,
      cached_input_tokens: 0,
      output_tokens: 0,
    },
    {
      source: "compaction",
      count: 0,
      input_tokens: 0,
      cached_input_tokens: 0,
      output_tokens: 0,
    },
    {
      source: "memory_capture",
      count: 0,
      input_tokens: 0,
      cached_input_tokens: 0,
      output_tokens: 0,
    },
  ],
  tools: [],
  turn_reasons: [],
  errors: [],
};

afterEach(() => {
  vi.clearAllMocks();
  /* The agent tests write the hash; start every test from a clean home URL. */
  window.history.replaceState(null, "", "#/home");
});

describe("dashboard render (AC-DSH-004)", () => {
  it("renders every section from a mocked response", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    expect(await screen.findByText("Right now")).toBeTruthy();
    expect(screen.getByText("129,000")).toBeTruthy();
    expect(screen.getByText("Cached 67% of input")).toBeTruthy();
    expect(screen.getByText("Failed 3 · 7%")).toBeTruthy();
    expect(screen.getByText("Errors 12 · 4%")).toBeTruthy();
    expect(screen.getByText("Provider retries")).toBeTruthy();

    expect(
      screen.getByRole("img", { name: /Token usage over time/ })
    ).toBeTruthy();

    expect(screen.getByText("By model")).toBeTruthy();
    expect(screen.getByText("By agent")).toBeTruthy();
    expect(screen.getByText("By source")).toBeTruthy();
    expect(screen.getByText("Chat turns")).toBeTruthy();
    expect(screen.getByText("Compaction")).toBeTruthy();
    expect(screen.getByText("Memory capture")).toBeTruthy();
    expect(screen.getByText("Tools")).toBeTruthy();
    expect(screen.getByText("Turn outcomes")).toBeTruthy();
    expect(screen.getByText("Error log")).toBeTruthy();
  });

  it("marks a tool row only with enough calls and a high error share", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    /* read_file: 80 calls, 30 errors (38%) is marked; grep: 4 calls is not. */
    const readFile = (await screen.findByText("read_file")).closest("tr");
    const grep = screen.getByText("grep").closest("tr");
    expect(readFile?.className).toContain("is-danger");
    expect(grep?.className).not.toContain("is-danger");
  });

  it("shows empty states for every section", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard(EMPTY_DASHBOARD));
    render(<HomePage />);

    expect(
      await screen.findByText("No token usage in this range.")
    ).toBeTruthy();
    expect(
      screen.getByText("No token usage by source in this range.")
    ).toBeTruthy();
    expect(screen.getByText("No model usage in this range.")).toBeTruthy();
    expect(screen.getByText("No agent activity in this range.")).toBeTruthy();
    expect(screen.getByText("No tool calls in this range.")).toBeTruthy();
    expect(screen.getByText("No turns in this range.")).toBeTruthy();
    expect(screen.getByText("No errors in this range.")).toBeTruthy();
  });

  it("switches range and refetches with the new range", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("24h");
    });

    fireEvent.click(screen.getByRole("button", { name: "7 days" }));
    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("7d");
    });
  });

  it("ignores a response for a range the owner already left", async () => {
    const pending = new Map<string, (value: Dashboard) => void>();
    vi.mocked(getDashboard).mockImplementation(
      (next) =>
        new Promise<Dashboard>((resolve) => {
          pending.set(next, resolve);
        })
    );

    render(<HomePage />);
    await waitFor(() => expect(pending.has("24h")).toBe(true));

    fireEvent.click(screen.getByRole("button", { name: "7 days" }));
    await waitFor(() => expect(pending.has("7d")).toBe(true));

    const sevenDay = makeDashboard({
      range: "7d",
      summary: { ...EMPTY_DASHBOARD.summary!, input_tokens: 700000 },
    });

    /* The 7d response resolves first and is shown. */
    pending.get("7d")!(sevenDay);
    expect(await screen.findByText("700,000")).toBeTruthy();

    /* The slower 24h response resolves last and must not overwrite it. */
    await act(async () => {
      pending.get("24h")!(makeDashboard());
    });
    expect(screen.getByText("700,000")).toBeTruthy();
    expect(screen.queryByText("129,000")).toBeNull();
  });

  it("keeps the last data and offers a retry when a request fails", async () => {
    vi.mocked(getDashboard).mockResolvedValueOnce(makeDashboard());
    render(<HomePage />);
    await screen.findByText("Right now");

    vi.mocked(getDashboard).mockRejectedValueOnce(
      new ApiError(500, "internal_error", "boom")
    );
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("boom");
    expect(screen.getByText("Right now")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });
});

describe("error log (AC-DSH-005)", () => {
  it("narrows rows by source when a chip is pressed", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    const list = await screen.findByRole("list", { name: "Error log" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);

    fireEvent.click(screen.getByRole("button", { name: "Tool" }));
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    expect(within(list).getByText(/file not found: a.txt/)).toBeTruthy();
    expect(within(list).queryByText(/mcp server exited/)).toBeNull();
  });

  it("links session rows to the trajectory view", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    const link = await screen.findByRole("link", { name: "Trajectory" });
    expect(link.getAttribute("href")).toBe(
      "#/sessions/s_abc?view=trajectory"
    );
  });

  it("expands an error row on click", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    const toggle = await screen.findByRole("button", {
      name: /file not found: a.txt/,
    });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("Level")).toBeTruthy();
  });
});

describe("agent filter (AC-DSH-008)", () => {
  it("lists the agents from the response next to the range switch", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    const select = await screen.findByRole("combobox", { name: "Agent" });
    const options = within(select)
      .getAllByRole("option")
      .map((option) => option.textContent);
    expect(options).toEqual(["All agents", "assistant", "lana"]);
    expect((select as HTMLSelectElement).value).toBe("");
  });

  it("refetches with the chosen agent and writes it to the hash", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);
    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("24h");
    });

    fireEvent.change(screen.getByRole("combobox", { name: "Agent" }), {
      target: { value: "lana" },
    });

    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("24h", "lana");
    });
    expect(window.location.hash).toBe("#/home?agent=lana");
  });

  it("restores the range and agent from the hash", async () => {
    window.history.replaceState(null, "", "#/home?range=7d&agent=lana");
    vi.mocked(getDashboard).mockResolvedValue(
      makeDashboard({ range: "7d", agent: "lana" })
    );
    render(<HomePage />);

    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenNthCalledWith(1, "7d", "lana");
    });
    const select = screen.getByRole("combobox", { name: "Agent" });
    expect((select as HTMLSelectElement).value).toBe("lana");
    expect(
      screen.getByRole("button", { name: "7 days" }).getAttribute("aria-pressed")
    ).toBe("true");
  });

  it("keeps an agent the response no longer lists as an option", async () => {
    window.history.replaceState(null, "", "#/home?agent=lana");
    vi.mocked(getDashboard).mockResolvedValue(
      makeDashboard({ agent: "lana", agents: ["assistant"] })
    );
    render(<HomePage />);

    const select = await screen.findByRole("combobox", { name: "Agent" });
    expect((select as HTMLSelectElement).value).toBe("lana");
    expect(within(select).getByRole("option", { name: "lana" })).toBeTruthy();
  });

  it("falls back to 24 hours for an invalid range", async () => {
    window.history.replaceState(null, "", "#/home?range=1y&agent=lana");
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("24h", "lana");
    });
  });

  it("writes a non-default range and keeps the agent", async () => {
    window.history.replaceState(null, "", "#/home?agent=lana");
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard({ agent: "lana" }));
    render(<HomePage />);
    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("24h", "lana");
    });

    fireEvent.click(screen.getByRole("button", { name: "7 days" }));
    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("7d", "lana");
    });
    expect(window.location.hash).toBe("#/home?range=7d&agent=lana");
  });

  it("omits default values from the hash", async () => {
    window.history.replaceState(null, "", "#/home?range=7d&agent=lana");
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);
    await waitFor(() => {
      expect(vi.mocked(getDashboard)).toHaveBeenCalledWith("7d", "lana");
    });

    fireEvent.change(screen.getByRole("combobox", { name: "Agent" }), {
      target: { value: "" },
    });
    await waitFor(() => {
      expect(window.location.hash).toBe("#/home?range=7d");
    });

    fireEvent.click(screen.getByRole("button", { name: "24 hours" }));
    await waitFor(() => {
      expect(window.location.hash).toBe("#/home");
    });
  });

  it("notes that daemon rows are not per agent when one is selected", async () => {
    vi.mocked(getDashboard).mockResolvedValue(makeDashboard());
    render(<HomePage />);

    expect(screen.queryByText(/Daemon logs are not per agent/)).toBeNull();
    await screen.findByRole("list", { name: "Error log" });

    fireEvent.change(screen.getByRole("combobox", { name: "Agent" }), {
      target: { value: "lana" },
    });

    expect(await screen.findByText(/Daemon logs are not per agent/)).toBeTruthy();
    const daemon = screen.getByRole("button", { name: "Daemon" });
    expect((daemon as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("sidebar (AC-DSH-006)", () => {
  it("shows Dashboard for the home item", () => {
    render(
      <Sidebar
        route={{ view: "home", section: "home", params: {}, path: "#/home" }}
        collapsed={false}
        onToggle={() => {}}
        onLogout={() => {}}
        daemon={{ state: "running", title: "Daemon running", meta: "127.0.0.1" }}
      />
    );

    const link = screen.getByRole("link", { name: "Dashboard" });
    expect(link.getAttribute("href")).toBe("#/home");
    expect(screen.queryByRole("link", { name: "Home" })).toBeNull();
  });
});

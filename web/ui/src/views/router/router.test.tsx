import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";

/* The Router page of spec 37 and the Claude Agent card of spec 38. The API
   module is mocked, so each test states the contract shapes it relies on. */
vi.mock("../../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/router")>();
  return {
    ...actual,
    getRouter: vi.fn(),
    getRouterUsage: vi.fn(),
    getRouterLimits: vi.fn(),
    getClaudeAgent: vi.fn(),
    listRouterCalls: vi.fn(),
    saveCombo: vi.fn(),
    deleteCombo: vi.fn(),
    listProxyKeys: vi.fn(),
    createProxyKey: vi.fn(),
    revokeProxyKey: vi.fn(),
    setProxyEnabled: vi.fn(),
    listProviders: vi.fn(),
    saveProvider: vi.fn(),
    deleteProvider: vi.fn(),
    testProvider: vi.fn(),
  };
});

import * as api from "../../api/router";
import type {
  ClaudeAgentStatus,
  LimitSnapshot,
  RouterOverview,
  RouterUsage,
  RouterCall,
} from "../../api/router";
import { ApiError } from "../../api/client";
import { RouterOverview as OverviewTab } from "./overview";
import { RouterCombos } from "./combos";
import { RouterLogs, PAGE_SIZE, requestedDiffers } from "./logs";
import { statsCostText } from "./router-common";
import { tickEvery } from "./usage-chart";
import { RouterProxy } from "./proxy";
import { RouterProviders } from "./providers";
import { windowText } from "./limits";

const ROUTER: RouterOverview = {
  version: "v1",
  providers: [
    { name: "deepseek", kind: "deepseek", base_url: "https://api.deepseek.com/v1", key_state: "set", used_by: ["lana"] },
    { name: "openrouter", kind: "openrouter", base_url: "https://openrouter.ai/api/v1", key_state: "set", used_by: [] },
  ],
  combos: [
    { name: "coding", description: "Claude first", models: ["anthropic/claude-sonnet-5-5", "deepseek/deepseek-flash"], used_by: ["lana"] },
  ],
  cooling: [{ target: "anthropic/claude-sonnet-5-5", until: "2026-11-08T10:05:00Z" }],
  proxy: { enabled: true, base_url: "http://127.0.0.1:18300/router/v1" },
  choices: [
    { value: "coding", label: "coding", group: "Combos" },
    { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek" },
    { value: "openrouter/openai/gpt-5-mini", label: "GPT-5 mini", group: "openrouter" },
  ],
};

const STATS = {
  calls: 0,
  errors: 0,
  input_tokens: 0,
  output_tokens: 0,
  cached_tokens: 0,
  cost_usd: 0,
  cost_partial: false,
  ttft_p50_ms: 0,
  ttft_p95_ms: 0,
};

const USAGE: RouterUsage = {
  range: "24h",
  totals: {
    calls: 120,
    errors: 3,
    input_tokens: 50000,
    output_tokens: 8000,
    cached_tokens: 20000,
    cost_usd: 1.23,
    cost_partial: true,
    ttft_p50_ms: 900,
    ttft_p95_ms: 2400,
  },
  series: [
    { t: "2026-11-08T00:00:00Z", input_tokens: 30000, output_tokens: 5000, cached_tokens: 10000, cost_usd: 0.8, calls: 70, errors: 1 },
    { t: "2026-11-08T01:00:00Z", input_tokens: 20000, output_tokens: 3000, cached_tokens: 10000, cost_usd: 0.43, calls: 50, errors: 2 },
  ],
  by_provider: [
    { ...STATS, provider: "deepseek", calls: 100, cost_usd: 1.23, ttft_p50_ms: 800 },
    { ...STATS, provider: "local", calls: 20, cost_usd: 0, cost_partial: true },
  ],
  by_model: [
    { ...STATS, address: "deepseek/deepseek-flash", calls: 100, cost_usd: 1.23 },
    { ...STATS, address: "local/llama-9", calls: 20, cost_usd: 0, cost_partial: true },
    { ...STATS, address: "down/model-x", calls: 3, errors: 3, cost_usd: 0 },
  ],
  by_source: [{ source: "turn", calls: 120, cost_usd: 1.23 }],
};

const LIMITS: LimitSnapshot[] = [
  {
    provider: "openai",
    kind: "openai",
    updated_at: new Date().toISOString(),
    error: "",
    rate: {
      requests: { limit: 500, remaining: 499 },
      tokens: { limit: 150000, remaining: 149000, reset_at: "2026-11-08T10:05:00Z" },
      input_tokens: null,
      output_tokens: null,
    },
    credit: null,
    balance: null,
    windows: [],
  },
  { provider: "openrouter", kind: "openrouter", credit: { limit_usd: 10, used_usd: 2.5, remaining_usd: 7.5 } },
  { provider: "deepseek", kind: "deepseek", balance: { currency: "USD", total: 4.2, granted: 0, topped_up: 4.2 } },
  { provider: "local", kind: "openai_compat", rate: null, credit: null, balance: null, windows: [] },
  {
    provider: "claude-agent",
    kind: "claude-code",
    windows: [{ type: "five_hour", status: "allowed", utilization: 0.42, resets_at: "2026-11-08T10:05:00Z" }],
  },
];

const CLAUDE: ClaudeAgentStatus = {
  configured_agents: ["claude"],
  binary: "/Users/x/.local/bin/claude",
  version: "2.1.319",
  version_ok: true,
  logged_in: false,
  auth_method: "",
  error: "",
  running: 1,
  queued: 0,
  max_concurrent: 2,
};

function call(id: number, overrides: Partial<RouterCall> = {}): RouterCall {
  return {
    id,
    at: "2026-11-08T09:00:00Z",
    source: "turn",
    agent: "lana",
    session_id: "s1",
    requested: "coding",
    provider: "deepseek",
    model: "deepseek-flash",
    attempt: 1,
    outcome: "ok",
    input_tokens: 10,
    output_tokens: 5,
    cached_tokens: 0,
    cost_usd: 0.001,
    ttft_ms: 900,
    duration_ms: 3000,
    error: "",
    ...overrides,
  };
}

beforeEach(() => {
  vi.mocked(api.getRouter).mockResolvedValue(ROUTER);
  vi.mocked(api.getRouterUsage).mockResolvedValue(USAGE);
  vi.mocked(api.getRouterLimits).mockResolvedValue(LIMITS);
  vi.mocked(api.getClaudeAgent).mockResolvedValue(CLAUDE);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("router overview", () => {
  /* AC-RTR-014: an unknown price shows "—" and the total says partial. */
  it("renders the totals, the partial cost marker, and — for an unknown cost", async () => {
    render(<OverviewTab />);
    const totals = await screen.findByLabelText("Totals");
    expect(within(totals).getByText("120")).toBeTruthy();
    expect(within(totals).getByText(/Error rate 3%/)).toBeTruthy();
    expect(within(totals).getByText("$1.23")).toBeTruthy();
    expect(within(totals).getByText("partial")).toBeTruthy();
    expect(within(totals).getByText("900 ms")).toBeTruthy();

    const byModel = screen.getByText("local/llama-9").closest("tr") as HTMLElement;
    const cost = within(byModel).getByText("partial").closest(".rt-cost") as HTMLElement;
    expect(cost.textContent).toBe("—partial");
    const deepseek = screen.getByText("deepseek/deepseek-flash").closest("tr") as HTMLElement;
    expect(within(deepseek).getByText("$1.23")).toBeTruthy();
    /* Every call failed: no price was ever known, so no "$0.00". */
    const down = screen.getByText("down/model-x").closest("tr") as HTMLElement;
    expect((down.querySelector(".rt-cost") as HTMLElement).textContent).toBe("—");
  });

  it("shows — for the cost of failed-only rows and the amount otherwise", () => {
    expect(statsCostText({ calls: 3, errors: 3, cost_usd: 0 })).toBe("—");
    expect(statsCostText({ calls: 3, errors: 3, cost_usd: 0.002 })).toBe("$0.002");
    expect(statsCostText({ calls: 3, errors: 1, cost_usd: 0 })).toBe("$0.00");
    expect(statsCostText({ calls: 3, errors: 0, cost_usd: 0, cost_partial: true })).toBe("—");
    expect(statsCostText({ calls: 0, errors: 0, cost_usd: 0 })).toBe("$0.00");
  });

  it("puts fewer time ticks on a narrow chart", () => {
    expect(tickEvery(24, 708)).toBe(3);
    expect(tickEvery(24, 300)).toBe(5);
    expect(tickEvery(4, 300)).toBe(1);
    expect(tickEvery(30, 100)).toBe(15);
  });

  it("loads the chosen range", async () => {
    render(<OverviewTab />);
    await screen.findByLabelText("Totals");
    fireEvent.click(screen.getByRole("tab", { name: "7 days" }));
    await waitFor(() => {
      expect(vi.mocked(api.getRouterUsage)).toHaveBeenLastCalledWith("7d");
    });
  });

  /* AC-RTR-016, AC-RTR-017, and the empty card of REQ-RTR-012. */
  it("renders the limit cards with rate, credit, balance, and nothing reported", async () => {
    render(<OverviewTab />);
    const openai = await screen.findByLabelText("Limits of openai");
    expect(within(openai).getByText(/149,000 of 150,000 tokens left · resets/)).toBeTruthy();
    expect(within(openai).getByText("499 of 500 requests left")).toBeTruthy();
    expect(within(screen.getByLabelText("Limits of openrouter")).getByText("$7.50 of $10 left")).toBeTruthy();
    expect(within(screen.getByLabelText("Limits of deepseek")).getByText("$4.20 balance")).toBeTruthy();
    expect(within(screen.getByLabelText("Limits of local")).getByText("Not reported by this provider")).toBeTruthy();
  });

  it("refreshes the limits with refresh=1", async () => {
    render(<OverviewTab />);
    await screen.findByLabelText("Limits of openai");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => {
      expect(vi.mocked(api.getRouterLimits)).toHaveBeenLastCalledWith(true);
    });
  });

  /* AC-CCR-009 and REQ-CCR-008: the plan window line, the login hint, and
     never a login button. */
  it("renders the Claude Agent card", async () => {
    render(<OverviewTab />);
    const card = await screen.findByLabelText("Claude Agent");
    expect(within(card).getByText(/^5-hour window · 42 % used · resets /)).toBeTruthy();
    expect(within(card).getByText("Not logged in")).toBeTruthy();
    expect(card.textContent).toContain("Run claude, then /login.");
    expect(within(card).getByText("1 running · 0 queued of 2 at once")).toBeTruthy();
    expect(within(card).queryByRole("button")).toBeNull();
    expect(screen.queryByText(/Login with Claude/i)).toBeNull();
  });

  it("lists the cooling-down targets", async () => {
    render(<OverviewTab />);
    expect(await screen.findByText("anthropic/claude-sonnet-5-5")).toBeTruthy();
  });
});

describe("limit text", () => {
  it("names the known windows and humanizes the others", () => {
    expect(windowText({ type: "seven_day", status: "allowed", utilization: 0.1 })).toBe(
      "Weekly window · 10 % used"
    );
    expect(windowText({ type: "seven_day_opus", status: "allowed", utilization: 0.5 })).toBe(
      "Seven day opus window · 50 % used"
    );
  });

  it("says usage not reported when utilization is null", () => {
    expect(windowText({ type: "five_hour", status: "allowed", utilization: null })).toBe(
      "5-hour window · usage not reported"
    );
    expect(windowText({ type: "five_hour", status: "rejected", utilization: null })).toBe(
      "5-hour window · limit reached"
    );
    expect(windowText({ type: "five_hour", status: "allowed", utilization: 0 })).toBe(
      "5-hour window · 0 % used"
    );
  });
});

describe("router combos", () => {
  /* AC-RTR-019: a combo in use cannot be deleted; the error names the users. */
  it("shows the in-use refusal when deleting a used combo", async () => {
    vi.mocked(api.deleteCombo).mockRejectedValue(
      new ApiError(409, "in_use", "used by agents: lana")
    );
    render(<RouterCombos />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit coding" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete combo" }));
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getAllByRole("button", { name: "Delete combo" }).at(-1) as HTMLElement);
    expect(await screen.findByText("Cannot delete coding: used by agents: lana.")).toBeTruthy();
    expect(vi.mocked(api.deleteCombo)).toHaveBeenCalledWith("coding", "v1");
  });

  it("saves the reordered models and shows a 422 issue on its row", async () => {
    vi.mocked(api.saveCombo).mockRejectedValue(
      new ApiError(422, "invalid", "invalid combo", [
        { path: "combos.coding.models[0]", message: "unknown provider" },
      ])
    );
    render(<RouterCombos />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit coding" }));
    fireEvent.click(screen.getByRole("button", { name: "Move deepseek/deepseek-flash up" }));
    fireEvent.click(screen.getByRole("button", { name: "Save combo" }));
    await waitFor(() => {
      expect(vi.mocked(api.saveCombo)).toHaveBeenCalledWith("coding", {
        description: "Claude first",
        models: ["deepseek/deepseek-flash", "anthropic/claude-sonnet-5-5"],
        expected_version: "v1",
      });
    });
    const first = (await screen.findByText("unknown provider")).closest("li") as HTMLElement;
    expect(within(first).getByText("deepseek/deepseek-flash")).toBeTruthy();
  });
});

describe("router logs", () => {
  it("loads the next page with the exclusive before cursor", async () => {
    vi.mocked(api.listRouterCalls)
      .mockResolvedValueOnce({ calls: [call(130), call(129, { outcome: "rate_limit", error: "429 from upstream" })], next_before: 129 })
      .mockResolvedValueOnce({ calls: [call(128)], next_before: 0 });
    render(<RouterLogs />);
    expect(await screen.findByText("Rate limit")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => {
      expect(vi.mocked(api.listRouterCalls)).toHaveBeenLastCalledWith({
        limit: PAGE_SIZE,
        provider: "",
        status: "",
        source: "",
        before: 129,
      });
    });
    expect(await screen.findByText("That is every call in the last 30 days.")).toBeTruthy();
    expect(screen.getAllByRole("row")).toHaveLength(4);
  });

  it("shows the route once when the requested name is the served target", async () => {
    vi.mocked(api.listRouterCalls).mockResolvedValue({
      calls: [
        call(131, { requested: "claude-agent/claude-haiku-5-5", provider: "claude-agent", model: "claude-haiku-5-5" }),
        call(130, { requested: "duo", provider: "ds", model: "deepseek-flash" }),
      ],
      next_before: 0,
    });
    render(<RouterLogs />);
    const same = (await screen.findByRole("row", { name: /Call 131/ })).querySelector(".rt-route") as HTMLElement;
    expect(same.textContent).toBe("claude-agent/claude-haiku-5-5");
    const combo = screen.getByRole("row", { name: /Call 130/ }).querySelector(".rt-route") as HTMLElement;
    expect(combo.textContent).toBe("duods/deepseek-flash");
    expect(requestedDiffers(" DS/deepseek-flash ", "ds/deepseek-flash")).toBe(false);
    expect(requestedDiffers("", "ds/deepseek-flash")).toBe(false);
  });

  it("filters by outcome and opens the redacted error", async () => {
    vi.mocked(api.listRouterCalls).mockResolvedValue({
      calls: [call(129, { outcome: "auth", error: "401 invalid key sk-[redacted]" })],
      next_before: 0,
    });
    render(<RouterLogs />);
    fireEvent.click(await screen.findByRole("tab", { name: "Errors" }));
    await waitFor(() => {
      expect(vi.mocked(api.listRouterCalls)).toHaveBeenLastCalledWith(
        expect.objectContaining({ status: "error" })
      );
    });
    fireEvent.click(screen.getByRole("row", { name: /Call 129/ }));
    expect(await screen.findByText("401 invalid key sk-[redacted]")).toBeTruthy();
  });
});

describe("router proxy", () => {
  it("shows a new key once with a warning", async () => {
    vi.mocked(api.listProxyKeys).mockResolvedValue([]);
    vi.mocked(api.createProxyKey).mockResolvedValue({
      id: "k_1",
      name: "cursor",
      prefix: "lrk_ab12",
      key: "lrk_ab12SECRETSECRET",
    });
    render(<RouterProxy />);
    expect(await screen.findByText("http://127.0.0.1:18300/router/v1")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "cursor" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Create key" }).at(-1) as HTMLElement);
    expect(await screen.findByText("lrk_ab12SECRETSECRET")).toBeTruthy();
    expect(screen.getByText(/It is shown only once/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByText("lrk_ab12SECRETSECRET")).toBeNull();
  });

  it("turns the proxy off with the config version", async () => {
    vi.mocked(api.listProxyKeys).mockResolvedValue([]);
    vi.mocked(api.setProxyEnabled).mockResolvedValue({ version: "v2", restart_required: false });
    render(<RouterProxy />);
    const toggle = await screen.findByRole("checkbox", { name: "Serve the proxy" });
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(true));
    fireEvent.click(toggle);
    await waitFor(() => {
      expect(vi.mocked(api.setProxyEnabled)).toHaveBeenCalledWith(false, "v1");
    });
  });
});

describe("router providers", () => {
  it("offers the presets and saves a preset without a base URL", async () => {
    vi.mocked(api.listProviders).mockResolvedValue({ version: "v1", providers: [], restart_required: false });
    vi.mocked(api.saveProvider).mockResolvedValue({ name: "deepseek", version: "v2", restart_required: false });
    render(<RouterProviders />);
    const add = await screen.findByRole("button", { name: /Add provider/ });
    await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(add);

    fireEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    const options = screen.getAllByRole("option").map((node) => node.textContent);
    expect(options).toEqual(["OpenAI", "DeepSeek", "Anthropic", "OpenRouter", "Custom (OpenAI-compatible)"]);
    fireEvent.click(screen.getByRole("option", { name: "DeepSeek" }));

    const base = screen.getByLabelText(/Base URL/) as HTMLInputElement;
    expect(base.placeholder).toBe("https://api.deepseek.com/v1");
    fireEvent.click(screen.getByRole("button", { name: "Save provider" }));
    await waitFor(() => {
      expect(vi.mocked(api.saveProvider)).toHaveBeenCalledWith("deepseek", {
        kind: "deepseek",
        api_key_env: "DEEPSEEK_API_KEY",
        expected_version: "v1",
      });
    });
    expect(await screen.findByText("Provider deepseek saved. It applies now.")).toBeTruthy();
    expect(screen.queryByText(/Restart the daemon/)).toBeNull();
  });

  it("shows who uses a provider when the delete is refused", async () => {
    vi.mocked(api.listProviders).mockResolvedValue({
      version: "v1",
      restart_required: false,
      providers: [
        {
          name: "deepseek",
          kind: "deepseek",
          base_url: "https://api.deepseek.com/v1",
          default_base_url: "https://api.deepseek.com/v1",
          api_key_env: "DEEPSEEK_API_KEY",
          key_state: "set",
          used_by: ["agents.lana"],
        },
      ],
    });
    vi.mocked(api.deleteProvider).mockRejectedValue(
      new ApiError(409, "in_use", "used by agents: lana; combos: coding")
    );
    render(<RouterProviders />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit deepseek" }));
    expect((screen.getByLabelText(/Base URL/) as HTMLInputElement).value).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "Delete provider" }));
    fireEvent.click(screen.getAllByRole("button", { name: "Delete provider" }).at(-1) as HTMLElement);
    expect(
      await screen.findByText("Cannot delete deepseek: used by agents: lana; combos: coding.")
    ).toBeTruthy();
  });

  /* API-RTR-006: the dialog sends no guessed test model; the daemon picks a
     model it knows unless the owner typed one. */
  it("tests a provider without a model unless one is typed", async () => {
    vi.mocked(api.listProviders).mockResolvedValue({ version: "v1", providers: [], restart_required: false });
    vi.mocked(api.testProvider).mockResolvedValue({ ok: true, latency_ms: 40 });
    render(<RouterProviders />);
    const add = await screen.findByRole("button", { name: /Add provider/ });
    await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(add);
    fireEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    fireEvent.click(screen.getByRole("option", { name: "Anthropic" }));

    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    await waitFor(() => {
      expect(vi.mocked(api.testProvider)).toHaveBeenCalledWith({
        name: "anthropic",
        kind: "anthropic",
        base_url: "https://api.anthropic.com",
        api_key_env: "ANTHROPIC_API_KEY",
      });
    });
    expect(await screen.findByText("Connected · 40 ms.")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Test model"), { target: { value: " claude-opus-5 " } });
    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    await waitFor(() => {
      expect(vi.mocked(api.testProvider)).toHaveBeenLastCalledWith(
        expect.objectContaining({ kind: "anthropic", model: "claude-opus-5" })
      );
    });
  });
});

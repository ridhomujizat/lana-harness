import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

/* The Router page of spec 37 and the Claude Agent card of spec 38. The API
   module is mocked, so each test states the contract shapes it relies on. */
vi.mock("../../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/router")>();
  return {
    ...actual,
    getRouter: vi.fn(),
    getRouterUsage: vi.fn(),
    getRouterLimits: vi.fn(),
    refreshRouterLimits: vi.fn(),
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
    testRouterModel: vi.fn(),
    listRouterModels: vi.fn(),
    saveClaudeCodeModels: vi.fn(),
    createClaudeAccount: vi.fn(),
    startClaudeSignIn: vi.fn(),
    getClaudeSignIn: vi.fn(),
    cancelClaudeSignIn: vi.fn(),
    signOutClaudeAccount: vi.fn(),
    setClaudeAccountLabel: vi.fn(),
  };
});

import * as api from "../../api/router";
import type {
  ClaudeAccount,
  ClaudeAgentStatus,
  LimitSnapshot,
  RouterOverview,
  RouterUsage,
  RouterCall,
} from "../../api/router";
import { ApiError } from "../../api/client";
import { RouterUsagePage as OverviewTab } from "./overview";
import { RouterQuota } from "./quota";
import { RouterCombos } from "./combos";
import { RouterLogs, PAGE_SIZE, requestedDiffers } from "./logs";
import { statsCostText } from "./router-common";
import { UsageChart, bucketText, niceMax, tickEvery } from "./usage-chart";
import { RouterProxy } from "./proxy";
import { RouterProviders } from "./providers";
import { ProviderDetail } from "./provider-detail";
import { ProviderLogo } from "./provider-logo";
import { monogram, presetFor } from "./presets";
import { resetsIn } from "./router-common";
import { windowText } from "./limits";
import { claudeLimitsStale } from "./limits-refresh";
import { SIGN_IN_HELP, SIGN_IN_POLL_MS } from "./claude-code-page";

const RUNNING: api.ClaudeSignIn = { state: "running", error: "", started_at: "2026-11-08T10:00:00Z", finished_at: null };

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

function account(overrides: Partial<ClaudeAccount> & { name: string }): ClaudeAccount {
  return {
    builtin: false,
    config_dir: "",
    binary: "/Users/x/.local/bin/claude",
    version: "2.1.319",
    version_ok: true,
    logged_in: true,
    email: "",
    plan: "",
    auth_method: "claude.ai",
    error: "",
    running: 0,
    queued: 0,
    login_command: "",
    windows: [],
    agents: [],
    combos: [],
    label: "",
    display_name: "",
    sign_in: { state: "idle", error: "", started_at: null, finished_at: null },
    ...overrides,
  };
}

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
  accounts: [
    account({
      name: "claude-code",
      builtin: true,
      logged_in: false,
      running: 1,
      login_command: "claude",
      agents: ["claude"],
    }),
  ],
};

/* AC-CCT-013: two logged-in accounts, each with its own email, plan,
   windows, and users. */
const TWO_ACCOUNTS: ClaudeAgentStatus = {
  ...CLAUDE,
  logged_in: true,
  accounts: [
    account({
      name: "claude-code",
      builtin: true,
      email: "me@home.dev",
      plan: "max",
      config_dir: "/Users/x/.claude",
      windows: [
        { type: "five_hour", status: "allowed", utilization: 0.42, resets_at: "2026-11-08T10:05:00Z" },
        { type: "seven_day", status: "allowed", utilization: 0.1, resets_at: "2026-11-12T10:00:00Z" },
      ],
      agents: ["claude"],
    }),
    account({
      name: "claude-work",
      email: "me@work.dev",
      plan: "team",
      config_dir: "/Users/x/.lana/claude/claude-work",
      windows: [
        { type: "five_hour", status: "allowed", utilization: 0.8, resets_at: "2026-11-08T11:00:00Z" },
        { type: "seven_day", status: "allowed", utilization: 0.55, resets_at: "2026-11-13T10:00:00Z" },
      ],
      combos: ["work"],
    }),
  ],
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
  vi.mocked(api.refreshRouterLimits).mockResolvedValue(LIMITS);
  vi.mocked(api.getClaudeAgent).mockResolvedValue(CLAUDE);
  vi.mocked(api.listRouterModels).mockResolvedValue([]);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("router usage", () => {
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

  it("rounds the axis maximum so the mid tick is round too", () => {
    expect(niceMax(157000)).toBe(200000);
    expect(niceMax(0.015)).toBe(0.02);
    expect(niceMax(3500)).toBe(4000);
    expect(niceMax(0)).toBe(1);
  });

  describe("tokens and cost chart", () => {
    const SERIES = [
      { t: "2026-11-08T00:00:00Z", input_tokens: 30000, output_tokens: 5000, cached_tokens: 10000, cost_usd: 0.8, calls: 70, errors: 1 },
      { t: "2026-11-08T01:00:00Z", input_tokens: 0, output_tokens: 0, cached_tokens: 0, cost_usd: 0, calls: 0, errors: 0 },
      { t: "2026-11-08T02:00:00Z", input_tokens: 2000, output_tokens: 0, cached_tokens: 0, cost_usd: 0, calls: 2, errors: 2 },
    ];
    const at = (iso: string) => bucketText(Date.parse(iso), "24h");
    const [T0, T1] = ["2026-11-08T00:00:00Z", "2026-11-08T01:00:00Z"];
    const sum = (container: HTMLElement) =>
      (container.querySelector(".rt-chart-sum") as HTMLElement).textContent;

    it("labels both panels and shows the range totals", () => {
      const { container } = render(<UsageChart series={SERIES} range="24h" />);
      expect(sum(container)).toBe("37k tokens$0.8072 calls");
      const tokens = screen.getByRole("region", { name: "Tokens" });
      expect(within(tokens).getByText("Tokens")).toBeTruthy();
      expect(within(tokens).getByText("Input")).toBeTruthy();
      expect(within(tokens).getByText("Cached")).toBeTruthy();
      expect(within(tokens).getByText("Output")).toBeTruthy();
      const cost = screen.getByRole("region", { name: "Cost (USD)" });
      expect(within(cost).getByText("Cost (USD)")).toBeTruthy();
      /* The time axis is only under the cost panel. */
      expect(within(tokens).queryByText(at(T0))).toBeNull();
      expect(within(cost).getByText(at(T0))).toBeTruthy();
      /* Same corner treatment for every column; empty buckets draw nothing. */
      expect(tokens.querySelectorAll(".rt-col")).toHaveLength(2);
      expect(cost.querySelectorAll(".rt-cost-bar")).toHaveLength(1);
      expect(screen.getByText("Show as a table")).toBeTruthy();
      expect(screen.queryByTestId("usage-tip")).toBeNull();
    });

    it("keeps the totals on hover and shows the bucket in a tooltip", () => {
      const { container } = render(<UsageChart series={SERIES} range="24h" />);
      const plot = screen.getByRole("group", { name: /Tokens and cost over time/ });
      vi.spyOn(plot, "getBoundingClientRect").mockReturnValue({
        x: 0, y: 0, left: 0, top: 0, right: 760, bottom: 300, width: 760, height: 300, toJSON: () => ({}),
      } as DOMRect);
      const before = sum(container);
      /* The first bucket sits just right of the y labels. */
      fireEvent.pointerMove(plot, { clientX: 60, clientY: 40 });
      const tip = screen.getByTestId("usage-tip");
      expect(tip.textContent).toContain(at(T0) + "–" + at(T1));
      expect(tip.textContent).toContain("20kInput");
      expect(tip.textContent).toContain("10kCached");
      expect(tip.textContent).toContain("5kOutput");
      expect(tip.textContent).toContain("$0.80Cost");
      expect(tip.textContent).toContain("70 calls · 1 failed");
      expect(sum(container)).toBe(before);
      expect(container.querySelectorAll(".rt-guide")).toHaveLength(2);

      fireEvent.pointerLeave(plot);
      expect(screen.queryByTestId("usage-tip")).toBeNull();
      expect(container.querySelectorAll(".rt-guide")).toHaveLength(0);
      expect(sum(container)).toBe(before);
    });

    it("reads the buckets with the keyboard, including No calls", () => {
      const { container } = render(<UsageChart series={SERIES} range="24h" />);
      const plot = screen.getByRole("group", { name: /Tokens and cost over time/ });
      const before = sum(container);
      plot.focus();
      fireEvent.keyDown(plot, { key: "ArrowRight" });
      fireEvent.keyDown(plot, { key: "ArrowRight" });
      const tip = screen.getByTestId("usage-tip");
      expect(tip.textContent).toContain(at(T1));
      expect(tip.textContent).toContain("No calls");
      expect(tip.textContent).not.toMatch(/Input|Cost/);
      expect(screen.getByText(/, No calls$/)).toBeTruthy();

      /* Failed-only calls: no price, so "—" and never "$0.00". */
      fireEvent.keyDown(plot, { key: "End" });
      expect(tip.textContent).toContain("2kInput");
      expect(tip.textContent).toContain("—Cost");
      expect(tip.textContent).toContain("2 calls · 2 failed");
      expect(tip.textContent).not.toContain("$0.00");
      expect(sum(container)).toBe(before);

      fireEvent.keyDown(plot, { key: "Escape" });
      expect(screen.queryByTestId("usage-tip")).toBeNull();
    });
  });

  it("loads the chosen range", async () => {
    render(<OverviewTab />);
    await screen.findByLabelText("Totals");
    fireEvent.click(screen.getByRole("tab", { name: "7 days" }));
    await waitFor(() => {
      expect(vi.mocked(api.getRouterUsage)).toHaveBeenLastCalledWith("7d");
    });
  });

  it("lists the cooling-down targets", async () => {
    render(<OverviewTab />);
    expect(await screen.findByText("anthropic/claude-sonnet-5-5")).toBeTruthy();
  });

  it("leaves the limits to the Quota page", async () => {
    render(<OverviewTab />);
    await screen.findByLabelText("Totals");
    expect(screen.queryByLabelText("Limits of openai")).toBeNull();
    expect(vi.mocked(api.getRouterLimits)).not.toHaveBeenCalled();
  });
});

describe("router quota", () => {
  /* AC-RTR-016, AC-RTR-017, and the empty card of REQ-RTR-012. */
  it("renders the limit cards with rate, credit, balance, and nothing reported", async () => {
    render(<RouterQuota />);
    const openai = await screen.findByLabelText("Limits of openai");
    expect(within(openai).getByText(/149,000 of 150,000 tokens left · resets/)).toBeTruthy();
    expect(within(openai).getByText("499 of 500 requests left")).toBeTruthy();
    expect(within(screen.getByLabelText("Limits of openrouter")).getByText("$7.50 of $10 left")).toBeTruthy();
    expect(within(screen.getByLabelText("Limits of deepseek")).getByText("$4.20 balance")).toBeTruthy();
    expect(within(screen.getByLabelText("Limits of local")).getByText("Not reported by this provider")).toBeTruthy();
  });

  /* Spec 39 Revision 4: Refresh posts /v1/router/limits/refresh. */
  it("refreshes the limits and the Claude Code plans", async () => {
    render(<RouterQuota />);
    await screen.findByLabelText("Limits of openai");
    const before = vi.mocked(api.refreshRouterLimits).mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => {
      expect(vi.mocked(api.refreshRouterLimits).mock.calls.length).toBe(before + 1);
    });
    expect((await screen.findAllByText(/^Updated /)).length).toBeGreaterThan(0);
  });

  /* AC-CCR-009 and REQ-CCT-010: the plan window from the old claude-agent
     snapshot, the login command, and never a login button. */
  it("renders the Claude Code plan card", async () => {
    render(<RouterQuota />);
    const card = await screen.findByLabelText("Claude Code plan of claude-code");
    expect(within(card).getByText(/^42 % used · resets /)).toBeTruthy();
    expect(within(card).getByText("Not logged in")).toBeTruthy();
    expect(card.textContent).toContain("Log in once in a terminal:");
    expect(card.textContent).toContain("then type /login.");
    expect(within(card).getByText("1 running · 0 queued")).toBeTruthy();
    expect(within(card).queryByRole("button", { name: /^log ?in/i })).toBeNull();
    expect(screen.queryByText(/Login with Claude/i)).toBeNull();
  });

  /* REQ-CCT-011: one Claude Code card per account with email, plan, and its
     own windows. */
  it("shows one Claude Code card per account", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    render(<RouterQuota />);
    const home = await screen.findByLabelText("Claude Code plan of claude-code");
    const work = screen.getByLabelText("Claude Code plan of claude-work");
    expect(within(home).getByText("me@home.dev")).toBeTruthy();
    expect(within(home).getByText("Max")).toBeTruthy();
    expect(within(home).getByText(/^42 % used/)).toBeTruthy();
    expect(within(work).getByText("me@work.dev")).toBeTruthy();
    expect(within(work).getByText("Team")).toBeTruthy();
    expect(within(work).getByText(/^80 % used/)).toBeTruthy();
    expect(within(work).getByText(/^55 % used/)).toBeTruthy();
    expect(screen.queryByLabelText("Limits of claude-agent")).toBeNull();
  });

  it("counts down to the reset", () => {
    const now = Date.parse("2026-11-08T10:00:00Z");
    expect(resetsIn("2026-11-08T10:03:00Z", now)).toBe("resets in 3m");
    expect(resetsIn("2026-11-08T11:32:30Z", now)).toBe("resets in 1h 32m");
    expect(resetsIn("2026-11-10T14:00:00Z", now)).toBe("resets in 2d 4h");
    expect(resetsIn("2026-11-08T09:00:00Z", now)).toBe("resets now");
    expect(resetsIn("", now)).toBe("");
  });

  it("shows provider logos on the cards", async () => {
    render(<RouterQuota />);
    const openai = await screen.findByLabelText("Limits of openai");
    expect(openai.querySelector('[data-logo="openai"]')).toBeTruthy();
    const local = screen.getByLabelText("Limits of local");
    expect(local.querySelector("[data-monogram]")).toBeTruthy();
    expect((await screen.findByLabelText("Claude Code plan of claude-code")).querySelector('[data-logo="claude"]')).toBeTruthy();
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


/* Router Providers revision 2: 9router-style cards per preset, a provider
   page per card, connections with an enable switch. */
function row(overrides: Partial<api.ProviderRow> & { name: string; kind: string }): api.ProviderRow {
  return {
    base_url: "",
    api_key_env: overrides.name.toUpperCase().replace(/-/g, "_") + "_API_KEY",
    key_state: "set",
    used_by: [],
    ...overrides,
  };
}

function providersResponse(rows: api.ProviderRow[]): api.ListProvidersResponse {
  return { version: "v1", providers: rows, restart_required: false };
}

function card(name: string): HTMLElement {
  return screen.getByRole("article", { name });
}

describe("router providers grid", () => {
  it("shows every preset with No connections when nothing is configured", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(providersResponse([]));
    render(<RouterProviders />);
    await screen.findByRole("article", { name: "OpenAI" });
    for (const name of [
      "OpenAI",
      "Anthropic",
      "DeepSeek",
      "OpenRouter",
      "Google Gemini",
      "xAI Grok",
      "Groq",
      "Mistral",
      "Together AI",
      "Fireworks AI",
      "Cerebras",
    ]) {
      expect(within(card(name)).getByText("No connections")).toBeTruthy();
    }
    expect(card("OpenAI").querySelector('[data-logo="openai"]')).toBeTruthy();
    expect(card("Google Gemini").querySelector('[data-logo="gemini"]')).toBeTruthy();
    expect(within(card("Groq")).getByRole("link").getAttribute("href")).toBe("#/router/providers/groq");
    expect(screen.getByRole("button", { name: "Add OpenAI Compatible" })).toBeTruthy();
  });

  it("counts connections by kind and by compat host, and flags keys and switches", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([
        row({ name: "ds", kind: "deepseek" }),
        row({ name: "deepseek", kind: "openai_compat", base_url: "https://api.deepseek.com/v1" }),
        row({ name: "gemini", kind: "openai_compat", base_url: "https://generativelanguage.googleapis.com/v1beta/openai" }),
        row({ name: "groq", kind: "openai_compat", base_url: "https://api.groq.com/openai/v1", key_state: "unset" }),
        row({ name: "openai", kind: "openai", disabled: true }),
        row({ name: "local", kind: "openai_compat", base_url: "http://127.0.0.1:11434/v1", api_key_env: "" }),
      ])
    );
    render(<RouterProviders />);
    await waitFor(() => expect(within(card("DeepSeek")).getByText("2 Connected")).toBeTruthy());
    expect(within(card("Google Gemini")).getByText("1 Connected")).toBeTruthy();
    expect(within(card("Groq")).getByText("Missing key")).toBeTruthy();
    expect(within(card("OpenAI")).getByText("Off")).toBeTruthy();
    expect(within(card("Anthropic")).getByText("No connections")).toBeTruthy();
    const local = card("local");
    expect(within(local).getByText("1 Connected")).toBeTruthy();
    expect(local.querySelector("[data-monogram]")?.textContent).toBe("LO");
    expect(within(local).getByRole("link").getAttribute("href")).toBe("#/router/providers/local");
  });

  it("tests one model of each connected card with Test All", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([
        row({ name: "deepseek", kind: "deepseek" }),
        row({ name: "openrouter", kind: "openrouter" }),
      ])
    );
    vi.mocked(api.testRouterModel).mockImplementation(async (address: string) =>
      address.startsWith("deepseek/")
        ? { ok: true, latency_ms: 320 }
        : { ok: false, class: "auth", status: 401, message: "bad key" }
    );
    render(<RouterProviders />);
    await waitFor(() => expect(within(card("DeepSeek")).getByText("1 Connected")).toBeTruthy());
    const section = screen.getByRole("heading", { name: "API Key Providers" }).closest("section") as HTMLElement;
    fireEvent.click(within(section).getByRole("button", { name: "Test All" }));
    expect(await within(card("DeepSeek")).findByText(/OK · 320 ms/)).toBeTruthy();
    expect(await within(card("OpenRouter")).findByText(/Failed: auth · HTTP 401 · bad key/)).toBeTruthy();
    expect(vi.mocked(api.testRouterModel)).toHaveBeenCalledWith("deepseek/deepseek-flash");
    expect(vi.mocked(api.testRouterModel)).toHaveBeenCalledWith("openrouter/openai/gpt-5-mini");
    expect(vi.mocked(api.testRouterModel)).toHaveBeenCalledTimes(2);
    expect(within(card("OpenAI")).queryByRole("status")).toBeNull();
  });

  it("shows the Claude Code states", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(providersResponse([]));
    vi.mocked(api.getClaudeAgent).mockResolvedValueOnce({
      ...CLAUDE,
      binary: "",
      accounts: CLAUDE.accounts.map((entry) => ({ ...entry, binary: "" })),
    });
    const first = render(<RouterProviders />);
    expect(await within(card("Claude Code")).findByText("Not installed")).toBeTruthy();
    expect(card("Claude Code").querySelector('[data-logo="claude"]')).toBeTruthy();
    first.unmount();

    vi.mocked(api.getClaudeAgent).mockResolvedValueOnce(CLAUDE);
    const second = render(<RouterProviders />);
    expect(await within(card("Claude Code")).findByText("1 account · 0 logged in")).toBeTruthy();
    second.unmount();

    vi.mocked(api.getClaudeAgent).mockResolvedValueOnce(TWO_ACCOUNTS);
    render(<RouterProviders />);
    expect(await within(card("Claude Code")).findByText("2 accounts · 2 logged in")).toBeTruthy();
    expect(within(card("Claude Code")).getByText("me@home.dev")).toBeTruthy();
    expect(within(card("Claude Code")).getByRole("link").getAttribute("href")).toBe("#/router/providers/claude-code");
  });

  it("keeps Claude Code accounts out of the compatible providers", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([row({ name: "claude-work", kind: "claude-code", api_key_env: "" })])
    );
    render(<RouterProviders />);
    await screen.findByRole("article", { name: "Claude Code" });
    expect(screen.queryByRole("article", { name: "claude-work" })).toBeNull();
  });
});

describe("router provider page", () => {
  it("adds a connection to a compat preset with its kind, base URL, and key variable", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(providersResponse([]));
    vi.mocked(api.saveProvider).mockResolvedValue({ name: "groq", version: "v2", restart_required: false });
    render(<ProviderDetail id="groq" />);
    fireEvent.click(await screen.findByRole("button", { name: "Connect Groq" }));
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("API key"), { target: { value: " gsk-test " } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(vi.mocked(api.saveProvider)).toHaveBeenCalledWith("groq", {
        kind: "openai_compat",
        base_url: "https://api.groq.com/openai/v1",
        api_key_env: "GROQ_API_KEY",
        api_key: "gsk-test",
        expected_version: "v1",
      });
    });
    expect(await screen.findByText("Connection groq saved. It applies now.")).toBeTruthy();
  });

  it("saves a native preset without a base URL and names a second connection id-2", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(providersResponse([row({ name: "deepseek", kind: "deepseek" })]));
    vi.mocked(api.saveProvider).mockResolvedValue({ name: "deepseek-2", version: "v2", restart_required: false });
    render(<ProviderDetail id="deepseek" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add connection" }));
    const dialog = screen.getByRole("dialog");
    expect((within(dialog).getByLabelText("Connection name") as HTMLInputElement).placeholder).toBe("deepseek-2");
    expect((within(dialog).getByLabelText("Base URL") as HTMLInputElement).placeholder).toBe("https://api.deepseek.com/v1");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(vi.mocked(api.saveProvider)).toHaveBeenCalledWith("deepseek-2", {
        kind: "deepseek",
        api_key_env: "DEEPSEEK_2_API_KEY",
        expected_version: "v1",
      });
    });
  });

  it("switches a connection off with disabled in the PUT", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([
        row({ name: "gemini", kind: "openai_compat", base_url: "https://generativelanguage.googleapis.com/v1beta/openai", api_key_env: "GEMINI_API_KEY" }),
      ])
    );
    vi.mocked(api.saveProvider).mockResolvedValue({ name: "gemini", version: "v2", restart_required: false });
    render(<ProviderDetail id="gemini" />);
    const toggle = await screen.findByRole("checkbox", { name: "Use gemini" });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    fireEvent.click(toggle);
    await waitFor(() => {
      expect(vi.mocked(api.saveProvider)).toHaveBeenCalledWith("gemini", {
        kind: "openai_compat",
        base_url: "https://generativelanguage.googleapis.com/v1beta/openai",
        api_key_env: "GEMINI_API_KEY",
        disabled: true,
        expected_version: "v1",
      });
    });
    expect(await screen.findByText("gemini is off. The router skips it.")).toBeTruthy();
  });

  it("tests a connection and shows who uses it when a delete is refused", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([row({ name: "deepseek", kind: "deepseek", used_by: ["agents.lana"] })])
    );
    vi.mocked(api.testProvider).mockResolvedValue({ ok: true, latency_ms: 40 });
    vi.mocked(api.deleteProvider).mockRejectedValue(new ApiError(409, "in_use", "used by agents: lana; combos: coding"));
    render(<ProviderDetail id="deepseek" />);
    fireEvent.click(await screen.findByRole("button", { name: "Test deepseek" }));
    await waitFor(() => {
      expect(vi.mocked(api.testProvider)).toHaveBeenCalledWith({
        name: "deepseek",
        kind: "deepseek",
        api_key_env: "DEEPSEEK_API_KEY",
      });
    });
    expect(await screen.findByText(/Connected · 40 ms/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Delete deepseek" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("Cannot delete deepseek: used by agents: lana; combos: coding.")).toBeTruthy();
    expect(vi.mocked(api.deleteProvider)).toHaveBeenCalledWith("deepseek", "v1");
  });

  it("lists the models of each connection", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(providersResponse([row({ name: "ds", kind: "deepseek" })]));
    vi.mocked(api.listRouterModels).mockResolvedValue([
      {
        provider: "ds",
        fetched_at: "",
        fetch_error: "",
        models: [
          {
            id: "deepseek-flash",
            address: "ds/deepseek-flash",
            name: "DeepSeek Flash",
            context_window: 128000,
            max_output: 8000,
            vision: false,
            thinking: true,
            price: { input: 0.1, cached_input: 0.01, output: 0.4 },
            source: "both",
          },
        ],
      },
    ]);
    render(<ProviderDetail id="deepseek" />);
    expect(await screen.findByText("DeepSeek Flash")).toBeTruthy();
    expect(vi.mocked(api.listRouterModels)).toHaveBeenCalledWith("ds", false);
  });

});

/* Spec 39 revision 2 (REQ-CCT-010, REQ-CCT-011): the Claude Code page lists
   the accounts as connections, adds one with a login command, and keeps the
   custom model IDs. */
describe("Claude Code page", () => {
  beforeEach(() => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([
        row({ name: "claude-code", kind: "claude-code", api_key_env: "", builtin: true, allow_scheduled: false }),
        row({
          name: "claude-work",
          kind: "claude-code",
          api_key_env: "",
          config_dir: "/Users/x/.lana/claude/claude-work",
          allow_scheduled: true,
        }),
      ])
    );
    vi.mocked(api.getRouterUsage).mockResolvedValue({
      ...USAGE,
      by_provider: [
        { ...STATS, provider: "claude-code", calls: 12, input_tokens: 45000, output_tokens: 3000, cost_usd: 0.42 },
        { ...STATS, provider: "claude-work", calls: 3, input_tokens: 9000, output_tokens: 1000, cost_usd: 0.05 },
      ],
    });
  });

  /* AC-CCT-013 (UI side). */
  it("shows each account with its email, plan, windows, and usage", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    render(<ProviderDetail id="claude-code" />);
    const home = await screen.findByRole("listitem", { name: "Account claude-code" });
    const work = screen.getByRole("listitem", { name: "Account claude-work" });
    expect(within(home).getByText("me@home.dev")).toBeTruthy();
    expect(within(home).getByText("Max")).toBeTruthy();
    expect(within(home).getByText("Built in")).toBeTruthy();
    expect(within(home).getByText(/^42 % used · resets /)).toBeTruthy();
    expect(within(home).getByText(/^10 % used · resets /)).toBeTruthy();
    expect(await within(home).findByText(/^12 calls · 45k in/)).toBeTruthy();
    expect(within(work).getByText("me@work.dev")).toBeTruthy();
    expect(within(work).getByText("Team")).toBeTruthy();
    expect(within(work).getByText(/^80 % used/)).toBeTruthy();
    expect(within(work).getByText(/^55 % used/)).toBeTruthy();
    expect(within(work).getByText(/^3 calls · 9k in/)).toBeTruthy();
    /* The built-in account cannot be removed or renamed; an added one can. */
    expect(within(home).queryByRole("button", { name: "Remove claude-code" })).toBeNull();
    expect(within(home).queryByRole("button", { name: "Rename claude-code" })).toBeNull();
    expect(within(work).getByRole("button", { name: "Remove claude-work" })).toBeTruthy();
    expect(within(work).getByRole("button", { name: "Rename claude-work" })).toBeTruthy();
    /* The display name is prominent; the internal name is small and mono. */
    expect(within(work).getByText("claude-work").className).toContain("mono");
    expect(screen.getByText(/break Anthropic's terms/)).toBeTruthy();
    expect(screen.getByText(SIGN_IN_HELP)).toBeTruthy();
  });

  /* REQ-CCT-012: a logged-out account offers one-click sign-in; the
     terminal command stays one click away for a remote daemon. */
  it("offers Sign in with Claude Code for a logged-out account", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue({
      ...TWO_ACCOUNTS,
      accounts: [
        account({
          name: "claude-work",
          logged_in: false,
          login_command: "CLAUDE_CONFIG_DIR='/x/claude-work' CLAUDE_SECURESTORAGE_CONFIG_DIR='/x/claude-work' claude auth login",
        }),
      ],
    });
    render(<ProviderDetail id="claude-code" />);
    const work = await screen.findByRole("listitem", { name: "Account claude-work" });
    expect(within(work).getByText("Not signed in")).toBeTruthy();
    expect(within(work).getByRole("button", { name: "Sign in with Claude Code" })).toBeTruthy();
    expect(within(work).getByText("Sign in from a terminal instead")).toBeTruthy();
    expect(within(work).getByRole("button", { name: "Copy login command" })).toBeTruthy();
    expect(work.textContent).not.toContain("/login");
    expect(screen.queryByText(/Login with Claude/i)).toBeNull();
  });

  it("switches scheduled runs per account with kind claude-code", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    vi.mocked(api.saveProvider).mockResolvedValue({ name: "claude-work", version: "v2", restart_required: false });
    render(<ProviderDetail id="claude-code" />);
    const toggle = await screen.findByRole("checkbox", { name: "Allow scheduled runs for claude-work" });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    fireEvent.click(toggle);
    await waitFor(() => {
      expect(vi.mocked(api.saveProvider)).toHaveBeenCalledWith("claude-work", {
        kind: "claude-code",
        config_dir: "/Users/x/.lana/claude/claude-work",
        allow_scheduled: false,
        disabled: false,
        expected_version: "v1",
      });
    });
  });

  /* REQ-CCT-012: "Add account" is one click: the account is created, the
     sign-in runs in the background and is polled every 1.5 s, and on success
     the new row is highlighted with its email and plan. */
  it("adds an account in one click and follows its sign-in", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.getClaudeAgent).mockResolvedValue(CLAUDE);
      vi.mocked(api.createClaudeAccount).mockResolvedValue({
        name: "claude-2",
        config_dir: "/Users/x/.lana/claude/claude-2",
        version: "v2",
        restart_required: false,
        login_command: "claude auth login --for claude-2",
        sign_in: { state: "running", error: "", started_at: "2026-11-08T10:00:00Z", finished_at: null },
      });
      vi.mocked(api.getClaudeSignIn).mockResolvedValue(RUNNING);
      render(<ProviderDetail id="claude-code" />);
      fireEvent.click(await screen.findByRole("button", { name: "Add account" }));
      const card = await screen.findByRole("status", { name: "Sign-in of claude-2" });
      expect(vi.mocked(api.createClaudeAccount)).toHaveBeenCalledWith();
      expect(card.textContent).toContain("Opening Claude Code sign-in in your browser… Finish signing in there.");
      expect(card.textContent).toContain(SIGN_IN_HELP);
      expect(screen.queryByRole("dialog")).toBeNull();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(SIGN_IN_POLL_MS);
      });
      expect(vi.mocked(api.getClaudeSignIn)).toHaveBeenCalledWith("claude-2");
      expect(screen.getByRole("status", { name: "Sign-in of claude-2" })).toBeTruthy();

      vi.mocked(api.getClaudeSignIn).mockResolvedValue({ ...RUNNING, state: "succeeded", finished_at: "2026-11-08T10:01:00Z" });
      vi.mocked(api.getClaudeAgent).mockResolvedValue({
        ...CLAUDE,
        accounts: [...CLAUDE.accounts, account({ name: "claude-2", email: "two@work.dev", plan: "pro", display_name: "two@work.dev" })],
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(SIGN_IN_POLL_MS);
      });
      const row = await screen.findByRole("listitem", { name: "Account claude-2" });
      expect(await within(row).findByText(/Signed in as two@work.dev · Pro/)).toBeTruthy();
      expect(row.className).toContain("is-new");
      expect(screen.queryByRole("status", { name: "Sign-in of claude-2" })).toBeNull();
      const polls = vi.mocked(api.getClaudeSignIn).mock.calls.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(SIGN_IN_POLL_MS * 2);
      });
      expect(vi.mocked(api.getClaudeSignIn).mock.calls.length).toBe(polls);
    } finally {
      vi.useRealTimers();
    }
  });

  /* REQ-CCT-012: a failed sign-in shows the daemon's sanitized error, Try
     again, and the terminal command collapsed. */
  it("shows a failed sign-in with Try again and the terminal fallback", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.getClaudeAgent).mockResolvedValue({
        ...TWO_ACCOUNTS,
        accounts: [account({ name: "claude-work", logged_in: false, login_command: "claude auth login --for work" })],
      });
      vi.mocked(api.startClaudeSignIn).mockResolvedValue(RUNNING);
      vi.mocked(api.getClaudeSignIn).mockResolvedValue({
        ...RUNNING,
        state: "failed",
        error: "Login failed: token exchange refused at sign-in link",
      });
      render(<ProviderDetail id="claude-code" />);
      const work = await screen.findByRole("listitem", { name: "Account claude-work" });
      fireEvent.click(within(work).getByRole("button", { name: "Sign in with Claude Code" }));
      await waitFor(() => expect(vi.mocked(api.startClaudeSignIn)).toHaveBeenCalledWith("claude-work"));
      expect(within(work).getByRole("status", { name: "Sign-in of claude-work" })).toBeTruthy();
      expect(within(work).queryByRole("button", { name: "Sign in with Claude Code" })).toBeNull();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(SIGN_IN_POLL_MS);
      });
      const alert = await within(work).findByRole("alert", { name: "Sign-in of claude-work" });
      expect(alert.textContent).toContain("Login failed: token exchange refused at sign-in link");
      expect(within(alert).getByText("Sign in from a terminal instead")).toBeTruthy();
      expect(within(alert).getByText("claude auth login --for work")).toBeTruthy();

      vi.mocked(api.getClaudeSignIn).mockResolvedValue(RUNNING);
      fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
      await waitFor(() => expect(vi.mocked(api.startClaudeSignIn)).toHaveBeenCalledTimes(2));
      expect(await within(work).findByRole("status", { name: "Sign-in of claude-work" })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("cancels a running sign-in", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue({
      ...TWO_ACCOUNTS,
      accounts: [account({ name: "claude-work", logged_in: false })],
    });
    vi.mocked(api.startClaudeSignIn).mockResolvedValue(RUNNING);
    vi.mocked(api.cancelClaudeSignIn).mockResolvedValue({ ...RUNNING, state: "cancelled" });
    render(<ProviderDetail id="claude-code" />);
    const work = await screen.findByRole("listitem", { name: "Account claude-work" });
    fireEvent.click(within(work).getByRole("button", { name: "Sign in with Claude Code" }));
    const card = await within(work).findByRole("status", { name: "Sign-in of claude-work" });
    fireEvent.click(within(card).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(vi.mocked(api.cancelClaudeSignIn)).toHaveBeenCalledWith("claude-work"));
    expect(await within(work).findByText("Sign-in cancelled.")).toBeTruthy();
  });

  /* REQ-CCT-012: sign out asks first; the built-in account warns that it
     signs the owner out of their own Claude Code and sends confirm. */
  it("signs an account out after a confirm, with stronger wording for the built-in one", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    vi.mocked(api.signOutClaudeAccount).mockResolvedValue(account({ name: "claude-work", logged_in: false }));
    render(<ProviderDetail id="claude-code" />);
    const work = await screen.findByRole("listitem", { name: "Account claude-work" });
    fireEvent.click(within(work).getByRole("button", { name: "Sign out" }));
    let dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("Sign out me@work.dev?");
    expect(dialog.textContent).not.toContain("on this machine");
    fireEvent.click(within(dialog).getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(vi.mocked(api.signOutClaudeAccount)).toHaveBeenCalledWith("claude-work", false));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    const home = screen.getByRole("listitem", { name: "Account claude-code" });
    fireEvent.click(within(home).getByRole("button", { name: "Sign out" }));
    dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("Sign out of your own Claude Code?");
    expect(dialog.textContent).toContain("signs you out of Claude Code on this machine");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(vi.mocked(api.signOutClaudeAccount)).toHaveBeenCalledTimes(1);
    fireEvent.click(within(home).getByRole("button", { name: "Sign out" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(vi.mocked(api.signOutClaudeAccount)).toHaveBeenLastCalledWith("claude-code", true));
  });

  it("removes an account with its folder after a confirm", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    vi.mocked(api.deleteProvider).mockResolvedValue({
      name: "claude-work",
      version: "v2",
      restart_required: false,
      folder_removed: true,
      message: "The account was signed out and its folder /Users/x/.lana/claude/claude-work was deleted.",
    });
    render(<ProviderDetail id="claude-code" />);
    const work = await screen.findByRole("listitem", { name: "Account claude-work" });
    fireEvent.click(within(work).getByRole("button", { name: "Remove claude-work" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("Signs this account out and deletes its folder");
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() =>
      expect(vi.mocked(api.deleteProvider)).toHaveBeenCalledWith("claude-work", "v1", { removeFolder: true })
    );
    expect(await screen.findByText(/its folder \/Users\/x\/.lana\/claude\/claude-work was deleted/)).toBeTruthy();
  });

  it("renames an account label inline", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    vi.mocked(api.setClaudeAccountLabel).mockResolvedValue({ name: "claude-work", label: "Work", version: "v2", restart_required: false });
    render(<ProviderDetail id="claude-code" />);
    const work = await screen.findByRole("listitem", { name: "Account claude-work" });
    fireEvent.click(within(work).getByRole("button", { name: "Rename claude-work" }));
    const input = within(work).getByLabelText("Label of claude-work");
    fireEvent.change(input, { target: { value: "  Work " } });
    fireEvent.click(within(work).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(vi.mocked(api.setClaudeAccountLabel)).toHaveBeenCalledWith("claude-work", "Work", "v1"));
    await waitFor(() => expect(within(work).queryByLabelText("Label of claude-work")).toBeNull());

    fireEvent.click(within(work).getByRole("button", { name: "Rename claude-work" }));
    fireEvent.change(within(work).getByLabelText("Label of claude-work"), { target: { value: "x".repeat(61) } });
    expect((within(work).getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(within(work).getByRole("button", { name: "Cancel" }));
    expect(within(work).queryByLabelText("Label of claude-work")).toBeNull();
  });

  it("adds and removes a custom model ID with PUT /v1/router/claude-code/models", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(TWO_ACCOUNTS);
    vi.mocked(api.listRouterModels).mockResolvedValue([
      {
        provider: "claude-code",
        fetched_at: "",
        fetch_error: "",
        models: [
          { id: "opus", address: "claude-code/opus", name: "Claude Code · Opus (latest)", context_window: 200000, max_output: 0, vision: true, thinking: true, price: null, source: "alias" },
          { id: "claude-opus-5-5", address: "claude-code/claude-opus-5-5", name: "Claude Code · Opus 5.5", context_window: 200000, max_output: 0, vision: true, thinking: true, price: null, source: "claude_code" },
          { id: "my-model", address: "claude-code/my-model", name: "my-model", context_window: 0, max_output: 0, vision: false, thinking: false, price: null, source: "custom" },
        ],
      },
    ]);
    vi.mocked(api.saveClaudeCodeModels).mockResolvedValue({ models: ["my-model", "claude-next"], version: "v2", restart_required: false });
    vi.mocked(api.testRouterModel).mockResolvedValue({ ok: true, latency_ms: 900 });
    render(<ProviderDetail id="claude-code" />);
    const table = await screen.findByLabelText("Claude Code models");
    expect(await within(table).findByText("Claude Code · Opus 5.5")).toBeTruthy();
    expect(within(table).getByText("Custom")).toBeTruthy();
    expect(within(table).getByText("Alias")).toBeTruthy();
    expect(table.textContent).toContain("uses your plan");

    fireEvent.click(within(table).getByRole("button", { name: "Test claude-code/opus" }));
    await waitFor(() => expect(vi.mocked(api.testRouterModel)).toHaveBeenCalledWith("claude-code/opus"));

    fireEvent.change(within(table).getByLabelText("Custom model ID"), { target: { value: "claude-next" } });
    fireEvent.click(within(table).getByRole("button", { name: "Add model" }));
    await waitFor(() => {
      expect(vi.mocked(api.saveClaudeCodeModels)).toHaveBeenCalledWith(["my-model", "claude-next"], "v1");
    });

    fireEvent.click(within(table).getByRole("button", { name: "Remove my-model" }));
    await waitFor(() => {
      expect(vi.mocked(api.saveClaudeCodeModels)).toHaveBeenLastCalledWith([], "v2");
    });
  });
});

describe("provider presets and logos", () => {
  it("matches compat entries by host and native entries by kind", () => {
    expect(presetFor({ name: "x", kind: "openai_compat", base_url: "https://api.x.ai/v1" })?.id).toBe("xai");
    expect(presetFor({ name: "x", kind: "anthropic" })?.id).toBe("anthropic");
    expect(presetFor({ name: "cmc", kind: "commandcode" })?.id).toBe("commandcode");
    expect(presetFor({ name: "x", kind: "openai_compat", base_url: "http://127.0.0.1:9/v1" })).toBeUndefined();
  });

  it("falls back to a two-letter monogram", () => {
    expect(monogram("Local LLM")).toBe("LL");
    expect(monogram("DeepSeek")).toBe("DS");
    expect(monogram("my-proxy")).toBe("MP");
    expect(monogram("groq")).toBe("GR");
    const { container } = render(
      <>
        <ProviderLogo logo="" name="Local LLM" />
        <ProviderLogo logo="openai" name="OpenAI" size={24} />
      </>
    );
    const [mono, brand] = Array.from(container.querySelectorAll(".pl"));
    expect(mono?.textContent).toBe("LL");
    expect(brand?.querySelector("svg")).toBeTruthy();
    expect(brand?.querySelector("title")).toBeNull();
    expect(brand?.querySelector("svg")?.getAttribute("fill")).toBe("currentColor");
  });
});

/* Spec 39 Revision 4: the plan usage refresh of the Quota, Usage, and Claude
   Code pages. */
describe("plan usage refresh", () => {
  const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60000).toISOString();
  const claudeLimit = (provider: string, updated: string, extra: Partial<LimitSnapshot> = {}): LimitSnapshot => ({
    provider,
    kind: "claude-code",
    updated_at: updated,
    error: "",
    windows: [{ type: "five_hour", status: "allowed", utilization: 0.43, resets_at: "2026-11-08T08:39:00Z", source: "usage_probe" }],
    ...extra,
  });
  const LOGGED_IN: ClaudeAgentStatus = {
    ...CLAUDE,
    logged_in: true,
    accounts: [account({ name: "claude-code", builtin: true, logged_in: true })],
  };

  it("is due for an old, untimed, or missing Claude Code snapshot only", () => {
    const fresh = claudeLimit("claude-code", minutesAgo(1));
    expect(claudeLimitsStale([fresh], ["claude-code"])).toBe(false);
    expect(claudeLimitsStale([claudeLimit("claude-code", minutesAgo(6))], ["claude-code"])).toBe(true);
    expect(claudeLimitsStale([{ ...fresh, updated_at: undefined }], [])).toBe(true);
    expect(claudeLimitsStale([fresh], ["claude-code", "claude-work"])).toBe(true);
    expect(claudeLimitsStale([fresh, { provider: "openai", kind: "openai", updated_at: minutesAgo(60) }], [])).toBe(false);
  });

  it("refreshes the Quota page on mount only when a plan is over five minutes old", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(LOGGED_IN);
    vi.mocked(api.getRouterLimits).mockResolvedValue([claudeLimit("claude-code", minutesAgo(1))]);
    const fresh = render(<RouterQuota />);
    await screen.findByLabelText("Claude Code plan of claude-code");
    await act(async () => {
      await Promise.resolve();
    });
    expect(vi.mocked(api.refreshRouterLimits)).not.toHaveBeenCalled();
    fresh.unmount();

    vi.mocked(api.getRouterLimits).mockResolvedValue([claudeLimit("claude-code", minutesAgo(10))]);
    render(<RouterQuota />);
    await waitFor(() => {
      expect(vi.mocked(api.refreshRouterLimits)).toHaveBeenCalledTimes(1);
    });
  });

  it("shows the extra weekly windows and the probe error on the Quota card", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue(LOGGED_IN);
    vi.mocked(api.getRouterLimits).mockResolvedValue([
      claudeLimit("claude-code", minutesAgo(1), {
        error: "Claude Code /usage timed out after 45s",
        windows: [
          { type: "five_hour", status: "allowed", utilization: 0.43, resets_at: "2026-11-08T08:39:00Z" },
          { type: "seven_day_fable", status: "allowed", utilization: 0, label: "Weekly · Fable", source: "usage_probe" },
        ],
      }),
    ]);
    render(<RouterQuota />);
    const card = await screen.findByLabelText("Claude Code plan of claude-code");
    expect(within(card).getByText("Weekly · Fable")).toBeTruthy();
    expect(within(card).getByText("Claude Code /usage timed out after 45s")).toBeTruthy();
    expect(windowText({ type: "seven_day_fable", status: "allowed", utilization: 0, label: "Weekly · Fable" })).toBe(
      "Weekly · Fable · 0 % used"
    );
  });

  it("refreshes from the Claude Code page and shows the probe error under the account", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      providersResponse([row({ name: "claude-code", kind: "claude-code", api_key_env: "", builtin: true })])
    );
    vi.mocked(api.getClaudeAgent).mockResolvedValue(LOGGED_IN);
    vi.mocked(api.getRouterLimits).mockResolvedValue([claudeLimit("claude-code", minutesAgo(1))]);
    vi.mocked(api.refreshRouterLimits).mockResolvedValue([
      claudeLimit("claude-code", minutesAgo(0), {
        error: "Not a subscription: Claude Code /usage shows no plan windows for this account.",
        windows: [{ type: "seven_day_fable", status: "allowed", utilization: 0.2, label: "Weekly · Fable" }],
      }),
    ]);
    render(<ProviderDetail id="claude-code" />);
    const home = await screen.findByRole("listitem", { name: "Account claude-code" });
    expect(await within(home).findByText(/^43 % used/)).toBeTruthy();
    expect(vi.mocked(api.refreshRouterLimits)).not.toHaveBeenCalled();
    const panel = screen.getByRole("heading", { name: "Accounts" }).closest("section") as HTMLElement;
    fireEvent.click(within(panel).getByRole("button", { name: "Refresh" }));
    expect(await within(home).findByText("Weekly · Fable")).toBeTruthy();
    expect(within(home).getByText(/^Not a subscription/)).toBeTruthy();
    expect(vi.mocked(api.refreshRouterLimits)).toHaveBeenCalledTimes(1);
  });

  it("refreshes the plans and reloads the usage from the Usage page", async () => {
    render(<OverviewTab />);
    await screen.findByLabelText("Totals");
    const usageCalls = vi.mocked(api.getRouterUsage).mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => {
      expect(vi.mocked(api.refreshRouterLimits)).toHaveBeenCalledTimes(1);
      expect(vi.mocked(api.getRouterUsage).mock.calls.length).toBe(usageCalls + 1);
    });
    expect(await screen.findByText(/^Updated /)).toBeTruthy();
  });
});

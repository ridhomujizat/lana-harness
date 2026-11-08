import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

/* The network calls of the General tab are mocked so the model picker, the
   Thinking select, and the legacy banner can be observed (REQ-MDL-012,
   REQ-MDL-020). */
vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return {
    ...actual,
    listAgents: vi.fn(),
    duplicateAgent: vi.fn(),
    updateAgent: vi.fn(),
  };
});
vi.mock("../api/settings", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/settings")>();
  return { ...actual, getConfig: vi.fn(), listProviders: vi.fn() };
});
vi.mock("../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/router")>();
  return { ...actual, getRouter: vi.fn(), getClaudeAgent: vi.fn() };
});
vi.mock("../api/models", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/models")>();
  return { ...actual, getModels: vi.fn() };
});
vi.mock("../api/sessions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/sessions")>();
  return { ...actual, createSession: vi.fn() };
});
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return {
    ...actual,
    getAgentOverview: vi.fn(),
    listRooms: vi.fn(),
    openRoomsStream: vi.fn(),
  };
});

import { listAgents, updateAgent } from "../api/agents";
import type { Agent } from "../api/agents";
import { getModels } from "../api/models";
import type { ModelsResponse } from "../api/models";
import { getAgentOverview, openRoomsStream } from "../api/rooms";
import type { AgentOverview as AgentOverviewData } from "../api/rooms";
import { getConfig } from "../api/settings";
import type { GetConfigResponse } from "../api/settings";
import { getClaudeAgent, getRouter } from "../api/router";
import type { ClaudeAccount, RouterOverview } from "../api/router";
import { AgentDetailPage } from "./agent-detail";

function baseAgent(overrides: Partial<Agent>): Agent {
  return {
    name: "coder",
    kind: "agent",
    title: "",
    color: "brand",
    model: "deepseek-flash",
    policy: "ask",
    skills: [],
    mcp: [],
    mcp_status: [],
    description: "",
    default: false,
    provider: "deepseek",
    permission: "ask",
    workspace: "",
    disabled_tools: [],
    tools_count: 0,
    sessions_count: 0,
    last_active_at: null,
    ...overrides,
  };
}

const CATALOG: ModelsResponse = {
  models: [
    {
      id: "deepseek-flash",
      name: "DeepSeek Flash",
      description: "Fast DeepSeek model with thinking.",
      context_window: 1048576,
      legacy: false,
      replacement: "",
      thinking: "deepseek",
      default_effort: "high",
    },
    {
      id: "deepseek-chat",
      name: "DeepSeek Chat",
      description: "Legacy DeepSeek model served without thinking.",
      context_window: 128000,
      legacy: true,
      replacement: "deepseek-flash",
      thinking: "",
      default_effort: "",
    },
  ],
  efforts: [
    { value: "off", description: "Use for simple tasks that do not need reasoning." },
    { value: "low", description: "Prefer for routine or latency-sensitive tasks." },
    { value: "high", description: "The default balance for most tasks." },
    { value: "max", description: "Reserve for the hardest quality-first tasks." },
  ],
};

function configFor(model: string): GetConfigResponse {
  return {
    version: "v1",
    loaded_version: "v1",
    restart_required: false,
    validation: { ok: true, errors: [] },
    raw: "",
    settings: {
      providers: {
        deepseek: { kind: "openai_compat", base_url: "https://api.deepseek.com/v1" },
      },
      agents: { coder: { provider: "deepseek", model } },
      routes: [],
      channels: {},
      mcp: {},
      modules: [],
    },
  };
}

/* GET /v1/router (API-RTR-008): two combos and three providers. */
const ROUTER: RouterOverview = {
  version: "v1",
  providers: [],
  combos: [],
  cooling: [],
  proxy: { enabled: false, base_url: "" },
  choices: [
    { value: "anthropic/claude-sonnet-5-5", label: "Claude Sonnet 5.5", group: "anthropic" },
    { value: "coding", label: "coding", group: "Combos" },
    { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek" },
    { value: "deepseek/deepseek-chat", label: "DeepSeek Chat", group: "deepseek" },
    { value: "fast", label: "fast", group: "Combos" },
    { value: "openrouter/openai/gpt-5-mini", label: "GPT-5 mini", group: "openrouter" },
  ],
};

function mockAgent(model: string): void {
  vi.mocked(listAgents).mockResolvedValue({ agents: [baseAgent({ model })] });
  vi.mocked(getConfig).mockResolvedValue(configFor(model));
  vi.mocked(getRouter).mockResolvedValue(ROUTER);
  vi.mocked(getModels).mockResolvedValue(CATALOG);
}

function load(model: string): void {
  mockAgent(model);
  render(<AgentDetailPage name="coder" tab="general" />);
}

function overviewData(): AgentOverviewData {
  return {
    name: "coder",
    kind: "agent",
    title: "Coding",
    color: "brand",
    persona_line: "",
    created_at: Date.now() - 86400000,
    presence: { state: "resting", label: "Resting", since: 0, session_id: "" },
    now: null,
    activity: [],
    week: { days: [], turns: 0, questions: 0, notes: 0, tokens: 0 },
    memory: { entries: [], total: 0 },
    job: "",
    looks_after: { channels: [], teams: [] },
  };
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("agent model settings", () => {
  /* REQ-MDL-012: a legacy model shows the banner and the one-click switch. */
  it("shows the legacy banner and switches the model field", async () => {
    load("deepseek-chat");

    expect(
      await screen.findByText("Legacy model — switch to deepseek-flash")
    ).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: "Switch to deepseek-flash" })
    );
    expect(
      screen.queryByText("Legacy model — switch to deepseek-flash")
    ).toBeNull();
    /* The Gr8r Select is a combobox button; its label is the catalog name. */
    expect(
      screen.getByRole("combobox", { name: "Model" }).textContent
    ).toContain("DeepSeek Flash");
  });

  /* REQ-MDL-020: the Thinking select is shown for a thinking model, with the
     effort descriptions as its options. */
  it("shows the Thinking select with effort descriptions for a thinking model", async () => {
    load("deepseek-flash");

    const thinking = await screen.findByRole("combobox", { name: "Thinking" });
    expect(thinking.textContent).toContain("Model default");
    fireEvent.click(thinking);
    expect(
      await screen.findByRole("option", {
        name: /Prefer for routine or latency-sensitive tasks/,
      })
    ).toBeTruthy();
  });

  it("hides the Thinking select for a non-thinking model", async () => {
    load("deepseek-chat");

    await screen.findByText("Legacy model — switch to deepseek-flash");
    expect(screen.queryByRole("combobox", { name: "Thinking" })).toBeNull();
  });

  /* REQ-MDL-020: a model outside the router choices stays selected in the
     free-text field instead of being dropped. */
  it("keeps an unknown model in the free-text field", async () => {
    mockRouterAgent("my-custom-model");
    render(<AgentDetailPage name="coder" tab="general" />);

    const input = (await screen.findByLabelText("Custom model id")) as HTMLInputElement;
    expect(input.value).toBe("my-custom-model");
  });

  /* A legacy agent (provider + raw model) keeps showing its saved pair. */
  it("shows a legacy provider and model as the current choice", async () => {
    load("my-custom-model");

    const model = await screen.findByRole("combobox", { name: "Model" });
    expect(model.textContent).toContain("my-custom-model · deepseek");
  });
});

function mockRouterAgent(model: string): void {
  vi.mocked(listAgents).mockResolvedValue({
    agents: [baseAgent({ model, provider: "" })],
  });
  vi.mocked(getConfig).mockResolvedValue({
    ...configFor(model),
    settings: { ...configFor(model).settings, agents: { coder: { model } } },
  });
  vi.mocked(getRouter).mockResolvedValue(ROUTER);
  vi.mocked(getModels).mockResolvedValue(CATALOG);
}

/* Spec 37 (AC-RTR-024): combos first, then models grouped by provider; the
   save writes model with no provider key. */
describe("agent router model picker (AC-RTR-024)", () => {
  it("lists combos first, then models grouped by provider", async () => {
    mockRouterAgent("coding");
    render(<AgentDetailPage name="coder" tab="general" />);

    const model = await screen.findByRole("combobox", { name: "Model" });
    expect(model.textContent).toContain("coding");
    fireEvent.click(model);
    const listbox = await screen.findByRole("listbox");
    const groups = Array.from(listbox.querySelectorAll('[role="group"]')).map(
      (node) => node.getAttribute("aria-label")
    );
    expect(groups).toEqual(["Combos", "anthropic", "deepseek", "openrouter"]);
    const options = screen
      .getAllByRole("option")
      .map((node) => node.getAttribute("data-value"));
    expect(options.slice(0, 2)).toEqual(["coding", "fast"]);
    expect(options.indexOf("deepseek/deepseek-flash")).toBeGreaterThan(
      options.indexOf("anthropic/claude-sonnet-5-5")
    );
  });

  it("saves the picked address without a provider key", async () => {
    mockRouterAgent("coding");
    vi.mocked(updateAgent).mockResolvedValue({
      name: "coder",
      version: "v2",
      restart_required: false,
    });
    render(<AgentDetailPage name="coder" tab="general" />);

    fireEvent.click(await screen.findByRole("combobox", { name: "Model" }));
    fireEvent.click(await screen.findByRole("option", { name: "DeepSeek Flash" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith(
        "coder",
        { model: "deepseek/deepseek-flash" },
        "v1"
      );
    });
    const patch = vi.mocked(updateAgent).mock.calls[0]?.[1] ?? {};
    expect("provider" in patch).toBe(false);
  });

  it("clears the provider of a legacy agent that picks a combo", async () => {
    load("deepseek-flash");
    vi.mocked(updateAgent).mockResolvedValue({
      name: "coder",
      version: "v2",
      restart_required: false,
    });

    fireEvent.click(await screen.findByRole("combobox", { name: "Model" }));
    fireEvent.click(await screen.findByRole("option", { name: "fast" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith(
        "coder",
        { provider: "", model: "fast" },
        "v1"
      );
    });
  });
});

/* Spec 39 (REQ-CCT-009, AC-CCT-011): no engine choice; Claude Code models are
   router choices, and the agent form shows the Claude Code note. */
const CLAUDE_ROUTER: RouterOverview = {
  ...ROUTER,
  choices: [
    ...ROUTER.choices,
    { value: "claude-code/opus", label: "Claude Code · Opus (latest)", group: "Claude Code" },
    { value: "claude-code/claude-opus-5-5", label: "Claude Code · Opus 5.5", group: "Claude Code" },
  ],
};

function claudeAccount(name: string, email: string): ClaudeAccount {
  return {
    name,
    builtin: name === "claude-code",
    config_dir: "",
    binary: "/usr/local/bin/claude",
    version: "2.1.319",
    version_ok: true,
    logged_in: true,
    email,
    plan: "max",
    auth_method: "claude.ai",
    error: "",
    running: 0,
    queued: 0,
    login_command: "",
    windows: [],
    agents: [],
    combos: [],
    label: "",
    display_name: email || name,
    sign_in: { state: "idle", error: "", started_at: null, finished_at: null },
  };
}

describe("Claude Code as a model (AC-CCT-011)", () => {
  it("has no engine choice and saves the Claude Code address alone", async () => {
    mockRouterAgent("coding");
    vi.mocked(getRouter).mockResolvedValue(CLAUDE_ROUTER);
    vi.mocked(updateAgent).mockResolvedValue({
      name: "coder",
      version: "v2",
      restart_required: false,
    });
    render(<AgentDetailPage name="coder" tab="general" />);

    const model = await screen.findByRole("combobox", { name: "Model" });
    expect(screen.queryByRole("radio", { name: "Claude Agent" })).toBeNull();
    expect(screen.queryByText("Engine")).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Reasoning effort" })).toBeNull();
    fireEvent.click(model);
    fireEvent.click(await screen.findByRole("option", { name: "Claude Code · Opus (latest)" }));

    expect(
      screen.getByText(/Runs in Claude Code with your Claude plan\. Claude Code uses its own file and shell tools; lana's memory, knowledge, calendar, workflow and other tools come in through MCP\. Max steps and compaction settings don't apply\./)
    ).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Reasoning effort" })).toBeTruthy();
    expect(screen.getByRole("link", { name: /Allow scheduled runs on claude-code/ }).getAttribute("href")).toBe(
      "#/router/providers/claude-code"
    );
    expect(screen.queryByText(/Login with Claude/i)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith("coder", { model: "claude-code/opus" }, "v1");
    });
    const patch = vi.mocked(updateAgent).mock.calls[0]?.[1] ?? {};
    expect("runtime" in patch).toBe(false);
    expect("provider" in patch).toBe(false);
  });

  it("shows a legacy runtime agent as its address with every tab, and saves the address", async () => {
    vi.mocked(listAgents).mockResolvedValue({
      agents: [
        baseAgent({
          model: "claude-code/opus",
          provider: "claude-code",
          runtime: "claude-code",
          claude_code: true,
        }),
      ],
    });
    vi.mocked(getConfig).mockResolvedValue({
      ...configFor("opus"),
      settings: {
        ...configFor("opus").settings,
        agents: { coder: { runtime: "claude-code", model: "opus" } },
      },
    });
    vi.mocked(getRouter).mockResolvedValue(CLAUDE_ROUTER);
    vi.mocked(updateAgent).mockResolvedValue({ name: "coder", version: "v2", restart_required: false });
    render(<AgentDetailPage name="coder" tab="general" />);

    const model = await screen.findByRole("combobox", { name: "Model" });
    expect(model.textContent).toContain("Claude Code · Opus (latest)");
    expect(screen.getByRole("tab", { name: "Tools" })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Persona & memory" })).toBeTruthy();
    expect(screen.getByText("Claude Code", { selector: ".rt-cc-badge span" })).toBeTruthy();
    expect(screen.queryByText("Claude Agent")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith("coder", { model: "claude-code/opus" }, "v1");
    });
  });

  it("groups Claude Code models per account with the account email", async () => {
    mockRouterAgent("coding");
    vi.mocked(getRouter).mockResolvedValue({
      ...CLAUDE_ROUTER,
      choices: [
        ...CLAUDE_ROUTER.choices,
        { value: "claude-work/opus", label: "Claude Code · Opus (latest)", group: "Claude Code" },
      ],
    });
    vi.mocked(getClaudeAgent).mockResolvedValue({
      configured_agents: [],
      binary: "/usr/local/bin/claude",
      version: "2.1.319",
      version_ok: true,
      logged_in: true,
      auth_method: "claude.ai",
      error: "",
      running: 0,
      queued: 0,
      max_concurrent: 2,
      accounts: [claudeAccount("claude-code", "me@home.dev"), claudeAccount("claude-work", "me@work.dev")],
    });
    render(<AgentDetailPage name="coder" tab="general" />);

    const model = await screen.findByRole("combobox", { name: "Model" });
    await waitFor(() => expect(vi.mocked(getClaudeAgent)).toHaveBeenCalled());
    fireEvent.click(model);
    const listbox = await screen.findByRole("listbox");
    await waitFor(() => {
      const groups = Array.from(listbox.querySelectorAll('[role="group"]')).map((node) =>
        node.getAttribute("aria-label")
      );
      expect(groups).toContain("Claude Code · claude-code (me@home.dev)");
      expect(groups).toContain("Claude Code · claude-work (me@work.dev)");
    });
    const work = listbox.querySelector('[aria-label="Claude Code · claude-work (me@work.dev)"]') as HTMLElement;
    expect(work.querySelector('[data-logo="claude"]')).toBeTruthy();
  });
});

/* The save footer always states when the changes land; the restart notice is
   driven by the save response's restart_required flag. */
describe("agent save notice", () => {
  it("always says changes apply from the next turn, with no restart notice", async () => {
    load("deepseek-flash");

    expect(
      await screen.findByText("Changes apply from the next turn.")
    ).toBeTruthy();
    expect(screen.queryByText(/Some changes need a daemon restart/)).toBeNull();
  });

  it("shows the restart notice only after a save that needs a restart", async () => {
    load("deepseek-flash");

    const model = await screen.findByRole("combobox", { name: "Model" });
    fireEvent.click(model);
    fireEvent.click(await screen.findByRole("option", { name: "DeepSeek Chat" }));
    vi.mocked(updateAgent).mockResolvedValue({
      name: "coder",
      version: "v2",
      restart_required: true,
    });

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(
      await screen.findByText(/Some changes need a daemon restart/)
    ).toBeTruthy();
    expect(
      screen.getByRole("link", {
        name: "Restart the daemon in Settings › General",
      })
    ).toBeTruthy();
    expect(screen.getByText("Changes saved.")).toBeTruthy();
  });
});

/* REQ-ROOM-009: the General tab writes the title and the color (AC-ROOM-014). */
describe("agent profile fields", () => {
  it("sends the title and the color in the PUT", async () => {
    load("deepseek-flash");

    const title = (await screen.findByLabelText("Title")) as HTMLInputElement;
    fireEvent.change(title, { target: { value: "Finance" } });
    fireEvent.click(screen.getByRole("radio", { name: "success" }));
    vi.mocked(updateAgent).mockResolvedValue({
      name: "coder",
      version: "v2",
      restart_required: false,
    });

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(vi.mocked(updateAgent)).toHaveBeenCalledWith(
        "coder",
        { title: "Finance", color: "success" },
        "v1"
      );
    });
  });
});

/* REQ-WEB-047: the Overview tab shows only the hero, so the legacy header must
   not duplicate the agent above it. The other tabs keep the header, and its
   avatar uses the new AgentFace with the agent's resolved color. */
describe("agent detail header (REQ-WEB-047)", () => {
  it("hides the legacy header on the Overview tab and keeps it on General", async () => {
    mockAgent("deepseek-flash");
    vi.mocked(openRoomsStream).mockImplementation((handlers) => {
      handlers.onRooms([]);
      handlers.onState?.(true);
      return { close: () => {} };
    });
    vi.mocked(getAgentOverview).mockResolvedValue(overviewData());

    const overviewRender = render(
      <AgentDetailPage name="coder" tab="overview" />
    );
    await screen.findByRole("tab", { name: "Overview" });
    const overviewHeader = overviewRender.container.querySelector(
      ".agent-detail-header"
    );
    expect(overviewHeader).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Start session" })
    ).toBeNull();
    overviewRender.unmount();

    const generalRender = render(
      <AgentDetailPage name="coder" tab="general" />
    );
    await screen.findByLabelText("Title");
    const generalHeader = generalRender.container.querySelector(
      ".agent-detail-header"
    );
    expect(generalHeader).toBeTruthy();
    expect(generalHeader?.querySelector(".av")).toBeTruthy();
    expect(generalHeader?.querySelector(".agent-avatar")).toBeNull();
  });
});

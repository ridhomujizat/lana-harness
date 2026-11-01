import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

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
vi.mock("../api/models", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/models")>();
  return { ...actual, getModels: vi.fn() };
});
vi.mock("../api/sessions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/sessions")>();
  return { ...actual, createSession: vi.fn() };
});

import { listAgents, updateAgent } from "../api/agents";
import type { Agent } from "../api/agents";
import { getModels } from "../api/models";
import type { ModelsResponse } from "../api/models";
import { getConfig, listProviders } from "../api/settings";
import type { GetConfigResponse, ListProvidersResponse } from "../api/settings";
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

const PROVIDERS: ListProvidersResponse = {
  version: "v1",
  providers: [
    {
      name: "deepseek",
      kind: "openai_compat",
      base_url: "https://api.deepseek.com/v1",
      api_key_env: "DEEPSEEK_API_KEY",
      key_state: "unset",
      used_by: [],
    },
  ],
  restart_required: false,
};

function load(model: string): void {
  vi.mocked(listAgents).mockResolvedValue({ agents: [baseAgent({ model })] });
  vi.mocked(getConfig).mockResolvedValue(configFor(model));
  vi.mocked(listProviders).mockResolvedValue(PROVIDERS);
  vi.mocked(getModels).mockResolvedValue(CATALOG);
  render(<AgentDetailPage name="coder" />);
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
    const select = screen.getByRole("combobox", {
      name: "Model",
    }) as HTMLSelectElement;
    expect(select.value).toBe("deepseek-flash");
  });

  /* REQ-MDL-020: the Thinking select is shown for a thinking model. */
  it("shows the Thinking select with effort descriptions for a thinking model", async () => {
    load("deepseek-flash");

    const select = (await screen.findByRole("combobox", {
      name: "Thinking",
    })) as HTMLSelectElement;
    expect(select.textContent).toContain(
      "Prefer for routine or latency-sensitive tasks."
    );
    expect(select.textContent).toContain("Model default");
  });

  it("hides the Thinking select for a non-thinking model", async () => {
    load("deepseek-chat");

    await screen.findByText("Legacy model — switch to deepseek-flash");
    expect(screen.queryByRole("combobox", { name: "Thinking" })).toBeNull();
  });

  /* REQ-MDL-020: a model outside the catalog stays selected in the free-text
     field instead of being dropped. */
  it("keeps an unknown model in the free-text field", async () => {
    load("my-custom-model");

    const input = (await screen.findByLabelText("Custom model id")) as HTMLInputElement;
    expect(input.value).toBe("my-custom-model");
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

    const select = (await screen.findByRole("combobox", {
      name: "Model",
    })) as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "deepseek-chat" } });
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

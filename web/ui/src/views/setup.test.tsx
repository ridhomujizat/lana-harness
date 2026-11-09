import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { AGENT_ID_HINT } from "./agent-label";

/* The Router onboarding (setup wizard): connect Claude Code and API key
   providers, pick a model with an optional fallback (the default combo) and
   a background model for Claude Code, and the ready summary. The API modules
   are mocked, so each test states the contract shapes it relies on. */

vi.mock("../api/setup", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/setup")>();
  return {
    ...actual,
    getSetup: vi.fn(),
    testProvider: vi.fn(),
    saveSetup: vi.fn(),
    getDaemonStartedAt: vi.fn(),
    pollDaemonRestart: vi.fn(),
  };
});

vi.mock("../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/router")>();
  return {
    ...actual,
    getClaudeAgent: vi.fn(),
    startClaudeSignIn: vi.fn(),
    getClaudeSignIn: vi.fn(),
    cancelClaudeSignIn: vi.fn(),
  };
});

import * as setupApi from "../api/setup";
import type { SetupStatus } from "../api/setup";
import * as routerApi from "../api/router";
import type { ClaudeAccount, ClaudeAgentStatus } from "../api/router";
import { SetupShell, SetupWizard } from "./setup";
import { CLAUDE_CODE_DOCS } from "./setup-models";
import { SIGN_IN_POLL_MS } from "./router/claude-code-page";
import { resetSetupMemory } from "./setup-draft";

const DRAFT_KEY = "lana.setup";
const SUMMARY_KEY = "lana.setup.summary";

const STATUS: SetupStatus = {
  needed: true,
  reasons: ["config_missing"],
  provider_count: 0,
  agent_count: 0,
  models: {
    openai: [
      { id: "gpt-5.1", name: "GPT-5.1" },
      { id: "gpt-5-mini", name: "GPT-5 mini" },
    ],
    anthropic: [{ id: "claude-sonnet-5-5", name: "Claude Sonnet 5.5" }],
    deepseek: [
      { id: "deepseek-flash", name: "DeepSeek Flash" },
      { id: "deepseek-v4-pro", name: "DeepSeek V4 Pro" },
    ],
    openrouter: [{ id: "openai/gpt-5-mini", name: "GPT-5 mini" }],
  },
  claude_code_models: [
    { id: "opus", name: "Opus" },
    { id: "sonnet", name: "Sonnet" },
    { id: "haiku", name: "Haiku" },
  ],
};

function account(overrides: Partial<ClaudeAccount>): ClaudeAccount {
  return {
    name: "claude-code",
    builtin: true,
    config_dir: "",
    binary: "/usr/local/bin/claude",
    version: "2.1.319",
    version_ok: true,
    logged_in: true,
    email: "me@lana.dev",
    plan: "max",
    auth_method: "oauth",
    error: "",
    running: 0,
    queued: 0,
    login_command: "claude",
    windows: [],
    agents: [],
    combos: [],
    label: "",
    display_name: "me@lana.dev",
    sign_in: { state: "idle", error: "", started_at: null, finished_at: null },
    ...overrides,
  };
}

function claudeStatus(entry: ClaudeAccount | null): ClaudeAgentStatus {
  return {
    configured_agents: [],
    binary: entry ? entry.binary : "",
    version: entry ? entry.version : "",
    version_ok: true,
    logged_in: Boolean(entry && entry.logged_in),
    auth_method: "",
    error: "",
    running: 0,
    queued: 0,
    max_concurrent: 2,
    accounts: entry ? [entry] : [],
  };
}

function seedDraft(draft: Record<string, unknown>) {
  window.sessionStorage.setItem(
    DRAFT_KEY,
    JSON.stringify({
      agent: { name: "default", template: "assistant", workspace: "~/lana/workspace", permission: "ask" },
      persona: { agentMd: "", userMd: "" },
      ...draft,
    })
  );
}

function storedDraft(): Record<string, unknown> {
  return JSON.parse(window.sessionStorage.getItem(DRAFT_KEY) || "{}");
}

const OPENAI = { id: "openai", apiKey: "sk-openai", baseUrl: "", latencyMs: 120 };
const DEEPSEEK = { id: "deepseek", apiKey: "sk-deepseek", baseUrl: "", latencyMs: 90 };

async function pick(label: string, option: string) {
  fireEvent.click(screen.getByRole("combobox", { name: label }));
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

beforeEach(() => {
  window.sessionStorage.clear();
  window.location.hash = "";
  resetSetupMemory();
  vi.mocked(setupApi.getSetup).mockResolvedValue(STATUS);
  vi.mocked(routerApi.getClaudeAgent).mockResolvedValue(claudeStatus(account({})));
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Connect your models", () => {
  it("shows the Router cards with real logos and the more providers", async () => {
    const { container } = render(<SetupWizard step="connect" />);
    expect(screen.getByRole("heading", { level: 1, name: "Connect your models" })).toBeTruthy();
    expect(screen.getByText(/Router → Providers/)).toBeTruthy();

    for (const [name, logo] of [
      ["OpenAI", "openai"],
      ["Anthropic", "anthropic"],
      ["DeepSeek", "deepseek"],
      ["OpenRouter", "openrouter"],
    ]) {
      const card = screen.getByRole("button", { name: new RegExp("^" + name) });
      expect(card.querySelector('[data-logo="' + logo + '"]')).not.toBeNull();
    }
    expect(container.querySelector('.setup-cc [data-logo="claude"]')).not.toBeNull();
    expect(screen.queryByRole("button", { name: /^Google Gemini/ })).toBeNull();

    const more = screen.getByRole("button", { name: /More providers/ });
    fireEvent.click(more);
    expect(more.getAttribute("aria-expanded")).toBe("true");
    for (const name of ["Google Gemini", "xAI Grok", "Groq", "Mistral", "Together AI", "Fireworks AI", "Cerebras"]) {
      expect(screen.getByRole("button", { name: new RegExp("^" + name) })).toBeTruthy();
    }
    expect(screen.getByRole("button", { name: /^Google Gemini/ }).querySelector('[data-logo="gemini"]')).not.toBeNull();

    // Nothing is connected or selected yet.
    expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Connect a provider or use Claude Code to continue.")).toBeTruthy();
    await screen.findByText(/Signed in/);
  });

  it("connects a key inline: paste, automatic test, Connected", async () => {
    vi.mocked(routerApi.getClaudeAgent).mockResolvedValue(claudeStatus(null));
    vi.mocked(setupApi.testProvider).mockResolvedValue({ ok: true, latency_ms: 87 });
    render(<SetupWizard step="connect" />);

    const card = screen.getByRole("button", { name: /^OpenAI/ });
    fireEvent.click(card);
    expect(card.getAttribute("aria-expanded")).toBe("true");
    const key = screen.getByLabelText("API key");
    await waitFor(() => expect(document.activeElement).toBe(key));
    // The base URL waits under Advanced.
    const advanced = screen.getByText("Advanced").closest("details") as HTMLDetailsElement;
    expect(advanced.open).toBe(false);
    expect(within(advanced).getByLabelText("Base URL")).toBeTruthy();

    fireEvent.paste(key, { clipboardData: { getData: () => " sk-openai-key " } });
    await screen.findByText(/Connected · answered in 87 ms/);
    expect(vi.mocked(setupApi.testProvider)).toHaveBeenCalledWith({ kind: "openai", baseUrl: "", apiKey: "sk-openai-key" });
    expect(within(card).getByText("Connected")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false);
    expect(storedDraft().connections).toEqual([{ id: "openai", apiKey: "sk-openai-key", baseUrl: "", latencyMs: 87 }]);

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(window.location.hash).toBe("#/setup/model");
  });

  it("shows a failed key test on the card and keeps it disconnected", async () => {
    vi.mocked(setupApi.testProvider).mockResolvedValue({ ok: false, status: 401, message: "401 Unauthorized" });
    render(<SetupWizard step="connect" />);
    fireEvent.click(screen.getByRole("button", { name: /^DeepSeek/ }));
    fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-bad" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect(await screen.findByText("401 Unauthorized")).toBeTruthy();
    expect(storedDraft().connections || []).toEqual([]);
  });

  it("tests a compatible preset with its base URL", async () => {
    vi.mocked(setupApi.testProvider).mockResolvedValue({ ok: true, latency_ms: 50 });
    render(<SetupWizard step="connect" />);
    fireEvent.click(screen.getByRole("button", { name: /More providers/ }));
    fireEvent.click(screen.getByRole("button", { name: /^Groq/ }));
    fireEvent.change(screen.getByLabelText("API key"), { target: { value: "gsk-key" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await screen.findByText(/Connected · answered in 50 ms/);
    expect(vi.mocked(setupApi.testProvider)).toHaveBeenCalledWith({
      kind: "openai_compat",
      baseUrl: "https://api.groq.com/openai/v1",
      apiKey: "gsk-key",
    });
  });

  it("offers Claude Code when it is signed in", async () => {
    render(<SetupWizard step="connect" />);
    expect(await screen.findByText("Signed in · me@lana.dev · Max")).toBeTruthy();
    const toggle = screen.getByRole("checkbox", { name: "Use Claude Code" }) as HTMLInputElement;
    expect(toggle.checked).toBe(false);
    fireEvent.click(toggle);
    expect(toggle.checked).toBe(true);
    expect(storedDraft().claudeCode).toBe(true);
    expect(storedDraft().claudeAccount).toEqual({ email: "me@lana.dev", plan: "max" });
    expect((screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("signs Claude Code in with one click and selects it", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(routerApi.getClaudeAgent).mockResolvedValue(
        claudeStatus(account({ logged_in: false, email: "", plan: "", login_command: "claude auth login" }))
      );
      vi.mocked(routerApi.startClaudeSignIn).mockResolvedValue({ state: "running", error: "", started_at: "2026-11-08T10:00:00Z", finished_at: null });
      vi.mocked(routerApi.getClaudeSignIn).mockResolvedValue({ state: "running", error: "", started_at: "2026-11-08T10:00:00Z", finished_at: null });
      render(<SetupWizard step="connect" />);

      fireEvent.click(await screen.findByRole("button", { name: "Sign in with Claude Code" }));
      const card = await screen.findByRole("status", { name: "Sign-in of claude-code" });
      expect(card.textContent).toContain("Opening Claude Code sign-in in your browser");
      expect(vi.mocked(routerApi.startClaudeSignIn)).toHaveBeenCalledWith("claude-code");

      vi.mocked(routerApi.getClaudeSignIn).mockResolvedValue({ state: "succeeded", error: "", started_at: null, finished_at: null });
      vi.mocked(routerApi.getClaudeAgent).mockResolvedValue(claudeStatus(account({})));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(SIGN_IN_POLL_MS);
      });
      expect(await screen.findByText("Signed in · me@lana.dev · Max")).toBeTruthy();
      expect((screen.getByRole("checkbox", { name: "Use Claude Code" }) as HTMLInputElement).checked).toBe(true);
      expect(storedDraft().claudeCode).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("falls back to the terminal command when the sign-in fails", async () => {
    vi.mocked(routerApi.getClaudeAgent).mockResolvedValue(claudeStatus(account({ logged_in: false, email: "", login_command: "claude auth login" })));
    vi.mocked(routerApi.startClaudeSignIn).mockResolvedValue({ state: "failed", error: "browser did not open", started_at: null, finished_at: null });
    render(<SetupWizard step="connect" />);
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with Claude Code" }));
    const card = await screen.findByRole("alert", { name: "Sign-in of claude-code" });
    expect(card.textContent).toContain("browser did not open");
    expect(within(card).getByText("Sign in from a terminal instead")).toBeTruthy();
    expect(card.textContent).toContain("claude auth login");
  });

  it("notes when Claude Code is not installed", async () => {
    vi.mocked(routerApi.getClaudeAgent).mockResolvedValue(claudeStatus(null));
    render(<SetupWizard step="connect" />);
    expect(await screen.findByText(/Not installed/)).toBeTruthy();
    const link = screen.getByRole("link", { name: /Get Claude Code/ });
    expect(link.getAttribute("href")).toBe(CLAUDE_CODE_DOCS);
    expect(screen.queryByRole("checkbox", { name: "Use Claude Code" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Sign in with Claude Code" })).toBeNull();
  });
});

describe("Pick a model", () => {
  it("groups the connected providers with logos and refers to the Router", async () => {
    seedDraft({ connections: [OPENAI, DEEPSEEK] });
    render(<SetupWizard step="model" />);
    expect(screen.getByRole("heading", { level: 1, name: "Pick a model" })).toBeTruthy();
    expect(screen.getByText(/Change it later in Router → Providers or in the agent's settings\./)).toBeTruthy();
    expect(document.body.textContent).not.toContain("in Settings");

    const main = screen.getByRole("combobox", { name: "Main model" });
    await waitFor(() => expect(main.textContent).toContain("GPT-5 mini"));
    fireEvent.click(main);
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).getByText("OpenAI")).toBeTruthy();
    expect(within(listbox).getByText("DeepSeek")).toBeTruthy();
    expect(listbox.querySelector('[data-logo="deepseek"]')).not.toBeNull();
    expect(within(listbox).queryByText("Claude Code")).toBeNull();
  });

  it("saves main + fallback as the default combo", async () => {
    seedDraft({ connections: [OPENAI, DEEPSEEK] });
    render(<SetupWizard step="model" />);
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Main model" }).textContent).toContain("GPT-5 mini"));
    await pick("Main model", "GPT-5.1");
    // The fallback lists the other providers only.
    fireEvent.click(screen.getByRole("combobox", { name: "Fallback when it fails" }));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).queryByText("GPT-5 mini")).toBeNull();
    fireEvent.click(within(listbox).getByRole("option", { name: "DeepSeek V4 Pro" }));
    expect(screen.getByText(/saves the pair as the combo default/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(window.location.hash).toBe("#/setup/agent");
    const draft = storedDraft();
    expect(draft.model).toBe("openai/gpt-5.1");
    expect(draft.fallback).toBe("deepseek/deepseek-v4-pro");
    expect(draft.background).toBe("");
  });

  it("asks for a model ID under Other model ID", async () => {
    seedDraft({ connections: [OPENAI] });
    render(<SetupWizard step="model" />);
    await pick("Main model", "Other model ID…");
    const field = screen.getByLabelText("Model ID on OpenAI");
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(window.location.hash).toBe("");
    expect(screen.getByText("Pick a model or enter its ID.")).toBeTruthy();
    fireEvent.change(field, { target: { value: "gpt-6-preview" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(storedDraft().model).toBe("openai/gpt-6-preview");
  });

  it("gives a Claude Code main model a background model", async () => {
    seedDraft({ connections: [OPENAI], claudeCode: true, claudeAccount: { email: "me@lana.dev", plan: "max" } });
    render(<SetupWizard step="model" />);
    const main = screen.getByRole("combobox", { name: "Main model" });
    await waitFor(() => expect(main.textContent).toContain("Sonnet"));
    expect(main.querySelector('[data-logo="claude"]')).not.toBeNull();
    const note = screen.getByRole("note");
    expect(note.textContent).toContain("Background tasks (session titles, memory, and knowledge) never run on Claude Code");
    const background = screen.getByRole("combobox", { name: "Background model" });
    expect(background.textContent).toContain("GPT-5 mini");
    expect(storedDraft().background).toBe("openai/gpt-5-mini");

    // A main API model needs no background model.
    await pick("Main model", "GPT-5.1");
    expect(screen.queryByRole("combobox", { name: "Background model" })).toBeNull();
    expect(screen.queryByRole("note")).toBeNull();
    expect(storedDraft().background).toBe("");
  });

  it("says background tasks are skipped without an API key", () => {
    seedDraft({ connections: [], claudeCode: true, claudeAccount: { email: "", plan: "" } });
    render(<SetupWizard step="model" />);
    expect(screen.getByRole("note").textContent).toContain("Connect an API key to run them; until then they are skipped.");
    expect(screen.queryByRole("combobox", { name: "Background model" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Fallback when it fails" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(window.location.hash).toBe("#/setup/agent");
    expect(storedDraft().model).toBe("claude-code/sonnet");
  });
});

describe("Save and ready", () => {
  it("posts the providers, model, fallback, and background model", async () => {
    seedDraft({
      connections: [OPENAI, { id: "gemini", apiKey: "g-key", baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai", latencyMs: 1 }],
      claudeCode: true,
      claudeAccount: { email: "me@lana.dev", plan: "max" },
      model: "claude-code/sonnet",
      fallback: "openai/gpt-5.1",
      background: "openai/gpt-5-mini",
    });
    vi.mocked(setupApi.getDaemonStartedAt).mockResolvedValue(1);
    vi.mocked(setupApi.saveSetup).mockResolvedValue({ restart: "scheduled", pid: 1 });
    vi.mocked(setupApi.pollDaemonRestart).mockReturnValue(new Promise(() => {}));
    render(<SetupWizard step="persona" />);
    fireEvent.change(screen.getByLabelText("Persona · AGENT.md"), { target: { value: "# default" } });
    fireEvent.click(screen.getByRole("button", { name: "Save and continue" }));
    await screen.findByText("Restarting lana…");
    expect(vi.mocked(setupApi.saveSetup)).toHaveBeenCalledWith({
      providers: [
        { name: "openai", kind: "openai", apiKey: "sk-openai", baseUrl: "", apiKeyEnv: "OPENAI_API_KEY" },
        {
          name: "gemini",
          kind: "openai_compat",
          apiKey: "g-key",
          baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
          apiKeyEnv: "GEMINI_API_KEY",
        },
      ],
      model: "claude-code/sonnet",
      fallback: "openai/gpt-5.1",
      backgroundModel: "openai/gpt-5-mini",
      agent: { name: "default", template: "assistant", workspace: "~/lana/workspace", permission: "ask" },
      persona: { agentMd: "# default", userMd: "" },
    });
    expect(window.sessionStorage.getItem(DRAFT_KEY)).toBeNull();
    expect(JSON.parse(window.sessionStorage.getItem(SUMMARY_KEY) || "{}").providers).toEqual(["openai", "gemini"]);
  });

  it("summarizes providers, models, the combo, and the Claude Code account", () => {
    window.sessionStorage.setItem(
      SUMMARY_KEY,
      JSON.stringify({
        providers: ["openai", "deepseek"],
        claudeCode: true,
        claudeAccount: { email: "me@lana.dev", plan: "max" },
        model: "claude-code/sonnet",
        fallback: "deepseek/deepseek-flash",
        background: "openai/gpt-5-mini",
        agentName: "lana",
        template: "coder",
        permission: "ask",
        workspace: "~/code",
      })
    );
    const { container } = render(<SetupWizard step="ready" />);
    expect(screen.getByRole("heading", { level: 1, name: "lana is ready" })).toBeTruthy();
    const row = (label: string) => screen.getByText(label).closest(".ready-row") as HTMLElement;

    const connected = row("Connected");
    expect(connected.textContent).toContain("Claude Code");
    expect(connected.textContent).toContain("OpenAI");
    expect(connected.textContent).toContain("DeepSeek");
    for (const logo of ["claude", "openai", "deepseek"]) {
      expect(connected.querySelector('[data-logo="' + logo + '"]')).not.toBeNull();
    }
    expect(row("Main model").textContent).toContain("Claude Code · sonnet");
    expect(row("Fallback · combo default").textContent).toContain("DeepSeek · deepseek-flash");
    expect(row("Background model").textContent).toContain("OpenAI · gpt-5-mini");
    expect(row("Claude Code account").textContent).toContain("Signed in · me@lana.dev · Max");
    expect(row("Agent").textContent).toContain("lana · Coder");
    expect(container.textContent).toContain("Router → Providers");

    expect(screen.getByRole("link", { name: "Open Router" }).getAttribute("href")).toBe("#/router/providers");
    expect(screen.getByRole("link", { name: "Open agent lana" }).getAttribute("href")).toBe("#/agents/lana");
  });
});

describe("Set up your agent", () => {
  it("shows the agent validation messages and stays on the step", () => {
    render(<SetupWizard step="agent" />);

    const name = screen.getByLabelText("Name");
    const id = screen.getByLabelText("ID");
    const workspace = screen.getByLabelText("Workspace");
    fireEvent.change(name, { target: { value: "Omnix Lead" } });
    /* The ID fills in from the name until the owner edits it by hand. */
    expect((id as HTMLInputElement).value).toBe("omnix-lead");
    fireEvent.change(id, { target: { value: "Bad Name" } });
    fireEvent.change(workspace, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      screen.getByText("The ID is not valid. " + AGENT_ID_HINT)
    ).toBeTruthy();
    expect(screen.getByText("Enter a workspace path.")).toBeTruthy();
    expect(window.location.hash).toBe("");
    expect(screen.queryByText(/API key|Base URL|provider/i)).toBeNull();
  });

  it("goes back to the model step", () => {
    render(<SetupWizard step="agent" />);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(window.location.hash).toBe("#/setup/model");
  });
});

describe("setup shell", () => {
  it("shows the Lana brand and the step progress for the wizard", () => {
    const { container } = render(
      <SetupShell step="connect">
        <p>Connect a provider.</p>
      </SetupShell>
    );

    expect(container.querySelector(".auth")).not.toBeNull();
    const brand = container.querySelector(".auth-top .brand");
    expect(brand).not.toBeNull();
    expect(brand?.querySelector("img.lana-mark")).not.toBeNull();
    expect(brand?.textContent).toContain("LΛNΛ");

    // Step 2 of 5: two bars filled, the second one current.
    const bars = container.querySelectorAll(".auth-card .steps i");
    expect(bars.length).toBe(5);
    expect(container.querySelectorAll(".steps i.on").length).toBe(2);
    expect(container.querySelectorAll(".steps i.cur").length).toBe(1);
  });

  it("counts Pick a model as step 3", () => {
    const { container } = render(<SetupShell step="model" />);
    expect(container.querySelectorAll(".steps i.on").length).toBe(3);
    expect(container.textContent).toContain("Step 3 of 5");
  });

  it("drops the step progress on the door variant", () => {
    const { container } = render(
      <SetupShell step="unlock" variant="door">
        <p>Unlock lana.</p>
      </SetupShell>
    );

    expect(container.querySelector(".auth-card.fx-auth")).not.toBeNull();
    expect(container.querySelector(".steps")).toBeNull();
  });
});

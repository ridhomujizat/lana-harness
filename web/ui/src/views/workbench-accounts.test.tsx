import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

import { ApiError } from "../api/client";
import * as router from "../api/router";
import * as workbench from "../api/workbench";
import {
  AGY_FILE_TOKEN_ENV,
  AGY_MANAGE_HREF,
  AccountsPanel,
  CLAUDE_MANAGE_HREF,
  COLLAPSE_STORAGE_KEY,
  agyArgv,
  agyMostRoom,
  agyWindowLabel,
  agentHookCount,
  mostRoom,
  sortAccounts,
  type AccountActions,
} from "./workbench-accounts";

/* Spec 77 (REQ-WRA-003/004/005): the Workbench Accounts popup reads the
   Router's Claude Code and Antigravity accounts; no claude-swap. */
vi.mock("../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/router")>();
  return { ...actual, getClaudeAgent: vi.fn(), getAntigravity: vi.fn(), refreshRouterLimits: vi.fn() };
});
vi.mock("../api/workbench", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workbench")>();
  return {
    ...actual,
    getAgentHook: vi.fn(),
    setAgentHook: vi.fn(),
    getWorkbenchClaudeAccount: vi.fn(),
    setWorkbenchClaudeAccount: vi.fn(),
  };
});

const hour = 3_600_000;

function account(over: Partial<router.ClaudeAccount> & { name: string }): router.ClaudeAccount {
  return {
    builtin: false,
    config_dir: "",
    binary: "",
    version: "2.1.330",
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
    display_name: over.name,
    sign_in: { state: "idle", error: "", started_at: null, finished_at: null },
    priority: 0,
    pool: true,
    cooling_until: null,
    ...over,
  };
}

function win(type: string, utilization: number, label?: string): router.PlanWindow {
  return { type, status: "allowed", utilization, resets_at: new Date(Date.now() + 2 * hour).toISOString(), label };
}

const builtin = account({
  name: "claude-code",
  builtin: true,
  binary: "/opt/bin/claude",
  display_name: "Home",
  email: "me@home.dev",
  plan: "max",
  windows: [win("five_hour", 0.8), win("seven_day", 0.5)],
  priority: 0,
});
const work = account({
  name: "claude-work",
  config_dir: "/Users/me/.lana/claude/work",
  display_name: "Work",
  email: "me@work.dev",
  plan: "team",
  windows: [win("five_hour", 0.1), win("seven_day", 0.2), win("seven_day_opus", 0.42, "Weekly · Opus")],
  priority: 100,
});
const spare = account({
  name: "claude-spare",
  config_dir: "/Users/me/.lana/claude/spare",
  display_name: "Spare",
  logged_in: false,
  login_command: "CLAUDE_CONFIG_DIR=/Users/me/.lana/claude/spare claude auth login",
  priority: 50,
  cooling_until: new Date(Date.now() + hour).toISOString(),
});

function status(accounts: router.ClaudeAccount[]): router.ClaudeAgentStatus {
  return {
    configured_agents: [],
    binary: "/opt/bin/claude",
    version: "2.1.330",
    version_ok: true,
    logged_in: true,
    auth_method: "",
    error: "",
    running: 0,
    queued: 0,
    max_concurrent: 2,
    accounts,
  };
}

function agyAccount(over: Partial<router.AntigravityAccount> & { name: string }): router.AntigravityAccount {
  return {
    builtin: false,
    label: "",
    display_name: over.name,
    email: "",
    home: "",
    binary: "",
    disabled: false,
    allow_scheduled: false,
    signed_in: true,
    error: "",
    tier: "",
    models_count: 0,
    running: 0,
    queued: 0,
    windows: [],
    windows_updated_at: null,
    windows_error: "",
    usage_24h: { calls: 0, input_tokens: 0, output_tokens: 0 },
    agents: [],
    combos: [],
    sign_in: { state: "idle", url: "", error: "", started_at: null, finished_at: null },
    priority: 0,
    pool: true,
    cooling_until: null,
    ...over,
  };
}

function agyWin(group: string, type: string, remaining: number | null): router.AntigravityWindow {
  return { group, type, remaining, resets_at: new Date(Date.now() + 3 * hour).toISOString() };
}

const agyMain = agyAccount({
  name: "antigravity",
  builtin: true,
  display_name: "Agy Main",
  tier: "Pro",
  windows: [agyWin("Gemini", "5h", 0.3), agyWin("Gemini", "weekly", null)],
});
const agyLab = agyAccount({
  name: "agy-2",
  display_name: "Agy Lab",
  email: "lab@gmail.com",
  home: "/Users/me/.lana/agy/agy-2",
  tier: "Ultra",
  label: "research",
  windows: [agyWin("Gemini", "5h", 0.1), agyWin("Claude", "weekly", 0.75)],
  priority: 10,
});
const agyOff = agyAccount({
  name: "agy-3",
  display_name: "Agy Off",
  home: "/Users/me/.lana/agy/agy-3",
  signed_in: false,
  priority: 20,
});

function agyStatus(accounts: router.AntigravityAccount[], installed = true): router.AntigravityStatus {
  return {
    installed,
    binary: "/opt/bin/agy",
    version: "1.6.0",
    version_ok: true,
    min_version: "1.5.17",
    accounts,
    pool_warning: false,
  };
}

function actions() {
  return { newTab: vi.fn<AccountActions["newTab"]>(), beside: vi.fn<AccountActions["beside"]>() };
}

function row(name: string): HTMLElement {
  const item = screen.getByText(name).closest(".wb-acc-item");
  if (!item) throw new Error("no row for " + name);
  return item as HTMLElement;
}

beforeEach(() => {
  vi.mocked(router.getClaudeAgent).mockResolvedValue(status([work, spare, builtin]));
  vi.mocked(router.refreshRouterLimits).mockResolvedValue([]);
  vi.mocked(router.getAntigravity).mockResolvedValue(agyStatus([agyLab, agyOff, agyMain]));
  window.localStorage.clear();
  vi.mocked(workbench.getAgentHook).mockRejectedValue(new Error("no hooks in this test"));
  vi.mocked(workbench.getWorkbenchClaudeAccount).mockResolvedValue({ account: "claude-code", config_dir: "", supported: true });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("AccountsPanel: Claude Code", () => {
  it("lists the Router accounts: built-in first, then pool order, with tags and window bars", async () => {
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    const names = [...group("Claude Code").querySelectorAll(".wb-acc-name b")].map((b) => b.textContent);
    expect(names).toEqual(["Home", "Spare", "Work"]);
    expect(screen.getByRole("button", { name: "Claude Code, 3 accounts" })).toBeTruthy();
    expect(within(row("Home")).getByText("me@home.dev · Max")).toBeTruthy();

    await waitFor(() => expect(within(row("Home")).getByText("default")).toBeTruthy());
    expect(within(row("Work")).getByText("most room")).toBeTruthy();
    expect(within(row("Spare")).getByText("signed out")).toBeTruthy();
    expect(within(row("Spare")).getByText("cooling")).toBeTruthy();

    const labels = [...row("Work").querySelectorAll(".wb-acc-bar-label")].map((l) => l.textContent);
    expect(labels).toEqual(["5h", "7d", "Weekly · Opus"]);
    const pcts = [...row("Work").querySelectorAll(".wb-acc-pct")].map((l) => l.textContent);
    expect(pcts).toEqual(["10%", "20%", "42%"]);

    const manage = screen.getByRole("link", { name: "Manage Claude Code in Router" });
    expect(manage.getAttribute("href")).toBe(CLAUDE_MANAGE_HREF);
    expect(CLAUDE_MANAGE_HREF).toBe("#/router/providers/claude-code");
  });

  it("opens the built-in account with -u and a Router account with its folder; Beside adds --continue", async () => {
    const a = actions();
    const onClose = vi.fn();
    render(<AccountsPanel actions={a} onClose={onClose} />);
    await screen.findByText("Home");

    fireEvent.click(within(row("Home")).getByRole("button", { name: /New tab/ }));
    expect(a.newTab).toHaveBeenLastCalledWith([
      "env",
      "-u",
      "CLAUDE_CONFIG_DIR",
      "-u",
      "CLAUDE_SECURESTORAGE_CONFIG_DIR",
      "/opt/bin/claude",
    ]);

    fireEvent.click(within(row("Work")).getByRole("button", { name: /New tab/ }));
    expect(a.newTab).toHaveBeenLastCalledWith([
      "env",
      "CLAUDE_CONFIG_DIR=/Users/me/.lana/claude/work",
      "CLAUDE_SECURESTORAGE_CONFIG_DIR=/Users/me/.lana/claude/work",
      "claude",
    ]);

    fireEvent.click(within(row("Work")).getByRole("button", { name: /Beside/ }));
    expect(a.beside).toHaveBeenLastCalledWith([
      "env",
      "CLAUDE_CONFIG_DIR=/Users/me/.lana/claude/work",
      "CLAUDE_SECURESTORAGE_CONFIG_DIR=/Users/me/.lana/claude/work",
      "claude",
      "--continue",
    ]);
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it("offers Sign in only on a signed-out account and runs its login command", async () => {
    const a = actions();
    render(<AccountsPanel actions={a} onClose={() => {}} />);
    await screen.findByText("Home");
    expect(within(row("Home")).queryByRole("button", { name: /Sign in/ })).toBeNull();
    expect(within(row("Work")).queryByRole("button", { name: /Sign in/ })).toBeNull();

    fireEvent.click(within(row("Spare")).getByRole("button", { name: /Sign in/ }));
    expect(a.newTab).toHaveBeenCalledWith(["sh", "-c", spare.login_command], "Sign in Spare");
  });

  it("Use for new terminals stores the default and moves the tag", async () => {
    vi.mocked(workbench.setWorkbenchClaudeAccount).mockResolvedValue({
      account: "claude-work",
      config_dir: work.config_dir,
      supported: true,
    });
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    await waitFor(() => expect(within(row("Home")).getByText("default")).toBeTruthy());
    expect(within(row("Home")).queryByRole("button", { name: /Use for new terminals/ })).toBeNull();

    fireEvent.click(within(row("Work")).getByRole("button", { name: /Use for new terminals/ }));
    expect(workbench.setWorkbenchClaudeAccount).toHaveBeenCalledWith("claude-work");
    await waitFor(() => expect(within(row("Work")).getByText("default")).toBeTruthy());
    expect(within(row("Home")).queryByText("default")).toBeNull();
    expect(within(row("Home")).getByRole("button", { name: /Use for new terminals/ })).toBeTruthy();
    // The default never carries "most room".
    expect(within(row("Work")).queryByText("most room")).toBeNull();
  });

  it("an older daemon (404) keeps the built-in default and hides Use for new terminals", async () => {
    vi.mocked(workbench.getWorkbenchClaudeAccount).mockResolvedValue({ account: "claude-code", config_dir: "", supported: false });
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    await waitFor(() => expect(within(row("Home")).getByText("default")).toBeTruthy());
    expect(screen.queryByRole("button", { name: /Use for new terminals/ })).toBeNull();
  });

  it("opening shows the accounts at once, then probes usage in the background and rereads", async () => {
    let finish: () => void = () => {};
    vi.mocked(router.refreshRouterLimits).mockImplementationOnce(
      () => new Promise((resolve) => (finish = () => resolve([])))
    );
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    /* The list shows while the probe is still running. */
    await screen.findByText("Home");
    await waitFor(() => expect(router.refreshRouterLimits).toHaveBeenCalledTimes(1));
    expect(router.getClaudeAgent).toHaveBeenCalledTimes(1);
    expect(router.getAntigravity).toHaveBeenCalledTimes(1);
    finish();
    await waitFor(() => expect(router.getClaudeAgent).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(router.getAntigravity).toHaveBeenCalledTimes(2));
  });

  it("Refresh probes usage, then rereads the accounts", async () => {
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    await waitFor(() => expect(router.getClaudeAgent).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(router.refreshRouterLimits).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(router.getClaudeAgent).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(router.getAntigravity).toHaveBeenCalledTimes(3));
  });
});

function group(title: string): HTMLElement {
  return screen.getByRole("group", { name: title });
}

describe("AccountsPanel: groups, search, Antigravity", () => {
  it("renders both provider groups with logos and counts", async () => {
    const { container } = render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    await screen.findByText("Agy Main");
    expect(screen.getByText("Accounts")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Claude Code, 3 accounts" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Antigravity, 3 accounts" })).toBeTruthy();
    const logos = [...container.querySelectorAll(".wb-acc-gh [data-agent]")].map((l) => l.getAttribute("data-agent"));
    expect(logos).toEqual(["claude", "agy"]);
    const agyNames = [...group("Antigravity").querySelectorAll(".wb-acc-name b")].map((b) => b.textContent);
    expect(agyNames).toEqual(["Agy Main", "Agy Lab", "Agy Off"]);
    expect(screen.getByRole("link", { name: "Manage Antigravity in Router" }).getAttribute("href")).toBe(AGY_MANAGE_HREF);
    expect(AGY_MANAGE_HREF).toBe("#/router/providers/antigravity");
  });

  /* Spec 77 revision 4: a clean, grouped layout. */
  it("draws sections with sticky headings, flat rows, and dividers", async () => {
    vi.mocked(workbench.getAgentHook).mockResolvedValue({
      state: "on",
      enabled: true,
      needs_install: false,
      folders: [{ dir: "/Users/me/.claude", account: "claude-code", installed: true, kind: "claude" }],
    });
    const { container } = render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Agy Main");
    const panel = container.querySelector(".wb-acc") as HTMLElement;
    /* Search on top (its own bar, a divider under it), then the scroll area,
       then the hook footer. */
    await waitFor(() =>
      expect([...panel.children].map((el) => el.className)).toEqual(["wb-acc-top-bar", "wb-acc-scroll", "wb-acc-hook"])
    );
    expect(panel.querySelector(".wb-acc-top-bar .wb-acc-search")).toBeTruthy();
    const sections = [...panel.querySelectorAll(".wb-acc-scroll > .wb-acc-group")];
    expect(sections.map((el) => el.getAttribute("aria-label"))).toEqual(["Claude Code", "Antigravity"]);
    for (const section of sections) {
      /* Heading: chevron, logo, name, count pill, Manage on the right. */
      const head = section.querySelector(":scope > .wb-acc-gh") as HTMLElement;
      expect(head.querySelector(".wb-acc-gh-chev")).toBeTruthy();
      expect(head.querySelector("[data-agent]")).toBeTruthy();
      expect(head.querySelector(".wb-acc-gh-count")?.textContent).toBe("3");
      expect(head.lastElementChild?.className).toBe("wb-acc-manage");
      expect(section.className).toContain("is-open");
      /* Rows are a flat list. */
      const list = section.querySelector(".wb-acc-gbody > ul.wb-acc-list") as HTMLElement;
      expect([...list.children].every((el) => el.tagName === "LI" && el.classList.contains("wb-acc-item"))).toBe(true);
      expect(list.children).toHaveLength(3);
    }
    expect(within(group("Claude Code")).getByRole("list", { name: "Claude Code accounts" })).toBeTruthy();
    expect(within(group("Antigravity")).getByRole("list", { name: "Antigravity accounts" })).toBeTruthy();
  });

  it("shows the agy email before the tier and finds an account by its email", async () => {
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Agy Lab");
    expect(within(row("Agy Lab")).getByText("lab@gmail.com · Ultra")).toBeTruthy();
    /* No email: the tier alone. */
    expect(within(row("Agy Main")).getByText("Pro")).toBeTruthy();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search accounts" }), { target: { value: "LAB@GMAIL" } });
    expect(screen.getByText("Agy Lab")).toBeTruthy();
    expect(screen.queryByText("Agy Main")).toBeNull();
    expect(screen.queryByRole("group", { name: "Claude Code" })).toBeNull();
    expect(screen.getByRole("button", { name: "Antigravity, 1 account" })).toBeTruthy();
  });

  it("shows Antigravity tags and bars: most room, signed out, used percent, – for unknown", async () => {
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Agy Main");
    expect(within(row("Agy Main")).getByText("most room")).toBeTruthy();
    expect(within(row("Agy Main")).queryByText("default")).toBeNull();
    expect(within(row("Agy Off")).getByText("signed out")).toBeTruthy();
    const labels = [...row("Agy Main").querySelectorAll(".wb-acc-bar-label")].map((l) => l.textContent);
    expect(labels).toEqual(["Gemini 5h", "Gemini wk"]);
    const pcts = [...row("Agy Main").querySelectorAll(".wb-acc-pct")].map((l) => l.textContent);
    expect(pcts).toEqual(["70%", "–"]);
    expect(within(group("Antigravity")).queryByRole("button", { name: /Use for new terminals/ })).toBeNull();
  });

  it("collapses a group and remembers it", async () => {
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Agy Main");
    const toggle = screen.getByRole("button", { name: /^Antigravity,/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Agy Main")).toBeNull();
    expect(screen.getByText("Home")).toBeTruthy();
    expect(JSON.parse(window.localStorage.getItem(COLLAPSE_STORAGE_KEY) || "{}")).toMatchObject({ agy: true });

    cleanup();
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    await waitFor(() => expect(screen.getByRole("button", { name: /^Antigravity,/ }).getAttribute("aria-expanded")).toBe("false"));
    expect(screen.queryByText("Agy Main")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^Antigravity,/ }));
    expect(await screen.findByText("Agy Main")).toBeTruthy();
  });

  it("search filters both groups, expands collapsed matches, hides empty groups", async () => {
    window.localStorage.setItem(COLLAPSE_STORAGE_KEY, JSON.stringify({ claude: true, agy: true }));
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await waitFor(() => expect(screen.getByRole("button", { name: /^Antigravity,/ })).toBeTruthy());
    expect(screen.queryByText("Home")).toBeNull();
    const box = screen.getByRole("searchbox", { name: "Search accounts" });

    // "work" matches the Claude account by name/email only.
    fireEvent.change(box, { target: { value: "WORK" } });
    expect(screen.getByText("Work")).toBeTruthy();
    expect(screen.queryByText("Home")).toBeNull();
    expect(screen.queryByRole("group", { name: "Antigravity" })).toBeNull();
    expect(screen.getByRole("button", { name: "Claude Code, 1 account" })).toBeTruthy();

    // Tier and label match Antigravity accounts.
    fireEvent.change(box, { target: { value: "ultra" } });
    expect(screen.getByText("Agy Lab")).toBeTruthy();
    expect(screen.queryByRole("group", { name: "Claude Code" })).toBeNull();
    fireEvent.change(box, { target: { value: "research" } });
    expect(screen.getByText("Agy Lab")).toBeTruthy();

    // Plan matches across groups: "max" is Home's plan.
    fireEvent.change(box, { target: { value: "max" } });
    expect(screen.getByText("Home")).toBeTruthy();

    fireEvent.change(box, { target: { value: "nobody-here" } });
    expect(screen.getByText("No accounts match")).toBeTruthy();
    expect(screen.queryByRole("group", { name: "Claude Code" })).toBeNull();
    expect(screen.queryByRole("group", { name: "Antigravity" })).toBeNull();

    // Escape clears the search first and keeps the popup open.
    const outer = vi.fn();
    document.addEventListener("keydown", outer);
    fireEvent.keyDown(box, { key: "Escape" });
    document.removeEventListener("keydown", outer);
    expect((box as HTMLInputElement).value).toBe("");
    expect(outer).not.toHaveBeenCalled();
    // Collapsed again once the search is gone.
    expect(screen.queryByText("Home")).toBeNull();
  });

  it("opens agy with the account's HOME, plain for the built-in; Beside adds --continue", async () => {
    const a = actions();
    const onClose = vi.fn();
    render(<AccountsPanel actions={a} onClose={onClose} />);
    await screen.findByText("Agy Main");

    fireEvent.click(within(row("Agy Main")).getByRole("button", { name: /New tab/ }));
    expect(a.newTab).toHaveBeenLastCalledWith(["/opt/bin/agy"]);
    fireEvent.click(within(row("Agy Main")).getByRole("button", { name: /Beside/ }));
    expect(a.beside).toHaveBeenLastCalledWith(["/opt/bin/agy", "--continue"]);

    fireEvent.click(within(row("Agy Lab")).getByRole("button", { name: /New tab/ }));
    expect(a.newTab).toHaveBeenLastCalledWith([
      "env",
      "HOME=/Users/me/.lana/agy/agy-2",
      "SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0",
      "/opt/bin/agy",
    ]);
    fireEvent.click(within(row("Agy Lab")).getByRole("button", { name: /Beside/ }));
    expect(a.beside).toHaveBeenLastCalledWith([
      "env",
      "HOME=/Users/me/.lana/agy/agy-2",
      "SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0",
      "/opt/bin/agy",
      "--continue",
    ]);
    expect(onClose).toHaveBeenCalledTimes(4);
  });

  it("a signed-out agy account links to the Router instead of Sign in", async () => {
    const onClose = vi.fn();
    render(<AccountsPanel actions={actions()} onClose={onClose} />);
    await screen.findByText("Agy Off");
    expect(within(row("Agy Off")).queryByRole("button", { name: /Sign in/ })).toBeNull();
    const link = within(row("Agy Off")).getByRole("link", { name: /Sign in in Router/ });
    expect(link.getAttribute("href")).toBe(AGY_MANAGE_HREF);
    fireEvent.click(link);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(within(row("Agy Lab")).queryByRole("link", { name: /Sign in in Router/ })).toBeNull();
  });

  it("hides Antigravity when it is not installed and has no accounts", async () => {
    vi.mocked(router.getAntigravity).mockResolvedValue(agyStatus([], false));
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    await waitFor(() => expect(router.getAntigravity).toHaveBeenCalled());
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.queryByRole("group", { name: "Antigravity" })).toBeNull();
    expect(screen.getByRole("group", { name: "Claude Code" })).toBeTruthy();
    cleanup();

    // Not installed but with an account: still listed.
    vi.mocked(router.getAntigravity).mockResolvedValue(agyStatus([agyLab], false));
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    expect(await screen.findByRole("group", { name: "Antigravity" })).toBeTruthy();
  });

  it("one group's fetch failing still shows the other", async () => {
    vi.mocked(router.getClaudeAgent).mockRejectedValue(new ApiError(500, "boom", "claude down"));
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Agy Main");
    expect(within(group("Claude Code")).getByText("claude down")).toBeTruthy();
    expect(within(group("Antigravity")).queryByText("claude down")).toBeNull();
    cleanup();

    vi.mocked(router.getClaudeAgent).mockResolvedValue(status([builtin]));
    vi.mocked(router.getAntigravity).mockRejectedValue(new ApiError(500, "boom", "agy down"));
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    await screen.findByText("Home");
    expect(await within(group("Antigravity")).findByText("agy down")).toBeTruthy();
    expect(within(group("Claude Code")).queryByText("agy down")).toBeNull();
  });
});

describe("getWorkbenchClaudeAccount", () => {
  it("reads 404 from an older daemon as the built-in account, unsupported", async () => {
    const actual = await vi.importActual<typeof import("../api/workbench")>("../api/workbench");
    const fetchMock = vi.fn().mockResolvedValue(new Response("404 page not found", { status: 404 }));
    vi.stubGlobal("fetch", fetchMock);
    try {
      await expect(actual.getWorkbenchClaudeAccount()).resolves.toEqual({
        account: "claude-code",
        config_dir: "",
        supported: false,
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe("account helpers", () => {
  it("sortAccounts puts the built-in first, then priority, then name", () => {
    const b = account({ name: "b", display_name: "B", priority: 10 });
    const a = account({ name: "a", display_name: "A", priority: 10 });
    const z = account({ name: "z", display_name: "Z", priority: 1 });
    expect(sortAccounts([b, a, z, builtin]).map((x) => x.name)).toEqual(["claude-code", "z", "a", "b"]);
  });

  it("mostRoom skips signed-out and cooling accounts and uses the tightest window", () => {
    expect(mostRoom([builtin, work, spare])).toBe("claude-work");
    const tight = account({ name: "t", windows: [win("five_hour", 0.05), win("seven_day", 0.9)] });
    expect(mostRoom([builtin, tight])).toBe("claude-code");
    expect(mostRoom([spare])).toBeNull();
  });

  it("agyMostRoom uses the highest minimum remaining; skips signed-out, disabled, cooling", () => {
    expect(agyMostRoom([agyMain, agyLab, agyOff])).toBe("antigravity");
    const roomy = agyAccount({ name: "r", windows: [agyWin("Gemini", "5h", 0.95)], disabled: true });
    expect(agyMostRoom([agyLab, roomy])).toBe("agy-2");
    const cool = agyAccount({
      name: "c",
      windows: [agyWin("Gemini", "5h", 0.95)],
      cooling_until: new Date(Date.now() + hour).toISOString(),
    });
    expect(agyMostRoom([agyLab, cool])).toBe("agy-2");
    expect(agyMostRoom([agyOff])).toBeNull();
  });

  it("agyArgv prefers the account binary, then the status binary, then agy", () => {
    expect(agyArgv(agyMain)).toEqual(["agy"]);
    expect(agyArgv({ ...agyMain, binary: "/x/agy" }, "/opt/bin/agy")).toEqual(["/x/agy"]);
    expect(agyArgv(agyLab, "/opt/bin/agy")).toEqual(["env", "HOME=/Users/me/.lana/agy/agy-2", AGY_FILE_TOKEN_ENV, "/opt/bin/agy"]);
    /* Spec 77 revision 4: agy uses its file token storage, no Keychain dialog. */
    expect(AGY_FILE_TOKEN_ENV).toBe("SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0");
    expect(agyArgv({ ...agyLab, home: "" }, "/opt/bin/agy")).toEqual([]);
  });

  it("agyWindowLabel shortens the span", () => {
    expect(agyWindowLabel(agyWin("Gemini", "5h", 1))).toBe("Gemini 5h");
    expect(agyWindowLabel(agyWin("Claude", "weekly", 1))).toBe("Claude wk");
    expect(agyWindowLabel(agyWin("", "daily", 1))).toBe("daily");
  });
});

/* Spec 77 REQ-WRA-009: the hook switch covers Claude Code and Antigravity and
   counts the folders of both kinds. */
describe("Agent hook switch", () => {
  const claudeDir = { dir: "/Users/me/.claude", account: "claude-code", kind: "claude" as const, installed: true };
  const agyDir = { dir: "/Users/me/.gemini/config", account: "antigravity", kind: "antigravity" as const, installed: true };

  it("counts both kinds; a folder without a kind is Claude", () => {
    expect(agentHookCount([claudeDir, { ...claudeDir, dir: "/w", kind: undefined }, agyDir])).toBe(
      "Installed in 3 of 3 folders (Claude 2, Antigravity 1).",
    );
    expect(agentHookCount([claudeDir, { ...agyDir, installed: false }])).toBe(
      "Installed in 1 of 2 folders (Claude 1, Antigravity 1).",
    );
    expect(agentHookCount([claudeDir])).toBe("Installed in 1 of 1 folders (Claude 1).");
    expect(agentHookCount([])).toBe("Installed in 0 of 0 folders.");
  });

  it("shows the switch with the shared title and the count", async () => {
    vi.mocked(workbench.getAgentHook).mockResolvedValue({
      enabled: true,
      state: "on",
      needs_install: false,
      folders: [claudeDir, agyDir],
    });
    render(<AccountsPanel actions={actions()} onClose={() => {}} />);
    const sw = await screen.findByRole("switch");
    expect(within(sw).getByText("Connect Claude Code and Antigravity to the Workbench")).toBeTruthy();
    expect(sw.textContent).toContain("Installed in 2 of 2 folders (Claude 1, Antigravity 1).");
  });
});

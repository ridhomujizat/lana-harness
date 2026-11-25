import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactElement } from "react";

/* Spec 74 (API-AGY-020..033): the Antigravity page, its sign-in panel, the
   Subscriptions card, and the model picker groups. The API module is
   mocked with the contract shapes. */
vi.mock("../../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/router")>();
  return {
    ...actual,
    getAntigravity: vi.fn(),
    createAntigravityAccount: vi.fn(),
    startAntigravitySignIn: vi.fn(),
    getAntigravitySignIn: vi.fn(),
    cancelAntigravitySignIn: vi.fn(),
    sendAntigravitySignInCode: vi.fn(),
    setAntigravityAccountLabel: vi.fn(),
    deleteProvider: vi.fn(),
    savePoolOrder: vi.fn(),
    listProviders: vi.fn(),
    refreshRouterLimits: vi.fn(),
    getClaudeAgent: vi.fn(),
    getRouter: vi.fn(),
  };
});

import * as api from "../../api/router";
import type { AntigravityAccount, AntigravitySignIn, AntigravityStatus } from "../../api/router";
import { choiceOptions } from "../agent-runtime";
import { POOL_TERMS_NOTE } from "./account-order";
import { AGY_CODE_HINT, AGY_SIGN_IN_POLL_MS, AGY_TERMS_NOTE, antigravityWindows } from "./antigravity-page";
import { groupClaudeChoices } from "./claude-code";
import { ProviderDetail } from "./provider-detail";
import { RouterProviders } from "./providers";
import { isClaudeLimit } from "./limits-refresh";

function agyAccount(overrides: Partial<AntigravityAccount> & { name: string }): AntigravityAccount {
  return {
    builtin: false,
    label: "",
    display_name: "",
    home: "/Users/x/.lana/antigravity/" + overrides.name,
    binary: "/usr/local/bin/agy",
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
    priority: 100,
    pool: true,
    cooling_until: null,
    ...overrides,
  };
}

function agyStatus(overrides: Partial<AntigravityStatus> = {}): AntigravityStatus {
  return {
    installed: true,
    binary: "/usr/local/bin/agy",
    version: "1.5.17",
    version_ok: true,
    min_version: "1.5.17",
    pool_warning: false,
    accounts: [agyAccount({ name: "antigravity", builtin: true, home: "", display_name: "antigravity", priority: 0 })],
    ...overrides,
  };
}

/* AC-AGY-015: two signed-in accounts with their own tier, windows, usage. */
const TWO: AntigravityStatus = agyStatus({
  pool_warning: true,
  accounts: [
    agyAccount({
      name: "antigravity",
      builtin: true,
      home: "",
      display_name: "antigravity",
      tier: "Free",
      priority: 0,
      windows: [
        { group: "Gemini", type: "5h", remaining: 0.58, resets_at: "2099-11-25T10:00:00Z" },
        { group: "Gemini", type: "weekly", remaining: 0.9, resets_at: "2099-11-30T10:00:00Z" },
      ],
      windows_updated_at: new Date().toISOString(),
      usage_24h: { calls: 12, input_tokens: 45000, output_tokens: 3000 },
      agents: ["lana"],
    }),
    agyAccount({
      name: "agy-2",
      label: "Work",
      display_name: "Work",
      tier: "Pro",
      models_count: 6,
      windows: [{ group: "Claude", type: "5h", remaining: 0.2, resets_at: "2099-11-25T11:00:00Z" }],
      usage_24h: { calls: 3, input_tokens: 9000, output_tokens: 1000 },
      combos: ["work"],
    }),
  ],
});

const SIGN_IN = (state: AntigravitySignIn["state"], extra: Partial<AntigravitySignIn> = {}): AntigravitySignIn => ({
  state,
  url: "",
  error: "",
  started_at: "2026-11-25T10:00:00Z",
  finished_at: null,
  ...extra,
});

const GOOGLE_URL = "https://accounts.google.com/o/oauth2/auth?client_id=x&state=y";

function renderPage(): ReturnType<typeof render> {
  const page: ReactElement = <ProviderDetail id="antigravity" />;
  return render(page);
}

beforeEach(() => {
  vi.mocked(api.listProviders).mockResolvedValue({ version: "v1", restart_required: false, providers: [] });
  vi.mocked(api.refreshRouterLimits).mockResolvedValue([]);
  vi.mocked(api.savePoolOrder).mockResolvedValue({ version: "v2", restart_required: false, pool: [] });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Antigravity page", () => {
  it("asks to install the CLI when agy is missing", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(
      agyStatus({
        installed: false,
        binary: "",
        version: "",
        version_ok: false,
        accounts: [agyAccount({ name: "antigravity", builtin: true, signed_in: false, home: "", binary: "" })],
      })
    );
    renderPage();
    const note = await screen.findByRole("note", { name: "Install the Antigravity CLI" });
    expect(note.textContent).toContain("1.5.17 or newer");
    expect((screen.getByRole("button", { name: "Add account" }) as HTMLButtonElement).disabled).toBe(true);
    const row = screen.getByRole("listitem", { name: "Account antigravity" });
    expect(row.textContent).toContain("Run agy in a terminal to sign in");
    expect(within(row).getByText("agy").tagName).toBe("CODE");
    /* The built-in account is never signed in or removed from here. */
    expect(within(row).queryByRole("button", { name: "Sign in" })).toBeNull();
    expect(within(row).queryByRole("button", { name: "Remove antigravity" })).toBeNull();
  });

  it("asks to update an agy older than 1.5.17", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(agyStatus({ version: "1.5.10", version_ok: false }));
    renderPage();
    const note = await screen.findByRole("note", { name: "Install the Antigravity CLI" });
    expect(note.textContent).toContain("agy 1.5.10 is too old");
  });

  it("shows each account with its tier, windows per group, usage, and the terms notes", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    renderPage();
    const home = await screen.findByRole("listitem", { name: "Account antigravity" });
    const work = screen.getByRole("listitem", { name: "Account agy-2" });
    expect(home.textContent).toContain("Signed in with your own agy login");
    expect(within(home).getAllByText("Free").length).toBeGreaterThan(0);
    expect(within(home).getByText("Gemini · 5 hours")).toBeTruthy();
    expect(within(home).getByText("Gemini · weekly")).toBeTruthy();
    expect(within(home).getByText(/^42 % used · resets /)).toBeTruthy();
    expect(within(home).getByText("12 calls · 45k in / 3k out")).toBeTruthy();
    expect(within(work).getByText("Work")).toBeTruthy();
    expect(within(work).getAllByText("Pro").length).toBeGreaterThan(0);
    expect(within(work).getByText("Claude · 5 hours")).toBeTruthy();
    expect(within(work).getByText(/^80 % used/)).toBeTruthy();
    expect(within(work).getByText("3 calls · 9k in / 1k out")).toBeTruthy();
    expect(within(work).getByText("Signed in")).toBeTruthy();
    expect(within(work).getByRole("button", { name: "Rename agy-2" })).toBeTruthy();
    expect(screen.getByText(AGY_TERMS_NOTE)).toBeTruthy();
    expect(screen.getByText(POOL_TERMS_NOTE)).toBeTruthy();
    expect(screen.getByText(/^Updated /)).toBeTruthy();
    expect(screen.getByRole("list", { name: "Antigravity account order" })).toBeTruthy();
  });

  it("refreshes the quota windows", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    renderPage();
    await screen.findByRole("listitem", { name: "Account agy-2" });
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(vi.mocked(api.refreshRouterLimits)).toHaveBeenCalled());
    await waitFor(() => expect(vi.mocked(api.getAntigravity)).toHaveBeenCalledTimes(2));
  });

  it("saves the account order with runtime antigravity", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    renderPage();
    const list = await screen.findByRole("list", { name: "Antigravity account order" });
    fireEvent.click(within(list).getByRole("checkbox", { name: "Use antigravity as backup" }));
    await waitFor(() =>
      expect(vi.mocked(api.savePoolOrder)).toHaveBeenCalledWith("antigravity", ["agy-2"], ["antigravity"], "v1")
    );
  });

  /* AC-AGY-013 (UI side): add, open Google, paste the code, signed in. */
  it("adds an account and follows the Google sign-in with the pasted code", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.getAntigravity).mockResolvedValue(agyStatus());
      vi.mocked(api.createAntigravityAccount).mockResolvedValue({
        name: "agy-2",
        home: "/Users/x/.lana/antigravity/agy-2",
        version: "v2",
        restart_required: false,
        sign_in: SIGN_IN("starting"),
      });
      vi.mocked(api.getAntigravitySignIn).mockResolvedValue(SIGN_IN("waiting_code", { url: GOOGLE_URL }));
      vi.mocked(api.sendAntigravitySignInCode).mockResolvedValue(SIGN_IN("running"));
      renderPage();
      fireEvent.click(await screen.findByRole("button", { name: "Add account" }));
      const starting = await screen.findByRole("status", { name: "Sign-in of agy-2" });
      expect(starting.textContent).toContain("Starting Antigravity sign-in…");
      expect(vi.mocked(api.createAntigravityAccount)).toHaveBeenCalledWith();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(AGY_SIGN_IN_POLL_MS);
      });
      expect(vi.mocked(api.getAntigravitySignIn)).toHaveBeenCalledWith("agy-2");
      const panel = await screen.findByRole("group", { name: "Sign-in of agy-2" });
      const link = within(panel).getByRole("link", { name: "Open Google sign-in" });
      expect(link.getAttribute("href")).toBe(GOOGLE_URL);
      expect(link.getAttribute("target")).toBe("_blank");
      expect(link.getAttribute("rel")).toBe("noopener noreferrer");
      expect(panel.textContent).toContain(AGY_CODE_HINT);
      const input = within(panel).getByRole("textbox", { name: "Code from Google for agy-2" }) as HTMLInputElement;
      expect(input.getAttribute("autocomplete")).toBe("off");
      expect(input.tagName).toBe("INPUT");
      fireEvent.change(input, { target: { value: "  4/0AbCdEf  " } });
      fireEvent.click(within(panel).getByRole("button", { name: "Send" }));
      await waitFor(() => expect(vi.mocked(api.sendAntigravitySignInCode)).toHaveBeenCalledWith("agy-2", "4/0AbCdEf"));
      expect(input.value).toBe("");
      /* The code is kept nowhere. */
      for (let index = 0; index < window.localStorage.length; index += 1) {
        const key = window.localStorage.key(index) || "";
        expect(String(window.localStorage.getItem(key))).not.toContain("4/0AbCdEf");
      }
      const running = await screen.findByRole("status", { name: "Sign-in of agy-2" });
      expect(running.textContent).toContain("Signing in…");

      vi.mocked(api.getAntigravitySignIn).mockResolvedValue(SIGN_IN("succeeded", { finished_at: "2026-11-25T10:01:00Z" }));
      vi.mocked(api.getAntigravity).mockResolvedValue(
        agyStatus({
          accounts: [...agyStatus().accounts, agyAccount({ name: "agy-2", tier: "Pro", display_name: "agy-2" })],
        })
      );
      await act(async () => {
        await vi.advanceTimersByTimeAsync(AGY_SIGN_IN_POLL_MS);
      });
      const row = await screen.findByRole("listitem", { name: "Account agy-2" });
      await waitFor(() => expect(row.className).toContain("is-new"));
      expect(screen.queryByRole("status", { name: "Sign-in of agy-2" })).toBeNull();
      const polls = vi.mocked(api.getAntigravitySignIn).mock.calls.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(AGY_SIGN_IN_POLL_MS * 2);
      });
      expect(vi.mocked(api.getAntigravitySignIn).mock.calls.length).toBe(polls);
    } finally {
      vi.useRealTimers();
    }
  });

  it("picks up a waiting sign-in and keeps the panel open on a refused code", async () => {
    const { ApiError } = await import("../../api/client");
    vi.mocked(api.getAntigravity).mockResolvedValue(
      agyStatus({
        accounts: [
          agyAccount({ name: "agy-2", signed_in: false, sign_in: SIGN_IN("waiting_code", { url: GOOGLE_URL }) }),
        ],
      })
    );
    vi.mocked(api.sendAntigravitySignInCode).mockRejectedValue(new ApiError(422, "bad_code", "bad code"));
    renderPage();
    const row = await screen.findByRole("listitem", { name: "Account agy-2" });
    /* The sign-in already waits on the daemon: the panel opens by itself. */
    expect(within(row).queryByRole("button", { name: "Sign in" })).toBeNull();
    const panel = await within(row).findByRole("group", { name: "Sign-in of agy-2" });
    const input = within(panel).getByRole("textbox") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "wrong" } });
    fireEvent.submit(input.closest("form") as HTMLFormElement);
    expect(await within(panel).findByRole("alert")).toBeTruthy();
    expect(panel.textContent).toContain("That code was not accepted");
    expect(input.value).toBe("");
  });

  it("shows a failed sign-in with Try again", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(api.getAntigravity).mockResolvedValue(
        agyStatus({ accounts: [agyAccount({ name: "agy-2", signed_in: false, label: "Work", display_name: "Work" })] })
      );
      vi.mocked(api.startAntigravitySignIn).mockResolvedValue(SIGN_IN("starting"));
      vi.mocked(api.getAntigravitySignIn).mockResolvedValue(
        SIGN_IN("failed", { error: "agy exited with status 1: login timed out" })
      );
      renderPage();
      const row = await screen.findByRole("listitem", { name: "Account agy-2" });
      expect(within(row).getByText("Not signed in")).toBeTruthy();
      fireEvent.click(within(row).getByRole("button", { name: "Sign in" }));
      await waitFor(() => expect(vi.mocked(api.startAntigravitySignIn)).toHaveBeenCalledWith("agy-2"));
      expect(within(row).queryByRole("button", { name: "Sign in" })).toBeNull();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(AGY_SIGN_IN_POLL_MS);
      });
      const alert = await within(row).findByRole("alert", { name: "Sign-in of agy-2" });
      expect(alert.textContent).toContain("agy exited with status 1: login timed out");
      vi.mocked(api.getAntigravitySignIn).mockResolvedValue(SIGN_IN("starting"));
      fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
      await waitFor(() => expect(vi.mocked(api.startAntigravitySignIn)).toHaveBeenCalledTimes(2));
      expect(await within(row).findByRole("status", { name: "Sign-in of agy-2" })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("cancels a sign-in that waits for the code", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(
      agyStatus({ accounts: [agyAccount({ name: "agy-2", signed_in: false })] })
    );
    vi.mocked(api.startAntigravitySignIn).mockResolvedValue(SIGN_IN("waiting_code", { url: GOOGLE_URL }));
    vi.mocked(api.cancelAntigravitySignIn).mockResolvedValue(SIGN_IN("cancelled"));
    renderPage();
    const row = await screen.findByRole("listitem", { name: "Account agy-2" });
    fireEvent.click(within(row).getByRole("button", { name: "Sign in" }));
    const panel = await within(row).findByRole("group", { name: "Sign-in of agy-2" });
    fireEvent.click(within(panel).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(vi.mocked(api.cancelAntigravitySignIn)).toHaveBeenCalledWith("agy-2"));
    expect((await within(row).findByRole("status", { name: "Sign-in of agy-2" })).textContent).toContain(
      "Sign-in cancelled."
    );
  });

  it("removes an account with its folder after a confirm", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    vi.mocked(api.deleteProvider).mockResolvedValue({
      name: "agy-2",
      version: "v2",
      restart_required: false,
      folder_removed: true,
    });
    renderPage();
    const row = await screen.findByRole("listitem", { name: "Account agy-2" });
    fireEvent.click(within(row).getByRole("button", { name: "Remove agy-2" }));
    const dialog = await screen.findByRole("dialog");
    const folder = within(dialog).getByRole("checkbox", { name: /Also delete the account folder/ }) as HTMLInputElement;
    expect(folder.checked).toBe(true);
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() =>
      expect(vi.mocked(api.deleteProvider)).toHaveBeenCalledWith("agy-2", "v1", { removeFolder: true })
    );
    expect(await screen.findByText(/Its folder was deleted/)).toBeTruthy();
  });

  it("keeps the folder when the box is cleared", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    vi.mocked(api.deleteProvider).mockResolvedValue({ name: "agy-2", version: "v2", restart_required: false });
    renderPage();
    const row = await screen.findByRole("listitem", { name: "Account agy-2" });
    fireEvent.click(within(row).getByRole("button", { name: "Remove agy-2" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /Also delete the account folder/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() =>
      expect(vi.mocked(api.deleteProvider)).toHaveBeenCalledWith("agy-2", "v1", { removeFolder: false })
    );
  });

  it("renames an account label", async () => {
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    vi.mocked(api.setAntigravityAccountLabel).mockResolvedValue({
      name: "agy-2",
      label: "Side",
      version: "v2",
      restart_required: false,
    });
    renderPage();
    const row = await screen.findByRole("listitem", { name: "Account agy-2" });
    fireEvent.click(within(row).getByRole("button", { name: "Rename agy-2" }));
    const input = within(row).getByRole("textbox", { name: "Label of agy-2" });
    fireEvent.change(input, { target: { value: "Side" } });
    fireEvent.click(within(row).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(vi.mocked(api.setAntigravityAccountLabel)).toHaveBeenCalledWith("agy-2", "Side", "v1")
    );
  });
});

describe("Antigravity elsewhere", () => {
  it("shows the Antigravity card under Subscriptions and keeps its accounts out of compatible providers", async () => {
    vi.mocked(api.getClaudeAgent).mockRejectedValue(new Error("down"));
    vi.mocked(api.getAntigravity).mockResolvedValue(TWO);
    vi.mocked(api.listProviders).mockResolvedValue({
      version: "v1",
      restart_required: false,
      providers: [
        { name: "agy-2", kind: "antigravity", base_url: "", api_key_env: "", key_state: "", used_by: [] },
      ],
    });
    render(<RouterProviders />);
    const card = await screen.findByRole("article", { name: "Antigravity" });
    expect(card.querySelector("[data-logo='antigravity']")).toBeTruthy();
    expect(within(card).getByRole("link").getAttribute("href")).toBe("#/router/providers/antigravity");
    expect(await within(card).findByText("2 accounts · 2 signed in")).toBeTruthy();
    expect(screen.queryByRole("article", { name: "agy-2" })).toBeNull();
  });

  it("groups Antigravity models with the Antigravity logo in the model picker", () => {
    const choices: api.RouterChoice[] = [
      { value: "coding", label: "coding", group: "Combos" },
      { value: "antigravity/gemini-4-neon-high", label: "Antigravity · Gemini 4 Neon", group: "Antigravity · antigravity" },
      { value: "agy-2/claude-sonnet", label: "Antigravity · Claude Sonnet", group: "Antigravity · Work" },
      { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek" },
    ];
    /* The Claude Code grouping leaves them alone. */
    expect(groupClaudeChoices(choices, [], []).map((choice) => choice.group)).toEqual(
      choices.map((choice) => choice.group)
    );
    const options = choiceOptions(choices, []);
    const work = options.find((option) => option.value === "agy-2/claude-sonnet");
    expect(work?.group).toBe("Antigravity · Work");
    const { container } = render(<>{work?.icon}</>);
    expect(container.querySelector("[data-logo='antigravity']")).toBeTruthy();
  });

  it("treats Antigravity snapshots like Claude Code ones for the refresh", () => {
    expect(isClaudeLimit({ provider: "agy-2", kind: "antigravity" })).toBe(true);
    expect(isClaudeLimit({ provider: "openai", kind: "openai" })).toBe(false);
  });

  it("turns remaining into used per window", () => {
    const windows = antigravityWindows(
      agyAccount({
        name: "agy-2",
        windows: [
          { group: "Gemini", type: "weekly", remaining: 0, resets_at: "" },
          { group: "Claude", type: "5h", remaining: null, resets_at: "" },
        ],
      })
    );
    expect(windows[0]).toMatchObject({ label: "Gemini · weekly", status: "rejected", utilization: 1 });
    expect(windows[1]).toMatchObject({ label: "Claude · 5 hours", utilization: null });
  });
});

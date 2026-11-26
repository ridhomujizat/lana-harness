import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";

/* Spec 75 (REQ-PAF-006): the account order list, the key order of an API
   provider, and the Claude Code page's account order. */
vi.mock("../../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/router")>();
  return {
    ...actual,
    listProviders: vi.fn(),
    saveProviderKeys: vi.fn(),
    savePoolOrder: vi.fn(),
    listRouterModels: vi.fn(),
    getRouterUsage: vi.fn(),
    getRouterLimits: vi.fn(),
    refreshRouterLimits: vi.fn(),
    getClaudeAgent: vi.fn(),
  };
});

import * as api from "../../api/router";
import { ApiError } from "../../api/client";
import { AccountOrderList, NOT_USED_AS_BACKUP, POOL_TERMS_NOTE, moveEntry, restingText } from "./account-order";
import type { OrderItem } from "./account-order";
import { ProviderDetail } from "./provider-detail";

const ROW_PX = 40;

/* jsdom has no layout: each row and the divider sit ROW_PX high in screen
   order (data-order-pos). */
function mockLayout() {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    const pos = Number(this.dataset.orderPos || 0);
    const top = pos * ROW_PX;
    return {
      top,
      bottom: top + ROW_PX,
      left: 0,
      right: 300,
      width: 300,
      height: ROW_PX,
      x: 0,
      y: top,
      toJSON: () => ({}),
    } as DOMRect;
  });
}

const ITEMS: OrderItem[] = [
  { id: "a", label: "Alpha", detail: "Pro" },
  { id: "b", label: "Beta", detail: "Max" },
  { id: "c", label: "Gamma" },
];

function rowNames(): string[] {
  const list = screen.getByRole("list", { name: "Account order" });
  return Array.from(list.querySelectorAll("[data-order-id]")).map((row) => row.getAttribute("data-order-id") || "");
}

beforeEach(() => {
  mockLayout();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("AccountOrderList", () => {
  it("labels the top row Used first and shows the cooldown time", () => {
    const until = new Date(Date.now() + 60 * 60 * 1000).toISOString();
    render(
      <AccountOrderList
        label="Account order"
        items={[ITEMS[0] as OrderItem, { ...(ITEMS[1] as OrderItem), coolingUntil: until }]}
        onSave={vi.fn()}
      />
    );
    const alpha = screen.getByRole("listitem", { name: "Alpha" });
    const beta = screen.getByRole("listitem", { name: "Beta" });
    expect(within(alpha).getByText("Used first")).toBeTruthy();
    expect(within(beta).queryByText("Used first")).toBeNull();
    expect(within(beta).getByText(/^Resting until \d\d:\d\d$/)).toBeTruthy();
    expect(restingText(new Date(Date.now() - 1000).toISOString())).toBe("");
    /* The handle is a 32 px button that takes focus. */
    expect(within(alpha).getByRole("button", { name: "Move Alpha" }).className).toContain("rt-order-grip");
  });

  it("reorders by pointer drag and saves the new order", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<AccountOrderList label="Account order" items={ITEMS} onSave={onSave} />);
    const handle = screen.getByRole("button", { name: "Move Gamma" });
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientY: 2 * ROW_PX + 20 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientY: 10 });
    /* The drop line shows before Alpha while dragging. */
    expect(screen.getByRole("listitem", { name: "Alpha" }).className).toContain("is-drop-before");
    expect(screen.getByRole("listitem", { name: "Gamma" }).className).toContain("is-dragging");
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 10 });
    expect(onSave).toHaveBeenCalledWith(["c", "a", "b"], []);
    expect(rowNames()).toEqual(["c", "a", "b"]);
    expect(await screen.findByText("Saved")).toBeTruthy();
    expect(screen.getByText("Gamma moved to position 1 of 3")).toBeTruthy();
  });

  it("moves the focused row with ArrowUp and announces it", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<AccountOrderList label="Account order" items={ITEMS} onSave={onSave} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "Move Gamma" }), { key: "ArrowUp" });
    expect(onSave).toHaveBeenCalledWith(["a", "c", "b"], []);
    const live = screen.getByText("Gamma moved to position 2 of 3");
    expect(live.getAttribute("aria-live")).toBe("polite");
    await screen.findByText("Saved");
    /* The top row cannot go higher. */
    fireEvent.keyDown(screen.getByRole("button", { name: "Move Alpha" }), { key: "ArrowUp" });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it("moves a row below the divider when its backup switch goes off, and drags it back", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<AccountOrderList label="Account order" items={ITEMS} onSave={onSave} />);
    expect(screen.queryByText(NOT_USED_AS_BACKUP)).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: "Use Alpha as backup" }));
    expect(onSave).toHaveBeenLastCalledWith(["b", "c"], ["a"]);
    await screen.findByText("Saved");
    expect(screen.getByText(NOT_USED_AS_BACKUP)).toBeTruthy();
    expect(rowNames()).toEqual(["b", "c", "a"]);
    const alpha = screen.getByRole("listitem", { name: "Alpha" });
    expect(alpha.className).toContain("is-unpooled");
    expect((within(alpha).getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
    /* Beta is used first now. */
    expect(within(screen.getByRole("listitem", { name: "Beta" })).getByText("Used first")).toBeTruthy();

    /* Alpha (pos 3, under the divider at pos 2) dragged to the top. */
    const handle = within(alpha).getByRole("button", { name: "Move Alpha" });
    fireEvent.pointerDown(handle, { button: 0, pointerId: 2, clientY: 3 * ROW_PX + 20 });
    fireEvent.pointerMove(handle, { pointerId: 2, clientY: 5 });
    fireEvent.pointerUp(handle, { pointerId: 2, clientY: 5 });
    expect(onSave).toHaveBeenLastCalledWith(["a", "b", "c"], []);
    await waitFor(() => expect(screen.queryByText(NOT_USED_AS_BACKUP)).toBeNull());
  });

  it("drags a row below the divider into the unused group", () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <AccountOrderList
        label="Account order"
        items={[ITEMS[0] as OrderItem, ITEMS[1] as OrderItem, { ...(ITEMS[2] as OrderItem), pool: false }]}
        onSave={onSave}
      />
    );
    expect(rowNames()).toEqual(["a", "b", "c"]);
    const handle = screen.getByRole("button", { name: "Move Alpha" });
    fireEvent.pointerDown(handle, { button: 0, pointerId: 3, clientY: 20 });
    /* Below the divider (pos 2) and below Gamma (pos 3). */
    fireEvent.pointerMove(handle, { pointerId: 3, clientY: 4 * ROW_PX + 30 });
    fireEvent.pointerUp(handle, { pointerId: 3 });
    expect(onSave).toHaveBeenCalledWith(["b"], ["c", "a"]);
  });

  it("rolls back and shows the error when the save fails", async () => {
    const onSave = vi.fn().mockRejectedValue(new ApiError(422, "invalid_request", "unknown account c"));
    render(<AccountOrderList label="Account order" items={ITEMS} onSave={onSave} />);
    fireEvent.keyDown(screen.getByRole("button", { name: "Move Beta" }), { key: "ArrowUp" });
    expect(rowNames()).toEqual(["b", "a", "c"]);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown account c");
    expect(rowNames()).toEqual(["a", "b", "c"]);
    expect(screen.getByText("Could not save the order. It is back as it was.")).toBeTruthy();
  });

  it("keeps the new props once the page reloads", () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(<AccountOrderList label="Account order" items={ITEMS} onSave={onSave} />);
    rerender(
      <AccountOrderList
        label="Account order"
        items={[ITEMS[2] as OrderItem, ITEMS[0] as OrderItem, ITEMS[1] as OrderItem]}
        onSave={onSave}
      />
    );
    expect(rowNames()).toEqual(["c", "a", "b"]);
  });

  it("moves entries between the groups", () => {
    const entries = [
      { id: "a", pool: true },
      { id: "b", pool: true },
      { id: "c", pool: false },
    ];
    expect(moveEntry(entries, "c", "pool", 1)).toEqual([
      { id: "a", pool: true },
      { id: "c", pool: true },
      { id: "b", pool: true },
    ]);
    expect(moveEntry(entries, "a", "rest", 1)).toEqual([
      { id: "b", pool: true },
      { id: "c", pool: false },
      { id: "a", pool: false },
    ]);
  });
});

/* ------------------------------------------------------------------ */
/* Keys of an API provider (AC-PAF-008, UI side)                       */
/* ------------------------------------------------------------------ */

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

function usage(): api.RouterUsage {
  return { range: "24h", totals: STATS, series: [], by_provider: [], by_model: [], by_source: [] };
}

/* Spec 77 revision 4: the accounts list is the order list; a row carries its
   logo, name line, always-on notes, and settings that open under it. */
describe("AccountOrderList rich rows", () => {
  const RICH: OrderItem[] = [
    {
      id: "a",
      label: "Alpha",
      rowLabel: "Account a",
      rowClass: "is-off",
      lead: <span data-testid="logo-a" />,
      sub: <span>a@x.dev</span>,
      rowActions: <button type="button">Sign in</button>,
      below: <p>Signing in…</p>,
      details: <button type="button">Remove a</button>,
    },
    { id: "b", label: "Beta", rowLabel: "Account b", details: <span>Beta settings</span>, open: true },
  ];

  it("shows each row once with its handle, line, notes, and a details toggle", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<AccountOrderList label="Accounts" items={RICH} onSave={onSave} />);
    const list = screen.getByRole("list", { name: "Accounts" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    const alpha = within(list).getByRole("listitem", { name: "Account a" });
    expect(alpha.className).toContain("is-off");
    expect(within(alpha).getByTestId("logo-a")).toBeTruthy();
    expect(within(alpha).getByText("a@x.dev")).toBeTruthy();
    expect(within(alpha).getByRole("button", { name: "Sign in" })).toBeTruthy();
    expect(within(alpha).getByText("Signing in…")).toBeTruthy();
    expect(within(alpha).getByRole("button", { name: "Move Alpha" })).toBeTruthy();
    expect(within(alpha).getByRole("checkbox", { name: "Use Alpha as backup" })).toBeTruthy();

    /* Closed by default; the chevron opens the settings region. */
    const toggle = within(alpha).getByRole("button", { name: "Details of a" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(within(alpha).queryByRole("button", { name: "Remove a" })).toBeNull();
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const region = within(alpha).getByRole("region", { name: "Details of a" });
    expect(toggle.getAttribute("aria-controls")).toBe(region.id);
    expect(within(region).getByRole("button", { name: "Remove a" })).toBeTruthy();

    /* open: true starts open; the owner can close it. */
    const beta = within(list).getByRole("listitem", { name: "Account b" });
    expect(within(beta).getByText("Beta settings")).toBeTruthy();
    fireEvent.click(within(beta).getByRole("button", { name: "Details of b" }));
    expect(within(beta).queryByText("Beta settings")).toBeNull();

    /* The handle still reorders with the same save. */
    fireEvent.keyDown(within(beta).getByRole("button", { name: "Move Beta" }), { key: "ArrowUp" });
    expect(onSave).toHaveBeenCalledWith(["b", "a"], []);
    await screen.findByText("Saved");
  });

  it("a list of one has no handle, backup switch, hint, or Used first", () => {
    render(<AccountOrderList label="Accounts" items={[RICH[0] as OrderItem]} sortable={false} onSave={vi.fn()} />);
    const alpha = screen.getByRole("listitem", { name: "Account a" });
    expect(within(alpha).queryByRole("button", { name: "Move Alpha" })).toBeNull();
    expect(within(alpha).queryByRole("checkbox", { name: "Use Alpha as backup" })).toBeNull();
    expect(within(alpha).queryByText("Used first")).toBeNull();
    expect(screen.queryByText(/Drag a row/)).toBeNull();
    expect(within(alpha).getByRole("button", { name: "Details of a" })).toBeTruthy();
  });
});

describe("provider keys", () => {
  beforeEach(() => {
    vi.mocked(api.listRouterModels).mockResolvedValue([]);
    vi.mocked(api.getRouterUsage).mockResolvedValue(usage());
    vi.mocked(api.getRouterLimits).mockResolvedValue([]);
    vi.mocked(api.listProviders).mockResolvedValue({
      version: "v1",
      restart_required: false,
      providers: [
        {
          name: "openai",
          kind: "openai",
          base_url: "",
          api_key_env: "OPENAI_API_KEY",
          key_state: "set",
          used_by: [],
          keys: [
            { name: "OPENAI_API_KEY", present: true, cooling_until: null },
            { name: "OPENAI_API_KEY_2", present: false, cooling_until: null },
          ],
        },
      ],
    });
    vi.mocked(api.saveProviderKeys).mockResolvedValue({ version: "v2", keys: [] });
  });

  it("reorders the keys with PUT /v1/providers/{name}/keys", async () => {
    render(<ProviderDetail id="openai" />);
    const list = await screen.findByRole("list", { name: "Keys of openai" });
    const second = within(list).getByRole("listitem", { name: "OPENAI_API_KEY_2" });
    expect(within(second).getByText("Missing")).toBeTruthy();
    expect(within(list).getByRole("listitem", { name: "OPENAI_API_KEY" }).textContent).toContain("Present");
    /* Keys have no backup switch. */
    expect(within(list).queryByRole("checkbox")).toBeNull();
    fireEvent.keyDown(within(second).getByRole("button", { name: "Move OPENAI_API_KEY_2" }), { key: "ArrowUp" });
    await waitFor(() =>
      expect(vi.mocked(api.saveProviderKeys)).toHaveBeenCalledWith("openai", ["OPENAI_API_KEY_2", "OPENAI_API_KEY"], "v1")
    );
  });

  it("adds a backup key by its secret name", async () => {
    render(<ProviderDetail id="openai" />);
    const input = await screen.findByRole("textbox", { name: "Backup key secret name for openai" });
    fireEvent.change(input, { target: { value: "bad name" } });
    fireEvent.click(screen.getByRole("button", { name: "Add backup key" }));
    expect((await screen.findByRole("alert")).textContent).toContain("letters, digits, and _ only");
    expect(vi.mocked(api.saveProviderKeys)).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "OPENAI_API_KEY_3" } });
    fireEvent.click(screen.getByRole("button", { name: "Add backup key" }));
    await waitFor(() =>
      expect(vi.mocked(api.saveProviderKeys)).toHaveBeenCalledWith(
        "openai",
        ["OPENAI_API_KEY", "OPENAI_API_KEY_2", "OPENAI_API_KEY_3"],
        "v1"
      )
    );
    await waitFor(() => expect((input as HTMLInputElement).value).toBe(""));
  });
});

/* ------------------------------------------------------------------ */
/* Claude Code account order                                           */
/* ------------------------------------------------------------------ */

function claudeAccount(overrides: Partial<api.ClaudeAccount> & { name: string }): api.ClaudeAccount {
  return {
    builtin: false,
    config_dir: "",
    binary: "/usr/bin/claude",
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
    priority: 0,
    pool: true,
    cooling_until: null,
    ...overrides,
  };
}

describe("Claude Code account order", () => {
  beforeEach(() => {
    vi.mocked(api.listRouterModels).mockResolvedValue([]);
    vi.mocked(api.getRouterUsage).mockResolvedValue(usage());
    vi.mocked(api.getRouterLimits).mockResolvedValue([]);
    vi.mocked(api.refreshRouterLimits).mockResolvedValue([]);
    vi.mocked(api.listProviders).mockResolvedValue({ version: "v1", restart_required: false, providers: [] });
    vi.mocked(api.savePoolOrder).mockResolvedValue({ version: "v2", restart_required: false, pool: [] });
  });

  it("saves the order with runtime claude-code and shows the terms note on pool_warning", async () => {
    vi.mocked(api.getClaudeAgent).mockResolvedValue({
      configured_agents: [],
      binary: "/usr/bin/claude",
      version: "2.1.319",
      version_ok: true,
      logged_in: true,
      auth_method: "",
      error: "",
      running: 0,
      queued: 0,
      max_concurrent: 2,
      pool_warning: true,
      accounts: [
        claudeAccount({ name: "claude-code", builtin: true, email: "me@home.dev", plan: "max", priority: 0 }),
        claudeAccount({ name: "claude-work", email: "me@work.dev", plan: "team", priority: 100 }),
      ],
    });
    render(<ProviderDetail id="claude-code" />);
    const list = await screen.findByRole("list", { name: "Claude Code accounts" });
    expect(within(list).getByRole("listitem", { name: "Account claude-code" }).textContent).toContain("Used first");
    /* Spec 77 revision 4: the order lives in the accounts list. */
    expect(screen.queryByRole("heading", { name: "Account order" })).toBeNull();
    expect(screen.getByText(POOL_TERMS_NOTE)).toBeTruthy();
    fireEvent.keyDown(within(list).getByRole("button", { name: "Move me@work.dev" }), { key: "ArrowUp" });
    await waitFor(() =>
      expect(vi.mocked(api.savePoolOrder)).toHaveBeenCalledWith("claude-code", ["claude-work", "claude-code"], [], "v1")
    );
  });
});

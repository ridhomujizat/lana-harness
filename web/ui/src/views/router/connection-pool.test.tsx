import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";

/* Spec 80 (REQ-ACP-005): the connections of an API provider page that share
   a pool group are one ordered list, saved with runtime "api". */
vi.mock("../../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/router")>();
  return {
    ...actual,
    listProviders: vi.fn(),
    savePoolOrder: vi.fn(),
    saveProvider: vi.fn(),
    deleteProvider: vi.fn(),
    testProvider: vi.fn(),
    saveProviderKeys: vi.fn(),
    listRouterModels: vi.fn(),
    getRouterUsage: vi.fn(),
    getRouterLimits: vi.fn(),
  };
});

import * as api from "../../api/router";
import type { ProviderRow } from "../../api/router";
import { ApiError } from "../../api/client";
import { NOT_USED_AS_BACKUP, POOL_TERMS_NOTE } from "./account-order";
import { CONNECTION_POOL_NOTE, ProviderDetail, connectionsPooled } from "./provider-detail";

const ROW_PX = 40;
const CC_GROUP = "commandcode https://api.commandcode.ai";

/* jsdom has no layout: each row and the divider sit ROW_PX high in screen
   order (data-order-pos). */
function mockLayout() {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    const pos = Number(this.dataset.orderPos || 0);
    const top = pos * ROW_PX;
    return { top, bottom: top + ROW_PX, left: 0, right: 300, width: 300, height: ROW_PX, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
  });
}

function connection(overrides: Partial<ProviderRow> & { name: string }): ProviderRow {
  return {
    kind: "commandcode",
    base_url: "",
    default_base_url: "https://api.commandcode.ai",
    api_key_env: overrides.name.toUpperCase().replace(/-/g, "_") + "_API_KEY",
    key_state: "set",
    used_by: [],
    disabled: false,
    priority: 100,
    pool: true,
    pool_group: CC_GROUP,
    ...overrides,
  };
}

function listed(providers: ProviderRow[], version = "v1") {
  return { version, restart_required: false, providers };
}

function rowIds(list: HTMLElement): string[] {
  return Array.from(list.querySelectorAll("[data-order-id]")).map((row) => row.getAttribute("data-order-id") || "");
}

beforeEach(() => {
  mockLayout();
  vi.mocked(api.listRouterModels).mockResolvedValue([]);
  vi.mocked(api.getRouterUsage).mockResolvedValue({
    range: "24h",
    totals: {} as api.RouterUsage["totals"],
    series: [],
    by_provider: [],
    by_model: [],
    by_source: [],
  });
  vi.mocked(api.getRouterLimits).mockResolvedValue([]);
  vi.mocked(api.savePoolOrder).mockResolvedValue({ version: "v2", restart_required: false, pool: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("connection pool", () => {
  it("AC-ACP-006: drags commandcode-2 to the top and saves the order with runtime api", async () => {
    vi.mocked(api.listProviders)
      .mockResolvedValueOnce(listed([connection({ name: "commandcode" }), connection({ name: "commandcode-2" })]))
      .mockResolvedValue(
        listed(
          [connection({ name: "commandcode", priority: 20 }), connection({ name: "commandcode-2", priority: 10 })],
          "v2"
        )
      );
    render(<ProviderDetail id="commandcode" />);
    const list = await screen.findByRole("list", { name: "Command Code connections" });
    expect(rowIds(list)).toEqual(["commandcode", "commandcode-2"]);
    const top = within(list).getByRole("listitem", { name: "Connection commandcode" });
    expect(top.textContent).toContain("Used first");
    expect(screen.getByText(CONNECTION_POOL_NOTE)).toBeTruthy();
    expect(screen.getByText(POOL_TERMS_NOTE)).toBeTruthy();
    expect(screen.queryByText(/Put them in a combo/)).toBeNull();

    const handle = within(list).getByRole("button", { name: "Move commandcode-2" });
    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientY: ROW_PX + 20 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientY: 5 });
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 5 });
    await waitFor(() =>
      expect(vi.mocked(api.savePoolOrder)).toHaveBeenCalledWith("api", ["commandcode-2", "commandcode"], [], "v1")
    );
    expect(await screen.findByText("Saved")).toBeTruthy();
    await waitFor(() => expect(rowIds(list)).toEqual(["commandcode-2", "commandcode"]));
  });

  it("keeps every connection control on a pooled row", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      listed([
        connection({ name: "commandcode", used_by: ["lana"] }),
        connection({ name: "commandcode-2", key_state: "unset", disabled: true }),
      ])
    );
    vi.mocked(api.saveProvider).mockResolvedValue({ name: "commandcode-2", version: "v2", restart_required: false });
    vi.mocked(api.testProvider).mockResolvedValue({ ok: true, latency_ms: 120 });
    vi.mocked(api.deleteProvider).mockResolvedValue({ name: "commandcode-2", version: "v2", restart_required: false });
    render(<ProviderDetail id="commandcode" />);
    const list = await screen.findByRole("list", { name: "Command Code connections" });
    const second = within(list).getByRole("listitem", { name: "Connection commandcode-2" });
    expect(second.className).toContain("is-off");
    expect(within(second).getByText("COMMANDCODE_2_API_KEY")).toBeTruthy();
    expect(within(second).getByRole("checkbox", { name: "Use commandcode-2 as backup" })).toBeTruthy();
    expect(within(within(list).getByRole("listitem", { name: "Connection commandcode" })).getByText("lana")).toBeTruthy();

    /* Enable switch. */
    const enable = within(second).getByRole("checkbox", { name: "Use commandcode-2" });
    expect((enable as HTMLInputElement).checked).toBe(false);
    fireEvent.click(enable);
    await waitFor(() =>
      expect(vi.mocked(api.saveProvider)).toHaveBeenCalledWith(
        "commandcode-2",
        expect.objectContaining({ kind: "commandcode", disabled: false, expected_version: "v1" })
      )
    );

    /* Test with its result under the row. */
    fireEvent.click(within(second).getByRole("button", { name: "Test commandcode-2" }));
    expect(await within(second).findByText(/Connected/)).toBeTruthy();

    /* Edit opens the dialog. */
    fireEvent.click(within(second).getByRole("button", { name: "Edit commandcode-2" }));
    expect(await screen.findByRole("dialog")).toBeTruthy();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

    /* Delete asks first. */
    fireEvent.click(within(second).getByRole("button", { name: "Delete commandcode-2" }));
    expect(within(second).getByText("Delete commandcode-2?")).toBeTruthy();
    fireEvent.click(within(second).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(vi.mocked(api.deleteProvider)).toHaveBeenCalledWith("commandcode-2", "v1"));
  });

  it("lists a pool = false connection below the divider and turns it back on as backup", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      listed([connection({ name: "commandcode", priority: 10 }), connection({ name: "commandcode-2", priority: 20, pool: false })])
    );
    render(<ProviderDetail id="commandcode" />);
    const list = await screen.findByRole("list", { name: "Command Code connections" });
    expect(within(list).getByText(NOT_USED_AS_BACKUP)).toBeTruthy();
    /* Only one pooled connection: no terms note. */
    expect(screen.queryByText(POOL_TERMS_NOTE)).toBeNull();
    fireEvent.click(within(list).getByRole("checkbox", { name: "Use commandcode-2 as backup" }));
    await waitFor(() =>
      expect(vi.mocked(api.savePoolOrder)).toHaveBeenCalledWith("api", ["commandcode", "commandcode-2"], [], "v1")
    );
  });

  it("puts the order back when the save fails", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      listed([connection({ name: "commandcode" }), connection({ name: "commandcode-2" })])
    );
    vi.mocked(api.savePoolOrder).mockRejectedValue(new ApiError(409, "version_conflict", "config.toml changed since it was loaded"));
    render(<ProviderDetail id="commandcode" />);
    const list = await screen.findByRole("list", { name: "Command Code connections" });
    fireEvent.keyDown(within(list).getByRole("button", { name: "Move commandcode-2" }), { key: "ArrowUp" });
    expect((await screen.findByRole("alert")).textContent).toContain("Could not save the order");
    expect(rowIds(list)).toEqual(["commandcode", "commandcode-2"]);
  });

  it("AC-ACP-007: a page with one connection has no handle and no pool notes", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(listed([connection({ name: "commandcode" })]));
    render(<ProviderDetail id="commandcode" />);
    const row = await screen.findByRole("listitem", { name: "Connection commandcode" });
    expect(within(row).queryByRole("button", { name: /^Move / })).toBeNull();
    expect(within(row).getByRole("button", { name: "Test commandcode" })).toBeTruthy();
    expect(screen.queryByText(POOL_TERMS_NOTE)).toBeNull();
    expect(screen.queryByText(CONNECTION_POOL_NOTE)).toBeNull();
    expect(screen.queryByText(/The top connection is used first/)).toBeNull();
  });

  it("keeps plain rows when the connections are in different groups", async () => {
    vi.mocked(api.listProviders).mockResolvedValue(
      listed([
        connection({ name: "commandcode" }),
        connection({ name: "commandcode-2", base_url: "https://proxy.example/v1", pool_group: "commandcode https://proxy.example/v1" }),
      ])
    );
    render(<ProviderDetail id="commandcode" />);
    await screen.findByRole("listitem", { name: "Connection commandcode" });
    expect(screen.getByRole("listitem", { name: "Connection commandcode-2" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Move commandcode/ })).toBeNull();
    expect(screen.queryByText(CONNECTION_POOL_NOTE)).toBeNull();
    expect(connectionsPooled([connection({ name: "a" }), connection({ name: "b", pool_group: undefined })])).toBe(false);
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { ROUTER_TREE_KEY, Sidebar } from "./Sidebar";
import type { Route } from "./router";

/* Home and Chats sit under Workspace (owner request 2026-11-02, replacing the
   untitled group of REQ-ROOM-012); Observe and Configure group the rest.
   History keeps the sessions list. */

function renderSidebar(
  collapsed = false,
  overrides: { onToggle?: () => void; onLogout?: () => void } = {}
) {
  render(
    <Sidebar
      route={{ view: "chats", section: "chats", params: {}, path: "#/chats" }}
      collapsed={collapsed}
      onToggle={overrides.onToggle || (() => {})}
      onLogout={overrides.onLogout || (() => {})}
      daemon={{ state: "running", title: "Daemon running", meta: "127.0.0.1" }}
    />
  );
}

describe("sidebar groups (REQ-ROOM-012)", () => {
  it("groups the navigation under Workspace, Observe and Configure", () => {
    renderSidebar();
    expect(screen.getByText("Workspace")).toBeTruthy();
    expect(screen.getByText("Observe")).toBeTruthy();
    expect(screen.getByText("Configure")).toBeTruthy();
  });

  it("links History to the sessions list and Chats to its page", () => {
    renderSidebar();
    expect(
      screen.getByRole("link", { name: "History" }).getAttribute("href")
    ).toBe("#/sessions");
    expect(
      screen.getByRole("link", { name: "Chats" }).getAttribute("href")
    ).toBe("#/chats");
    expect(
      screen.getByRole("link", { name: "Home" }).getAttribute("href")
    ).toBe("#/home");
  });

  it("marks the current section with aria-current", () => {
    renderSidebar();
    expect(
      screen.getByRole("link", { name: "Chats" }).getAttribute("aria-current")
    ).toBe("page");
    expect(
      screen.getByRole("link", { name: "History" }).getAttribute("aria-current")
    ).toBeNull();
  });

  it("keeps Settings in the footer and the daemon details in the header", () => {
    renderSidebar();
    expect(
      screen.getByRole("link", { name: "Settings" }).getAttribute("href")
    ).toBe("#/settings/general");
    expect(screen.getByText("127.0.0.1")).toBeTruthy();
  });
});

describe("sidebar collapse and logout", () => {
  it("collapses from the header control", () => {
    const onToggle = vi.fn();
    renderSidebar(false, { onToggle });
    fireEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("expands from the rail and tips the labels when collapsed", () => {
    const onToggle = vi.fn();
    renderSidebar(true, { onToggle });
    const expand = screen.getByRole("button", { name: "Expand sidebar" });
    fireEvent.click(expand);
    expect(onToggle).toHaveBeenCalledTimes(1);
    expect(
      screen.getByRole("link", { name: "Home" }).getAttribute("data-tip")
    ).toBe("Home");
  });

  it("logs out from the owner row", () => {
    const onLogout = vi.fn();
    renderSidebar(false, { onLogout });
    fireEvent.click(screen.getByRole("button", { name: "Log out" }));
    expect(onLogout).toHaveBeenCalledTimes(1);
  });
});

/* Owner request 2026-11-08: the Router pages (spec 37, API-RTR-017 revision 2)
   no longer get a group of their own. "Router" opens Configure as a collapsible
   parent; its pages hang below it as a tree, or in a flyout on the rail. */
const CHATS: Route = { view: "chats", section: "chats", params: {}, path: "#/chats" };
const PROVIDER_PAGE: Route = {
  view: "router",
  section: "router",
  params: { tab: "providers", provider: "gemini" },
  path: "#/router/providers/gemini",
};
const ROUTER_PAGES = ["Providers", "Combos", "Usage", "Quota", "Router logs", "Endpoint"];

function sidebarAt(route: Route, collapsed = false, onNavigate?: () => void) {
  return (
    <Sidebar
      route={route}
      collapsed={collapsed}
      onToggle={() => {}}
      onLogout={() => {}}
      onNavigate={onNavigate}
      daemon={{ state: "running", title: "Daemon running", meta: "127.0.0.1" }}
    />
  );
}

function treeLabels(): string[] {
  const list = document.getElementById("sidebar-router-tree") as HTMLElement;
  return Array.from(list.querySelectorAll("a")).map(
    (node) => node.getAttribute("aria-label") || ""
  );
}

describe("sidebar router tree", () => {
  it("opens Configure with Router and drops the Router group", () => {
    renderSidebar();
    const titles = Array.from(document.querySelectorAll(".sgroup-h")).map((node) => node.textContent);
    expect(titles).toEqual(["Workspace", "Observe", "Configure"]);
    const configure = screen.getByText("Configure").parentElement as HTMLElement;
    const labels = Array.from(configure.querySelectorAll(":scope > .stree > .stree-row > a, :scope > a.sitem")).map(
      (node) => node.getAttribute("aria-label")
    );
    expect(labels).toEqual(["Router", "Agents", "Knowledge", "Skills", "Tools", "MCP", "Channels"]);
    expect(screen.getByRole("link", { name: "Router" }).getAttribute("href")).toBe("#/router/providers");
    expect(treeLabels()).toEqual(ROUTER_PAGES);
  });

  it("is closed by default off the router pages", () => {
    renderSidebar();
    const chevron = screen.getByRole("button", { name: "Router pages" });
    expect(chevron.getAttribute("aria-expanded")).toBe("false");
    expect(chevron.getAttribute("aria-controls")).toBe("sidebar-router-tree");
    expect((document.getElementById("sidebar-router-tree") as HTMLElement).hidden).toBe(true);
    expect(screen.queryByRole("link", { name: "Providers" })).toBeNull();
  });

  it("opens itself on a router page and marks the current page", () => {
    render(sidebarAt(PROVIDER_PAGE));
    expect(screen.getByRole("button", { name: "Router pages" }).getAttribute("aria-expanded")).toBe("true");
    const tree = screen.getByRole("list", { name: "Router pages" });
    expect(within(tree).getAllByRole("link").map((node) => node.getAttribute("aria-label"))).toEqual(ROUTER_PAGES);
    expect(screen.getByRole("link", { name: "Providers" }).getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("link", { name: "Combos" }).getAttribute("aria-current")).toBeNull();
    expect(screen.getByRole("link", { name: "Router logs" }).getAttribute("href")).toBe("#/router/logs");
    /* The app's own Logs page keeps its item and name. */
    expect(screen.getByRole("link", { name: "Logs" }).getAttribute("href")).toBe("#/logs");
    expect(screen.getByRole("link", { name: "Logs" }).getAttribute("aria-current")).toBeNull();
    /* The parent is an ancestor, not the current page. */
    const parent = screen.getByRole("link", { name: "Router" });
    expect(parent.getAttribute("aria-current")).toBeNull();
    expect(parent.className).toContain("anc");
  });

  it("opens again when the route enters the router", () => {
    const view = render(sidebarAt(CHATS));
    const chevron = screen.getByRole("button", { name: "Router pages" });
    expect(chevron.getAttribute("aria-expanded")).toBe("false");
    view.rerender(sidebarAt({ view: "router", section: "router", params: { tab: "quota" }, path: "#/router/quota" }));
    expect(chevron.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("link", { name: "Quota" }).getAttribute("aria-current")).toBe("page");
  });

  it("toggles from the chevron and remembers the choice", () => {
    const view = render(sidebarAt(CHATS));
    fireEvent.click(screen.getByRole("button", { name: "Router pages" }));
    expect(screen.getByRole("button", { name: "Router pages" }).getAttribute("aria-expanded")).toBe("true");
    expect(window.localStorage.getItem(ROUTER_TREE_KEY)).toBe("open");
    view.unmount();

    render(sidebarAt(CHATS));
    const chevron = screen.getByRole("button", { name: "Router pages" });
    expect(chevron.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(chevron);
    expect(chevron.getAttribute("aria-expanded")).toBe("false");
    expect(window.localStorage.getItem(ROUTER_TREE_KEY)).toBe("closed");
  });

  it("navigates from the Router label and opens the tree", () => {
    const onNavigate = vi.fn();
    render(sidebarAt(CHATS, false, onNavigate));
    fireEvent.click(screen.getByRole("link", { name: "Router" }));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Router pages" }).getAttribute("aria-expanded")).toBe("true");
    /* Only the chevron stores a choice. */
    expect(window.localStorage.getItem(ROUTER_TREE_KEY)).toBeNull();
  });

  it("survives blocked storage", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    try {
      render(sidebarAt(CHATS));
      const chevron = screen.getByRole("button", { name: "Router pages" });
      fireEvent.click(chevron);
      expect(chevron.getAttribute("aria-expanded")).toBe("true");
    } finally {
      getItem.mockRestore();
      setItem.mockRestore();
    }
  });
});

describe("sidebar router flyout (collapsed rail)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("links the Router icon to Providers without a tree", () => {
    render(sidebarAt(CHATS, true));
    const router = screen.getByRole("link", { name: "Router" });
    expect(router.getAttribute("href")).toBe("#/router/providers");
    expect(router.getAttribute("aria-haspopup")).toBe("menu");
    expect(router.getAttribute("aria-expanded")).toBe("false");
    expect(document.getElementById("sidebar-router-tree")).toBeNull();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("shows the router pages on hover and hides them after leaving", () => {
    vi.useFakeTimers();
    render(sidebarAt(PROVIDER_PAGE, true));
    const router = screen.getByRole("link", { name: "Router" });
    expect(router.className).toContain("on");
    fireEvent.mouseEnter(router);
    const menu = screen.getByRole("menu", { name: "Router" });
    expect(router.getAttribute("aria-expanded")).toBe("true");
    expect(router.getAttribute("aria-controls")).toBe(menu.id);
    expect(within(menu).getAllByRole("menuitem").map((node) => node.getAttribute("aria-label"))).toEqual(ROUTER_PAGES);
    expect(within(menu).getByRole("menuitem", { name: "Providers" }).getAttribute("aria-current")).toBe("page");
    /* Hover never moves focus. */
    expect(document.activeElement).not.toBe(within(menu).getAllByRole("menuitem")[0]);

    /* Crossing into the flyout keeps it open. */
    fireEvent.mouseLeave(router);
    fireEvent.mouseEnter(menu);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(screen.getByRole("menu")).toBeTruthy();
    fireEvent.mouseLeave(menu);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("shows the flyout on focus", () => {
    render(sidebarAt(CHATS, true));
    act(() => {
      screen.getByRole("link", { name: "Router" }).focus();
    });
    expect(screen.getByRole("menu", { name: "Router" })).toBeTruthy();
  });

  it("opens from the keyboard, moves with arrows and closes on Escape", () => {
    render(sidebarAt(CHATS, true));
    const router = screen.getByRole("link", { name: "Router" });
    fireEvent.keyDown(router, { key: "Enter" });
    const items = within(screen.getByRole("menu")).getAllByRole("menuitem");
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(items[0] as HTMLElement, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(items[1] as HTMLElement, { key: "ArrowUp" });
    fireEvent.keyDown(items[0] as HTMLElement, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items[5]);
    fireEvent.keyDown(items[5] as HTMLElement, { key: "Home" });
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(items[0] as HTMLElement, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(router);
    expect(router.getAttribute("aria-expanded")).toBe("false");
  });

  it("opens from Space too and navigates from a page", () => {
    const onNavigate = vi.fn();
    render(sidebarAt(CHATS, true, onNavigate));
    fireEvent.keyDown(screen.getByRole("link", { name: "Router" }), { key: " " });
    const logs = screen.getByRole("menuitem", { name: "Router logs" });
    expect(logs.getAttribute("href")).toBe("#/router/logs");
    fireEvent.click(logs);
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
  });
});

describe("sidebar router tree in the mobile drawer", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps the tree even when the desktop rail is collapsed", () => {
    vi.stubGlobal(
      "matchMedia",
      (query: string) =>
        ({
          matches: query === "(max-width: 900px)",
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
        }) as unknown as MediaQueryList
    );
    render(sidebarAt(PROVIDER_PAGE, true));
    expect(screen.getByRole("link", { name: "Router" }).getAttribute("aria-haspopup")).toBeNull();
    expect(screen.getByRole("button", { name: "Router pages" }).getAttribute("aria-expanded")).toBe("true");
    expect(treeLabels()).toEqual(ROUTER_PAGES);
  });
});

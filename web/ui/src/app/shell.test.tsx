import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Sidebar } from "./Sidebar";
import { dragRegion, isMacAppShell, topbarToggle } from "./shell";

/* Spec 52: the Lana.app window marks the page with html[data-shell] and
   keeps the top-left corner free for the traffic lights. */

afterEach(() => {
  cleanup();
  delete document.documentElement.dataset.shell;
});

function renderSidebar(collapsed: boolean) {
  render(
    <Sidebar
      route={{ view: "chats", section: "chats", params: {}, path: "#/chats" }}
      collapsed={collapsed}
      onToggle={() => {}}
      onLogout={() => {}}
      daemon={{ state: "running", title: "Daemon running", meta: "127.0.0.1" }}
    />
  );
}

describe("macOS app shell (spec 52)", () => {
  it("is off in a browser", () => {
    expect(isMacAppShell()).toBe(false);
    expect(dragRegion(false)).toEqual({});
    expect(topbarToggle(false, true)).toBe(false);
  });

  it("reads the attribute the app sets", () => {
    document.documentElement.dataset.shell = "macos-app";
    expect(isMacAppShell()).toBe(true);
    expect(dragRegion(true)).toEqual({ "data-drag-region": "" });
  });

  it("puts the toggle in the topbar only for the collapsed rail", () => {
    expect(topbarToggle(true, true)).toBe(true);
    expect(topbarToggle(true, false)).toBe(false);
  });

  it("keeps the browser sidebar header as it was", () => {
    renderSidebar(false);
    /* Logo, wordmark, host line, and the collapse toggle in one header row. */
    const top = document.querySelector(".side-top");
    const logo = screen.getByRole("link", { name: "Lana home" });
    expect(logo.closest(".side-top")).toBe(top);
    expect(logo.querySelector(".lana-word")?.textContent).toBe("LΛNΛ");
    expect(logo.querySelector(".ws-sub")?.textContent).toBe("127.0.0.1");
    const toggle = screen.getByRole("button", { name: "Collapse sidebar" });
    expect(toggle.closest(".side-top")).toBe(top);
    expect(top?.querySelector(".row")?.lastElementChild).toBe(toggle);
    expect(document.documentElement.dataset.shell).toBeUndefined();
    expect(document.querySelector(".side-chrome")).toBeNull();
    expect(document.querySelector("[data-drag-region]")).toBeNull();
  });

  it("moves the collapse toggle into the draggable lights row", () => {
    document.documentElement.dataset.shell = "macos-app";
    renderSidebar(false);
    const toggle = screen.getByRole("button", { name: "Collapse sidebar" });
    const row = toggle.closest(".side-chrome");
    expect(row).toBeTruthy();
    expect(row?.hasAttribute("data-drag-region")).toBe(true);
    expect(toggle.closest(".side-top")).toBeNull();
    /* The brand stays, below the row. */
    expect(screen.getByRole("link", { name: "Lana home" })).toBeTruthy();
  });

  it("keeps an empty lights row on the collapsed rail", () => {
    document.documentElement.dataset.shell = "macos-app";
    renderSidebar(true);
    expect(document.querySelector(".side-chrome")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Collapse sidebar" })).toBeNull();
  });
});

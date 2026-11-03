import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { Sidebar } from "./Sidebar";

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

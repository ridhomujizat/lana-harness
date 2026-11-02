import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Sidebar } from "./Sidebar";

/* REQ-ROOM-012: Home and Chats lead without a title; Observe and Configure
   group the rest. History keeps the sessions list. */

function renderSidebar(collapsed = false) {
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

describe("sidebar groups (REQ-ROOM-012)", () => {
  it("groups the navigation under Observe and Configure", () => {
    renderSidebar();
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
});

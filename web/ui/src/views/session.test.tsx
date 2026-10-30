import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { SessionDetail } from "../api/sessions";
import { SessionPage } from "./session";

const detail: SessionDetail = {
  id: "s1",
  channel: "web",
  address: "",
  agent: "lana",
  title: "Disk check",
  status: "idle",
  preview: "",
  turn_count: 2,
  created_at: 1,
  updated_at: 2,
  in_use: "none",
  holder_pid: null,
  team: null,
  model: "deepseek:chat",
  policy: "ask",
  workspace: "/tmp/work",
  parent_session_id: "",
  turn_running: false,
};

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("session chat", () => {
  it("renders the header, empty thread, and composer", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/events")) {
          return json({ events: [], last_seq: 0 });
        }
        if (url.endsWith("/agents")) {
          return json({ agents: [] });
        }
        if (url.includes("/stream")) {
          return new Response("", { status: 200 });
        }
        return json(detail);
      })
    );

    render(<SessionPage id="s1" />);
    expect(await screen.findByText("Disk check")).toBeTruthy();
    expect(screen.getByText("2 turns")).toBeTruthy();
    expect(screen.getByText("No messages yet")).toBeTruthy();
    expect(screen.getByLabelText("Message lana")).toBeTruthy();
  });
});

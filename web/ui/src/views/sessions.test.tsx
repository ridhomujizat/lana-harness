import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ListSessionsResponse, Session } from "../api/sessions";
import { SessionsPage } from "./sessions";

function stubSessionsFetch(sessions: Session[]) {
  const body: ListSessionsResponse = {
    sessions,
    next_before: null,
    facets: { channel: {}, status: {}, agent: [] },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    })
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("sessions list", () => {
  it("shows the empty state when the page has no sessions", async () => {
    stubSessionsFetch([]);
    render(<SessionsPage />);
    expect(await screen.findByText("No sessions found")).toBeTruthy();
    expect(screen.getByText("Start a session to see it here.")).toBeTruthy();
  });

  it("renders one row with its agent, channel, and title", async () => {
    stubSessionsFetch([
      {
        id: "abc",
        channel: "web",
        address: "",
        agent: "lana",
        title: "Disk check",
        status: "idle",
        preview: "All good",
        turn_count: 1,
        created_at: 1,
        updated_at: Date.now(),
        in_use: "none",
        team: null,
      },
    ]);
    render(<SessionsPage />);
    expect(await screen.findByText("Disk check")).toBeTruthy();
    expect(screen.getByText("lana")).toBeTruthy();
    expect(screen.getByText("Web")).toBeTruthy();
  });
});

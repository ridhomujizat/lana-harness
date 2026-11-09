import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ListSessionsResponse, Session } from "../api/sessions";
import { SessionsPage } from "./sessions";
import { resetAvatarStore, setAgentAvatar } from "../lib/avatar-store";

const BLOB =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<path d="M50 12 C 72 12 88 28 88 50 C 88 72 72 88 50 88 C 28 88 12 72 12 50 C 12 28 28 12 50 12 Z" fill="currentColor"/>' +
  '<ellipse cx="40" cy="46" rx="4" ry="7" fill="#ffffff"/>' +
  '<ellipse cx="60" cy="46" rx="4" ry="7" fill="#ffffff"/></svg>';

/* The session list resolves its agent labels from the shared rooms store, so
   that store is stubbed with no rows and the ID stays the fallback label. */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return {
    ...actual,
    listRooms: vi.fn().mockResolvedValue([]),
    openRoomsStream: vi.fn(() => ({ close: () => {} })),
  };
});

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

/* Spec 43: a history row shows the agent's avatar next to its name. */
describe("sessions list avatars", () => {
  afterEach(() => {
    resetAvatarStore();
  });

  it("shows the stored svg for the row's agent", async () => {
    setAgentAvatar("lana", BLOB, "#12b5a6");
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
    const { container } = render(<SessionsPage />);
    expect(await screen.findByText("Disk check")).toBeTruthy();
    const avatar = container.querySelector(".session-row-meta .av.has-svg") as HTMLElement;
    expect(avatar.querySelector("svg path")).not.toBeNull();
    expect(avatar.style.getPropertyValue("--c")).toBe("#12b5a6");
  });
});

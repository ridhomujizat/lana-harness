import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

/* The presence kit reads one module. The stream and the list are mocked so the
   face, the label, the skeleton, and the listRooms fallback can be observed
   (REQ-ROOM-006, REQ-ROOM-013). */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return { ...actual, listRooms: vi.fn(), openRoomsStream: vi.fn() };
});

import { listRooms, openRoomsStream } from "../api/rooms";
import type { RoomRow } from "../api/rooms";
import {
  AgentFace,
  PresenceLabel,
  SkeletonRows,
  TeamFace,
  sortRooms,
  useRooms,
} from "./presence";

function room(overrides: Partial<RoomRow> = {}): RoomRow {
  return {
    name: "dana",
    kind: "agent",
    title: "Finance",
    color: "success",
    members: [],
    lead: "",
    presence: { state: "online", label: "Active 2 min ago", since: 0, session_id: "s_1" },
    last_message: null,
    unread: 0,
    turns_7d: 0,
    last_active_at: 0,
    ...overrides,
  };
}

afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("AgentFace (REQ-ROOM-006)", () => {
  it("shows the avatar and names the presence in the label", () => {
    render(
      <AgentFace
        name="dana"
        color="success"
        presence={{ state: "working", label: "Running bash", since: 0, session_id: "s_1" }}
      />
    );

    /* Spec 43: the server-rendered avatar, then the initial if it fails. */
    const face = screen.getByRole("img", { name: "dana, working" });
    const image = face.querySelector("img.av-img") as HTMLImageElement;
    expect(image.getAttribute("src")).toBe("/v1/agents/dana/avatar.svg?theme=light");
    fireEvent.error(image);
    expect(face.textContent).toContain("D");
  });
});

describe("TeamFace (REQ-ROOM-001)", () => {
  /* The team avatar reserves its slot so the stacked faces never overlap the
     text beside them; at most two faces render inside the fixed box. */
  it("renders the faces inside a box of the requested size", () => {
    const { container } = render(
      <TeamFace faces={[{ name: "lana" }, { name: "rasya" }, { name: "cody" }]} size={44} />
    );

    const box = container.querySelector(".team-face") as HTMLElement;
    expect(box.style.width).toBe("44px");
    expect(box.style.height).toBe("44px");
    expect(box.querySelectorAll(".agent-face")).toHaveLength(2);
  });
});

describe("PresenceLabel (REQ-ROOM-006)", () => {
  it("renders the presence text", () => {
    render(
      <PresenceLabel
        presence={{
          state: "waiting",
          label: "Waiting for your answer",
          since: 0,
          session_id: "s_1",
        }}
      />
    );

    expect(screen.getByText("Waiting for your answer")).toBeTruthy();
  });
});

describe("SkeletonRows (REQ-ROOM-013)", () => {
  it("renders one skeleton per row", () => {
    const { container } = render(<SkeletonRows count={5} />);
    expect(container.querySelectorAll(".skeleton-row")).toHaveLength(5);
  });
});

describe("sortRooms (API-ROOM-001)", () => {
  it("puts the most recently active room first and idle rooms last", () => {
    const rows = [
      room({ name: "lana", last_active_at: 300 }),
      room({ name: "zed", last_active_at: 0 }),
      room({ name: "omnix", last_active_at: 100 }),
      room({ name: "alpha", last_active_at: 0 }),
      room({ name: "example", last_active_at: 500 }),
    ];
    expect(sortRooms(rows).map((r) => r.name)).toEqual([
      "example",
      "lana",
      "omnix",
      "alpha",
      "zed",
    ]);
  });
});

describe("useRooms (REQ-ROOM-007)", () => {
  it("falls back to listRooms when the stream errors", async () => {
    vi.mocked(openRoomsStream).mockImplementation((handlers) => {
      handlers.onState?.(false);
      return { close: () => {} };
    });
    vi.mocked(listRooms).mockResolvedValue([room({ name: "dana" })]);

    function Probe() {
      const { rows, loading } = useRooms();
      if (loading) {
        return <p>loading</p>;
      }
      return (
        <ul>
          {rows.map((row) => (
            <li key={row.name}>{row.name}</li>
          ))}
        </ul>
      );
    }

    render(<Probe />);

    expect(await screen.findByText("dana")).toBeTruthy();
    expect(listRooms).toHaveBeenCalled();
  });

  /* The list must populate even when the stream connects but never delivers a
     `rooms` event. */
  it("loads the rows from listRooms even when the stream never emits", async () => {
    vi.mocked(openRoomsStream).mockImplementation(() => ({ close: () => {} }));
    vi.mocked(listRooms).mockResolvedValue([room({ name: "nadia" })]);

    function Probe() {
      const { rows, loading } = useRooms();
      if (loading) {
        return <p>loading</p>;
      }
      return (
        <ul>
          {rows.map((row) => (
            <li key={row.name}>{row.name}</li>
          ))}
        </ul>
      );
    }

    render(<Probe />);

    expect(await screen.findByText("nadia")).toBeTruthy();
    expect(listRooms).toHaveBeenCalled();
  });

  /* When no `rooms` event arrives within 4 s, the 10 s poll starts. */
  it("starts listRooms polling when no rooms event arrives within 4 s", async () => {
    vi.useFakeTimers();
    vi.mocked(openRoomsStream).mockImplementation(() => ({ close: () => {} }));
    vi.mocked(listRooms).mockResolvedValue([room({ name: "nadia" })]);

    function Probe() {
      useRooms();
      return null;
    }

    const { unmount } = render(<Probe />);
    /* The immediate load happens on the first subscriber. */
    expect(listRooms).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(listRooms).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(listRooms).toHaveBeenCalledTimes(3);

    unmount();
  });
});

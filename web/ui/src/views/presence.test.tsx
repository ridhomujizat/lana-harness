import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

/* The presence kit reads one module. The stream and the list are mocked so the
   face, the label, the skeleton, and the listRooms fallback can be observed
   (REQ-ROOM-006, REQ-ROOM-013). */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return { ...actual, listRooms: vi.fn(), openRoomsStream: vi.fn() };
});

import { listRooms, openRoomsStream } from "../api/rooms";
import type { RoomRow } from "../api/rooms";
import { AgentFace, PresenceLabel, SkeletonRows, useRooms } from "./presence";

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
});

describe("AgentFace (REQ-ROOM-006)", () => {
  it("shows the initial and names the presence in the label", () => {
    render(
      <AgentFace
        name="dana"
        color="success"
        presence={{ state: "working", label: "Running bash", since: 0, session_id: "s_1" }}
      />
    );

    const face = screen.getByRole("img", { name: "dana, working" });
    expect(face.textContent).toContain("D");
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
});

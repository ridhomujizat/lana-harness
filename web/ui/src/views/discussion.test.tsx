import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/teams", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/teams")>();
  return { ...actual, createTeam: vi.fn(), configVersion: vi.fn() };
});
vi.mock("./presence", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./presence")>();
  return { ...actual, refreshRooms: vi.fn().mockResolvedValue(undefined) };
});

import { configVersion, createTeam } from "../api/teams";
import type { RoomRow } from "../api/rooms";
import type { SessionEvent } from "../api/sessions";
import { discussionStatus } from "../lib/discussion";
import { DiscussionBar, TeamAvatar } from "./discussion";
import { NewGroupRoom, teamSlug } from "./new-group-room";
import { RoomGroup } from "./room-group";
import { buildTurns } from "./turn";
import type { ChatTurn } from "./turn";

function ev(seq: number, kind: string, payload: Record<string, unknown>): SessionEvent {
  return { seq, kind, turn_id: "t1", created_at: 1762876680000 + seq * 1000, payload: { v: 1, ...payload } };
}

const STARTED = ev(2, "discussion/started", {
  id: "d1",
  call_id: "c1",
  topic: "Ambil tawaran Tokopedia?",
  opening: "Saya ajak Karin dan Dana diskusi dulu.",
  lead: "lana",
  members: [
    { agent: "karin", angle: "karier" },
    { agent: "dana", angle: "uang" },
  ],
  rounds: 3,
  extra_rounds: "once",
});

function turnOf(events: SessionEvent[], running = false): ChatTurn {
  const items = buildTurns(
    [
      { seq: 1, kind: "user/message", turn_id: "t1", created_at: 1762876680000, payload: { content: [{ type: "text", text: "ambil ga?" }] } },
      { seq: 1.5, kind: "turn/start", turn_id: "t1", created_at: 1762876680000, payload: { turn_id: "t1" } },
      ...events,
    ],
    running ? { running: true, turnId: "t1", startedAt: 1762876680000 } : {}
  );
  const item = items.find((entry) => entry.kind === "turn");
  if (!item || item.kind !== "turn") {
    throw new Error("no turn");
  }
  return item.turn;
}

const room = { name: "career", kind: "team", lead: "lana", members: ["karin", "dana"], title: "", color: "brand" } as unknown as RoomRow;

describe("discussion log", () => {
  it("folds the events into asks, replies, passes and the end", () => {
    const turn = turnOf([
      STARTED,
      ev(3, "discussion/message", { id: "d1", role: "lead", agent: "karin", text: "@Karin, layak?", round: 1, call_id: "c2" }),
      ev(4, "discussion/message", { id: "d1", role: "member", agent: "karin", text: "Layak.", round: 1, call_id: "c2" }),
      ev(5, "discussion/message", { id: "d1", role: "lead", agent: "dana", text: "@Dana?", round: 1, call_id: "c3" }),
    ], true);
    const log = turn.discussion;
    expect(log?.topic).toBe("Ambil tawaran Tokopedia?");
    expect(log?.entries.map((entry) => entry.kind)).toEqual(["ask", "reply", "ask"]);
    const status = discussionStatus(log!);
    expect(status.open).toBe(true);
    expect(status.pending).toEqual(["dana"]);
    expect(status.faces).toEqual([
      { agent: "karin", state: "done" },
      { agent: "dana", state: "speaking" },
    ]);
  });
});

describe("discussion in the group room", () => {
  const ended = [
    STARTED,
    ev(3, "discussion/message", { id: "d1", role: "lead", agent: "karin", text: "@Karin, layak?", round: 1 }),
    ev(4, "discussion/message", { id: "d1", role: "member", agent: "karin", text: "Layak, persentil 60.", round: 1 }),
    ev(5, "discussion/message", { id: "d1", role: "lead", agent: "dana", text: "@Dana, setuju?", round: 1 }),
    ev(6, "discussion/message", { id: "d1", role: "member", agent: "dana", text: "setuju dengan Karin", passed: true, round: 1, reply_to: "karin" }),
    ev(7, "discussion/ended", {
      id: "d1",
      reason: "agreed",
      rounds: 1,
      limit: 3,
      summary: { conclusion: "Ambil, tapi nego dulu.", agreed: [{ text: "Gaji wajar", agent: "karin" }], different: [], open: [{ text: "Mau 31 jt?" }] },
      notes_saved: 2,
      shared_with: ["karin", "dana"],
      learning: ["dana"],
    }),
    ev(8, "assistant/message", { content: [{ type: "text", text: "Kesimpulannya: ambil." }] }),
    ev(9, "turn/end", { reason: "end_turn" }),
  ];

  it("shows the discussion as messages and a pass line", () => {
    render(<RoomGroup turn={turnOf(ended)} room={room} faces={{}} />);
    expect(screen.getByText("Saya ajak Karin dan Dana diskusi dulu.")).toBeTruthy();
    expect(screen.getByText("@Karin, layak?")).toBeTruthy();
    expect(screen.getByText("Layak, persentil 60.")).toBeTruthy();
    expect(screen.getByText("Dana passed · setuju dengan Karin")).toBeTruthy();
    expect(screen.getByText("Kesimpulannya: ambil.")).toBeTruthy();
  });

  it("opens the summary from the small Summary button", () => {
    render(<RoomGroup turn={turnOf(ended)} room={room} faces={{}} />);
    fireEvent.click(screen.getByRole("button", { name: "Summary" }));
    const dialog = screen.getByRole("dialog", { name: "Discussion summary" });
    expect(dialog.textContent).toContain("Ambil, tapi nego dulu.");
    expect(dialog.textContent).toContain("Gaji wajar");
    expect(dialog.textContent).toContain("Saved 2 notes to room memory");
    expect(dialog.textContent).toContain("Dana will learn from it");
  });

  it("offers Answer directly instead while it runs", () => {
    const onDirect = vi.fn();
    render(<RoomGroup turn={turnOf([STARTED], true)} room={room} faces={{}} onDirect={onDirect} />);
    fireEvent.click(screen.getByText("Answer directly instead"));
    expect(onDirect).toHaveBeenCalled();
  });

  it("shows a stop as a divider", () => {
    render(<RoomGroup turn={turnOf([STARTED, ev(3, "discussion/message", { id: "d1", role: "owner", stop: "stop", round: 2 })])} room={room} faces={{}} />);
    expect(screen.getByText("You stopped the discussion in round 2")).toBeTruthy();
  });
});

describe("discussion bar", () => {
  it("names who the lead is asking and stops", () => {
    const turn = turnOf([STARTED, ev(3, "discussion/message", { id: "d1", role: "lead", agent: "dana", text: "@Dana", round: 2 })], true);
    const onStop = vi.fn();
    render(<DiscussionBar log={turn.discussion!} faces={{}} onStop={onStop} stopping={false} />);
    expect(screen.getByText("Lana: asking Dana")).toBeTruthy();
    expect(screen.getByText("2/3")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalled();
  });
});

describe("team avatar", () => {
  it("shows two, three, or two faces and +N", () => {
    const { container, rerender } = render(<TeamAvatar items={[{ name: "a" }, { name: "b" }]} />);
    expect(container.querySelector(".team-av-2")).toBeTruthy();
    rerender(<TeamAvatar items={[{ name: "a" }, { name: "b" }, { name: "c" }]} />);
    expect(container.querySelector(".team-av-3")).toBeTruthy();
    expect(container.querySelector(".team-av-more")).toBeNull();
    rerender(<TeamAvatar items={["a", "b", "c", "d", "e", "f", "g"].map((name) => ({ name }))} />);
    expect(container.querySelector(".team-av-more")?.textContent).toBe("+5");
  });
});

describe("new group room", () => {
  const rows = ["lana", "karin", "dana", "cody"].map(
    (name) => ({ name, kind: "agent", title: name + " role", color: "brand", members: [], lead: "" }) as unknown as RoomRow
  );

  it("makes a team name from the room name", () => {
    expect(teamSlug("Career plan")).toBe("career-plan");
    expect(teamSlug("2027 Rumah Baru!")).toBe("room-2027-rumah-baru");
  });

  it("creates the team with the lead, members and purpose", async () => {
    vi.mocked(configVersion).mockResolvedValue("v1");
    vi.mocked(createTeam).mockResolvedValue({ name: "career-plan", kind: "team", version: "v2", restart_required: false });
    const onClose = vi.fn();
    render(<NewGroupRoom rows={rows} defaultLead="lana" onClose={onClose} />);
    fireEvent.change(screen.getByPlaceholderText("Career plan"), { target: { value: "Career plan" } });
    fireEvent.change(screen.getByPlaceholderText(/Keputusan karier/), { target: { value: "Keputusan karier" } });
    fireEvent.click(screen.getByLabelText("Add karin"));
    fireEvent.click(screen.getByLabelText("Add dana"));
    expect(screen.getByText("3 selected")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Create room" }));
    await waitFor(() => expect(createTeam).toHaveBeenCalled());
    expect(vi.mocked(createTeam).mock.calls[0]?.[0]).toMatchObject({
      name: "career-plan",
      display_name: "Career plan",
      lead: "lana",
      members: ["karin", "dana"],
      description: "Keputusan karier",
      expected_version: "v1",
      rounds: 3,
    });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("warns that a big room costs more", () => {
    render(<NewGroupRoom rows={rows} defaultLead="lana" onClose={() => undefined} />);
    for (const name of ["karin", "dana", "cody"]) {
      fireEvent.click(screen.getByLabelText("Add " + name));
    }
    expect(screen.getByRole("note").textContent).toContain("bigger rooms cost more");
  });
});

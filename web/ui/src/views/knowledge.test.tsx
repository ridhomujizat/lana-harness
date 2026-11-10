import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../api/client";
import {
  approveReview,
  consolidateKnowledge,
  getKnowledge,
  getKnowledgePage,
  getKnowledgePages,
  getKnowledgeReport,
  getReview,
  rejectReview,
  uploadKnowledge,
} from "../api/knowledge";
import type { KnowledgeStatus } from "../api/knowledge";
import { KnowledgePage } from "./knowledge";

/* The knowledge page reads two endpoints (API-KNU-003, API-KNU-004) and calls
   two more for its actions (API-KNU-005, API-KNU-006). The module is mocked so
   the render and the calls can be observed (AC-KNU-008); the real error
   helpers are kept so the busy and unavailable cases are exercised. */
vi.mock("../api/knowledge", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/knowledge")>();
  return {
    ...actual,
    getKnowledge: vi.fn(),
    getKnowledgeReport: vi.fn(),
    uploadKnowledge: vi.fn(),
    consolidateKnowledge: vi.fn(),
    getKnowledgePages: vi.fn(),
    getKnowledgePage: vi.fn(),
    getReview: vi.fn(),
    approveReview: vi.fn(),
    rejectReview: vi.fn(),
  };
});

/* The knowledge page resolves the agent label from the shared rooms store, so
   that store is stubbed with no rows and the ID stays the fallback label. */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return {
    ...actual,
    listRooms: vi.fn().mockResolvedValue([]),
    openRoomsStream: vi.fn(() => ({ close: () => {} })),
  };
});

function makeStatus(overrides: Partial<KnowledgeStatus> = {}): KnowledgeStatus {
  return {
    available: true,
    path: "/Users/ridho/.lana/knowledge",
    exists: true,
    counts: { pages: 42, sources: 10, journal_days: 7 },
    sources_pending: [],
    sources_failed: [],
    inbox_unsupported: [],
    embedding: {
      provider: "openai",
      model: "text-embedding-3-small",
      chunks: 900,
      vectors: 880,
    },
    agent: "lana",
    agent_error: "",
    consolidation: {
      running: false,
      last_at: 0,
      last_error: "",
      next_at: 0,
      report: "",
    },
    log: [],
    ...overrides,
  };
}

beforeEach(() => {
  vi.mocked(getKnowledge).mockResolvedValue(makeStatus());
  vi.mocked(getKnowledgeReport).mockResolvedValue(null);
  vi.mocked(getKnowledgePages).mockResolvedValue({ pages: [], total: 0 });
  vi.mocked(getReview).mockResolvedValue([]);
});

describe("knowledge page (AC-KNU-008)", () => {
  it("renders the vault status", async () => {
    render(<KnowledgePage />);

    expect(
      await screen.findByText("/Users/ridho/.lana/knowledge")
    ).toBeTruthy();
    const stats = document.querySelector(".knowledge-stats") as HTMLElement;
    expect(stats.textContent).toContain("42");
    expect(screen.getByText("10")).toBeTruthy();
    expect(stats.textContent).toContain("7");
    expect(
      screen.getByText(/openai text-embedding-3-small · 880\/900/)
    ).toBeTruthy();
    /* Spec 43: the knowledge agent shows with its avatar. */
    const note = Array.from(document.querySelectorAll(".knowledge-note")).find((node) =>
      (node.textContent || "").includes("Knowledge agent")
    ) as HTMLElement;
    expect(note.textContent).toContain("Knowledge agent: lana");
    expect(note.querySelector(".agent-name-chip .av")).toBeTruthy();
  });

  it("explains when the knowledge service is not running", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(
      makeStatus({ available: false })
    );
    render(<KnowledgePage />);

    expect(await screen.findByText(/not running/)).toBeTruthy();
    expect(screen.queryByText("Vault")).toBeNull();
    expect(screen.queryByText("Latest lint report")).toBeNull();
  });

  it("uploads the picked files and reports the saved paths", async () => {
    vi.mocked(uploadKnowledge).mockResolvedValue({ saved: ["inbox/notes.md"] });
    render(<KnowledgePage />);

    const input = (await screen.findByLabelText(
      "Files to upload"
    )) as HTMLInputElement;
    const file = new File(["# note"], "notes.md", { type: "text/markdown" });
    Object.defineProperty(input, "files", {
      value: [file],
      configurable: true,
    });
    fireEvent.change(input);

    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    await waitFor(() => {
      expect(vi.mocked(uploadKnowledge)).toHaveBeenCalledWith([file]);
    });
    expect(await screen.findByText("Saved 1 file: inbox/notes.md")).toBeTruthy();
  });

  it("reports a consolidation that is already running", async () => {
    vi.mocked(consolidateKnowledge).mockRejectedValue(
      new ApiError(409, "busy", "a run is in progress")
    );
    render(<KnowledgePage />);

    await screen.findByText("/Users/ridho/.lana/knowledge");
    fireEvent.click(
      screen.getByRole("button", { name: "Run consolidation now" })
    );

    expect(
      await screen.findByText("A consolidation is already running")
    ).toBeTruthy();
  });

  it("renders the report markdown and the log entries", async () => {
    vi.mocked(getKnowledgeReport).mockResolvedValue({
      path: "reports/lint-2026-11-01.md",
      markdown:
        "# Knowledge lint 2026-11-01\n\n## Likely duplicates\n- [[Budi Santoso]] and [[Pak Budi]]\n",
    });
    vi.mocked(getKnowledge).mockResolvedValue(
      makeStatus({
        log: [
          {
            heading: "2026-11-01 03:00 consolidation",
            lines: ["created [[Proyek X]]"],
          },
        ],
      })
    );
    render(<KnowledgePage />);

    expect(await screen.findByText("Knowledge lint 2026-11-01")).toBeTruthy();
    expect(screen.getByText("Likely duplicates")).toBeTruthy();
    expect(
      screen.getByText("[[Budi Santoso]] and [[Pak Budi]]")
    ).toBeTruthy();
    expect(
      screen.getByText("2026-11-01 03:00 consolidation")
    ).toBeTruthy();
    expect(screen.getByText("created [[Proyek X]]")).toBeTruthy();
  });
});

describe("knowledge domains (spec 46, AC-DKN-008)", () => {
  const scopes = [
    { scope: "shared", pages: 40, journal_days: 7, updated_at: 0 },
    { scope: "dana", pages: 2, journal_days: 1, updated_at: 0 },
  ];

  it("lists pages, filters by scope, and opens a page", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes }));
    vi.mocked(getKnowledgePages).mockImplementation(async (scope: string) => ({
      pages:
        scope === "dana"
          ? [
              {
                path: "agents/dana/wiki/Gaji.md",
                title: "Gaji",
                scope: "dana",
                type: "topic",
                description: "Aturan gaji tim",
                updated: "2026-11-10",
                mtime: 1,
              },
            ]
          : [],
      total: scope === "dana" ? 1 : 0,
    }));
    vi.mocked(getKnowledgePage).mockResolvedValue({
      path: "agents/dana/wiki/Gaji.md",
      title: "Gaji",
      scope: "dana",
      markdown: "# Gaji\n\nGaji dibayar tanggal 25.",
    });
    render(<KnowledgePage />);

    const tab = await screen.findByRole("tab", { name: /dana/ });
    fireEvent.click(tab);
    await waitFor(() => {
      expect(vi.mocked(getKnowledgePages)).toHaveBeenCalledWith("dana", "");
    });
    fireEvent.click(await screen.findByText("Gaji"));
    expect(await screen.findByText(/dibayar tanggal 25/)).toBeTruthy();
    expect(vi.mocked(getKnowledgePage)).toHaveBeenCalledWith("agents/dana/wiki/Gaji.md");
  });

  it("preselects the scope from the route", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes }));
    render(<KnowledgePage params={{ scope: "dana" }} />);
    await waitFor(() => {
      expect(vi.mocked(getKnowledgePages)).toHaveBeenCalledWith("dana", "");
    });
  });

  it("approves and rejects review items", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes, review_pending: 2 }));
    vi.mocked(getReview).mockResolvedValueOnce([
      {
        id: "p_1",
        kind: "knowledge",
        agent: "rio",
        from: "dana",
        title: "Pajak",
        action: "append",
        body: "PPh 21 dihitung bulanan",
        owner_turn: false,
        session_id: "s1",
        created_at: 1762041600000,
      },
      {
        id: "s_dana.payroll-check",
        kind: "skill",
        agent: "dana",
        from: "dana",
        title: "payroll-check",
        action: "create",
        description: "When checking payroll",
        body: "1. Open the sheet",
        owner_turn: true,
        session_id: "s2",
        created_at: 1762041000000,
      },
    ]);
    vi.mocked(getReview).mockResolvedValue([]);
    vi.mocked(approveReview).mockResolvedValue({ id: "p_1", status: "approved" });
    render(<KnowledgePage />);

    const items = await screen.findAllByTestId("review-item");
    expect(items).toHaveLength(2);
    expect(screen.getByText("Needs review")).toBeTruthy();
    fireEvent.click(items[0].querySelector("button.btn-primary") as HTMLElement);
    await waitFor(() => {
      expect(vi.mocked(approveReview)).toHaveBeenCalledWith("p_1");
    });
    expect(await screen.findByText(/Approved Pajak/)).toBeTruthy();
    expect(vi.mocked(rejectReview)).not.toHaveBeenCalled();
  });
});

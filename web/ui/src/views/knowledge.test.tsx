import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../api/client";
import {
  consolidateKnowledge,
  getKnowledge,
  getKnowledgeReport,
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
});

describe("knowledge page (AC-KNU-008)", () => {
  it("renders the vault status", async () => {
    render(<KnowledgePage />);

    expect(
      await screen.findByText("/Users/ridho/.lana/knowledge")
    ).toBeTruthy();
    expect(screen.getByText("42")).toBeTruthy();
    expect(screen.getByText("10")).toBeTruthy();
    expect(screen.getByText("7")).toBeTruthy();
    expect(
      screen.getByText(/openai text-embedding-3-small · 880\/900/)
    ).toBeTruthy();
    expect(screen.getByText(/Knowledge agent: lana/)).toBeTruthy();
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

import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ApiError } from "../api/client";
import {
  approveReview,
  consolidateKnowledge,
  getDecidedReview,
  getKnowledge,
  getKnowledgeActivity,
  getKnowledgeGraph,
  getKnowledgeHealth,
  getKnowledgePageDetail,
  getKnowledgePages,
  getKnowledgeReport,
  getReview,
  rejectReview,
  uploadKnowledge,
} from "../api/knowledge";
import type { KnowledgeGraph, KnowledgeStatus } from "../api/knowledge";
import { KnowledgePage } from "./knowledge";
import { buildGraphData } from "./knowledge-graph";
import { firstParagraph, journalTitle, latestChange, makeScopeColors, pageBody } from "./knowledge-shared";

/* force-graph draws on a canvas jsdom does not have. The fake keeps the
   handlers so a test can click a node. */
const fakeGraph = vi.hoisted(() => ({
  instances: [] as Array<Record<string, unknown>>,
}));
vi.mock("force-graph", () => {
  class FakeForceGraph {
    handlers: Record<string, unknown> = {};
    data: unknown = null;
    constructor() {
      fakeGraph.instances.push(this as unknown as Record<string, unknown>);
      return new Proxy(this, {
        get(target, prop: string) {
          if (prop in target) return (target as Record<string, unknown>)[prop];
          if (prop === "zoom") return () => 1;
          if (prop === "d3Force") return () => ({ strength: () => undefined });
          return (...args: unknown[]) => {
            if (prop === "graphData") target.data = args[0];
            else target.handlers[prop] = args[0];
            return target;
          };
        },
      });
    }
  }
  return { default: FakeForceGraph };
});

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
    getKnowledgePageDetail: vi.fn(),
    getKnowledgeGraph: vi.fn(),
    getKnowledgeActivity: vi.fn(),
    getKnowledgeHealth: vi.fn(),
    getDecidedReview: vi.fn(),
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

const scopes = [
  { scope: "shared", pages: 1, journal_days: 7, updated_at: 0 },
  { scope: "dana", pages: 2, journal_days: 1, updated_at: 0 },
];

const graph: KnowledgeGraph = {
  nodes: [
    { id: "agents/dana/wiki/Gaji.md", title: "Gaji", scope: "dana", kind: "page", type: "topic", updated: "2026-11-10", degree: 2 },
    { id: "agents/dana/wiki/BPJS.md", title: "BPJS", scope: "dana", kind: "page", type: "topic", updated: "2026-11-10", degree: 1 },
    { id: "wiki/PPh 21.md", title: "PPh 21", scope: "shared", kind: "page", type: "topic", updated: "2026-11-09", degree: 1 },
  ],
  hubs: [
    { scope: "dana", pages: 2 },
    { scope: "shared", pages: 1 },
  ],
  edges: [
    { source: "agents/dana/wiki/Gaji.md", target: "agents/dana/wiki/BPJS.md" },
    { source: "agents/dana/wiki/Gaji.md", target: "wiki/PPh 21.md" },
    { source: "agents/dana/wiki/Gaji.md", target: "missing.md" },
  ],
  counts: { pages: 3, links: 2, broken: 1 },
  truncated: false,
};

const gajiDetail = {
  path: "agents/dana/wiki/Gaji.md",
  title: "Gaji",
  scope: "dana",
  type: "topic",
  description: "",
  updated: "2026-11-10",
  markdown:
    "---\ntype: topic\n---\n# Gaji\n\nGaji dibayar tanggal 25. Lihat [[BPJS]] dan [[PPh 21|pajak]] dan [[Slip]].\n\n- Potongan: ~~4%~~ *(until 2026-11-01)* 5% *(since 2026-11-01)*\n",
  sources: ["agents/dana/journal/2026-11-01"],
  links_out: [
    { path: "agents/dana/wiki/BPJS.md", title: "BPJS", scope: "dana" },
    { path: "wiki/PPh 21.md", title: "PPh 21", scope: "shared" },
  ],
  unresolved: ["Slip"],
  links_in: [
    { path: "agents/dana/wiki/Tagihan.md", title: "Tagihan", scope: "dana", context: "transfer gaji tanggal 25" },
  ],
};

beforeEach(() => {
  fakeGraph.instances.length = 0;
  window.location.hash = "";
  vi.mocked(getKnowledge).mockResolvedValue(makeStatus());
  vi.mocked(getKnowledgeReport).mockResolvedValue(null);
  vi.mocked(getKnowledgePages).mockResolvedValue({ pages: [], total: 0 });
  vi.mocked(getReview).mockResolvedValue([]);
  vi.mocked(getDecidedReview).mockResolvedValue([]);
  vi.mocked(getKnowledgeGraph).mockResolvedValue(graph);
  vi.mocked(getKnowledgePageDetail).mockResolvedValue(gajiDetail);
  vi.mocked(getKnowledgeActivity).mockResolvedValue({ entries: [], next_before: "" });
  vi.mocked(getKnowledgeHealth).mockResolvedValue({
    last_run: null,
    next_at: "",
    lint: null,
    review_pending: 0,
    memory: [],
  });
});

describe("knowledge page shell (AC-KNU-008, AC-KGR-006)", () => {
  it("opens on the graph tab with the four tabs", async () => {
    render(<KnowledgePage />);
    const tabs = await screen.findAllByRole("tab");
    expect(tabs.map((tab) => tab.textContent)).toEqual([
      "Graph",
      "Pages42",
      "Review",
      "Activity",
    ]);
    expect(tabs[0]?.getAttribute("aria-selected")).toBe("true");
    await waitFor(() => {
      expect(vi.mocked(getKnowledgeGraph)).toHaveBeenCalledWith("all", false, false);
    });
  });

  it("explains when the knowledge service is not running", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ available: false }));
    render(<KnowledgePage />);
    expect(await screen.findByText(/not running/)).toBeTruthy();
    expect(screen.queryByRole("tab")).toBeNull();
  });

  it("uploads picked files right away and reports the saved paths", async () => {
    vi.mocked(uploadKnowledge).mockResolvedValue({ saved: ["inbox/notes.md"] });
    render(<KnowledgePage />);
    const input = (await screen.findByLabelText("Files to upload")) as HTMLInputElement;
    const file = new File(["# note"], "notes.md", { type: "text/markdown" });
    Object.defineProperty(input, "files", { value: [file], configurable: true });
    fireEvent.change(input);
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
    fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
    expect(await screen.findByText("A consolidation is already running")).toBeTruthy();
  });

  it("switches tabs through the route", async () => {
    render(<KnowledgePage />);
    fireEvent.click(await screen.findByRole("tab", { name: /Activity/ }));
    expect(window.location.hash).toBe("#/knowledge?tab=activity");
  });
});

describe("knowledge graph (REQ-KGR-051)", () => {
  it("adds hub nodes and hub edges on the client", () => {
    const data = buildGraphData(graph, (scope) => scope.toUpperCase());
    expect(data.nodes.map((node) => node.id)).toEqual([
      "hub:dana",
      "hub:shared",
      "agents/dana/wiki/Gaji.md",
      "agents/dana/wiki/BPJS.md",
      "wiki/PPh 21.md",
    ]);
    expect(data.nodes[0]?.title).toBe("DANA");
    expect(data.links.filter((link) => link.hub)).toHaveLength(3);
    /* The edge to a node that is not in the graph is dropped. */
    expect(data.links.filter((link) => !link.hub)).toHaveLength(2);
    expect(Array.from(data.neighbors.get("agents/dana/wiki/Gaji.md") || []).sort()).toEqual([
      "agents/dana/wiki/BPJS.md",
      "hub:dana",
      "wiki/PPh 21.md",
    ]);
  });

  it("hands the data to the canvas and opens the summary on a node click", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes }));
    render(<KnowledgePage />);
    expect(await screen.findByText(/3 pages · 2 links · 1 broken/)).toBeTruthy();
    await waitFor(() => {
      expect(fakeGraph.instances[0]?.data).toBeTruthy();
    });
    const instance = fakeGraph.instances[0] as { handlers: Record<string, (node: unknown) => void> };
    act(() => {
      instance.handlers.onNodeClick?.({ id: "agents/dana/wiki/Gaji.md", kind: "page", scope: "dana" });
    });
    const panel = await screen.findByTestId("graph-panel");
    expect(await within(panel).findByText("Gaji")).toBeTruthy();
    expect(within(panel).getByText(/Gaji dibayar tanggal 25/)).toBeTruthy();
    expect(within(panel).getByText("Links to · 2")).toBeTruthy();
    expect(within(panel).getByText("Linked from · 1")).toBeTruthy();
    expect(within(panel).getByText("4%")).toBeTruthy();
    expect(
      within(panel).getByRole("link", { name: "Open page" }).getAttribute("href")
    ).toBe("#/knowledge?tab=pages&page=agents%2Fdana%2Fwiki%2FGaji.md");
    expect(within(panel).getByRole("link", { name: /Ask dana/ }).getAttribute("href")).toContain(
      "#/chats/dana?draft="
    );
  });

  it("filters by scope and asks for journals", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes }));
    render(<KnowledgePage params={{ scope: "dana" }} />);
    await waitFor(() => {
      expect(vi.mocked(getKnowledgeGraph)).toHaveBeenCalledWith("dana", false, false);
    });
    fireEvent.click(screen.getByLabelText("Show journals"));
    await waitFor(() => {
      expect(vi.mocked(getKnowledgeGraph)).toHaveBeenCalledWith("dana", true, false);
    });
  });

  it("notes a small map", async () => {
    vi.mocked(getKnowledgeGraph).mockResolvedValue({
      ...graph,
      nodes: graph.nodes.slice(0, 2),
      counts: { pages: 2, links: 1, broken: 0 },
    });
    render(<KnowledgePage />);
    expect(await screen.findByText(/The map grows as agents learn. 2 pages so far./)).toBeTruthy();
  });
});

describe("knowledge pages (REQ-KGR-053)", () => {
  it("lists pages and reads one with links and backlinks", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes }));
    vi.mocked(getKnowledgePages).mockResolvedValue({
      pages: [
        { path: "agents/dana/wiki/Gaji.md", title: "Gaji", scope: "dana", type: "topic", description: "", updated: "2026-11-10", mtime: 2 },
      ],
      total: 1,
    });
    render(<KnowledgePage params={{ tab: "pages", page: "agents/dana/wiki/Gaji.md" }} />);
    const reader = await screen.findByRole("article", { name: "Gaji" });
    await within(reader).findByText(/Gaji dibayar tanggal 25/);
    expect(within(reader).getByRole("link", { name: "BPJS" }).getAttribute("href")).toBe(
      "#/knowledge?tab=pages&page=agents%2Fdana%2Fwiki%2FBPJS.md"
    );
    expect(within(reader).getByRole("link", { name: "pajak" })).toBeTruthy();
    expect(within(reader).getByText("Slip").className).toContain("is-broken");
    expect(within(reader).getByText("transfer gaji tanggal 25")).toBeTruthy();
    /* Front matter and the title heading are not repeated in the body. */
    expect(within(reader).queryByText(/type: topic/)).toBeNull();
    const row = await screen.findByRole("link", { name: /Gaji/, current: "page" });
    expect(row).toBeTruthy();
  });

  it("resolves an ?open= title from the log", async () => {
    vi.mocked(getKnowledgePages).mockImplementation(async (_scope: string, q: string) => ({
      pages: q
        ? [{ path: "agents/dana/wiki/Gaji.md", title: "Gaji", scope: "dana", type: "", description: "", updated: "", mtime: 1 }]
        : [],
      total: q ? 1 : 0,
    }));
    render(<KnowledgePage params={{ tab: "pages", open: "gaji" }} />);
    await waitFor(() => {
      expect(vi.mocked(getKnowledgePageDetail)).toHaveBeenCalledWith("agents/dana/wiki/Gaji.md");
    });
  });
});

describe("knowledge review (REQ-KGR-054)", () => {
  it("approves a proposal and filters skills", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes, review_pending: 2 }));
    vi.mocked(getReview).mockResolvedValueOnce([
      {
        id: "p_1",
        kind: "knowledge",
        agent: "rio",
        from: "dana",
        title: "Pajak",
        action: "replace",
        body: "PPh 21 dihitung bulanan",
        old_text: "PPh 21 dihitung tahunan",
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
        created_at: 1762041000,
      },
    ]);
    vi.mocked(getReview).mockResolvedValue([]);
    vi.mocked(approveReview).mockResolvedValue({ id: "p_1", status: "approved" });
    render(<KnowledgePage params={{ tab: "review" }} />);

    const items = await screen.findAllByTestId("review-item");
    expect(items).toHaveLength(2);
    expect(screen.getByText("Change Pajak")).toBeTruthy();
    expect(screen.getByText("PPh 21 dihitung tahunan").tagName).toBe("DEL");
    expect(screen.getByText("Needs review")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /Skills/ }));
    expect(screen.getAllByTestId("review-item")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: /Waiting/ }));
    const first = screen.getAllByTestId("review-item")[0] as HTMLElement;
    fireEvent.click(within(first).getByRole("button", { name: "Approve" }));
    await waitFor(() => {
      expect(vi.mocked(approveReview)).toHaveBeenCalledWith("p_1");
    });
    expect(await screen.findByText(/Approved Pajak/)).toBeTruthy();
    expect(vi.mocked(rejectReview)).not.toHaveBeenCalled();
  });

  it("shows decided items", async () => {
    vi.mocked(getDecidedReview).mockResolvedValue([
      {
        id: "p_2",
        kind: "knowledge",
        agent: "karin",
        from: "dana",
        title: "Gaji Pasar",
        action: "append",
        body: "x",
        owner_turn: true,
        session_id: "s",
        created_at: 1,
        status: "applied",
        decided_at: 2,
      },
    ]);
    render(<KnowledgePage params={{ tab: "review" }} />);
    fireEvent.click(await screen.findByRole("button", { name: /Decided/ }));
    expect(await screen.findByText("Applied")).toBeTruthy();
    expect(screen.getByText(/dana → karin · Gaji Pasar/)).toBeTruthy();
  });
});

describe("knowledge activity (REQ-KGR-055)", () => {
  it("shows the last run, the timeline with page links, health, and details", async () => {
    vi.mocked(getKnowledge).mockResolvedValue(makeStatus({ scopes }));
    vi.mocked(getKnowledgeHealth).mockResolvedValue({
      last_run: { at: "2026-11-11T06:44:00+07:00", journaled: 11, applied: 2, skipped: 1, error: "" },
      next_at: "2026-11-12T03:00:00+07:00",
      lint: { date: "2026-11-11", path: "reports/lint-2026-11-11.md", pages: 3, contradictions: 1, conflicts: 0, duplicates: 2, broken: 0, orphans: 1, missing: 0 },
      review_pending: 2,
      memory: [{ agent: "dana", characters: 3300, limit: 8000 }],
    });
    vi.mocked(getKnowledgeActivity).mockResolvedValue({
      entries: [
        { at: "2026-11-11T06:44:00+07:00", heading: "consolidation", kind: "run", actor: "", lines: ["journaled 11 sessions"] },
        { at: "2026-11-11T06:43:00+07:00", heading: "promotion dana", kind: "promotion", actor: "dana", lines: ["created [[Gaji]] (dana)"] },
      ],
      next_before: "2026-11-11T06:43:00+07:00",
    });
    vi.mocked(getKnowledgeReport).mockResolvedValue({
      path: "reports/lint-2026-11-11.md",
      markdown: "# Knowledge lint 2026-11-11\n\n## Likely duplicates\n- [[Budi]] and [[Pak Budi]]\n",
    });
    render(<KnowledgePage params={{ tab: "activity" }} />);

    expect(await screen.findByText("Nightly run")).toBeTruthy();
    expect(screen.getByText("dana updated its knowledge")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Gaji" }).getAttribute("href")).toBe(
      "#/knowledge?tab=pages&open=Gaji"
    );
    expect(await screen.findByText("chats read")).toBeTruthy();
    expect(screen.getByText("Possible duplicates")).toBeTruthy();
    expect(screen.getByText("3.3k / 8k")).toBeTruthy();
    expect(screen.getByText("/Users/ridho/.lana/knowledge")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /Open report/ }));
    expect(await screen.findByText("Knowledge lint 2026-11-11")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Load older" }));
    await waitFor(() => {
      expect(vi.mocked(getKnowledgeActivity)).toHaveBeenLastCalledWith({
        kind: undefined,
        before: "2026-11-11T06:43:00+07:00",
      });
    });
  });

  it("filters the timeline", async () => {
    render(<KnowledgePage params={{ tab: "activity" }} />);
    fireEvent.click(await screen.findByRole("button", { name: "Agent writes" }));
    await waitFor(() => {
      expect(vi.mocked(getKnowledgeActivity)).toHaveBeenLastCalledWith({ kind: "writes" });
    });
  });
});

describe("knowledge helpers", () => {
  it("strips front matter, summarises, and finds the latest change", () => {
    const md = gajiDetail.markdown;
    expect(pageBody(md).startsWith("Gaji dibayar")).toBe(true);
    expect(firstParagraph(md)).toBe("Gaji dibayar tanggal 25. Lihat BPJS dan pajak dan Slip.");
    expect(latestChange(md)).toEqual({
      old: "4%",
      until: "2026-11-01",
      current: "5%",
      since: "2026-11-01",
    });
  });

  it("names journals by agent and date", () => {
    const label = (name: string) => (name === "dana" ? "Dana" : name);
    expect(journalTitle("agents/dana/journal/2026-11-05.md", label)).toBe("Dana journal (5 Nov 2026)");
    expect(journalTitle("agents/dana/journal/2026-11-05", label)).toBe("Dana journal (5 Nov 2026)");
    expect(journalTitle("journal/2026-01-31.md", label)).toBe("Shared journal (31 Jan 2026)");
    expect(journalTitle("agents/dana/wiki/Gaji.md", label)).toBeNull();
    const data = buildGraphData(
      {
        ...graph,
        nodes: [
          ...graph.nodes,
          { id: "agents/dana/journal/2026-11-05.md", title: "2026-11-05", scope: "dana", kind: "journal", type: "", updated: "", degree: 1 },
        ],
      },
      label
    );
    expect(data.nodes.find((node) => node.kind === "journal")?.title).toBe("Dana journal (5 Nov 2026)");
  });

  it("gives every agent its own stable colour and shared grey", () => {
    const colors = makeScopeColors(["shared", "rio", "dana"]);
    expect(colors("shared")).toBe("#8A867E");
    expect(colors("dana")).not.toBe(colors("rio"));
    expect(colors("dana")).toBe(makeScopeColors(["dana", "rio"])("dana"));
  });
});

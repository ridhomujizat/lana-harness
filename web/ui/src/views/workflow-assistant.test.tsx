import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";

/* Spec 51 REQ-WFC-007/008: the workflow assistant column shows the guardian
   chat session like the main chat. The workflow, session, stream and upload
   APIs are mocked like the other view tests. */
vi.mock("../api/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workflows")>();
  return {
    ...actual,
    getAssistant: vi.fn(),
    ensureAssistantSession: vi.fn(),
    sendAssistant: vi.fn(),
    newAssistantSession: vi.fn(),
    getWorkflow: vi.fn(),
    lockPreview: vi.fn(),
    openWorkflowStream: vi.fn(() => ({ close: () => undefined })),
    scriptSuggestions: vi.fn(() => Promise.resolve({ suggestions: [] })),
  };
});
vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return { ...actual, openStream: vi.fn() };
});
vi.mock("../api/sessions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/sessions")>();
  return {
    ...actual,
    getSession: vi.fn(),
    listEvents: vi.fn(),
    getSessionUsage: vi.fn(() => new Promise(() => undefined)),
  };
});
vi.mock("../api/attachments", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/attachments")>();
  return { ...actual, uploadAttachments: vi.fn() };
});
vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return { ...actual, listAgents: vi.fn(() => Promise.resolve({ agents: [] })) };
});
vi.mock("../api/toolbox", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/toolbox")>();
  return { ...actual, listPackages: vi.fn(() => Promise.resolve({ packages: [] })) };
});

import { openStream } from "../api/client";
import type { StreamHandlers } from "../api/client";
import { uploadAttachments } from "../api/attachments";
import { getSession, listEvents } from "../api/sessions";
import type { SessionDetail, SessionEvent } from "../api/sessions";
import {
  ensureAssistantSession,
  getAssistant,
  getWorkflow,
  lockPreview,
  newAssistantSession,
  sendAssistant,
} from "../api/workflows";
import type { ChatMessage, Graph, Workflow } from "../api/workflows";
import { ToastProvider } from "../ui/gr8r";
import { WorkflowEditor } from "./workflow-editor";

const graph: Graph = {
  nodes: [
    { id: "go", type: "trigger.manual", name: "Run button", position: { x: 0, y: 0 }, params: { enabled: true } },
  ],
  edges: [],
};

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: "wf",
    name: "Test flow",
    description: "",
    guardian: "default",
    status: "draft",
    version: 1,
    version_author: "owner",
    version_summary: "",
    version_at: 0,
    graph,
    lock: null,
    lock_current: false,
    failure_default: {},
    webhook: { path: "/hooks/wf", token: "t" },
    consecutive_failures: 0,
    created_at: 0,
    updated_at: 0,
    ...overrides,
  };
}

function ev(seq: number, kind: string, turnId: string, payload: Record<string, unknown>): SessionEvent {
  return { seq, kind, turn_id: turnId, created_at: seq, payload };
}

function session(running = false): SessionDetail {
  return { id: "s1", agent: "default", channel: "workflow", turn_running: running } as unknown as SessionDetail;
}

let handlers: StreamHandlers | null = null;

beforeEach(() => {
  handlers = null;
  vi.mocked(openStream).mockImplementation((_id, options) => {
    handlers = options || null;
    return { close: () => undefined };
  });
  vi.mocked(lockPreview).mockResolvedValue({ tools: [] });
  vi.mocked(getSession).mockResolvedValue(session());
  vi.mocked(listEvents).mockResolvedValue({ events: [], last_seq: 0 });
  vi.mocked(getWorkflow).mockResolvedValue(workflow());
});

afterEach(() => {
  vi.clearAllMocks();
});

function renderEditor(wf: Workflow = workflow()) {
  return render(
    <ToastProvider>
      <WorkflowEditor workflow={wf} onSaved={() => undefined} />
    </ToastProvider>
  );
}

function composer(): HTMLTextAreaElement {
  return screen.getByRole("textbox", { name: "Message the guardian" }) as HTMLTextAreaElement;
}

describe("workflow guardian chat (spec 51)", () => {
  it("renders legacy messages first, then the session turn with its tool step and its change card", async () => {
    const messages: ChatMessage[] = [
      { id: 1, role: "owner", text: "old request", created_at: 1 },
      { id: 2, role: "guardian", text: "old reply", created_at: 2 },
      {
        id: 3,
        role: "guardian",
        text: "Add a summary step",
        created_at: 9,
        session_id: "s1",
        turn_id: "t1",
        change: { from_version: 1, to_version: 2, summary: "Add a summary step", added: ["Summarize"], removed: [], changed: [] },
      },
    ];
    vi.mocked(getAssistant).mockResolvedValue({ session_id: "s1", messages });
    vi.mocked(listEvents).mockResolvedValue({
      events: [
        ev(1, "user/message", "t1", { content: [{ type: "text", text: "add a summary" }] }),
        ev(2, "tool/call", "t1", { call_id: "c1", name: "workflow_builder", input: { action: "save" } }),
        ev(3, "tool/result", "t1", { call_id: "c1", status: "ok", output: "candidate 4" }),
        ev(4, "assistant/message", "t1", { content: [{ type: "text", text: "Published v2 after a green test." }] }),
        ev(5, "turn/end", "t1", { reason: "end_turn" }),
      ],
      last_seq: 5,
    });
    const { container } = renderEditor();
    const reply = await screen.findByText("Published v2 after a green test.");
    expect(listEvents).toHaveBeenCalledWith("s1", 0);
    expect(openStream).toHaveBeenCalledWith("s1", expect.objectContaining({ fromSeq: 5 }));
    /* The owner message of the session and the collapsed process row. */
    expect(screen.getByText("add a summary")).toBeTruthy();
    const head = container.querySelector(".wf-thread .tp-head") as HTMLButtonElement;
    expect(head).not.toBeNull();
    fireEvent.click(head);
    expect(container.querySelector(".wf-thread .tp-list")).not.toBeNull();
    /* Order: legacy bubbles, then the turn, then its change card. */
    const legacy = screen.getByText("old reply");
    const card = screen.getByText(/v1 → v2 · Add a summary step/);
    expect(legacy.compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(reply.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(card.closest(".wf-turn")).not.toBeNull();
    expect(screen.getByText("+ Summarize")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Undo" })).toBeTruthy();
  });

  it("/new starts a fresh guardian session", async () => {
    vi.mocked(getAssistant).mockResolvedValue({ session_id: "s1", messages: [] });
    vi.mocked(newAssistantSession).mockResolvedValue({ session_id: "s2" });
    renderEditor();
    await waitFor(() => expect(listEvents).toHaveBeenCalledWith("s1", 0));
    fireEvent.change(composer(), { target: { value: "/new" } });
    expect(screen.getByRole("listbox", { name: "Commands" })).toBeTruthy();
    fireEvent.keyDown(composer(), { key: "Enter" });
    await waitFor(() => expect(newAssistantSession).toHaveBeenCalledWith("wf"));
    await waitFor(() => expect(listEvents).toHaveBeenCalledWith("s2", 0));
    expect(sendAssistant).not.toHaveBeenCalled();
  });

  it("an upload before the first message ensures the session first", async () => {
    vi.mocked(getAssistant).mockResolvedValue({ session_id: "", messages: [] });
    vi.mocked(ensureAssistantSession).mockResolvedValue({ session_id: "s9" });
    vi.mocked(uploadAttachments).mockResolvedValue([
      { path: "attachments/plan.png", name: "plan.png", media_type: "image/png", size: 3 },
    ]);
    vi.mocked(sendAssistant).mockResolvedValue({ session_id: "s9", turn: "started", turn_id: "t1" });
    const { container } = renderEditor();
    await waitFor(() => expect(getAssistant).toHaveBeenCalledWith("wf"));
    const file = new File(["png"], "plan.png", { type: "image/png" });
    const input = container.querySelector("input.attach-input") as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
    fireEvent.change(composer(), { target: { value: "use this plan" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(sendAssistant).toHaveBeenCalledWith("wf", "use this plan", ["attachments/plan.png"])
    );
    expect(ensureAssistantSession).toHaveBeenCalledWith("wf");
    expect(uploadAttachments).toHaveBeenCalledWith("s9", [file]);
    expect(vi.mocked(ensureAssistantSession).mock.invocationCallOrder[0]!).toBeLessThan(
      vi.mocked(uploadAttachments).mock.invocationCallOrder[0]!
    );
    /* The column now follows the new session. */
    await waitFor(() => expect(listEvents).toHaveBeenCalledWith("s9", 0));
  });

  it("streams the running turn, stops it, and refetches the workflow when it ends", async () => {
    vi.mocked(getAssistant).mockResolvedValue({ session_id: "s1", messages: [] });
    vi.mocked(getSession).mockResolvedValue(session(true));
    vi.mocked(listEvents).mockResolvedValue({
      events: [ev(1, "user/message", "t1", { content: [{ type: "text", text: "test it" }] })],
      last_seq: 1,
    });
    vi.mocked(getWorkflow).mockResolvedValue(workflow({ version: 2 }));
    renderEditor();
    await waitFor(() => expect(handlers).not.toBeNull());
    act(() => {
      handlers!.onFrame!({ kind: "text_delta", turn_id: "t1", text: "Testing the candidate…" });
    });
    expect(await screen.findByText("Testing the candidate…")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
    act(() => {
      handlers!.onEvent!(ev(2, "assistant/message", "t1", { content: [{ type: "text", text: "All green." }] }));
      handlers!.onEvent!(ev(3, "turn/end", "t1", { reason: "end_turn" }));
    });
    expect(await screen.findByText("All green.")).toBeTruthy();
    await waitFor(() => expect(getWorkflow).toHaveBeenCalledWith("wf"));
    expect(screen.getByRole("button", { name: "Send" })).toBeTruthy();
  });

  it("shows the ask_user question card from the session log", async () => {
    vi.mocked(getAssistant).mockResolvedValue({ session_id: "s1", messages: [] });
    vi.mocked(getSession).mockResolvedValue(session(true));
    vi.mocked(listEvents).mockResolvedValue({
      events: [
        ev(1, "user/message", "t1", { content: [{ type: "text", text: "message the team" }] }),
        ev(2, "tool/call", "t1", {
          call_id: "c1",
          name: "ask_user",
          input: {
            questions: [
              {
                question: "Send a real message to the team during the test?",
                header: "Side effect",
                options: [{ label: "Yes" }, { label: "No" }],
              },
            ],
          },
        }),
        ev(3, "question/asked", "t1", {
          request_id: "q1",
          call_id: "c1",
          expires_at: Date.now() + 60_000,
        }),
      ],
      last_seq: 3,
    });
    renderEditor();
    expect(await screen.findByText("Send a real message to the team during the test?")).toBeTruthy();
  });
});

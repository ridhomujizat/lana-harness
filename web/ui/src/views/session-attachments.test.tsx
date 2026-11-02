import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Agent } from "../api/agents";
import * as agentsApi from "../api/agents";
import * as attachmentsApi from "../api/attachments";
import { ApiError } from "../api/client";
import type { ModelsResponse } from "../api/models";
import * as sessionsApi from "../api/sessions";
import type { SessionDetail, SessionEvent } from "../api/sessions";
import * as turnsApi from "../api/turns";
import { NewSessionPage } from "./new-session";
import { SessionPage } from "./session";

/* The turn calls are mocked so the send flow can be observed (API-ATT-002). */
vi.mock("../api/turns", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/turns")>();
  return {
    ...actual,
    sendMessage: vi.fn(),
    continueSession: vi.fn(),
    cancelTurn: vi.fn(),
  };
});

/* The jobs strip fetches on mount (REQ-JOB-020); these tests do not exercise
   it, so it is mocked to an empty list. */
vi.mock("../api/jobs", () => ({
  listSessionJobs: vi.fn().mockResolvedValue([]),
  stopSessionJob: vi.fn(),
}));

/* API-ATT-001: the upload is mocked so the paths it returns can be asserted. */
vi.mock("../api/attachments", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/attachments")>();
  return { ...actual, uploadAttachments: vi.fn() };
});

/* The new-session page loads the picker; the session page loads the roster. */
vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return { ...actual, listAgents: vi.fn() };
});

/* createSession and listSessions are mocked for the new-session cases; the
   session page keeps the real getSession, listEvents, and patchSession. */
vi.mock("../api/sessions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/sessions")>();
  return { ...actual, createSession: vi.fn(), listSessions: vi.fn() };
});

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
  reasoning_effort: "",
  workspace: "/tmp/work",
  parent_session_id: "",
  turn_running: false,
};

const agent: Agent = {
  name: "lana",
  kind: "agent",
  title: "",
  color: "brand",
  model: "deepseek:chat",
  policy: "ask",
  skills: [],
  mcp: [],
  mcp_status: [],
  description: "Test agent.",
  default: true,
  provider: "deepseek",
  permission: "ask",
  workspace: "/tmp/work",
  disabled_tools: [],
  tools_count: 0,
  sessions_count: 0,
  last_active_at: null,
};

/* The model catalog of GET /v1/models (REQ-MDL-020). */
const CATALOG: ModelsResponse = {
  models: [
    {
      id: "chat",
      name: "DeepSeek Chat",
      description: "Current model.",
      context_window: 128000,
      legacy: false,
      replacement: "",
      thinking: "deepseek",
      default_effort: "high",
    },
  ],
  efforts: [{ value: "high", description: "The default balance." }],
};

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function sessionEvent(
  seq: number,
  kind: string,
  turnId: string,
  payload: Record<string, unknown>
): SessionEvent {
  return { seq, kind, turn_id: turnId, created_at: seq, payload };
}

/* The minimal fetch stub of session.test.tsx: events, models, agents, stream,
   and the session detail. */
function stubFetch(events: SessionEvent[]) {
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = String(input);
      const method = init && init.method ? init.method : "GET";
      if (method === "PATCH" && url.includes("/v1/sessions/")) {
        return json(detail);
      }
      if (url.includes("/events")) {
        return json({ events, last_seq: events.length });
      }
      if (url.includes("/models")) {
        return json(CATALOG);
      }
      if (url.endsWith("/agents")) {
        return json({ agents: [] });
      }
      if (url.includes("/stream")) {
        return new Response("", { status: 200 });
      }
      return json(detail);
    }
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function fileInputOf(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector('input[type="file"]');
  if (!input) {
    throw new Error("no file input found");
  }
  return input as HTMLInputElement;
}

const realCreateObjectURL = URL.createObjectURL;
const realRevokeObjectURL = URL.revokeObjectURL;

beforeEach(() => {
  let seq = 0;
  URL.createObjectURL = vi.fn(() => {
    seq += 1;
    return "blob:test-" + seq;
  });
  URL.revokeObjectURL = vi.fn();

  vi.mocked(turnsApi.sendMessage).mockResolvedValue({ turn: "started" });
  vi.mocked(turnsApi.continueSession).mockResolvedValue({ turn: "started" });
  vi.mocked(turnsApi.cancelTurn).mockResolvedValue({ turn: "cancelling" });
  vi.mocked(agentsApi.listAgents).mockResolvedValue({ agents: [] });
  vi.mocked(sessionsApi.listSessions).mockResolvedValue({
    sessions: [],
    next_before: null,
    facets: { channel: {}, status: {}, agent: [] },
  });
  vi.mocked(sessionsApi.createSession).mockResolvedValue(detail);
  vi.mocked(attachmentsApi.uploadAttachments).mockResolvedValue([]);
});

afterEach(() => {
  URL.createObjectURL = realCreateObjectURL;
  URL.revokeObjectURL = realRevokeObjectURL;
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/");
});

/* AC-ATT-007: 2 images and a PDF upload before the send and show as chips. */
describe("session attachments", () => {
  it("uploads before sending and shows the chips then the pending bubble", async () => {
    stubFetch([]);
    vi.mocked(attachmentsApi.uploadAttachments).mockResolvedValue([
      {
        path: "attachments/2026-11-01/a.png",
        name: "a.png",
        media_type: "image/png",
        size: 3,
      },
      {
        path: "attachments/2026-11-01/b.png",
        name: "b.png",
        media_type: "image/png",
        size: 3,
      },
      {
        path: "attachments/2026-11-01/report.pdf",
        name: "report.pdf",
        media_type: "application/pdf",
        size: 3,
      },
    ]);

    const { container } = render(<SessionPage id="s1" />);
    const input = (await screen.findByLabelText(
      "Message lana"
    )) as HTMLTextAreaElement;
    const fileInput = fileInputOf(container);
    const pngA = new File(["a"], "a.png", { type: "image/png" });
    const pngB = new File(["b"], "b.png", { type: "image/png" });
    const pdf = new File(["p"], "report.pdf", { type: "application/pdf" });
    fireEvent.change(fileInput, { target: { files: [pngA, pngB, pdf] } });

    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(3);
    });
    expect(container.querySelectorAll(".attach-chip-thumb").length).toBe(2);

    fireEvent.change(input, { target: { value: "cek ini" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    await waitFor(() => {
      expect(vi.mocked(turnsApi.sendMessage)).toHaveBeenCalledWith(
        "s1",
        "cek ini",
        [
          "attachments/2026-11-01/a.png",
          "attachments/2026-11-01/b.png",
          "attachments/2026-11-01/report.pdf",
        ]
      );
    });

    const upload = vi.mocked(attachmentsApi.uploadAttachments);
    const send = vi.mocked(turnsApi.sendMessage);
    expect(upload).toHaveBeenCalledWith("s1", [pngA, pngB, pdf]);
    const uploadOrder = upload.mock.invocationCallOrder[0] ?? 0;
    const sendOrder = send.mock.invocationCallOrder[0] ?? 0;
    expect(uploadOrder).toBeLessThan(sendOrder);

    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(0);
    });
    expect(container.querySelectorAll(".chat-attachment-image").length).toBe(2);
    expect(container.querySelectorAll(".chat-attachment-file").length).toBe(1);
  });

  it("does not upload a file whose chip was removed", async () => {
    stubFetch([]);
    vi.mocked(attachmentsApi.uploadAttachments).mockResolvedValue([
      {
        path: "attachments/2026-11-01/a.png",
        name: "a.png",
        media_type: "image/png",
        size: 3,
      },
    ]);

    const { container } = render(<SessionPage id="s1" />);
    const input = (await screen.findByLabelText(
      "Message lana"
    )) as HTMLTextAreaElement;
    const fileInput = fileInputOf(container);
    const keep = new File(["a"], "a.png", { type: "image/png" });
    const drop = new File(["b"], "b.png", { type: "image/png" });
    fireEvent.change(fileInput, { target: { files: [keep, drop] } });
    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(2);
    });

    fireEvent.click(screen.getByRole("button", { name: "Remove b.png" }));
    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(1);
    });

    fireEvent.change(input, { target: { value: "cek ini" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    await waitFor(() => {
      expect(vi.mocked(attachmentsApi.uploadAttachments)).toHaveBeenCalledWith(
        "s1",
        [keep]
      );
    });
  });

  it("sends attachments with empty text", async () => {
    stubFetch([]);
    vi.mocked(attachmentsApi.uploadAttachments).mockResolvedValue([
      {
        path: "attachments/2026-11-01/a.png",
        name: "a.png",
        media_type: "image/png",
        size: 3,
      },
    ]);

    const { container } = render(<SessionPage id="s1" />);
    await screen.findByLabelText("Message lana");
    const fileInput = fileInputOf(container);
    const png = new File(["a"], "a.png", { type: "image/png" });
    fireEvent.change(fileInput, { target: { files: [png] } });
    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(1);
    });

    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    await waitFor(() => {
      expect(vi.mocked(turnsApi.sendMessage)).toHaveBeenCalledWith("s1", "", [
        "attachments/2026-11-01/a.png",
      ]);
    });
  });

  it("adds an image pasted into the textarea as a chip", async () => {
    stubFetch([]);
    const { container } = render(<SessionPage id="s1" />);
    const textarea = (await screen.findByLabelText(
      "Message lana"
    )) as HTMLTextAreaElement;
    const png = new File(["a"], "pasted.png", { type: "image/png" });
    fireEvent.paste(textarea, {
      clipboardData: { files: [png], types: ["Files"] },
    });

    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(1);
    });
  });

  it("refuses an 11th file, names it, and keeps send enabled", async () => {
    stubFetch([]);
    const { container } = render(<SessionPage id="s1" />);
    await screen.findByLabelText("Message lana");
    const fileInput = fileInputOf(container);
    const files: File[] = [];
    for (let i = 1; i <= 11; i += 1) {
      files.push(new File(["x"], "f" + i + ".txt", { type: "text/plain" }));
    }
    fireEvent.change(fileInput, { target: { files } });

    expect(await screen.findByText(/f11\.txt was not added/)).toBeTruthy();
    expect(container.querySelectorAll(".attach-chip").length).toBe(10);
    const send = screen.getByRole("button", {
      name: "Send message",
    }) as HTMLButtonElement;
    expect(send.disabled).toBe(false);
  });

  it("refuses a file over the 20 MB limit, naming it", async () => {
    stubFetch([]);
    const { container } = render(<SessionPage id="s1" />);
    await screen.findByLabelText("Message lana");
    const fileInput = fileInputOf(container);
    const big = new File(["x"], "big.bin", { type: "application/octet-stream" });
    Object.defineProperty(big, "size", { value: 21 * 1024 * 1024 });
    fireEvent.change(fileInput, { target: { files: [big] } });

    expect(
      await screen.findByText("big.bin is over the 20 MB limit.")
    ).toBeTruthy();
    expect(container.querySelectorAll(".attach-chip").length).toBe(0);
  });

  it("keeps the chips and text when the upload fails", async () => {
    stubFetch([]);
    vi.mocked(attachmentsApi.uploadAttachments).mockRejectedValue(
      new ApiError(413, "file_too_large", "file too large", [])
    );

    const { container } = render(<SessionPage id="s1" />);
    const input = (await screen.findByLabelText(
      "Message lana"
    )) as HTMLTextAreaElement;
    const fileInput = fileInputOf(container);
    const png = new File(["a"], "a.png", { type: "image/png" });
    fireEvent.change(fileInput, { target: { files: [png] } });
    fireEvent.change(input, { target: { value: "cek ini" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    expect(await screen.findByText("file too large")).toBeTruthy();
    await waitFor(() => {
      expect(container.querySelectorAll(".attach-chip").length).toBe(1);
      expect(input.value).toBe("cek ini");
    });
  });
});

/* API-ATT-003: a stored image block serves its attachment with one prefix. */
describe("session attachment history", () => {
  it("renders a stored image with the single-prefix attachment URL", async () => {
    stubFetch([
      sessionEvent(1, "turn/start", "t1", { turn_id: "t1" }),
      sessionEvent(2, "user/message", "t1", {
        content: [
          { type: "text", text: "lihat" },
          {
            type: "image",
            path: "attachments/2026-11-01/a.jpg",
            media_type: "image/jpeg",
          },
        ],
      }),
      sessionEvent(3, "turn/end", "t1", { reason: "end_turn" }),
    ]);

    render(<SessionPage id="s1" />);
    const img = (await screen.findByAltText(
      "attachments/2026-11-01/a.jpg"
    )) as HTMLImageElement;

    expect(img.getAttribute("src")).toBe(
      "/v1/sessions/s1/attachments/2026-11-01/a.jpg"
    );
    expect(img.getAttribute("src")).not.toContain("/attachments/attachments/");
  });
});

/* REQ-ATT-006: the new-session page starts a session with attachments. */
describe("new session attachments", () => {
  it("creates the session, uploads, then sends the first message", async () => {
    stubFetch([]);
    vi.mocked(agentsApi.listAgents).mockResolvedValue({ agents: [agent] });
    vi.mocked(sessionsApi.createSession).mockResolvedValue({
      ...detail,
      id: "s9",
    });
    vi.mocked(attachmentsApi.uploadAttachments).mockResolvedValue([
      {
        path: "attachments/2026-11-01/a.png",
        name: "a.png",
        media_type: "image/png",
        size: 3,
      },
    ]);

    const { container } = render(<NewSessionPage />);
    const textarea = (await screen.findByPlaceholderText(
      "What should we work on?"
    )) as HTMLTextAreaElement;
    await waitFor(() => {
      const start = screen.getByRole("button", {
        name: "Start session",
      }) as HTMLButtonElement;
      expect(start.disabled).toBe(false);
    });

    const png = new File(["a"], "a.png", { type: "image/png" });
    fireEvent.change(fileInputOf(container), { target: { files: [png] } });
    fireEvent.change(textarea, { target: { value: "cek ini" } });
    fireEvent.click(screen.getByRole("button", { name: "Start session" }));

    await waitFor(() => {
      expect(vi.mocked(turnsApi.sendMessage)).toHaveBeenCalledWith("s9", "cek ini", [
        "attachments/2026-11-01/a.png",
      ]);
    });

    const create = vi.mocked(sessionsApi.createSession);
    const upload = vi.mocked(attachmentsApi.uploadAttachments);
    const send = vi.mocked(turnsApi.sendMessage);
    expect(create).toHaveBeenCalledWith("lana", "deepseek:chat");
    expect(upload).toHaveBeenCalledWith("s9", [png]);
    const createOrder = create.mock.invocationCallOrder[0] ?? 0;
    const uploadOrder = upload.mock.invocationCallOrder[0] ?? 0;
    const sendOrder = send.mock.invocationCallOrder[0] ?? 0;
    expect(createOrder).toBeLessThan(uploadOrder);
    expect(uploadOrder).toBeLessThan(sendOrder);
    expect(window.location.hash).toBe("#/sessions/s9");
  });
});

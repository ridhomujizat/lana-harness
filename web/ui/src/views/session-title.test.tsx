import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ListSessionsResponse, Session, SessionDetail } from "../api/sessions";
import { SessionPage } from "./session";
import { SessionsPage } from "./sessions";
import { publishSessionTitle } from "./session-title";

/* Spec 40: session titles in the History list and the session header. */

vi.mock("../api/jobs", () => ({
  listSessionJobs: vi.fn().mockResolvedValue([]),
  stopSessionJob: vi.fn(),
}));

afterEach(() => {
  vi.unstubAllGlobals();
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const row: Session = {
  id: "abc",
  channel: "web",
  address: "owner",
  agent: "lana",
  title: "Disk check",
  title_source: "auto",
  title_pending: false,
  status: "idle",
  preview: "All good",
  turn_count: 1,
  created_at: 1,
  updated_at: Date.now(),
  in_use: "none",
  team: null,
};

const detail: SessionDetail = {
  ...row,
  model: "deepseek:chat",
  policy: "ask",
  reasoning_effort: "",
  workspace: "/tmp/work",
  parent_session_id: "",
  turn_running: false,
};

interface Call {
  method: string;
  url: string;
  body: Record<string, unknown> | null;
}

/* Routes the dashboard calls of both views. regenerate answers the given
   response; the stream answers streamBody once. */
function stubFetch(options: {
  sessions?: Session[];
  detail?: Partial<SessionDetail>;
  regenerate?: () => Response;
  streamBody?: string;
}) {
  const calls: Call[] = [];
  let current: SessionDetail = { ...detail, ...options.detail };
  let streamed = false;
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = String(input);
      const method = init && init.method ? init.method : "GET";
      const body = init && init.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null;
      calls.push({ method, url, body });
      if (method === "PATCH") {
        const title = String(body?.title ?? "");
        current = {
          ...current,
          title,
          title_source: title ? "owner" : "",
          title_pending: title === "",
        };
        return json(current);
      }
      if (method === "POST" && url.endsWith("/title/regenerate")) {
        return options.regenerate ? options.regenerate() : json({ ...current, title: "Fresh title", title_source: "auto" });
      }
      if (url.includes("/stream")) {
        if (!streamed && options.streamBody) {
          streamed = true;
          return new Response(options.streamBody, { status: 200 });
        }
        return new Response("", { status: 200 });
      }
      if (url.includes("/events")) {
        return json({ events: [], last_seq: 0 });
      }
      if (url.includes("/models")) {
        return json({ models: [], efforts: [] });
      }
      if (url.endsWith("/agents")) {
        return json({ agents: [] });
      }
      if (url.startsWith("/v1/sessions?") || url === "/v1/sessions") {
        const page: ListSessionsResponse = {
          sessions: options.sessions || [row],
          next_before: null,
          facets: { channel: {}, status: {}, agent: [] },
        };
        return json(page);
      }
      return json(current);
    }
  );
  vi.stubGlobal("fetch", fetchMock);
  return calls;
}

describe("session header title", () => {
  it("renames with Enter and cancels with Escape", async () => {
    const calls = stubFetch({});
    render(<SessionPage id="abc" />);
    const button = await screen.findByRole("button", { name: "Rename session: Disk check" });

    fireEvent.click(button);
    let input = screen.getByLabelText("Session title") as HTMLInputElement;
    expect(input.value).toBe("Disk check");
    fireEvent.change(input, { target: { value: "Ignored" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByLabelText("Session title")).toBeNull();
    expect(calls.some((call) => call.method === "PATCH")).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "Rename session: Disk check" }));
    input = screen.getByLabelText("Session title") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "  Server disk  " } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByRole("button", { name: "Rename session: Server disk" })).toBeTruthy();
    const patch = calls.find((call) => call.method === "PATCH");
    expect(patch?.url).toBe("/v1/sessions/abc");
    expect(patch?.body).toEqual({ title: "Server disk" });
  });

  it("an empty title goes back to an automatic one and shows Naming…", async () => {
    const calls = stubFetch({});
    render(<SessionPage id="abc" />);
    fireEvent.click(await screen.findByRole("button", { name: "Rename session: Disk check" }));
    const input = screen.getByLabelText("Session title");
    fireEvent.change(input, { target: { value: "" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByText("Naming…")).toBeTruthy();
    expect(calls.find((call) => call.method === "PATCH")?.body).toEqual({ title: "" });
  });

  it("shows the automatic title when the stream sends it", async () => {
    stubFetch({
      detail: { title: "", title_source: "", title_pending: true },
      streamBody:
        'event: title\ndata: {"title":"Fix the build","title_source":"auto","title_pending":false}\n\n',
    });
    const seen: string[] = [];
    const listener = (event: Event) => {
      seen.push((event as CustomEvent<{ title: string }>).detail.title);
    };
    window.addEventListener("lana:session-title", listener);
    render(<SessionPage id="abc" />);
    expect(await screen.findByText("Naming…")).toBeTruthy();
    expect(await screen.findByText("Fix the build")).toBeTruthy();
    window.removeEventListener("lana:session-title", listener);
    /* The History list hears the same title. */
    expect(seen).toContain("Fix the build");
  });
});

describe("History list titles", () => {
  it("renames a row inline from its pencil button", async () => {
    const calls = stubFetch({});
    render(<SessionsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Rename Disk check" }));
    const input = screen.getByLabelText("Session title");
    fireEvent.change(input, { target: { value: "Backup plan" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(await screen.findByText("Backup plan")).toBeTruthy();
    expect(calls.find((call) => call.method === "PATCH")?.body).toEqual({ title: "Backup plan" });
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("data-session-rename")).toBe("abc")
    );
  });

  it("regenerates a title from the row menu", async () => {
    const calls = stubFetch({});
    render(<SessionsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "More actions for Disk check" }));
    expect(screen.getByRole("menuitem", { name: "Rename" })).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitem", { name: "Regenerate title" }));
    expect(await screen.findByText("Fresh title")).toBeTruthy();
    expect(calls.some((call) => call.method === "POST" && call.url === "/v1/sessions/abc/title/regenerate")).toBe(true);
  });

  it("says so when a title is already being made", async () => {
    stubFetch({
      regenerate: () =>
        json({ error: { code: "title_busy", message: "a title is already being generated" } }, 409),
    });
    render(<SessionsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "More actions for Disk check" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Regenerate title" }));
    expect(await screen.findByText("A title is already being made for this session.")).toBeTruthy();
    expect(screen.getByText("Disk check")).toBeTruthy();
  });

  it("shows Naming… for a pending row and updates live", async () => {
    stubFetch({ sessions: [{ ...row, title: "", title_source: "", title_pending: true }] });
    render(<SessionsPage />);
    expect(await screen.findByText("Naming…")).toBeTruthy();
    act(() => {
      publishSessionTitle({ id: "abc", title: "Weekly report", title_source: "auto", title_pending: false });
    });
    expect(await screen.findByText("Weekly report")).toBeTruthy();
    expect(screen.queryByText("Naming…")).toBeNull();
  });

  it("shows Untitled session when nothing exists yet", async () => {
    stubFetch({ sessions: [{ ...row, title: "", title_source: "", title_pending: false }] });
    render(<SessionsPage />);
    expect(await screen.findByText("Untitled session")).toBeTruthy();
  });
});

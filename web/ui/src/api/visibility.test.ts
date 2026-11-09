import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { openStream } from "./client";
import { HIDDEN_RELEASE_MS, watchVisibility } from "./visibility";
import { openWorkflowStream } from "./workflows";

/* A body whose reader never resolves keeps the stream open until its fetch is
   aborted, exactly like a live SSE connection. */
function openBody(): Response {
  return {
    ok: true,
    status: 200,
    body: {
      getReader() {
        return {
          read: () => new Promise<never>(() => {}),
          releaseLock() {},
        };
      },
    },
  } as unknown as Response;
}

function setVisibility(state: "visible" | "hidden"): void {
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event("visibilitychange"));
}

function signalOf(call: unknown[]): AbortSignal {
  return (call[1] as RequestInit).signal as AbortSignal;
}

describe("stream visibility", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockImplementation(() => Promise.resolve(openBody()));
    setVisibility("visible");
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    setVisibility("visible");
  });

  it("releases the fetch after 15 s hidden and reconnects when visible", async () => {
    const stream = openWorkflowStream(vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const first = signalOf(fetchMock.mock.calls[0]!);
    expect(first.aborted).toBe(false);

    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(HIDDEN_RELEASE_MS);
    expect(first.aborted).toBe(true);
    /* It stays released and does not reconnect while the tab is hidden. */
    expect(fetchMock).toHaveBeenCalledTimes(1);

    setVisibility("visible");
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    stream.close();
  });

  it("keeps the fetch when the tab comes back before 15 s", async () => {
    const stream = openWorkflowStream(vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    const first = signalOf(fetchMock.mock.calls[0]!);

    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(HIDDEN_RELEASE_MS - 1);
    expect(first.aborted).toBe(false);

    setVisibility("visible");
    await vi.advanceTimersByTimeAsync(HIDDEN_RELEASE_MS);
    expect(first.aborted).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    stream.close();
  });

  it("asks the workflow view to refresh when the stream resumes", async () => {
    const onChange = vi.fn();
    const stream = openWorkflowStream(onChange);
    await vi.advanceTimersByTimeAsync(0);

    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(HIDDEN_RELEASE_MS);
    expect(onChange).not.toHaveBeenCalled();

    setVisibility("visible");
    await vi.advanceTimersByTimeAsync(0);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith({ workflow_id: "", refresh: true });
    stream.close();
  });

  it("reconnects the session stream after a hidden pause", async () => {
    const stream = openStream("s1");
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const first = signalOf(fetchMock.mock.calls[0]!);

    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(HIDDEN_RELEASE_MS);
    expect(first.aborted).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    setVisibility("visible");
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    stream.close();
  });

  it("clears the pending timer on close", async () => {
    const stream = openWorkflowStream(vi.fn());
    await vi.advanceTimersByTimeAsync(0);
    setVisibility("hidden");
    await vi.advanceTimersByTimeAsync(1000);
    stream.close();
    /* close() stops the stream for good; the helper's timer must not reconnect
       or release anything afterwards. */
    await vi.advanceTimersByTimeAsync(HIDDEN_RELEASE_MS);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does nothing without a document", () => {
    vi.stubGlobal("document", undefined);
    const onRelease = vi.fn();
    const visibility = watchVisibility({ onRelease, onResume: vi.fn() });
    expect(visibility.blocked()).toBe(false);
    visibility.close();
    expect(onRelease).not.toHaveBeenCalled();
  });
});

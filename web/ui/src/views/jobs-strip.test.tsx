import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../api/client";
import { listSessionJobs, stopSessionJob } from "../api/jobs";
import type { BackgroundJob } from "../api/jobs";
import { JobsStrip } from "./session";

/* REQ-JOB-020: the strips talks to the jobs API only, so the module is mocked
   and the component is rendered on its own. */
vi.mock("../api/jobs", () => ({
  listSessionJobs: vi.fn(),
  stopSessionJob: vi.fn(),
}));

function makeJob(overrides: Partial<BackgroundJob> = {}): BackgroundJob {
  return {
    id: "bash-1",
    command: "sleep 30",
    status: "running",
    exit_code: 0,
    pid: 100,
    started_at: Date.now(),
    ended_at: null,
    tail: "",
    ...overrides,
  };
}

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("jobs strip", () => {
  it("renders nothing with zero jobs", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([]);

    const { container } = render(<JobsStrip sessionId="s1" />);
    await waitFor(() => {
      expect(vi.mocked(listSessionJobs)).toHaveBeenCalledWith("s1");
    });
    expect(container.querySelector(".jobs-strip")).toBeNull();
  });

  it("lists the running job first, then ended jobs newest first", async () => {
    const now = Date.now();
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ id: "old", command: "cmd-old", status: "exited", started_at: now - 600000 }),
      makeJob({ id: "run", command: "cmd-run", started_at: now - 60000 }),
      makeJob({ id: "new", command: "cmd-new", status: "exited", exit_code: 1, started_at: now - 300000 }),
    ]);

    const { container } = render(<JobsStrip sessionId="s1" />);
    await screen.findByText("Background jobs");

    const commands = Array.from(
      container.querySelectorAll(".job-row .job-command")
    ).map((node) => node.textContent);
    expect(commands).toEqual(["cmd-run", "cmd-new", "cmd-old"]);
    expect(screen.getByText("exited with code 1")).toBeTruthy();
    expect(screen.getByText("exited with code 0")).toBeTruthy();
  });

  it("shows the tail in a pre when Logs is toggled", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ tail: "hello\nworld" }),
    ]);

    render(<JobsStrip sessionId="s1" />);
    const toggle = await screen.findByRole("button", { name: "Logs" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");

    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const pre = screen.getByText(/hello/);
    expect(pre.tagName).toBe("PRE");
    expect(pre.textContent).toBe("hello\nworld");
  });

  it("shows a notice when the initial fetch fails", async () => {
    vi.mocked(listSessionJobs).mockRejectedValue(
      new ApiError(500, "internal_error", "boom")
    );

    render(<JobsStrip sessionId="s1" />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("boom");
  });

  it("stops a running job and disables the button while pending", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([makeJob()]);
    let resolveStop: (job: BackgroundJob) => void = () => undefined;
    vi.mocked(stopSessionJob).mockImplementation(
      () =>
        new Promise<BackgroundJob>((resolve) => {
          resolveStop = resolve;
        })
    );

    render(<JobsStrip sessionId="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));

    expect(vi.mocked(stopSessionJob)).toHaveBeenCalledWith("s1", "bash-1");
    const pending = screen.getByRole("button", { name: "Stopping…" });
    expect((pending as HTMLButtonElement).disabled).toBe(true);

    await act(async () => {
      resolveStop(
        makeJob({ status: "killed", ended_at: Date.now(), started_at: Date.now() })
      );
    });
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    });
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeTruthy();
  });

  it("shows a stop error in the row", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([makeJob()]);
    vi.mocked(stopSessionJob).mockRejectedValue(
      new ApiError(500, "internal_error", "boom")
    );

    render(<JobsStrip sessionId="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("boom");
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
  });

  it("polls every 3s while a job runs and stops once none run", async () => {
    vi.useFakeTimers();
    const ended = makeJob({
      status: "exited",
      exit_code: 0,
      started_at: Date.now() - 60000,
      ended_at: Date.now(),
    });
    vi.mocked(listSessionJobs).mockResolvedValue([ended]);

    const { unmount } = render(<JobsStrip sessionId="s1" />);
    await act(async () => {});
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);

    // No running job: the interval is never scheduled.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);
    unmount();
  });

  it("keeps polling while a job is running and stops after it ends", async () => {
    vi.useFakeTimers();
    vi.mocked(listSessionJobs)
      .mockResolvedValueOnce([makeJob()])
      .mockResolvedValueOnce([
        makeJob({ status: "exited", exit_code: 0, ended_at: Date.now() }),
      ]);

    const { unmount } = render(<JobsStrip sessionId="s1" />);
    await act(async () => {});
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(2);

    // The job ended, so the interval is cleared.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(2);
    unmount();
  });
});

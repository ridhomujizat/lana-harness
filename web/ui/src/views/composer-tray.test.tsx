import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../api/client";
import { listSessionJobs, stopSessionJob } from "../api/jobs";
import type { BackgroundJob } from "../api/jobs";
import { ComposerTray } from "./composer-tray";

/* REQ-JBN-021, REQ-JBN-022: the pill talks to the jobs API only, so the
   module is mocked and the component is rendered on its own. */
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

describe("composer tray", () => {
  it("renders nothing with zero jobs", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([]);

    const { container } = render(<ComposerTray sessionId="s1" />);
    await waitFor(() => {
      expect(vi.mocked(listSessionJobs)).toHaveBeenCalledWith("s1");
    });
    expect(container.querySelector(".cx-tray")).toBeNull();
  });

  it("shows a quiet Background row when only ended jobs remain", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ status: "exited", exit_code: 0, ended_at: Date.now() }),
    ]);

    const { container } = render(<ComposerTray sessionId="s1" />);
    await waitFor(() => {
      expect(vi.mocked(listSessionJobs)).toHaveBeenCalled();
    });
    expect(container.querySelector(".cx-run")).toBeNull();
    expect(await screen.findByRole("button", { name: "Background" })).toBeTruthy();
    expect(screen.getByText("1 done")).toBeTruthy();
  });

  it("shows a pulsing pill with the running count", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ id: "bash-1" }),
      makeJob({ id: "bash-2" }),
    ]);

    const { container } = render(<ComposerTray sessionId="s1" />);
    expect(await screen.findByText("2 background tasks running")).toBeTruthy();
    expect(container.querySelector(".cx-run")).toBeTruthy();
    expect(screen.getByRole("button", { name: "2 background tasks running" })).toBeTruthy();
  });

  it("lists the running job first, then ended jobs newest first", async () => {
    const now = Date.now();
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ id: "old", command: "cmd-old", status: "exited", started_at: now - 600000 }),
      makeJob({ id: "run", command: "cmd-run", started_at: now - 60000 }),
      makeJob({ id: "new", command: "cmd-new", status: "exited", exit_code: 1, started_at: now - 300000 }),
    ]);

    const { container } = render(<ComposerTray sessionId="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "1 background task running" }));

    const commands = Array.from(
      container.querySelectorAll(".cx-tray-list .cx-job .cx-job-cmd")
    ).map((node) => node.textContent);
    expect(commands).toEqual(["cmd-run", "cmd-new", "cmd-old"]);
    expect(screen.getByText(/^exited 1/)).toBeTruthy();
    expect(screen.getByText(/^exited 0/)).toBeTruthy();
  });

  it("shows the tail in a pre when Logs is toggled", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ tail: "hello\nworld" }),
    ]);

    render(<ComposerTray sessionId="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "1 background task running" }));
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

    render(<ComposerTray sessionId="s1" />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("boom");
  });

  it("stops a running job and disables the button while pending", async () => {
    const now = Date.now();
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ id: "bash-1", started_at: now }),
      makeJob({ id: "bash-2", started_at: now - 1000 }),
    ]);
    let resolveStop: (job: BackgroundJob) => void = () => undefined;
    vi.mocked(stopSessionJob).mockImplementation(
      () =>
        new Promise<BackgroundJob>((resolve) => {
          resolveStop = resolve;
        })
    );

    render(<ComposerTray sessionId="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "2 background tasks running" }));
    const stops = await screen.findAllByRole("button", { name: "Stop" });
    const firstStop = stops[0];
    expect(firstStop).toBeTruthy();
    if (firstStop) {
      fireEvent.click(firstStop);
    }

    expect(vi.mocked(stopSessionJob)).toHaveBeenCalledWith("s1", "bash-1");
    const pending = screen.getByRole("button", { name: "Stopping…" });
    expect((pending as HTMLButtonElement).disabled).toBe(true);

    await act(async () => {
      resolveStop(
        makeJob({ id: "bash-1", status: "killed", ended_at: Date.now() })
      );
    });
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Stopping…" })).toBeNull();
    });
    /* The other job still runs, so the pill stays and one Stop remains. */
    expect(screen.getAllByRole("button", { name: "Stop" })).toHaveLength(1);
    expect(screen.getByText(/^stopped/)).toBeTruthy();
  });

  it("shows a stop error in the row", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([makeJob()]);
    vi.mocked(stopSessionJob).mockRejectedValue(
      new ApiError(500, "internal_error", "boom")
    );

    render(<ComposerTray sessionId="s1" />);
    fireEvent.click(await screen.findByRole("button", { name: "1 background task running" }));
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("boom");
    expect(screen.getByRole("button", { name: "Stop" })).toBeTruthy();
  });

  it("does not poll while no job is running", async () => {
    vi.useFakeTimers();
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ status: "exited", exit_code: 0, ended_at: Date.now() }),
    ]);

    render(<ComposerTray sessionId="s1" />);
    await act(async () => {});
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);
  });

  it("polls every 3s while a job is running and stops after it ends", async () => {
    vi.useFakeTimers();
    vi.mocked(listSessionJobs)
      .mockResolvedValueOnce([makeJob()])
      .mockResolvedValueOnce([
        makeJob({ status: "exited", exit_code: 0, ended_at: Date.now() }),
      ]);

    render(<ComposerTray sessionId="s1" />);
    await act(async () => {});
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(2);

    /* The job ended, so the interval is cleared. */
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(2);
  });

  /* REQ-JBN-022: the session stream bumps refreshKey and the list re-reads. */
  it("re-reads the list when refreshKey changes", async () => {
    vi.useFakeTimers();
    vi.mocked(listSessionJobs).mockResolvedValue([makeJob()]);

    const { rerender } = render(<ComposerTray sessionId="s1" refreshKey={0} />);
    await act(async () => {});
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(1);

    await act(async () => {
      rerender(<ComposerTray sessionId="s1" refreshKey={1} />);
    });
    expect(vi.mocked(listSessionJobs)).toHaveBeenCalledTimes(2);
  });

  it("opens the task panel from outside, even with only ended jobs", async () => {
    vi.mocked(listSessionJobs).mockResolvedValue([
      makeJob({ status: "killed", ended_at: Date.now() }),
    ]);
    const onOpenChange = vi.fn();

    render(<ComposerTray sessionId="s1" open onOpenChange={onOpenChange} />);
    expect(await screen.findByText(/^stopped/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Close background tasks" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

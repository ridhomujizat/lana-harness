import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { ApiError } from "../api/client";
import { getWorkbenchCore, restartWorkbenchCore, type WorkbenchCore } from "../api/workbench";
import {
  CORE_OUTDATED_NOTICE,
  RESTART_CONFIRM,
  WORKBENCH_RESTARTED_EVENT,
  WorkbenchCoreNotice,
  WorkbenchCoreRow,
  restartedText,
} from "./workbench-core";

/* Spec 84: the Workbench core row in Settings, the restart confirm, and the
   Workbench's outdated notice. */
vi.mock("../api/workbench", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workbench")>();
  return { ...actual, getWorkbenchCore: vi.fn(), restartWorkbenchCore: vi.fn() };
});

function core(over: Partial<WorkbenchCore> = {}): WorkbenchCore {
  return {
    running: true,
    version: "0.6.0-beta.13",
    commit: "aaa1111",
    daemon_version: "0.6.0-beta.14",
    daemon_commit: "bbb2222",
    outdated: true,
    sessions: 3,
    ...over,
  };
}

const current = core({ version: "0.6.0-beta.14", commit: "bbb2222", outdated: false });

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.mocked(getWorkbenchCore).mockReset();
  vi.mocked(restartWorkbenchCore).mockReset();
});

describe("WorkbenchCoreRow", () => {
  it("shows the core's version and Outdated with the daemon's version", async () => {
    vi.mocked(getWorkbenchCore).mockResolvedValue(core());
    render(<WorkbenchCoreRow />);
    expect(await screen.findByText("Outdated")).toBeTruthy();
    expect(screen.getByText(/0\.6\.0-beta\.13 \(aaa1111\)/)).toBeTruthy();
    expect(screen.getByText(/Restart to use the new version \(0\.6\.0-beta\.14\)/)).toBeTruthy();
  });

  it("shows no badge for a current core, and Not running without one", async () => {
    vi.mocked(getWorkbenchCore).mockResolvedValue(current);
    render(<WorkbenchCoreRow />);
    expect(await screen.findByText(/0\.6\.0-beta\.14 \(bbb2222\)/)).toBeTruthy();
    expect(screen.queryByText("Outdated")).toBeNull();
    cleanup();

    vi.mocked(getWorkbenchCore).mockResolvedValue(core({ running: false, version: "", commit: "", outdated: false }));
    render(<WorkbenchCoreRow />);
    expect(await screen.findByText("Not running")).toBeTruthy();
  });

  it("asks first, restarts, and reports the restored panes", async () => {
    vi.mocked(getWorkbenchCore).mockResolvedValue(core());
    vi.mocked(restartWorkbenchCore).mockResolvedValue(current);
    const restarted = vi.fn();
    window.addEventListener(WORKBENCH_RESTARTED_EVENT, restarted);
    render(<WorkbenchCoreRow />);
    await screen.findByText("Outdated");

    fireEvent.click(screen.getByRole("button", { name: "Restart Workbench" }));
    expect(screen.getByText(RESTART_CONFIRM)).toBeTruthy();
    expect(restartWorkbenchCore).not.toHaveBeenCalled();

    // Cancel restarts nothing.
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText(RESTART_CONFIRM)).toBeNull();
    expect(restartWorkbenchCore).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Restart Workbench" }));
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(restartWorkbenchCore).toHaveBeenCalledTimes(1);
    expect(await screen.findAllByText("Restarted. 3 panes restored.")).toHaveLength(2);
    expect(restarted).toHaveBeenCalledTimes(1);
    window.removeEventListener(WORKBENCH_RESTARTED_EVENT, restarted);

    // The footer's Close (the modal header has its own close button too).
    const closes = screen.getAllByRole("button", { name: "Close" });
    fireEvent.click(closes[closes.length - 1] as HTMLElement);
    await waitFor(() => expect(screen.queryByText("Outdated")).toBeNull());
    expect(screen.getByText("Restarted. 3 panes restored.")).toBeTruthy();
  });

  it("shows the error when the new core does not come up", async () => {
    vi.mocked(getWorkbenchCore).mockResolvedValue(core());
    vi.mocked(restartWorkbenchCore).mockRejectedValue(
      new ApiError(503, "workbench_unavailable", "the new Workbench core did not start"),
    );
    render(<WorkbenchCoreRow />);
    await screen.findByText("Outdated");
    fireEvent.click(screen.getByRole("button", { name: "Restart Workbench" }));
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect((await screen.findByRole("alert")).textContent).toBe("the new Workbench core did not start");
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
  });

  it("says one pane in the singular", () => {
    expect(restartedText(1)).toBe("Restarted. 1 pane restored.");
    expect(restartedText(0)).toBe("Restarted. 0 panes restored.");
  });
});

describe("WorkbenchCoreNotice", () => {
  it("shows only while the core is outdated, and Restart opens the restart", () => {
    const onRestart = vi.fn();
    const { container, rerender } = render(<WorkbenchCoreNotice core={current} onRestart={onRestart} />);
    expect(container.querySelector(".wb-core-notice")).toBeNull();
    rerender(<WorkbenchCoreNotice core={null} onRestart={onRestart} />);
    expect(container.querySelector(".wb-core-notice")).toBeNull();

    rerender(<WorkbenchCoreNotice core={core()} onRestart={onRestart} />);
    expect(screen.getByText(CORE_OUTDATED_NOTICE)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(onRestart).toHaveBeenCalledTimes(1);
  });

  it("stays dismissed for this build and returns for the next", () => {
    const { container } = render(<WorkbenchCoreNotice core={core()} onRestart={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(container.querySelector(".wb-core-notice")).toBeNull();
    cleanup();

    render(<WorkbenchCoreNotice core={core()} onRestart={() => {}} />);
    expect(screen.queryByText(CORE_OUTDATED_NOTICE)).toBeNull();
    cleanup();

    render(<WorkbenchCoreNotice core={core({ daemon_version: "0.6.0-beta.15" })} onRestart={() => {}} />);
    expect(screen.getByText(CORE_OUTDATED_NOTICE)).toBeTruthy();
  });
});

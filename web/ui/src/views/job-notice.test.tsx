import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { JobNoticePayload } from "../api/sessions";
import { JobNoticeLine } from "./job-notice";

/* REQ-JBN-020: one compact system line per ended job. */

function makeJob(overrides: Partial<JobNoticePayload> = {}): JobNoticePayload {
  return {
    id: "bash-1",
    command: "npm run dev",
    status: "exited",
    exit_code: 0,
    tail: "",
    ...overrides,
  };
}

describe("job notice line", () => {
  it("reads finished for a clean exit", () => {
    render(<JobNoticeLine job={makeJob()} />);
    expect(screen.getByText("Background task finished")).toBeTruthy();
  });

  it("names the exit code when the job failed", () => {
    render(<JobNoticeLine job={makeJob({ exit_code: 1 })} />);
    expect(screen.getByText("Background task failed · exit 1")).toBeTruthy();
  });

  it("reads stopped when the job was killed", () => {
    render(<JobNoticeLine job={makeJob({ status: "killed" })} />);
    expect(screen.getByText("Background task stopped")).toBeTruthy();
  });

  it("shows the command in mono, truncated by the layout", () => {
    render(<JobNoticeLine job={makeJob({ command: "npm run dev -- --host" })} />);
    const command = screen.getByText("npm run dev -- --host");
    expect(command.tagName).toBe("CODE");
    expect(command.className).toBe("job-notice-cmd");
    expect(command.getAttribute("title")).toBe("npm run dev -- --host");
  });

  it("toggles the stored tail on click", () => {
    render(<JobNoticeLine job={makeJob({ tail: "ready in 300ms\nlistening" })} />);
    const row = screen.getByRole("button", {
      name: /Background task finished/,
    });
    expect(row.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText(/ready in 300ms/)).toBeNull();

    fireEvent.click(row);
    expect(row.getAttribute("aria-expanded")).toBe("true");
    const pre = screen.getByText(/ready in 300ms/);
    expect(pre.tagName).toBe("PRE");
    expect(pre.textContent).toBe("ready in 300ms\nlistening");

    fireEvent.click(row);
    expect(row.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText(/ready in 300ms/)).toBeNull();
  });

  it("falls back to a placeholder when the tail is empty", () => {
    render(<JobNoticeLine job={makeJob({ tail: "" })} />);
    fireEvent.click(screen.getByRole("button", { name: /Background task finished/ }));
    expect(screen.getByText("No output.")).toBeTruthy();
  });
});

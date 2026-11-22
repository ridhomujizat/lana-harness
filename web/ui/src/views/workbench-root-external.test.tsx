import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

import type { WorkbenchPreview } from "../api/workbench";

vi.mock("../api/workbench-git", async (orig) => ({
  ...(await orig<typeof import("../api/workbench-git")>()),
  listFolder: vi.fn(() => new Promise(() => {})),
  getGitTree: vi.fn(() => new Promise(() => {})),
}));

import { RootFilesTab } from "./workbench-root";

/* A ⌘+clicked file outside every repo of a non-git root (e.g. an agent's
   temp scratchpad) opens beside the root's tree instead of being dropped. */

const external: WorkbenchPreview = {
  path: "/private/tmp/claude-501/scratchpad/review.md",
  name: "review.md",
  dir: "/private/tmp/claude-501/scratchpad",
  kind: "markdown",
  size: 512,
  line: 0,
  col: 0,
  url: "/v1/workbench/preview/tok/review.md?view=1",
  raw_url: "/v1/workbench/preview/tok/review.md",
};

const root = { path: "/Users/me/hub", name: "hub", repos: 2 };

describe("root Files tab", () => {
  it("shows an outside file in the viewer", () => {
    const { container } = render(<RootFilesTab root={root} summary={null} selected={null} onSelect={() => {}} external={external} />);
    expect(screen.getByText("review.md")).toBeTruthy();
    expect(container.querySelector("iframe")?.getAttribute("src")).toContain("/v1/workbench/preview/tok/review.md?view=1");
  });

  it("shows the empty viewer without one", () => {
    const { container } = render(<RootFilesTab root={root} summary={null} selected={null} onSelect={() => {}} />);
    expect(container.querySelector("iframe")).toBeNull();
    expect(screen.getByText(/Pick a file/)).toBeTruthy();
  });

  it("opens an outside file on the phone too", () => {
    const { container } = render(<RootFilesTab root={root} summary={null} selected={null} onSelect={() => {}} external={external} phone />);
    expect(container.querySelector(".wbg-phone-diff iframe")).not.toBeNull();
  });
});

describe("figcraftBase", () => {
  it("reads a repo file against its repo", async () => {
    const { figcraftBase } = await import("./workbench-root");
    expect(figcraftBase("/Users/me/hub", { key: "/Users/me/hub/app/d.figcraft", worktree: "/Users/me/hub/app", rel: "d.figcraft" })).toEqual({
      worktree: "/Users/me/hub/app",
      path: "d.figcraft",
    });
  });

  it("reads a non-git file against the root", async () => {
    const { figcraftBase } = await import("./workbench-root");
    expect(figcraftBase("/Users/me/hub", { key: "/Users/me/hub/docs/mockups/x/x.figcraft" })).toEqual({
      worktree: "/Users/me/hub",
      path: "docs/mockups/x/x.figcraft",
    });
  });

  it("falls back to the file's own folder outside the root", async () => {
    const { figcraftBase } = await import("./workbench-root");
    expect(figcraftBase("/Users/me/hub", { key: "/tmp/a/b.figcraft" })).toEqual({ worktree: "/tmp/a", path: "b.figcraft" });
  });
});

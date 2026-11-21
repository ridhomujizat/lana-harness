import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

import type { WorkbenchPreview } from "../api/workbench";
import { WorkbenchPreview as PreviewPanel, humanSize, shortenHome } from "./workbench-preview";

/* Spec 61 REQ-WP-013, REQ-WP-051: the preview panel's header, body and the
   binary fallback. */

function fixture(over: Partial<WorkbenchPreview> = {}): WorkbenchPreview {
  return {
    path: "/Users/me/code/lana/README.md",
    name: "README.md",
    dir: "/Users/me/code/lana",
    kind: "markdown",
    size: 2048,
    line: 0,
    col: 0,
    url: "/v1/workbench/preview/tok/README.md?view=1",
    raw_url: "/v1/workbench/preview/tok/README.md",
    ...over,
  };
}

describe("preview panel", () => {
  it("shows the name, the ~-shortened folder and a sandboxed iframe", () => {
    const { container } = render(<PreviewPanel data={fixture()} onClose={() => {}} />);
    expect(screen.getByText("README.md")).toBeTruthy();
    expect(screen.getByText("~/code/lana")).toBeTruthy();

    const frame = container.querySelector("iframe");
    expect(frame).not.toBeNull();
    /* The page draws in the app's theme (jsdom: light). */
    expect(frame!.getAttribute("src")).toBe("/v1/workbench/preview/tok/README.md?view=1&theme=light");
    const sandbox = frame!.getAttribute("sandbox") ?? "";
    expect(sandbox).toBe("allow-scripts allow-forms allow-popups");
    expect(sandbox).not.toContain("allow-same-origin");
  });

  it("opens and downloads the raw file", () => {
    render(<PreviewPanel data={fixture()} onClose={() => {}} />);
    const open = screen.getByRole("link", { name: "Open in new tab" });
    expect(open.getAttribute("href")).toBe("/v1/workbench/preview/tok/README.md");
    expect(open.getAttribute("target")).toBe("_blank");
    expect(open.getAttribute("rel")).toBe("noopener");

    const download = screen.getByRole("link", { name: "Download" });
    expect(download.getAttribute("href")).toBe("/v1/workbench/preview/tok/README.md");
    expect(download.getAttribute("download")).toBe("README.md");
  });

  it("shows a binary file's name and size instead of an iframe", () => {
    const { container } = render(
      <PreviewPanel data={fixture({ kind: "binary", name: "archive.bin", size: 4096 })} onClose={() => {}} />
    );
    expect(container.querySelector("iframe")).toBeNull();
    const block = container.querySelector(".wb-preview-binary");
    expect(block).not.toBeNull();
    expect(block!.querySelector("b")?.textContent).toBe("archive.bin");
    expect(block!.textContent).toContain("4 KB");
    expect(block!.querySelector("a")?.getAttribute("download")).toBe("archive.bin");
  });

  it("closes on Esc", () => {
    const onClose = vi.fn();
    render(<PreviewPanel data={fixture()} onClose={onClose} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("preview helpers", () => {
  it("shortens the home folder", () => {
    expect(shortenHome("/Users/me/code/x")).toBe("~/code/x");
    expect(shortenHome("/home/me")).toBe("~");
    expect(shortenHome("/tmp/a")).toBe("/tmp/a");
  });

  it("formats sizes", () => {
    expect(humanSize(512)).toBe("512 B");
    expect(humanSize(1536)).toBe("1.5 KB");
    expect(humanSize(2048)).toBe("2 KB");
    expect(humanSize(1048576)).toBe("1 MB");
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import * as toolbox from "../api/toolbox";
import { ToastProvider } from "../ui/gr8r";
import { PLUGINS } from "./plugins";
import { ToolsPage } from "./tools";

/* Spec 77 REQ-WRA-007: the claude-swap plugin left; the Plugins section hides
   while there is no plugin. */
vi.mock("../api/toolbox", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/toolbox")>();
  return { ...actual, listPackages: vi.fn(), openToolboxStream: vi.fn(() => ({ close: () => {} })) };
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const pkg: toolbox.PackageView = {
  id: "claude-swap",
  name: "claude-swap",
  description: "Claude Code account switcher",
  homepage: "",
  status: "installed",
  version: "1.0.0",
  binary_path: "/usr/local/bin/cswap",
  error: "",
  installing: false,
  commands: [],
  install: [],
  updated_at: 0,
};

describe("ToolsPage", () => {
  it("has no plugins and hides the Plugins section", async () => {
    expect(PLUGINS).toEqual([]);
    vi.mocked(toolbox.listPackages).mockResolvedValue({ packages: [pkg] } as Awaited<ReturnType<typeof toolbox.listPackages>>);
    render(
      <ToastProvider>
        <ToolsPage />
      </ToastProvider>,
    );
    expect(await screen.findByText("Installed tools")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Plugins" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Tools & plugins" })).toBeTruthy();
  });
});

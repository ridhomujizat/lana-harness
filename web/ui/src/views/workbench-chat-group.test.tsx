import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ChatItem } from "../api/workbench-chat";
import { ToolGroup } from "./workbench-chat";

const tool = (id: string): ChatItem => ({ id, kind: "tool", name: "Bash", status: "done", summary: { verb: "Bash", target: "ls " + id } });

describe("ToolGroup", () => {
  it("counts only tools and shows thinking as a step row", () => {
    const items: ChatItem[] = [
      { id: "t0", kind: "thinking", text: "Check the tree first.\nThen list files." },
      tool("a"),
      tool("b"),
    ];
    const { container } = render(<ToolGroup items={items} />);
    expect(screen.getByText("2 steps")).toBeTruthy();
    expect(screen.getByText("2 commands")).toBeTruthy();
    fireEvent.click(screen.getByText("2 steps"));
    const row = container.querySelector(".wbc-think-row")!;
    expect(row.textContent).toContain("Thinking");
    expect(row.textContent).toContain("Check the tree first.");
    expect(row.textContent).not.toContain("Then list files.");
    fireEvent.click(row.querySelector(".wbc-tool-line")!);
    expect(container.querySelector(".wbc-think-text")!.textContent).toBe("Check the tree first.\nThen list files.");
  });
});

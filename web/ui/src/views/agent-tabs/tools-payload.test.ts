import { describe, expect, it } from "vitest";
import type { ToolEntry, ToolGroup } from "../../api/tools";
import { computeToolsPayload } from "./tools-payload";

function entry(
  name: string,
  source: ToolEntry["source"],
  enabled: boolean
): ToolEntry {
  return { name, risk: "read", asks_first: false, enabled, source };
}

function group(id: string, tools: ToolEntry[]): ToolGroup {
  return { id, label: id, tools };
}

describe("computeToolsPayload", () => {
  it("turns a not-listed built-in on by adding it to tools", () => {
    const payload = computeToolsPayload(
      [group("chat", [entry("ask_user", "not_listed", true)])],
      { tools: ["read_file"], disabled_tools: ["ask_user"], mcp: [] }
    );
    expect(payload).toEqual({
      tools: ["read_file", "ask_user"],
      disabled_tools: [],
      mcp: [],
    });
  });

  it("turns a listed or auto built-in on by clearing disabled_tools", () => {
    const payload = computeToolsPayload(
      [
        group("shell", [entry("bash", "listed", true)]),
        group("mcp-admin", [entry("mcp_manage", "auto", true)]),
      ],
      {
        tools: ["read_file", "bash"],
        disabled_tools: ["bash", "mcp_manage"],
        mcp: [],
      }
    );
    expect(payload.tools).toEqual(["read_file", "bash"]);
    expect(payload.disabled_tools).toEqual([]);
  });

  it("turns a built-in off by adding it to disabled_tools and keeping tools", () => {
    const payload = computeToolsPayload(
      [
        group("files", [
          entry("read_file", "listed", true),
          entry("write_file", "not_listed", false),
        ]),
        group("shell", [entry("bash", "listed", false)]),
      ],
      { tools: ["read_file", "bash"], disabled_tools: [], mcp: [] }
    );
    expect(payload.tools).toEqual(["read_file", "bash"]);
    // write_file is not listed, so it is off already and is not written.
    expect(payload.disabled_tools).toEqual(["bash"]);
  });

  it("attaches a not-attached MCP server and clears the tool reference", () => {
    const payload = computeToolsPayload(
      [
        group("mcp:github", [
          entry("github/create_issue", "not_attached", true),
        ]),
      ],
      {
        tools: ["read_file"],
        disabled_tools: ["github/create_issue"],
        mcp: [],
      }
    );
    expect(payload).toEqual({
      tools: ["read_file"],
      disabled_tools: [],
      mcp: ["github"],
    });
  });

  it("removes or adds the reference of an attached MCP tool", () => {
    const on = computeToolsPayload(
      [
        group("mcp:github", [
          entry("github/create_issue", "attached", true),
        ]),
      ],
      {
        tools: [],
        disabled_tools: ["github/create_issue"],
        mcp: ["github"],
      }
    );
    expect(on.disabled_tools).toEqual([]);

    const off = computeToolsPayload(
      [
        group("mcp:github", [
          entry("github/create_issue", "attached", false),
        ]),
      ],
      { tools: [], disabled_tools: [], mcp: ["github"] }
    );
    expect(off.disabled_tools).toEqual(["github/create_issue"]);
  });

  it("turns every tool of a group off and keeps unrelated entries", () => {
    const payload = computeToolsPayload(
      [
        group("files", [entry("read_file", "listed", false)]),
        group("chat", [entry("ask_user", "not_listed", false)]),
        group("mcp:github", [
          entry("github/create_issue", "not_attached", false),
        ]),
      ],
      {
        tools: ["read_file"],
        disabled_tools: ["hidden_tool"],
        mcp: ["other"],
      }
    );
    expect(payload.tools).toEqual(["read_file"]);
    // Not-listed and not-attached tools are off already and are not written.
    expect(payload.disabled_tools).toEqual(["hidden_tool", "read_file"]);
    expect(payload.mcp).toEqual(["other"]);
  });
});

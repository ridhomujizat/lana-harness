/* REQ-TTS-002: build the [agents.<name>] fields the Tools tab saves. The
   catalog source says how a tool is offered today; the switch says what the
   owner wants. The payload keeps listed tools and MCP servers that the tab did
   not change. */

import type { ToolGroup } from "../../api/tools";

export interface ToolConfigState {
  tools: string[];
  disabled_tools: string[];
  mcp: string[];
}

/* isMcpGroup reports whether the catalog group is one MCP server. */
export function isMcpGroup(group: ToolGroup): boolean {
  return group.id.indexOf("mcp:") === 0;
}

/* mcpServerName returns the server name of an MCP group, or "". */
export function mcpServerName(group: ToolGroup): string {
  return isMcpGroup(group) ? group.id.slice("mcp:".length) : "";
}

function addUnique(list: string[], name: string) {
  if (name && !list.includes(name)) {
    list.push(name);
  }
}

function without(list: string[], name: string): string[] {
  return list.filter((item) => item !== name);
}

/* computeToolsPayload applies the REQ-TTS-002 switch rules to the catalog.
   Off always disables a tool; it never removes a built-in from tools. On for a
   not-listed built-in adds it to tools, and On for a not-attached MCP server
   attaches the server. */
export function computeToolsPayload(
  groups: ToolGroup[],
  state: ToolConfigState
): ToolConfigState {
  const tools = [...state.tools];
  let disabled = [...state.disabled_tools];
  const mcp = [...state.mcp];

  for (const group of groups) {
    const server = mcpServerName(group);
    for (const tool of group.tools) {
      // MCP catalog names are already the config-facing "<server>/<tool>"
      // reference; built-ins use the plain tool name.
      const ref = tool.name;
      if (server) {
        if (tool.enabled) {
          if (tool.source === "not_attached") {
            addUnique(mcp, server);
          }
          disabled = without(disabled, ref);
        } else if (tool.source !== "not_attached") {
          // A tool of a server the agent does not use is off already.
          addUnique(disabled, ref);
        }
        continue;
      }
      if (tool.enabled) {
        if (tool.source === "not_listed") {
          addUnique(tools, tool.name);
        }
        disabled = without(disabled, tool.name);
      } else if (tool.source !== "not_listed") {
        // A tool that is not listed is off already; keep disabled_tools short.
        addUnique(disabled, tool.name);
      }
    }
  }

  return { tools, disabled_tools: disabled, mcp };
}

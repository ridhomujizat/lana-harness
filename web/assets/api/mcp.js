import { request } from "/assets/api.js";

/* MCP calls of API-WEB-027 and the agent `mcp` list edits of API-WEB-025.
   The config version travels with every write, like agent-detail.js does. */

export function listMcpServers() {
  return request("GET", "/v1/mcp");
}

export function saveMcpServer(name, definition, expectedVersion) {
  const body = Object.assign({}, definition, {
    expected_version: expectedVersion,
  });
  return request("PUT", "/v1/mcp/" + encodeURIComponent(name), body);
}

export function deleteMcpServer(name, expectedVersion) {
  return request("DELETE", "/v1/mcp/" + encodeURIComponent(name), {
    expected_version: expectedVersion,
  });
}

export function testMcpServer(definition) {
  return request("POST", "/v1/mcp/test", definition);
}

export function restartMcpServer(name) {
  return request(
    "POST",
    "/v1/mcp/" + encodeURIComponent(name) + "/restart"
  );
}

export function listAgents() {
  return request("GET", "/v1/agents");
}

export function saveAgentMcp(name, mcp, expectedVersion) {
  return request("PUT", "/v1/agents/" + encodeURIComponent(name), {
    mcp: Array.isArray(mcp) ? mcp : [],
    expected_version: expectedVersion,
  });
}

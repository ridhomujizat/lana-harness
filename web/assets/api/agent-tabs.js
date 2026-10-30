import { request, errorMessage, ApiError } from "/assets/api.js";

/* API calls for the agent detail tabs (T-030). The shared api.js client sets
   the CSRF header and throws ApiError for every failure. */

export { errorMessage };

export function isErrorCode(error, code) {
  return error instanceof ApiError && error.code === code;
}

/* The General tab reads the config for its version and settings. The tabs use
   the same read plus the agent row for keys the settings view omits
   (disabled_tools). */
export function loadAgentTab(name) {
  return Promise.all([
    request("GET", "/v1/config"),
    request("GET", "/v1/agents"),
  ]).then(([config, page]) => {
    const settings = config && config.settings ? config.settings : {};
    const agents = settings.agents || {};
    const rows = page && Array.isArray(page.agents) ? page.agents : [];
    return {
      version: config && config.version ? String(config.version) : "",
      agent: agents[name] || {},
      row: rows.find((item) => item && item.name === name) || {},
    };
  });
}

/* API-WEB-016: the shared USER.md and the agent's AGENT.md and MEMORY.md. */
export function getAgentFile(name, file) {
  return request(
    "GET",
    "/v1/agents/" + encodeURIComponent(name) + "/files/" + encodeURIComponent(file)
  );
}

export function saveAgentFile(name, file, content, expectedVersion) {
  return request(
    "PUT",
    "/v1/agents/" + encodeURIComponent(name) + "/files/" + encodeURIComponent(file),
    { content, expected_version: expectedVersion }
  );
}

/* API-WEB-026: the tool groups, On state, and Asks first markers. */
export function getToolCatalog(name) {
  return request("GET", "/v1/tools?agent=" + encodeURIComponent(name));
}

/* API-WEB-025: save any subset of the agent keys with the config version. */
export function updateAgent(name, fields, expectedVersion) {
  const body = Object.assign({}, fields, {
    expected_version: expectedVersion,
  });
  return request("PUT", "/v1/agents/" + encodeURIComponent(name), body);
}

/* API-WEB-017: the global and agent-scoped skill library. */
export function listSkills() {
  return request("GET", "/v1/skills");
}

/* API-WEB-027: the configured MCP servers with their live status. */
export function listMcp() {
  return request("GET", "/v1/mcp");
}

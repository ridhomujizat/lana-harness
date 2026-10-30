/* Skills and agent-skill API calls (REQ-WEB-048, REQ-WEB-049;
   AC-WEB-051, AC-WEB-052). Thin wrappers over request() so the views never
   build paths by hand. */

import { request } from "/assets/api.js";

function skillPath(scope, name) {
  return (
    "/v1/skills/" +
    encodeURIComponent(scope) +
    "/" +
    encodeURIComponent(name)
  );
}

export function listSkills(scope) {
  const path = "/v1/skills";
  if (!scope) {
    return request("GET", path);
  }
  return request("GET", path + "?scope=" + encodeURIComponent(scope));
}

export function listAgents() {
  return request("GET", "/v1/agents");
}

export function getSkill(scope, name) {
  return request("GET", skillPath(scope, name));
}

export function putSkill(scope, name, content, expectedVersion) {
  return request("PUT", skillPath(scope, name), {
    content,
    expected_version: expectedVersion || "",
  });
}

export function deleteSkill(scope, name, expectedVersion) {
  return request("DELETE", skillPath(scope, name), {
    expected_version: expectedVersion,
  });
}

/* PUT /v1/agents/{name} takes the full skills list and the config version
   (API-WEB-025). The response carries the new version, so a batch can chain
   one save into the next. */
export function putAgentSkills(name, skills, expectedVersion) {
  return request("PUT", "/v1/agents/" + encodeURIComponent(name), {
    skills,
    expected_version: expectedVersion,
  });
}

export function getConfig() {
  return request("GET", "/v1/config");
}

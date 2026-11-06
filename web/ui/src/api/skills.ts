import { request } from "./client";

/* Skills and agent-skill calls (REQ-WEB-048, REQ-WEB-049;
   AC-WEB-051, AC-WEB-052). */

export { listAgents } from "./agents";
export { getConfig } from "./settings";

export interface SkillRow {
  name: string;
  scope: string;
  description: string;
  valid: boolean;
  error: string;
  size: number;
  updated_at: number;
}

export interface ListSkillsResponse {
  skills: SkillRow[];
  invalid: SkillRow[];
}

export interface SkillContentResponse {
  content: string;
  version: string;
  path: string;
}

export interface PutSkillResponse {
  version: string;
  path: string;
}

export interface DeleteSkillResponse {
  name: string;
  scope: string;
  deleted: boolean;
}

export interface PutAgentSkillsResponse {
  name: string;
  version: string;
  restart_required: boolean;
}

function skillPath(scope: string, name: string): string {
  return (
    "/v1/skills/" +
    encodeURIComponent(scope) +
    "/" +
    encodeURIComponent(name)
  );
}

export function listSkills(scope?: string): Promise<ListSkillsResponse> {
  const path = "/v1/skills";
  if (!scope) {
    return request<ListSkillsResponse>("GET", path);
  }
  return request<ListSkillsResponse>(
    "GET",
    path + "?scope=" + encodeURIComponent(scope)
  );
}

export function getSkill(
  scope: string,
  name: string
): Promise<SkillContentResponse> {
  return request<SkillContentResponse>("GET", skillPath(scope, name));
}

export function putSkill(
  scope: string,
  name: string,
  content: string,
  expectedVersion: string
): Promise<PutSkillResponse> {
  return request<PutSkillResponse>("PUT", skillPath(scope, name), {
    content,
    expected_version: expectedVersion || "",
  });
}

export function saveSkill(
  scope: string,
  name: string,
  content: string,
  expectedVersion: string
): Promise<PutSkillResponse> {
  return putSkill(scope, name, content, expectedVersion);
}

export function deleteSkill(
  scope: string,
  name: string,
  expectedVersion: string
): Promise<DeleteSkillResponse> {
  return request<DeleteSkillResponse>("DELETE", skillPath(scope, name), {
    expected_version: expectedVersion,
  });
}

/* PUT /v1/agents/{name} takes the full skills list and the config version
   (API-WEB-025). The response carries the new version, so a batch can chain
   one save into the next. */
export function putAgentSkills(
  name: string,
  skills: string[],
  expectedVersion: string
): Promise<PutAgentSkillsResponse> {
  return request<PutAgentSkillsResponse>(
    "PUT",
    "/v1/agents/" + encodeURIComponent(name),
    { skills, expected_version: expectedVersion }
  );
}

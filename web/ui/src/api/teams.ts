import { request } from "./client";

/* Team calls of API-TEAM-007 through API-TEAM-010. */

export interface TeamRecord {
  name: string;
  lead: string;
  members: string[];
  description: string;
}

export interface TeamResponse extends TeamRecord {
  version: string;
  restart_required: boolean;
}

export interface TeamCreatedResponse {
  name: string;
  kind: string;
  version: string;
  restart_required: boolean;
}

export interface TeamDeletedResponse {
  name: string;
  deleted: boolean;
  version: string;
  restart_required: boolean;
}

export interface CreateTeamRequest {
  name: string;
  lead: string;
  members: string[];
  description?: string;
  expected_version: string;
}

export interface SaveTeamRequest {
  lead: string;
  members: string[];
  description?: string;
  expected_version: string;
}

export function createTeam(body: CreateTeamRequest): Promise<TeamCreatedResponse> {
  return request<TeamCreatedResponse>("POST", "/v1/teams", body);
}

export function getTeam(name: string): Promise<TeamResponse> {
  return request<TeamResponse>(
    "GET",
    "/v1/teams/" + encodeURIComponent(name)
  );
}

export function saveTeam(
  name: string,
  body: SaveTeamRequest
): Promise<TeamResponse> {
  return request<TeamResponse>(
    "PUT",
    "/v1/teams/" + encodeURIComponent(name),
    body
  );
}

export function deleteTeam(
  name: string,
  expectedVersion: string
): Promise<TeamDeletedResponse> {
  return request<TeamDeletedResponse>(
    "DELETE",
    "/v1/teams/" + encodeURIComponent(name),
    { expected_version: expectedVersion }
  );
}

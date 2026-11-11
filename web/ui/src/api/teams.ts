import { request } from "./client";

/* Team calls of API-TEAM-007 through API-TEAM-010. */

/* Spec 47 API-RDS-010: the discussion settings of a team. */
export interface TeamDiscussionSettings {
  mode: "group" | "lead";
  discuss: "auto" | "ask";
  rounds: number;
  extra_rounds: "once" | "ask" | "never";
  room_memory: boolean;
  share_summary: boolean;
  learn: boolean;
  telegram_all: boolean;
}

export const TEAM_DISCUSSION_DEFAULTS: TeamDiscussionSettings = {
  mode: "group",
  discuss: "auto",
  rounds: 3,
  extra_rounds: "once",
  room_memory: true,
  share_summary: true,
  learn: true,
  telegram_all: true,
};

export interface TeamRecord extends Partial<TeamDiscussionSettings> {
  name: string;
  /* Spec 47: the room name people see; "" shows the team name. */
  display_name?: string;
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

export interface CreateTeamRequest extends Partial<TeamDiscussionSettings> {
  name: string;
  display_name?: string;
  lead: string;
  members: string[];
  description?: string;
  expected_version: string;
}

export interface SaveTeamRequest extends Partial<TeamDiscussionSettings> {
  display_name?: string;
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
  /* API-TEAM-010 takes expected_version as a query parameter. */
  return request<TeamDeletedResponse>(
    "DELETE",
    "/v1/teams/" + encodeURIComponent(name) + "?expected_version=" + encodeURIComponent(expectedVersion)
  );
}

/* The config version a team create or save must name (expected_version). */
export async function configVersion(): Promise<string> {
  const config = await request<{ version?: string }>("GET", "/v1/config");
  return config && config.version ? String(config.version) : "";
}

import { request } from "./client";

/* Secret calls of API-WEB-015. Values are sent on write and never returned. */

export interface SecretRow {
  name: string;
  state: string;
  source: string;
  file: string;
  overridden_by_env: boolean;
  updated_at: number | null;
  used_by: string[];
}

export interface ListSecretsResponse {
  version: string;
  secrets: SecretRow[];
  restart_required: boolean;
}

export interface SecretWriteResponse {
  name: string;
  state: string;
  restart_required: boolean;
}

export function listSecrets(): Promise<ListSecretsResponse> {
  return request<ListSecretsResponse>("GET", "/v1/secrets");
}

export function setSecret(
  name: string,
  value: string,
  expectedVersion: string
): Promise<SecretWriteResponse> {
  return request<SecretWriteResponse>(
    "PUT",
    "/v1/secrets/" + encodeURIComponent(name),
    { value, expected_version: expectedVersion }
  );
}

export function deleteSecret(
  name: string,
  expectedVersion: string
): Promise<SecretWriteResponse> {
  return request<SecretWriteResponse>(
    "DELETE",
    "/v1/secrets/" + encodeURIComponent(name),
    { expected_version: expectedVersion }
  );
}

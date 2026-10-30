import { queryString, request } from "./client";

/* Audit calls of API-WEB-030. */

export interface AuditEntry {
  time: number;
  kind: string;
  target: string;
  changed_keys: string[];
}

export interface AuditPage {
  entries: AuditEntry[];
  next_before: number | null;
}

export function listAudit(
  before?: number,
  limit?: number
): Promise<AuditPage> {
  return request<AuditPage>(
    "GET",
    "/v1/audit" + queryString({ before, limit: limit || 50 })
  );
}

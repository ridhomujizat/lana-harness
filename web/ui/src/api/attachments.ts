import { request } from "./client";

/* Attachment calls of API-ATT-001 and API-ATT-003 (REQ-ATT-006). */

export interface UploadedAttachment {
  path: string;
  name: string;
  media_type: string;
  size: number;
}

/* REQ-ATT-001: 20 MB per file and 10 files per message. */
export const MAX_ATTACHMENTS = 10;
export const MAX_ATTACHMENT_BYTES = 20 * 1024 * 1024;

interface UploadAttachmentsResponse {
  attachments: UploadedAttachment[];
}

export function uploadAttachments(
  id: string,
  files: File[]
): Promise<UploadedAttachment[]> {
  const form = new FormData();
  for (const file of files) {
    form.append("files", file, file.name);
  }
  return request<UploadAttachmentsResponse>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(id) + "/attachments",
    form
  ).then((response) => (response && response.attachments) || []);
}

/* API-ATT-003: the stored path already starts with attachments/, which the
   route adds back, so it is stripped and every segment is encoded. */
export function attachmentURL(id: string, path: string): string {
  const prefix = "attachments/";
  const rest = path.startsWith(prefix) ? path.slice(prefix.length) : path;
  const encoded = rest
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");
  return "/v1/sessions/" + encodeURIComponent(id) + "/attachments/" + encoded;
}

export function attachmentName(path: string): string {
  const parts = String(path || "").split("/");
  return parts[parts.length - 1] || path;
}

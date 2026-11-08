import { request } from "./client";

/* Spec #42, API-NTF-003: the dashboard reports which session a tab shows, so
   the macOS menu bar app does not notify a reply the owner is reading. */

export function reportPresence(sessionId: string, visible: boolean): Promise<void> {
  return request<void>(
    "POST",
    "/v1/sessions/" + encodeURIComponent(sessionId) + "/presence",
    { visible }
  );
}

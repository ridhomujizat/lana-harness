import { useCallback } from "react";
import type { RoomRow } from "../api/rooms";
import { useRooms } from "./presence";

/* An agent has an ID name (config key, URL, home folder, @mention) and an
   optional display name such as "Omnix Lead". The UI shows the display name
   and falls back to the ID name. */

/* MAX_DISPLAY_NAME mirrors the config rule of agents.<name>.display_name. */
export const MAX_DISPLAY_NAME = 40;

/* AGENT_ID_RULE mirrors agenthome.ValidName. */
export const AGENT_ID_RULE = /^[a-z][a-z0-9_-]{0,31}$/;

export const AGENT_ID_HINT =
  "Start with a letter. Use lowercase letters, digits, dashes, or underscores, at most 32 characters.";

/* displayLabel picks the display name of one record, else its ID name. */
export function displayLabel(
  record: { name?: string; display_name?: string } | null | undefined
): string {
  if (!record) {
    return "";
  }
  const display = (record.display_name || "").trim();
  return display || record.name || "";
}

/* agentLabel resolves an ID name through the room rows. */
export function agentLabel(
  name: string | null | undefined,
  byName: Record<string, RoomRow>
): string {
  if (!name) {
    return "";
  }
  const row = byName[name];
  const display = row && row.display_name ? row.display_name.trim() : "";
  return display || name;
}

/* useAgentLabel returns a resolver that maps an ID name to its display name.
   It reads the shared rooms store, so it costs no extra request. */
export function useAgentLabel(): (name: string | null | undefined) => string {
  const { byName } = useRooms();
  return useCallback(
    (name: string | null | undefined) => agentLabel(name, byName),
    [byName]
  );
}

/* slugifyAgentName turns "Omnix Lead" into "omnix-lead". The result matches
   AGENT_ID_RULE, or is "" when the input has no usable characters. */
export function slugifyAgentName(value: string): string {
  let slug = value
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  slug = slug.replace(/^[^a-z]+/, "");
  slug = slug.slice(0, 32).replace(/-+$/, "");
  return slug;
}

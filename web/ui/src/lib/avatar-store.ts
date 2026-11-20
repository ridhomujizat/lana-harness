import { useSyncExternalStore } from "react";
import { isAvatarDoc } from "./pixel-avatar";
import type { AvatarDoc } from "./pixel-avatar";

/* The shared agent avatar store (spec 43, pixel avatars since spec 62). Most
   avatar call sites only know an agent's name, so every response that carries
   `avatar` (GET /v1/agents, GET /v1/rooms and its stream, the agent overview)
   feeds this map, and the avatar dialog updates it after a save or a reset.
   AgentAvatar reads it, so a new avatar shows everywhere without a reload. */

export interface StoredAvatar {
  avatar: AvatarDoc;
  /* The agent color: a token or "#rrggbb"; "" when the row has none. */
  color: string;
  /* "saved" for a chosen avatar, "default" for the one seeded from the name;
     "" from an older server. */
  source: string;
}

export type AvatarMap = Readonly<Record<string, StoredAvatar>>;

let avatars: AvatarMap = {};
let signatures: Record<string, string> = {};
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) {
    listener();
  }
}

export interface AvatarRowLike {
  name?: string | null;
  color?: string | null;
  avatar?: unknown;
  avatar_source?: string | null;
}

/* rememberAvatars records the rows that carry a valid avatar document. Rows
   without one (an older server) leave the map alone. */
export function rememberAvatars(
  rows: ReadonlyArray<AvatarRowLike | null | undefined> | null | undefined
): void {
  if (!rows) {
    return;
  }
  let next: Record<string, StoredAvatar> | null = null;
  for (const row of rows) {
    if (!row || !row.name || !isAvatarDoc(row.avatar)) {
      continue;
    }
    const name = String(row.name);
    const value: StoredAvatar = {
      avatar: row.avatar,
      color: typeof row.color === "string" ? row.color : "",
      source: typeof row.avatar_source === "string" ? row.avatar_source : "",
    };
    const signature = JSON.stringify([value.avatar, value.color, value.source]);
    if (signatures[name] === signature) {
      continue;
    }
    if (!next) {
      next = { ...avatars };
    }
    next[name] = value;
    signatures = { ...signatures, [name]: signature };
  }
  if (next) {
    avatars = next;
    emit();
  }
}

export function rememberAvatar(row: AvatarRowLike | null | undefined): void {
  rememberAvatars(row ? [row] : []);
}

/* setAgentAvatar records an avatar after a save or a reset. */
export function setAgentAvatar(
  name: string,
  avatar: AvatarDoc,
  color: string,
  source = "saved"
): void {
  rememberAvatars([{ name, avatar, color, avatar_source: source }]);
}

export function getAgentAvatar(name: string | null | undefined): StoredAvatar | undefined {
  return name ? avatars[name] : undefined;
}

/* Tests start from an empty map. */
export function resetAvatarStore(): void {
  avatars = {};
  signatures = {};
  emit();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function snapshot(): AvatarMap {
  return avatars;
}

export function useAgentAvatars(): AvatarMap {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

export function useAgentAvatar(name: string | null | undefined): StoredAvatar | undefined {
  const map = useAgentAvatars();
  return name ? map[name] : undefined;
}

import { useSyncExternalStore } from "react";

/* The shared agent avatar store (spec 43). Most avatar call sites only know an
   agent's name, so every response that carries avatar_svg (GET /v1/agents,
   GET /v1/rooms and its stream, the agent overview) feeds this map, and the
   avatar dialog updates it after a save or a reset. AgentAvatar reads it, so
   a new avatar shows everywhere without a reload. */

export interface StoredAvatar {
  svg: string;
  color: string;
  /* "saved" for a chosen avatar, "default" for the one seeded from the name;
     "" from an older server. */
  source: string;
}

export type AvatarMap = Readonly<Record<string, StoredAvatar>>;

let avatars: AvatarMap = {};
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) {
    listener();
  }
}

export interface AvatarRowLike {
  name?: string | null;
  color?: string | null;
  avatar_svg?: string | null;
  avatar_source?: string | null;
}

/* rememberAvatars records the rows that carry avatar_svg. Rows from an older
   server without the field leave the map alone. */
export function rememberAvatars(
  rows: ReadonlyArray<AvatarRowLike | null | undefined> | null | undefined
): void {
  if (!rows) {
    return;
  }
  let next: Record<string, StoredAvatar> | null = null;
  for (const row of rows) {
    if (!row || !row.name || typeof row.avatar_svg !== "string") {
      continue;
    }
    const name = String(row.name);
    const value: StoredAvatar = {
      svg: row.avatar_svg,
      color: typeof row.color === "string" ? row.color : "",
      source: typeof row.avatar_source === "string" ? row.avatar_source : "",
    };
    const current = (next || avatars)[name];
    if (
      current &&
      current.svg === value.svg &&
      current.color === value.color &&
      current.source === value.source
    ) {
      continue;
    }
    if (!next) {
      next = { ...avatars };
    }
    next[name] = value;
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
  svg: string,
  color: string,
  source = "saved"
): void {
  rememberAvatars([{ name, avatar_svg: svg, color, avatar_source: source }]);
}

export function getAgentAvatar(name: string | null | undefined): StoredAvatar | undefined {
  return name ? avatars[name] : undefined;
}

/* Tests start from an empty map. */
export function resetAvatarStore(): void {
  avatars = {};
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

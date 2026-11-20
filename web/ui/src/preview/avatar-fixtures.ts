import type {
  AvatarCatalog,
  DeleteAvatarResponse,
  GenerateAvatarResponse,
  PutAvatarResponse,
} from "../api/agents";
import { isAvatarDoc } from "../lib/pixel-avatar";
import type { AvatarDoc, AvatarParts } from "../lib/pixel-avatar";
import samples from "./pixel-avatars.json";

/* Spec 62 preview fixtures: real pixel avatars dumped from the Go renderer
   (internal/avatar DefaultFor, RandomParts) and the avatar routes, so the
   preview shows pixel characters and the dialog works without a daemon.
   Regenerate pixel-avatars.json when the renderer changes. */

interface Samples {
  catalog: AvatarCatalog;
  avatars: Record<string, AvatarDoc>;
  random: AvatarDoc[];
}

const DATA = samples as unknown as Samples;

export const AVATAR_CATALOG: AvatarCatalog = DATA.catalog;

const RANDOM: AvatarDoc[] = DATA.random.filter(isAvatarDoc);

function fallback(index: number): AvatarDoc {
  return RANDOM[Math.abs(index) % RANDOM.length] as AvatarDoc;
}

/* The default avatar of a fixture agent, or a stable pick for other names. */
export function previewAvatar(name: string): AvatarDoc {
  const known = DATA.avatars[name];
  if (known && isAvatarDoc(known)) {
    return known;
  }
  let hash = 0;
  for (const char of name) {
    hash = (hash * 31 + char.charCodeAt(0)) | 0;
  }
  return fallback(hash);
}

let generated = 0;

/* POST/PUT/DELETE /v1/agents/{name}/avatar[...]. */
export function mockAvatar(
  method: string,
  name: string,
  action: string,
  body: unknown
): { status: number; body: unknown } {
  const request = (body && typeof body === "object" ? body : {}) as {
    mode?: string;
    model?: string;
    seed?: number;
    parts?: AvatarParts;
    avatar?: AvatarDoc;
    color?: string;
    prompt?: string;
  };
  if (action === "generate" && method === "POST") {
    generated += 1;
    const seed = typeof request.seed === "number" ? request.seed : generated;
    let avatar = fallback(seed + generated);
    if (request.mode === "render" && request.parts) {
      /* The preview cannot render parts; it shows another sample with the
         picked parts so the pickers follow. */
      avatar = { ...fallback(generated), parts: { ...request.parts } };
    }
    if (request.mode === "ai_draw") {
      avatar = { kind: "drawn", sprite: avatar.sprite, prompt: request.prompt || "" };
    }
    const ai = request.mode === "ai_parts" || request.mode === "ai_draw";
    const reply: GenerateAvatarResponse = {
      avatar,
      seed,
      model: ai ? request.model || "openai/gpt-5-mini" : "",
      source: ai ? "model" : "local",
    };
    return { status: 200, body: reply };
  }
  if (method === "PUT") {
    const reply: PutAvatarResponse = {
      name,
      avatar: request.avatar && isAvatarDoc(request.avatar) ? request.avatar : previewAvatar(name),
      avatar_source: "saved",
      color: request.color || "brand",
      restart_required: false,
    };
    return { status: 200, body: reply };
  }
  if (method === "DELETE") {
    const reply: DeleteAvatarResponse = {
      name,
      avatar: previewAvatar(name),
      avatar_source: "default",
      color: "brand",
    };
    return { status: 200, body: reply };
  }
  return { status: 404, body: { error: "not_found" } };
}

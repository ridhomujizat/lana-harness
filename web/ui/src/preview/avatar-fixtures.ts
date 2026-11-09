import type {
  DeleteAvatarResponse,
  GenerateAvatarResponse,
  PutAvatarResponse,
} from "../api/agents";

/* Spec 43 preview fixtures: canonical blob avatars (a solid body in
   currentColor with two white eye holes) and the avatar routes, so the
   preview shows generated avatars and the dialog works without a daemon. */

const OPEN = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">';
const CLOSE = "</svg>";

function pill(x: number, y: number, w: number, h: number, angle: number): string {
  const cx = x + w / 2;
  const cy = y + h / 2;
  return (
    '<rect x="' + x + '" y="' + y + '" width="' + w + '" height="' + h +
    '" rx="' + w / 2 + '" fill="#ffffff" transform="rotate(' + angle + " " + cx + " " + cy + ')"/>'
  );
}

function eye(cx: number, cy: number, rx: number, ry: number): string {
  return '<ellipse cx="' + cx + '" cy="' + cy + '" rx="' + rx + '" ry="' + ry + '" fill="#ffffff"/>';
}

export const AVATAR_CIRCLE =
  OPEN +
  '<path d="M50 12 C 72 12 88 28 88 50 C 88 72 72 88 50 88 C 28 88 12 72 12 50 C 12 28 28 12 50 12 Z" fill="currentColor"/>' +
  pill(54, 30, 7, 18, -28) +
  pill(68, 28, 7, 18, -28) +
  CLOSE;

export const AVATAR_SQUIRCLE =
  OPEN +
  '<rect x="15" y="15" width="70" height="70" rx="26" fill="currentColor" transform="rotate(-8 50 50)"/>' +
  pill(37, 40, 7, 17, 6) +
  pill(56, 42, 7, 17, 6) +
  CLOSE;

export const AVATAR_TRIANGLE =
  OPEN +
  '<path d="M50 13 C 56 13 60 17 64 24 L 87 66 C 93 78 85 88 70 88 L 30 88 C 15 88 7 78 13 66 L 36 24 C 40 17 44 13 50 13 Z" fill="currentColor"/>' +
  eye(43, 58, 3.5, 5.5) +
  eye(58, 58, 3.5, 5.5) +
  CLOSE;

export const AVATAR_CLOUD =
  OPEN +
  '<circle cx="36" cy="58" r="22" fill="currentColor"/>' +
  '<circle cx="58" cy="40" r="22" fill="currentColor"/>' +
  '<circle cx="66" cy="64" r="20" fill="currentColor"/>' +
  '<ellipse cx="50" cy="70" rx="26" ry="16" fill="currentColor"/>' +
  pill(41, 46, 7, 17, 8) +
  pill(57, 47, 7, 17, 8) +
  CLOSE;

export const AVATAR_TEARDROP =
  OPEN +
  '<path d="M44 14 C 48 12 52 14 56 18 L 76 40 C 86 51 86 68 76 78 C 66 88 49 90 37 82 C 24 73 20 58 26 46 L 40 20 C 41 17 42 15 44 14 Z" fill="currentColor"/>' +
  eye(46, 62, 4.5, 6.5) +
  eye(60, 58, 3.5, 3.5) +
  CLOSE;

export const AVATAR_ELLIPSE =
  OPEN +
  '<ellipse cx="50" cy="54" rx="40" ry="32" fill="currentColor"/>' +
  pill(34, 38, 7, 18, 24) +
  pill(52, 37, 7, 18, -10) +
  CLOSE;

const SHAPES = [
  AVATAR_SQUIRCLE,
  AVATAR_TRIANGLE,
  AVATAR_CLOUD,
  AVATAR_TEARDROP,
  AVATAR_ELLIPSE,
  AVATAR_CIRCLE,
];

let generated = 0;

/* POST/PUT/DELETE /v1/agents/{name}/avatar[...]. */
export function mockAvatar(
  method: string,
  name: string,
  action: string,
  body: unknown
): { status: number; body: unknown } {
  const request = (body && typeof body === "object" ? body : {}) as {
    model?: string;
    seed?: number;
    svg?: string;
    color?: string;
  };
  if (action === "generate" && method === "POST") {
    generated += 1;
    const seed = typeof request.seed === "number" ? request.seed : generated;
    const reply: GenerateAvatarResponse = {
      svg: SHAPES[Math.abs(seed + generated) % SHAPES.length] || AVATAR_CIRCLE,
      model: request.model ? request.model : generated % 3 === 0 ? "" : "openai/gpt-5-mini",
      seed,
      source: request.model || generated % 3 !== 0 ? "model" : "local",
    };
    return { status: 200, body: reply };
  }
  if (method === "PUT") {
    const reply: PutAvatarResponse = {
      name,
      avatar_svg: request.svg || "",
      avatar_source: "saved",
      color: request.color || "brand",
      restart_required: false,
    };
    return { status: 200, body: reply };
  }
  if (method === "DELETE") {
    const reply: DeleteAvatarResponse = {
      name,
      avatar_svg: SHAPES[name.length % SHAPES.length] || AVATAR_CIRCLE,
      avatar_source: "default",
      color: "brand",
    };
    return { status: 200, body: reply };
  }
  return { status: 404, body: { error: "not_found" } };
}

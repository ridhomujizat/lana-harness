/* Agent identity helpers (REQ-WEB-041). The color follows the name, so one
   agent keeps one identity in the picker, the chat, and the agents pages.
   The palette classes (tone-brand, tone-success, tone-warning, tone-error,
   tone-gray) live in app.css. */

const TONES = ["brand", "success", "warning", "error", "gray"];

export interface Avatar {
  initial: string;
  colorClass: string;
}

export function avatarFor(name: string | null | undefined): Avatar {
  const text = String(name || "").trim();
  let hash = 0;
  for (let index = 0; index < text.length; index += 1) {
    hash = (hash * 31 + text.charCodeAt(index)) >>> 0;
  }
  const tone = TONES[hash % TONES.length] || "brand";
  return {
    initial: text ? (Array.from(text)[0] || "").toUpperCase() : "?",
    colorClass: "tone-" + tone,
  };
}

/* Avatar motion (prototype design/prototypes/avatar-motion.html). A generated
   avatar is rigged by lib/avatar-svg: g.av-squash > body + g.av-look >
   g.av-gaze > g.av-eye. The mood of an avatar host is its data-mood attribute;
   styles/pages-gr8r/avatar-motion.css draws each mood. This module runs the
   parts CSS cannot: the eyes follow the pointer, random blinks, the squish on
   hover and the boop on click. One set of document listeners serves every
   avatar on the page. */

export type AvatarMood =
  | "rest"
  | "idle"
  | "thinking"
  | "tool"
  | "talk"
  | "ask"
  | "error"
  | "done"
  | "sleep";

/* The room presence of API-ROOM-001 as a mood. "working" names no detail, so
   it reads as thinking; the face's working ring stays as it was. */
export function moodFromPresence(state: string | null | undefined): AvatarMood {
  switch (state) {
    case "working":
      return "thinking";
    case "waiting":
      return "ask";
    case "problem":
      return "error";
    case "online":
      return "idle";
    default:
      return "rest";
  }
}

/* The moods that hold the eyes closed or squinted: no blinking there. */
const NO_BLINK = new Set(["sleep", "done", "error"]);

const HOSTS = ".av, .agent-avatar, .agent-face";

/* The largest eye travel, in avatar units (the viewBox is 100 wide). */
const GAZE_UNITS = 6;

function reducedMotion(): boolean {
  const root = document.documentElement;
  const setting = root.getAttribute("data-motion");
  if (setting === "reduce") {
    return true;
  }
  if (setting === "full") {
    return false;
  }
  return typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function moodOf(svg: Element): string {
  const host = svg.parentElement;
  return (host && host.getAttribute("data-mood")) || "rest";
}

/* Restarts a one-shot animation class on an avatar's svg. */
function play(svg: Element, cls: string, ms: number): void {
  svg.classList.remove(cls);
  void (svg as HTMLElement).getBoundingClientRect();
  svg.classList.add(cls);
  window.setTimeout(() => svg.classList.remove(cls), ms);
}

function faceOf(target: EventTarget | null): Element | null {
  if (!(target instanceof Element)) {
    return null;
  }
  const host = target.closest(HOSTS);
  if (!host) {
    return null;
  }
  const svg = host.querySelector(":scope > .av-svg.has-eyes");
  return svg;
}

let started = false;
let pointerX = -1e5;
let pointerY = -1e5;
let frame = 0;

/* Eyes look toward the pointer when it comes near: reads first, then writes,
   so the frame lays out once. */
function track(): void {
  frame = 0;
  const looks = Array.from(document.querySelectorAll<SVGGElement>(".av-svg.has-eyes .av-look"));
  if (looks.length === 0) {
    return;
  }
  const still = reducedMotion();
  const moves: Array<[SVGGElement, number, number]> = [];
  for (const look of looks) {
    const svg = look.ownerSVGElement;
    if (!svg) {
      continue;
    }
    if (still || moodOf(svg) === "sleep") {
      moves.push([look, 0, 0]);
      continue;
    }
    const box = svg.getBoundingClientRect();
    if (box.width === 0) {
      continue;
    }
    const dx = pointerX - (box.left + box.width / 2);
    const dy = pointerY - (box.top + box.height / 2);
    const distance = Math.hypot(dx, dy);
    const reach = Math.max(box.width * 5, 260);
    if (distance > reach) {
      moves.push([look, 0, 0]);
      continue;
    }
    const pull = Math.min(distance / Math.max(box.width * 1.2, 40), 1) * GAZE_UNITS;
    moves.push([look, (dx / (distance || 1)) * pull, (dy / (distance || 1)) * pull]);
  }
  for (const [look, x, y] of moves) {
    const next = x === 0 && y === 0 ? "" : "translate(" + x.toFixed(2) + "px, " + y.toFixed(2) + "px)";
    if (look.style.transform !== next) {
      look.style.transform = next;
    }
  }
}

function schedule(): void {
  if (!frame) {
    frame = window.requestAnimationFrame(track);
  }
}

/* The moods where the face is free to glance around on its own. */
const GLANCE = new Set(["rest", "idle"]);

/* A resting face looks somewhere else for a moment, then back, unless the
   pointer already holds its gaze. */
function glance(svg: Element): void {
  const gaze = svg.querySelector<SVGGElement>(".av-gaze");
  const look = svg.querySelector<SVGGElement>(".av-look");
  if (!gaze || !look || look.style.transform || reducedMotion()) {
    return;
  }
  const x = (Math.random() < 0.5 ? -1 : 1) * (3 + Math.random() * 3);
  const y = (Math.random() - 0.6) * 5;
  gaze.style.transform = "translate(" + x.toFixed(1) + "px, " + y.toFixed(1) + "px)";
  window.setTimeout(() => {
    gaze.style.transform = "";
  }, 900 + Math.random() * 700);
}

/* Every 250 ms each visible face blinks with a 1-in-16 chance, about once in
   four seconds, sometimes twice in a row; a resting face glances around
   about once in ten seconds. */
function blinkTick(): void {
  if (document.hidden) {
    return;
  }
  for (const svg of Array.from(document.querySelectorAll(".av-svg.has-eyes"))) {
    const mood = moodOf(svg);
    if (GLANCE.has(mood) && Math.random() < 1 / 40) {
      glance(svg);
    }
    if (Math.random() >= 1 / 16 || NO_BLINK.has(mood)) {
      continue;
    }
    play(svg, "av-blink", 200);
    if (Math.random() < 0.18) {
      window.setTimeout(() => play(svg, "av-blink", 200), 260);
    }
  }
}

/* startAvatarMotion installs the shared listeners once. */
export function startAvatarMotion(): void {
  if (started || typeof document === "undefined") {
    return;
  }
  started = true;
  document.addEventListener(
    "pointermove",
    (event) => {
      pointerX = event.clientX;
      pointerY = event.clientY;
      schedule();
    },
    { passive: true }
  );
  document.addEventListener("scroll", schedule, { passive: true, capture: true });
  document.documentElement.addEventListener("pointerleave", () => {
    pointerX = -1e5;
    pointerY = -1e5;
    schedule();
  });
  document.addEventListener("pointerover", (event) => {
    const svg = faceOf(event.target);
    if (!svg || reducedMotion()) {
      return;
    }
    const related = event.relatedTarget;
    if (related instanceof Node && svg.parentElement?.contains(related)) {
      return;
    }
    play(svg, "av-poke", 600);
  });
  document.addEventListener("click", (event) => {
    const svg = faceOf(event.target);
    if (svg && !reducedMotion()) {
      play(svg, "av-boop", 650);
    }
  });
  window.setInterval(blinkTick, 250);
}

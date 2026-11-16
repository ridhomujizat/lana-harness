/* Spec 55: a chime when an agent finishes or needs the owner. Synthesized
   with Web Audio (no asset), loud enough to hear across the room. Browsers
   start audio only after a user gesture, so the context is unlocked on the
   first pointer or key press. */

const SOUND_KEY = "lana.workbench.sound";

let ctx: AudioContext | null = null;

function context(): AudioContext | null {
  if (typeof window === "undefined") return null;
  const Ctor = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctor) return null;
  if (!ctx) ctx = new Ctor();
  return ctx;
}

export function unlockAudio(): void {
  const c = context();
  if (c && c.state === "suspended") void c.resume();
}

export function soundEnabled(): boolean {
  try {
    return window.localStorage.getItem(SOUND_KEY) !== "off";
  } catch {
    return true;
  }
}

export function setSoundEnabled(on: boolean): void {
  try {
    window.localStorage.setItem(SOUND_KEY, on ? "on" : "off");
  } catch {
    /* Storage is optional. */
  }
}

function tone(c: AudioContext, freq: number, start: number, length: number, gain: number): void {
  const osc = c.createOscillator();
  const amp = c.createGain();
  osc.type = "sine";
  osc.frequency.setValueAtTime(freq, start);
  amp.gain.setValueAtTime(0.0001, start);
  amp.gain.exponentialRampToValueAtTime(gain, start + 0.015);
  amp.gain.exponentialRampToValueAtTime(0.0001, start + length);
  osc.connect(amp).connect(c.destination);
  osc.start(start);
  osc.stop(start + length + 0.02);
}

/* "done": a rising two-note chime. "needs": three quick notes, so a waiting
   agent sounds different from a finished one. */
export function chime(kind: "done" | "needs"): void {
  if (!soundEnabled()) return;
  const c = context();
  if (!c || c.state !== "running") return;
  const t = c.currentTime + 0.01;
  if (kind === "done") {
    tone(c, 784, t, 0.35, 0.5);
    tone(c, 1175, t + 0.16, 0.5, 0.5);
  } else {
    tone(c, 988, t, 0.18, 0.55);
    tone(c, 988, t + 0.2, 0.18, 0.55);
    tone(c, 1319, t + 0.4, 0.4, 0.55);
  }
}

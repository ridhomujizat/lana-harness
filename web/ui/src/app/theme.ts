import { useSyncExternalStore } from "react";
/* Theme state for the dashboard shell. The choice is stored per browser. */

export const THEME_KEY = "lana.theme";
export const THEME_CHOICES = ["light", "dark", "system"] as const;

export type ThemeChoice = (typeof THEME_CHOICES)[number];

const DARK_QUERY = "(prefers-color-scheme: dark)";

export function isThemeChoice(value: unknown): value is ThemeChoice {
  return (
    typeof value === "string" &&
    (THEME_CHOICES as readonly string[]).includes(value)
  );
}

export function themeLabel(choice: ThemeChoice): string {
  switch (choice) {
    case "light":
      return "Light";
    case "dark":
      return "Dark";
    default:
      return "System";
  }
}

export function readTheme(): ThemeChoice {
  try {
    const stored = window.localStorage.getItem(THEME_KEY);
    if (isThemeChoice(stored)) {
      return stored;
    }
  } catch {
    /* Storage may be disabled. Fall through to the system choice. */
  }
  return "system";
}

export function saveTheme(choice: ThemeChoice): void {
  try {
    window.localStorage.setItem(THEME_KEY, choice);
  } catch {
    /* The theme still applies for this page even when storage is disabled. */
  }
}

export function systemPrefersDark(): boolean {
  if (typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia(DARK_QUERY).matches;
}

export function resolveTheme(choice: ThemeChoice): "light" | "dark" {
  if (choice === "light" || choice === "dark") {
    return choice;
  }
  return systemPrefersDark() ? "dark" : "light";
}

/* Sets the resolved theme on <html>. System resolves against the OS and is
   re-applied by the Root effect when the OS preference changes. */
export function applyTheme(choice: ThemeChoice): "light" | "dark" {
  const resolved = resolveTheme(choice);
  document.documentElement.setAttribute("data-theme", resolved);
  syncThemeColor(resolved);
  return resolved;
}

/* The topbar colour (--surface) of each theme. The installed phone app
   (spec 67) paints the status bar with <meta name="theme-color">. */
export const THEME_COLORS = { light: "#ffffff", dark: "#1d1d1c" } as const;

/* index.html carries one theme-color meta per OS scheme. A chosen theme
   overrides both, so the status bar follows the app, not the OS. */
function syncThemeColor(resolved: "light" | "dark"): void {
  const metas = document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]');
  metas.forEach((meta) => {
    meta.content = THEME_COLORS[resolved];
  });
}

export function watchSystemTheme(callback: () => void): () => void {
  if (typeof window.matchMedia !== "function") {
    return () => {};
  }
  const query = window.matchMedia(DARK_QUERY);
  const handler = () => callback();
  if (typeof query.addEventListener === "function") {
    query.addEventListener("change", handler);
    return () => query.removeEventListener("change", handler);
  }
  if (typeof query.addListener === "function") {
    query.addListener(handler);
    return () => query.removeListener(handler);
  }
  return () => {};
}

/* The theme on <html> right now ("light" or "dark"), for a component that
   must follow it, such as the Workbench preview iframe (owner, 2026-11-22).
   It re-renders when the attribute changes. */
function subscribePageTheme(callback: () => void): () => void {
  if (typeof MutationObserver === "undefined") return () => {};
  const observer = new MutationObserver(callback);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => observer.disconnect();
}

function pageTheme(): "light" | "dark" {
  const attr = document.documentElement.getAttribute("data-theme");
  if (attr === "light" || attr === "dark") return attr;
  return systemPrefersDark() ? "dark" : "light";
}

export function usePageTheme(): "light" | "dark" {
  return useSyncExternalStore(subscribePageTheme, pageTheme, () => "light");
}

/* A preview page URL with the theme it should draw in (the daemon reads
   ?theme=light|dark); a #fragment stays at the end. */
export function themedUrl(url: string, theme: "light" | "dark"): string {
  const hash = url.indexOf("#");
  const base = hash < 0 ? url : url.slice(0, hash);
  const frag = hash < 0 ? "" : url.slice(hash);
  return base + (base.includes("?") ? "&" : "?") + "theme=" + theme + frag;
}

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
  return resolved;
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

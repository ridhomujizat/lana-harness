/* Theme state for the dashboard shell. The choice is stored per browser. */

export const THEME_KEY = "lana.theme";
export const THEME_CHOICES = ["light", "dark", "system"];

const DARK_QUERY = "(prefers-color-scheme: dark)";

export function themeLabel(choice) {
  switch (choice) {
    case "light":
      return "Light";
    case "dark":
      return "Dark";
    default:
      return "System";
  }
}

export function readTheme() {
  try {
    const stored = window.localStorage.getItem(THEME_KEY);
    if (THEME_CHOICES.includes(stored)) {
      return stored;
    }
  } catch (cause) {
    /* Storage may be disabled. Fall through to the system choice. */
  }
  return "system";
}

export function saveTheme(choice) {
  try {
    window.localStorage.setItem(THEME_KEY, choice);
  } catch (cause) {
    /* The theme still applies for this page even when storage is disabled. */
  }
}

export function systemPrefersDark() {
  if (typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia(DARK_QUERY).matches;
}

export function resolveTheme(choice) {
  if (choice === "light" || choice === "dark") {
    return choice;
  }
  return systemPrefersDark() ? "dark" : "light";
}

/* Sets the resolved theme on <html>. System resolves against the OS and is
   re-applied by the Root effect when the OS preference changes. */
export function applyTheme(choice) {
  const resolved = resolveTheme(choice);
  document.documentElement.setAttribute("data-theme", resolved);
  return resolved;
}

export function watchSystemTheme(callback) {
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

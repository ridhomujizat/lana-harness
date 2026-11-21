import { describe, expect, it, vi } from "vitest";
import {
  THEME_CHOICES,
  THEME_KEY,
  applyTheme,
  readTheme,
  resolveTheme,
  saveTheme,
  systemPrefersDark,
  watchSystemTheme,
} from "./theme";
import { SIDEBAR_KEY, readCollapsed, saveCollapsed } from "./Sidebar";

function mockMatchMedia(matches: boolean) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string): MediaQueryList =>
      ({
        matches,
        media: query,
        onchange: null,
        addEventListener: () => {},
        removeEventListener: () => {},
        addListener: () => {},
        removeListener: () => {},
        dispatchEvent: () => false,
      }) as MediaQueryList,
  });
}

describe("theme persistence", () => {
  it("uses the lana.theme storage key", () => {
    expect(THEME_KEY).toBe("lana.theme");
    expect(THEME_CHOICES).toEqual(["light", "dark", "system"]);
  });

  it("reads back a saved choice and defaults to system", () => {
    expect(readTheme()).toBe("system");
    saveTheme("dark");
    expect(window.localStorage.getItem(THEME_KEY)).toBe("dark");
    expect(readTheme()).toBe("dark");
  });

  it("applies the resolved theme to the html element", () => {
    mockMatchMedia(true);
    expect(systemPrefersDark()).toBe(true);
    expect(resolveTheme("system")).toBe("dark");
    expect(applyTheme("light")).toBe("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(applyTheme("system")).toBe("dark");
  });

  it("watches the prefers-color-scheme query", () => {
    mockMatchMedia(false);
    const callback = vi.fn();
    const stop = watchSystemTheme(callback);
    expect(typeof stop).toBe("function");
    stop();
  });
});

describe("sidebar persistence", () => {
  it("uses the lana.sidebar storage key", () => {
    expect(SIDEBAR_KEY).toBe("lana.sidebar");
  });

  it("stores collapsed and expanded the same way as today", () => {
    expect(readCollapsed()).toBe(false);
    saveCollapsed(true);
    expect(window.localStorage.getItem(SIDEBAR_KEY)).toBe("collapsed");
    expect(readCollapsed()).toBe(true);
    saveCollapsed(false);
    expect(window.localStorage.getItem(SIDEBAR_KEY)).toBe("expanded");
    expect(readCollapsed()).toBe(false);
  });
});

describe("themedUrl", () => {
  it("adds the theme before a fragment", async () => {
    const { themedUrl } = await import("./theme");
    expect(themedUrl("/v1/workbench/preview/t/a.md?view=1", "dark")).toBe("/v1/workbench/preview/t/a.md?view=1&theme=dark");
    expect(themedUrl("/v1/workbench/preview/t/a.go?view=1#L12", "light")).toBe("/v1/workbench/preview/t/a.go?view=1&theme=light#L12");
    expect(themedUrl("/raw/a.png", "light")).toBe("/raw/a.png?theme=light");
  });
});

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/* Owner bug 2026-11-24: a permission option with a long path ("Yes, and always
   allow access to /Users/…") ran past the card, because app.css gives every
   button white-space: nowrap. jsdom has no layout, so these checks pin the CSS
   that keeps terminal text inside the Workbench chat cards. */

const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");
const css = readFileSync(join(SRC, "styles/pages-gr8r/workbench-chat.css"), "utf8");
const appCss = readFileSync(join(SRC, "styles/app.css"), "utf8");

/* The declarations of the first rule whose selector list is exactly `selector`. */
function rule(source: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/,\s*/g, ",\\s*");
  const match = new RegExp("(?:^|\\n)" + escaped + "\\s*\\{([^}]*)\\}").exec(source);
  if (!match) {
    throw new Error("no CSS rule for " + selector);
  }
  return match[1] ?? "";
}

describe("Workbench chat cards keep long terminal text inside", () => {
  it("lets any long word in a card break", () => {
    const card = rule(css, ".wbc-card");
    expect(card).toMatch(/overflow-wrap:\s*anywhere/);
    expect(card).toMatch(/min-width:\s*0/);
    expect(card).toMatch(/max-width:\s*100%/);
  });

  it("lets option buttons wrap although buttons are nowrap by default", () => {
    expect(appCss).toMatch(/white-space:\s*nowrap/);
    expect(rule(css, ".wbc-option, .wbc-q-option")).toMatch(/white-space:\s*normal/);
  });

  it("lets the text children of flex rows shrink", () => {
    expect(rule(css, ".wbc-option > span, .wbc-q-body, .wbc-card-foot > span")).toMatch(/min-width:\s*0/);
    expect(rule(css, ".wbc-card-head > div")).toMatch(/min-width:\s*0/);
  });
});

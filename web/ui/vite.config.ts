import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { icons as lucide } from "lucide";

const root = dirname(fileURLToPath(import.meta.url));

/* A deterministic hash of the dashboard sources. It is written into the built
   index.html so `make web-check` can tell a stale build from a fresh one even
   when a source edit is erased by minification (for example a trailing
   newline). */
function sourceStamp(dir: string): string {
  const hash = createHash("sha256");
  const walk = (current: string) => {
    const entries = readdirSync(current, { withFileTypes: true }).sort((a, b) =>
      a.name.localeCompare(b.name)
    );
    for (const entry of entries) {
      const full = join(current, entry.name);
      if (entry.isDirectory()) {
        walk(full);
      } else {
        hash.update(relative(dir, full).replaceAll("\\", "/"));
        hash.update("\0");
        hash.update(readFileSync(full));
        hash.update("\0");
      }
    }
  };
  walk(join(dir, "src"));
  hash.update(readFileSync(join(dir, "index.html")));
  return hash.digest("hex");
}

function sourceStampPlugin(dir: string): Plugin {
  const stamp = sourceStamp(dir);
  return {
    name: "lana-source-stamp",
    transformIndexHtml(html) {
      return html.replace("<head>", `<head>\n    <!-- build: ${stamp} -->`);
    },
  };
}

/* `virtual:icons` exports only the Lucide icons the source actually names, as
   inline SVG markup, so the bundle carries the subset instead of the full set.
   Any quoted kebab-case token under src/ that matches a Lucide icon name is
   included. Ported from gr8r-studio's vite.config.js (lucideSubset). */
function lucideSubset(dir: string): Plugin {
  const id = "virtual:icons";
  const resolved = "\0" + id;
  const walk = (current: string): string[] =>
    readdirSync(current, { withFileTypes: true }).flatMap((entry) => {
      const full = join(current, entry.name);
      if (entry.isDirectory()) {
        return walk(full);
      }
      return /\.(ts|tsx)$/.test(entry.name) ? [full] : [];
    });
  const pascal = (name: string): string =>
    name
      .split("-")
      .map((part) => (part[0] ?? "").toUpperCase() + part.slice(1))
      .join("");
  const icons = lucide as unknown as Record<string, unknown[] | undefined>;
  return {
    name: "lucide-subset",
    resolveId(source) {
      return source === id ? resolved : null;
    },
    load(source) {
      if (source !== resolved) {
        return null;
      }
      const text = walk(dir)
        .map((file) => readFileSync(file, "utf8"))
        .join("\n");
      const names = [
        ...new Set(
          [...text.matchAll(/['"`]([a-z][a-z0-9]*(?:-[a-z0-9]+)*)['"`]/g)].map(
            (match) => match[1] ?? ""
          )
        ),
      ]
        .filter((name) => icons[pascal(name)])
        .sort();
      const out: Record<string, string> = {};
      for (const name of names) {
        let node = icons[pascal(name)];
        if (!node) {
          continue;
        }
        if (node[0] === "svg") {
          node = node[2] as unknown[];
        }
        out[name] = node
          .map((child) => {
            const [tag, attrs] = child as [
              string,
              Record<string, string | number | undefined>,
            ];
            const attributes = Object.entries(attrs)
              .filter(([key]) => key !== "key")
              .map(([key, value]) => `${key}="${value}"`)
              .join(" ");
            return `<${tag} ${attributes}/>`;
          })
          .join("");
      }
      return `export const ICONS = ${JSON.stringify(out)};`;
    },
  };
}

export default defineConfig({
  base: "/assets/",
  plugins: [react(), sourceStampPlugin(root), lucideSubset(join(root, "src"))],
  build: {
    outDir: "../dist",
    emptyOutDir: true,
    modulePreload: { polyfill: false },
  },
  server: {
    proxy: {
      "/v1": "http://127.0.0.1:18300",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
  },
});

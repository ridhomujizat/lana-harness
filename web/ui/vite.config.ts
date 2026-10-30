import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

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

export default defineConfig({
  base: "/assets/",
  plugins: [react(), sourceStampPlugin(root)],
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

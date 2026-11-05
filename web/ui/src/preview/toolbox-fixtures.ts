import { MANIFEST_TEMPLATE } from "../api/toolbox";
import type { PackageDetail } from "../api/toolbox";

/* Preview data for the Tools pages (spec #34). ?empty=tools shows the empty catalog. */
const NOW = Date.now();

const obscura: PackageDetail = {
  id: "obscura",
  name: "Obscura",
  description: "Headless browser for scraping and automation",
  homepage: "https://www.obscura.sh",
  status: "installed",
  version: "obscura 0.2.2",
  binary_path: "/Users/owner/.nix-profile/bin/obscura",
  error: "",
  installing: false,
  commands: [
    {
      name: "fetch",
      tool: "obscura_fetch",
      description: "Load a page in a real browser and return its content",
      risk: "read",
      input: [
        { name: "format", type: "string", description: "", required: false, default: "markdown", enum: ["markdown", "text", "html"] },
        { name: "url", type: "string", description: "Page URL", required: true },
      ],
    },
  ],
  install: [{ label: "Homebrew", kind: "command", detail: "brew install obscura" }],
  updated_at: NOW,
  manifest: MANIFEST_TEMPLATE,
  jobs: [
    { id: "j1", package: "obscura", kind: "install", method: 0, label: "Homebrew", status: "succeeded", started_at: NOW - 600000, ended_at: NOW - 540000, error: "" },
  ],
  used_by: { agents: ["default"], workflows: [{ id: "riset-harian", name: "Riset harian" }] },
};

const ytdlp: PackageDetail = {
  ...obscura,
  id: "yt-dlp",
  name: "yt-dlp",
  description: "Download video and audio, read subtitles",
  homepage: "https://github.com/yt-dlp/yt-dlp",
  status: "missing",
  version: "",
  binary_path: "",
  commands: [{ name: "subtitles", tool: "yt_dlp_subtitles", description: "Fetch the subtitles of a video", risk: "read", input: [{ name: "url", type: "string", description: "Video URL", required: true }] }],
  install: [{ label: "pipx", kind: "command", detail: "pipx install yt-dlp" }, { label: "Download (macOS)", kind: "download", detail: "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos" }],
  jobs: [],
  used_by: { agents: [], workflows: [] },
};

export function mockToolbox(method: string, parts: string[]): { status: number; body: unknown; stream?: string } {
  const at = (index: number) => parts[index] || "";
  const empty = new URLSearchParams(window.location.search).get("empty") === "tools";
  if (!at(2)) {
    if (method === "POST") return { status: 201, body: ytdlp };
    return { status: 200, body: { packages: empty ? [] : [obscura, ytdlp] } };
  }
  if (at(2) === "stream") return { status: 200, body: null, stream: ": ping\n\n" };
  const pkg = at(2) === "yt-dlp" ? ytdlp : obscura;
  if (at(3) === "jobs") return { status: 200, body: { text: "==> Fetching obscura\n==> Pouring obscura--0.2.2.arm64_sequoia.bottle.tar.gz\n🍺  obscura was successfully installed!\n", done: true } };
  if (at(3) === "run") return { status: 200, body: { output: "# Example Domain\n\nThis domain is for use in documentation examples.", exit_code: 0, duration_ms: 812 } };
  if (at(3) === "install" || at(3) === "uninstall") return { status: 202, body: pkg.jobs[0] || obscura.jobs[0] };
  return { status: method === "DELETE" ? 204 : 200, body: pkg };
}

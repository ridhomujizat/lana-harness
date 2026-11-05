import { request } from "./client";

/* Tools page calls of spec #34 (design "Web API"). The Go side calls the
   feature the toolbox; the sidebar calls it Tools. */

export type PackageState = "missing" | "installing" | "installed" | "too_old" | "broken" | "failed";

export interface InputFieldView {
  name: string;
  type: string;
  description: string;
  required: boolean;
  default?: unknown;
  enum?: string[] | null;
}

export interface CommandView {
  name: string;
  tool: string;
  description: string;
  risk: string;
  input: InputFieldView[];
}

export interface InstallView {
  label: string;
  kind: "command" | "download";
  detail: string;
  fits?: boolean;
  reason?: string;
}

export interface PackageView {
  id: string;
  name: string;
  description: string;
  homepage: string;
  status: PackageState;
  version: string;
  binary_path: string;
  error: string;
  installing: boolean;
  commands: CommandView[];
  install: InstallView[];
  updated_at: number;
  /* Revision 2 (REQ-TB-001/009/011). */
  source?: "catalog" | "custom";
  icon?: string;
  category?: string;
  mcp?: { args: string[] } | null;
  auto_method?: number;
}

export interface ToolJob {
  id: string;
  package: string;
  kind: "install" | "uninstall";
  method: number;
  label: string;
  status: "running" | "succeeded" | "failed" | "cancelled";
  started_at: number;
  ended_at: number;
  error: string;
}

export interface UsedBy {
  agents: string[];
  workflows: { id: string; name: string }[];
}

/* One agent and whether it may use this tool (its MCP server and commands). */
export interface AgentUse {
  name: string;
  enabled: boolean;
}

export interface PackageDetail extends PackageView {
  manifest: string;
  jobs: ToolJob[];
  used_by: UsedBy;
  agents?: AgentUse[];
}

export interface RunResult {
  output: string;
  exit_code: number;
  duration_ms: number;
}

const base = "/v1/toolbox";
const pkg = (id: string) => base + "/" + encodeURIComponent(id);

export function listPackages(): Promise<{ packages: PackageView[] }> {
  return request<{ packages: PackageView[] }>("GET", base);
}

export function getPackage(id: string): Promise<PackageDetail> {
  return request<PackageDetail>("GET", pkg(id));
}

export function addPackage(manifest: string): Promise<PackageView> {
  return request<PackageView>("POST", base, { manifest });
}

export function updatePackage(id: string, manifest: string): Promise<PackageView> {
  return request<PackageView>("PUT", pkg(id), { manifest });
}

export function deletePackage(id: string, uninstall = false): Promise<void> {
  return request<void>("DELETE", pkg(id) + (uninstall ? "?uninstall=1" : ""));
}

/* method -1 lets lana pick the first method that fits this machine. */
export function installPackage(id: string, method = -1): Promise<ToolJob> {
  return request<ToolJob>("POST", pkg(id) + "/install", { method });
}

export function uninstallPackage(id: string): Promise<ToolJob> {
  return request<ToolJob>("POST", pkg(id) + "/uninstall");
}

export function setAgentUse(id: string, agent: string, enabled: boolean): Promise<PackageDetail> {
  return request<PackageDetail>("POST", pkg(id) + "/agents", { agent, enabled });
}

export function checkPackage(id: string): Promise<PackageView> {
  return request<PackageView>("POST", pkg(id) + "/check");
}

export function jobLog(id: string, job: string): Promise<{ text: string; done: boolean }> {
  return request<{ text: string; done: boolean }>(
    "GET",
    pkg(id) + "/jobs/" + encodeURIComponent(job) + "/log"
  );
}

export function runCommand(id: string, command: string, input: Record<string, unknown>): Promise<RunResult> {
  return request<RunResult>("POST", pkg(id) + "/run", { command, input });
}

/* GET /v1/toolbox/stream: `changed` events name the package that changed. */
export function openToolboxStream(onChange: (id: string) => void): { close: () => void } {
  let closed = false;
  let controller: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const retry = () => {
    if (!closed) {
      timer = setTimeout(connect, 1500);
    }
  };
  async function connect(): Promise<void> {
    if (closed) {
      return;
    }
    controller = new AbortController();
    try {
      const response = await fetch(base + "/stream", {
        headers: { Accept: "text/event-stream" },
        credentials: "same-origin",
        signal: controller.signal,
      });
      if (!response.ok || !response.body) {
        retry();
        return;
      }
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (!closed) {
        const chunk = await reader.read();
        if (chunk.done) {
          break;
        }
        buffer += decoder.decode(chunk.value, { stream: true });
        let index = buffer.indexOf("\n\n");
        while (index >= 0) {
          const block = buffer.slice(0, index);
          buffer = buffer.slice(index + 2);
          index = buffer.indexOf("\n\n");
          if (!block.includes("event: changed")) {
            continue;
          }
          const data = block.split("\n").find((line) => line.startsWith("data:"));
          if (data) {
            try {
              const payload = JSON.parse(data.slice(5).trim()) as { package?: string };
              onChange(payload.package || "");
            } catch {
              /* skip a broken frame */
            }
          }
        }
      }
    } catch {
      /* reconnect below */
    }
    retry();
  }
  void connect();
  return {
    close() {
      closed = true;
      if (timer) {
        clearTimeout(timer);
      }
      controller?.abort();
    },
  };
}

/* A starting point for "Add tool" (no package ships with lana). */
export const MANIFEST_TEMPLATE = `id = "obscura"
name = "Obscura"
description = "Headless browser for scraping and automation"
homepage = "https://www.obscura.sh"

[detect]
binary = "obscura"
version_args = ["--version"]

[[install]]
label = "Homebrew"
command = "brew install obscura"

[[commands]]
name = "fetch"
description = "Load a page in a real browser and return its content"
args = ["fetch", "--dump", "{{format}}", "{{url}}"]
risk = "read"
timeout_secs = 60

[commands.input]
url = { type = "string", description = "Page URL", required = true }
format = { type = "string", enum = ["markdown", "text", "html"], default = "markdown" }
`;

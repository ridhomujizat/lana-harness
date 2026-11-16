/* Spec 55: brand logos of the coding agents the Workbench core recognizes
   (labels from internal/workbench/agentdetect). Files come from the LobeHub
   icon set (MIT, see src/assets/agents/README.md); an agent without a logo
   gets a monogram tile. */

const FILES = import.meta.glob("../assets/agents/*.svg", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/* Agent label (herdr's names) → file name without folder and extension. */
const AGENT_FILES: Record<string, string> = {
  claude: "claudecode-color",
  codex: "codex-color",
  gemini: "geminicli-color",
  cursor: "cursor",
  devin: "devin-color",
  agy: "antigravity-color",
  cline: "cline",
  mastracode: "mastra",
  opencode: "opencode",
  copilot: "githubcopilot",
  kimi: "kimi-color",
  kiro: "kiro-color",
  amp: "amp-color",
  grok: "grok",
  hermes: "hermesagent",
  kilo: "kilocode",
  qodercli: "qoder-color",
  qwen: "qwen-color",
  muse: "muse-color",
};

function inlineSvg(raw: string): string {
  return raw
    .replace(/<title>[\s\S]*?<\/title>/g, "")
    .replace(/\s(width|height)="1em"/g, "")
    .replace(/\sstyle="[^"]*"/, "")
    .replace("<svg ", '<svg aria-hidden="true" focusable="false" ');
}

const LOGOS: Record<string, string> = {};
for (const [agent, file] of Object.entries(AGENT_FILES)) {
  const raw = FILES["../assets/agents/" + file + ".svg"];
  if (raw) LOGOS[agent] = inlineSvg(raw);
}

export function hasAgentLogo(agent: string): boolean {
  return Boolean(LOGOS[agent]);
}

export function AgentLogo({ agent, size = 20 }: { agent: string; size?: number }) {
  const svg = LOGOS[agent];
  if (svg) {
    return (
      <span
        className="wb-agent-logo"
        style={{ width: size, height: size }}
        data-agent={agent}
        title={agent}
        dangerouslySetInnerHTML={{ __html: svg }}
      />
    );
  }
  return (
    <span className="wb-agent-logo wb-agent-mono" style={{ width: size, height: size }} data-agent={agent} title={agent}>
      {agent.slice(0, 2)}
    </span>
  );
}

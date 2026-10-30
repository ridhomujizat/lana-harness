import { avatarFor } from "../lib/avatar";
import { Icon as SharedIcon } from "../ui/ui";

/* Shared helpers for the agents pages (REQ-WEB-045, REQ-WEB-047). */

const PROVIDER_LABELS: Record<string, string> = {
  openai: "OpenAI",
  openrouter: "OpenRouter",
  anthropic: "Anthropic",
  deepseek: "DeepSeek",
  google: "Google",
  ollama: "Ollama",
};

export interface IconProps {
  name: string;
  className?: string;
}

export function Icon({ name, className = "icon-20" }: IconProps) {
  return <SharedIcon name={name} className={className} />;
}

export interface AgentLike {
  name?: string;
  kind?: string;
  provider?: string | null;
  model?: string | null;
  lead?: string;
  members?: string[];
  description?: string;
  default?: boolean;
  permission?: string;
  policy?: string;
  skills?: string[];
  mcp?: string[];
  disabled_tools?: string[];
  tools_count?: number;
  sessions_count?: number;
  last_active_at?: number | null;
}

export interface AgentAvatarProps {
  name: string;
  size?: string;
}

export function AgentAvatar({ name, size = "36" }: AgentAvatarProps) {
  const avatar = avatarFor(name);
  return (
    <span
      className={"agent-avatar avatar-" + size + " " + avatar.colorClass}
      aria-hidden="true"
    >
      {avatar.initial}
    </span>
  );
}

export function isTeam(agent: AgentLike | null | undefined): boolean {
  return agent != null && agent.kind === "team";
}

export function rosterNames(agent: AgentLike | null | undefined): string[] {
  if (!agent) {
    return [];
  }
  const names: string[] = [];
  if (agent.lead) {
    names.push(String(agent.lead));
  }
  const members = Array.isArray(agent.members) ? agent.members : [];
  for (const member of members) {
    const name = String(member || "");
    if (name && !names.includes(name)) {
      names.push(name);
    }
  }
  if (names.length === 0 && agent.name) {
    names.push(String(agent.name));
  }
  return names;
}

export interface TeamAvatarsProps {
  agent: AgentLike | null;
  size?: string;
}

export function TeamAvatars({ agent, size = "24" }: TeamAvatarsProps) {
  const names = rosterNames(agent).slice(0, 3);
  if (names.length === 0) {
    return null;
  }
  return (
    <span className="agent-avatar-group" aria-hidden="true">
      {names.map((name) => (
        <AgentAvatar key={name} name={name} size={size} />
      ))}
    </span>
  );
}

/* The API returns the combined "<provider>:<model>" string in the list row. */
export function splitModel(agent: AgentLike | null | undefined): {
  provider: string;
  model: string;
} {
  const explicitProvider = agent && agent.provider ? String(agent.provider) : "";
  const value = agent && agent.model ? String(agent.model) : "";
  if (!value) {
    return { provider: explicitProvider, model: "" };
  }
  const index = value.indexOf(":");
  if (index > 0) {
    return { provider: value.slice(0, index), model: value.slice(index + 1) };
  }
  return { provider: explicitProvider, model: value };
}

/* A fallback target is "<provider>:<model>" (config/validate.go). */
export function splitTarget(target: unknown): {
  provider: string;
  model: string;
} {
  const value = String(target || "");
  const index = value.indexOf(":");
  if (index > 0) {
    return { provider: value.slice(0, index), model: value.slice(index + 1) };
  }
  return { provider: "", model: value };
}

export function providerLabel(value: unknown): string {
  const name = String(value || "");
  if (!name) {
    return "";
  }
  const known = PROVIDER_LABELS[name.toLowerCase()];
  if (known) {
    return known;
  }
  return name
    .split(/[-_]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export type Permission = "ask" | "trust" | "never";

export function normalizePermission(value: unknown): Permission {
  const text = String(value || "").toLowerCase();
  if (text === "trust" || text === "never") {
    return text;
  }
  return "ask";
}

export interface PermissionMeta {
  label: string;
  className: string;
}

export function permissionMeta(value: unknown): PermissionMeta {
  const permission = normalizePermission(value);
  if (permission === "trust") {
    return { label: "Trust", className: "badge-ui badge-ui-warning" };
  }
  if (permission === "never") {
    return { label: "Never", className: "badge-ui badge-ui-error" };
  }
  return { label: "Ask", className: "badge-ui" };
}

/* Compact relative time for the list and the detail header. */
export function agoLabel(ms: unknown): string {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) {
    return "";
  }
  const now = Date.now();
  const diff = Math.max(0, now - value);
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) {
    return "just now";
  }
  if (minutes < 60) {
    return minutes + " min ago";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return hours + " h ago";
  }
  const then = new Date(value);
  const today = new Date(now);
  const startToday = new Date(
    today.getFullYear(),
    today.getMonth(),
    today.getDate()
  ).getTime();
  const startThen = new Date(
    then.getFullYear(),
    then.getMonth(),
    then.getDate()
  ).getTime();
  const days = Math.round((startToday - startThen) / 86400000);
  if (days <= 1) {
    return "Yesterday";
  }
  if (days < 7) {
    return days + " days ago";
  }
  return then.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function sessionSummary(agent: AgentLike | null | undefined): string {
  const count = Number(agent && agent.sessions_count) || 0;
  const sessions = count === 1 ? "1 session" : count + " sessions";
  const active = agoLabel(agent && agent.last_active_at);
  if (!active) {
    return sessions + ", never active";
  }
  return sessions + ", last active " + active;
}

export function agentHref(name: string, tab = "general"): string {
  return "#/agents/" + encodeURIComponent(name) + "/" + tab;
}

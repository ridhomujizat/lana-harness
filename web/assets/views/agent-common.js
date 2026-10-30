import { html } from "/assets/vendor/preact-htm.js";
import { avatarFor } from "/assets/avatar.js";

/* Shared helpers for the agents pages (REQ-WEB-045, REQ-WEB-047). */

const PROVIDER_LABELS = {
  openai: "OpenAI",
  openrouter: "OpenRouter",
  anthropic: "Anthropic",
  deepseek: "DeepSeek",
  google: "Google",
  ollama: "Ollama",
};

export function Icon({ name, className = "icon-20" }) {
  return html`<img
    class=${className}
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
}

export function AgentAvatar({ name, size = "36" }) {
  const avatar = avatarFor(name);
  return html`<span
    class=${"agent-avatar avatar-" + size + " " + avatar.colorClass}
    aria-hidden="true"
  >${avatar.initial}</span>`;
}

export function isTeam(agent) {
  return Boolean(agent) && agent.kind === "team";
}

export function rosterNames(agent) {
  if (!agent) {
    return [];
  }
  const names = [];
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

export function TeamAvatars({ agent, size = "24" }) {
  const names = rosterNames(agent).slice(0, 3);
  if (names.length === 0) {
    return null;
  }
  return html`<span class="agent-avatar-group" aria-hidden="true">
    ${names.map((name) => html`<${AgentAvatar} key=${name} name=${name} size=${size} />`)}
  </span>`;
}

/* The API returns the combined "<provider>:<model>" string in the list row. */
export function splitModel(agent) {
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
export function splitTarget(target) {
  const value = String(target || "");
  const index = value.indexOf(":");
  if (index > 0) {
    return { provider: value.slice(0, index), model: value.slice(index + 1) };
  }
  return { provider: "", model: value };
}

export function providerLabel(value) {
  const name = String(value || "");
  if (!name) {
    return "";
  }
  if (PROVIDER_LABELS[name.toLowerCase()]) {
    return PROVIDER_LABELS[name.toLowerCase()];
  }
  return name
    .split(/[-_]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function normalizePermission(value) {
  const text = String(value || "").toLowerCase();
  if (text === "trust" || text === "never") {
    return text;
  }
  return "ask";
}

export function permissionMeta(value) {
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
export function agoLabel(ms) {
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

export function sessionSummary(agent) {
  const count = Number(agent && agent.sessions_count) || 0;
  const sessions = count === 1 ? "1 session" : count + " sessions";
  const active = agoLabel(agent && agent.last_active_at);
  if (!active) {
    return sessions + ", never active";
  }
  return sessions + ", last active " + active;
}

export function agentHref(name, tab = "general") {
  return "#/agents/" + encodeURIComponent(name) + "/" + tab;
}

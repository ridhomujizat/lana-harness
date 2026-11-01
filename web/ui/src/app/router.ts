/* The route table of REQ-WEB-030. {name} segments match any value; tab and
   step segments are limited by the lists on each entry. */

export const DEFAULT_ROUTE = "#/chats";

export const AGENT_TABS = [
  "general",
  "persona",
  "tools",
  "skills",
  "mcp",
  "rules",
  "workspace",
] as const;
export const SETTINGS_TABS = [
  "general",
  "providers",
  "secrets",
  "config",
  "audit",
] as const;
export const LEGACY_SETTINGS_TABS = ["agents", "skills"] as const;
export const SETUP_STEPS = [
  "unlock",
  "provider",
  "connect",
  "agent",
  "persona",
  "ready",
] as const;

export type RouteView =
  | "home"
  | "chats"
  | "chat"
  | "sessions"
  | "new-session"
  | "session"
  | "agents"
  | "agent"
  | "skills"
  | "new-skill"
  | "skill"
  | "knowledge"
  | "mcp"
  | "mcp-detail"
  | "channels"
  | "telegram"
  | "settings"
  | "setup"
  | "login"
  | "welcome"
  | "forgot"
  | "not-found";

export interface RouteEntry {
  path: string;
  view: RouteView;
  section: string;
  tabs?: readonly string[];
  steps?: readonly string[];
}

export interface Route {
  view: RouteView;
  section: string;
  params: Record<string, string>;
  path: string;
}

export const ROUTE_TABLE: RouteEntry[] = [
  { path: "#/home", view: "home", section: "home" },
  { path: "#/chats", view: "chats", section: "chats" },
  { path: "#/chats/{name}", view: "chat", section: "chats" },
  { path: "#/sessions", view: "sessions", section: "sessions" },
  { path: "#/history", view: "sessions", section: "sessions" },
  { path: "#/sessions/new", view: "new-session", section: "sessions" },
  { path: "#/sessions/{id}", view: "session", section: "sessions" },
  { path: "#/agents", view: "agents", section: "agents" },
  {
    path: "#/agents/{name}/{tab}",
    view: "agent",
    section: "agents",
    tabs: AGENT_TABS,
  },
  { path: "#/skills", view: "skills", section: "skills" },
  { path: "#/skills/new", view: "new-skill", section: "skills" },
  { path: "#/skills/{scope}/{name}", view: "skill", section: "skills" },
  { path: "#/knowledge", view: "knowledge", section: "knowledge" },
  { path: "#/mcp", view: "mcp", section: "mcp" },
  { path: "#/mcp/{name}", view: "mcp-detail", section: "mcp" },
  { path: "#/channels", view: "channels", section: "channels" },
  { path: "#/channels/telegram", view: "telegram", section: "channels" },
  {
    path: "#/settings/{tab}",
    view: "settings",
    section: "settings",
    tabs: [...SETTINGS_TABS, ...LEGACY_SETTINGS_TABS],
  },
  { path: "#/setup/{step}", view: "setup", section: "", steps: SETUP_STEPS },
  { path: "#/welcome", view: "welcome", section: "" },
  { path: "#/forgot", view: "forgot", section: "" },
];

function safeDecode(part: string): string {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

/* The sign-in link puts its one-time code in the hash query
   (#/welcome?code=…). Read it next to the path params. */
function parseQuery(query: string): Record<string, string> {
  const params: Record<string, string> = {};
  if (!query) {
    return params;
  }
  new URLSearchParams(query).forEach((value, key) => {
    params[key] = value;
  });
  return params;
}

export function matchRoute(
  parts: string[],
  entry: RouteEntry
): Record<string, string> | null {
  const segments = entry.path.slice(2).split("/");
  if (segments.length !== parts.length) {
    return null;
  }
  const params: Record<string, string> = {};
  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index] || "";
    const value = parts[index] || "";
    if (segment.startsWith("{") && segment.endsWith("}")) {
      const name = segment.slice(1, -1);
      if (name === "tab" && entry.tabs && !entry.tabs.includes(value)) {
        return null;
      }
      if (name === "step" && entry.steps && !entry.steps.includes(value)) {
        return null;
      }
      params[name] = value;
      continue;
    }
    if (segment !== value) {
      return null;
    }
  }
  return params;
}

export function parseRoute(hash: string): Route {
  const raw = String(hash || "").replace(/^#/, "");
  const queryIndex = raw.indexOf("?");
  const rawPath = queryIndex >= 0 ? raw.slice(0, queryIndex) : raw;
  const query = queryIndex >= 0 ? raw.slice(queryIndex + 1) : "";
  const queryParams = parseQuery(query);
  const parts = rawPath.split("/").filter(Boolean).map(safeDecode);
  if (parts.length === 0) {
    return {
      view: "chats",
      section: "chats",
      params: queryParams,
      path: DEFAULT_ROUTE,
    };
  }
  if (parts.length === 1 && parts[0] === "login") {
    /* Rev 1 login alias; the setup wizard owns #/setup/unlock. */
    return {
      view: "login",
      section: "",
      params: queryParams,
      path: "#/login",
    };
  }
  if (parts.length === 1 && parts[0] === "new") {
    /* Rev 1 new-session alias kept so the sessions list keeps working. */
    return {
      view: "new-session",
      section: "sessions",
      params: queryParams,
      path: "#/sessions/new",
    };
  }
  for (const entry of ROUTE_TABLE) {
    const params = matchRoute(parts, entry);
    if (params !== null) {
      return {
        view: entry.view,
        section: entry.section,
        params: { ...params, ...queryParams },
        path: "#/" + parts.join("/"),
      };
    }
  }
  return {
    view: "not-found",
    section: "",
    params: queryParams,
    path: "#/" + parts.join("/"),
  };
}

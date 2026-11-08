/* The route table of REQ-WEB-030. {name} segments match any value; tab and
   step segments are limited by the lists on each entry. */

export const DEFAULT_ROUTE = "#/chats";

/* A sign-in link may carry next=#/… (spec #41, the macOS menu bar app) to
   open a page after the claim. Follow it only when it is an in-app hash with
   URL-safe characters and not a sign-in page itself; the daemon applies the
   same rule (web.SafeNextHash) before it puts next in the link. */
const SAFE_NEXT = /^#\/[A-Za-z0-9/\-_.~?=&%+]+$/;
const SIGN_IN_PAGES = ["welcome", "login", "forgot"];

export function safeNextHash(next: string | undefined | null): string {
  if (!next || next.length > 256 || !SAFE_NEXT.test(next) || next.startsWith("#//")) {
    return "";
  }
  const page = next.slice(2).split(/[/?]/)[0] || "";
  return SIGN_IN_PAGES.includes(page) ? "" : next;
}

export const AGENT_TABS = [
  "overview",
  "general",
  "persona",
  "tools",
  "skills",
  "mcp",
  "rules",
  "workspace",
] as const;

/* REQ-ROOM-010: the Overview tab is the first tab and the default when a name
   is opened without a tab segment. */
export const DEFAULT_AGENT_TAB = AGENT_TABS[0];
/* Spec 37 (API-RTR-017): Providers moved from Settings to the Router page;
   #/settings/providers redirects to #/router/providers. */
export const SETTINGS_TABS = [
  "general",
  "secrets",
  "config",
  "audit",
] as const;
export const LEGACY_SETTINGS_TABS = ["agents", "skills"] as const;

/* The Router pages (API-RTR-017 revision 2): each is its own sidebar item
   under the Router group. #/router opens Providers; one provider's page is
   #/router/providers/<preset or name>. */
export const ROUTER_TABS = [
  "providers",
  "combos",
  "usage",
  "quota",
  "logs",
  "endpoint",
] as const;
export const DEFAULT_ROUTER_TAB = "providers";

/* Old hashes that now live elsewhere. The App replaces the hash with the
   target so the address bar shows the new place. The Router's first layout
   had Overview, Models, and Proxy tabs. */
export const ROUTE_REDIRECTS: Record<string, string> = {
  "#/settings/providers": "#/router/providers",
  "#/router/overview": "#/router/usage",
  "#/router/proxy": "#/router/endpoint",
  "#/router/models": "#/router/providers",
};
/* "connect" is the Connect your models step; "provider" is its rev 2 name,
   kept for open tabs and links. */
export const SETUP_STEPS = [
  "unlock",
  "provider",
  "connect",
  "model",
  "agent",
  "persona",
  "ready",
] as const;

export type RouteView =
  | "home"
  | "chats"
  | "chat"
  | "logs"
  | "tools"
  | "tool"
  | "schedules"
  | "calendar"
  | "workflows"
  | "workflow-new"
  | "workflow"
  | "workflow-runs"
  | "workflow-run"
  | "workflow-settings"
  | "sessions"
  | "new-session"
  | "session"
  | "router"
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
  { path: "#/logs", view: "logs", section: "logs" },
  { path: "#/tools", view: "tools", section: "tools" },
  { path: "#/tools/{id}", view: "tool", section: "tools" },
  { path: "#/schedules", view: "schedules", section: "schedules" },
  { path: "#/calendar", view: "calendar", section: "calendar" },
  { path: "#/workflows", view: "workflows", section: "workflows" },
  { path: "#/workflows/new", view: "workflow-new", section: "workflows" },
  { path: "#/workflows/{id}", view: "workflow", section: "workflows" },
  { path: "#/workflows/{id}/runs", view: "workflow-runs", section: "workflows" },
  { path: "#/workflows/{id}/runs/{run}", view: "workflow-run", section: "workflows" },
  { path: "#/workflows/{id}/settings", view: "workflow-settings", section: "workflows" },
  { path: "#/sessions", view: "sessions", section: "sessions" },
  { path: "#/history", view: "sessions", section: "sessions" },
  { path: "#/sessions/new", view: "new-session", section: "sessions" },
  { path: "#/sessions/{id}", view: "session", section: "sessions" },
  { path: "#/router", view: "router", section: "router" },
  {
    path: "#/router/{tab}",
    view: "router",
    section: "router",
    tabs: ROUTER_TABS,
  },
  { path: "#/router/providers/{provider}", view: "router", section: "router" },
  { path: "#/agents", view: "agents", section: "agents" },
  { path: "#/agents/{name}", view: "agent", section: "agents" },
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
  const redirect = ROUTE_REDIRECTS["#/" + parts.join("/")];
  if (redirect) {
    const target = parseRoute(redirect);
    return { ...target, params: { ...target.params, ...queryParams } };
  }
  for (const entry of ROUTE_TABLE) {
    const params = matchRoute(parts, entry);
    if (params !== null) {
      if (entry.view === "agent" && !params.tab) {
        params.tab = DEFAULT_AGENT_TAB;
      }
      if (entry.view === "router" && !params.tab) {
        params.tab = DEFAULT_ROUTER_TAB;
      }
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

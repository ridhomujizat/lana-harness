import { describe, expect, it } from "vitest";
import {
  AGENT_TABS,
  LEGACY_SETTINGS_TABS,
  ROUTE_TABLE,
  SETTINGS_TABS,
  SETUP_STEPS,
  parseRoute,
} from "./router";

const ROUTES = [
  "#/home",
  "#/sessions",
  "#/sessions/new",
  "#/sessions/{id}",
  "#/agents",
  "#/agents/{name}/{tab}",
  "#/skills",
  "#/skills/{scope}/{name}",
  "#/skills/new",
  "#/mcp",
  "#/mcp/{name}",
  "#/channels",
  "#/channels/telegram",
  "#/settings/{tab}",
  "#/setup/{step}",
  "#/welcome",
  "#/forgot",
];

const SEGMENTS = [
  "general",
  "persona",
  "tools",
  "skills",
  "mcp",
  "rules",
  "workspace",
  "providers",
  "secrets",
  "config",
  "audit",
  "unlock",
  "provider",
  "connect",
  "agent",
  "ready",
];

describe("route table", () => {
  it("lists the same routes as the requirement", () => {
    const paths = ROUTE_TABLE.map((entry) => entry.path);
    for (const route of ROUTES) {
      expect(paths).toContain(route);
    }
  });

  it("lists the tab and step segments", () => {
    const segments = new Set<string>();
    for (const entry of ROUTE_TABLE) {
      for (const tab of entry.tabs || []) {
        segments.add(tab);
      }
      for (const step of entry.steps || []) {
        segments.add(step);
      }
    }
    for (const segment of SEGMENTS) {
      expect(segments.has(segment)).toBe(true);
    }
    expect(AGENT_TABS).toContain("workspace");
    expect(SETTINGS_TABS).toContain("audit");
    expect(LEGACY_SETTINGS_TABS).toContain("agents");
    expect(SETUP_STEPS).toContain("ready");
  });

  it("resolves agent tabs and setup steps", () => {
    const agent = parseRoute("#/agents/coder/persona");
    expect(agent.view).toBe("agent");
    expect(agent.params).toEqual({ name: "coder", tab: "persona" });

    const setup = parseRoute("#/setup/provider");
    expect(setup.view).toBe("setup");
    expect(setup.params).toEqual({ step: "provider" });

    const skill = parseRoute("#/skills/global/review");
    expect(skill.view).toBe("skill");
    expect(skill.params).toEqual({ scope: "global", name: "review" });
  });

  it("parses the sign-in code from the hash query", () => {
    const welcome = parseRoute("#/welcome?code=abc123");
    expect(welcome.view).toBe("welcome");
    expect(welcome.params).toEqual({ code: "abc123" });

    expect(parseRoute("#/welcome").view).toBe("welcome");
    expect(parseRoute("#/forgot").view).toBe("forgot");
  });

  it("maps unknown routes to not-found", () => {
    expect(parseRoute("#/nope").view).toBe("not-found");
    expect(parseRoute("#/agents/coder/bogus").view).toBe("not-found");
    expect(parseRoute("#/setup/bogus").view).toBe("not-found");
    expect(parseRoute("#/settings/bogus").view).toBe("not-found");
  });

  it("keeps the rev 1 aliases and the default route", () => {
    expect(parseRoute("").path).toBe("#/sessions");
    expect(parseRoute("#/").view).toBe("sessions");
    expect(parseRoute("#/login").view).toBe("login");
    expect(parseRoute("#/new").path).toBe("#/sessions/new");
    expect(parseRoute("#/new").view).toBe("new-session");
  });
});

import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("../api/skills", () => ({
  listSkills: vi.fn(),
  listAgents: vi.fn(),
  getSkill: vi.fn(),
  putSkill: vi.fn(),
  deleteSkill: vi.fn(),
  putAgentSkills: vi.fn(),
  getConfig: vi.fn(),
}));

import { listSkills, getSkill, getConfig, listAgents } from "../api/skills";
import type { GetConfigResponse } from "../api/settings";
import type { Agent } from "../api/agents";
import { SkillsPage, SkillEditorPage } from "./skills";

const CONFIG: GetConfigResponse = {
  version: "cfg1",
  loaded_version: "cfg1",
  restart_required: false,
  validation: { ok: true, errors: [] },
  raw: "",
  settings: {
    providers: {},
    agents: {},
    routes: [],
    channels: {},
    mcp: {},
    modules: [],
  },
};

const AGENT: Agent = {
  name: "scout",
  kind: "agent",
  title: "",
  color: "brand",
  model: "openai:gpt-4o",
  policy: "ask",
  skills: ["deploy"],
  mcp: [],
  mcp_status: [],
  description: "Scout",
  default: false,
  provider: "openai",
  permission: "ask",
  workspace: "",
  disabled_tools: [],
  tools_count: 0,
  sessions_count: 0,
  last_active_at: null,
};

describe("skills list", () => {
  it("shows the empty state when there are no skills", async () => {
    vi.mocked(listSkills).mockResolvedValue({ skills: [], invalid: [] });
    render(<SkillsPage />);
    expect(await screen.findByText("No skills yet")).toBeTruthy();
    expect(
      screen.getByText(/A skill is a folder with a SKILL.md/)
    ).toBeTruthy();
  });

  it("shows readable and invalid rows with the scope badge", async () => {
    vi.mocked(listSkills).mockResolvedValue({
      skills: [
        {
          name: "deploy",
          scope: "global",
          description: "Deploy the app",
          valid: true,
          error: "",
          size: 2048,
          updated_at: Date.now(),
        },
      ],
      invalid: [
        {
          name: "broken",
          scope: "agent:scout",
          description: "",
          valid: false,
          error: "frontmatter is not closed",
          size: 0,
          updated_at: 0,
        },
      ],
    });
    render(<SkillsPage />);
    expect(await screen.findByText("deploy")).toBeTruthy();
    expect(screen.getByText("Deploy the app")).toBeTruthy();
    expect(screen.getByText("broken")).toBeTruthy();
    expect(screen.getByText("Can't load")).toBeTruthy();
    expect(screen.getByText("frontmatter is not closed")).toBeTruthy();
    expect(screen.getAllByText("Global").length).toBeGreaterThan(0);
  });
});

describe("skill editor", () => {
  it("loads an existing skill into the form and the folder hint", async () => {
    vi.mocked(listAgents).mockResolvedValue({ agents: [AGENT] });
    vi.mocked(getConfig).mockResolvedValue(CONFIG);
    vi.mocked(listSkills).mockResolvedValue({
      skills: [
        {
          name: "deploy",
          scope: "global",
          description: "Deploy app",
          valid: true,
          error: "",
          size: 40,
          updated_at: Date.now(),
        },
      ],
      invalid: [],
    });
    vi.mocked(getSkill).mockResolvedValue({
      content:
        "---\nname: deploy\ndescription: Deploy app\n---\nStep one\n",
      version: "v1",
      path: "/home/owner/.lana/skills/deploy/SKILL.md",
    });

    render(<SkillEditorPage scope="global" name="deploy" />);
    expect(
      await screen.findByRole("heading", { name: "deploy" })
    ).toBeTruthy();
    expect(screen.getByDisplayValue("Deploy app")).toBeTruthy();
    expect(screen.getByDisplayValue(/Step one/)).toBeTruthy();
    expect(screen.getByText("$LANA_HOME/skills/deploy/")).toBeTruthy();
    expect(screen.getByText("1 of 1")).toBeTruthy();
  });

  it("starts a new skill with the scope picker and editable name", async () => {
    vi.mocked(listAgents).mockResolvedValue({ agents: [AGENT] });
    vi.mocked(getConfig).mockResolvedValue(CONFIG);

    render(<SkillEditorPage isNew={true} />);
    expect(
      await screen.findByRole("heading", { name: "New skill" })
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save skill" })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: /Scope/ })).toBeTruthy();
  });
});

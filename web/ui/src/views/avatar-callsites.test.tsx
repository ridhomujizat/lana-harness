import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { resetAvatarStore, setAgentAvatar } from "../lib/avatar-store";
import { AgentFace } from "./workflow-common";

/* Spec 43: no letter avatars for agents. Every agent avatar goes through
   AgentAvatar (views/agent-avatar.tsx) or the presence AgentFace, which keep
   the letter only as the last fallback when the server image fails. */

const SRC = join(dirname(fileURLToPath(import.meta.url)), "..");

function sources(): Array<{ path: string; text: string }> {
  const out: Array<{ path: string; text: string }> = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(full);
      } else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) {
        out.push({ path: relative(SRC, full).replaceAll("\\", "/"), text: readFileSync(full, "utf8") });
      }
    }
  };
  walk(SRC);
  return out;
}

describe("letter avatars for agents (spec 43)", () => {
  const files = sources();

  it("uses avatarFor only inside AgentAvatar", () => {
    const users = files
      .filter((file) => /\bavatarFor\b/.test(file.text))
      .map((file) => file.path)
      .sort();
    expect(users).toEqual(["lib/avatar.ts", "views/agent-avatar.tsx"]);
  });

  it("renders the legacy tone letter nowhere else", () => {
    const users = files
      .filter((file) => /agent-avatar tone-|agentInitial|agentTone\(/.test(file.text))
      .map((file) => file.path);
    expect(users).toEqual([]);
  });

  /* The Gr8r Avatar primitive draws letters. Outside AgentAvatar it is only
     used for non-agents: the empty "Unassigned" slot, Telegram chats and
     owners, and the dialog preview that renders a given svg. */
  it("keeps the Avatar primitive off agent names", () => {
    const allowed = new Set([
      "ui/gr8r/primitives.tsx",
      "views/agent-avatar.tsx",
      "views/avatar-dialog.tsx",
      "views/channels.tsx",
      "views/new-session.tsx",
    ]);
    const users = files
      .filter((file) => /<Avatar\b|<AvatarStack\b/.test(file.text))
      .map((file) => file.path)
      .filter((path) => !allowed.has(path));
    expect(users).toEqual([]);
    const newSession = files.find((file) => file.path === "views/new-session.tsx");
    expect(newSession?.text.match(/<Avatar\b[^>]*>/g)).toEqual(['<Avatar empty size="sm" />']);
  });
});

describe("agent avatars at call sites (spec 43)", () => {
  afterEach(() => {
    resetAvatarStore();
  });

  it("shows the workflow guardian's svg", () => {
    setAgentAvatar(
      "guard",
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
        '<rect x="15" y="15" width="70" height="70" rx="26" fill="currentColor"/>' +
        '<ellipse cx="40" cy="46" rx="4" ry="7" fill="#ffffff"/>' +
        '<ellipse cx="60" cy="46" rx="4" ry="7" fill="#ffffff"/></svg>',
      "#ff6a00"
    );
    const { container } = render(<AgentFace name="guard" />);
    const avatar = container.querySelector(".av.has-svg") as HTMLElement;
    expect(avatar.querySelector("svg rect")).not.toBeNull();
    expect(avatar.style.getPropertyValue("--c")).toBe("#ff6a00");
  });

  it("asks the server for a guardian the store has not seen", () => {
    const { container } = render(<AgentFace name="night-bot" />);
    expect(container.querySelector("img.av-img")?.getAttribute("src")).toBe(
      "/v1/agents/night-bot/avatar.svg?theme=light"
    );
  });
});

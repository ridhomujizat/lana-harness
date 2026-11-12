import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { parseAvatarSvg } from "./avatar-svg";
import { turnMood } from "../views/turn";
import type { Turn, TurnStep } from "../views/turn";
import { Avatar } from "../ui/gr8r";

const OPEN = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">';

const BLOB =
  OPEN +
  '<ellipse cx="50" cy="54" rx="40" ry="32" fill="currentColor"/>' +
  '<ellipse cx="40" cy="46" rx="4" ry="7" fill="#ffffff"/>' +
  '<g fill="#ffffff"><ellipse cx="60" cy="46" rx="4" ry="7"/></g></svg>';

const MIXED =
  OPEN +
  '<g><ellipse cx="50" cy="54" rx="40" ry="32" fill="currentColor"/>' +
  '<ellipse cx="40" cy="46" rx="4" ry="7" fill="#ffffff"/></g></svg>';

describe("avatar rig", () => {
  it("moves the eyes into look > gaze > eye, after the body", () => {
    const { container } = render(<span>{parseAvatarSvg(BLOB)}</span>);
    const svg = container.querySelector("svg.av-svg");
    expect(svg?.classList.contains("has-eyes")).toBe(true);
    const squash = svg?.querySelector(":scope > g.av-squash");
    expect(squash).not.toBeNull();
    expect(squash?.firstElementChild?.getAttribute("fill")).toBe("currentColor");
    const eyes = squash?.querySelectorAll(":scope > g.av-look > g.av-gaze > g.av-eye");
    expect(eyes?.length).toBe(2);
  });

  it("leaves a group that mixes body and eyes unrigged", () => {
    const { container } = render(<span>{parseAvatarSvg(MIXED)}</span>);
    const svg = container.querySelector("svg.av-svg");
    expect(svg?.classList.contains("has-eyes")).toBe(false);
    expect(svg?.querySelector(".av-eye")).toBeNull();
    expect(svg?.querySelectorAll("ellipse").length).toBe(2);
  });
});

describe("Avatar mood", () => {
  it("marks the host and adds the mood marks", () => {
    const { container } = render(<Avatar name="nara" svg={BLOB} size="lg" mood="tool" />);
    const host = container.querySelector(".av") as HTMLElement;
    expect(host.getAttribute("data-mood")).toBe("tool");
    expect(host.style.getPropertyValue("--avpx")).toBe("36px");
    expect(host.querySelector(".av-ring")).not.toBeNull();
  });

  it("keeps a letter avatar still", () => {
    const { container } = render(<Avatar name="nara" size="lg" mood="tool" />);
    const host = container.querySelector(".av") as HTMLElement;
    expect(host.hasAttribute("data-mood")).toBe(false);
    expect(host.querySelector(".av-ring")).toBeNull();
  });
});

function step(patch: Partial<TurnStep>): TurnStep {
  return { key: "s", callId: "c", name: "bash", input: {}, status: "done", output: "ok", approval: null, ...patch };
}

describe("turnMood", () => {
  it("rests when the turn is over", () => {
    expect(turnMood({ running: false })).toBe("rest");
  });

  it("names what a running turn is doing", () => {
    const running: Turn = { running: true, groups: [] };
    expect(turnMood(running)).toBe("thinking");
    expect(turnMood(running, { streaming: true })).toBe("talk");
    expect(turnMood(running, { waitingForInput: true })).toBe("ask");
    const tool: Turn = {
      running: true,
      groups: [{ kind: "activity", key: "a", steps: [step({ status: "running", output: "" })] }],
    };
    expect(turnMood(tool, { streaming: true })).toBe("tool");
    const approval: Turn = {
      running: true,
      groups: [
        {
          kind: "activity",
          key: "a",
          steps: [
            step({
              approval: { request_id: "r", call_id: "c", tool: "bash", risk: "high", label: "", decided: "" },
            }),
          ],
        },
      ],
    };
    expect(turnMood(approval)).toBe("ask");
    const answered: Turn = { running: true, groups: [{ kind: "text", key: "t", text: "Hi" }] };
    expect(turnMood(answered)).toBe("talk");
    expect(turnMood(answered, { thinking: true })).toBe("thinking");
    expect(turnMood(running, { tool: true })).toBe("tool");
    expect(turnMood(running, { tool: true, streaming: true })).toBe("talk");
  });
});

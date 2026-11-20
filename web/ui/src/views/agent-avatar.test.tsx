import { afterEach, describe, expect, it } from "vitest";
import { act, fireEvent, render } from "@testing-library/react";
import {
  agentCssColor,
  composeFrame,
  isAvatarDoc,
  isSprite,
  isWorkingMood,
  OUTLINE,
  runsOf,
} from "../lib/pixel-avatar";
import { rememberAvatars, resetAvatarStore, setAgentAvatar } from "../lib/avatar-store";
import { sampleAvatar, sampleDrawn, sampleSprite } from "../test/pixel-sample";
import { AgentAvatar, AgentAvatarStack } from "./agent-avatar";
import { AgentFace, gr8rColor } from "./presence";

/* Spec 62: pixel-art agent avatars. The sprite comes from the server as a
   palette and two layers; the client composes frames, adds the outline, and
   draws rects. Anything malformed falls back to the server image, then the
   letter. */

afterEach(() => {
  resetAvatarStore();
});

describe("sprite checks and frames", () => {
  it("accepts a well-formed sprite and refuses malformed ones", () => {
    const sprite = sampleSprite();
    expect(isSprite(sprite)).toBe(true);
    expect(isAvatarDoc(sampleAvatar())).toBe(true);
    expect(isSprite({ ...sprite, head: sprite.head.slice(1) })).toBe(false);
    expect(isSprite({ ...sprite, body: [...sprite.body.slice(1), "x".repeat(16)] })).toBe(false);
    expect(isSprite({ ...sprite, palette: { ...sprite.palette, c: "red" } })).toBe(false);
    expect(isSprite({ ...sprite, palette: { ...sprite.palette, "<": "#ffffff" } })).toBe(false);
    expect(isAvatarDoc({ kind: "blob", sprite })).toBe(false);
    expect(isAvatarDoc("<svg/>")).toBe(false);
  });

  it("outlines the silhouette and keeps the eyes", () => {
    const cells = composeFrame(sampleSprite(), 0);
    expect(cells.get("7,7")).toBe("#1b1820");
    /* Above the hair and below the shoes is the outline. */
    expect(cells.get("5,1")).toBe(OUTLINE);
    expect(cells.get("5,18")).toBe(OUTLINE);
    /* Nothing outside the outline ring. */
    expect(cells.has("0,0")).toBe(false);
  });

  it("nods, types, and blinks in the animation frames", () => {
    const sprite = sampleSprite();
    const rest = composeFrame(sprite, 0);
    const nod = composeFrame(sprite, 1);
    const blink = composeFrame(sprite, 2);
    /* Frame 1: the head is one row lower and the left hand one row higher. */
    expect(nod.get("7,8")).toBe(rest.get("7,7"));
    expect(nod.get("4,13")).toBe(sprite.palette.s);
    /* Frame 2: the top eye cell takes the skin to its left. */
    expect(blink.get("7,7")).toBe(sprite.palette.s);
    expect(blink.get("7,8")).toBe("#1b1820");
  });

  it("merges neighbours of one color into one rect", () => {
    const runs = runsOf(
      new Map([
        ["0,0", "#000000"],
        ["1,0", "#000000"],
        ["2,0", "#ffffff"],
      ])
    );
    expect(runs).toEqual([
      { x: 0, y: 0, w: 2, fill: "#000000" },
      { x: 2, y: 0, w: 1, fill: "#ffffff" },
    ]);
  });

  it("animates only the working moods", () => {
    expect(isWorkingMood("thinking")).toBe(true);
    expect(isWorkingMood("tool")).toBe(true);
    expect(isWorkingMood("talk")).toBe(true);
    expect(isWorkingMood("ask")).toBe(false);
    expect(isWorkingMood(undefined)).toBe(false);
  });
});

describe("agent colors", () => {
  it("maps tokens to Gr8r variables and passes hex through", () => {
    expect(agentCssColor("success")).toBe("var(--green)");
    expect(agentCssColor("#FF6A00")).toBe("#ff6a00");
    expect(agentCssColor("nope")).toBe("var(--violet)");
    expect(gr8rColor("#12b5a6")).toBe("#12b5a6");
    expect(gr8rColor("blue")).toBe("var(--blue)");
  });
});

describe("AgentAvatar", () => {
  it("renders the stored sprite in a tile of the agent color", () => {
    setAgentAvatar("lana", sampleAvatar(), "success");
    const { container } = render(<AgentAvatar name="lana" color="var(--violet)" size="lg" />);
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.classList.contains("has-pixel")).toBe(true);
    expect(avatar.style.getPropertyValue("--pxc")).toBe("var(--green)");
    const svg = avatar.querySelector("svg.px-sprite") as SVGElement;
    expect(svg.getAttribute("viewBox")).toBe("0 -1 16 16");
    expect(svg.getAttribute("shape-rendering")).toBe("crispEdges");
    expect(svg.querySelectorAll("g.px-f")).toHaveLength(1);
    expect(svg.querySelector("rect")).not.toBeNull();
    expect(avatar.getAttribute("aria-label")).toBe("lana");
    expect(avatar.textContent).toBe("");
  });

  it("animates a working mood with four frames and a bubble on large avatars", () => {
    setAgentAvatar("lana", sampleAvatar(), "brand");
    const { container } = render(<AgentAvatar name="lana" size="xl" mood="thinking" />);
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.classList.contains("is-working")).toBe(true);
    expect(avatar.querySelector("svg.px-sprite.is-animated")).not.toBeNull();
    expect(avatar.querySelectorAll("g.px-f")).toHaveLength(4);
    expect(avatar.querySelector(".px-bubble")).not.toBeNull();
  });

  it("keeps a resting or asking avatar still", () => {
    setAgentAvatar("lana", sampleAvatar(), "brand");
    const { container } = render(
      <>
        <AgentAvatar name="lana" size="md" mood="ask" />
        <AgentAvatar name="lana" size="md" mood="thinking" />
      </>
    );
    const [ask, thinking] = Array.from(container.querySelectorAll(".av"));
    expect(ask?.querySelectorAll("g.px-f")).toHaveLength(1);
    expect(thinking?.querySelectorAll("g.px-f")).toHaveLength(4);
    /* A small working avatar has no bubble. */
    expect(thinking?.querySelector(".px-bubble")).toBeNull();
  });

  it("shows the whole character in the full view", () => {
    setAgentAvatar("lana", sampleDrawn(), "#13b5c8");
    const { container } = render(<AgentAvatar name="lana" size="xl" view="full" />);
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.classList.contains("is-full")).toBe(true);
    expect(avatar.querySelector("svg")?.getAttribute("viewBox")).toBe("-1 -1 18 21");
    expect(avatar.querySelector("rect.px-ground")).not.toBeNull();
  });

  it("uses the server image for a name the store has not seen", () => {
    const { container } = render(<AgentAvatar name="cody" color="var(--blue)" size="md" />);
    const avatar = container.querySelector(".av") as HTMLElement;
    const img = avatar.querySelector("img.av-img") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/v1/agents/cody/avatar.svg?theme=light&view=bust");
    expect(avatar.classList.contains("has-pixel")).toBe(true);
    expect(avatar.getAttribute("aria-label")).toBe("cody");
  });

  it("falls back to the letter only when the image fails", () => {
    const { container } = render(<AgentAvatar name="cody" color="var(--blue)" size="md" />);
    fireEvent.error(container.querySelector("img") as HTMLImageElement);
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.querySelector("img")).toBeNull();
    expect(avatar.classList.contains("has-pixel")).toBe(false);
    expect(avatar.textContent).toBe("C");
    expect(avatar.style.getPropertyValue("--c")).toBe("var(--blue)");
  });

  it("ignores malformed avatars and old blob rows", () => {
    rememberAvatars([
      { name: "evil", avatar: { kind: "parts", sprite: { palette: { "<": "#fff" } } }, color: "brand" },
      { name: "old", avatar: '<svg viewBox="0 0 100 100"></svg>', color: "brand" },
    ]);
    const { container } = render(
      <>
        <AgentAvatar name="evil" size="md" />
        <AgentAvatar name="old" size="md" />
      </>
    );
    expect(container.querySelector("svg.px-sprite")).toBeNull();
    expect(container.querySelectorAll("img.av-img")).toHaveLength(2);
  });

  it("follows the store without a reload", () => {
    const { container } = render(<AgentAvatar name="lana" size="md" />);
    expect(container.querySelector("svg")).toBeNull();
    act(() => setAgentAvatar("lana", sampleAvatar(), "blue"));
    expect(container.querySelector(".av.has-pixel svg.px-sprite")).not.toBeNull();
    act(() => setAgentAvatar("lana", sampleDrawn(), "#ff6a00", "default"));
    expect((container.querySelector(".av") as HTMLElement).style.getPropertyValue("--pxc")).toBe(
      "#ff6a00"
    );
  });

  it("uses an explicit avatar prop with its tile color", () => {
    const { container } = render(
      <AgentAvatar name="lana" avatar={sampleAvatar()} bodyColor="#9b5cff" size="xl" />
    );
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.style.getPropertyValue("--pxc")).toBe("#9b5cff");
  });

  it("keeps the presence dot class", () => {
    setAgentAvatar("lana", sampleAvatar(), "brand");
    const { container } = render(
      <AgentAvatar name="lana" size="lg" presence className="state-working" />
    );
    const avatar = container.querySelector(".av");
    expect(avatar?.classList.contains("presence")).toBe(true);
    expect(avatar?.classList.contains("state-working")).toBe(true);
  });

  it("renders the sprite in the legacy pixel sizes", () => {
    setAgentAvatar("lana", sampleAvatar(), "#12b5a6");
    const { container } = render(<AgentAvatar name="lana" size="24" />);
    const avatar = container.querySelector(".agent-avatar") as HTMLElement;
    expect(avatar.classList.contains("avatar-24")).toBe(true);
    expect(avatar.classList.contains("has-pixel")).toBe(true);
    expect(avatar.style.getPropertyValue("--pxc")).toBe("#12b5a6");
    expect(avatar.querySelector("svg.px-sprite")).not.toBeNull();
  });

  it("keeps the legacy box with the image, then the letter", () => {
    const { container } = render(<AgentAvatar name="lana" size="20" />);
    const avatar = container.querySelector(".agent-avatar") as HTMLElement;
    expect(avatar.classList.contains("avatar-20")).toBe(true);
    fireEvent.error(avatar.querySelector("img") as HTMLImageElement);
    expect(avatar.className).toMatch(/avatar-20 tone-/);
    expect(avatar.textContent).toBe("L");
  });
});

describe("AgentAvatarStack and AgentFace", () => {
  it("stacks stored sprites next to server images", () => {
    setAgentAvatar("lana", sampleAvatar(), "brand");
    const { container } = render(
      <AgentAvatarStack items={[{ name: "lana" }, { name: "rasya" }, { name: "cody" }]} max={2} />
    );
    const avatars = container.querySelectorAll(".avs > .av");
    expect(avatars[0]?.querySelector("svg.px-sprite")).not.toBeNull();
    expect(avatars[1]?.querySelector("img")?.getAttribute("src")).toContain("/v1/agents/rasya/");
    expect(avatars[2]?.textContent).toBe("+1");
  });

  it("shows the sprite in the presence face and animates it while working", () => {
    setAgentAvatar("lana", sampleAvatar(), "#ff3d9a");
    const { container } = render(
      <AgentFace
        name="lana"
        color="brand"
        size={44}
        mood="tool"
        presence={{ state: "working", label: "Working", since: 0, session_id: "" }}
      />
    );
    const face = container.querySelector(".agent-face") as HTMLElement;
    expect(face.classList.contains("has-pixel")).toBe(true);
    expect(face.style.getPropertyValue("--pxc")).toBe("#ff3d9a");
    expect(face.querySelectorAll("g.px-f")).toHaveLength(4);
    expect(face.querySelector(".px-bubble")).not.toBeNull();
    expect(face.querySelector(".agent-face-dot")).not.toBeNull();
    expect(face.getAttribute("aria-label")).toBe("lana, working");
  });
});

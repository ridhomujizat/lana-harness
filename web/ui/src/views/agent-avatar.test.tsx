import { afterEach, describe, expect, it } from "vitest";
import { act, fireEvent, render } from "@testing-library/react";
import { agentCssColor, parseAvatarSvg } from "../lib/avatar-svg";
import { rememberAvatars, resetAvatarStore, setAgentAvatar } from "../lib/avatar-store";
import { AgentAvatar, AgentAvatarStack } from "./agent-avatar";
import { AgentFace, gr8rColor } from "./presence";

/* Spec 43: generated agent avatars. The SVG is rebuilt from an allowlist,
   colored by the agent color, and anything else falls back to the letter. */

const BLOB =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<path d="M50 12 C 72 12 88 28 88 50 C 88 72 72 88 50 88 C 28 88 12 72 12 50 C 12 28 28 12 50 12 Z" fill="currentColor"/>' +
  '<rect x="36" y="38" width="7" height="16" rx="3.5" fill="#ffffff" transform="rotate(-12 39.5 46)"/>' +
  '<ellipse cx="60" cy="46" rx="4" ry="7" fill="#ffffff"/></svg>';

const BLOB2 =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<circle cx="50" cy="50" r="38" fill="currentColor"/>' +
  '<ellipse cx="40" cy="46" rx="4" ry="7" fill="#ffffff"/>' +
  '<ellipse cx="60" cy="46" rx="4" ry="7" fill="#ffffff"/></svg>';

const WITH_SCRIPT =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<circle cx="50" cy="50" r="40" fill="currentColor"/><script>alert(1)</script></svg>';

const WITH_IMAGE =
  '<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 100 100">' +
  '<image href="https://example.com/x.png" width="100" height="100"/></svg>';

const WITH_HANDLER =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<circle cx="50" cy="50" r="40" fill="currentColor" onclick="alert(1)"/></svg>';

const WITH_URL_FILL =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
  '<circle cx="50" cy="50" r="40" fill="url(#g)"/></svg>';

afterEach(() => {
  resetAvatarStore();
});

describe("parseAvatarSvg", () => {
  it("rebuilds the allowed elements", () => {
    const { container } = render(<span>{parseAvatarSvg(BLOB)}</span>);
    const svg = container.querySelector("svg.av-svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("viewBox")).toBe("0 0 100 100");
    expect(svg?.getAttribute("width")).toBe("100%");
    expect(container.querySelector("path")?.getAttribute("fill")).toBe("currentColor");
    expect(container.querySelector("rect")?.getAttribute("transform")).toBe(
      "rotate(-12 39.5 46)"
    );
    expect(container.querySelector("ellipse")?.getAttribute("fill")).toBe("#ffffff");
  });

  it("rejects scripts, images, handlers, url fills and broken markup", () => {
    expect(parseAvatarSvg(WITH_SCRIPT)).toBeNull();
    expect(parseAvatarSvg(WITH_IMAGE)).toBeNull();
    expect(parseAvatarSvg(WITH_HANDLER)).toBeNull();
    expect(parseAvatarSvg(WITH_URL_FILL)).toBeNull();
    expect(parseAvatarSvg("<svg")).toBeNull();
    expect(parseAvatarSvg("<div>hi</div>")).toBeNull();
    expect(parseAvatarSvg("")).toBeNull();
  });

  it("memoizes per string", () => {
    expect(parseAvatarSvg(BLOB)).toBe(parseAvatarSvg(BLOB));
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
  it("renders the stored svg with a token body color", () => {
    setAgentAvatar("lana", BLOB, "success");
    const { container } = render(
      <AgentAvatar name="lana" color="var(--violet)" size="lg" />
    );
    const avatar = container.querySelector(".av");
    expect(avatar?.classList.contains("has-svg")).toBe(true);
    expect((avatar as HTMLElement).style.getPropertyValue("--c")).toBe("var(--green)");
    expect(avatar?.querySelector("svg.av-svg path")).not.toBeNull();
    expect(avatar?.getAttribute("aria-label")).toBe("lana");
    expect(avatar?.textContent).toBe("");
  });

  it("renders a hex body color", () => {
    setAgentAvatar("rasya", BLOB, "#ff6a00");
    const { container } = render(<AgentAvatar name="rasya" size="md" />);
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.style.getPropertyValue("--c")).toBe("#ff6a00");
    expect(avatar.querySelector("svg")).not.toBeNull();
  });

  it("uses the server image for a name the store has not seen", () => {
    const { container } = render(
      <AgentAvatar name="cody" color="var(--blue)" size="md" />
    );
    const avatar = container.querySelector(".av") as HTMLElement;
    const img = avatar.querySelector("img.av-img") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/v1/agents/cody/avatar.svg?theme=light");
    expect(avatar.classList.contains("has-svg")).toBe(true);
    expect(avatar.getAttribute("aria-label")).toBe("cody");
  });

  it("asks for the dark image in the dark theme", () => {
    document.documentElement.setAttribute("data-theme", "dark");
    try {
      const { container } = render(<AgentAvatar name="cody team" size="md" />);
      expect(container.querySelector("img")?.getAttribute("src")).toBe(
        "/v1/agents/cody%20team/avatar.svg?theme=dark"
      );
    } finally {
      document.documentElement.removeAttribute("data-theme");
    }
  });

  it("falls back to the letter only when the image fails", () => {
    const { container } = render(
      <AgentAvatar name="cody" color="var(--blue)" size="md" />
    );
    fireEvent.error(container.querySelector("img") as HTMLImageElement);
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.querySelector("img")).toBeNull();
    expect(avatar.classList.contains("has-svg")).toBe(false);
    expect(avatar.textContent).toBe("C");
    expect(avatar.style.getPropertyValue("--c")).toBe("var(--blue)");
  });

  it("never renders a bad svg", () => {
    rememberAvatars([
      { name: "evil", avatar_svg: WITH_SCRIPT, color: "brand" },
      { name: "pic", avatar_svg: WITH_IMAGE, color: "brand" },
    ]);
    const { container } = render(
      <>
        <AgentAvatar name="evil" size="md" />
        <AgentAvatar name="pic" size="md" />
      </>
    );
    expect(container.querySelector("svg")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("image")).toBeNull();
    const images = container.querySelectorAll("img.av-img");
    expect(images).toHaveLength(2);
    fireEvent.error(images[0] as HTMLImageElement);
    expect(container.querySelectorAll(".av")[0]?.textContent).toBe("E");
  });

  it("follows the store without a reload", () => {
    const { container } = render(<AgentAvatar name="lana" size="md" />);
    expect(container.querySelector("svg")).toBeNull();
    act(() => setAgentAvatar("lana", BLOB, "blue"));
    expect(container.querySelector(".av.has-svg svg")).not.toBeNull();
    act(() => setAgentAvatar("lana", BLOB2, "#ff6a00", "default"));
    expect(container.querySelector(".av.has-svg svg circle")).not.toBeNull();
    expect((container.querySelector(".av") as HTMLElement).style.getPropertyValue("--c")).toBe(
      "#ff6a00"
    );
  });

  it("uses an explicit svg prop with its body color", () => {
    const { container } = render(
      <AgentAvatar name="lana" svg={BLOB} bodyColor="#9b5cff" size="xl" />
    );
    const avatar = container.querySelector(".av") as HTMLElement;
    expect(avatar.style.getPropertyValue("--c")).toBe("#9b5cff");
  });

  it("keeps the presence dot class", () => {
    setAgentAvatar("lana", BLOB, "brand");
    const { container } = render(
      <AgentAvatar name="lana" size="lg" presence className="state-working" />
    );
    const avatar = container.querySelector(".av");
    expect(avatar?.classList.contains("presence")).toBe(true);
    expect(avatar?.classList.contains("state-working")).toBe(true);
  });

  it("renders the svg in the legacy pixel sizes", () => {
    setAgentAvatar("lana", BLOB, "#12b5a6");
    const { container } = render(<AgentAvatar name="lana" size="24" />);
    const avatar = container.querySelector(".agent-avatar") as HTMLElement;
    expect(avatar.classList.contains("avatar-24")).toBe(true);
    expect(avatar.classList.contains("has-svg")).toBe(true);
    expect(avatar.style.color).toBe("rgb(18, 181, 166)");
    expect(avatar.querySelector("svg")).not.toBeNull();
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
  it("stacks stored svgs next to server images", () => {
    setAgentAvatar("lana", BLOB, "brand");
    const { container } = render(
      <AgentAvatarStack
        items={[{ name: "lana" }, { name: "rasya" }, { name: "cody" }]}
        max={2}
      />
    );
    const avatars = container.querySelectorAll(".avs > .av");
    expect(avatars[0]?.querySelector("svg")).not.toBeNull();
    expect(avatars[1]?.querySelector("img")?.getAttribute("src")).toContain("/v1/agents/rasya/");
    expect(avatars[2]?.textContent).toBe("+1");
  });

  it("shows the svg in the presence face", () => {
    setAgentAvatar("lana", BLOB, "#ff3d9a");
    const { container } = render(
      <AgentFace
        name="lana"
        color="brand"
        presence={{ state: "working", label: "Working", since: 0, session_id: "" }}
      />
    );
    const face = container.querySelector(".agent-face") as HTMLElement;
    expect(face.classList.contains("has-svg")).toBe(true);
    expect(face.querySelector("svg")).not.toBeNull();
    expect(face.querySelector(".agent-face-dot")).not.toBeNull();
    expect(face.getAttribute("aria-label")).toBe("lana, working");
  });
});

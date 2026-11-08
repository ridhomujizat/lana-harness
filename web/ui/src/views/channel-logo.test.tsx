import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { ChannelLogo, isBrandChannel } from "./channel-logo";

describe("ChannelLogo", () => {
  it.each(["telegram", "discord", "whatsapp", "slack"])(
    "renders the vendored %s logo as a tile",
    (channel) => {
      const { container } = render(<ChannelLogo channel={channel} size={40} />);
      const tile = container.querySelector<HTMLElement>(".cl");
      expect(tile).toBeTruthy();
      expect(tile?.dataset.channel).toBe(channel);
      expect(tile?.getAttribute("aria-hidden")).toBe("true");
      expect(tile?.hasAttribute("data-monogram")).toBe(false);
      expect(tile?.classList.contains("cl-sm")).toBe(false);
      const svg = tile?.querySelector("svg");
      expect(svg).toBeTruthy();
      expect(svg?.getAttribute("aria-hidden")).toBe("true");
      expect(svg?.querySelector("path")).toBeTruthy();
      expect(svg?.querySelector("title")).toBeNull();
      expect(tile?.style.getPropertyValue("--cl-size")).toBe("40px");
    }
  );

  it("draws the brand colors and keeps Slack multicolor", () => {
    const { container } = render(
      <>
        <ChannelLogo channel="telegram" />
        <ChannelLogo channel="discord" />
        <ChannelLogo channel="whatsapp" />
        <ChannelLogo channel="slack" />
      </>
    );
    const color = (channel: string) =>
      container
        .querySelector<HTMLElement>('[data-channel="' + channel + '"]')
        ?.style.getPropertyValue("--cl-brand");
    expect(color("telegram")).toBe("#26A5E4");
    expect(color("discord")).toBe("#5865F2");
    expect(color("whatsapp")).toBe("#25D366");
    const slack = container.querySelector('[data-channel="slack"]');
    expect(slack?.classList.contains("cl-multi")).toBe(true);
    expect(slack?.querySelectorAll("path[fill]").length).toBe(4);
  });

  it("draws only the Telegram plane on the tile and the full mark when small", () => {
    const { container } = render(
      <>
        <ChannelLogo channel="telegram" size={40} />
        <ChannelLogo channel="telegram" size={14} />
      </>
    );
    const [tile, small] = Array.from(
      container.querySelectorAll<HTMLElement>('[data-channel="telegram"]')
    );
    expect(tile?.querySelector("path")?.getAttribute("d")).toMatch(/^M16\.906 7\.224/);
    expect(small?.classList.contains("cl-sm")).toBe(true);
    expect(small?.querySelector("path")?.getAttribute("d")).toMatch(/^M11\.944 0A/);
  });

  it("matches the channel name without regard to case", () => {
    const { container } = render(<ChannelLogo channel="Discord" />);
    expect(container.querySelector('[data-channel="discord"] svg')).toBeTruthy();
    expect(isBrandChannel("WhatsApp")).toBe(true);
    expect(isBrandChannel("web")).toBe(false);
    expect(isBrandChannel("")).toBe(false);
  });

  it("falls back to a monogram tile for other channels", () => {
    const { container } = render(<ChannelLogo channel="matrix" size={28} />);
    const tile = container.querySelector<HTMLElement>(".cl");
    expect(tile?.hasAttribute("data-monogram")).toBe(true);
    expect(tile?.classList.contains("cl-mono")).toBe(true);
    expect(tile?.querySelector("svg")).toBeNull();
    expect(tile?.textContent).toBe("MA");
  });

  it("uses the label for the monogram when one is given", () => {
    const { container } = render(<ChannelLogo channel="" label="Signal app" />);
    expect(container.querySelector(".cl-mono")?.textContent).toBe("SA");
  });
});

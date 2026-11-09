import { describe, expect, it } from "vitest";
import { slugifyAgentName } from "./agent-label";

/* The ID falls out of the display name, so "Omnix Lead" becomes a valid
   config key (AGENT_ID_RULE) with no stray characters. */
describe("slugifyAgentName", () => {
  it("turns spaces into dashes and lowercases the words", () => {
    expect(slugifyAgentName("Omnix Lead")).toBe("omnix-lead");
  });

  it("drops leading characters that are not letters", () => {
    expect(slugifyAgentName("2 Bots")).toBe("bots");
  });

  it("strips the accents of a decomposed name", () => {
    expect(slugifyAgentName("Café Ops")).toBe("cafe-ops");
  });

  it("returns an empty string when nothing usable remains", () => {
    expect(slugifyAgentName("!!!")).toBe("");
  });
});

import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { HomePage } from "./home";

describe("home empty state", () => {
  it("shows the empty-state copy and a link to sessions", () => {
    render(<HomePage />);
    expect(screen.getByText("Home is coming soon")).toBeTruthy();
    const link = screen.getByRole("link", { name: "Go to sessions" });
    expect(link.getAttribute("href")).toBe("#/sessions");
    expect(screen.getByText(/Stats and recent activity will live here/)).toBeTruthy();
  });
});

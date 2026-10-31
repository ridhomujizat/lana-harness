import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { SetupShell, SetupWizard } from "./setup";

/* Step navigation and validation keep the behavior of views/setup.js. The
   wizard reads its draft once per page, so each test seeds sessionStorage
   before the first render of the module. */

const DRAFT_KEY = "lana.setup";

const EMPTY_CONNECT_DRAFT = {
  provider: "openai",
  connect: { apiKey: "", baseUrl: "", model: "" },
  agent: {
    name: "default",
    template: "assistant",
    workspace: "~/lana/workspace",
    permission: "ask",
  },
  persona: { agentMd: "", userMd: "" },
};

beforeEach(() => {
  window.sessionStorage.clear();
  window.location.hash = "";
});

describe("setup wizard", () => {
  it("shows the connect validation messages without testing the provider", () => {
    window.sessionStorage.setItem(DRAFT_KEY, JSON.stringify(EMPTY_CONNECT_DRAFT));
    render(<SetupWizard step="connect" />);

    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));

    expect(screen.getByText("Enter the API key.")).toBeTruthy();
    expect(screen.getByText("Enter the base URL.")).toBeTruthy();
    expect(screen.getByText("Enter the default model.")).toBeTruthy();
    const save = screen.getByRole("button", { name: "Save and continue" });
    expect((save as HTMLButtonElement).disabled).toBe(true);
  });

  it("selects a provider and navigates to the connect step", () => {
    render(<SetupWizard step="provider" />);

    const anthropic = screen.getByRole("radio", { name: /Anthropic/ });
    fireEvent.click(anthropic);
    expect((anthropic as HTMLInputElement).checked).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(window.location.hash).toBe("#/setup/connect");
  });

  it("shows the agent validation messages and stays on the step", () => {
    render(<SetupWizard step="agent" />);

    const name = screen.getByLabelText("Agent name");
    const workspace = screen.getByLabelText("Workspace");
    fireEvent.change(name, { target: { value: "Bad Name" } });
    fireEvent.change(workspace, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(
      screen.getByText("Use lowercase letters, numbers, and dashes only.")
    ).toBeTruthy();
    expect(screen.getByText("Enter a workspace path.")).toBeTruthy();
    expect(window.location.hash).toBe("");
  });
});

describe("setup shell", () => {
  it("renders the door mark behind the onboarding steps", () => {
    const { container } = render(
      <SetupShell step="provider">
        <p>Connect a provider.</p>
      </SetupShell>
    );

    const mark = container.querySelector(".door-mark");
    expect(mark).not.toBeNull();
    expect(mark?.getAttribute("aria-hidden")).toBe("true");
    expect(mark?.querySelector(".door-right")).not.toBeNull();
    expect(container.querySelector(".setup-steps")).not.toBeNull();
    // The mark sits before the rail content so the steps and logo paint over it.
    expect(mark?.nextElementSibling?.classList.contains("setup-rail-top")).toBe(
      true
    );
  });

  it("keeps the door mark on the door variant", () => {
    const { container } = render(
      <SetupShell step="unlock" variant="door">
        <p>Unlock lana.</p>
      </SetupShell>
    );

    expect(container.querySelector(".door-mark")).not.toBeNull();
    expect(container.querySelector(".setup-steps")).toBeNull();
  });
});

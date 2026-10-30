import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { authState, claim } from "../api/auth";
import { WelcomePage } from "./welcome";

/* The welcome view shares PasswordField with the login view, so the mock also
   carries the login export that module reads. */
vi.mock("../api/auth", () => ({
  authState: vi.fn(),
  claim: vi.fn(),
  login: vi.fn(),
}));

const mockedAuthState = vi.mocked(authState);
const mockedClaim = vi.mocked(claim);

afterEach(() => {
  vi.clearAllMocks();
});

describe("welcome page", () => {
  it("claims the code with the chosen password and signs in", async () => {
    mockedAuthState.mockResolvedValue({
      password_set: false,
      telegram_reset: false,
      locked_until: null,
    });
    mockedClaim.mockResolvedValue({ csrf: "csrf-value" });
    const onLoggedIn = vi.fn();
    render(<WelcomePage code="sign-in-code" onLoggedIn={onLoggedIn} />);

    fireEvent.change(await screen.findByLabelText("Password"), {
      target: { value: "correcthorse" },
    });
    fireEvent.change(screen.getByLabelText("Type it again"), {
      target: { value: "correcthorse" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create password" }));

    await waitFor(() => {
      expect(mockedClaim).toHaveBeenCalledWith("sign-in-code", "correcthorse");
    });
    expect(onLoggedIn).toHaveBeenCalledWith("csrf-value");
  });

  it("shows the expired screen without a form when the link has no code", async () => {
    render(<WelcomePage onLoggedIn={vi.fn()} />);

    expect(await screen.findByText("This link has expired")).toBeTruthy();
    expect(screen.getByText("Restart lana to get a new one.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Create password" })).toBeNull();
  });
});

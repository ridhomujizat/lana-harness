import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { authState, resetTelegram, resetVerify } from "../api/auth";
import { ForgotPage } from "./forgot";

/* The forgot view shares PasswordField with the login view, so the mock also
   carries the login export that module reads. */
vi.mock("../api/auth", () => ({
  authState: vi.fn(),
  login: vi.fn(),
  resetTelegram: vi.fn(),
  resetVerify: vi.fn(),
  resetRestart: vi.fn(),
}));

const mockedAuthState = vi.mocked(authState);
const mockedResetTelegram = vi.mocked(resetTelegram);
const mockedResetVerify = vi.mocked(resetVerify);

afterEach(() => {
  vi.clearAllMocks();
});

describe("forgot page", () => {
  it("hides the Telegram row when Telegram reset is unavailable", async () => {
    mockedAuthState.mockResolvedValue({
      password_set: true,
      telegram_reset: false,
      locked_until: null,
    });
    render(<ForgotPage onLoggedIn={vi.fn()} />);

    expect(await screen.findByText("Reset your password")).toBeTruthy();
    expect(screen.queryByText("Send a code to Telegram")).toBeNull();
    expect(
      screen.getByText("Restart lana to get a sign-in link")
    ).toBeTruthy();
  });

  it("verifies the code and new password from the code screen", async () => {
    mockedAuthState.mockResolvedValue({
      password_set: true,
      telegram_reset: true,
      locked_until: null,
    });
    mockedResetTelegram.mockResolvedValue({ sent_to: 1 });
    mockedResetVerify.mockResolvedValue({ csrf: "csrf-value" });
    const onLoggedIn = vi.fn();
    render(<ForgotPage onLoggedIn={onLoggedIn} />);

    fireEvent.click(await screen.findByRole("button", { name: "Send code" }));

    fireEvent.change(await screen.findByLabelText("Code"), {
      target: { value: "482915" },
    });
    fireEvent.change(screen.getByLabelText("New password"), {
      target: { value: "newpassword" },
    });
    fireEvent.change(screen.getByLabelText("Type it again"), {
      target: { value: "newpassword" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Set new password" }));

    await waitFor(() => {
      expect(mockedResetVerify).toHaveBeenCalledWith("482915", "newpassword");
    });
    expect(onLoggedIn).toHaveBeenCalledWith("csrf-value");
  });
});

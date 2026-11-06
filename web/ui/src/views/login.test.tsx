import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ApiError } from "../api/client";
import { authState, login } from "../api/auth";
import { LoginPage, PasswordField } from "./login";

/* The login view reads the auth state, then posts the password. Mocking the
   api/auth module keeps the tests off the network. */
vi.mock("../api/auth", () => ({
  authState: vi.fn(),
  login: vi.fn(),
}));

const mockedAuthState = vi.mocked(authState);
const mockedLogin = vi.mocked(login);

function passwordSetState() {
  return {
    password_set: true,
    telegram_reset: false,
    locked_until: null,
  };
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("login page", () => {
  it("shows the invalid password message on 401", async () => {
    mockedAuthState.mockResolvedValue(passwordSetState());
    mockedLogin.mockRejectedValue(
      new ApiError(401, "invalid_password", "nope")
    );
    render(<LoginPage onLoggedIn={vi.fn()} />);

    fireEvent.change(await screen.findByLabelText("Password"), {
      target: { value: "wrong-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Log in" }));

    expect(await screen.findByText("That password is not right.")).toBeTruthy();
  });

  it("shows the too many tries message on 429", async () => {
    mockedAuthState.mockResolvedValue(passwordSetState());
    mockedLogin.mockRejectedValue(
      new ApiError(429, "too_many_attempts", "slow down")
    );
    render(<LoginPage onLoggedIn={vi.fn()} />);

    fireEvent.change(await screen.findByLabelText("Password"), {
      target: { value: "wrong-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Log in" }));

    expect(
      await screen.findByText(
        "Too many tries. Wait 5 minutes or reset your password."
      )
    ).toBeTruthy();
  });

  it("shows the setup prompt instead of the form while no password is set", async () => {
    mockedAuthState.mockResolvedValue({
      password_set: false,
      telegram_reset: false,
      locked_until: null,
    });
    render(<LoginPage onLoggedIn={vi.fn()} />);

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Set up your password",
      })
    ).toBeTruthy();
    expect(
      screen.getByText(
        "lana opened a sign-in link in a new browser tab when it started. Switch to that tab, or restart lana to open a new one."
      )
    ).toBeTruthy();
    expect(screen.queryByLabelText("Password")).toBeNull();
    expect(
      screen.queryByRole("link", { name: "Reset your password" })
    ).toBeNull();
  });
});

describe("password field", () => {
  it("toggles the input type and the toggle label", () => {
    render(
      <PasswordField
        id="pw"
        label="Password"
        value=""
        autoComplete="current-password"
        onChange={vi.fn()}
      />
    );

    const input = screen.getByLabelText<HTMLInputElement>("Password");
    expect(input.type).toBe("password");

    const show = screen.getByRole("button", { name: "Show password" });
    expect(show.getAttribute("aria-pressed")).toBe("false");

    fireEvent.click(show);

    expect(input.type).toBe("text");
    const hide = screen.getByRole("button", { name: "Hide password" });
    expect(hide.getAttribute("aria-pressed")).toBe("true");

    fireEvent.click(hide);

    expect(input.type).toBe("password");
    expect(
      screen
        .getByRole("button", { name: "Show password" })
        .getAttribute("aria-pressed")
    ).toBe("false");
  });
});

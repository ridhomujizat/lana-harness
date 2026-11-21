import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

/* Spec 63 REQ-HLF-007: the web step-up approval card. The code API is mocked;
   the answer is observed through the onAnswer prop. */
vi.mock("../api/approvals", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/approvals")>();
  return { ...actual, sendApprovalCode: vi.fn() };
});

import { sendApprovalCode } from "../api/approvals";
import { ApprovalCard } from "./approval";
import type { ApprovalItem } from "./approval";

const LONG_ARGUMENT =
  "rm -rf /System/Library/Frameworks/SomeVeryLongDirectoryName/Contents";

function item(overrides: Partial<ApprovalItem> = {}): ApprovalItem {
  return {
    request_id: "a1",
    tool: "bash",
    argument: "rm -rf ~",
    risk: "high",
    expires_at: Date.now() + 60_000,
    ...overrides,
  };
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("ApprovalCard hardline", () => {
  it("block card hides allow-for-session, disables Allow until a password is typed, and sends it", async () => {
    const onAnswer = vi.fn().mockResolvedValue({ ok: true });
    render(
      <ApprovalCard
        approval={item({
          argument: LONG_ARGUMENT,
          hardline: {
            rule: "HL-B-001",
            tier: "block",
            reason: "recursive delete of a system directory",
            step_up: "password",
          },
        })}
        onAnswer={onAnswer}
      />
    );

    expect(
      screen.queryByRole("button", { name: "Allow for this session" })
    ).toBeNull();
    expect(screen.getByText("HL-B-001")).toBeTruthy();
    expect(screen.getByText("recursive delete of a system directory")).toBeTruthy();
    /* The full argument shows, not a shortened one. */
    expect(screen.getByText("$ " + LONG_ARGUMENT)).toBeTruthy();

    const password = screen.getByLabelText("Lana password") as HTMLInputElement;
    expect(password.type).toBe("password");
    expect(password.autocomplete).toBe("current-password");

    const allow = screen.getByRole("button", {
      name: "Allow once",
    }) as HTMLButtonElement;
    expect(allow.disabled).toBe(true);

    fireEvent.change(password, { target: { value: "hunter2" } });
    expect(allow.disabled).toBe(false);
    fireEvent.click(allow);

    await waitFor(() =>
      expect(onAnswer).toHaveBeenCalledWith(
        expect.objectContaining({ request_id: "a1" }),
        "allowed_once",
        { password: "hunter2" }
      )
    );
  });

  it("shows the wrong-password error inline, keeps the card, and clears the field", async () => {
    const onAnswer = vi
      .fn()
      .mockResolvedValue({ ok: false, code: "invalid_password" });
    render(
      <ApprovalCard
        approval={item({
          hardline: {
            rule: "HL-B-004",
            tier: "block",
            reason: "write to a protected path",
            step_up: "password",
          },
        })}
        onAnswer={onAnswer}
      />
    );

    const password = screen.getByLabelText("Lana password") as HTMLInputElement;
    fireEvent.change(password, { target: { value: "nope" } });
    fireEvent.click(screen.getByRole("button", { name: "Allow once" }));

    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toBe("Wrong password.")
    );
    expect(password.value).toBe("");
    /* The card stays open. */
    expect(screen.getByRole("button", { name: "Allow once" })).toBeTruthy();
    expect(screen.getByLabelText("Lana password")).toBeTruthy();
  });

  it("password_or_telegram sends a Telegram code and submits the code", async () => {
    vi.mocked(sendApprovalCode).mockResolvedValue({ sent_to: 1 });
    const onAnswer = vi.fn().mockResolvedValue({ ok: true });
    render(
      <ApprovalCard
        approval={item({
          hardline: {
            rule: "HL-B-002",
            tier: "block",
            reason: "disk destruction",
            step_up: "password_or_telegram",
          },
        })}
        onAnswer={onAnswer}
      />
    );

    fireEvent.click(screen.getByRole("button", { name: "Send Telegram code" }));
    await waitFor(() => expect(sendApprovalCode).toHaveBeenCalledWith("a1"));
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toBe("Code sent to Telegram.")
    );
    /* A second send is disabled during the cooldown. */
    expect(
      (screen.getByRole("button", { name: /Send Telegram code/ }) as HTMLButtonElement)
        .disabled
    ).toBe(true);

    const code = screen.getByLabelText("Telegram code") as HTMLInputElement;
    expect(code.getAttribute("inputmode")).toBe("numeric");
    fireEvent.change(code, { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Allow once" }));

    await waitFor(() =>
      expect(onAnswer).toHaveBeenCalledWith(
        expect.objectContaining({ request_id: "a1" }),
        "allowed_once",
        { code: "123456" }
      )
    );
  });

  it("sends the password when both the password and the code are filled", async () => {
    vi.mocked(sendApprovalCode).mockResolvedValue({ sent_to: 1 });
    const onAnswer = vi.fn().mockResolvedValue({ ok: true });
    render(
      <ApprovalCard
        approval={item({
          hardline: {
            rule: "HL-B-002",
            tier: "block",
            reason: "disk destruction",
            step_up: "password_or_telegram",
          },
        })}
        onAnswer={onAnswer}
      />
    );

    fireEvent.change(screen.getByLabelText("Lana password"), {
      target: { value: "hunter2" },
    });
    fireEvent.change(screen.getByLabelText("Telegram code"), {
      target: { value: "123456" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Allow once" }));

    await waitFor(() =>
      expect(onAnswer).toHaveBeenCalledWith(
        expect.objectContaining({ request_id: "a1" }),
        "allowed_once",
        { password: "hunter2" }
      )
    );
  });

  it("escalate card has no password field and no allow-for-session", () => {
    render(
      <ApprovalCard
        approval={item({
          hardline: {
            rule: "HL-E-002",
            tier: "escalate",
            reason: "privilege change",
            step_up: "",
          },
        })}
        onAnswer={vi.fn()}
      />
    );

    expect(screen.queryByLabelText("Lana password")).toBeNull();
    expect(screen.queryByLabelText("Telegram code")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Allow for this session" })
    ).toBeNull();
    expect(screen.getByText("HL-E-002")).toBeTruthy();
    const allow = screen.getByRole("button", {
      name: "Allow once",
    }) as HTMLButtonElement;
    expect(allow.disabled).toBe(false);
  });

  it("leaves a non-hardline card unchanged", () => {
    render(<ApprovalCard approval={item()} workspace="/tmp/work" onAnswer={vi.fn()} />);

    expect(
      screen.getByRole("button", { name: "Allow for this session" })
    ).toBeTruthy();
    expect(screen.queryByLabelText("Lana password")).toBeNull();
    expect(screen.getByText("$ rm -rf ~")).toBeTruthy();
  });
});

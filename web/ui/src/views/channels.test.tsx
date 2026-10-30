import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChannelsPage, TelegramPage } from "./channels";
import {
  discoverTelegram,
  listAgents,
  listChannels,
  saveTelegram,
} from "../api/channels";
import type { TelegramChannelCard } from "../api/channels";

vi.mock("../api/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/channels")>();
  return {
    ...actual,
    listChannels: vi.fn(),
    listAgents: vi.fn(),
    readConfigVersion: vi.fn(),
    saveTelegram: vi.fn(),
    checkTelegram: vi.fn(),
    listWaitingChats: vi.fn(),
    saveTelegramRoute: vi.fn(),
    deleteTelegramRoute: vi.fn(),
    putTelegramToken: vi.fn(),
    discoverTelegram: vi.fn(),
    connectTelegram: vi.fn(),
  };
});

const telegramCard: TelegramChannelCard = {
  name: "telegram",
  status: "connected",
  state: "connected",
  enabled: true,
  token_env: "TELEGRAM_BOT_TOKEN",
  token_present: true,
  bot_username: "lana_bot",
  bot_url: "",
  chats: [{ chat_id: 42, name: "Ada", type: "private", agent: "coder" }],
  owners: [{ id: 7, name: "Ada" }],
  chat_count: 1,
  owner_count: 1,
  polling_since: null,
  last_message_at: null,
};

describe("channels views", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(listChannels).mockResolvedValue({
      channels: [
        telegramCard,
        { name: "web", status: "on", addr: "127.0.0.1:18300" },
      ],
      version: "v1",
    });
    vi.mocked(listAgents).mockResolvedValue({ agents: [] });
  });

  it("renders the channel cards with the daemon web address", async () => {
    render(<ChannelsPage />);
    expect(await screen.findByText("Telegram")).toBeTruthy();
    expect(screen.getByText("This dashboard · 127.0.0.1:18300")).toBeTruthy();
    expect(screen.getByText("Connected")).toBeTruthy();
    expect(screen.getByText("On")).toBeTruthy();
  });

  it("blocks a bot token pasted into the Advanced token variable", async () => {
    const user = userEvent.setup();
    const { container } = render(<TelegramPage />);
    await screen.findByRole("heading", { name: "Telegram" });

    const input = screen.getByLabelText("Token variable");
    fireEvent.change(input, {
      target: { value: "123456:abcdefghijklmnopqrstuvwxyz012345" },
    });
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(
      await screen.findByText(
        "This looks like the bot token itself. Paste it in Bot token above instead."
      )
    ).toBeTruthy();
    expect(vi.mocked(saveTelegram)).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(
        container.querySelector<HTMLDetailsElement>("details.channel-advanced")
          ?.open
      ).toBe(true);
    });
  });

  it("rechecks the chat list every 3 s and stops on unmount", async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(discoverTelegram).mockResolvedValue({
        bot_username: "lana_bot",
        bot_url: "",
        chats: [],
      });
      const { unmount } = render(<TelegramPage />);
      await act(async () => {});
      expect(screen.getByRole("heading", { name: "Telegram" })).toBeTruthy();

      fireEvent.click(screen.getByRole("button", { name: "Show steps" }));
      await act(async () => {});
      fireEvent.click(screen.getByRole("button", { name: "Find my chat" }));
      await act(async () => {});
      expect(vi.mocked(discoverTelegram)).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(vi.mocked(discoverTelegram)).toHaveBeenCalledTimes(2);

      unmount();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(vi.mocked(discoverTelegram)).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

/* eslint-disable react-hooks/set-state-in-effect -- the old Preact views loaded
   data on mount and synced form state in effects; this port keeps that shape. */
import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { ApiError, errorMessage } from "../api/client";
import { avatarFor } from "../lib/avatar";
import {
  checkTelegram,
  connectTelegram,
  deleteTelegramRoute,
  discoverTelegram,
  listAgents,
  listChannels,
  listWaitingChats,
  putTelegramToken,
  readConfigVersion,
  saveTelegram,
  saveTelegramRoute,
} from "../api/channels";
import type {
  ChannelCard,
  ChannelChat,
  ChannelOwner,
  SimpleChannelCard,
  TelegramChannelCard,
  TelegramSaveBody,
  TelegramSeenChat,
  WaitingChat,
} from "../api/channels";
import type { Agent } from "../api/agents";
import {
  getDaemon,
  restartDaemon,
  waitForDaemonRestart,
} from "../api/settings";
import {
  Dialog,
  ErrorNotice,
  Icon,
  Loading,
  Notice,
  useAnnounce,
} from "../ui/ui";
import { AgentAvatar, agoLabel } from "./agent-common";
import { SettingsRestartDialog } from "./settings-pages";

/* REQ-WEB-052: the channels list and the Telegram detail. REQ-WEB-053: the
   add-chat dialog with the chats that wrote to the bot without a route.
   The token value never reaches this file: the API only returns token_env and
   token_present. */

const RESTART_NOTE = "Saved. Restart the daemon for the change to apply.";
const DEFAULT_TOKEN_ENV = "TELEGRAM_BOT_TOKEN";
const TOKEN_ENV_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const BOT_TOKEN_RE = /^[0-9]{5,}:[A-Za-z0-9_-]{30,}$/;
const DISCOVER_POLL_MS = 3000;
const DISCOVER_WINDOW_MS = 120000;
const CHANNEL_POLL_MS = 1000;
const CHANNEL_POLL_LIMIT_MS = 30000;

const TELEGRAM_CONFLICT_TEXT =
  "Telegram is already connected elsewhere. Stop the other bot program or remove its webhook, then try again.";
const STALE_VERSION_TEXT =
  "Settings changed in another tab. Check them and try again.";
const BOT_TOKEN_MISPLACED_TEXT =
  "This looks like the bot token itself. Paste it in Bot token above instead.";
const TOKEN_ENV_INVALID_TEXT =
  "The token variable must use letters, digits, and underscores, and must not start with a digit.";
const TOKEN_EMPTY_TEXT = "Paste the token from @BotFather first.";
const CONFIG_VERSION_MISSING_TEXT =
  "The config version is unavailable. Reload the page and try again.";

const COMING_SOON = [
  {
    name: "discord",
    label: "Discord",
    letter: "D",
    description: "Talk to agents in a server or DM.",
  },
  {
    name: "slack",
    label: "Slack",
    letter: "S",
    description: "Mention an agent in a channel.",
  },
  {
    name: "whatsapp",
    label: "WhatsApp",
    letter: "W",
    description: "Chat with an agent from your phone.",
  },
];

function Badge({
  label,
  className = "badge-ui",
}: {
  label: string;
  className?: string;
}) {
  return (
    <span className={className}>
      <span className="badge-ui-dot" aria-hidden="true"></span>
      {label}
    </span>
  );
}

function InitialAvatar({ name, small = false }: { name: string; small?: boolean }) {
  return (
    <span
      className={"channel-avatar" + (small ? " channel-avatar-sm" : "")}
      aria-hidden="true"
    >
      {avatarFor(name).initial}
    </span>
  );
}

function TelegramMark() {
  return (
    <svg
      className="channel-tile-mark"
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M16.906 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z"
        fill="currentColor"
      ></path>
    </svg>
  );
}

function ChannelTile({
  children,
  telegram = false,
  large = false,
}: {
  children?: ReactNode;
  telegram?: boolean;
  large?: boolean;
}) {
  let className = "channel-tile";
  if (telegram) {
    className += " channel-tile-telegram";
  }
  if (large) {
    className += " channel-tile-lg";
  }
  return (
    <span className={className} aria-hidden="true">
      {children}
    </span>
  );
}

function telegramStatus(status: string | undefined): {
  label: string;
  className: string;
} {
  if (status === "connected") {
    return {
      label: "Connected",
      className: "badge-ui channel-badge-success",
    };
  }
  if (status === "token_missing") {
    return { label: "Token missing", className: "badge-ui badge-ui-warning" };
  }
  return { label: "Off", className: "badge-ui" };
}

function chatTypeLabel(type: string): string {
  return type === "group" ? "Group" : "Private";
}

function countText(count: unknown, singular: string, plural: string): string {
  const value = Number(count);
  const safe = Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0;
  return safe + " " + (safe === 1 ? singular : plural);
}

function telegramMeta(telegram: TelegramChannelCard): string {
  const parts: string[] = [];
  if (telegram.bot_username) {
    parts.push("@" + telegram.bot_username);
  }
  const since = Number(telegram.polling_since);
  if (Number.isFinite(since) && since > 0) {
    const hours = Math.floor((Date.now() - since) / 3600000);
    parts.push(
      hours < 1 ? "polling for less than 1 h" : "polling for " + hours + " h"
    );
  } else {
    parts.push("not polling");
  }
  const lastMessage = Number(telegram.last_message_at);
  if (Number.isFinite(lastMessage) && lastMessage > 0) {
    const relative = agoLabel(lastMessage);
    if (relative) {
      parts.push("last message " + relative);
    }
  }
  return parts.join(" · ");
}

function TelegramBreadcrumb() {
  return (
    <nav className="agent-breadcrumb" aria-label="Breadcrumb">
      <ol className="channel-crumbs">
        <li>
          <a href="#/channels">Channels</a>
        </li>
        <li aria-current="page">Telegram</li>
      </ol>
    </nav>
  );
}

function parseChatId(value: unknown): number | null {
  const input = String(value || "").trim();
  if (!/^-?[0-9]+$/.test(input)) {
    return null;
  }
  const number = Number(input);
  if (!Number.isSafeInteger(number) || number === 0) {
    return null;
  }
  return number;
}

/* "not_configured" and "off" mean the channel cannot answer yet. */
function telegramIsOn(status: string | undefined): boolean {
  return status !== "off" && status !== "not_configured";
}

function connectedText(bot: string, agent: string): string {
  const name = bot ? "@" + bot : "your bot";
  if (agent) {
    return "Telegram is on. Send a message to " + name + " to talk to " + agent + ".";
  }
  return "Telegram is on. Send a message to " + name + ".";
}

function routedAgent(
  telegram: TelegramChannelCard | null,
  chatId: number | null
): string {
  if (!telegram || !Array.isArray(telegram.chats)) {
    return "";
  }
  if (chatId !== null && chatId !== undefined) {
    const match = telegram.chats.find(
      (chat) => chat && Number(chat.chat_id) === Number(chatId)
    );
    if (match && match.agent) {
      return match.agent;
    }
  }
  const routed = telegram.chats.find((chat) => chat && chat.agent);
  return routed ? routed.agent : "";
}

function discoverEmptyText(bot: string): string {
  return (
    "No messages yet. Send any message to " +
    (bot ? "@" + bot : "your bot") +
    ", then try again."
  );
}

function isTelegramCard(card: ChannelCard): card is TelegramChannelCard {
  return card.name === "telegram";
}

function isWebCard(card: ChannelCard): card is SimpleChannelCard {
  return card.name === "web";
}

export function ChannelsPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [channels, setChannels] = useState<ChannelCard[]>([]);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    listChannels()
      .then((page) => {
        if (!alive) {
          return;
        }
        setChannels(page.channels);
        setStatus("ready");
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        announce("Could not load channels. " + errorMessage(failure));
      });
    return () => {
      alive = false;
    };
  }, [reloadToken, announce]);

  const cards = new Map<string, ChannelCard>();
  for (const card of channels) {
    if (card && card.name) {
      cards.set(card.name, card);
    }
  }
  const telegramCard = cards.get("telegram");
  const telegram =
    telegramCard && isTelegramCard(telegramCard) ? telegramCard : null;
  const webCard = cards.get("web");
  const web = webCard && isWebCard(webCard) ? webCard : null;
  const telegramState = telegramStatus(telegram?.status);
  const chatCount = telegram
    ? Number.isFinite(Number(telegram.chat_count))
      ? Number(telegram.chat_count)
      : Array.isArray(telegram.chats)
        ? telegram.chats.length
        : 0
    : 0;
  const ownerCount = telegram
    ? Number.isFinite(Number(telegram.owner_count))
      ? Number(telegram.owner_count)
      : Array.isArray(telegram.owners)
        ? telegram.owners.length
        : 0
    : 0;
  const telegramMetaParts: string[] = [];
  if (telegram?.bot_username) {
    telegramMetaParts.push("@" + telegram.bot_username);
  }
  telegramMetaParts.push(countText(chatCount, "chat", "chats"));
  telegramMetaParts.push(countText(ownerCount, "owner", "owners"));

  return (
    <main id="main" className="page channels-page">
      <header className="channels-header">
        <h1 className="agents-title">Channels</h1>
        <p className="agents-subtitle">
          Where people reach your agents. Each chat goes to one agent.
        </p>
      </header>

      {status === "loading" ? (
        <div className="channels-state" role="status">
          <Loading label="Loading channels…" />
        </div>
      ) : null}

      {status === "error" ? (
        <div className="channels-state" role="alert">
          <ErrorNotice message={error} details={details} />
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={() => setReloadToken((current) => current + 1)}
          >
            Try again
          </button>
        </div>
      ) : null}

      {status === "ready" && channels.length === 0 ? (
        <div className="channels-empty">
          <h2 className="channels-empty-title">No channels yet</h2>
          <p className="channels-empty-text">
            Channels appear here when the daemon has Telegram, the web view, or
            the CLI available.
          </p>
        </div>
      ) : null}

      {status === "ready" && channels.length > 0 ? (
        <ul className="channels-list">
          <li className="channel-card">
            <ChannelTile telegram>
              <TelegramMark />
            </ChannelTile>
            <div className="channel-card-main">
              <div className="channel-card-name-row">
                <h2 className="channel-card-name">Telegram</h2>
                <Badge
                  label={telegramState.label}
                  className={telegramState.className}
                />
              </div>
              <p className="channel-card-meta">{telegramMetaParts.join(" · ")}</p>
            </div>
            <div className="channel-card-action">
              <a
                className="button-ui button-ui-secondary"
                href="#/channels/telegram"
                aria-label="Manage Telegram"
              >
                Manage
              </a>
            </div>
          </li>

          <li className="channel-card">
            <ChannelTile>
              <Icon name="globe" />
            </ChannelTile>
            <div className="channel-card-main">
              <div className="channel-card-name-row">
                <h2 className="channel-card-name">Web</h2>
                <Badge
                  label={web?.status === "on" ? "On" : "Off"}
                  className={
                    web?.status === "on"
                      ? "badge-ui channel-badge-success"
                      : "badge-ui"
                  }
                />
              </div>
              <p className="channel-card-meta">
                {web?.addr ? "This dashboard · " + web.addr : "This dashboard"}
              </p>
            </div>
            <div className="channel-card-action">
              <a
                className="button-ui button-ui-secondary"
                href="#/settings/general"
                aria-label="Web settings"
              >
                Settings
              </a>
            </div>
          </li>

          <li className="channel-card">
            <ChannelTile>
              <Icon name="terminal" />
            </ChannelTile>
            <div className="channel-card-main">
              <div className="channel-card-name-row">
                <h2 className="channel-card-name">CLI</h2>
                <Badge label="Built in" />
              </div>
              <p className="channel-card-meta">lana chat in your terminal</p>
            </div>
          </li>

          {COMING_SOON.map((channel) => (
            <li className="channel-card is-muted" key={channel.name}>
              <ChannelTile>{channel.letter}</ChannelTile>
              <div className="channel-card-main">
                <div className="channel-card-name-row">
                  <h2 className="channel-card-name">{channel.label}</h2>
                  <Badge label="Coming soon" />
                </div>
                <p className="channel-card-meta">{channel.description}</p>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </main>
  );
}

export function TelegramPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [telegram, setTelegram] = useState<TelegramChannelCard | null>(null);
  const [version, setVersion] = useState("");
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loadError, setLoadError] = useState("");
  const [loadDetails, setLoadDetails] = useState<unknown[]>([]);
  const [saveError, setSaveError] = useState("");
  const [saveDetails, setSaveDetails] = useState<unknown[]>([]);
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [tokenEnv, setTokenEnv] = useState(DEFAULT_TOKEN_ENV);
  const [apiBase, setApiBase] = useState("");
  const [checkResult, setCheckResult] = useState<{
    tone: string;
    text: string;
  } | null>(null);
  const [checking, setChecking] = useState(false);
  const [showAddChat, setShowAddChat] = useState(false);
  const [waiting, setWaiting] = useState<WaitingChat[]>([]);
  const [waitingError, setWaitingError] = useState("");
  const [openingChat, setOpeningChat] = useState(false);
  const openingChatRef = useRef(false);
  const [ownerFormOpen, setOwnerFormOpen] = useState(false);
  const [ownerDraft, setOwnerDraft] = useState("");
  const [ownerError, setOwnerError] = useState("");

  /* Step 1: the token field is local state and is cleared after a save. */
  const [tokenInput, setTokenInput] = useState("");
  const [savingToken, setSavingToken] = useState(false);
  const [tokenError, setTokenError] = useState("");
  const [tokenBot, setTokenBot] = useState("");
  const [savedBotURL, setSavedBotURL] = useState("");
  const [replaceToken, setReplaceToken] = useState(false);
  const [tokenEnvError, setTokenEnvError] = useState("");

  /* Step 2: seen chats and the 3 s re-check for up to 2 minutes. */
  const [discoveredChats, setDiscoveredChats] = useState<TelegramSeenChat[]>([]);
  const [discoverPhase, setDiscoverPhase] = useState<
    "idle" | "loading" | "found" | "empty" | "error"
  >("idle");
  const [discovering, setDiscovering] = useState(false);
  const [discoverError, setDiscoverError] = useState("");
  const [discoverDetails, setDiscoverDetails] = useState<unknown[]>([]);
  const discoverTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const discoverInFlightRef = useRef(false);
  const discoverDeadlineRef = useRef(0);

  /* Step 3: connect, restart, then poll the channel card. */
  const [connectAgent, setConnectAgent] = useState("");
  const [connectingChat, setConnectingChat] = useState<number | null>(null);
  const [connectStatus, setConnectStatus] = useState("");
  const [connectError, setConnectError] = useState("");
  const [connectDetails, setConnectDetails] = useState<unknown[]>([]);
  const [restartFallback, setRestartFallback] = useState(false);
  const [restartDialogOpen, setRestartDialogOpen] = useState(false);
  const [lastConnectedAgent, setLastConnectedAgent] = useState("");
  const [lastConnectedChatId, setLastConnectedChatId] = useState<number | null>(
    null
  );
  const [showSteps, setShowSteps] = useState(false);
  const connectPollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const connectPollCancelRef = useRef<(() => void) | null>(null);
  const mountedRef = useRef(false);
  const advancedRef = useRef<HTMLDetailsElement>(null);

  const load = useCallback(
    async (initial: boolean) => {
      if (initial) {
        setStatus("loading");
        setLoadError("");
        setLoadDetails([]);
      }
      try {
        const [page, agentsPage] = await Promise.all([
          listChannels(),
          listAgents(),
        ]);
        let nextVersion = page.version;
        if (!nextVersion) {
          nextVersion = await readConfigVersion();
        }
        const card = page.channels.find(isTelegramCard);
        if (!card) {
          throw new ApiError(
            0,
            "invalid_response",
            "The daemon did not return the Telegram channel.",
            []
          );
        }
        setTelegram(card);
        setAgents(agentsPage.agents);
        setVersion(nextVersion);
        setStatus("ready");
      } catch (failure: unknown) {
        if (initial) {
          setLoadError(errorMessage(failure));
          setLoadDetails(failure instanceof ApiError ? failure.details : []);
          setStatus("error");
          announce(
            "Could not load the Telegram channel. " + errorMessage(failure)
          );
        } else {
          setSaveError(errorMessage(failure));
          setSaveDetails(failure instanceof ApiError ? failure.details : []);
          announce(
            "Could not reload the Telegram channel. " + errorMessage(failure)
          );
        }
      }
    },
    [announce]
  );

  useEffect(() => {
    void load(true);
  }, [load]);

  /* Both timers must stop when the detail page unmounts. */
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (discoverTimerRef.current !== null) {
        clearTimeout(discoverTimerRef.current);
        discoverTimerRef.current = null;
      }
      if (connectPollCancelRef.current) {
        const cancel = connectPollCancelRef.current;
        connectPollCancelRef.current = null;
        cancel();
      }
    };
  }, []);

  useEffect(() => {
    if (!telegram) {
      return;
    }
    /* The switch follows the same state as the badge: only a configured
       channel that is not off can be on. */
    const on =
      Boolean(telegram.enabled) &&
      telegram.status !== "not_configured" &&
      telegram.status !== "off";
    setEnabled(on);
    setTokenEnv(telegram.token_env || DEFAULT_TOKEN_ENV);
    setApiBase(telegram.api_base || "");
    setCheckResult(null);
  }, [telegram]);

  useEffect(() => {
    setConnectAgent((current) => {
      if (current && agents.some((agent) => agent.name === current)) {
        return current;
      }
      return agents.length > 0 ? (agents[0]?.name ?? "") : "";
    });
  }, [agents]);

  const owners = telegram && Array.isArray(telegram.owners) ? telegram.owners : [];
  const chats = telegram && Array.isArray(telegram.chats) ? telegram.chats : [];
  const ownerIds = owners
    .map((owner) => Number(owner.id))
    .filter((id) => Number.isFinite(id));
  const zeroChats = chats.length === 0;
  const botUsername = (telegram && telegram.bot_username) || tokenBot;
  const botURL = (telegram && telegram.bot_url) || savedBotURL;
  const tokenDone = Boolean(telegram && telegram.token_present && botUsername);
  const connected = Boolean(telegram && telegram.status === "connected");
  const activeAgent = lastConnectedAgent || routedAgent(telegram, lastConnectedChatId);

  function writeErrorText(failure: unknown): string {
    if (failure instanceof ApiError && failure.code === "stale_version") {
      return "The config changed. Reload the page and try again.";
    }
    return errorMessage(failure);
  }

  async function runWrite(
    action: (expectedVersion: string) => Promise<unknown>,
    successMessage: string
  ): Promise<boolean> {
    if (saving) {
      return false;
    }
    setSaveError("");
    setSaveDetails([]);
    setNotice("");
    if (!version) {
      setSaveError(CONFIG_VERSION_MISSING_TEXT);
      return false;
    }
    setSaving(true);
    try {
      await action(version);
      setNotice(RESTART_NOTE);
      announce(successMessage + " " + RESTART_NOTE);
      await load(false);
      return true;
    } catch (failure: unknown) {
      const message = writeErrorText(failure);
      setSaveError(message);
      setSaveDetails(failure instanceof ApiError ? failure.details : []);
      announce("Could not save the Telegram channel. " + message);
      return false;
    } finally {
      setSaving(false);
    }
  }

  function putTelegram(
    fields: Omit<TelegramSaveBody, "expected_version">,
    successMessage: string
  ): Promise<boolean> {
    return runWrite(
      (expectedVersion) =>
        saveTelegram({ ...fields, expected_version: expectedVersion }),
      successMessage
    );
  }

  async function saveChanges(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setNotice("");
    setTokenEnvError("");
    setSaveError("");
    setSaveDetails([]);
    const variable = tokenEnv.trim();
    /* REQ-TGC-004: catch a pasted bot token before validation or any request. */
    if (BOT_TOKEN_RE.test(variable)) {
      if (advancedRef.current) {
        advancedRef.current.open = true;
      }
      setTokenEnvError(BOT_TOKEN_MISPLACED_TEXT);
      announce(BOT_TOKEN_MISPLACED_TEXT);
      return;
    }
    if (!TOKEN_ENV_RE.test(variable)) {
      if (advancedRef.current) {
        advancedRef.current.open = true;
      }
      setTokenEnvError(TOKEN_ENV_INVALID_TEXT);
      return;
    }
    await putTelegram(
      {
        enabled,
        token_env: variable,
        owner_ids: ownerIds,
        api_base: apiBase.trim(),
      },
      "Telegram settings saved."
    );
  }

  async function disconnect() {
    await putTelegram({ enabled: false }, "Telegram disconnected.");
  }

  async function checkToken() {
    if (checking) {
      return;
    }
    setChecking(true);
    setCheckResult({ tone: "pending", text: "Checking token…" });
    try {
      const result = await checkTelegram();
      if (result.ok) {
        setCheckResult({
          tone: "ok",
          text: result.bot_username
            ? "Token found. Bot is @" + result.bot_username + "."
            : "Token found.",
        });
      } else {
        setCheckResult({ tone: "warn", text: result.message || "Token missing" });
      }
    } catch (failure: unknown) {
      setCheckResult({ tone: "error", text: errorMessage(failure) });
    } finally {
      setChecking(false);
    }
  }

  async function saveToken() {
    if (savingToken) {
      return;
    }
    const token = tokenInput.trim();
    if (!token) {
      setTokenError(TOKEN_EMPTY_TEXT);
      announce(TOKEN_EMPTY_TEXT);
      return;
    }
    setSavingToken(true);
    setTokenError("");
    try {
      /* API-TGC-001. The token stays in this request body only. */
      const result = await putTelegramToken(token);
      if (!mountedRef.current) {
        return;
      }
      setTokenInput("");
      setReplaceToken(false);
      if (result && result.version) {
        setVersion(result.version);
      }
      if (result && result.bot_username) {
        setTokenBot(result.bot_username);
        announce("Connected to @" + result.bot_username + ".");
      }
      if (result && result.bot_url) {
        setSavedBotURL(result.bot_url);
      }
      stopDiscoverTimer();
      setDiscoveredChats([]);
      setDiscoverPhase("idle");
      setDiscoverError("");
      setDiscoverDetails([]);
      await load(false);
    } catch (failure: unknown) {
      if (!mountedRef.current) {
        return;
      }
      const message = errorMessage(failure);
      setTokenError(message);
      announce("Could not save the bot token. " + message);
    } finally {
      if (mountedRef.current) {
        setSavingToken(false);
      }
    }
  }

  function stopDiscoverTimer() {
    if (discoverTimerRef.current !== null) {
      clearTimeout(discoverTimerRef.current);
      discoverTimerRef.current = null;
    }
  }

  function scheduleDiscover() {
    stopDiscoverTimer();
    const remaining = discoverDeadlineRef.current - Date.now();
    if (remaining <= 0) {
      return;
    }
    const delay = Math.min(DISCOVER_POLL_MS, remaining);
    discoverTimerRef.current = setTimeout(() => {
      discoverTimerRef.current = null;
      if (!mountedRef.current) {
        return;
      }
      /* Never stack a re-check on top of one that is still running. */
      if (discoverInFlightRef.current) {
        scheduleDiscover();
        return;
      }
      void runDiscover(false);
    }, delay);
  }

  async function runDiscover(manual: boolean) {
    if (discoverInFlightRef.current) {
      return;
    }
    discoverInFlightRef.current = true;
    setDiscovering(true);
    if (manual) {
      setDiscoverError("");
      setDiscoverDetails([]);
      setDiscoveredChats([]);
      setDiscoverPhase("loading");
    }
    try {
      /* API-TGC-002. It is read-only on Telegram. */
      const result = await discoverTelegram();
      if (!mountedRef.current) {
        return;
      }
      const seen = Array.isArray(result && result.chats) ? result.chats : [];
      if (result && result.bot_url) {
        setSavedBotURL(result.bot_url);
      }
      if (seen.length > 0) {
        stopDiscoverTimer();
        setDiscoveredChats(seen);
        setDiscoverPhase("found");
        setDiscoverError("");
        setDiscoverDetails([]);
        announce("Chats found.");
      } else {
        setDiscoveredChats([]);
        setDiscoverPhase("empty");
        if (manual) {
          announce(discoverEmptyText(botUsername));
        }
        scheduleDiscover();
      }
    } catch (failure: unknown) {
      if (!mountedRef.current) {
        return;
      }
      stopDiscoverTimer();
      const message =
        failure instanceof ApiError && failure.code === "telegram_conflict"
          ? TELEGRAM_CONFLICT_TEXT
          : errorMessage(failure);
      setDiscoverPhase("error");
      setDiscoverError(message);
      setDiscoverDetails(failure instanceof ApiError ? failure.details : []);
      announce("Could not find your chat. " + message);
    } finally {
      discoverInFlightRef.current = false;
      if (mountedRef.current) {
        setDiscovering(false);
      }
    }
  }

  function findChats() {
    stopDiscoverTimer();
    discoverDeadlineRef.current = Date.now() + DISCOVER_WINDOW_MS;
    if (discoverInFlightRef.current) {
      return;
    }
    void runDiscover(true);
  }

  function stopConnectPoll() {
    if (connectPollTimerRef.current !== null) {
      clearTimeout(connectPollTimerRef.current);
      connectPollTimerRef.current = null;
    }
  }

  function pollChannelsUntilOn(): Promise<boolean> {
    return new Promise((resolve) => {
      let finished = false;
      const deadline = Date.now() + CHANNEL_POLL_LIMIT_MS;

      function finish(result: boolean) {
        if (finished) {
          return;
        }
        finished = true;
        connectPollCancelRef.current = null;
        stopConnectPoll();
        resolve(result);
      }

      connectPollCancelRef.current = () => finish(false);

      async function tick() {
        if (finished || !mountedRef.current) {
          finish(false);
          return;
        }
        let on = false;
        try {
          const page = await listChannels();
          if (finished || !mountedRef.current) {
            return;
          }
          const card = page.channels.find(isTelegramCard);
          on = card ? telegramIsOn(card.status) : false;
        } catch (failure: unknown) {
          if (failure instanceof ApiError && failure.status === 401) {
            finish(false);
            return;
          }
        }
        if (on) {
          finish(true);
          return;
        }
        if (Date.now() >= deadline) {
          finish(false);
          return;
        }
        connectPollTimerRef.current = setTimeout(tick, CHANNEL_POLL_MS);
      }

      void tick();
    });
  }

  async function finishConnected(agent: string) {
    setConnectingChat((current) => (current === null ? -1 : current));
    const on = await pollChannelsUntilOn();
    if (!mountedRef.current) {
      return;
    }
    if (!on) {
      setConnectStatus("");
      setRestartFallback(true);
      setConnectingChat(null);
      return;
    }
    const message = connectedText(botUsername, agent);
    setConnectStatus(message);
    setConnectError("");
    setConnectDetails([]);
    setRestartFallback(false);
    setConnectingChat(null);
    announce(message);
    await load(false);
  }

  function showRestartFallback(failure: unknown) {
    setConnectStatus("");
    if (failure) {
      setConnectError(errorMessage(failure));
      setConnectDetails(failure instanceof ApiError ? failure.details : []);
    } else {
      setConnectError("");
      setConnectDetails([]);
    }
    setRestartFallback(true);
    setConnectingChat(null);
  }

  async function resumeAfterRestart() {
    setRestartDialogOpen(false);
    setRestartFallback(false);
    setConnectError("");
    setConnectDetails([]);
    setConnectStatus("");
    setConnectingChat(lastConnectedChatId === null ? -1 : lastConnectedChatId);
    await finishConnected(lastConnectedAgent || connectAgent);
  }

  async function connectChat(chat: TelegramSeenChat) {
    if (connectingChat !== null) {
      return;
    }
    if (!connectAgent) {
      setConnectError("Choose an agent for this chat.");
      return;
    }
    if (!version) {
      setConnectError(CONFIG_VERSION_MISSING_TEXT);
      return;
    }
    setConnectError("");
    setConnectDetails([]);
    setRestartFallback(false);
    setConnectStatus("Connecting…");
    setConnectingChat(chat.chat_id);
    try {
      /* API-TGC-003. The version is the latest one this page wrote or read. */
      const result = await connectTelegram(version, {
        chat_id: chat.chat_id,
        sender_id: chat.sender_id,
        agent: connectAgent,
      });
      if (result && result.version) {
        setVersion(result.version);
      }
    } catch (failure: unknown) {
      if (!mountedRef.current) {
        return;
      }
      setConnectStatus("");
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setConnectError(STALE_VERSION_TEXT);
        setConnectDetails([]);
        announce(STALE_VERSION_TEXT);
        await load(false);
      } else {
        const message = errorMessage(failure);
        setConnectError(message);
        setConnectDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not connect the chat. " + message);
      }
      setConnectingChat(null);
      return;
    }
    setLastConnectedAgent(connectAgent);
    setLastConnectedChatId(chat.chat_id);

    let info: Awaited<ReturnType<typeof getDaemon>> | null = null;
    try {
      info = await getDaemon();
    } catch (failure: unknown) {
      if (!mountedRef.current) {
        return;
      }
      showRestartFallback(failure);
      return;
    }
    if (info && info.restart_supported === false) {
      showRestartFallback(null);
      return;
    }
    try {
      await restartDaemon();
    } catch (failure: unknown) {
      if (!mountedRef.current) {
        return;
      }
      showRestartFallback(failure);
      return;
    }
    try {
      await waitForDaemonRestart(info ? info.started_at : 0, (text) => {
        if (mountedRef.current) {
          setConnectStatus(text);
        }
      });
    } catch (failure: unknown) {
      if (!mountedRef.current) {
        return;
      }
      showRestartFallback(failure);
      return;
    }
    await finishConnected(connectAgent);
  }

  async function addOwner() {
    const text = ownerDraft.trim();
    if (!/^[0-9]+$/.test(text)) {
      setOwnerError("Enter the Telegram user ID as a positive whole number.");
      return;
    }
    const id = Number(text);
    if (!Number.isSafeInteger(id) || id <= 0) {
      setOwnerError("Enter the Telegram user ID as a positive whole number.");
      return;
    }
    if (ownerIds.includes(id)) {
      setOwnerError("That user is already an owner.");
      return;
    }
    setOwnerError("");
    const saved = await putTelegram(
      { owner_ids: ownerIds.concat(id) },
      "Owner added."
    );
    if (saved) {
      setOwnerFormOpen(false);
      setOwnerDraft("");
    }
  }

  async function removeOwner(owner: ChannelOwner) {
    const next = ownerIds.filter((id) => id !== Number(owner.id));
    await putTelegram({ owner_ids: next }, "Owner removed.");
  }

  async function removeChat(chat: ChannelChat) {
    await runWrite(
      (expectedVersion) =>
        deleteTelegramRoute(chat.chat_id, { expected_version: expectedVersion }),
      "Chat removed."
    );
  }

  async function openAddChat() {
    if (openingChatRef.current) {
      return;
    }
    openingChatRef.current = true;
    setOpeningChat(true);
    setWaitingError("");
    try {
      const page = await listWaitingChats();
      setWaiting(page.chats);
      setShowAddChat(true);
    } catch (failure: unknown) {
      setWaiting([]);
      setWaitingError(errorMessage(failure));
      setShowAddChat(true);
    } finally {
      openingChatRef.current = false;
      setOpeningChat(false);
    }
  }

  async function handleChatAdded() {
    setShowAddChat(false);
    setNotice(RESTART_NOTE);
    announce("Chat added. " + RESTART_NOTE);
    await load(false);
  }

  if (status === "loading") {
    return (
      <main id="main" className="page channels-page">
        <TelegramBreadcrumb />
        <div className="channels-state" role="status">
          <Loading label="Loading Telegram…" />
        </div>
      </main>
    );
  }

  if (status === "error" || !telegram) {
    return (
      <main id="main" className="page channels-page">
        <TelegramBreadcrumb />
        <div className="channels-state" role="alert">
          <ErrorNotice message={loadError} details={loadDetails} />
        </div>
        <div className="agent-state-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={() => void load(true)}
          >
            Try again
          </button>
          <a className="button-ui button-ui-secondary" href="#/channels">
            Back to channels
          </a>
        </div>
      </main>
    );
  }

  const state = telegramStatus(telegram.status);
  const tokenState =
    checkResult ||
    (telegram.token_present
      ? { tone: "ok", text: "Token found." }
      : { tone: "warn", text: "Token missing." });

  return (
    <main id="main" className="page channels-page">
      <TelegramBreadcrumb />

      <form className="channel-form" noValidate onSubmit={saveChanges}>
        <header className="agent-detail-header">
          <div className="agent-detail-identity">
            <ChannelTile telegram large>
              <TelegramMark />
            </ChannelTile>
            <div className="agent-detail-heading">
              <div className="agent-detail-name-row">
                <h1 className="agent-detail-name">Telegram</h1>
                <Badge label={state.label} className={state.className} />
              </div>
              <p className="agent-detail-meta">{telegramMeta(telegram)}</p>
            </div>
          </div>
          <div className="agent-detail-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={saving}
              onClick={disconnect}
            >
              Disconnect
            </button>
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={saving}
            >
              {saving ? "Saving…" : "Save changes"}
            </button>
          </div>
        </header>

        <div className="channel-notice" role="status" aria-live="polite">
          {notice ? <Notice>{notice}</Notice> : null}
        </div>

        {saveError ? (
          <div className="agent-save-error" role="alert">
            <ErrorNotice message={saveError} details={saveDetails} />
          </div>
        ) : null}

        {connected && !showSteps ? (
          <section
            className="channel-connected-summary"
            aria-labelledby="channel-connected-title"
          >
            <h2 id="channel-connected-title" className="channel-connected-title">
              Connected
            </h2>
            <p className="channel-connected-name">
              Connected to {botUsername ? "@" + botUsername : "your bot"}
            </p>
            <p
              className="channel-connected-text"
              role="status"
              aria-live="polite"
            >
              {connectedText(botUsername, activeAgent)}
            </p>
            <div className="channel-connected-actions">
              <button
                type="button"
                className="button-ui button-ui-secondary"
                onClick={() => setShowSteps(true)}
              >
                Show steps
              </button>
            </div>
          </section>
        ) : (
          <ol className="channel-setup-steps">
            <li className="channel-setup-step">
              <h2 className="channel-step-title">Paste your bot token</h2>
              <p className="channel-step-help">
                Create a bot with @BotFather in Telegram, then paste the token it
                gives you.
              </p>
              {tokenDone && !replaceToken ? (
                <div className="channel-step-done">
                  <p className="channel-step-done-text">
                    Connected to @{botUsername}
                  </p>
                  <button
                    type="button"
                    className="button-ui button-ui-secondary"
                    onClick={() => {
                      setReplaceToken(true);
                      setTokenError("");
                    }}
                  >
                    Replace token
                  </button>
                </div>
              ) : (
                <div className="channel-step-form">
                  <div className="agent-form-field">
                    <label
                      className="agent-form-field-label"
                      htmlFor="telegram-bot-token"
                    >
                      Bot token
                    </label>
                    <input
                      id="telegram-bot-token"
                      className="agent-number-input"
                      type="password"
                      name="bot_token"
                      value={tokenInput}
                      autoComplete="off"
                      spellCheck={false}
                      disabled={savingToken}
                      aria-describedby={
                        tokenError ? "telegram-bot-token-error" : undefined
                      }
                      onChange={(event) => {
                        setTokenInput(event.target.value);
                        if (tokenError) {
                          setTokenError("");
                        }
                      }}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          event.preventDefault();
                          void saveToken();
                        }
                      }}
                    />
                  </div>
                  {tokenError ? (
                    <p
                      id="telegram-bot-token-error"
                      className="channel-step-error"
                      role="alert"
                    >
                      {tokenError}
                    </p>
                  ) : null}
                  <div className="channel-step-actions">
                    <button
                      type="button"
                      className="button-ui button-ui-primary"
                      disabled={savingToken}
                      onClick={saveToken}
                    >
                      {savingToken ? "Saving…" : "Save token"}
                    </button>
                    {tokenDone ? (
                      <button
                        type="button"
                        className="button-ui button-ui-secondary"
                        disabled={savingToken}
                        onClick={() => {
                          setReplaceToken(false);
                          setTokenInput("");
                          setTokenError("");
                        }}
                      >
                        Cancel
                      </button>
                    ) : null}
                  </div>
                </div>
              )}
            </li>

            <li className="channel-setup-step">
              <h2 className="channel-step-title">Send a message to your bot</h2>
              <p className="channel-step-help">
                Open{" "}
                {tokenDone && botURL ? (
                  <a
                    className="channel-step-link"
                    href={botURL}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    @{botUsername}
                    <span className="sr-only"> (opens in a new tab)</span>
                  </a>
                ) : (
                  <span>{botUsername ? "@" + botUsername : "your bot"}</span>
                )}{" "}
                in Telegram and send any message, then press Find my chat.
              </p>
              <div className="channel-step-actions">
                <button
                  type="button"
                  className="button-ui button-ui-primary"
                  disabled={!tokenDone || discovering}
                  onClick={findChats}
                >
                  {discovering ? "Looking…" : "Find my chat"}
                </button>
              </div>
              <p className="channel-step-status" role="status" aria-live="polite">
                {discoverPhase === "empty" ? discoverEmptyText(botUsername) : ""}
              </p>
              {discoverError ? (
                <div className="channel-step-alert" role="alert">
                  <ErrorNotice
                    message={discoverError}
                    details={discoverDetails}
                  />
                </div>
              ) : null}
            </li>

            <li className="channel-setup-step">
              <h2 className="channel-step-title">Connect the chat</h2>
              {!tokenDone || discoveredChats.length === 0 ? (
                <p className="channel-step-help">
                  Find your chat first, then connect it here.
                </p>
              ) : (
                <div className="channel-seen-list">
                  <div className="agent-form-field channel-agent-select">
                    <label
                      className="agent-form-field-label"
                      htmlFor="telegram-connect-agent"
                    >
                      Agent
                    </label>
                    <select
                      id="telegram-connect-agent"
                      value={connectAgent}
                      disabled={connectingChat !== null}
                      onChange={(event) => setConnectAgent(event.target.value)}
                    >
                      {agents.map((agent) => (
                        <option key={agent.name} value={agent.name}>
                          {agent.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  {agents.length === 0 ? (
                    <p className="channel-hint">
                      No agents are configured yet.{" "}
                      <a href="#/agents">Create an agent</a> first.
                    </p>
                  ) : null}
                  <div className="channel-table-wrap">
                    <table className="channel-table channel-seen-table">
                      <caption className="sr-only">
                        Chats that sent your bot a message
                      </caption>
                      <thead>
                        <tr>
                          <th scope="col">Name</th>
                          <th scope="col">Type</th>
                          <th scope="col">Sender</th>
                          <th scope="col" className="channel-table-actions">
                            <span className="sr-only">Actions</span>
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {discoveredChats.map((chat) => {
                          const name = chat.name || chat.sender_name || "";
                          return (
                            <tr key={String(chat.chat_id)}>
                              <td>
                                <span className="channel-chat-cell">
                                  <InitialAvatar name={name} small />
                                  <span className="channel-chat-name">
                                    {name}
                                  </span>
                                </span>
                              </td>
                              <td>
                                <Badge label={chatTypeLabel(chat.type)} />
                              </td>
                              <td>{chat.sender_name || ""}</td>
                              <td className="channel-table-actions">
                                <button
                                  type="button"
                                  className="button-ui button-ui-primary"
                                  disabled={connectingChat !== null || !connectAgent}
                                  onClick={() => void connectChat(chat)}
                                >
                                  Connect
                                </button>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}
              <p className="channel-step-status" role="status" aria-live="polite">
                {connectStatus}
              </p>
              {connectError ? (
                <div className="channel-step-alert" role="alert">
                  <ErrorNotice message={connectError} details={connectDetails} />
                </div>
              ) : null}
              {restartFallback ? (
                <div className="channel-step-fallback">
                  <p className="channel-step-fallback-text">
                    Restart lana to finish
                  </p>
                  <button
                    type="button"
                    className="button-ui button-ui-secondary"
                    onClick={() => setRestartDialogOpen(true)}
                  >
                    Restart daemon
                  </button>
                </div>
              ) : null}
            </li>
          </ol>
        )}

        <section className="agent-form-section" aria-labelledby="channel-status-title">
          <div className="agent-form-copy">
            <h2 id="channel-status-title">Status</h2>
            <p>Saved with this page. A daemon restart applies the change.</p>
          </div>
          <div className="agent-form-controls">
            <label className="channel-switch">
              <input
                type="checkbox"
                role="switch"
                aria-checked={enabled ? "true" : "false"}
                checked={enabled}
                disabled={saving || (zeroChats && !enabled)}
                onChange={(event) => setEnabled(event.target.checked)}
              />
              <span className="channel-switch-track" aria-hidden="true"></span>
              <span className="channel-switch-text">
                {enabled ? "On" : "Off"}
              </span>
            </label>
            {zeroChats ? (
              <p className="channel-hint channel-switch-hint">
                Add a chat first before you turn Telegram on.
              </p>
            ) : (
              <p className="channel-hint">
                Telegram polls for new messages while the daemon runs.
              </p>
            )}
          </div>
        </section>

        <details className="channel-advanced" ref={advancedRef}>
          <summary className="channel-advanced-summary">Advanced</summary>
          <div className="channel-advanced-body">
            <div className="agent-form-field channel-advanced-field">
              <label
                className="agent-form-field-label"
                htmlFor="telegram-token-env"
              >
                Token variable
              </label>
              <input
                id="telegram-token-env"
                className="agent-number-input"
                type="text"
                name="token_env"
                value={tokenEnv}
                autoComplete="off"
                spellCheck={false}
                aria-describedby={
                  tokenEnvError ? "telegram-token-env-error" : undefined
                }
                onChange={(event) => {
                  setTokenEnv(event.target.value);
                  if (tokenEnvError) {
                    setTokenEnvError("");
                  }
                }}
              />
              {tokenEnvError ? (
                <p
                  id="telegram-token-env-error"
                  className="channel-step-error"
                  role="alert"
                >
                  {tokenEnvError}
                </p>
              ) : null}
            </div>
            <div className="agent-form-field channel-advanced-field">
              <label
                className="agent-form-field-label"
                htmlFor="telegram-api-base"
              >
                API base
              </label>
              <input
                id="telegram-api-base"
                className="agent-number-input"
                type="text"
                name="api_base"
                value={apiBase}
                autoComplete="off"
                spellCheck={false}
                placeholder="Default Bot API server"
                onChange={(event) => setApiBase(event.target.value)}
              />
            </div>
            <div className="channel-token-row">
              <p
                className={"channel-token-status is-" + tokenState.tone}
                role="status"
                aria-live="polite"
              >
                <span className="channel-token-dot" aria-hidden="true"></span>
                <span>{tokenState.text}</span>
              </p>
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={checking || saving}
                onClick={checkToken}
              >
                {checking ? "Checking…" : "Check token"}
              </button>
            </div>
          </div>
        </details>

        <section className="agent-form-section" aria-labelledby="channel-owners-title">
          <div className="agent-form-copy">
            <h2 id="channel-owners-title">Owners</h2>
            <p>
              Owners approve tools, and run /new, /agent, /compact and /cancel in
              groups.
            </p>
          </div>
          <div className="agent-form-controls">
            <div className="channel-panel">
              <div className="channel-owner-table-wrap">
                <table className="channel-table">
                  <caption className="sr-only">Telegram owners</caption>
                  <thead>
                    <tr>
                      <th scope="col">Owner</th>
                      <th scope="col">Telegram user ID</th>
                      <th scope="col" className="channel-table-actions">
                        <span className="sr-only">Actions</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {owners.length === 0 ? (
                      <tr className="channel-empty-row">
                        <td colSpan={3}>
                          No owners yet. Add the Telegram user ID of each person
                          who may run commands.
                        </td>
                      </tr>
                    ) : (
                      owners.map((owner) => (
                        <tr key={owner.id}>
                          <td>
                            <span className="channel-chat-cell">
                              <InitialAvatar
                                name={owner.name || String(owner.id)}
                                small
                              />
                              <span
                                className={
                                  "channel-owner-name" +
                                  (owner.name ? "" : " is-empty")
                                }
                              >
                                {owner.name || "No display name"}
                              </span>
                            </span>
                          </td>
                          <td>
                            <code className="channel-owner-id">{owner.id}</code>
                          </td>
                          <td className="channel-table-actions">
                            <button
                              type="button"
                              className="channel-icon-button"
                              aria-label={
                                "Remove owner " + (owner.name || owner.id)
                              }
                              disabled={saving}
                              onClick={() => void removeOwner(owner)}
                            >
                              <Icon name="close" />
                            </button>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
              {ownerFormOpen ? (
                <div className="channel-add-owner">
                  <div className="channel-add-owner-row">
                    <label className="agent-form-field channel-add-owner-field">
                      <span className="agent-form-field-label">
                        Telegram user ID
                      </span>
                      <input
                        className="agent-number-input"
                        type="number"
                        step="1"
                        inputMode="numeric"
                        value={ownerDraft}
                        autoComplete="off"
                        onChange={(event) => setOwnerDraft(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter") {
                            event.preventDefault();
                            void addOwner();
                          }
                        }}
                      />
                    </label>
                    <button
                      type="button"
                      className="button-ui button-ui-primary"
                      disabled={saving}
                      onClick={addOwner}
                    >
                      Add
                    </button>
                    <button
                      type="button"
                      className="button-ui button-ui-secondary"
                      disabled={saving}
                      onClick={() => {
                        setOwnerFormOpen(false);
                        setOwnerDraft("");
                        setOwnerError("");
                      }}
                    >
                      Cancel
                    </button>
                  </div>
                  {ownerError ? (
                    <div className="channel-field-error" role="alert">
                      <ErrorNotice message={ownerError} />
                    </div>
                  ) : null}
                </div>
              ) : (
                <button
                  type="button"
                  className="channel-add-owner-button"
                  disabled={saving}
                  onClick={() => setOwnerFormOpen(true)}
                >
                  <Icon name="plus" />
                  <span>Add owner</span>
                </button>
              )}
            </div>
          </div>
        </section>

        <section className="channel-chats-section" aria-labelledby="channel-chats-title">
          <div className="channel-chats-head">
            <div>
              <h2 id="channel-chats-title" className="channel-section-title">
                Chats
              </h2>
              <p className="channel-section-text">
                Only these chats reach the bot. In groups the bot answers only
                when someone mentions it.
              </p>
            </div>
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={saving}
              aria-busy={openingChat ? "true" : "false"}
              onClick={openAddChat}
            >
              {openingChat ? "Loading chats…" : "+ Add chat"}
            </button>
          </div>
          <div className="channel-table-wrap">
            <table className="channel-table">
              <caption className="sr-only">
                Telegram chats and the agent each one reaches
              </caption>
              <thead>
                <tr>
                  <th scope="col">Chat</th>
                  <th scope="col">Type</th>
                  <th scope="col">Chat ID</th>
                  <th scope="col">Agent</th>
                  <th scope="col" className="channel-table-actions">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {chats.length === 0 ? (
                  <tr className="channel-empty-row">
                    <td colSpan={5}>
                      No chats yet. Add a chat to let people reach an agent from
                      Telegram.
                    </td>
                  </tr>
                ) : (
                  chats.map((chat) => {
                    const name = chat.name || "Chat " + chat.chat_id;
                    return (
                      <tr key={chat.chat_id}>
                        <td>
                          <span className="channel-chat-cell">
                            <InitialAvatar name={name} small />
                            <span className="channel-chat-name">{name}</span>
                          </span>
                        </td>
                        <td>
                          <Badge label={chatTypeLabel(chat.type)} />
                        </td>
                        <td>
                          <code className="channel-chat-id">{chat.chat_id}</code>
                        </td>
                        <td>
                          <span className="channel-agent">
                            {chat.agent ? (
                              <AgentAvatar name={chat.agent} size="24" />
                            ) : null}
                            <span>{chat.agent || "No agent"}</span>
                          </span>
                        </td>
                        <td className="channel-table-actions">
                          <button
                            type="button"
                            className="channel-icon-button"
                            aria-label={"Remove chat " + name}
                            disabled={saving}
                            onClick={() => void removeChat(chat)}
                          >
                            <Icon name="close" />
                          </button>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
        </section>
      </form>

      {restartDialogOpen ? (
        <SettingsRestartDialog
          onClose={() => setRestartDialogOpen(false)}
          onRestarted={resumeAfterRestart}
        />
      ) : null}

      {showAddChat ? (
        <AddChatDialog
          agents={agents}
          version={version}
          waiting={waiting}
          waitingError={waitingError}
          onClose={() => setShowAddChat(false)}
          onAdded={handleChatAdded}
        />
      ) : null}
    </main>
  );
}

interface AddChatDialogProps {
  agents: Agent[];
  version: string;
  waiting: WaitingChat[];
  waitingError: string;
  onClose: () => void;
  onAdded: () => void;
}

function AddChatDialog({
  agents,
  version,
  waiting,
  waitingError,
  onClose,
  onAdded,
}: AddChatDialogProps) {
  const announce = useAnnounce();
  const [selected, setSelected] = useState("");
  const [manualId, setManualId] = useState("");
  const [agent, setAgent] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const chats = Array.isArray(waiting) ? waiting : [];
  const firstWaiting = chats.length > 0;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    setError("");
    setDetails([]);

    const manual = manualId.trim();
    let chatId: number | null = null;
    let name = "";
    if (manual) {
      chatId = parseChatId(manual);
      if (chatId === null) {
        setError("Enter the chat ID as a whole number. Group chat IDs are negative.");
        return;
      }
    } else if (selected) {
      const match = chats.find((chat) => String(chat.chat_id) === selected);
      chatId = match ? Number(match.chat_id) : parseChatId(selected);
      name = match && match.name ? match.name : "";
    }
    if (chatId === null) {
      setError("Choose a chat from the list or enter a chat ID.");
      return;
    }
    if (!agent) {
      setError("Choose an agent for this chat.");
      return;
    }
    if (!version) {
      setError(
        "The config version is unavailable. Close this dialog and reload the page."
      );
      return;
    }

    setBusy(true);
    try {
      await saveTelegramRoute(chatId, {
        agent,
        name,
        expected_version: version,
      });
      announce("Chat added. " + RESTART_NOTE);
      onAdded();
    } catch (failure: unknown) {
      const message =
        failure instanceof ApiError && failure.code === "stale_version"
          ? "The config changed. Reload the page and try again."
          : errorMessage(failure);
      setError(message);
      setDetails(failure instanceof ApiError ? failure.details : []);
      announce("Could not add the chat. " + message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      title="Add chat"
      className="channel-dialog"
      dismissible={!busy}
      onClose={onClose}
    >
      <form className="agent-dialog-form" noValidate onSubmit={submit}>
        <p className="channel-dialog-intro">
          Send any message to the bot from the chat or group. It shows up below.
        </p>

        <fieldset className="channel-waiting">
          <legend>Chats waiting for access</legend>
          {waitingError ? (
            <div className="channel-field-error" role="alert">
              <ErrorNotice message={waitingError} />
            </div>
          ) : null}
          {!waitingError && chats.length === 0 ? (
            <p className="channel-waiting-empty">
              No chats are waiting. Send a message to the bot from the chat, then
              open this again.
            </p>
          ) : null}
          {chats.length > 0 ? (
            <ul className="channel-waiting-list">
              {chats.map((chat, index) => {
                const id = String(chat.chat_id);
                const name = chat.name || "Chat " + chat.chat_id;
                const isSelected = selected === id;
                return (
                  <li key={id}>
                    <label
                      className={
                        "channel-waiting-item" +
                        (isSelected ? " is-selected" : "")
                      }
                    >
                      <InitialAvatar name={name} small />
                      <span className="channel-waiting-text">
                        <span className="channel-waiting-name-row">
                          <span className="channel-waiting-name">{name}</span>
                          <Badge label={chatTypeLabel(chat.type)} />
                        </span>
                        <span className="channel-waiting-meta">
                          <code>{chat.chat_id}</code> · wrote{" "}
                          {agoLabel(chat.last_message_at) || "just now"}
                        </span>
                      </span>
                      <input
                        className="channel-radio"
                        type="radio"
                        name="waiting-chat"
                        value={id}
                        checked={isSelected}
                        data-autofocus={index === 0 ? "" : undefined}
                        onChange={() => setSelected(id)}
                      />
                    </label>
                  </li>
                );
              })}
            </ul>
          ) : null}
        </fieldset>

        <label className="agent-form-field">
          <span className="agent-form-field-label">
            Not listed? Enter the chat ID by hand.
          </span>
          <input
            className="agent-number-input"
            type="number"
            step="1"
            inputMode="numeric"
            value={manualId}
            autoComplete="off"
            placeholder="Chat ID"
            data-autofocus={firstWaiting ? undefined : ""}
            onChange={(event) => setManualId(event.target.value)}
          />
          <span className="channel-hint">
            Group chat IDs are negative. A typed ID takes precedence over the list.
          </span>
        </label>

        <label className="agent-form-field">
          <span className="agent-form-field-label">Agent</span>
          <select
            value={agent}
            onChange={(event) => setAgent(event.target.value)}
          >
            <option value="">Choose an agent</option>
            {agents.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}
              </option>
            ))}
          </select>
        </label>
        <p className="channel-hint">
          New messages from this chat go to this agent. People can switch with
          /agent.
        </p>
        {agents.length === 0 ? (
          <p className="channel-hint">
            No agents are configured yet. <a href="#/agents">Create an agent</a>{" "}
            first.
          </p>
        ) : null}

        {error ? (
          <div className="channel-field-error" role="alert">
            <ErrorNotice message={error} details={details} />
          </div>
        ) : null}

        <div className="dialog-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </button>
          <button
            type="submit"
            className="button-ui button-ui-primary"
            disabled={busy}
          >
            {busy ? "Adding…" : "Add chat"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

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
import { getDaemon } from "../api/settings";
import { Dialog, detailText, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { agoLabel } from "./agent-common";
import {
  Avatar,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  Input,
  Panel,
  Radio,
  Select,
  Skeleton,
  Toggle,
} from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { RestartDaemonDialog, useDaemonRestart } from "./restart-dialog";

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
    icon: "message-circle",
    description: "Talk to agents in a server or DM.",
  },
  {
    name: "slack",
    label: "Slack",
    icon: "hash",
    description: "Mention an agent in a channel.",
  },
  {
    name: "whatsapp",
    label: "WhatsApp",
    icon: "phone",
    description: "Chat with an agent from your phone.",
  },
];

/* The agent identity colors (lib/avatar.ts) map onto the Gr8r status tokens so
   one agent keeps one color in the roster, the list and the detail page. */
const AVATAR_COLORS: Record<string, string> = {
  "tone-brand": "var(--violet)",
  "tone-success": "var(--green)",
  "tone-warning": "var(--amber)",
  "tone-error": "var(--red)",
  "tone-gray": "var(--gray)",
};

function agentColor(name: string): string {
  return AVATAR_COLORS[avatarFor(name).colorClass] || "var(--violet)";
}

function TelegramMark() {
  return (
    <svg
      className="chan-mark"
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

function telegramStatus(status: string | undefined): {
  label: string;
  tone: BadgeTone;
} {
  if (status === "connected") {
    return { label: "Connected", tone: "green" };
  }
  if (status === "token_missing") {
    return { label: "Token missing", tone: "amber" };
  }
  return { label: "Off", tone: "gray" };
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

function DetailsList({ details }: { details: unknown[] }) {
  if (!details || details.length === 0) {
    return null;
  }
  return (
    <ul className="err-list">
      {details.map((detail, index) => (
        <li key={index}>{detailText(detail)}</li>
      ))}
    </ul>
  );
}

function ErrorAlert({
  message,
  details,
}: {
  message: string;
  details?: unknown[];
}) {
  return (
    <div className="alert danger" role="alert">
      <Ic name="circle-alert" size={15} />
      <div className="grow">
        <p className="chan-alert-msg">{message}</p>
        <DetailsList details={details || []} />
      </div>
    </div>
  );
}

interface SettingRowProps {
  id: string;
  label: string;
  hint?: ReactNode;
  control: ReactNode;
  error?: ReactNode;
}

/* A Gr8r settings row: label and hint on the left, control on the right. */
function SettingRow({ id, label, hint, control, error }: SettingRowProps) {
  return (
    <div className="srow">
      <div className="chan-srow-text">
        <label className="t" htmlFor={id}>
          {label}
        </label>
        {hint ? <div className="d">{hint}</div> : null}
      </div>
      <div className="chan-ctl">
        {control}
        {error ? (
          <span className="err" role="alert">
            <Ic name="circle-alert" size={12} />
            {error}
          </span>
        ) : null}
      </div>
    </div>
  );
}

interface ChannelTileProps {
  children: ReactNode;
  brand?: boolean;
}

function ChannelTile({ children, brand }: ChannelTileProps) {
  return (
    <span className={"chan-tile" + (brand ? " is-brand" : "")} aria-hidden="true">
      {children}
    </span>
  );
}

interface ChannelRowProps {
  tile: ReactNode;
  brand?: boolean;
  title: string;
  badge: ReactNode;
  meta: ReactNode;
  agent?: string;
  action?: ReactNode;
  muted?: boolean;
}

/* One card of the channels grid (Gr8r projects/members style). */
function ChannelRow({
  tile,
  brand,
  title,
  badge,
  meta,
  agent,
  action,
  muted,
}: ChannelRowProps) {
  return (
    <article className={"chan-card" + (muted ? " is-muted" : "")}>
      <div className="chan-card-head">
        <ChannelTile brand={brand}>{tile}</ChannelTile>
        <div className="chan-card-id">
          <div className="chan-card-title">
            <h2 className="chan-card-name">{title}</h2>
            {badge}
          </div>
          <p className="chan-card-meta">{meta}</p>
        </div>
      </div>
      {agent ? (
        <div className="chan-card-agent">
          <Avatar name={agent} color={agentColor(agent)} size="sm" />
          <span>{agent}</span>
        </div>
      ) : null}
      {action ? <div className="chan-card-foot">{action}</div> : null}
    </article>
  );
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
  const telegramAgent = telegram ? routedAgent(telegram, null) : "";

  return (
    <main id="main" className="page channels-page">
      <header className="ph">
        <div>
          <h1>Channels</h1>
          <p>Where people reach your agents. Each chat goes to one agent.</p>
        </div>
      </header>

      {status === "loading" ? (
        <div className="chan-grid" aria-busy="true">
          {Array.from({ length: 3 }).map((_, index) => (
            <div key={index} className="chan-card chan-skeleton">
              <div className="chan-card-head">
                <Skeleton width={40} height={40} radius={10} />
                <div className="chan-card-id">
                  <Skeleton width="45%" height={13} />
                  <Skeleton width="70%" height={11} />
                </div>
              </div>
              <Skeleton width="100%" height={26} radius={6} />
            </div>
          ))}
        </div>
      ) : null}

      {status === "error" ? (
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load channels"
            text={error}
            action={
              <Button
                variant="secondary"
                onClick={() => setReloadToken((current) => current + 1)}
              >
                Try again
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
      ) : null}

      {status === "ready" && channels.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="radio-tower"
            title="No channels yet"
            text="Channels appear here when the daemon has Telegram, the web view, or the CLI available."
          />
        </div>
      ) : null}

      {status === "ready" && channels.length > 0 ? (
        <div className="chan-grid">
          <ChannelRow
            brand
            tile={<TelegramMark />}
            title="Telegram"
            badge={
              <Badge tone={telegramState.tone} dot>
                {telegramState.label}
              </Badge>
            }
            meta={telegramMetaParts.join(" · ")}
            agent={telegramAgent}
            action={
              <Button
                as="a"
                variant="secondary"
                size="sm"
                href="#/channels/telegram"
                aria-label="Manage Telegram"
              >
                Manage
              </Button>
            }
          />

          <ChannelRow
            tile={<Ic name="globe" size={20} />}
            title="Web"
            badge={
              <Badge tone={web?.status === "on" ? "green" : "gray"} dot>
                {web?.status === "on" ? "On" : "Off"}
              </Badge>
            }
            meta={web?.addr ? "This dashboard · " + web.addr : "This dashboard"}
            action={
              <Button
                as="a"
                variant="secondary"
                size="sm"
                href="#/settings/general"
                aria-label="Web settings"
              >
                Settings
              </Button>
            }
          />

          <ChannelRow
            tile={<Ic name="terminal" size={20} />}
            title="CLI"
            badge={<Badge>Built in</Badge>}
            meta="lana chat in your terminal"
          />

          {COMING_SOON.map((channel) => (
            <ChannelRow
              key={channel.name}
              muted
              tile={<Ic name={channel.icon} size={20} />}
              title={channel.label}
              badge={<Badge tone="gray">Coming soon</Badge>}
              meta={channel.description}
            />
          ))}
        </div>
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
  const [showToken, setShowToken] = useState(false);
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
  const [lastConnectedAgent, setLastConnectedAgent] = useState("");
  const [lastConnectedChatId, setLastConnectedChatId] = useState<number | null>(
    null
  );
  const [showSteps, setShowSteps] = useState(false);
  const connectPollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const connectPollCancelRef = useRef<(() => void) | null>(null);
  const mountedRef = useRef(false);
  const advancedRef = useRef<HTMLDetailsElement>(null);

  /* Step 3 shares the restart flow of Settings > General (REQ-TFB-003): a
     draining daemon shows the waiting dialog with Restart now and Cancel. */
  const restart = useDaemonRestart({
    onRestarted: () => {
      void resumeAfterRestart();
    },
    onError: (failure) => {
      showRestartFallback(failure);
    },
  });

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
  const stepTwoDone = discoveredChats.length > 0 || chats.length > 0;
  const stepThreeDone = chats.length > 0;

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
    setRestartFallback(false);
    setConnectError("");
    setConnectDetails([]);
    setConnectStatus("");
    setConnectingChat(lastConnectedChatId === null ? -1 : lastConnectedChatId);
    await finishConnected(lastConnectedAgent || connectAgent);
  }

  /* The fallback button restarts again with the shared flow (REQ-TFB-003). */
  async function retryRestart() {
    setConnectError("");
    setConnectDetails([]);
    try {
      const info = await getDaemon();
      if (info && info.restart_supported === false) {
        setConnectError(
          "Restarting from the browser is not supported on this platform."
        );
        return;
      }
      await restart.start(info ? info.started_at : 0);
    } catch (failure: unknown) {
      setConnectError(errorMessage(failure));
      setConnectDetails(failure instanceof ApiError ? failure.details : []);
    }
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
    if (!mountedRef.current) {
      return;
    }
    setConnectStatus("Restarting…");
    /* The shared restart flow calls resumeAfterRestart when the daemon is back
       (REQ-TFB-003); a draining daemon keeps the waiting dialog open. */
    await restart.start(info ? info.started_at : 0);
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
      <main id="main" className="page channels-page channels-detail">
        <header className="ph">
          <div>
            <a className="chan-back" href="#/channels">
              <Ic name="arrow-left" size={14} />
              Channels
            </a>
          </div>
        </header>
        <div className="panel chan-loading" aria-busy="true">
          <span className="sr">Loading Telegram…</span>
          <Skeleton width={160} height={14} />
          <Skeleton width="100%" height={30} radius={6} />
          <Skeleton width="100%" height={30} radius={6} />
          <Skeleton width="100%" height={90} radius={6} />
        </div>
      </main>
    );
  }

  if (status === "error" || !telegram) {
    return (
      <main id="main" className="page channels-page channels-detail">
        <header className="ph">
          <div>
            <a className="chan-back" href="#/channels">
              <Ic name="arrow-left" size={14} />
              Channels
            </a>
            <h1>Telegram</h1>
          </div>
          <div className="acts">
            <Button as="a" variant="secondary" href="#/channels">
              Back to channels
            </Button>
            <Button variant="primary" onClick={() => void load(true)}>
              Try again
            </Button>
          </div>
        </header>
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load the Telegram channel"
            text={loadError}
          >
            <DetailsList details={loadDetails} />
          </ErrorState>
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
  const agentOptions = agents.map((agent) => ({
    value: agent.name,
    label: agent.name,
  }));

  return (
    <main id="main" className="page channels-page channels-detail">
      <header className="ph">
        <div>
          <a className="chan-back" href="#/channels">
            <Ic name="arrow-left" size={14} />
            Channels
          </a>
          <div className="chan-ph-title">
            <h1>Telegram</h1>
            <Badge tone={state.tone} dot>
              {state.label}
            </Badge>
          </div>
          <p>{telegramMeta(telegram)}</p>
        </div>
        <div className="acts">
          <Button variant="secondary" disabled={saving} onClick={disconnect}>
            Disconnect
          </Button>
          <Button
            type="submit"
            form="telegram-form"
            variant="primary"
            disabled={saving}
          >
            {saving ? "Saving…" : "Save changes"}
          </Button>
        </div>
      </header>

      <form
        id="telegram-form"
        className="channels-form"
        noValidate
        onSubmit={saveChanges}
      >
        <div className="chan-notice" role="status" aria-live="polite">
          {notice ? (
            <div className="alert ok">
              <Ic name="circle-check" size={15} />
              <span>{notice}</span>
            </div>
          ) : null}
        </div>

        {saveError ? (
          <ErrorAlert message={saveError} details={saveDetails} />
        ) : null}

        {connected && !showSteps ? (
          <Panel
            className="chan-connected"
            title="Connected"
            actions={
              <Button
                size="sm"
                variant="secondary"
                onClick={() => setShowSteps(true)}
              >
                Show steps
              </Button>
            }
          >
            <p className="chan-connected-name">
              Connected to {botUsername ? "@" + botUsername : "your bot"}
            </p>
            <p className="chan-connected-text" role="status" aria-live="polite">
              {connectedText(botUsername, activeAgent)}
            </p>
          </Panel>
        ) : (
          <ol className="chan-steps">
            <li className={"chan-step" + (tokenDone ? " is-done" : "")}>
              <span className="chan-step-ic" aria-hidden="true">
                {tokenDone ? <Ic name="check" size={14} /> : "1"}
              </span>
              <div className="chan-step-body">
                <h2 className="chan-step-title">Paste your bot token</h2>
                {tokenDone && !replaceToken ? (
                  <div className="chan-step-done">
                    <p className="chan-step-done-text">
                      Connected to @{botUsername}
                    </p>
                    <Button
                      variant="secondary"
                      onClick={() => {
                        setReplaceToken(true);
                        setTokenError("");
                      }}
                    >
                      Replace token
                    </Button>
                  </div>
                ) : (
                  <div className="chan-step-form">
                    <SettingRow
                      id="telegram-bot-token"
                      label="Bot token"
                      hint="Create a bot with @BotFather in Telegram, then paste the token it gives you."
                      error={tokenError || undefined}
                      control={
                        <div className="chan-token-input">
                          <Input
                            id="telegram-bot-token"
                            type={showToken ? "text" : "password"}
                            name="bot_token"
                            value={tokenInput}
                            autoComplete="off"
                            spellCheck={false}
                            disabled={savingToken}
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
                          <IconButton
                            icon={showToken ? "eye-off" : "eye"}
                            size="sm"
                            label={showToken ? "Hide token" : "Show token"}
                            onClick={() =>
                              setShowToken((current) => !current)
                            }
                          />
                        </div>
                      }
                    />
                    <div className="chan-step-actions">
                      <Button
                        variant="primary"
                        disabled={savingToken}
                        onClick={saveToken}
                      >
                        {savingToken ? "Saving…" : "Save token"}
                      </Button>
                      {tokenDone ? (
                        <Button
                          variant="secondary"
                          disabled={savingToken}
                          onClick={() => {
                            setReplaceToken(false);
                            setTokenInput("");
                            setTokenError("");
                          }}
                        >
                          Cancel
                        </Button>
                      ) : null}
                    </div>
                  </div>
                )}
              </div>
            </li>

            <li className={"chan-step" + (stepTwoDone ? " is-done" : "")}>
              <span className="chan-step-ic" aria-hidden="true">
                {stepTwoDone ? <Ic name="check" size={14} /> : "2"}
              </span>
              <div className="chan-step-body">
                <h2 className="chan-step-title">Send a message to your bot</h2>
                <p className="chan-step-help">
                  Open{" "}
                  {tokenDone && botURL ? (
                    <a
                      className="chan-step-link"
                      href={botURL}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      @{botUsername}
                      <span className="sr"> (opens in a new tab)</span>
                    </a>
                  ) : (
                    <span>{botUsername ? "@" + botUsername : "your bot"}</span>
                  )}{" "}
                  in Telegram and send any message, then press Find my chat.
                </p>
                <div className="chan-step-actions">
                  <Button
                    variant="primary"
                    disabled={!tokenDone || discovering}
                    onClick={findChats}
                  >
                    {discovering ? "Looking…" : "Find my chat"}
                  </Button>
                </div>
                <p
                  className="chan-step-status"
                  role="status"
                  aria-live="polite"
                >
                  {discoverPhase === "empty"
                    ? discoverEmptyText(botUsername)
                    : ""}
                </p>
                {discoverError ? (
                  <ErrorAlert
                    message={discoverError}
                    details={discoverDetails}
                  />
                ) : null}
              </div>
            </li>

            <li className={"chan-step" + (stepThreeDone ? " is-done" : "")}>
              <span className="chan-step-ic" aria-hidden="true">
                {stepThreeDone ? <Ic name="check" size={14} /> : "3"}
              </span>
              <div className="chan-step-body">
                <h2 className="chan-step-title">Connect the chat</h2>
                {!tokenDone || discoveredChats.length === 0 ? (
                  <p className="chan-step-help">
                    Find your chat first, then connect it here.
                  </p>
                ) : (
                  <div className="chan-seen">
                    <SettingRow
                      id="telegram-connect-agent"
                      label="Agent"
                      hint="New messages from this chat go to this agent. People can switch with /agent."
                      control={
                        <Select
                          aria-label="Agent for this chat"
                          options={agentOptions}
                          value={connectAgent}
                          onChange={setConnectAgent}
                        />
                      }
                    />
                    {agents.length === 0 ? (
                      <p className="hint">
                        No agents are configured yet.{" "}
                        <a href="#/agents">Create an agent</a> first.
                      </p>
                    ) : null}
                    <div className="chan-tablewrap">
                      <table className="perm-t chan-table">
                        <caption className="sr">
                          Chats that sent your bot a message
                        </caption>
                        <thead>
                          <tr>
                            <th scope="col">Name</th>
                            <th scope="col">Type</th>
                            <th scope="col">Sender</th>
                            <th scope="col">
                              <span className="sr">Actions</span>
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {discoveredChats.map((chat) => {
                            const name = chat.name || chat.sender_name || "";
                            return (
                              <tr key={String(chat.chat_id)}>
                                <td>
                                  <span className="chan-agent">
                                    <Avatar
                                      name={name}
                                      color={agentColor(name)}
                                      size="sm"
                                    />
                                    <span className="chan-name">{name}</span>
                                  </span>
                                </td>
                                <td>
                                  <Badge>{chatTypeLabel(chat.type)}</Badge>
                                </td>
                                <td>{chat.sender_name || ""}</td>
                                <td className="chan-rowactions">
                                  <Button
                                    size="sm"
                                    variant="primary"
                                    disabled={
                                      connectingChat !== null || !connectAgent
                                    }
                                    onClick={() => void connectChat(chat)}
                                  >
                                    Connect
                                  </Button>
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}
                <p
                  className="chan-step-status"
                  role="status"
                  aria-live="polite"
                >
                  {connectStatus}
                </p>
                {connectError ? (
                  <ErrorAlert message={connectError} details={connectDetails} />
                ) : null}
                {restartFallback ? (
                  <div className="alert warn">
                    <Ic name="triangle-alert" size={15} />
                    <span className="grow">Restart lana to finish</span>
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => {
                        void retryRestart();
                      }}
                    >
                      Restart daemon
                    </Button>
                  </div>
                ) : null}
              </div>
            </li>
          </ol>
        )}

        <section className="sblock">
          <h2>Status</h2>
          <div className="srow">
            <div>
              <div className="t">Enabled</div>
              <div className="d">
                Saved with this page. A daemon restart applies the change.
              </div>
            </div>
            <div className="row">
              <span className="chan-switch-label">{enabled ? "On" : "Off"}</span>
              <Toggle
                aria-label="Telegram on"
                checked={enabled}
                disabled={saving || (zeroChats && !enabled)}
                onChange={setEnabled}
              />
            </div>
          </div>
          <p className={zeroChats ? "hint chan-hint-warn" : "hint"}>
            {zeroChats
              ? "Add a chat first before you turn Telegram on."
              : "Telegram polls for new messages while the daemon runs."}
          </p>
        </section>

        <section className="sblock">
          <h2>Advanced</h2>
          <Panel bodyClassName="chan-advanced-body">
            <details className="chan-advanced" ref={advancedRef}>
              <summary className="chan-advanced-summary">
                <Ic name="chevron-right" size={15} />
                <span>Token variable and API base</span>
              </summary>
              <div className="chan-advanced-in">
                <SettingRow
                  id="telegram-token-env"
                  label="Token variable"
                  hint="The environment variable that holds the bot token."
                  error={tokenEnvError || undefined}
                  control={
                    <Input
                      id="telegram-token-env"
                      type="text"
                      name="token_env"
                      value={tokenEnv}
                      autoComplete="off"
                      spellCheck={false}
                      onChange={(event) => {
                        setTokenEnv(event.target.value);
                        if (tokenEnvError) {
                          setTokenEnvError("");
                        }
                      }}
                    />
                  }
                />
                <SettingRow
                  id="telegram-api-base"
                  label="API base"
                  hint="Leave empty to use the default Bot API server."
                  control={
                    <Input
                      id="telegram-api-base"
                      type="text"
                      name="api_base"
                      value={apiBase}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="Default Bot API server"
                      onChange={(event) => setApiBase(event.target.value)}
                    />
                  }
                />
                <div className="chan-token-row">
                  <p
                    className={"chan-status is-" + tokenState.tone}
                    role="status"
                    aria-live="polite"
                  >
                    <span className="chan-dot" aria-hidden="true"></span>
                    <span>{tokenState.text}</span>
                  </p>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={checking || saving}
                    onClick={checkToken}
                  >
                    {checking ? "Checking…" : "Check token"}
                  </Button>
                </div>
              </div>
            </details>
          </Panel>
        </section>

        <section className="sblock">
          <h2>Owners</h2>
          <p className="chan-section-hint">
            Owners approve tools, and run /new, /agent, /compact and /cancel in
            groups.
          </p>
          <div className="panel chan-panel">
            <div className="chan-tablewrap">
              <table className="perm-t chan-table">
                <caption className="sr">Telegram owners</caption>
                <thead>
                  <tr>
                    <th scope="col">Owner</th>
                    <th scope="col">Telegram user ID</th>
                    <th scope="col">
                      <span className="sr">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {owners.length === 0 ? (
                    <tr>
                      <td colSpan={3} className="chan-empty">
                        No owners yet. Add the Telegram user ID of each person
                        who may run commands.
                      </td>
                    </tr>
                  ) : (
                    owners.map((owner) => (
                      <tr key={owner.id}>
                        <td>
                          <span className="chan-agent">
                            <Avatar
                              name={owner.name || String(owner.id)}
                              color={agentColor(owner.name || String(owner.id))}
                              size="sm"
                            />
                            <span
                              className={
                                "chan-name" + (owner.name ? "" : " is-empty")
                              }
                            >
                              {owner.name || "No display name"}
                            </span>
                          </span>
                        </td>
                        <td>
                          <code className="chan-code">{owner.id}</code>
                        </td>
                        <td className="chan-rowactions">
                          <IconButton
                            size="sm"
                            icon="x"
                            label={"Remove owner " + (owner.name || owner.id)}
                            disabled={saving}
                            onClick={() => void removeOwner(owner)}
                          />
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
            {ownerFormOpen ? (
              <div className="chan-addrow-form">
                <Field
                  label="Telegram user ID"
                  hint="The number shown on the person's Telegram profile."
                  className="chan-addrow-grow"
                >
                  <Input
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
                </Field>
                <Button
                  variant="primary"
                  size="sm"
                  disabled={saving}
                  onClick={addOwner}
                >
                  Add
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={saving}
                  onClick={() => {
                    setOwnerFormOpen(false);
                    setOwnerDraft("");
                    setOwnerError("");
                  }}
                >
                  Cancel
                </Button>
              </div>
            ) : (
              <button
                type="button"
                className="chan-addrow"
                disabled={saving}
                onClick={() => setOwnerFormOpen(true)}
              >
                <Ic name="plus" size={14} />
                <span>Add owner</span>
              </button>
            )}
            {ownerError ? (
              <div className="chan-inline-alert">
                <ErrorAlert message={ownerError} />
              </div>
            ) : null}
          </div>
        </section>

        <section className="sblock">
          <div className="chan-sechead">
            <div>
              <h2>Chats</h2>
              <p className="chan-section-hint">
                Only these chats reach the bot. In groups the bot answers only
                when someone mentions it.
              </p>
            </div>
            <Button
              variant="secondary"
              size="sm"
              icon="plus"
              disabled={saving}
              aria-busy={openingChat ? "true" : "false"}
              onClick={openAddChat}
            >
              {openingChat ? "Loading chats…" : "Add chat"}
            </Button>
          </div>
          <div className="panel chan-panel">
            <div className="chan-tablewrap">
              <table className="perm-t chan-table">
                <caption className="sr">
                  Telegram chats and the agent each one reaches
                </caption>
                <thead>
                  <tr>
                    <th scope="col">Chat</th>
                    <th scope="col">Type</th>
                    <th scope="col">Chat ID</th>
                    <th scope="col">Agent</th>
                    <th scope="col">
                      <span className="sr">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {chats.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="chan-empty">
                        No chats yet. Add a chat to let people reach an agent
                        from Telegram.
                      </td>
                    </tr>
                  ) : (
                    chats.map((chat) => {
                      const name = chat.name || "Chat " + chat.chat_id;
                      return (
                        <tr key={chat.chat_id}>
                          <td>
                            <span className="chan-agent">
                              <Avatar
                                name={name}
                                color={agentColor(name)}
                                size="sm"
                              />
                              <span className="chan-name">{name}</span>
                            </span>
                          </td>
                          <td>
                            <Badge>{chatTypeLabel(chat.type)}</Badge>
                          </td>
                          <td>
                            <code className="chan-code">{chat.chat_id}</code>
                          </td>
                          <td>
                            <span className="chan-agent">
                              {chat.agent ? (
                                <Avatar
                                  name={chat.agent}
                                  color={agentColor(chat.agent)}
                                  size="sm"
                                />
                              ) : null}
                              <span
                                className={
                                  "chan-name" +
                                  (chat.agent ? "" : " is-empty")
                                }
                              >
                                {chat.agent || "No agent"}
                              </span>
                            </span>
                          </td>
                          <td className="chan-rowactions">
                            <IconButton
                              size="sm"
                              icon="x"
                              label={"Remove chat " + name}
                              disabled={saving}
                              onClick={() => void removeChat(chat)}
                            />
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </section>
      </form>

      {restart.phase !== "idle" ? (
        <RestartDaemonDialog
          controller={restart}
          confirmFirst={false}
          onClose={() => {
            void restart.cancel();
          }}
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
  const agentOptions = [{ value: "", label: "Choose an agent" }].concat(
    agents.map((item) => ({ value: item.name, label: item.name }))
  );

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

  function clearError() {
    if (error) {
      setError("");
    }
  }

  return (
    <Dialog title="Add chat" dismissible={!busy} onClose={onClose}>
      <form className="stack" noValidate onSubmit={submit}>
        <p className="chan-dialog-intro">
          Send any message to the bot from the chat or group. It shows up below.
        </p>

        <fieldset className="chan-waiting">
          <legend>Chats waiting for access</legend>
          {waitingError ? <ErrorAlert message={waitingError} /> : null}
          {!waitingError && chats.length === 0 ? (
            <p className="chan-waiting-empty">
              No chats are waiting. Send a message to the bot from the chat, then
              open this again.
            </p>
          ) : null}
          {chats.length > 0 ? (
            <ul className="chan-waiting-list">
              {chats.map((chat, index) => {
                const id = String(chat.chat_id);
                const name = chat.name || "Chat " + chat.chat_id;
                const isSelected = selected === id;
                return (
                  <li key={id}>
                    <label
                      className={
                        "chan-waiting-item" + (isSelected ? " is-selected" : "")
                      }
                    >
                      <Avatar name={name} color={agentColor(name)} size="sm" />
                      <span className="chan-waiting-text">
                        <span className="chan-waiting-name-row">
                          <span className="chan-waiting-name">{name}</span>
                          <Badge>{chatTypeLabel(chat.type)}</Badge>
                        </span>
                        <span className="chan-waiting-meta">
                          <code>{chat.chat_id}</code> · wrote{" "}
                          {agoLabel(chat.last_message_at) || "just now"}
                        </span>
                      </span>
                      <Radio
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

        <Field
          label="Not listed? Enter the chat ID by hand."
          hint="Group chat IDs are negative. A typed ID takes precedence over the list."
        >
          <Input
            type="number"
            step="1"
            inputMode="numeric"
            value={manualId}
            autoComplete="off"
            placeholder="Chat ID"
            data-autofocus={firstWaiting ? undefined : ""}
            onChange={(event) => {
              setManualId(event.target.value);
              clearError();
            }}
          />
        </Field>

        <Field label="Agent">
          <Select
            aria-label="Agent for this chat"
            options={agentOptions}
            value={agent}
            onChange={(value) => {
              setAgent(value);
              clearError();
            }}
          />
        </Field>
        <p className="hint">
          New messages from this chat go to this agent. People can switch with
          /agent.
        </p>
        {agents.length === 0 ? (
          <p className="hint">
            No agents are configured yet. <a href="#/agents">Create an agent</a>{" "}
            first.
          </p>
        ) : null}

        {error ? <ErrorAlert message={error} details={details} /> : null}

        <div className="dialog-actions">
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? "Adding…" : "Add chat"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

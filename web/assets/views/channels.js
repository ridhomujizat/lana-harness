import {
  html,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "/assets/vendor/preact-htm.js";
import { api, errorMessage, ApiError } from "/assets/api.js";
import {
  Dialog,
  ErrorNotice,
  Loading,
  Notice,
  useAnnounce,
} from "/assets/ui.js";
import { avatarFor } from "/assets/avatar.js";
import { Icon, AgentAvatar, agoLabel } from "/assets/views/agent-common.js";
import {
  checkTelegram,
  deleteTelegramRoute,
  listAgents,
  listChannels,
  listWaitingChats,
  readConfigVersion,
  saveTelegram,
  saveTelegramRoute,
} from "/assets/api/channels.js";
import {
  getDaemon,
  restartDaemon,
  waitForDaemonRestart,
} from "/assets/api/settings.js";
import { SettingsRestartDialog } from "/assets/views/settings-pages.js";

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

function Badge({ label, className = "badge-ui" }) {
  return html`<span class=${className}>
    <span class="badge-ui-dot" aria-hidden="true"></span>
    ${label}
  </span>`;
}

function InitialAvatar({ name, small = false }) {
  return html`<span
    class=${"channel-avatar" + (small ? " channel-avatar-sm" : "")}
    aria-hidden="true"
  >${avatarFor(name).initial}</span>`;
}

function TelegramMark() {
  return html`<svg
    class="channel-tile-mark"
    viewBox="0 0 24 24"
    aria-hidden="true"
    focusable="false"
  >
    <path
      d="M16.906 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z"
      fill="currentColor"
    ></path>
  </svg>`;
}

function ChannelTile({ children, telegram = false, large = false }) {
  let className = "channel-tile";
  if (telegram) {
    className += " channel-tile-telegram";
  }
  if (large) {
    className += " channel-tile-lg";
  }
  return html`<span class=${className} aria-hidden="true">${children}</span>`;
}

function telegramStatus(status) {
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

function chatTypeLabel(type) {
  return type === "group" ? "Group" : "Private";
}

function countText(count, singular, plural) {
  const value = Number(count);
  const safe = Number.isFinite(value) && value >= 0 ? Math.floor(value) : 0;
  return safe + " " + (safe === 1 ? singular : plural);
}

function telegramMeta(telegram) {
  const parts = [];
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
  return html`<nav class="agent-breadcrumb" aria-label="Breadcrumb">
    <ol class="channel-crumbs">
      <li><a href="#/channels">Channels</a></li>
      <li aria-current="page">Telegram</li>
    </ol>
  </nav>`;
}

function parseChatId(value) {
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
function telegramIsOn(status) {
  return status !== "off" && status !== "not_configured";
}

function connectedText(bot, agent) {
  const name = bot ? "@" + bot : "your bot";
  if (agent) {
    return "Telegram is on. Send a message to " + name + " to talk to " + agent + ".";
  }
  return "Telegram is on. Send a message to " + name + ".";
}

function routedAgent(telegram, chatId) {
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

function discoverEmptyText(bot) {
  return (
    "No messages yet. Send any message to " +
    (bot ? "@" + bot : "your bot") +
    ", then try again."
  );
}

export function ChannelsPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState("loading");
  const [channels, setChannels] = useState([]);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
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
      .catch((failure) => {
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

  const cards = {};
  for (const card of channels) {
    if (card && card.name) {
      cards[card.name] = card;
    }
  }
  const telegram = cards.telegram || {};
  const web = cards.web || {};
  const telegramState = telegramStatus(telegram.status);
  const chatCount = Number.isFinite(Number(telegram.chat_count))
    ? Number(telegram.chat_count)
    : Array.isArray(telegram.chats)
      ? telegram.chats.length
      : 0;
  const ownerCount = Number.isFinite(Number(telegram.owner_count))
    ? Number(telegram.owner_count)
    : Array.isArray(telegram.owners)
      ? telegram.owners.length
      : 0;
  const telegramMetaParts = [];
  if (telegram.bot_username) {
    telegramMetaParts.push("@" + telegram.bot_username);
  }
  telegramMetaParts.push(countText(chatCount, "chat", "chats"));
  telegramMetaParts.push(countText(ownerCount, "owner", "owners"));

  return html`<main id="main" class="page channels-page">
    <header class="channels-header">
      <h1 class="agents-title">Channels</h1>
      <p class="agents-subtitle">
        Where people reach your agents. Each chat goes to one agent.
      </p>
    </header>

    ${status === "loading"
      ? html`<div class="channels-state" role="status">
          <${Loading} label="Loading channels…" />
        </div>`
      : null}

    ${status === "error"
      ? html`<div class="channels-state" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          <button
            type="button"
            class="button-ui button-ui-secondary"
            onClick=${() => setReloadToken((current) => current + 1)}
          >Try again</button>
        </div>`
      : null}

    ${status === "ready" && channels.length === 0
      ? html`<div class="channels-empty">
          <h2 class="channels-empty-title">No channels yet</h2>
          <p class="channels-empty-text">
            Channels appear here when the daemon has Telegram, the web view, or
            the CLI available.
          </p>
        </div>`
      : null}

    ${status === "ready" && channels.length > 0
      ? html`<ul class="channels-list">
          <li class="channel-card">
            <${ChannelTile} telegram><${TelegramMark} /></${ChannelTile}>
            <div class="channel-card-main">
              <div class="channel-card-name-row">
                <h2 class="channel-card-name">Telegram</h2>
                <${Badge} label=${telegramState.label} className=${telegramState.className} />
              </div>
              <p class="channel-card-meta">${telegramMetaParts.join(" · ")}</p>
            </div>
            <div class="channel-card-action">
              <a
                class="button-ui button-ui-secondary"
                href="#/channels/telegram"
                aria-label="Manage Telegram"
              >Manage</a>
            </div>
          </li>

          <li class="channel-card">
            <${ChannelTile}><${Icon} name="globe" /></${ChannelTile}>
            <div class="channel-card-main">
              <div class="channel-card-name-row">
                <h2 class="channel-card-name">Web</h2>
                <${Badge}
                  label=${web.status === "on" ? "On" : "Off"}
                  className=${web.status === "on" ? "badge-ui channel-badge-success" : "badge-ui"}
                />
              </div>
              <p class="channel-card-meta">
                ${web.addr ? "This dashboard · " + web.addr : "This dashboard"}
              </p>
            </div>
            <div class="channel-card-action">
              <a
                class="button-ui button-ui-secondary"
                href="#/settings/general"
                aria-label="Web settings"
              >Settings</a>
            </div>
          </li>

          <li class="channel-card">
            <${ChannelTile}><${Icon} name="terminal" /></${ChannelTile}>
            <div class="channel-card-main">
              <div class="channel-card-name-row">
                <h2 class="channel-card-name">CLI</h2>
                <${Badge} label="Built in" />
              </div>
              <p class="channel-card-meta">lana chat in your terminal</p>
            </div>
          </li>

          ${COMING_SOON.map(
            (channel) => html`<li class="channel-card is-muted" key=${channel.name}>
              <${ChannelTile}>${channel.letter}</${ChannelTile}>
              <div class="channel-card-main">
                <div class="channel-card-name-row">
                  <h2 class="channel-card-name">${channel.label}</h2>
                  <${Badge} label="Coming soon" />
                </div>
                <p class="channel-card-meta">${channel.description}</p>
              </div>
            </li>`
          )}
        </ul>`
      : null}
  </main>`;
}

export function TelegramPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState("loading");
  const [telegram, setTelegram] = useState(null);
  const [version, setVersion] = useState("");
  const [agents, setAgents] = useState([]);
  const [loadError, setLoadError] = useState("");
  const [loadDetails, setLoadDetails] = useState([]);
  const [saveError, setSaveError] = useState("");
  const [saveDetails, setSaveDetails] = useState([]);
  const [notice, setNotice] = useState("");
  const [saving, setSaving] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [tokenEnv, setTokenEnv] = useState(DEFAULT_TOKEN_ENV);
  const [apiBase, setApiBase] = useState("");
  const [checkResult, setCheckResult] = useState(null);
  const [checking, setChecking] = useState(false);
  const [showAddChat, setShowAddChat] = useState(false);
  const [waiting, setWaiting] = useState([]);
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
  const [discoveredChats, setDiscoveredChats] = useState([]);
  const [discoverPhase, setDiscoverPhase] = useState("idle");
  const [discovering, setDiscovering] = useState(false);
  const [discoverError, setDiscoverError] = useState("");
  const [discoverDetails, setDiscoverDetails] = useState([]);
  const discoverTimerRef = useRef(null);
  const discoverInFlightRef = useRef(false);
  const discoverDeadlineRef = useRef(0);

  /* Step 3: connect, restart, then poll the channel card. */
  const [connectAgent, setConnectAgent] = useState("");
  const [connectingChat, setConnectingChat] = useState(null);
  const [connectStatus, setConnectStatus] = useState("");
  const [connectError, setConnectError] = useState("");
  const [connectDetails, setConnectDetails] = useState([]);
  const [restartFallback, setRestartFallback] = useState(false);
  const [restartDialogOpen, setRestartDialogOpen] = useState(false);
  const [lastConnectedAgent, setLastConnectedAgent] = useState("");
  const [lastConnectedChatId, setLastConnectedChatId] = useState(null);
  const [showSteps, setShowSteps] = useState(false);
  const connectPollTimerRef = useRef(null);
  const connectPollCancelRef = useRef(null);
  const mountedRef = useRef(false);
  const advancedRef = useRef(null);

  const load = useCallback(
    async (initial) => {
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
        const card = page.channels.find(
          (item) => item && item.name === "telegram"
        );
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
      } catch (failure) {
        if (initial) {
          setLoadError(errorMessage(failure));
          setLoadDetails(failure instanceof ApiError ? failure.details : []);
          setStatus("error");
          announce("Could not load the Telegram channel. " + errorMessage(failure));
        } else {
          setSaveError(errorMessage(failure));
          setSaveDetails(failure instanceof ApiError ? failure.details : []);
          announce("Could not reload the Telegram channel. " + errorMessage(failure));
        }
      }
    },
    [announce]
  );

  useEffect(() => {
    load(true);
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
      return agents.length > 0 ? agents[0].name : "";
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

  function writeErrorText(failure) {
    if (failure instanceof ApiError && failure.code === "stale_version") {
      return "The config changed. Reload the page and try again.";
    }
    return errorMessage(failure);
  }

  async function runWrite(action, successMessage) {
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
    } catch (failure) {
      const message = writeErrorText(failure);
      setSaveError(message);
      setSaveDetails(failure instanceof ApiError ? failure.details : []);
      announce("Could not save the Telegram channel. " + message);
      return false;
    } finally {
      setSaving(false);
    }
  }

  function putTelegram(fields, successMessage) {
    return runWrite(
      (expectedVersion) =>
        saveTelegram(Object.assign({}, fields, { expected_version: expectedVersion })),
      successMessage
    );
  }

  async function saveChanges(event) {
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
    } catch (failure) {
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
      const result = await api.putTelegramToken(token);
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
    } catch (failure) {
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
      runDiscover(false);
    }, delay);
  }

  async function runDiscover(manual) {
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
      const result = await api.discoverTelegram();
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
    } catch (failure) {
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
    runDiscover(true);
  }

  function stopConnectPoll() {
    if (connectPollTimerRef.current !== null) {
      clearTimeout(connectPollTimerRef.current);
      connectPollTimerRef.current = null;
    }
  }

  function pollChannelsUntilOn() {
    return new Promise((resolve) => {
      let finished = false;
      const deadline = Date.now() + CHANNEL_POLL_LIMIT_MS;

      function finish(result) {
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
          const card = page.channels.find(
            (item) => item && item.name === "telegram"
          );
          on = Boolean(card) && telegramIsOn(card.status);
        } catch (failure) {
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

      tick();
    });
  }

  async function finishConnected(agent) {
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

  function showRestartFallback(failure) {
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

  async function connectChat(chat) {
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
      const result = await api.connectTelegram(version, {
        chat_id: chat.chat_id,
        sender_id: chat.sender_id,
        agent: connectAgent,
      });
      if (result && result.version) {
        setVersion(result.version);
      }
    } catch (failure) {
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

    let info = null;
    try {
      info = await getDaemon();
    } catch (failure) {
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
    } catch (failure) {
      if (!mountedRef.current) {
        return;
      }
      showRestartFallback(failure);
      return;
    }
    try {
      await waitForDaemonRestart(info && info.started_at, (text) => {
        if (mountedRef.current) {
          setConnectStatus(text);
        }
      });
    } catch (failure) {
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

  async function removeOwner(owner) {
    const next = ownerIds.filter((id) => id !== Number(owner.id));
    await putTelegram({ owner_ids: next }, "Owner removed.");
  }

  async function removeChat(chat) {
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
    } catch (failure) {
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
    return html`<main id="main" class="page channels-page">
      <${TelegramBreadcrumb} />
      <div class="channels-state" role="status">
        <${Loading} label="Loading Telegram…" />
      </div>
    </main>`;
  }

  if (status === "error" || !telegram) {
    return html`<main id="main" class="page channels-page">
      <${TelegramBreadcrumb} />
      <div class="channels-state" role="alert">
        <${ErrorNotice} message=${loadError} details=${loadDetails} />
      </div>
      <div class="agent-state-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          onClick=${() => load(true)}
        >Try again</button>
        <a class="button-ui button-ui-secondary" href="#/channels">Back to channels</a>
      </div>
    </main>`;
  }

  const state = telegramStatus(telegram.status);
  const tokenState = checkResult || (telegram.token_present
    ? { tone: "ok", text: "Token found." }
    : { tone: "warn", text: "Token missing." });

  return html`<main id="main" class="page channels-page">
    <${TelegramBreadcrumb} />

    <form class="channel-form" novalidate onSubmit=${saveChanges}>
      <header class="agent-detail-header">
        <div class="agent-detail-identity">
          <${ChannelTile} telegram large><${TelegramMark} /></${ChannelTile}>
          <div class="agent-detail-heading">
            <div class="agent-detail-name-row">
              <h1 class="agent-detail-name">Telegram</h1>
              <${Badge} label=${state.label} className=${state.className} />
            </div>
            <p class="agent-detail-meta">${telegramMeta(telegram)}</p>
          </div>
        </div>
        <div class="agent-detail-actions">
          <button
            type="button"
            class="button-ui button-ui-secondary"
            disabled=${saving}
            onClick=${disconnect}
          >Disconnect</button>
          <button
            type="submit"
            class="button-ui button-ui-primary"
            disabled=${saving}
          >${saving ? "Saving…" : "Save changes"}</button>
        </div>
      </header>

      <div class="channel-notice" role="status" aria-live="polite">
        ${notice ? html`<${Notice}>${notice}</${Notice}>` : null}
      </div>

      ${saveError
        ? html`<div class="agent-save-error" role="alert">
            <${ErrorNotice} message=${saveError} details=${saveDetails} />
          </div>`
        : null}

      ${connected && !showSteps
        ? html`<section
            class="channel-connected-summary"
            aria-labelledby="channel-connected-title"
          >
            <h2 id="channel-connected-title" class="channel-connected-title">
              Connected
            </h2>
            <p class="channel-connected-name">
              Connected to ${botUsername ? "@" + botUsername : "your bot"}
            </p>
            <p
              class="channel-connected-text"
              role="status"
              aria-live="polite"
            >${connectedText(botUsername, activeAgent)}</p>
            <div class="channel-connected-actions">
              <button
                type="button"
                class="button-ui button-ui-secondary"
                onClick=${() => setShowSteps(true)}
              >Show steps</button>
            </div>
          </section>`
        : html`<ol class="channel-setup-steps">
            <li class="channel-setup-step">
              <h2 class="channel-step-title">Paste your bot token</h2>
              <p class="channel-step-help">
                Create a bot with @BotFather in Telegram, then paste the token it
                gives you.
              </p>
              ${tokenDone && !replaceToken
                ? html`<div class="channel-step-done">
                    <p class="channel-step-done-text">
                      Connected to @${botUsername}
                    </p>
                    <button
                      type="button"
                      class="button-ui button-ui-secondary"
                      onClick=${() => {
                        setReplaceToken(true);
                        setTokenError("");
                      }}
                    >Replace token</button>
                  </div>`
                : html`<div class="channel-step-form">
                    <div class="agent-form-field">
                      <label
                        class="agent-form-field-label"
                        for="telegram-bot-token"
                      >Bot token</label>
                      <input
                        id="telegram-bot-token"
                        class="agent-number-input"
                        type="password"
                        name="bot_token"
                        value=${tokenInput}
                        autocomplete="off"
                        spellcheck="false"
                        disabled=${savingToken}
                        aria-describedby=${tokenError ? "telegram-bot-token-error" : null}
                        onInput=${(event) => {
                          setTokenInput(event.target.value);
                          if (tokenError) {
                            setTokenError("");
                          }
                        }}
                        onKeyDown=${(event) => {
                          if (event.key === "Enter") {
                            event.preventDefault();
                            saveToken();
                          }
                        }}
                      />
                    </div>
                    ${tokenError
                      ? html`<p
                          id="telegram-bot-token-error"
                          class="channel-step-error"
                          role="alert"
                        >${tokenError}</p>`
                      : null}
                    <div class="channel-step-actions">
                      <button
                        type="button"
                        class="button-ui button-ui-primary"
                        disabled=${savingToken}
                        onClick=${saveToken}
                      >${savingToken ? "Saving…" : "Save token"}</button>
                      ${tokenDone
                        ? html`<button
                            type="button"
                            class="button-ui button-ui-secondary"
                            disabled=${savingToken}
                            onClick=${() => {
                              setReplaceToken(false);
                              setTokenInput("");
                              setTokenError("");
                            }}
                          >Cancel</button>`
                        : null}
                    </div>
                  </div>`}
            </li>

            <li class="channel-setup-step">
              <h2 class="channel-step-title">Send a message to your bot</h2>
              <p class="channel-step-help">
                Open ${tokenDone && botURL
                  ? html`<a
                      class="channel-step-link"
                      href=${botURL}
                      target="_blank"
                      rel="noopener noreferrer"
                    >@${botUsername}<span class="sr-only"> (opens in a new tab)</span></a>`
                  : html`<span>${botUsername ? "@" + botUsername : "your bot"}</span>`}
                in Telegram and send any message, then press Find my chat.
              </p>
              <div class="channel-step-actions">
                <button
                  type="button"
                  class="button-ui button-ui-primary"
                  disabled=${!tokenDone || discovering}
                  onClick=${findChats}
                >${discovering ? "Looking…" : "Find my chat"}</button>
              </div>
              <p class="channel-step-status" role="status" aria-live="polite">
                ${discoverPhase === "empty" ? discoverEmptyText(botUsername) : ""}
              </p>
              ${discoverError
                ? html`<div class="channel-step-alert" role="alert">
                    <${ErrorNotice}
                      message=${discoverError}
                      details=${discoverDetails}
                    />
                  </div>`
                : null}
            </li>

            <li class="channel-setup-step">
              <h2 class="channel-step-title">Connect the chat</h2>
              ${!tokenDone || discoveredChats.length === 0
                ? html`<p class="channel-step-help">
                    Find your chat first, then connect it here.
                  </p>`
                : html`<div class="channel-seen-list">
                    <div class="agent-form-field channel-agent-select">
                      <label
                        class="agent-form-field-label"
                        for="telegram-connect-agent"
                      >Agent</label>
                      <select
                        id="telegram-connect-agent"
                        value=${connectAgent}
                        disabled=${connectingChat !== null}
                        onChange=${(event) => setConnectAgent(event.target.value)}
                      >
                        ${agents.map(
                          (agent) => html`<option key=${agent.name} value=${agent.name}>
                            ${agent.name}
                          </option>`
                        )}
                      </select>
                    </div>
                    ${agents.length === 0
                      ? html`<p class="channel-hint">
                          No agents are configured yet.
                          <a href="#/agents">Create an agent</a> first.
                        </p>`
                      : null}
                    <div class="channel-table-wrap">
                      <table class="channel-table channel-seen-table">
                        <caption class="sr-only">
                          Chats that sent your bot a message
                        </caption>
                        <thead>
                          <tr>
                            <th scope="col">Name</th>
                            <th scope="col">Type</th>
                            <th scope="col">Sender</th>
                            <th scope="col" class="channel-table-actions">
                              <span class="sr-only">Actions</span>
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          ${discoveredChats.map((chat) => {
                            const name = chat.name || chat.sender_name || "";
                            return html`<tr key=${String(chat.chat_id)}>
                              <td>
                                <span class="channel-chat-cell">
                                  <${InitialAvatar} name=${name} small />
                                  <span class="channel-chat-name">${name}</span>
                                </span>
                              </td>
                              <td><${Badge} label=${chatTypeLabel(chat.type)} /></td>
                              <td>${chat.sender_name || ""}</td>
                              <td class="channel-table-actions">
                                <button
                                  type="button"
                                  class="button-ui button-ui-primary"
                                  disabled=${connectingChat !== null || !connectAgent}
                                  onClick=${() => connectChat(chat)}
                                >Connect</button>
                              </td>
                            </tr>`;
                          })}
                        </tbody>
                      </table>
                    </div>
                  </div>`}
              <p class="channel-step-status" role="status" aria-live="polite">
                ${connectStatus}
              </p>
              ${connectError
                ? html`<div class="channel-step-alert" role="alert">
                    <${ErrorNotice}
                      message=${connectError}
                      details=${connectDetails}
                    />
                  </div>`
                : null}
              ${restartFallback
                ? html`<div class="channel-step-fallback">
                    <p class="channel-step-fallback-text">
                      Restart lana to finish
                    </p>
                    <button
                      type="button"
                      class="button-ui button-ui-secondary"
                      onClick=${() => setRestartDialogOpen(true)}
                    >Restart daemon</button>
                  </div>`
                : null}
            </li>
          </ol>`}

      <section class="agent-form-section" aria-labelledby="channel-status-title">
        <div class="agent-form-copy">
          <h2 id="channel-status-title">Status</h2>
          <p>Saved with this page. A daemon restart applies the change.</p>
        </div>
        <div class="agent-form-controls">
          <label class="channel-switch">
            <input
              type="checkbox"
              role="switch"
              aria-checked=${enabled ? "true" : "false"}
              checked=${enabled}
              disabled=${saving || (zeroChats && !enabled)}
              onChange=${(event) => setEnabled(event.target.checked)}
            />
            <span class="channel-switch-track" aria-hidden="true"></span>
            <span class="channel-switch-text">${enabled ? "On" : "Off"}</span>
          </label>
          ${zeroChats
            ? html`<p class="channel-hint channel-switch-hint">
                Add a chat first before you turn Telegram on.
              </p>`
            : html`<p class="channel-hint">
                Telegram polls for new messages while the daemon runs.
              </p>`}
        </div>
      </section>

      <details class="channel-advanced" ref=${advancedRef}>
        <summary class="channel-advanced-summary">Advanced</summary>
        <div class="channel-advanced-body">
          <div class="agent-form-field channel-advanced-field">
            <label
              class="agent-form-field-label"
              for="telegram-token-env"
            >Token variable</label>
            <input
              id="telegram-token-env"
              class="agent-number-input"
              type="text"
              name="token_env"
              value=${tokenEnv}
              autocomplete="off"
              spellcheck="false"
              aria-describedby=${tokenEnvError ? "telegram-token-env-error" : null}
              onInput=${(event) => {
                setTokenEnv(event.target.value);
                if (tokenEnvError) {
                  setTokenEnvError("");
                }
              }}
            />
            ${tokenEnvError
              ? html`<p
                  id="telegram-token-env-error"
                  class="channel-step-error"
                  role="alert"
                >${tokenEnvError}</p>`
              : null}
          </div>
          <div class="agent-form-field channel-advanced-field">
            <label
              class="agent-form-field-label"
              for="telegram-api-base"
            >API base</label>
            <input
              id="telegram-api-base"
              class="agent-number-input"
              type="text"
              name="api_base"
              value=${apiBase}
              autocomplete="off"
              spellcheck="false"
              placeholder="Default Bot API server"
              onInput=${(event) => setApiBase(event.target.value)}
            />
          </div>
          <div class="channel-token-row">
            <p
              class=${"channel-token-status is-" + tokenState.tone}
              role="status"
              aria-live="polite"
            >
              <span class="channel-token-dot" aria-hidden="true"></span>
              <span>${tokenState.text}</span>
            </p>
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${checking || saving}
              onClick=${checkToken}
            >${checking ? "Checking…" : "Check token"}</button>
          </div>
        </div>
      </details>

      <section class="agent-form-section" aria-labelledby="channel-owners-title">
        <div class="agent-form-copy">
          <h2 id="channel-owners-title">Owners</h2>
          <p>
            Owners approve tools, and run /new, /agent, /compact and /cancel in groups.
          </p>
        </div>
        <div class="agent-form-controls">
          <div class="channel-panel">
            <div class="channel-owner-table-wrap">
              <table class="channel-table">
                <caption class="sr-only">Telegram owners</caption>
                <thead>
                  <tr>
                    <th scope="col">Owner</th>
                    <th scope="col">Telegram user ID</th>
                    <th scope="col" class="channel-table-actions">
                      <span class="sr-only">Actions</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  ${owners.length === 0
                    ? html`<tr class="channel-empty-row">
                        <td colspan="3">
                          No owners yet. Add the Telegram user ID of each person
                          who may run commands.
                        </td>
                      </tr>`
                    : owners.map(
                        (owner) => html`<tr key=${owner.id}>
                          <td>
                            <span class="channel-chat-cell">
                              <${InitialAvatar}
                                name=${owner.name || String(owner.id)}
                                small
                              />
                              <span
                                class=${"channel-owner-name" + (owner.name ? "" : " is-empty")}
                              >
                                ${owner.name || "No display name"}
                              </span>
                            </span>
                          </td>
                          <td>
                            <code class="channel-owner-id">${owner.id}</code>
                          </td>
                          <td class="channel-table-actions">
                            <button
                              type="button"
                              class="channel-icon-button"
                              aria-label=${"Remove owner " + (owner.name || owner.id)}
                              disabled=${saving}
                              onClick=${() => removeOwner(owner)}
                            ><${Icon} name="close" /></button>
                          </td>
                        </tr>`
                      )}
                </tbody>
              </table>
            </div>
            ${ownerFormOpen
              ? html`<div class="channel-add-owner">
                  <div class="channel-add-owner-row">
                    <label class="agent-form-field channel-add-owner-field">
                      <span class="agent-form-field-label">Telegram user ID</span>
                      <input
                        class="agent-number-input"
                        type="number"
                        step="1"
                        inputmode="numeric"
                        value=${ownerDraft}
                        autocomplete="off"
                        onInput=${(event) => setOwnerDraft(event.target.value)}
                        onKeyDown=${(event) => {
                          if (event.key === "Enter") {
                            event.preventDefault();
                            addOwner();
                          }
                        }}
                      />
                    </label>
                    <button
                      type="button"
                      class="button-ui button-ui-primary"
                      disabled=${saving}
                      onClick=${addOwner}
                    >Add</button>
                    <button
                      type="button"
                      class="button-ui button-ui-secondary"
                      disabled=${saving}
                      onClick=${() => {
                        setOwnerFormOpen(false);
                        setOwnerDraft("");
                        setOwnerError("");
                      }}
                    >Cancel</button>
                  </div>
                  ${ownerError
                    ? html`<div class="channel-field-error" role="alert">
                        <${ErrorNotice} message=${ownerError} />
                      </div>`
                    : null}
                </div>`
              : html`<button
                  type="button"
                  class="channel-add-owner-button"
                  disabled=${saving}
                  onClick=${() => setOwnerFormOpen(true)}
                >
                  <${Icon} name="plus" />
                  <span>Add owner</span>
                </button>`}
          </div>
        </div>
      </section>

      <section class="channel-chats-section" aria-labelledby="channel-chats-title">
        <div class="channel-chats-head">
          <div>
            <h2 id="channel-chats-title" class="channel-section-title">Chats</h2>
            <p class="channel-section-text">
              Only these chats reach the bot. In groups the bot answers only when
              someone mentions it.
            </p>
          </div>
          <button
            type="button"
            class="button-ui button-ui-secondary"
            disabled=${saving}
            aria-busy=${openingChat ? "true" : "false"}
            onClick=${openAddChat}
          >${openingChat ? "Loading chats…" : "+ Add chat"}</button>
        </div>
        <div class="channel-table-wrap">
          <table class="channel-table">
            <caption class="sr-only">
              Telegram chats and the agent each one reaches
            </caption>
            <thead>
              <tr>
                <th scope="col">Chat</th>
                <th scope="col">Type</th>
                <th scope="col">Chat ID</th>
                <th scope="col">Agent</th>
                <th scope="col" class="channel-table-actions">
                  <span class="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              ${chats.length === 0
                ? html`<tr class="channel-empty-row">
                    <td colspan="5">
                      No chats yet. Add a chat to let people reach an agent from
                      Telegram.
                    </td>
                  </tr>`
                : chats.map((chat) => {
                    const name = chat.name || "Chat " + chat.chat_id;
                    return html`<tr key=${chat.chat_id}>
                      <td>
                        <span class="channel-chat-cell">
                          <${InitialAvatar} name=${name} small />
                          <span class="channel-chat-name">${name}</span>
                        </span>
                      </td>
                      <td>
                        <${Badge} label=${chatTypeLabel(chat.type)} />
                      </td>
                      <td><code class="channel-chat-id">${chat.chat_id}</code></td>
                      <td>
                        <span class="channel-agent">
                          ${chat.agent
                            ? html`<${AgentAvatar} name=${chat.agent} size="24" />`
                            : null}
                          <span>${chat.agent || "No agent"}</span>
                        </span>
                      </td>
                      <td class="channel-table-actions">
                        <button
                          type="button"
                          class="channel-icon-button"
                          aria-label=${"Remove chat " + name}
                          disabled=${saving}
                          onClick=${() => removeChat(chat)}
                        ><${Icon} name="close" /></button>
                      </td>
                    </tr>`;
                  })}
            </tbody>
          </table>
        </div>
      </section>
    </form>

    ${restartDialogOpen
      ? html`<${SettingsRestartDialog}
          onClose=${() => setRestartDialogOpen(false)}
          onRestarted=${resumeAfterRestart}
        />`
      : null}

    ${showAddChat
      ? html`<${AddChatDialog}
          agents=${agents}
          version=${version}
          waiting=${waiting}
          waitingError=${waitingError}
          onClose=${() => setShowAddChat(false)}
          onAdded=${handleChatAdded}
        />`
      : null}
  </main>`;
}

function AddChatDialog({
  agents,
  version,
  waiting,
  waitingError,
  onClose,
  onAdded,
}) {
  const announce = useAnnounce();
  const [selected, setSelected] = useState("");
  const [manualId, setManualId] = useState("");
  const [agent, setAgent] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);

  const chats = Array.isArray(waiting) ? waiting : [];
  const firstWaiting = chats.length > 0;

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    setError("");
    setDetails([]);

    const manual = manualId.trim();
    let chatId = null;
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
      setError("The config version is unavailable. Close this dialog and reload the page.");
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
    } catch (failure) {
      const message = failure instanceof ApiError && failure.code === "stale_version"
        ? "The config changed. Reload the page and try again."
        : errorMessage(failure);
      setError(message);
      setDetails(failure instanceof ApiError ? failure.details : []);
      announce("Could not add the chat. " + message);
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title="Add chat"
    className="channel-dialog"
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form class="agent-dialog-form" novalidate onSubmit=${submit}>
      <p class="channel-dialog-intro">
        Send any message to the bot from the chat or group. It shows up below.
      </p>

      <fieldset class="channel-waiting">
        <legend>Chats waiting for access</legend>
        ${waitingError
          ? html`<div class="channel-field-error" role="alert">
              <${ErrorNotice} message=${waitingError} />
            </div>`
          : null}
        ${!waitingError && chats.length === 0
          ? html`<p class="channel-waiting-empty">
              No chats are waiting. Send a message to the bot from the chat, then
              open this again.
            </p>`
          : null}
        ${chats.length > 0
          ? html`<ul class="channel-waiting-list">
              ${chats.map((chat, index) => {
                const id = String(chat.chat_id);
                const name = chat.name || "Chat " + chat.chat_id;
                const isSelected = selected === id;
                return html`<li key=${id}>
                  <label
                    class=${"channel-waiting-item" + (isSelected ? " is-selected" : "")}
                  >
                    <${InitialAvatar} name=${name} small />
                    <span class="channel-waiting-text">
                      <span class="channel-waiting-name-row">
                        <span class="channel-waiting-name">${name}</span>
                        <${Badge} label=${chatTypeLabel(chat.type)} />
                      </span>
                      <span class="channel-waiting-meta">
                        <code>${chat.chat_id}</code> · wrote ${agoLabel(chat.last_message_at) || "just now"}
                      </span>
                    </span>
                    <input
                      class="channel-radio"
                      type="radio"
                      name="waiting-chat"
                      value=${id}
                      checked=${isSelected}
                      data-autofocus=${index === 0 ? "" : null}
                      onChange=${() => setSelected(id)}
                    />
                  </label>
                </li>`;
              })}
            </ul>`
          : null}
      </fieldset>

      <label class="agent-form-field">
        <span class="agent-form-field-label">
          Not listed? Enter the chat ID by hand.
        </span>
        <input
          class="agent-number-input"
          type="number"
          step="1"
          inputmode="numeric"
          value=${manualId}
          autocomplete="off"
          placeholder="Chat ID"
          data-autofocus=${firstWaiting ? null : ""}
          onInput=${(event) => setManualId(event.target.value)}
        />
        <span class="channel-hint">
          Group chat IDs are negative. A typed ID takes precedence over the list.
        </span>
      </label>

      <label class="agent-form-field">
        <span class="agent-form-field-label">Agent</span>
        <select
          value=${agent}
          onChange=${(event) => setAgent(event.target.value)}
        >
          <option value="">Choose an agent</option>
          ${agents.map(
            (item) => html`<option key=${item.name} value=${item.name}>
              ${item.name}
            </option>`
          )}
        </select>
      </label>
      <p class="channel-hint">
        New messages from this chat go to this agent. People can switch with /agent.
      </p>
      ${agents.length === 0
        ? html`<p class="channel-hint">
            No agents are configured yet.
            <a href="#/agents">Create an agent</a> first.
          </p>`
        : null}

      ${error
        ? html`<div class="channel-field-error" role="alert">
            <${ErrorNotice} message=${error} details=${details} />
          </div>`
        : null}

      <div class="dialog-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${busy}
          onClick=${onClose}
        >Cancel</button>
        <button
          type="submit"
          class="button-ui button-ui-primary"
          disabled=${busy}
        >${busy ? "Adding…" : "Add chat"}</button>
      </div>
    </form>
  </${Dialog}>`;
}

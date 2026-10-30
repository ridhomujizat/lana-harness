import {
  html,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
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

/* REQ-WEB-052: the channels list and the Telegram detail. REQ-WEB-053: the
   add-chat dialog with the chats that wrote to the bot without a route.
   The token value never reaches this file: the API only returns token_env and
   token_present. */

const RESTART_NOTE = "Saved. Restart the daemon for the change to apply.";
const DEFAULT_TOKEN_ENV = "TELEGRAM_BOT_TOKEN";
const TOKEN_ENV_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

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

  const owners = telegram && Array.isArray(telegram.owners) ? telegram.owners : [];
  const chats = telegram && Array.isArray(telegram.chats) ? telegram.chats : [];  const ownerIds = owners
    .map((owner) => Number(owner.id))
    .filter((id) => Number.isFinite(id));
  const zeroChats = chats.length === 0;

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
      setSaveError("The config version is unavailable. Reload the page and try again.");
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
    const token = tokenEnv.trim();
    if (!TOKEN_ENV_RE.test(token)) {
      setSaveError(
        "The token variable must use letters, digits, and underscores, and must not start with a digit."
      );
      setSaveDetails([]);
      return;
    }
    await putTelegram(
      {
        enabled,
        token_env: token,
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

      <section class="agent-form-section" aria-labelledby="channel-token-title">
        <div class="agent-form-copy">
          <h2 id="channel-token-title">Bot token</h2>
          <p>The env variable that holds the token from @BotFather.</p>
        </div>
        <div class="agent-form-controls">
          <label class="agent-form-field">
            <span class="agent-form-field-label">Token variable</span>
            <input
              class="agent-number-input"
              type="text"
              name="token_env"
              value=${tokenEnv}
              autocomplete="off"
              spellcheck="false"
              onInput=${(event) => setTokenEnv(event.target.value)}
            />
          </label>
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
      </section>

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

      <section class="agent-form-section" aria-labelledby="channel-advanced-title">
        <div class="agent-form-copy">
          <h2 id="channel-advanced-title">Advanced</h2>
          <p>Change only for a local Bot API server.</p>
        </div>
        <div class="agent-form-controls">
          <label class="agent-form-field">
            <span class="agent-form-field-label">API base</span>
            <input
              class="agent-number-input"
              type="text"
              name="api_base"
              value=${apiBase}
              autocomplete="off"
              spellcheck="false"
              placeholder="Default Bot API server"
              onInput=${(event) => setApiBase(event.target.value)}
            />
          </label>
        </div>
      </section>
    </form>

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

import { request, errorMessage, ApiError } from "/assets/api.js";

/* Network calls for the channels views (API-WEB-028). Each function returns a
   plain object so the views never touch a raw response, and the views never
   call fetch themselves. */

async function call(method, path, body) {
  try {
    return await request(method, path, body);
  } catch (failure) {
    if (failure instanceof ApiError) {
      throw failure;
    }
    throw new ApiError(0, "network_error", errorMessage(failure), []);
  }
}

function text(value) {
  return typeof value === "string" ? value : "";
}

function requireVersion(data) {
  const version = text(data && data.version);
  if (!version) {
    throw new ApiError(
      0,
      "invalid_response",
      "The daemon did not return a config version.",
      []
    );
  }
  return version;
}

/* GET /v1/channels. The config version is included when the daemon returns it;
   the Telegram view falls back to readConfigVersion when it does not. */
export async function listChannels() {
  const data = await call("GET", "/v1/channels");
  return {
    channels: Array.isArray(data && data.channels) ? data.channels : [],
    version: text(data && data.version),
  };
}

/* GET /v1/config: the version used for expected_version on every write. */
export async function readConfigVersion() {
  const data = await call("GET", "/v1/config");
  return requireVersion(data);
}

/* GET /v1/agents: the agent picker in the add-chat dialog. */
export async function listAgents() {
  const data = await call("GET", "/v1/agents");
  return {
    agents: Array.isArray(data && data.agents) ? data.agents : [],
  };
}

/* PUT /v1/channels/telegram. The caller adds expected_version; the token value
   is never part of the body. */
export async function saveTelegram(body) {
  const data = await call("PUT", "/v1/channels/telegram", body);
  return {
    version: text(data && data.version),
    restart_required: Boolean(data && data.restart_required),
  };
}

/* POST /v1/channels/telegram/check: the Bot API getMe result. */
export async function checkTelegram() {
  const data = await call("POST", "/v1/channels/telegram/check");
  return {
    ok: Boolean(data && data.ok),
    bot_username: text(data && data.bot_username),
    message: text(data && data.message),
  };
}

/* GET /v1/channels/telegram/waiting: chats that wrote without a route. */
export async function listWaitingChats() {
  const data = await call("GET", "/v1/channels/telegram/waiting");
  return {
    chats: Array.isArray(data && data.chats) ? data.chats : [],
  };
}

/* PUT /v1/channels/telegram/routes/{chat_id}: one [[routes]] entry. */
export async function saveTelegramRoute(chatId, body) {
  const path =
    "/v1/channels/telegram/routes/" + encodeURIComponent(String(chatId));
  const data = await call("PUT", path, body);
  return {
    chat_id: data && data.chat_id !== undefined ? data.chat_id : chatId,
    agent: text(data && data.agent),
    restart_required: Boolean(data && data.restart_required),
  };
}

/* DELETE /v1/channels/telegram/routes/{chat_id}. */
export async function deleteTelegramRoute(chatId, body) {
  const path =
    "/v1/channels/telegram/routes/" + encodeURIComponent(String(chatId));
  const data = await call("DELETE", path, body);
  return {
    chat_id: data && data.chat_id !== undefined ? data.chat_id : chatId,
    restart_required: Boolean(data && data.restart_required),
  };
}

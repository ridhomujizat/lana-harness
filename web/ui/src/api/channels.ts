import { ApiError, errorMessage, request } from "./client";
import { listAgents } from "./agents";

/* Network calls for the channels views (API-WEB-028, API-TGC-001 to
   API-TGC-003). Each function returns a plain object so the views never touch
   a raw response, and the views never call fetch themselves. */

export { errorMessage } from "./client";

async function call<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  try {
    return await request<T>(method, path, body);
  } catch (failure) {
    if (failure instanceof ApiError) {
      throw failure;
    }
    throw new ApiError(0, "network_error", errorMessage(failure), []);
  }
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function requireVersion(data: VersionedResponse | null): string {
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

export interface VersionedResponse {
  version?: string;
}

export interface ChannelChat {
  chat_id: number;
  name: string;
  type: string;
  agent: string;
}

export interface ChannelOwner {
  id: number;
  name: string;
}

export interface TelegramChannelCard {
  name: string;
  status: string;
  state: string;
  enabled: boolean;
  token_env: string;
  token_present: boolean;
  bot_username: string;
  bot_url: string;
  chats: ChannelChat[];
  owners: ChannelOwner[];
  chat_count: number;
  owner_count: number;
  polling_since: number | null;
  last_message_at: number | null;
  /* Not part of telegramCardJSON today; the Advanced section falls back to "".
     Kept optional so a daemon that adds the field still type-checks. */
  api_base?: string;
}

export interface SimpleChannelCard {
  name: string;
  status: string;
  addr?: string;
}

export type ChannelCard = TelegramChannelCard | SimpleChannelCard;

export interface ChannelsResponse {
  channels: ChannelCard[];
  version?: string;
}

export interface ChannelsList {
  channels: ChannelCard[];
  version: string;
}

export interface ConfigVersionResponse {
  version: string;
}

export interface TelegramSaveBody {
  enabled?: boolean;
  token_env?: string;
  owner_ids?: number[];
  api_base?: string;
  expected_version: string;
}

export interface TelegramSaveResponse {
  version: string;
  restart_required: boolean;
}

export interface TelegramCheckResponse {
  ok: boolean;
  bot_username: string;
  message: string;
}

export interface TelegramTokenResponse {
  bot_username: string;
  bot_url: string;
  token_env: string;
  version: string;
  restart_required: boolean;
}

export interface TelegramSeenChat {
  chat_id: number;
  type: string;
  name: string;
  sender_id: number;
  sender_name: string;
  last_message_at: number;
}

export interface TelegramDiscoverResponse {
  bot_username: string;
  bot_url: string;
  chats: TelegramSeenChat[];
}

export interface TelegramConnectInput {
  chat_id: number;
  sender_id: number;
  agent: string;
}

export interface WaitingChat {
  chat_id: number;
  name: string;
  type: string;
  last_message_at: number;
}

export interface WaitingChatsResponse {
  chats: WaitingChat[];
}

export interface TelegramRouteResponse {
  chat_id: number;
  agent: string;
  restart_required: boolean;
}

export interface TelegramRouteDeleteResponse {
  chat_id: number;
  restart_required: boolean;
}

export interface TelegramRouteBody {
  agent: string;
  name?: string;
  expected_version: string;
}

export interface TelegramRouteDeleteBody {
  expected_version: string;
}

/* GET /v1/channels. The config version is included when the daemon returns it;
   the Telegram view falls back to readConfigVersion when it does not. */
export async function listChannels(): Promise<ChannelsList> {
  const data = await call<ChannelsResponse>("GET", "/v1/channels");
  return {
    channels: Array.isArray(data && data.channels) ? data.channels : [],
    version: text(data && data.version),
  };
}

/* GET /v1/config: the version used for expected_version on every write. */
export async function readConfigVersion(): Promise<string> {
  const data = await call<ConfigVersionResponse>("GET", "/v1/config");
  return requireVersion(data);
}

export { listAgents };

/* GET /v1/agents: the agent picker in the add-chat dialog. */
export async function listChannelAgents() {
  return listAgents();
}

/* PUT /v1/channels/telegram. The caller adds expected_version; the token value
   is never part of the body. */
export async function saveTelegram(
  body: TelegramSaveBody
): Promise<TelegramSaveResponse> {
  const data = await call<TelegramSaveResponse>(
    "PUT",
    "/v1/channels/telegram",
    body
  );
  return {
    version: text(data && data.version),
    restart_required: Boolean(data && data.restart_required),
  };
}

/* POST /v1/channels/telegram/check: the Bot API getMe result. */
export async function checkTelegram(): Promise<TelegramCheckResponse> {
  const data = await call<TelegramCheckResponse>(
    "POST",
    "/v1/channels/telegram/check"
  );
  return {
    ok: Boolean(data && data.ok),
    bot_username: text(data && data.bot_username),
    message: text(data && data.message),
  };
}

/* GET /v1/channels/telegram/waiting: chats that wrote without a route. */
export async function listWaitingChats(): Promise<WaitingChatsResponse> {
  const data = await call<WaitingChatsResponse>(
    "GET",
    "/v1/channels/telegram/waiting"
  );
  return {
    chats: Array.isArray(data && data.chats) ? data.chats : [],
  };
}

/* PUT /v1/channels/telegram/routes/{chat_id}: one [[routes]] entry. */
export async function saveTelegramRoute(
  chatId: number,
  body: TelegramRouteBody
): Promise<TelegramRouteResponse> {
  const path =
    "/v1/channels/telegram/routes/" + encodeURIComponent(String(chatId));
  const data = await call<TelegramRouteResponse>("PUT", path, body);
  return {
    chat_id: data && data.chat_id !== undefined ? data.chat_id : chatId,
    agent: text(data && data.agent),
    restart_required: Boolean(data && data.restart_required),
  };
}

/* DELETE /v1/channels/telegram/routes/{chat_id}. */
export async function deleteTelegramRoute(
  chatId: number,
  body: TelegramRouteDeleteBody
): Promise<TelegramRouteDeleteResponse> {
  const path =
    "/v1/channels/telegram/routes/" + encodeURIComponent(String(chatId));
  const data = await call<TelegramRouteDeleteResponse>("DELETE", path, body);
  return {
    chat_id: data && data.chat_id !== undefined ? data.chat_id : chatId,
    restart_required: Boolean(data && data.restart_required),
  };
}

/* API-TGC-001: the token travels only in this request body. */
export function putTelegramToken(token: string): Promise<TelegramTokenResponse> {
  return request<TelegramTokenResponse>(
    "PUT",
    "/v1/channels/telegram/token",
    { token }
  );
}

/* API-TGC-002: read-only getUpdates; no offset confirmation. */
export function discoverTelegram(): Promise<TelegramDiscoverResponse> {
  return request<TelegramDiscoverResponse>(
    "POST",
    "/v1/channels/telegram/discover"
  );
}

/* API-TGC-003: route the seen chat and turn the channel on. */
export function connectTelegram(
  version: string,
  chat: TelegramConnectInput
): Promise<TelegramSaveResponse> {
  return request<TelegramSaveResponse>(
    "POST",
    "/v1/channels/telegram/connect",
    {
      version,
      chat_id: chat.chat_id,
      sender_id: chat.sender_id,
      agent: chat.agent,
    }
  );
}

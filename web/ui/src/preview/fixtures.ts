import { mockToolbox } from "./toolbox-fixtures";
import { mockWorkflows } from "./workflow-fixtures";
import { mockUpdate } from "./update-fixtures";
import { mockCalendar } from "./calendar-fixtures";
import {
  AVATAR_CLOUD,
  AVATAR_ELLIPSE,
  AVATAR_SQUIRCLE,
  AVATAR_TRIANGLE,
  mockAvatar,
} from "./avatar-fixtures";
import type {
  Agent,
  AgentCreatedResponse,
  AgentFileResponse,
  ListAgentsResponse,
  PutAgentResponse,
} from "../api/agents";
import type { ApprovalAnswerResponse } from "../api/approvals";
import type { UploadedAttachment } from "../api/attachments";
import type { AuditPage } from "../api/audit";
import type {
  AuthSession,
  AuthState,
  CsrfResponse,
  ListBrowserSessionsResponse,
  ResetTelegramResponse,
  RevokeOtherBrowsersResponse,
} from "../api/auth";
import type {
  ChannelCard,
  ChannelsResponse,
  TelegramCheckResponse,
  TelegramDiscoverResponse,
  TelegramRouteDeleteResponse,
  TelegramRouteResponse,
  TelegramSaveResponse,
  TelegramTokenResponse,
  WaitingChatsResponse,
} from "../api/channels";
import type {
  CancelRestartResponse,
  DaemonInfo,
  RestartDaemonResponse,
} from "../api/daemon";
import type { Dashboard, DashboardSeriesPoint } from "../api/dashboard";
import type { BackgroundJob } from "../api/jobs";
import type {
  KnowledgeReport,
  KnowledgeStatus,
  KnowledgeUploadResponse,
} from "../api/knowledge";
import type {
  ListMcpResponse,
  McpRestartState,
  McpSaveResponse,
  McpTestResult,
} from "../api/mcp";
import type { ModelsResponse } from "../api/models";
import type { QuestionAnswerResponse } from "../api/questions";
import type {
  AgentOverview,
  MarkRoomReadResponse,
  RoomRow,
  RoomSegmentsResponse,
  Segment,
  SendRoomResponse,
} from "../api/rooms";
import type {
  ListSecretsResponse,
  SecretWriteResponse,
} from "../api/secrets";
import type {
  ListEventsResponse,
  ListSessionsResponse,
  Session,
  SessionDetail,
  SessionEvent,
} from "../api/sessions";
import type {
  GetConfigResponse,
  ListProvidersResponse,
  ProviderSaveResponse,
  ProviderTestResponse,
  PutConfigResponse,
  SaveWebResponse,
} from "../api/settings";
import type { SetupSaveResponse, SetupStatus } from "../api/setup";
import type {
  DeleteSkillResponse,
  ListSkillsResponse,
  PutSkillResponse,
  SkillContentResponse,
} from "../api/skills";
import type {
  TeamCreatedResponse,
  TeamDeletedResponse,
  TeamResponse,
} from "../api/teams";
import type { ToolCatalogResponse } from "../api/tools";
import type {
  CancelTurnResponse,
  CompactSessionResponse,
  SendMessageResponse,
} from "../api/turns";

/* Fixture data for the dev-only preview harness (web/ui/preview.html). Every
   fixture is typed with the real response shape from src/api, so a contract
   change breaks the typecheck instead of the screenshots. mockApi() maps a
   method and URL to one of these; anything unmocked answers 200 with an empty
   object and a console warning. */

const world = Date.now();
const MIN = 60_000;
const HOUR = 3_600_000;
const DAY = 86_400_000;

const CONFIG_VERSION = "sha256:9f2c1a7de4b8";

/* ?state=switch for the dev screenshots: `login` answers the session check with
   401 so the logged-out auth pages render, `setup` reports first-run setup as
   needed so the wizard renders. */
const previewState = new URLSearchParams(window.location.search).get("state");

/* ------------------------------------------------------------------ auth */

export const authStateFixture: AuthState = {
  password_set: true,
  telegram_reset: true,
  locked_until: null,
};

export const authSessionFixture: AuthSession = {
  csrf: "preview-csrf-token",
  expires_at: world + 12 * HOUR,
};

const csrfFixture: CsrfResponse = { csrf: "preview-csrf-token" };
const resetTelegramFixture: ResetTelegramResponse = { sent_to: 1 };

const browserSessionsFixture: ListBrowserSessionsResponse = {
  sessions: [
    {
      id: "browser-this",
      user_agent:
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15",
      created_at: world - 3 * DAY,
      last_used_at: world - 2 * MIN,
      current: true,
    },
    {
      id: "browser-laptop",
      user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/142",
      created_at: world - 12 * DAY,
      last_used_at: world - 2 * DAY,
      current: false,
    },
  ],
};

const revokeBrowsersFixture: RevokeOtherBrowsersResponse = { revoked: 1 };

/* ----------------------------------------------------------------- daemon */

export const daemonFixture: DaemonInfo = {
  pid: 9182,
  started_at: world - 3 * HOUR,
  uptime_ms: 3 * HOUR,
  addr: "127.0.0.1:18300",
  home: "/Users/ridho/lana",
  version: "0.9.4",
  go_version: "go1.24.3",
  os: "darwin",
  arch: "arm64",
  restart_supported: true,
  restart: { state: "idle", deadline: null },
  running_turns: [],
  browsers_signed_in: 1,
};

const restartFixture: RestartDaemonResponse = {
  restart: "draining",
  running_turns: 0,
  deadline: world + 30_000,
};

const cancelRestartFixture: CancelRestartResponse = { restart: "idle" };

/* ------------------------------------------------------------------ setup */

const setupFixture: SetupStatus = {
  needed: false,
  reasons: [],
  provider_count: 2,
  agent_count: 3,
};

const setupNeededFixture: SetupStatus = {
  needed: true,
  reasons: [],
  provider_count: 0,
  agent_count: 0,
};

const setupTestFixture: ProviderTestResponse = {
  ok: true,
  latency_ms: 142,
  status: 200,
  message: "gpt-5-mini answered in 142 ms",
};

const setupSaveFixture: SetupSaveResponse = { restart: "draining", pid: 9182 };

const claudeAgentFixture = {
  configured_agents: [],
  binary: "/usr/local/bin/claude",
  version: "2.1.319",
  version_ok: true,
  logged_in: true,
  auth_method: "oauth",
  error: "",
  running: 0,
  queued: 0,
  max_concurrent: 2,
  accounts: [
    {
      name: "claude-code",
      builtin: true,
      binary: "/usr/local/bin/claude",
      version: "2.1.319",
      logged_in: true,
      email: "owner@example.com",
      plan: "max",
      login_command: "claude",
    },
  ],
};

/* ------------------------------------------------------------------- agents */

const agentLana: Agent = {
  name: "lana",
  kind: "agent",
  title: "Chief of staff",
  color: "brand",
  avatar_svg: AVATAR_SQUIRCLE,
  avatar_source: "saved",
  model: "gpt-5",
  policy: "ask",
  skills: ["release-notes", "sql-style"],
  mcp: ["filesystem"],
  mcp_status: [{ name: "filesystem", state: "running", error: "" }],
  description:
    "Coordinates the other agents, keeps the knowledge vault tidy, and answers first.",
  default: false,
  provider: "openai",
  permission: "ask",
  workspace: "/Users/ridho/lana/workspace",
  disabled_tools: ["bash"],
  reasoning_effort: "medium",
  tools_count: 18,
  sessions_count: 42,
  last_active_at: world - 2 * MIN,
};

const agentDefault: Agent = {
  name: "default",
  kind: "agent",
  title: "General assistant",
  color: "gray",
  avatar_svg: AVATAR_ELLIPSE,
  avatar_source: "default",
  model: "gpt-5-mini",
  policy: "ask",
  skills: [],
  mcp: [],
  mcp_status: [],
  description: "Everyday help: drafts, summaries, and small fixes.",
  default: true,
  provider: "openai",
  permission: "ask",
  workspace: "/Users/ridho/lana/workspace",
  disabled_tools: [],
  reasoning_effort: "low",
  tools_count: 12,
  sessions_count: 27,
  last_active_at: world - 35 * MIN,
};

const agentResearcher: Agent = {
  name: "researcher",
  kind: "agent",
  title: "Research analyst",
  color: "#ff6a00",
  avatar_svg: AVATAR_CLOUD,
  avatar_source: "saved",
  model: "claude-sonnet-4-5",
  policy: "trust",
  skills: [],
  mcp: [],
  mcp_status: [],
  description:
    "Reads papers and the web, then writes short, sourced summaries.",
  default: false,
  provider: "anthropic",
  permission: "trust",
  workspace: "/Users/ridho/lana/research",
  disabled_tools: [],
  reasoning_effort: "high",
  tools_count: 14,
  sessions_count: 18,
  last_active_at: world - 8 * MIN,
};

const agentTeam: Agent = {
  name: "field-team",
  kind: "team",
  title: "Nightly triage",
  color: "success",
  avatar_svg: AVATAR_TRIANGLE,
  avatar_source: "default",
  model: null,
  policy: "ask",
  lead: "lana",
  members: ["researcher", "default"],
  skills: ["release-notes"],
  mcp: [],
  mcp_status: [],
  description: "Triage the nightly build: scan logs, then write the fix.",
  default: false,
  provider: "",
  permission: "ask",
  workspace: "/Users/ridho/lana/workspace",
  disabled_tools: [],
  tools_count: 0,
  sessions_count: 5,
  last_active_at: world - 20 * MIN,
};

export const agentsFixture: ListAgentsResponse = {
  agents: [agentDefault, agentLana, agentResearcher, agentTeam],
};

const agentCreatedFixture: AgentCreatedResponse = {
  name: "reviewer",
  restart_required: true,
};

const putAgentFixture: PutAgentResponse = {
  name: "lana",
  version: "sha256:1b2c3d4e5f6a",
  restart_required: true,
};

const agentFileFixture: AgentFileResponse = {
  content:
    "# AGENT.md\n\nYou are lana, the chief of staff. Be concise, keep the vault\ncurrent, and hand off work to the right agent.\n",
  version: "sha256:7a8b9c0d1e2f",
  characters: 214,
  limit: 20_000,
};

/* -------------------------------------------------------------- dashboard */

const dashboardSeries: DashboardSeriesPoint[] = Array.from(
  { length: 12 },
  (_unused, index) => ({
    start: world - (11 - index) * HOUR,
    input_tokens: 4_200 + index * 860,
    cached_input_tokens: 1_400 + index * 320,
    output_tokens: 720 + index * 180,
  })
);

export const dashboardFixture: Dashboard = {
  range: "24h",
  agent: "",
  agents: ["default", "field-team", "lana", "researcher"],
  from: world - 24 * HOUR,
  to: world,
  bucket_ms: HOUR,
  daemon_started_at: world - 3 * HOUR,
  now: { sessions_running: 2, sessions_waiting_input: 1 },
  summary: {
    input_tokens: 812_400,
    cached_input_tokens: 262_800,
    output_tokens: 96_500,
    turns: 193,
    turns_failed: 6,
    turns_cancelled: 9,
    tool_calls: 1_284,
    tool_errors: 37,
    tool_rejected: 12,
    provider_retries: 21,
  },
  series: dashboardSeries,
  by_model: [
    {
      provider: "openai",
      model: "gpt-5",
      requests: 118,
      input_tokens: 512_300,
      cached_input_tokens: 182_400,
      output_tokens: 61_200,
    },
    {
      provider: "openai",
      model: "gpt-5-mini",
      requests: 96,
      input_tokens: 218_700,
      cached_input_tokens: 64_100,
      output_tokens: 24_800,
    },
    {
      provider: "anthropic",
      model: "claude-sonnet-4-5",
      requests: 41,
      input_tokens: 81_400,
      cached_input_tokens: 16_300,
      output_tokens: 10_500,
    },
  ],
  by_agent: [
    {
      agent: "lana",
      turns: 96,
      input_tokens: 402_100,
      cached_input_tokens: 140_200,
      output_tokens: 48_300,
    },
    {
      agent: "researcher",
      turns: 52,
      input_tokens: 244_800,
      cached_input_tokens: 78_100,
      output_tokens: 31_200,
    },
    {
      agent: "default",
      turns: 45,
      input_tokens: 165_500,
      cached_input_tokens: 44_500,
      output_tokens: 17_000,
    },
  ],
  by_source: [
    {
      source: "turn",
      count: 193,
      input_tokens: 690_200,
      cached_input_tokens: 224_100,
      output_tokens: 88_400,
    },
    {
      source: "compaction",
      count: 14,
      input_tokens: 62_100,
      cached_input_tokens: 28_400,
      output_tokens: 4_100,
    },
    {
      source: "memory_capture",
      count: 31,
      input_tokens: 60_100,
      cached_input_tokens: 10_300,
      output_tokens: 4_000,
    },
  ],
  tools: [
    {
      name: "read_file",
      calls: 412,
      errors: 3,
      rejected: 0,
      avg_ms: 41,
      p95_ms: 120,
      last_error: "no such file: internal/web/sessions.go",
      last_error_at: world - 3 * HOUR,
    },
    {
      name: "edit_file",
      calls: 188,
      errors: 6,
      rejected: 2,
      avg_ms: 63,
      p95_ms: 210,
      last_error: "old_string not found",
      last_error_at: world - 90 * MIN,
    },
    {
      name: "bash",
      calls: 164,
      errors: 14,
      rejected: 8,
      avg_ms: 1_240,
      p95_ms: 4_800,
      last_error: "exit status 1: npm test",
      last_error_at: world - 22 * MIN,
    },
    {
      name: "grep",
      calls: 141,
      errors: 1,
      rejected: 0,
      avg_ms: 28,
      p95_ms: 74,
      last_error: "",
      last_error_at: 0,
    },
    {
      name: "web_search",
      calls: 78,
      errors: 9,
      rejected: 0,
      avg_ms: 880,
      p95_ms: 2_100,
      last_error: "rate limited by the search provider",
      last_error_at: world - 5 * HOUR,
    },
  ],
  turn_reasons: [
    { reason: "end_turn", count: 178 },
    { reason: "cancelled", count: 9 },
    { reason: "max_steps", count: 4 },
    { reason: "loop", count: 2 },
  ],
  turn_duration: { avg_ms: 14_200, p95_ms: 61_400 },
  errors: [
    {
      at: world - 22 * MIN,
      source: "tool",
      level: "error",
      agent: "default",
      session_id: "sess-default-tests",
      turn_id: "turn-fail-1",
      tool: "bash",
      message: "exit status 1: npm test — 2 failing tests in parser.test.ts",
    },
    {
      at: world - 5 * HOUR,
      source: "provider",
      level: "warn",
      agent: "researcher",
      session_id: "sess-researcher-dbs",
      turn_id: "turn-db-1",
      tool: "",
      message: "provider returned 429; retried after 2 s",
    },
    {
      at: world - 3 * HOUR,
      source: "turn",
      level: "error",
      agent: "lana",
      session_id: "sess-lana-tools",
      turn_id: "turn-tools-1",
      tool: "",
      message: "turn stopped after the 40-step limit",
    },
  ],
};

/* --------------------------------------------------------------- sessions */

const S_TOOLS = "sess-lana-tools";
const S_CODE = "sess-lana-code";
const S_DEFAULT = "sess-default-notes";
const S_FAIL = "sess-default-tests";
const S_DBS = "sess-researcher-dbs";
const S_PAPERS = "sess-researcher-papers";
const S_TEAM = "sess-team-triage";

interface EventRow {
  kind: string;
  at: number;
  turn: string;
  payload: Record<string, unknown>;
}

function toEvents(rows: EventRow[]): SessionEvent[] {
  return rows.map((row, index) => ({
    seq: index + 1,
    kind: row.kind,
    turn_id: row.turn,
    created_at: row.at,
    payload: row.payload,
  }));
}

function userText(text: string): Record<string, unknown> {
  return { content: [{ type: "text", text }] };
}

function assistantText(text: string): Record<string, unknown> {
  return { content: [{ type: "text", text }] };
}

const toolsEvents = toEvents([
  { kind: "turn/start", at: world - 40 * MIN, turn: "turn-tools-1", payload: {} },
  {
    kind: "user/message",
    at: world - 40 * MIN + 1_000,
    turn: "turn-tools-1",
    payload: userText(
      "My login test fails after the last deploy — can you trace it?"
    ),
  },
  {
    kind: "step/start",
    at: world - 40 * MIN + 2_000,
    turn: "turn-tools-1",
    payload: { step: 1 },
  },
  {
    kind: "assistant/message",
    at: world - 40 * MIN + 3_000,
    turn: "turn-tools-1",
    payload: {
      content: [
        {
          type: "thinking",
          text: "Start with the session middleware; the failure smells like a cookie problem.",
        },
      ],
    },
  },
  {
    kind: "tool/call",
    at: world - 40 * MIN + 4_000,
    turn: "turn-tools-1",
    payload: {
      call_id: "call-1",
      name: "read_file",
      input: { path: "internal/web/session.go" },
    },
  },
  {
    kind: "tool/result",
    at: world - 40 * MIN + 5_000,
    turn: "turn-tools-1",
    payload: {
      call_id: "call-1",
      status: "success",
      output:
        "func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {\n\tcookie := &http.Cookie{Name: \"lana\", MaxAge: 0}\n\t// ...\n}",
    },
  },
  {
    kind: "assistant/message",
    at: world - 40 * MIN + 6_000,
    turn: "turn-tools-1",
    payload: assistantText(
      "Found it — the session cookie sets `MaxAge: 0`, so the browser drops it\nright after login. Set an explicit max age and the failing test passes."
    ),
  },
  {
    kind: "turn/end",
    at: world - 40 * MIN + 7_000,
    turn: "turn-tools-1",
    payload: { reason: "max_steps" },
  },
]);

const codeEvents = toEvents([
  { kind: "turn/start", at: world - 5 * HOUR, turn: "turn-code-1", payload: {} },
  {
    kind: "user/message",
    at: world - 5 * HOUR + 1_000,
    turn: "turn-code-1",
    payload: userText("How does the deploy pipeline pick a version?"),
  },
  {
    kind: "assistant/message",
    at: world - 5 * HOUR + 2_000,
    turn: "turn-code-1",
    payload: assistantText(
      "The pipeline reads the tag from `git describe` and stamps it into the\nbinary.\n\n### Steps\n\n1. Read the latest tag\n2. Stamp `main.version` with `-ldflags`\n3. Build and push\n\n```go\nfunc Version() string {\n\treturn buildVersion\n}\n```\n\nThat way `lana --version` always matches the tag."
    ),
  },
  {
    kind: "turn/end",
    at: world - 5 * HOUR + 3_000,
    turn: "turn-code-1",
    payload: { reason: "end_turn" },
  },
]);

const notesEvents = toEvents([
  { kind: "turn/start", at: world - 35 * MIN, turn: "turn-notes-1", payload: {} },
  {
    kind: "user/message",
    at: world - 35 * MIN + 1_000,
    turn: "turn-notes-1",
    payload: userText("Summarize the release notes for 0.9.4."),
  },
  {
    kind: "assistant/message",
    at: world - 35 * MIN + 2_000,
    turn: "turn-notes-1",
    payload: assistantText(
      "**0.9.4** lands the rooms redesign, per-column chat scrolling, and a\nfaster knowledge compile. No breaking changes."
    ),
  },
  {
    kind: "turn/end",
    at: world - 35 * MIN + 3_000,
    turn: "turn-notes-1",
    payload: { reason: "end_turn" },
  },
]);

const failEvents = toEvents([
  { kind: "turn/start", at: world - 22 * MIN, turn: "turn-fail-1", payload: {} },
  {
    kind: "user/message",
    at: world - 22 * MIN + 1_000,
    turn: "turn-fail-1",
    payload: userText("Write unit tests for the parser."),
  },
  {
    kind: "tool/call",
    at: world - 22 * MIN + 2_000,
    turn: "turn-fail-1",
    payload: {
      call_id: "call-fail-1",
      name: "bash",
      input: { command: "npm test" },
    },
  },
  {
    kind: "tool/result",
    at: world - 22 * MIN + 3_000,
    turn: "turn-fail-1",
    payload: {
      call_id: "call-fail-1",
      status: "error",
      output: "2 failing tests in src/lib/format.test.ts",
    },
  },
  {
    kind: "turn/end",
    at: world - 22 * MIN + 4_000,
    turn: "turn-fail-1",
    payload: { reason: "error", error: { message: "exit status 1: npm test" } },
  },
]);

const dbsEvents = toEvents([
  { kind: "turn/start", at: world - 2 * HOUR, turn: "turn-db-1", payload: {} },
  {
    kind: "user/message",
    at: world - 2 * HOUR + 1_000,
    turn: "turn-db-1",
    payload: userText("Compare pgvector and Qdrant for the knowledge vault."),
  },
  {
    kind: "assistant/message",
    at: world - 2 * HOUR + 2_000,
    turn: "turn-db-1",
    payload: assistantText(
      "**pgvector** keeps everything in Postgres you already run; **Qdrant**\nscales further and filters faster.\n\n| Option | Ops cost | Filtering |\n| --- | --- | --- |\n| pgvector | low | good |\n| Qdrant | medium | great |"
    ),
  },
  {
    kind: "turn/end",
    at: world - 2 * HOUR + 3_000,
    turn: "turn-db-1",
    payload: { reason: "end_turn" },
  },
]);

const papersEvents = toEvents([
  { kind: "turn/start", at: world - 9 * HOUR, turn: "turn-papers-1", payload: {} },
  {
    kind: "user/message",
    at: world - 9 * HOUR + 1_000,
    turn: "turn-papers-1",
    payload: userText("Find recent papers on context caching."),
  },
  {
    kind: "assistant/message",
    at: world - 9 * HOUR + 2_000,
    turn: "turn-papers-1",
    payload: assistantText(
      "Three good ones: prefix caching for shared prompts, KV-cache offloading\nto host memory, and a survey of block-sparse attention."
    ),
  },
  {
    kind: "turn/end",
    at: world - 9 * HOUR + 3_000,
    turn: "turn-papers-1",
    payload: { reason: "end_turn" },
  },
]);

const teamEvents = toEvents([
  { kind: "turn/start", at: world - 20 * MIN, turn: "turn-team-1", payload: {} },
  {
    kind: "user/message",
    at: world - 20 * MIN + 1_000,
    turn: "turn-team-1",
    payload: userText("Triage the failing nightly build."),
  },
  {
    kind: "assistant/message",
    at: world - 20 * MIN + 2_000,
    turn: "turn-team-1",
    payload: assistantText(
      "I'll split this: researcher scans the logs, default writes the fix."
    ),
  },
  {
    kind: "tool/call",
    at: world - 20 * MIN + 3_000,
    turn: "turn-team-1",
    payload: {
      call_id: "call-team-1",
      name: "task",
      input: {
        agent: "researcher",
        title: "Scan the nightly logs",
        prompt: "Find the first failing step in the nightly log.",
      },
    },
  },
  {
    kind: "tool/result",
    at: world - 20 * MIN + 5_000,
    turn: "turn-team-1",
    payload: {
      call_id: "call-team-1",
      status: "success",
      output: JSON.stringify({
        session_id: "sess-child-1",
        agent: "researcher",
        status: "success",
        answer: "The failure is in the lint step: two unused imports.",
        tool_calls: 3,
      }),
    },
  },
  {
    kind: "assistant/message",
    at: world - 20 * MIN + 6_000,
    turn: "turn-team-1",
    payload: assistantText(
      "researcher found it in the lint step. default has opened a fix."
    ),
  },
  {
    kind: "turn/end",
    at: world - 20 * MIN + 7_000,
    turn: "turn-team-1",
    payload: { reason: "end_turn" },
  },
]);

function eventsResponse(events: SessionEvent[]): ListEventsResponse {
  return { events, last_seq: events.length };
}

const eventsById: Record<string, ListEventsResponse> = {
  [S_TOOLS]: eventsResponse(toolsEvents),
  [S_CODE]: eventsResponse(codeEvents),
  [S_DEFAULT]: eventsResponse(notesEvents),
  [S_FAIL]: eventsResponse(failEvents),
  [S_DBS]: eventsResponse(dbsEvents),
  [S_PAPERS]: eventsResponse(papersEvents),
  [S_TEAM]: eventsResponse(teamEvents),
};

function sessionOf(
  id: string,
  agent: string,
  title: string,
  status: string,
  preview: string,
  updatedAt: number,
  createdAt: number,
  turnCount: number,
  channel: string,
  team: Session["team"] = null
): Session {
  return {
    id,
    channel,
    address: "owner",
    agent,
    title,
    status,
    preview,
    turn_count: turnCount,
    created_at: createdAt,
    updated_at: updatedAt,
    in_use: "none",
    holder_pid: null,
    team,
  };
}

const teamRef = { name: "field-team", lead: "lana", members: ["researcher", "default"] };

export const sessionsFixture: Session[] = [
  sessionOf(
    S_TOOLS,
    "lana",
    "Trace the failing login test",
    "running",
    "Found it — the session cookie sets MaxAge: 0…",
    world - 40 * MIN + 7_000,
    world - 2 * HOUR,
    4,
    "web"
  ),
  sessionOf(
    S_CODE,
    "lana",
    "How the deploy pipeline picks a version",
    "idle",
    "The pipeline reads the tag from git describe…",
    world - 5 * HOUR + 3_000,
    world - 6 * HOUR,
    3,
    "web"
  ),
  sessionOf(
    S_DEFAULT,
    "default",
    "Summarize the release notes",
    "idle",
    "0.9.4 lands the rooms redesign and faster knowledge compile.",
    world - 35 * MIN + 3_000,
    world - 50 * MIN,
    2,
    "cli"
  ),
  sessionOf(
    S_FAIL,
    "default",
    "Write unit tests for the parser",
    "failed",
    "exit status 1: npm test — 2 failing tests",
    world - 22 * MIN + 4_000,
    world - 30 * MIN,
    1,
    "web"
  ),
  sessionOf(
    S_DBS,
    "researcher",
    "Compare vector databases",
    "waiting_input",
    "pgvector vs Qdrant for the knowledge vault…",
    world - 2 * HOUR + 3_000,
    world - 3 * HOUR,
    3,
    "web"
  ),
  sessionOf(
    S_PAPERS,
    "researcher",
    "Find papers on context caching",
    "idle",
    "Three good ones, including prefix caching…",
    world - 9 * HOUR + 3_000,
    world - 10 * HOUR,
    2,
    "telegram"
  ),
  sessionOf(
    S_TEAM,
    "field-team",
    "Nightly build triage",
    "running",
    "researcher found the failure in the lint step.",
    world - 20 * MIN + 7_000,
    world - 25 * MIN,
    4,
    "web",
    teamRef
  ),
];

export const sessionsListFixture: ListSessionsResponse = {
  sessions: sessionsFixture,
  next_before: null,
  facets: {
    channel: { web: 5, cli: 1, telegram: 1 },
    status: { running: 2, waiting_input: 1, idle: 3, failed: 1 },
    agent: [
      { name: "default", kind: "agent", count: 2 },
      { name: "field-team", kind: "team", count: 1 },
      { name: "lana", kind: "agent", count: 2 },
      { name: "researcher", kind: "agent", count: 2 },
    ],
  },
};

function detailOf(
  session: Session,
  extra: Partial<SessionDetail>
): SessionDetail {
  return {
    ...session,
    model: "gpt-5",
    policy: "ask",
    reasoning_effort: "",
    workspace: "/Users/ridho/lana/workspace",
    parent_session_id: "",
    turn_running: session.status === "running",
    ...extra,
  };
}

const detailsById: Record<string, SessionDetail> = {
  [S_TOOLS]: detailOf(sessionsFixture[0] as Session, {
    model: "openai:gpt-5",
    reasoning_effort: "medium",
  }),
  [S_CODE]: detailOf(sessionsFixture[1] as Session, {
    model: "openai:gpt-5",
  }),
  [S_DEFAULT]: detailOf(sessionsFixture[2] as Session, {
    model: "openai:gpt-5-mini",
    reasoning_effort: "low",
  }),
  [S_FAIL]: detailOf(sessionsFixture[3] as Session, {
    model: "openai:gpt-5-mini",
  }),
  [S_DBS]: detailOf(sessionsFixture[4] as Session, {
    model: "anthropic:claude-sonnet-4-5",
    policy: "trust",
    reasoning_effort: "high",
  }),
  [S_PAPERS]: detailOf(sessionsFixture[5] as Session, {
    model: "anthropic:claude-sonnet-4-5",
  }),
  [S_TEAM]: detailOf(sessionsFixture[6] as Session, {
    model: "",
    policy: "ask",
  }),
};

/* ------------------------------------------------------------------ rooms */

function presence(state: string, label: string, since: number, session: string) {
  return { state, label, since, session_id: session };
}

export const roomsFixture: RoomRow[] = [
  {
    name: "lana",
    kind: "agent",
    title: "Chief of staff",
    color: "brand",
    avatar_svg: AVATAR_SQUIRCLE,
    avatar_source: "saved",
    members: [],
    lead: "",
    presence: presence("working", "Working", world - 40 * MIN, S_TOOLS),
    last_message: {
      role: "assistant",
      agent: "lana",
      preview: "Found it — the session cookie sets MaxAge: 0…",
      at: world - 2 * MIN,
      session_id: S_TOOLS,
      channel: "web",
    },
    unread: 2,
    turns_7d: 96,
    last_active_at: world - 2 * MIN,
  },
  {
    name: "default",
    kind: "agent",
    title: "General assistant",
    color: "gray",
    avatar_svg: AVATAR_ELLIPSE,
    avatar_source: "default",
    members: [],
    lead: "",
    presence: presence("online", "Online", world - 4 * HOUR, S_DEFAULT),
    last_message: {
      role: "assistant",
      agent: "default",
      preview: "0.9.4 lands the rooms redesign and faster knowledge compile.",
      at: world - 35 * MIN,
      session_id: S_DEFAULT,
      channel: "cli",
    },
    unread: 0,
    turns_7d: 45,
    last_active_at: world - 35 * MIN,
  },
  {
    name: "researcher",
    kind: "agent",
    title: "Research analyst",
    color: "#ff6a00",
    avatar_svg: AVATAR_CLOUD,
    avatar_source: "saved",
    members: [],
    lead: "",
    presence: presence("waiting", "Needs you", world - 2 * HOUR, S_DBS),
    last_message: {
      role: "assistant",
      agent: "researcher",
      preview: "pgvector vs Qdrant — which one should I wire up?",
      at: world - 8 * MIN,
      session_id: S_DBS,
      channel: "web",
    },
    unread: 1,
    turns_7d: 52,
    last_active_at: world - 8 * MIN,
  },
  {
    name: "field-team",
    kind: "team",
    title: "Nightly triage",
    color: "success",
    avatar_svg: AVATAR_TRIANGLE,
    avatar_source: "default",
    members: ["researcher", "default"],
    lead: "lana",
    presence: presence("working", "Working", world - 20 * MIN, S_TEAM),
    last_message: {
      role: "assistant",
      agent: "lana",
      preview: "researcher found the failure in the lint step.",
      at: world - 18 * MIN,
      session_id: S_TEAM,
      channel: "web",
    },
    unread: 1,
    turns_7d: 5,
    last_active_at: world - 18 * MIN,
  },
];

function segment(
  sessionId: string,
  channel: string,
  status: string,
  createdAt: number,
  updatedAt: number,
  turnCount: number
): Segment {
  return {
    session_id: sessionId,
    channel,
    address: "owner",
    created_at: createdAt,
    updated_at: updatedAt,
    turn_count: turnCount,
    status,
  };
}

const segmentsByRoom: Record<string, Segment[]> = {
  lana: [
    segment(S_CODE, "web", "idle", world - 6 * HOUR, world - 5 * HOUR, 3),
    segment(S_TOOLS, "web", "running", world - 2 * HOUR, world - 39 * MIN, 4),
  ],
  default: [
    segment(S_DEFAULT, "cli", "idle", world - 50 * MIN, world - 34 * MIN, 2),
    segment(S_FAIL, "web", "failed", world - 30 * MIN, world - 21 * MIN, 1),
  ],
  researcher: [
    segment(S_PAPERS, "telegram", "idle", world - 10 * HOUR, world - 9 * HOUR, 2),
    segment(S_DBS, "web", "waiting_input", world - 3 * HOUR, world - 8 * MIN, 3),
  ],
  "field-team": [
    segment(S_TEAM, "web", "running", world - 25 * MIN, world - 19 * MIN, 4),
  ],
};

export const agentOverviewFixture: AgentOverview = {
  name: "lana",
  kind: "agent",
  title: "Chief of staff",
  color: "brand",
  avatar_svg: AVATAR_SQUIRCLE,
  avatar_source: "saved",
  persona_line: "Keep the workspace moving and the vault honest.",
  created_at: world - 41 * DAY,
  presence: presence("working", "Working", world - 40 * MIN, S_TOOLS),
  now: {
    session_id: S_TOOLS,
    turn_id: "turn-tools-1",
    started_at: world - 40 * MIN,
    steps: [
      { name: "Reading internal/web/session.go", status: "ok", at: world - 39 * MIN },
      { name: "Checking the login test", status: "running", at: world - 38 * MIN },
      { name: "Writing the fix", status: "pending", at: 0 },
    ],
  },
  activity: [
    {
      kind: "answered",
      title: "Traced the failing login test",
      detail: "Session cookie had MaxAge: 0.",
      at: world - 39 * MIN,
      session_id: S_TOOLS,
    },
    {
      kind: "learned",
      title: "Compiled 3 knowledge pages",
      detail: "From inbox/context-caching.md",
      at: world - 5 * HOUR,
      session_id: S_CODE,
    },
    {
      kind: "remembered",
      title: "Noted your deploy preferences",
      detail: "Tag-driven versions, no manual bumps.",
      at: world - 20 * HOUR,
      session_id: S_CODE,
    },
    {
      kind: "approval",
      title: "Asked to run npm test",
      detail: "Policy ask",
      at: world - 22 * MIN,
      session_id: S_FAIL,
    },
  ],
  week: {
    days: [
      { day: "Mon", turns: 12 },
      { day: "Tue", turns: 18 },
      { day: "Wed", turns: 9 },
      { day: "Thu", turns: 21 },
      { day: "Fri", turns: 16 },
      { day: "Sat", turns: 4 },
      { day: "Sun", turns: 16 },
    ],
    turns: 96,
    questions: 7,
    notes: 14,
    tokens: 402_100,
  },
  memory: {
    entries: [
      "Prefers concise answers with a short rationale",
      "Runs the nightly build triage every morning",
      "Uses gpt-5 for lana and gpt-5-mini for default",
    ],
    total: 6,
  },
  job: "Coordinate the agents, keep the knowledge vault current, and answer first.",
  looks_after: {
    channels: [
      { channel: "web", match: "owner" },
      { channel: "telegram", match: "owner chat" },
    ],
    teams: ["field-team"],
  },
};

/* ------------------------------------------------------------------- team */

const teamFixture: TeamResponse = {
  name: "field-team",
  lead: "lana",
  members: ["researcher", "default"],
  description: "Triage the nightly build: scan logs, then write the fix.",
  version: CONFIG_VERSION,
  restart_required: false,
};

const teamCreatedFixture: TeamCreatedResponse = {
  name: "field-team",
  kind: "team",
  version: CONFIG_VERSION,
  restart_required: true,
};

const teamDeletedFixture: TeamDeletedResponse = {
  name: "field-team",
  deleted: true,
  version: CONFIG_VERSION,
  restart_required: true,
};

/* ----------------------------------------------------------------- skills */

const skillGlobal = {
  name: "release-notes",
  scope: "global",
  description: "Turn a set of commits into short, user-facing release notes.",
  valid: true,
  error: "",
  size: 1_820,
  updated_at: world - 6 * DAY,
};

const skillAgent = {
  name: "sql-style",
  scope: "agent:lana",
  description: "House style for writing and reviewing SQL queries.",
  valid: true,
  error: "",
  size: 940,
  updated_at: world - 2 * DAY,
};

const skillInvalid = {
  name: "broken-skill",
  scope: "agent:researcher",
  description: "",
  valid: false,
  error: "missing frontmatter description",
  size: 120,
  updated_at: world - 3 * DAY,
};

export const skillsFixture: ListSkillsResponse = {
  skills: [skillGlobal, skillAgent],
  invalid: [skillInvalid],
};

const skillContentFixture: SkillContentResponse = {
  content:
    "---\nname: release-notes\ndescription: Turn a set of commits into short, user-facing release notes.\n---\n\nSummarize the commits as 2-4 bullets a user can skim.\n",
  version: "sha256:a1b2c3d4e5f6",
  path: "/Users/ridho/lana/skills/release-notes/SKILL.md",
};

const putSkillFixture: PutSkillResponse = {
  version: "sha256:b2c3d4e5f6a1",
  path: "/Users/ridho/lana/skills/release-notes/SKILL.md",
};

const deleteSkillFixture: DeleteSkillResponse = {
  name: "release-notes",
  scope: "global",
  deleted: true,
};

/* -------------------------------------------------------------- knowledge */

export const knowledgeFixture: KnowledgeStatus = {
  available: true,
  path: "/Users/ridho/lana/knowledge",
  exists: true,
  counts: { pages: 48, sources: 31, journal_days: 12 },
  sources_pending: ["inbox/context-caching.md", "inbox/rooms-redesign.md"],
  sources_failed: [
    {
      path: "inbox/scan.pdf",
      error: "could not extract text (encrypted)",
    },
  ],
  inbox_unsupported: ["inbox/diagram.sketch"],
  embedding: {
    provider: "openai",
    model: "text-embedding-3-small",
    chunks: 612,
    vectors: 612,
  },
  agent: "lana",
  agent_error: "",
  consolidation: {
    running: false,
    last_at: world - 6 * HOUR,
    last_error: "",
    next_at: world + 18 * HOUR,
    report: "merged 3 notes into 2 pages",
  },
  log: [
    {
      heading: "Today",
      lines: [
        "Compiled inbox/context-caching.md into 2 pages",
        "Merged duplicate notes on agent routing",
      ],
    },
    {
      heading: "Yesterday",
      lines: ["Created journal/day-2026-11-01.md"],
    },
  ],
};

const knowledgeReportFixture: KnowledgeReport = {
  path: "/Users/ridho/lana/knowledge/report.md",
  markdown:
    "# Lint report\n\n- 48 pages, 0 broken links\n- 2 sources pending compile\n- 1 unsupported inbox file\n",
};

const knowledgeUploadFixture: KnowledgeUploadResponse = {
  saved: ["inbox/context-caching.md", "inbox/rooms-redesign.md"],
};

/* -------------------------------------------------------------------- mcp */

export const mcpFixture: ListMcpResponse = {
  version: CONFIG_VERSION,
  servers: [
    {
      name: "filesystem",
      type: "command",
      command: "npx",
      args: [
        "-y",
        "@modelcontextprotocol/server-filesystem",
        "/Users/ridho/lana/workspace",
      ],
      url: "",
      env_keys: [],
      header_keys: [],
      tools_count: 12,
      agents: ["lana", "default"],
      state: "running",
      status: "connected",
      error: "",
      running_for_ms: 3 * HOUR,
      tools: [
        { name: "read_file", description: "Read a file from the workspace" },
        { name: "write_file", description: "Write a file in the workspace" },
        { name: "list_directory", description: "List a directory" },
        { name: "search_files", description: "Search file contents" },
      ],
    },
    {
      name: "github",
      type: "url",
      command: "",
      args: [],
      url: "http://127.0.0.1:9200/mcp",
      env_keys: [],
      header_keys: ["Authorization"],
      tools_count: 0,
      agents: [],
      state: "unavailable",
      status: "unreachable",
      error: "connection refused",
      running_for_ms: 0,
      tools: [],
    },
  ],
};

const mcpSaveFixture: McpSaveResponse = {
  name: "filesystem",
  version: "sha256:c3d4e5f6a1b2",
  restart_required: true,
};

const mcpTestFixture: McpTestResult = {
  ok: true,
  tool_count: 12,
  message: "Connected",
};

const mcpRestartFixture: McpRestartState = {
  name: "filesystem",
  state: "running",
  error: "",
};

/* ------------------------------------------------------------------ models */

export const modelsFixture: ModelsResponse = {
  models: [
    {
      id: "gpt-5",
      name: "GPT-5",
      description: "Most capable OpenAI model.",
      context_window: 400_000,
      legacy: false,
      replacement: "",
      thinking: "on",
      default_effort: "medium",
    },
    {
      id: "gpt-5-mini",
      name: "GPT-5 mini",
      description: "Fast and cheap for everyday work.",
      context_window: 200_000,
      legacy: false,
      replacement: "",
      thinking: "on",
      default_effort: "low",
    },
    {
      id: "gpt-4.1",
      name: "GPT-4.1",
      description: "Previous generation.",
      context_window: 128_000,
      legacy: true,
      replacement: "gpt-5",
      thinking: "off",
      default_effort: "",
    },
    {
      id: "claude-sonnet-4-5",
      name: "Claude Sonnet 4.5",
      description: "Balanced Anthropic model.",
      context_window: 200_000,
      legacy: false,
      replacement: "",
      thinking: "on",
      default_effort: "medium",
    },
  ],
  efforts: [
    { value: "low", description: "Faster, less deliberation." },
    { value: "medium", description: "Balanced." },
    { value: "high", description: "More deliberation for hard tasks." },
    { value: "max", description: "Most deliberation." },
  ],
};

/* ------------------------------------------------------------------- tools */

export const toolsFixture: ToolCatalogResponse = {
  agent: "lana",
  groups: [
    {
      id: "builtin",
      label: "Built-in tools",
      tools: [
        { name: "read_file", risk: "low", asks_first: false, enabled: true, source: "listed" },
        { name: "write_file", risk: "medium", asks_first: true, enabled: true, source: "listed" },
        { name: "edit_file", risk: "medium", asks_first: true, enabled: true, source: "listed" },
        { name: "glob", risk: "low", asks_first: false, enabled: true, source: "auto" },
        { name: "grep", risk: "low", asks_first: false, enabled: true, source: "auto" },
        { name: "bash", risk: "high", asks_first: true, enabled: false, source: "not_listed" },
        { name: "web_fetch", risk: "low", asks_first: false, enabled: true, source: "listed" },
        { name: "web_search", risk: "low", asks_first: false, enabled: true, source: "listed" },
        { name: "task", risk: "medium", asks_first: false, enabled: true, source: "listed" },
        { name: "memory_read", risk: "low", asks_first: false, enabled: true, source: "listed" },
        { name: "memory_write", risk: "low", asks_first: false, enabled: true, source: "listed" },
        { name: "skill_load", risk: "low", asks_first: false, enabled: true, source: "listed" },
      ],
    },
    {
      id: "mcp",
      label: "MCP · filesystem",
      tools: [
        { name: "mcp__filesystem__read_file", risk: "low", asks_first: false, enabled: true, source: "attached" },
        { name: "mcp__filesystem__write_file", risk: "medium", asks_first: true, enabled: true, source: "attached" },
      ],
    },
  ],
};

/* -------------------------------------------------------------------- jobs */

export const jobsFixture: BackgroundJob[] = [
  {
    id: "job-1",
    command: "npm test -- --run",
    status: "running",
    exit_code: 0,
    pid: 4421,
    started_at: world - 4 * MIN,
    ended_at: null,
    tail: "RUN  v5.0.5\n ✓ src/lib/format.test.ts (12 tests)\n",
  },
  {
    id: "job-2",
    command: "git log --oneline -n 20",
    status: "exited",
    exit_code: 0,
    pid: 0,
    started_at: world - 30 * MIN,
    ended_at: world - 29 * MIN,
    tail: "f91545d Merge branch 'feature/agent-rooms'\n57e1125 fix(web): chats scroll per column\n",
  },
];

/* --------------------------------------------------------------- secrets */

export const secretsFixture: ListSecretsResponse = {
  version: CONFIG_VERSION,
  restart_required: false,
  secrets: [
    {
      name: "OPENAI_API_KEY",
      state: "set",
      source: "secrets.env",
      file: "/Users/ridho/lana/secrets.env",
      overridden_by_env: false,
      updated_at: world - 9 * DAY,
      used_by: ["openai"],
    },
    {
      name: "ANTHROPIC_API_KEY",
      state: "set",
      source: "secrets.env",
      file: "/Users/ridho/lana/secrets.env",
      overridden_by_env: false,
      updated_at: world - 9 * DAY,
      used_by: ["anthropic"],
    },
    {
      name: "TELEGRAM_BOT_TOKEN",
      state: "set",
      source: "secrets.env",
      file: "/Users/ridho/lana/secrets.env",
      overridden_by_env: false,
      updated_at: world - 4 * DAY,
      used_by: ["telegram"],
    },
  ],
};

const secretWriteFixture: SecretWriteResponse = {
  name: "OPENAI_API_KEY",
  state: "set",
  restart_required: false,
};

export const providersFixture: ListProvidersResponse = {
  version: CONFIG_VERSION,
  restart_required: false,
  providers: [
    {
      name: "openai",
      kind: "openai_compat",
      base_url: "https://api.openai.com/v1",
      api_key_env: "OPENAI_API_KEY",
      key_state: "set",
      used_by: ["lana", "default"],
    },
    {
      name: "anthropic",
      kind: "openai_compat",
      base_url: "https://api.anthropic.com/v1",
      api_key_env: "ANTHROPIC_API_KEY",
      key_state: "set",
      used_by: ["researcher"],
    },
  ],
};

const providerSaveFixture: ProviderSaveResponse = {
  name: "openai",
  version: "sha256:d4e5f6a1b2c3",
  restart_required: true,
};

export const auditFixture: AuditPage = {
  entries: [
    {
      time: world - 4 * DAY,
      kind: "secret",
      target: "TELEGRAM_BOT_TOKEN",
      changed_keys: ["value"],
    },
    {
      time: world - 6 * DAY,
      kind: "agent",
      target: "lana",
      changed_keys: ["model", "reasoning_effort"],
    },
    {
      time: world - 12 * DAY,
      kind: "config",
      target: "config.toml",
      changed_keys: ["web.addr"],
    },
  ],
  next_before: null,
};

/* ----------------------------------------------------------------- config */

export const configFixture: GetConfigResponse = {
  version: CONFIG_VERSION,
  loaded_version: CONFIG_VERSION,
  restart_required: false,
  validation: { ok: true, errors: [] },
  raw:
    'web = { addr = "127.0.0.1:18300" }\n\n[providers.openai]\nkind = "openai_compat"\napi_key_env = "OPENAI_API_KEY"\n\n[agents.lana]\nprovider = "openai"\nmodel = "gpt-5"\n',
  settings: {
    providers: {
      openai: {
        kind: "openai_compat",
        base_url: "https://api.openai.com/v1",
        api_key_env: "OPENAI_API_KEY",
        timeout: "60s",
      },
      anthropic: {
        kind: "openai_compat",
        base_url: "https://api.anthropic.com/v1",
        api_key_env: "ANTHROPIC_API_KEY",
        timeout: "60s",
      },
    },
    agents: {
      lana: {
        provider: "openai",
        model: "gpt-5",
        title: "Chief of staff",
        color: "brand",
        prompt_file: "AGENT.md",
        workspace: "/Users/ridho/lana/workspace",
        tools: ["read_file", "edit_file", "task"],
        subagents: ["researcher", "default"],
        mcp: ["filesystem"],
        skills: ["release-notes", "sql-style"],
        permission: "ask",
        max_steps: 40,
        context_window: 400_000,
        memory_capture: true,
        rules: { allow: ["read_file"], deny: ["bash"] },
      },
      default: {
        provider: "openai",
        model: "gpt-5-mini",
        title: "General assistant",
        color: "gray",
        workspace: "/Users/ridho/lana/workspace",
        tools: ["read_file", "edit_file", "web_search"],
        skills: [],
        mcp: [],
        permission: "ask",
      },
      researcher: {
        provider: "anthropic",
        model: "claude-sonnet-4-5",
        title: "Research analyst",
        color: "#ff6a00",
        workspace: "/Users/ridho/lana/research",
        tools: ["read_file", "web_search", "web_fetch"],
        skills: [],
        mcp: [],
        permission: "trust",
      },
    },
    routes: [
      { channel: "telegram", agent: "lana", match: { chat_id: 555 } },
    ],
    channels: { telegram: { enabled: true, token_env: "TELEGRAM_BOT_TOKEN" } },
    mcp: {
      filesystem: {
        command: "npx",
        args: ["-y", "@modelcontextprotocol/server-filesystem", "/Users/ridho/lana/workspace"],
      },
      github: { url: "http://127.0.0.1:9200/mcp" },
    },
    modules: [{ id: "knowledge", disabled: false }],
  },
};

const putConfigFixture: PutConfigResponse = {
  version: "sha256:e5f6a1b2c3d4",
  restart_required: true,
};

const webSaveFixture: SaveWebResponse = {
  addr: "127.0.0.1:18300",
  url: "http://127.0.0.1:18300/assets/",
  version: "sha256:f6a1b2c3d4e5",
  restart_required: true,
};

/* --------------------------------------------------------------- channels */

export const channelsFixture: ChannelsResponse = {
  version: CONFIG_VERSION,
  channels: [
    {
      name: "telegram",
      status: "connected",
      state: "running",
      enabled: true,
      token_env: "TELEGRAM_BOT_TOKEN",
      token_present: true,
      bot_username: "lana_pilot_bot",
      bot_url: "https://t.me/lana_pilot_bot",
      chats: [
        { chat_id: 555, name: "Ridho", type: "private", agent: "lana" },
        { chat_id: -1002003, name: "Build squad", type: "group", agent: "field-team" },
      ],
      owners: [{ id: 555, name: "Ridho" }],
      chat_count: 2,
      owner_count: 1,
      polling_since: world - 3 * HOUR,
      last_message_at: world - 8 * MIN,
      api_base: "",
    } satisfies ChannelCard,
    { name: "web", status: "on", addr: "127.0.0.1:18300" } satisfies ChannelCard,
  ],
};

const telegramSaveFixture: TelegramSaveResponse = {
  version: "sha256:a1b2c3d4e5f6",
  restart_required: true,
};

const telegramCheckFixture: TelegramCheckResponse = {
  ok: true,
  bot_username: "lana_pilot_bot",
  message: "Token found",
};

const telegramWaitingFixture: WaitingChatsResponse = {
  chats: [
    { chat_id: 7788, name: "Nadia", type: "private", last_message_at: world - 3 * MIN },
    { chat_id: -1009911, name: "Ops", type: "group", last_message_at: world - 40 * MIN },
  ],
};

const telegramRouteFixture: TelegramRouteResponse = {
  chat_id: 555,
  agent: "lana",
  restart_required: true,
};

const telegramRouteDeleteFixture: TelegramRouteDeleteResponse = {
  chat_id: 555,
  restart_required: true,
};

const telegramTokenFixture: TelegramTokenResponse = {
  bot_username: "lana_pilot_bot",
  bot_url: "https://t.me/lana_pilot_bot",
  token_env: "TELEGRAM_BOT_TOKEN",
  version: "sha256:b2c3d4e5f6a1",
  restart_required: true,
};

const telegramDiscoverFixture: TelegramDiscoverResponse = {
  bot_username: "lana_pilot_bot",
  bot_url: "https://t.me/lana_pilot_bot",
  chats: [
    {
      chat_id: 7788,
      type: "private",
      name: "Nadia",
      sender_id: 7788,
      sender_name: "Nadia",
      last_message_at: world - 3 * MIN,
    },
  ],
};

/* ------------------------------------------------------------ attachments */

const uploadedFixture: UploadedAttachment[] = [
  {
    path: "attachments/screenshot.png",
    name: "screenshot.png",
    media_type: "image/png",
    size: 182_400,
  },
];

/* ------------------------------------------------------------------ misc */

const sendMessageFixture: SendMessageResponse = {
  turn: "turn-new-1",
  turn_id: "turn-new-1",
};

const cancelTurnFixture: CancelTurnResponse = { turn: "turn-new-1" };
const compactFixture: CompactSessionResponse = {
  tokens_before: 96_400,
  tokens_after: 21_800,
};
const approvalAnswerFixture: ApprovalAnswerResponse = { outcome: "allowed_once" };
const questionAnswerFixture: QuestionAnswerResponse = { outcome: "answered" };
const roomSendFixture: SendRoomResponse = {
  session_id: S_TOOLS,
  created: false,
  turn: "turn-new-1",
  turn_id: "turn-new-1",
};
const roomReadFixture: MarkRoomReadResponse = { read_at: world };

/* --------------------------------------------------------------- streams */

/* The rooms stream sends one full `rooms` event on connect (so the shared
   store stops its fallback poll) and then stays open. The session stream only
   sends a heartbeat comment. Neither body ever closes on its own. */
export const ROOMS_STREAM_BODY =
  ": ping\n\nevent: rooms\ndata: " +
  JSON.stringify({ rooms: roomsFixture }) +
  "\n\n";

export const SESSION_STREAM_BODY = ": connected\n\n";

/* ------------------------------------------------------------- the router */

export interface MockResult {
  status: number;
  body?: unknown;
  /* When present, main.tsx answers with a text/event-stream Response that
     emits this text and then stays open. */
  stream?: string;
}

function unknownRoute(method: string, path: string): MockResult {
  console.warn("[preview] unmocked request: " + method + " " + path);
  return { status: 200, body: {} };
}

export function mockApi(method: string, url: URL, body?: unknown): MockResult {
  const m = method.toUpperCase();
  const parts = url.pathname.split("/").filter(Boolean);
  const at = (index: number): string => parts[index] || "";
  const isGet = m === "GET";
  const isWrite = m === "POST" || m === "PUT" || m === "PATCH" || m === "DELETE";

  if (at(0) !== "v1") {
    return unknownRoute(m, url.pathname);
  }

  switch (at(1)) {
    case "router":
      /* The Claude Code card of the setup wizard: the built-in account,
         signed in. */
      if (at(2) === "claude-agent") {
        return { status: 200, body: claudeAgentFixture };
      }
      break;
    case "workflows":
      return mockWorkflows(m, parts);
    case "calendar":
      return mockCalendar(m, parts, url, body);
    case "toolbox":
      return mockToolbox(m, parts);
    case "update":
      return mockUpdate(m, parts);
    case "auth": {
      if (at(2) === "state" && isGet) return { status: 200, body: authStateFixture };
      if (at(2) === "session" && isGet) {
        if (previewState === "login") {
          return { status: 401, body: { error: "unauthorized" } };
        }
        return { status: 200, body: authSessionFixture };
      }
      if (at(2) === "sessions" && isGet)
        return { status: 200, body: browserSessionsFixture };
      if (at(2) === "sessions" && at(3) === "revoke-others")
        return { status: 200, body: revokeBrowsersFixture };
      if (at(2) === "reset" && at(3) === "telegram")
        return { status: 200, body: resetTelegramFixture };
      if (isWrite) return { status: 200, body: csrfFixture };
      break;
    }
    case "daemon": {
      if (at(2) === "restart") {
        if (isGet) return { status: 200, body: daemonFixture };
        if (m === "DELETE") return { status: 200, body: cancelRestartFixture };
        return { status: 200, body: restartFixture };
      }
      return { status: 200, body: daemonFixture };
    }
    case "setup": {
      if (isGet)
        return {
          status: 200,
          body: previewState === "setup" ? setupNeededFixture : setupFixture,
        };
      return { status: 200, body: setupSaveFixture };
    }
    case "dashboard":
      return { status: 200, body: dashboardFixture };
    case "config": {
      if (isGet) return { status: 200, body: configFixture };
      return { status: 200, body: putConfigFixture };
    }
    case "web":
      return { status: 200, body: webSaveFixture };
    case "providers": {
      if (at(2) === "test") return { status: 200, body: setupTestFixture };
      if (isGet) return { status: 200, body: providersFixture };
      if (m === "DELETE") return { status: 200, body: providerSaveFixture };
      return { status: 200, body: providerSaveFixture };
    }
    case "secrets": {
      if (isGet) return { status: 200, body: secretsFixture };
      return { status: 200, body: secretWriteFixture };
    }
    case "audit":
      return { status: 200, body: auditFixture };
    case "models":
      return { status: 200, body: modelsFixture };
    case "tools":
      return { status: 200, body: toolsFixture };
    case "approvals":
      return { status: 200, body: approvalAnswerFixture };
    case "questions":
      return { status: 200, body: questionAnswerFixture };
    case "knowledge": {
      if (at(2) === "report") return { status: 200, body: knowledgeReportFixture };
      if (at(2) === "upload") return { status: 200, body: knowledgeUploadFixture };
      if (at(2) === "consolidate") return { status: 200, body: { started: true } };
      return { status: 200, body: knowledgeFixture };
    }
    case "mcp": {
      if (at(2) === "test") return { status: 200, body: mcpTestFixture };
      if (at(3) === "restart") return { status: 200, body: mcpRestartFixture };
      if (isGet) {
        /* ?empty=mcp shows the empty state. */
        const empty = new URLSearchParams(window.location.search).get("empty");
        return { status: 200, body: empty === "mcp" ? { ...mcpFixture, servers: [] } : mcpFixture };
      }
      return { status: 200, body: mcpSaveFixture };
    }
    case "skills": {
      if (isGet && at(2) && at(3)) return { status: 200, body: skillContentFixture };
      if (isGet) {
        /* ?empty=skills shows the empty state. */
        const empty = new URLSearchParams(window.location.search).get("empty");
        return {
          status: 200,
          body: empty === "skills" ? { skills: [], invalid: [] } : skillsFixture,
        };
      }
      if (m === "DELETE") return { status: 200, body: deleteSkillFixture };
      return { status: 200, body: putSkillFixture };
    }
    case "teams": {
      if (isGet) return { status: 200, body: teamFixture };
      if (m === "DELETE") return { status: 200, body: teamDeletedFixture };
      if (m === "POST") return { status: 200, body: teamCreatedFixture };
      return { status: 200, body: teamFixture };
    }
    case "channels": {
      if (at(2) !== "telegram") return { status: 200, body: channelsFixture };
      if (at(3) === "check") return { status: 200, body: telegramCheckFixture };
      if (at(3) === "waiting") return { status: 200, body: telegramWaitingFixture };
      if (at(3) === "routes") {
        if (m === "DELETE") return { status: 200, body: telegramRouteDeleteFixture };
        return { status: 200, body: telegramRouteFixture };
      }
      if (at(3) === "token") return { status: 200, body: telegramTokenFixture };
      if (at(3) === "discover") return { status: 200, body: telegramDiscoverFixture };
      if (at(3) === "connect") return { status: 200, body: telegramSaveFixture };
      return { status: 200, body: telegramSaveFixture };
    }
    case "agents": {
      const name = at(2);
      if (!name) {
        if (isGet) return { status: 200, body: agentsFixture };
        return { status: 200, body: agentCreatedFixture };
      }
      if (at(3) === "overview" && isGet)
        return { status: 200, body: agentOverviewFixture };
      if (at(3) === "avatar") return mockAvatar(m, name, at(4), body);
      if (at(3) === "files" && isGet)
        return { status: 200, body: agentFileFixture };
      if (at(3) === "files")
        return {
          status: 200,
          body: { version: "sha256:2b3c4d5e6f7a", characters: agentFileFixture.characters },
        };
      if (at(3) === "duplicate")
        return { status: 200, body: agentCreatedFixture };
      if (isGet) return { status: 200, body: agentsFixture };
      return { status: 200, body: putAgentFixture };
    }
    case "rooms": {
      const name = at(2);
      if (name === "stream") {
        return { status: 200, stream: ROOMS_STREAM_BODY };
      }
      if (at(3) === "segments") {
        const segments = segmentsByRoom[name] || [];
        const body: RoomSegmentsResponse = { segments, has_more: false };
        return { status: 200, body };
      }
      if (at(3) === "messages") return { status: 200, body: roomSendFixture };
      if (at(3) === "read") return { status: 200, body: roomReadFixture };
      if (isGet) return { status: 200, body: { rooms: roomsFixture } };
      break;
    }
    case "sessions": {
      const id = at(2);
      if (!id) {
        if (isGet) return { status: 200, body: sessionsListFixture };
        return { status: 200, body: detailsById[S_TOOLS] as SessionDetail };
      }
      if (at(3) === "stream") {
        return { status: 200, stream: SESSION_STREAM_BODY };
      }
      if (at(3) === "events") {
        return { status: 200, body: eventsById[id] || eventsResponse([]) };
      }
      if (at(3) === "jobs") {
        if (at(4)) return { status: 200, body: jobsFixture[0] as BackgroundJob };
        return { status: 200, body: jobsFixture };
      }
      if (at(3) === "attachments")
        return { status: 200, body: { attachments: uploadedFixture } };
      if (at(3) === "messages") return { status: 200, body: sendMessageFixture };
      if (at(3) === "cancel") return { status: 200, body: cancelTurnFixture };
      if (at(3) === "continue") return { status: 200, body: sendMessageFixture };
      if (at(3) === "compact") return { status: 200, body: compactFixture };
      if (at(3) === "fork")
        return { status: 200, body: detailsById[id] || (detailsById[S_TOOLS] as SessionDetail) };
      if (isGet) {
        const detail = detailsById[id];
        if (detail) return { status: 200, body: detail };
        /* Unknown session id: fall back to the first session so a hand-typed
           URL still renders instead of erroring. */
        return { status: 200, body: { ...(detailsById[S_TOOLS] as SessionDetail), id } };
      }
      return { status: 200, body: detailsById[S_TOOLS] as SessionDetail };
    }
    default:
      break;
  }

  return unknownRoute(m, url.pathname);
}

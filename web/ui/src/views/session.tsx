import { fileMeta } from "../lib/attachment-blocks";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  FormEvent,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
} from "react";
import { listAgents } from "../api/agents";
import type { Agent } from "../api/agents";
import { getModels } from "../api/models";
import { COMBOS_GROUP, getRouter } from "../api/router";
import { groupClaudeChoices, useClaudeAccounts } from "./router/claude-code";
import { CLAUDE_CODE_GROUP, isAntigravityGroup } from "./router/presets";
import { ProviderLogo } from "./router/provider-logo";
import type { RouterChoice } from "../api/router";
import type { CatalogEffort, CatalogModel } from "../api/models";
import { choiceLevels, cleanEfforts, effortName, keptEffort } from "./effort";
import type { EffortLevels } from "./effort";
import { NOT_OFFERED, defaultEffortLabel } from "./effort-control";
import { answerApproval } from "../api/approvals";
import { useSessionPresence } from "../lib/session-presence";
import type {
  ApprovalAnswerResult,
  ApprovalEvent,
  ApprovalProof,
} from "../api/approvals";
import {
  attachmentName,
  attachmentURL,
  uploadAttachments,
} from "../api/attachments";
import { authSession } from "../api/auth";
import {
  ApiError,
  DAEMON_DRAINING_TEXT,
  errorMessage,
  isDaemonDraining,
  openStream,
} from "../api/client";
import type { SSEStream, StreamFrame, StreamState } from "../api/client";
import { answerQuestion, skipAllQuestions } from "../api/questions";
import type { QuestionAnswerRequest, QuestionEvent } from "../api/questions";
import {
  getSession,
  listEvents,
  patchSession,
  renameSession,
} from "../api/sessions";
import type {
  PatchSessionRequest,
  SessionDetail,
  SessionEvent,
  Team,
} from "../api/sessions";
import {
  cancelTurn as cancelTurnRequest,
  compactSession as compactSessionRequest,
  continueSession as continueSessionRequest,
  forkSession as forkSessionRequest,
  sendMessage as sendMessageRequest,
} from "../api/turns";
import { holderText, jobNoticeOf, stopReasonWords } from "../lib/format";
import { ErrorNotice, Loading, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { pinToBottom, useStickToBottom } from "./scroll-pin";
import {
  Button,
  IconButton,
  Input,
  Menu,
  Modal,
} from "../ui/gr8r";
import { ApprovalCard, approvalResultWords } from "./approval";
import type { ApprovalItem } from "./approval";
import { isTeam, rosterNames } from "./agent-common";
import { displayLabel } from "./agent-label";
import { openQuestionFromEvents, QuestionCard } from "./question";
import type { OpenQuestion } from "./question";
import { SecretRequestCard, useSecretRequest } from "./secret-request";
import { buildTurns, ReplyChip, TurnBlock, UserBubble } from "./turn";
import type { ReplyTarget, UserAttachment, UserAttachmentRef } from "./turn";
import { JobNoticeLine } from "./job-notice";
import { ComposerTray } from "./composer-tray";
import { TrajectoryView } from "./trajectory";
import {
  NAMING_SESSION,
  TitleInput,
  UNTITLED_SESSION,
  publishSessionTitle,
  titleUpdateFrom,
  useSessionTitleUpdates,
} from "./session-title";
import type { SessionTitleUpdate } from "./session-title";
import {
  AttachmentChips,
  AttachmentDropOverlay,
  AttachButton,
  draftFileMeta,
  pasteImageFiles,
  revokeAttachmentURLs,
  useAttachmentDraft,
  useFileDrop,
} from "./attachments";
import type { DraftAttachment } from "./attachments";
import { AgentAvatar, AgentAvatarStack } from "./agent-avatar";

/* Chat view of the sessions screen (REQ-WEB-038, REQ-WEB-039, REQ-WEB-040).
   It replaces the rev 1 history list and keeps the turn block and the approval
   card in their own components for the team block and question card later. */

const POLICY_OPTIONS = [
  { value: "ask", label: "Ask", hint: "Ask before risky tools." },
  { value: "trust", label: "Trust", hint: "Run tools without asking." },
  { value: "never", label: "Never", hint: "Never ask; block unapproved tools." },
];

const COMPOSER_LINE_HEIGHT = 20;
const COMPOSER_MIN_ROWS = 2;
const COMPOSER_MAX_ROWS = 8;

type SessionTab = "chat" | "trajectory";

/* REQ-TRJ-001: the active tab lives in the URL (?view=trajectory). App parses
   the hash query into route params, but the workspace renders SessionPage
   directly, so SessionPage reads the same hash and follows hashchange. */
function tabFromValue(value: string | undefined): SessionTab {
  return value === "trajectory" ? "trajectory" : "chat";
}

function viewFromHash(): string {
  if (typeof window === "undefined") {
    return "";
  }
  const hash = window.location.hash || "";
  const queryIndex = hash.indexOf("?");
  if (queryIndex < 0) {
    return "";
  }
  try {
    return new URLSearchParams(hash.slice(queryIndex + 1)).get("view") || "";
  } catch {
    return "";
  }
}

function sessionHash(id: string, tab: SessionTab): string {
  const base = "#/sessions/" + encodeURIComponent(id);
  return tab === "trajectory" ? base + "?view=trajectory" : base;
}

interface ChildLogRequest {
  id: string;
  agent: string;
}

interface LiveTool {
  callId: string;
  name: string;
  input: unknown;
  status: string;
  output: unknown;
  at: number;
}

interface ChildLiveTurn {
  [key: string]: unknown;
  agent: string;
  turnId: string;
  text: string;
  textAt: number;
  tools: Record<string, LiveTool>;
}

interface SubmitResult {
  ok: boolean;
  message?: string;
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/* storedAttachments turns an owner message's image blocks into served URLs
   (API-ATT-003, API-ATT-004). It is exported so the room timeline renders the
   same owner bubbles as the session view (REQ-ROOM-002). */
export function storedAttachments(
  id: string,
  refs: UserAttachmentRef[] | undefined
): UserAttachment[] {
  return (refs || []).map((ref, index) => {
    const image = !ref.file && ref.mediaType.startsWith("image/");
    const meta = ref.file ? fileMeta(ref.file) : null;
    return {
      key: ref.path + "#" + index,
      name: attachmentName(ref.path),
      src: attachmentURL(id, ref.path),
      alt: ref.path,
      isImage: image,
      path: ref.path,
      ...(ref.file ? { file: ref.file } : {}),
      ...(meta ? { meta: meta.text, warn: meta.warn } : {}),
    };
  });
}

/* pendingAttachments renders the chosen files of an optimistic bubble from
   their local object URLs. */
function pendingAttachments(files: DraftAttachment[]): UserAttachment[] {
  return files.map((file) => ({
    key: file.id,
    name: file.file.name,
    src: file.url,
    alt: file.file.name,
    isImage: file.isImage,
    meta: draftFileMeta(file.file),
  }));
}

/* Count the textarea's rendered lines and clamp the rows attribute to 2..8.
   The composer uses a 20px line height, so the content height gives the count. */
function composerRowCount(node: HTMLTextAreaElement): number {
  const lines = Math.round(node.scrollHeight / COMPOSER_LINE_HEIGHT);
  return Math.min(COMPOSER_MAX_ROWS, Math.max(COMPOSER_MIN_ROWS, lines));
}

function channelLabel(value: string): string {
  switch (value) {
    case "web":
      return "Web";
    case "cli":
      return "CLI";
    case "telegram":
      return "Telegram";
    case "library":
      return "Library";
    default:
      return value || "Unknown";
  }
}

function policyLabel(value: string): string {
  for (const option of POLICY_OPTIONS) {
    if (option.value === value) {
      return option.label;
    }
  }
  return value ? value : "Ask";
}

function shortModel(value: string): string {
  const text = String(value || "");
  const parts = text.split(":");
  return parts.length > 1 ? parts.slice(1).join(":") : text;
}


function turnCountText(count: unknown): string {
  const value = Number(count) || 0;
  return value + (value === 1 ? " turn" : " turns");
}

function turnEndAnnouncement(reason: string, error: unknown): string {
  if (reason === "cancelled") {
    return "Turn stopped: cancelled.";
  }
  if (reason === "error") {
    const record =
      error && typeof error === "object"
        ? (error as Record<string, unknown>)
        : null;
    const message =
      record && record.message ? " " + String(record.message) : "";
    return "Turn failed." + message;
  }
  if (reason === "max_steps") {
    return "Turn stopped: maximum steps reached.";
  }
  if (reason === "loop") {
    return "Turn stopped: repeated a step without progress.";
  }
  if (reason === "max_tokens") {
    return "Turn stopped: reply cut off by the model's output limit.";
  }
  return "Turn finished: " + stopReasonWords(reason) + ".";
}

/* API-TFB-003: the refusal text of a failed Continue. */
function continueErrorMessage(failure: unknown): string {
  if (failure instanceof ApiError) {
    if (failure.status === 409 && failure.code === "turn_running") {
      return "A turn is already running.";
    }
    if (failure.status === 409 && failure.code === "nothing_to_continue") {
      return "Nothing to continue. The last turn ended normally.";
    }
  }
  if (isDaemonDraining(failure)) {
    return DAEMON_DRAINING_TEXT;
  }
  return errorMessage(failure);
}

function approvalAnnouncement(outcome: string): string {
  if (outcome === "unavailable") {
    return "Approval expired. The tool will not run.";
  }
  if (outcome === "cancelled") {
    return "Approval cancelled.";
  }
  return "Approval decided: " + approvalResultWords(outcome) + ".";
}

function questionClosedWords(payload: Record<string, unknown>): string {
  if (payload && payload.outcome === "answered") {
    return "Already answered elsewhere";
  }
  if (payload && payload.reason === "timeout") {
    return "Question expired";
  }
  if (
    payload &&
    (payload.reason === "cancelled" || payload.reason === "interrupted")
  ) {
    return "Question cancelled";
  }
  return "Already answered elsewhere";
}

function AgentMenuContent({
  agents,
  current,
  disabled,
  onPick,
}: {
  agents: Agent[];
  current: string;
  disabled: boolean;
  onPick: (name: string) => void;
}) {
  return (
    <>
      <p className="mh">Switch agent or team</p>
      {agents.length === 0 ? (
        <p className="mi-empty">No agents configured.</p>
      ) : (
        agents.map((entry) => {
          const team = isTeam(entry);
          return (
            <button
              key={entry.name}
              type="button"
              role="menuitemradio"
              className="mi"
              aria-checked={entry.name === current}
              disabled={disabled}
              onClick={() => onPick(entry.name)}
            >
              {team ? (
                <AgentAvatarStack size="sm" max={2} items={rosterNames(entry).map((name) => ({ name }))} />
              ) : (
                <AgentAvatar name={entry.name} size="sm" aria-hidden="true" />
              )}
              <span className="mi-text">
                <span className="mi-name">{displayLabel(entry)}</span>
                <span className="mi-meta">
                  {team
                    ? "Team · " + rosterNames(entry).length + " agents"
                    : entry.model || "agent default"}
                </span>
              </span>
              {entry.name === current ? (
                <span className="ck">
                  <Ic name="check" size={14} />
                </span>
              ) : null}
            </button>
          );
        })
      )}
    </>
  );
}

function ModelMenuContent({
  current,
  agentModel,
  provider,
  models,
  choices,
  onPick,
}: {
  current: string;
  agentModel: string;
  provider: string;
  models: CatalogModel[];
  /* Spec 37 (REQ-RTR-018): the router choices, combos first. When they are
     known they replace the one-provider catalog list. */
  choices: RouterChoice[];
  onPick: (value: string) => void;
}) {
  const routed = choices.length > 0;
  const [custom, setCustom] = useState("");
  const options: { value: string; label: string; meta: string }[] = [];
  const currentId = shortModel(current);
  const currentInCatalog = routed
    ? choices.some((choice) => choice.value === current)
    : models.some((model) => model.id === currentId);
  if (current && !currentInCatalog) {
    options.push({
      value: current,
      label: shortModel(current),
      meta: "Current model",
    });
  }
  if (agentModel && agentModel !== current) {
    options.push({
      value: agentModel,
      label: shortModel(agentModel),
      meta: "Agent default",
    });
  }

  function submitCustom(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = custom.trim();
    if (value) {
      onPick(value);
    }
  }

  return (
    <>
      <p className="mh">Model</p>
      {options.length === 0 && models.length === 0 ? (
        <p className="mi-empty">No model is set yet.</p>
      ) : null}
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          role="menuitemradio"
          className="mi"
          aria-checked={option.value === current}
          onClick={() => onPick(option.value)}
        >
          <span className="mi-text">
            <span className="mi-name">{option.label}</span>
            <span className="mi-meta">
              {option.meta + " · " + option.value}
            </span>
          </span>
          {option.value === current ? (
            <span className="ck">
              <Ic name="check" size={14} />
            </span>
          ) : null}
        </button>
      ))}
      {routed ? <RouterChoiceItems choices={choices} current={current} onPick={onPick} /> : null}
      {(routed ? [] : models).map((model) => {
        const value = provider ? provider + ":" + model.id : model.id;
        return (
          <button
            key={"catalog-" + model.id}
            type="button"
            role="menuitemradio"
            className="mi"
            aria-checked={value === current}
            onClick={() => onPick(value)}
          >
            <span className="mi-text">
              <span className="mi-name">
                {model.name}
                {model.legacy ? <span className="tag">Legacy</span> : null}
              </span>
              <span className="mi-meta">{value}</span>
            </span>
            {value === current ? (
              <span className="ck">
                <Ic name="check" size={14} />
              </span>
            ) : null}
          </button>
        );
      })}
      <form className="mi-custom" onSubmit={submitCustom}>
        <Input
          type="text"
          className="mi-custom-input"
          aria-label={routed ? "Custom model: a combo, provider/model, or provider:model" : "Custom model as provider:model"}
          placeholder={routed ? "combo or provider/model" : "provider:model"}
          value={custom}
          onChange={(event) => setCustom(event.target.value)}
        />
        <Button type="submit" size="sm" disabled={!custom.trim()}>
          Set
        </Button>
      </form>
    </>
  );
}

/* The router choices grouped as the agent picker shows them: combos, then
   each provider's models under its name. */
function RouterChoiceItems({
  choices,
  current,
  onPick,
}: {
  choices: RouterChoice[];
  current: string;
  onPick: (value: string) => void;
}) {
  const groups: string[] = [];
  if (choices.some((choice) => choice.group === COMBOS_GROUP)) {
    groups.push(COMBOS_GROUP);
  }
  for (const choice of choices) {
    if (!groups.includes(choice.group)) {
      groups.push(choice.group);
    }
  }
  return (
    <>
      {groups.map((group) => (
        <div key={group} role="group" aria-label={group}>
          <p className="mh" aria-hidden="true">
            {group === CLAUDE_CODE_GROUP || group.startsWith(CLAUDE_CODE_GROUP + " · ") ? (
              <span className="mh-logo">
                <ProviderLogo logo="claude" name="Claude Code" size={14} />
                {group}
              </span>
            ) : isAntigravityGroup(group) ? (
              <span className="mh-logo">
                <ProviderLogo logo="antigravity" name="Antigravity" size={14} />
                {group}
              </span>
            ) : (
              group
            )}
          </p>
          {choices
            .filter((choice) => choice.group === group)
            .map((choice) => (
              <button
                key={choice.value}
                type="button"
                role="menuitemradio"
                className="mi"
                aria-checked={choice.value === current}
                onClick={() => onPick(choice.value)}
              >
                <span className="mi-text">
                  <span className="mi-name">{choice.label || choice.value}</span>
                  {choice.label && choice.label !== choice.value ? (
                    <span className="mi-meta">{choice.value}</span>
                  ) : null}
                </span>
                {choice.value === current ? (
                  <span className="ck">
                    <Ic name="check" size={14} />
                  </span>
                ) : null}
              </button>
            ))}
        </div>
      ))}
    </>
  );
}

/* The Thinking menu of REQ-MDL-001: "Model default" clears the session effort,
   each level the model supports (spec 76, REQ-MES-002) sets it, with its
   description from /v1/models when the catalog has one. A saved level the
   model does not offer stays checked with the level it uses (REQ-MES-003). */
function ThinkingMenuContent({
  current,
  levels,
  descriptions,
  onPick,
}: {
  current: string;
  levels: EffortLevels;
  descriptions: CatalogEffort[];
  onPick: (value: string) => void;
}) {
  const efforts = levels.efforts.map((value) => {
    const known = descriptions.find((effort) => effort.value === value);
    return { value, description: known ? known.description : "" };
  });
  const stale = current !== "" && !levels.efforts.includes(current);
  if (stale) {
    efforts.push({ value: current, description: "Not offered by this model." });
  }
  return (
    <>
      <p className="mh">Thinking</p>
      <button
        type="button"
        role="menuitemradio"
        className="mi"
        aria-checked={current === ""}
        onClick={() => onPick("")}
      >
        <span className="mi-text">
          <span className="mi-name">Model default</span>
          <span className="mi-meta">
            {levels.default_effort
              ? "Use the model's own effort: " + effortName(levels.default_effort) + "."
              : "Use the model's own effort."}
          </span>
        </span>
        {current === "" ? (
          <span className="ck">
            <Ic name="check" size={14} />
          </span>
        ) : null}
      </button>
      {efforts.map((effort) => (
        <button
          key={effort.value}
          type="button"
          role="menuitemradio"
          className="mi"
          aria-checked={effort.value === current}
          onClick={() => onPick(effort.value)}
        >
          <span className="mi-text">
            <span className="mi-name">{effortName(effort.value)}</span>
            <span className="mi-meta">{effort.description}</span>
          </span>
          {effort.value === current ? (
            <span className="ck">
              <Ic name="check" size={14} />
            </span>
          ) : null}
        </button>
      ))}
    </>
  );
}

function PolicyMenuContent({
  current,
  onPick,
}: {
  current: string;
  onPick: (value: string) => void;
}) {
  return (
    <>
      <p className="mh">Policy</p>
      {POLICY_OPTIONS.map((option) => (
        <button
          key={option.value}
          type="button"
          role="menuitemradio"
          className="mi"
          aria-checked={option.value === current}
          onClick={() => onPick(option.value)}
        >
          <span className="mi-text">
            <span className="mi-name">{option.label}</span>
            <span className="mi-meta">{option.hint}</span>
          </span>
          {option.value === current ? (
            <span className="ck">
              <Ic name="check" size={14} />
            </span>
          ) : null}
        </button>
      ))}
      <p className="mi-note">Choosing Trust asks for confirmation first.</p>
    </>
  );
}

function TrustDialog({
  busy,
  onCancel,
  onConfirm,
}: {
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal
      title="Set policy to trust?"
      onClose={onCancel}
      footer={
        <>
          <Button
            type="button"
            variant="secondary"
            data-autofocus
            disabled={busy}
            onClick={onCancel}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="danger"
            disabled={busy}
            onClick={onConfirm}
          >
            {busy ? "Saving…" : "Set policy to trust"}
          </Button>
        </>
      }
    >
      <p>
        Policy <strong>trust</strong> lets tools in this session run without
        asking for approval. Only choose it for an agent and session you trust.
      </p>
    </Modal>
  );
}

interface ComposerProps {
  agent: string;
  agentLabel: string;
  team: Team | null;
  teamLabel: string;
  model: string;
  policy: string;
  reasoningEffort: string;
  agents: Agent[];
  draft: string;
  attachments: DraftAttachment[];
  attachmentNotice: string;
  uploading: boolean;
  readOnly: boolean;
  questionOpen: boolean;
  turnRunning: boolean;
  cancelling: boolean;
  sending: boolean;
  /* API reply: the automation message the next send replies to, if any. */
  replyTarget: ReplyTarget | null;
  onDraft: (value: string) => void;
  onFiles: (files: File[]) => void;
  onRemoveAttachment: (id: string) => void;
  onSend: () => void;
  onStop: () => void;
  onCancelReply: () => void;
  onAgent: (name: string) => void;
  /* resetEffort: the new model does not list the session effort, so the
     model change clears it (spec 76, REQ-MES-006). */
  onModel: (value: string, resetEffort?: boolean) => void;
  onEffort: (value: string) => void;
  onPolicy: (value: string, confirmTrust: boolean) => Promise<boolean>;
  /* The Background tab docked on top of the composer shell. */
  tray?: ReactNode;
}

function Composer({
  agent,
  agentLabel,
  team,
  teamLabel,
  model,
  policy,
  reasoningEffort,
  agents,
  draft,
  attachments,
  attachmentNotice,
  uploading,
  readOnly,
  questionOpen,
  turnRunning,
  cancelling,
  sending,
  replyTarget,
  onDraft,
  onFiles,
  onRemoveAttachment,
  onSend,
  onStop,
  onCancelReply,
  onAgent,
  onModel,
  onEffort,
  onPolicy,
  tray,
}: ComposerProps) {
  const [menu, setMenu] = useState("");
  const [trustOpen, setTrustOpen] = useState(false);
  const [trustBusy, setTrustBusy] = useState(false);
  const [rows, setRows] = useState(COMPOSER_MIN_ROWS);
  const [catalogState, setCatalogState] = useState<{
    provider: string;
    models: CatalogModel[];
    efforts: CatalogEffort[];
  }>({ provider: "", models: [], efforts: [] });
  const [choices, setChoices] = useState<RouterChoice[]>([]);
  const plusRef = useRef<HTMLSpanElement>(null);
  const modelRef = useRef<HTMLSpanElement>(null);
  const thinkingRef = useRef<HTMLSpanElement>(null);
  const policyRef = useRef<HTMLSpanElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useLayoutEffect(() => {
    const node = inputRef.current;
    if (!node) {
      return;
    }
    /* Collapse to one row first so scrollHeight reports the content height even
       when the previous draft filled eight rows. */
    node.rows = 1;
    const next = composerRowCount(node);
    node.rows = next;
    setRows(next);
  }, [draft]);

  /* API reply: clicking Reply sets a target and focuses the composer input. */
  useEffect(() => {
    if (replyTarget) {
      inputRef.current?.focus();
    }
  }, [replyTarget]);

  const agentEntry = agents.find((entry) => entry.name === agent) || null;
  const agentModel = agentEntry && agentEntry.model ? agentEntry.model : "";
  const modelValue = model || agentModel;
  const teamSession = Boolean(team) || isTeam(agentEntry);
  const messageTarget =
    teamSession && team && team.name
      ? teamLabel || String(team.name)
      : agentLabel || agent || "agent";
  const busySession = readOnly || turnRunning;

  /* Spec 39 (REQ-CCT-009): Claude Code models sit in one group per account. */
  const { accounts: claudeAccounts } = useClaudeAccounts();
  const menuChoices = useMemo(
    () => groupClaudeChoices(choices, null, claudeAccounts),
    [choices, claudeAccounts]
  );

  /* Spec 37: the session model menu offers the router choices. */
  useEffect(() => {
    let cancelled = false;
    getRouter()
      .then((data) => {
        if (!cancelled) {
          setChoices(Array.isArray(data.choices) ? data.choices : []);
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  /* The provider of the current model, or the agent's provider when the
     session has none. It keys the catalog fetch of REQ-MDL-020. A router
     address "provider/model" names its provider before the slash; a combo
     names none. */
  const modelProvider = useMemo(() => {
    const text = modelValue ? String(modelValue) : "";
    const colon = text.indexOf(":");
    const slash = text.indexOf("/");
    const isCombo = choices.some(
      (choice) => choice.group === COMBOS_GROUP && choice.value === text
    );
    const fromModel =
      colon > 0
        ? text.slice(0, colon)
        : slash > 0 && !isCombo
          ? text.slice(0, slash)
          : isCombo
            ? ""
            : text;
    if (fromModel) {
      return fromModel;
    }
    return agentEntry && agentEntry.provider ? agentEntry.provider : "";
  }, [modelValue, agentEntry, choices]);

  useEffect(() => {
    if (!modelProvider) {
      return undefined;
    }
    let cancelled = false;
    getModels(modelProvider)
      .then((response) => {
        if (cancelled) {
          return;
        }
        setCatalogState({
          provider: modelProvider,
          models: Array.isArray(response.models) ? response.models : [],
          efforts: Array.isArray(response.efforts) ? response.efforts : [],
        });
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setCatalogState({ provider: modelProvider, models: [], efforts: [] });
      });
    return () => {
      cancelled = true;
    };
  }, [modelProvider]);

  /* The fetched catalog belongs to one provider; switching providers hides it
     until the new fetch lands. */
  const catalog =
    catalogState.provider === modelProvider ? catalogState.models : [];
  const efforts =
    catalogState.provider === modelProvider ? catalogState.efforts : [];

  const catalogId =
    modelProvider && String(modelValue || "").startsWith(modelProvider + "/")
      ? String(modelValue).slice(modelProvider.length + 1)
      : shortModel(modelValue);
  const currentEntry =
    catalog.find((entry) => entry.id === catalogId) || null;
  /* Spec 76 (REQ-MES-002): the levels come from the model's router choice; a
     model the router does not list falls back to the catalog's Thinking
     levels. */
  const levelChoice = menuChoices.find((choice) => choice.value === modelValue) || null;
  const claudeChoice = Boolean(
    levelChoice &&
      (levelChoice.group === CLAUDE_CODE_GROUP ||
        levelChoice.group.startsWith(CLAUDE_CODE_GROUP + " · "))
  );
  const effortLevels: EffortLevels | null =
    levelChoice && (Array.isArray(levelChoice.efforts) || claudeChoice)
      ? choiceLevels(levelChoice, claudeChoice)
      : currentEntry && currentEntry.thinking
        ? {
            efforts: cleanEfforts(efforts.map((effort) => effort.value)),
            default_effort: currentEntry.default_effort || "",
          }
        : null;
  const thinksModel = Boolean(
    !teamSession && effortLevels && effortLevels.efforts.length > 0
  );
  const effortLabel = reasoningEffort
    ? "Thinking: " +
      effortName(reasoningEffort) +
      (effortLevels && !effortLevels.efforts.includes(reasoningEffort)
        ? " " + NOT_OFFERED
        : "")
    : "Thinking: Model default";
  const effortTitle =
    !reasoningEffort && effortLevels
      ? "Thinking: " + defaultEffortLabel(effortLevels.default_effort)
      : effortLabel;

  /* The Gr8r buttons do not forward refs, so the trigger refs point at their
     wrapper spans; focus the button inside. */
  function focusIn(ref: typeof plusRef) {
    window.setTimeout(
      () => ref.current?.querySelector<HTMLButtonElement>("button")?.focus(),
      0
    );
  }

  function closeMenu(kind: "plus" | "model" | "thinking" | "policy") {
    setMenu("");
    const target =
      kind === "plus"
        ? plusRef
        : kind === "model"
          ? modelRef
          : kind === "thinking"
            ? thinkingRef
            : policyRef;
    focusIn(target);
  }

  function pickAgent(name: string) {
    setMenu("");
    focusIn(plusRef);
    onAgent(name);
  }

  function pickModel(value: string) {
    setMenu("");
    focusIn(modelRef);
    /* Spec 76 (REQ-MES-006): keep the session effort when the new model lists
       it, else go back to the model's default. A model whose levels are not
       known keeps it. */
    const next = menuChoices.find((choice) => choice.value === value) || null;
    const nextClaude = Boolean(
      next &&
        (next.group === CLAUDE_CODE_GROUP || next.group.startsWith(CLAUDE_CODE_GROUP + " · "))
    );
    const reset = Boolean(
      reasoningEffort &&
        next &&
        (Array.isArray(next.efforts) || nextClaude) &&
        keptEffort(reasoningEffort, choiceLevels(next, nextClaude)) === ""
    );
    onModel(value, reset);
  }

  function pickEffort(value: string) {
    setMenu("");
    focusIn(thinkingRef);
    onEffort(value);
  }

  function pickPolicy(value: string) {
    setMenu("");
    if (value === "trust") {
      setTrustOpen(true);
      return;
    }
    focusIn(policyRef);
    onPolicy(value, false);
  }

  function cancelTrust() {
    setTrustOpen(false);
    focusIn(policyRef);
  }

  async function confirmTrust() {
    setTrustBusy(true);
    await onPolicy("trust", true);
    setTrustBusy(false);
    setTrustOpen(false);
    focusIn(policyRef);
  }

  function composerKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Escape" && replyTarget) {
      event.preventDefault();
      onCancelReply();
      return;
    }
    if (readOnly || questionOpen || turnRunning) {
      return;
    }
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      onSend();
    }
  }

  return (
    <div className="composer">
      {questionOpen ? (
        <p className="sr" id="composer-question-note">
          Answer the question above
        </p>
      ) : null}
      {tray}
      <div className="cx-shell">
      <div className="cbox cx-field">
        {replyTarget ? (
          <ReplyChip target={replyTarget} onCancel={onCancelReply} />
        ) : null}
        <AttachmentChips items={attachments} onRemove={onRemoveAttachment} />
        {(attachmentNotice || uploading) ? (
          <p className="composer-status" role="status">
            {uploading ? "Uploading…" : attachmentNotice}
          </p>
        ) : null}
        <div className="cx-field-row">
        <textarea
          ref={inputRef}
          className="composer-input"
          rows={rows}
          aria-label={"Message " + messageTarget}
          aria-describedby={questionOpen ? "composer-question-note" : undefined}
          placeholder={
            questionOpen
              ? "Answer the question above"
              : "Message " + messageTarget + "…"
          }
          value={draft}
          disabled={readOnly || questionOpen}
          onChange={(event) => onDraft(event.target.value)}
          onKeyDown={composerKeyDown}
          onPaste={(event) => pasteImageFiles(event, onFiles)}
        ></textarea>
        <span className="cx-send">
          {turnRunning && !readOnly ? (
            <button
              type="button"
              className="cx-send-btn is-stop"
              aria-label="Stop the running turn"
              title="Stop the running turn"
              disabled={cancelling}
              onClick={onStop}
            >
              <i />
            </button>
          ) : (
            <button
              type="button"
              className="cx-send-btn"
              aria-label="Send message"
              title="Send message"
              disabled={
                readOnly ||
                questionOpen ||
                sending ||
                uploading ||
                (!draft.trim() && attachments.length === 0)
              }
              onClick={onSend}
            >
              <Ic name="arrow-up" size={16} />
            </button>
          )}
        </span>
        </div>
      </div>
        <div className="composer-bar cx-bar">
          <span className="composer-control" ref={plusRef}>
            <IconButton
              icon="plus"
              size="sm"
              label="Composer actions"
              tip="Composer actions"
              aria-haspopup="menu"
              aria-expanded={menu === "plus" || menu === "agent"}
              disabled={readOnly}
              onClick={() =>
                setMenu(menu === "plus" || menu === "agent" ? "" : "plus")
              }
            />
            <Menu
              anchorRef={plusRef}
              open={menu === "plus"}
              onClose={() => closeMenu("plus")}
            >
              <p className="mh">Composer</p>
              <button
                type="button"
                role="menuitem"
                className="mi"
                disabled={busySession}
                onClick={() => setMenu("agent")}
              >
                <span className="mi-text">
                  <span className="mi-name">Switch agent or team</span>
                  <span className="mi-meta">{agentLabel || agent || "no agent"}</span>
                </span>
              </button>
            </Menu>
            <Menu
              anchorRef={plusRef}
              open={menu === "agent"}
              onClose={() => closeMenu("plus")}
            >
              <AgentMenuContent
                agents={agents}
                current={agent}
                disabled={busySession}
                onPick={pickAgent}
              />
            </Menu>
          </span>

          <span className="composer-control">
            <AttachButton
              disabled={readOnly || questionOpen || uploading}
              onFiles={onFiles}
            />
          </span>

          <span className="composer-chips">
            <span className="composer-control" ref={modelRef}>
              {teamSession ? (
                <Button
                  size="sm"
                  variant="ghost"
                  iconRight="chevron-down"
                  aria-label="Each agent in the team uses its own model."
                  title="Each agent in the team uses its own model."
                  disabled
                >
                  per agent
                </Button>
              ) : (
                <>
                  <Button
                    size="sm"
                    variant="ghost"
                    iconRight="chevron-down"
                    aria-label={"Model: " + (modelValue || "not set")}
                    aria-haspopup="menu"
                    aria-expanded={menu === "model"}
                    title={modelValue || "No model set"}
                    disabled={busySession}
                    onClick={() => setMenu(menu === "model" ? "" : "model")}
                  >
                    {shortModel(modelValue) || "Model"}
                  </Button>
                  <Menu
                    anchorRef={modelRef}
                    open={menu === "model"}
                    onClose={() => closeMenu("model")}
                  >
                    <ModelMenuContent
                      current={modelValue}
                      agentModel={agentModel}
                      provider={modelProvider}
                      models={catalog}
                      choices={menuChoices}
                      onPick={pickModel}
                    />
                  </Menu>
                </>
              )}
            </span>

            {thinksModel ? (
              <span className="composer-control" ref={thinkingRef}>
                <Button
                  size="sm"
                  variant="ghost"
                  iconRight="chevron-down"
                  aria-label={effortLabel}
                  aria-haspopup="menu"
                  aria-expanded={menu === "thinking"}
                  title={effortTitle}
                  disabled={busySession}
                  onClick={() => setMenu(menu === "thinking" ? "" : "thinking")}
                >
                  {effortLabel}
                </Button>
                <Menu
                  anchorRef={thinkingRef}
                  open={menu === "thinking"}
                  onClose={() => closeMenu("thinking")}
                >
                  {effortLevels ? (
                    <ThinkingMenuContent
                      current={reasoningEffort}
                      levels={effortLevels}
                      descriptions={efforts}
                      onPick={pickEffort}
                    />
                  ) : null}
                </Menu>
              </span>
            ) : null}

            <span className="composer-control" ref={policyRef}>
              <Button
                size="sm"
                variant="ghost"
                icon="lock"
                iconRight="chevron-down"
                aria-label={"Policy: " + policyLabel(policy)}
                aria-haspopup="menu"
                aria-expanded={menu === "policy"}
                disabled={busySession}
                onClick={() => setMenu(menu === "policy" ? "" : "policy")}
              >
                {policyLabel(policy)}
              </Button>
              <Menu
                anchorRef={policyRef}
                open={menu === "policy"}
                onClose={() => closeMenu("policy")}
              >
                <PolicyMenuContent current={policy} onPick={pickPolicy} />
              </Menu>
            </span>
          </span>

          <span className="sp"></span>
        </div>
      </div>
      {!readOnly ? (
        <p className="cx-hint">
          {turnRunning
            ? "Stop ends the turn · Enter queues your message"
            : "Enter to send · Shift+Enter for a new line"}
        </p>
      ) : null}
      {trustOpen ? (
        <TrustDialog
          busy={trustBusy}
          onCancel={cancelTrust}
          onConfirm={confirmTrust}
        />
      ) : null}
    </div>
  );
}

export interface SessionPageProps {
  id: string;
  /* The route's ?view= value. SessionPage also reads the hash when the
     workspace does not pass it. */
  view?: string;
}

export function SessionPage({ id, view }: SessionPageProps) {
  const announce = useAnnounce();
  const [tab, setTab] = useState<SessionTab>(() =>
    tabFromValue(view !== undefined ? view : viewFromHash())
  );
  const [detail, setDetail] = useState<SessionDetail | null>(null);
  /* Spec 40: the header title is being edited. */
  const [editingTitle, setEditingTitle] = useState(false);
  const titleButtonRef = useRef<HTMLButtonElement>(null);
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [loadDetails, setLoadDetails] = useState<unknown[]>([]);
  const [streamState, setStreamState] = useState<
    StreamState | "connecting" | "closed"
  >("connecting");
  const [streamReady, setStreamReady] = useState(false);
  const [turnRunning, setTurnRunning] = useState(false);
  const [liveTurnId, setLiveTurnId] = useState("");
  const [liveStartedAt, setLiveStartedAt] = useState(0);
  const [streamText, setStreamText] = useState("");
  const [liveThinking, setLiveThinking] = useState(false);
  /* The streamed text of the thought in progress: the process line shows its
     end while the model thinks (owner request 2026-11-24). */
  const [liveThought, setLiveThought] = useState("");
  const [holder, setHolder] = useState("none");
  const [holderPid, setHolderPid] = useState<number | null>(null);
  const [draft, setDraft] = useState("");
  /* API reply: the automation message the next send replies to. */
  const [replyTarget, setReplyTarget] = useState<ReplyTarget | null>(null);
  const [sending, setSending] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [continueBusy, setContinueBusy] = useState(false);
  const [continueError, setContinueError] = useState("");
  const [pendingText, setPendingText] = useState("");
  const [pendingFiles, setPendingFiles] = useState<DraftAttachment[]>([]);
  const [uploading, setUploading] = useState(false);
  const [queued, setQueued] = useState(false);
  /* REQ-JBN-022: bumped on a tool/result or job notice so JobsPill re-reads. */
  const [jobRefresh, setJobRefresh] = useState(0);
  const [notice, setNotice] = useState<{ text: string; tone: string } | null>(
    null
  );
  const [actionError, setActionError] = useState("");
  const [actionDetails, setActionDetails] = useState<unknown[]>([]);
  const [approvals, setApprovals] = useState<ApprovalItem[]>([]);
  const [questionBusy, setQuestionBusy] = useState(false);
  const [closedQuestionIds, setClosedQuestionIds] = useState<string[]>([]);
  const [liveQuestion, setLiveQuestion] = useState<OpenQuestion | null>(null);
  const [childEvents, setChildEvents] = useState<
    Record<string, SessionEvent[]>
  >({});
  const [childLive, setChildLive] = useState<Record<string, ChildLiveTurn>>({});
  /* Spec 53: the secure secret input card. */
  const secrets = useSecretRequest(events, childEvents);
  const receiveSecret = secrets.receive;

  const lastSeqRef = useRef(0);
  const childSeenRef = useRef(new Set<string>());
  const sessionIdRef = useRef(id);
  sessionIdRef.current = id;
  const pendingRef = useRef("");
  const decidedRef = useRef(new Set<string>());
  const openApprovalIdsRef = useRef(new Set<string>());
  const questionNotifiedRef = useRef(new Set<string>());
  const closedQuestionIdsRef = useRef(new Set<string>());
  const questionSubmittingRef = useRef("");
  const openQuestionIdRef = useRef("");
  const endedRef = useRef(new Set<string>());
  const refreshRef = useRef<(() => Promise<SessionDetail | null>) | null>(null);
  const streamRef = useRef<SSEStream | null>(null);
  const lastHolderRef = useRef<string | null>(null);
  const lastStreamStateRef = useRef("connecting");
  const lastRunningRef = useRef(false);
  const messagesRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const attachmentDraft = useAttachmentDraft();
  const pendingFilesRef = useRef<DraftAttachment[]>([]);
  const fileDrop = useFileDrop(attachmentDraft.addFiles);
  /* Spec #42: no macOS reply notification while this page is in front. */
  useSessionPresence(id);

  const readOnly = holder === "other";
  const busySession = readOnly || turnRunning;

  /* Keep the switch in sync with the URL. A caller that passes `view` owns the
     value; otherwise the hash is the source of truth. */
  useEffect(() => {
    if (view !== undefined) {
      setTab(tabFromValue(view));
      return undefined;
    }
    const sync = () => setTab(tabFromValue(viewFromHash()));
    sync();
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, [id, view]);

  const switchTab = useCallback(
    (next: SessionTab) => {
      setTab(next);
      const nextHash = sessionHash(id, next);
      if (window.location.hash !== nextHash) {
        window.location.hash = nextHash;
      }
    },
    [id]
  );

  /* The open request comes from the session log: question/asked without a
     question/answered. It follows the same replay path as the rest of the
     thread, so a page load or reconnect shows the card without a second
     question-only stream. */
  const openQuestion = useMemo(() => {
    const found = openQuestionFromEvents(events, childEvents);
    if (!found) {
      return null;
    }
    if (closedQuestionIds.includes(found.request_id)) {
      return null;
    }
    return found;
  }, [events, childEvents, closedQuestionIds]);
  const activeQuestion: QuestionEvent | null =
    openQuestion ||
    (liveQuestion && !closedQuestionIds.includes(liveQuestion.request_id)
      ? liveQuestion
      : null);
  openQuestionIdRef.current = activeQuestion ? activeQuestion.request_id : "";

  const clearError = useCallback(() => {
    setActionError("");
    setActionDetails([]);
  }, []);

  const showNotice = useCallback((text: string, tone = "info") => {
    setNotice({ text, tone });
  }, []);

  const reportError = useCallback(
    (failure: unknown, prefix: string) => {
      let message = errorMessage(failure);
      if (failure instanceof ApiError) {
        if (failure.code === "session_in_use") {
          const pid = failure.pid;
          message = pid
            ? "Session is in use by another process (PID " + pid + ")."
            : "Session is in use by another process.";
          setHolder("other");
          if (pid) {
            setHolderPid(pid);
          }
        }
        if (failure.code === "invalid_state") {
          message = "The operation is not allowed while a turn is running.";
        }
        setActionDetails(failure.details || []);
      } else {
        setActionDetails([]);
      }
      setActionError(message);
      announce(prefix + " " + message);
    },
    [announce]
  );

  /* Spec 40: a title change from the stream, a rename here, or one made in
     the History list. */
  const applyTitle = useCallback(
    (update: SessionTitleUpdate) => {
      if (update.id !== id) {
        return;
      }
      setDetail((current) =>
        current
          ? {
              ...current,
              title: update.title,
              title_source: update.title_source,
              title_pending: update.title_pending,
            }
          : current
      );
    },
    [id]
  );
  const applyTitleRef = useRef(applyTitle);
  useEffect(() => {
    applyTitleRef.current = applyTitle;
  }, [applyTitle]);
  useSessionTitleUpdates(applyTitle);

  const endTitleEdit = useCallback(() => {
    setEditingTitle(false);
    window.setTimeout(() => titleButtonRef.current?.focus(), 0);
  }, []);

  /* REQ-TTL-008, REQ-TTL-009: Enter saves; an empty title goes back to an
     automatic one. */
  const saveTitle = useCallback(
    async (value: string) => {
      try {
        const updated = await renameSession(id, value);
        const update = titleUpdateFrom(id, updated as unknown as Record<string, unknown>);
        applyTitle(update);
        publishSessionTitle(update);
        endTitleEdit();
        announce(value ? "Session renamed." : "The session will get an automatic title.");
      } catch (failure) {
        const message = errorMessage(failure);
        setActionError("Could not rename the session. " + message);
        setActionDetails([]);
        announce("Could not rename the session. " + message);
        throw failure;
      }
    },
    [id, applyTitle, endTitleEdit, announce]
  );

  const refreshDetail = useCallback(async (): Promise<SessionDetail | null> => {
    try {
      const next = await getSession(id);
      setDetail(next);
      setHolder(next.in_use || "none");
      setHolderPid(next.holder_pid || null);
      setTurnRunning(Boolean(next.turn_running));
      lastRunningRef.current = Boolean(next.turn_running);
      return next;
    } catch {
      return null;
    }
  }, [id]);

  refreshRef.current = refreshDetail;

  const markApprovalDecided = useCallback(
    (requestId: string, outcome: string, announcement?: string) => {
      if (!requestId) {
        return;
      }
      const first = !decidedRef.current.has(requestId);
      decidedRef.current.add(requestId);
      openApprovalIdsRef.current.delete(requestId);
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === requestId
            ? { ...item, decided: outcome, deciding: false, error: "" }
            : item
        )
      );
      if (first && announcement) {
        announce(announcement);
      }
    },
    [announce]
  );

  const closeQuestion = useCallback((requestId: string) => {
    if (!requestId) {
      return;
    }
    openQuestionIdRef.current = "";
    closedQuestionIdsRef.current.add(requestId);
    setLiveQuestion((current) =>
      current && current.request_id === requestId ? null : current
    );
    setClosedQuestionIds((current) =>
      current.includes(requestId) ? current : current.concat([requestId])
    );
    /* The status projection moves from waiting_input back to running (or
       idle) once question/answered is written. Refresh it for the header. */
    if (refreshRef.current) {
      refreshRef.current();
    }
  }, []);

  const announceQuestionResult = useCallback(
    (requestId: string, words: string) => {
      if (!requestId || questionNotifiedRef.current.has(requestId)) {
        return;
      }
      questionNotifiedRef.current.add(requestId);
      announce(words);
    },
    [announce]
  );

  const submitQuestion = useCallback(
    async (
      request: QuestionEvent,
      send: (requestId: string) => Promise<unknown>
    ): Promise<SubmitResult> => {
      const requestId = request && request.request_id;
      if (!requestId) {
        return { ok: false, message: "Could not send. Try again." };
      }
      setQuestionBusy(true);
      questionSubmittingRef.current = requestId;
      try {
        await send(requestId);
        closeQuestion(requestId);
        announceQuestionResult(requestId, "Answer sent");
        return { ok: true };
      } catch (failure) {
        if (failure instanceof ApiError) {
          if (failure.status === 422) {
            return { ok: false, message: "Check your answers" };
          }
          if (failure.status === 409) {
            closeQuestion(requestId);
            announceQuestionResult(requestId, "Already answered elsewhere");
            return { ok: true };
          }
          if (failure.status === 404) {
            closeQuestion(requestId);
            announceQuestionResult(requestId, "This question is no longer open");
            return { ok: true };
          }
        }
        return { ok: false, message: "Could not send. Try again." };
      } finally {
        if (questionSubmittingRef.current === requestId) {
          questionSubmittingRef.current = "";
        }
        setQuestionBusy(false);
      }
    },
    [announceQuestionResult, closeQuestion]
  );

  const handleQuestionSubmit = useCallback(
    (request: QuestionEvent, body: QuestionAnswerRequest) =>
      submitQuestion(request, (requestId) => answerQuestion(requestId, body)),
    [submitQuestion]
  );

  const handleQuestionSkipAll = useCallback(
    (request: QuestionEvent) =>
      submitQuestion(request, (requestId) => skipAllQuestions(requestId)),
    [submitQuestion]
  );

  const handleQuestionExpired = useCallback(
    (request: QuestionEvent) => {
      const requestId = request && request.request_id;
      closeQuestion(requestId);
      announceQuestionResult(requestId, "Question expired");
    },
    [announceQuestionResult, closeQuestion]
  );

  const finishTurn = useCallback(
    (turnId: string, reason: string, error: unknown) => {
      if (turnId && endedRef.current.has(turnId)) {
        return;
      }
      if (turnId) {
        endedRef.current.add(turnId);
      }
      lastRunningRef.current = false;
      setTurnRunning(false);
      setCancelling(false);
      setStreamText("");
      setLiveThinking(false);
      setLiveThought("");
      setQueued(false);
      setLiveTurnId("");
      setLiveStartedAt(0);
      openApprovalIdsRef.current.clear();
      setApprovals((current) =>
        current.map((item) =>
          item.decided
            ? item
            : {
                ...item,
                decided:
                  reason === "cancelled" ? "cancelled" : "unavailable",
                deciding: false,
              }
        )
      );
      pendingRef.current = "";
      setPendingText("");
      const text = turnEndAnnouncement(reason, error);
      if (reason !== "end_turn") {
        showNotice(text, reason === "error" ? "warn" : "info");
      }
      announce(text);
      if (refreshRef.current) {
        refreshRef.current();
      }
    },
    [announce, showNotice]
  );

  /* Older turns load their child logs only when the owner expands the work
     panel (Q-011). The fetched events are tagged with the child agent so the
     panel can group its tool rows. */
  const loadChildEvents = useCallback(
    async (requests: ChildLogRequest[]) => {
      if (!Array.isArray(requests) || requests.length === 0) {
        return;
      }
      const list = requests;
      const loadId = sessionIdRef.current;
      const loaded: Record<string, SessionEvent[]> = {};
      await Promise.all(
        list.map(async (request) => {
          const childId = request && request.id ? String(request.id) : "";
          if (!childId) {
            return;
          }
          try {
            const page = await listEvents(childId, 0);
            loaded[childId] = (page.events || []).map((event) => ({
              ...event,
              child_session_id: childId,
              agent: (request && request.agent) || "",
            }));
          } catch {
            loaded[childId] = [];
          }
        })
      );
      if (sessionIdRef.current !== loadId) {
        return;
      }
      setChildEvents((current) => {
        let changed = false;
        const next = { ...current };
        for (const childId of Object.keys(loaded)) {
          const incoming = loaded[childId];
          if (!incoming || incoming.length === 0) {
            continue;
          }
          const bySeq = new Map<string, SessionEvent>();
          const existing = Array.isArray(next[childId]) ? next[childId] : [];
          for (const event of existing) {
            if (event && event.seq !== undefined) {
              bySeq.set(String(event.seq), event);
            }
          }
          for (const event of incoming) {
            if (event && event.seq !== undefined) {
              bySeq.set(String(event.seq), event);
            }
          }
          next[childId] = Array.from(bySeq.values()).sort(
            (left, right) => (Number(left.seq) || 0) - (Number(right.seq) || 0)
          );
          changed = true;
        }
        return changed ? next : current;
      });
    },
    []
  );

  const loadTrajectoryChild = useCallback(
    (childId: string) => loadChildEvents([{ id: childId, agent: "" }]),
    [loadChildEvents]
  );

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setLoadError("");
    setLoadDetails([]);
    setStreamReady(false);
    setApprovals([]);
    setStreamText("");
    setLiveThinking(false);
    setLiveThought("");
    setQueued(false);
    setJobRefresh(0);
    setPendingText("");
    setNotice(null);
    setActionError("");
    setActionDetails([]);
    setCancelling(false);
    setContinueBusy(false);
    setContinueError("");
    setChildEvents({});
    setChildLive({});
    setQuestionBusy(false);
    setClosedQuestionIds([]);
    setLiveQuestion(null);
    pendingRef.current = "";
    closedQuestionIdsRef.current.clear();
    decidedRef.current.clear();
    openApprovalIdsRef.current.clear();
    questionNotifiedRef.current.clear();
    questionSubmittingRef.current = "";
    openQuestionIdRef.current = "";
    childSeenRef.current.clear();
    endedRef.current.clear();
    lastRunningRef.current = false;
    lastHolderRef.current = null;
    stickRef.current = true;
    setReplyTarget(null);

    Promise.all([getSession(id), listEvents(id, 0)])
      .then(([session, page]) => {
        if (!alive) {
          return;
        }
        setDetail(session);
        setEvents(page.events || []);
        lastSeqRef.current = page.last_seq || 0;
        setHolder(session.in_use || "none");
        setHolderPid(session.holder_pid || null);
        setTurnRunning(Boolean(session.turn_running));
        lastRunningRef.current = Boolean(session.turn_running);
        setDraft("");
        setLoading(false);
        setStreamReady(true);
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        setLoadError(errorMessage(failure));
        setLoadDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not load the session. " + errorMessage(failure));
        setLoading(false);
      });

    listAgents()
      .then((page) => {
        if (alive) {
          setAgents(page.agents || []);
        }
      })
      .catch(() => {
        if (alive) {
          setAgents([]);
        }
      });

    return () => {
      alive = false;
    };
  }, [id, announce]);

  useEffect(() => {
    if (!streamReady) {
      return undefined;
    }
    setStreamState("connecting");
    const stream = openStream(id, {
      fromSeq: lastSeqRef.current,
      onState: (value) => {
        const previous = lastStreamStateRef.current;
        lastStreamStateRef.current = value;
        setStreamState(value);
        if (value === "reconnecting" && previous !== "reconnecting") {
          announce("The live stream is reconnecting.");
        } else if (value === "connected" && previous === "reconnecting") {
          announce("The live stream is connected.");
        }
      },
      onEvent: (event) => {
        const childId =
          typeof event.child_session_id === "string"
            ? event.child_session_id
            : "";
        if (childId) {
          /* Child items never touch the root history or the root turn state.
             A reconnect replays them, so drop a seen (child, seq) pair. */
          const seenKey = childId + "|" + String(event.seq);
          if (childSeenRef.current.has(seenKey)) {
            return;
          }
          childSeenRef.current.add(seenKey);
          setChildEvents((current) => {
            const list = Array.isArray(current[childId])
              ? current[childId]
              : [];
            if (list.some((entry) => entry.seq === event.seq)) {
              return current;
            }
            return {
              ...current,
              [childId]: list
                .concat([event])
                .sort((left, right) => left.seq - right.seq),
            };
          });
          return;
        }
        setEvents((current) => {
          if (current.some((entry) => entry.seq === event.seq)) {
            return current;
          }
          return current
            .concat([event])
            .sort((left, right) => left.seq - right.seq);
        });
        lastSeqRef.current = Math.max(lastSeqRef.current, event.seq);
        const payload =
          event.payload && typeof event.payload === "object"
            ? event.payload
            : {};

        if (event.kind === "user/message" && pendingRef.current) {
          pendingRef.current = "";
          setPendingText("");
          /* The stored message now shows its own attachments, so the pending
             object URLs are freed. */
          revokeAttachmentURLs(pendingFilesRef.current);
          pendingFilesRef.current = [];
          setPendingFiles([]);
          setQueued(false);
        }
        /* REQ-JBN-022: a tool result or a job notice means the background pill
           should re-read the job list, so a job started mid-visit appears. */
        if (
          event.kind === "tool/result" ||
          (event.kind === "user/message" && jobNoticeOf(payload) !== null)
        ) {
          setJobRefresh((value) => value + 1);
        }
        if (event.kind === "assistant/message" || event.kind === "tool/call") {
          setStreamText("");
          setLiveThinking(false);
          setLiveThought("");
        }
        if (event.kind === "approval/decided") {
          markApprovalDecided(
            stringOf(payload.request_id),
            stringOf(payload.outcome) || "unavailable",
            approvalAnnouncement(stringOf(payload.outcome) || "unavailable")
          );
        }
        if (event.kind === "question/answered") {
          const requestId = stringOf(payload.request_id);
          if (requestId) {
            closedQuestionIdsRef.current.add(requestId);
            setLiveQuestion((current) =>
              current && current.request_id === requestId ? null : current
            );
          }
          if (requestId && openQuestionIdRef.current === requestId) {
            /* A POST that is still in flight announces its own result. The
               stream event closes the card either way. */
            const inFlight = questionSubmittingRef.current === requestId;
            closeQuestion(requestId);
            if (!inFlight) {
              announceQuestionResult(requestId, questionClosedWords(payload));
            }
          }
        }
        if (event.kind === "turn/end") {
          finishTurn(
            stringOf(payload.turn_id),
            stringOf(payload.reason),
            payload.error
          );
        }
      },
      onFrame: (frame: StreamFrame) => {
        const childId = stringOf(frame.child_session_id);
        if (childId) {
          /* A child frame updates only that child's live thread. The root's
             turn id and streaming text stay untouched (API-TEAM-011). */
          setChildLive((current) => {
            const previous =
              current[childId] || {
                agent: stringOf(frame.agent),
                turnId: "",
                text: "",
                textAt: 0,
                tools: {},
              };
            const turnId = stringOf(frame.turn_id) || previous.turnId || "";
            const fresh = Boolean(
              turnId && previous.turnId && turnId !== previous.turnId
            );
            const entry: ChildLiveTurn = {
              agent: stringOf(frame.agent) || previous.agent || "",
              turnId,
              text: fresh ? "" : previous.text || "",
              textAt: fresh ? 0 : previous.textAt || 0,
              tools: fresh ? {} : previous.tools ? { ...previous.tools } : {},
            };
            if (frame.kind === "text_delta" && frame.text) {
              entry.text += String(frame.text);
              entry.textAt = Date.now();
            } else if (
              frame.kind === "tool_call" ||
              frame.kind === "tool_result"
            ) {
              const callId = stringOf(frame.call_id);
              if (!callId) {
                return current;
              }
              const tool = entry.tools[callId] || {
                callId,
                name: "",
                input: null,
                status: "running",
                output: "",
                at: 0,
              };
              if (frame.kind === "tool_call") {
                entry.tools[callId] = {
                  ...tool,
                  name: stringOf(frame.name) || tool.name,
                  input: frame.input === undefined ? tool.input : frame.input,
                  status: tool.status || "running",
                  at: tool.at || Date.now(),
                };
              } else {
                entry.tools[callId] = {
                  ...tool,
                  status: stringOf(frame.status) || "success",
                  output:
                    frame.output === undefined ? tool.output : frame.output,
                  at: tool.at || Date.now(),
                };
              }
            }
            return { ...current, [childId]: entry };
          });
          return;
        }
        const turnId = stringOf(frame.turn_id);
        if (turnId) {
          setLiveTurnId(turnId);
          if (!lastRunningRef.current) {
            setLiveStartedAt(Date.now());
          }
        }
        if (frame.kind === "text_delta" && frame.text) {
          setLiveThinking(false);
          setLiveThought("");
          setStreamText((current) => current + String(frame.text));
        }
        /* REQ-MDL-006: a thinking delta shows the live process line, with the
           end of the streamed thought under it; the answer text or the next
           step clears it. */
        if (frame.kind === "thinking_delta") {
          setLiveThinking(true);
          if (frame.text) {
            setLiveThought((current) => current + String(frame.text));
          }
        }
        if (frame.kind === "tool_call" || frame.kind === "tool_result") {
          setLiveThinking(false);
          setLiveThought("");
          setStreamText("");
        }
        /* REQ-LTC-032: a step_retry drops the partial text of the failed step;
           streamText only ever holds the current step's streamed answer. */
        if (frame.kind === "step_retry") {
          setLiveThinking(false);
          setLiveThought("");
          setStreamText("");
        }
      },
      onStatus: (status: StreamFrame) => {
        const next = stringOf(status.holder) || "none";
        const previous = lastHolderRef.current;
        lastHolderRef.current = next;
        setHolder(next);
        setHolderPid(typeof status.pid === "number" ? status.pid : null);
        const running =
          Boolean(status.turn_running) || openApprovalIdsRef.current.size > 0;
        if (running && !lastRunningRef.current) {
          announce("Turn running.");
        }
        lastRunningRef.current = running;
        setTurnRunning(running);
        if (next === "other" && previous !== "other") {
          announce(
            typeof status.pid === "number"
              ? "This session is now read-only: another process holds it (PID " +
                  status.pid +
                  ")."
              : "This session is now read-only: another process holds it."
          );
        } else if (previous === "other" && next !== "other") {
          announce("The session is no longer held by another process.");
        }
      },
      onTurn: (turn: StreamFrame) => {
        finishTurn(
          stringOf(turn.turn_id),
          stringOf(turn.stop_reason),
          null
        );
      },
      onApproval: (approval) => {
        if (approval.request_id) {
          openApprovalIdsRef.current.add(approval.request_id);
        }
        setApprovals((current) => {
          if (current.some((item) => item.request_id === approval.request_id)) {
            return current;
          }
          return current.concat([
            {
              ...approval,
              decided: "",
              deciding: false,
              error: "",
            },
          ]);
        });
        lastRunningRef.current = true;
        setTurnRunning(true);
        announce("Approval requested for " + (approval.tool || "a tool") + ".");
      },
      onQuestion: (question) => {
        const requestId = question && question.request_id;
        if (!requestId) {
          return;
        }
        if (
          openQuestionIdRef.current === requestId ||
          closedQuestionIdsRef.current.has(requestId)
        ) {
          return;
        }
        setLiveQuestion((current) => {
          if (current && current.request_id === requestId) {
            return current;
          }
          return {
            request_id: requestId,
            call_id: stringOf(question.call_id),
            expires_at: Number(question.expires_at) || 0,
            turn_id: stringOf(question.turn_id),
            questions: Array.isArray(question.questions) ? question.questions : [],
          };
        });
      },
      onApprovalClosed: (payload: StreamFrame) => {
        const requestId = stringOf(payload.request_id);
        if (requestId) {
          markApprovalDecided(
            requestId,
            "approval_closed",
            "Approval closed. Another tab answered first or the approval expired."
          );
        }
      },
      onSecretRequest: (incoming) => {
        receiveSecret(incoming);
      },
      onTitle: (frame: StreamFrame) => {
        /* REQ-TTL-011: the automatic title arrived or a rename elsewhere. */
        const update = titleUpdateFrom(id, frame);
        applyTitleRef.current(update);
        publishSessionTitle(update);
      },
      onShutdown: () => {
        setStreamState("closed");
        announce("The daemon is shutting down.");
        if (streamRef.current) {
          streamRef.current.close();
        }
      },
      onError: () => {
        setStreamState("reconnecting");
      },
      onUnauthorized: () => {
        authSession().catch(() => undefined);
      },
    });
    streamRef.current = stream;
    return () => {
      streamRef.current = null;
      stream.close();
    };
  }, [
    id,
    streamReady,
    announce,
    finishTurn,
    markApprovalDecided,
    closeQuestion,
    announceQuestionResult,
    receiveSecret,
  ]);

  const thread = useMemo(
    () =>
      buildTurns(events, {
        running: turnRunning,
        turnId: liveTurnId,
        startedAt: liveStartedAt,
      }),
    [events, turnRunning, liveTurnId, liveStartedAt]
  );

  /* Root events grouped by turn id so the team block can build its steps
   from the turn's own log. */
  const turnEvents = useMemo(() => {
    const map = new Map<string, SessionEvent[]>();
    for (const event of events) {
      const turnId =
        event.turn_id ||
        (event.payload && stringOf(event.payload.turn_id)) ||
        "";
      if (!turnId) {
        continue;
      }
      const list = map.get(turnId) || [];
      list.push(event);
      map.set(turnId, list);
    }
    return map;
  }, [events]);

  useEffect(() => {
    const node = messagesRef.current;
    if (node && stickRef.current) {
      node.scrollTop = node.scrollHeight;
    }
  }, [thread, pendingText, pendingFiles, streamText]);

  useStickToBottom(messagesRef, stickRef);

  function handleScroll() {
    const node = messagesRef.current;
    if (!node) {
      return;
    }
    stickRef.current = node.scrollHeight - node.scrollTop - node.clientHeight < 96;
  }

  async function sendMessage() {
    const text = draft.trim();
    const files = attachmentDraft.items.slice();
    if ((!text && files.length === 0) || readOnly || sending || turnRunning) {
      return;
    }
    setSending(true);
    clearError();
    /* Show the bubble before the request so a fast turn that logs the user
       message before the response cannot leave a duplicate pending bubble. */
    setDraft("");
    pinToBottom(messagesRef, stickRef);
    pendingRef.current = text || "(attachments)";
    setPendingText(text);
    pendingFilesRef.current = files;
    setPendingFiles(files);
    setQueued(false);
    try {
      let paths: string[] = [];
      if (files.length > 0) {
        /* API-ATT-001 then API-ATT-002: upload the files, then send their
           paths with the text. */
        setUploading(true);
        const uploaded = await uploadAttachments(
          id,
          files.map((item) => item.file)
        );
        paths = uploaded.map((item) => item.path);
        setUploading(false);
      }
      const result = replyTarget
        ? await sendMessageRequest(id, text, paths, replyTarget.seq)
        : paths.length > 0
          ? await sendMessageRequest(id, text, paths)
          : await sendMessageRequest(id, text);
      /* API reply: a successful send clears the target; the stored message now
         carries its own reply_to. */
      setReplyTarget(null);
      if (result.command) {
        /* API-WF-021: a workflow chat command started a run, not a turn. */
        pendingRef.current = "";
        setPendingText("");
        pendingFilesRef.current = [];
        setPendingFiles([]);
        attachmentDraft.clear();
        showNotice(result.command.reply, "info");
        announce(result.command.reply);
        return;
      }
      if (result.turn === "queued") {
        setQueued(true);
        announce("Message queued. It will be claimed at the next step.");
      } else {
        if (!lastRunningRef.current) {
          announce("Turn running.");
        }
        if (result.turn_id) {
          setLiveTurnId(result.turn_id);
          setLiveStartedAt(Date.now());
        }
        setStreamText("");
        setLiveThinking(false);
        setLiveThought("");
      }
      lastRunningRef.current = true;
      setTurnRunning(true);
      /* The pending bubble owns the object URLs until the user/message event
         lands, so the chips are dropped without revoking them. */
      attachmentDraft.clear();
    } catch (failure) {
      pendingRef.current = "";
      setPendingText("");
      pendingFilesRef.current = [];
      setPendingFiles([]);
      setDraft((current) => current || text);
      /* Keep the files and the object URLs so the owner can retry. */
      attachmentDraft.restore(files);
      /* API-TFB-008: the daemon refuses new turns while it drains. Keep the
         typed text so the owner can send it again. */
      if (isDaemonDraining(failure)) {
        showNotice(DAEMON_DRAINING_TEXT, "warn");
        announce(DAEMON_DRAINING_TEXT);
      } else {
        reportError(failure, "Could not send the message.");
      }
    } finally {
      setUploading(false);
      setSending(false);
    }
  }

  async function cancelTurn() {
    if (cancelling) {
      return;
    }
    setCancelling(true);
    clearError();
    try {
      const result = await cancelTurnRequest(id);
      if (result.turn === "cancelling") {
        showNotice("Stopping the turn…", "info");
        announce("Stopping the turn.");
      } else {
        lastRunningRef.current = false;
        setTurnRunning(false);
        setCancelling(false);
        showNotice("No turn is running.", "info");
        announce("No turn is running.");
      }
    } catch (failure) {
      setCancelling(false);
      reportError(failure, "Could not stop the turn.");
    }
  }

  async function compactSession() {
    clearError();
    try {
      const result = await compactSessionRequest(id);
      const text =
        "Compacted from " +
        result.tokens_before +
        " to " +
        result.tokens_after +
        " tokens.";
      showNotice(text, "ok");
      announce(text);
      if (refreshRef.current) {
        refreshRef.current();
      }
    } catch (failure) {
      reportError(failure, "Could not compact the session.");
    }
  }

  async function forkSession() {
    clearError();
    try {
      const created = await forkSessionRequest(id);
      announce("Session forked.");
      window.location.hash = "#/sessions/" + encodeURIComponent(created.id);
    } catch (failure) {
      reportError(failure, "Could not fork the session.");
    }
  }

  /* REQ-TFB-005: Continue resumes the abnormally ended last turn. It follows
     the same live path as a sent message (API-TFB-003). */
  async function continueLastTurn() {
    if (continueBusy || readOnly || turnRunning) {
      return;
    }
    setContinueBusy(true);
    clearError();
    setContinueError("");
    try {
      const result = await continueSessionRequest(id);
      if (result.turn === "queued") {
        setQueued(true);
        announce("Message queued. It will be claimed at the next step.");
      } else {
        if (!lastRunningRef.current) {
          announce("Turn running.");
        }
        if (result.turn_id) {
          setLiveTurnId(result.turn_id);
          setLiveStartedAt(Date.now());
        }
        setStreamText("");
        setLiveThinking(false);
        setLiveThought("");
      }
      lastRunningRef.current = true;
      setTurnRunning(true);
    } catch (failure) {
      /* API-TFB-003: a refused Continue shows its reason in the status card. */
      const message = continueErrorMessage(failure);
      setContinueError(message);
      announce(message);
      if (refreshRef.current) {
        refreshRef.current();
      }
    } finally {
      setContinueBusy(false);
    }
  }

  const patch = useCallback(
    async (update: PatchSessionRequest, successText: string) => {
      clearError();
      try {
        const updated = await patchSession(id, update);
        setDetail(updated);
        setHolder(updated.in_use || "none");
        setHolderPid(updated.holder_pid || null);
        setTurnRunning(Boolean(updated.turn_running));
        lastRunningRef.current = Boolean(updated.turn_running);
        showNotice(successText, "ok");
        announce(successText);
        return true;
      } catch (failure) {
        reportError(failure, "Could not change the session.");
        return false;
      }
    },
    [announce, clearError, id, reportError, showNotice]
  );

  const changeAgent = useCallback(
    (name: string) => {
      const entry = agents.find((item) => item.name === name) || null;
      return patch(
        { agent: name },
        "Agent changed to " + (displayLabel(entry) || name) + "."
      );
    },
    [agents, patch]
  );

  const changeModel = useCallback(
    (value: string, resetEffort?: boolean) => {
      /* A team session never changes one model: each agent uses its own. */
      const currentAgent = agents.find(
        (entry) => entry.name === (detail && detail.agent)
      );
      if ((detail && detail.team) || isTeam(currentAgent || null)) {
        return Promise.resolve(false);
      }
      return patch(
        resetEffort ? { model: value, reasoning_effort: "" } : { model: value },
        "Model changed to " + value + "." +
          (resetEffort ? " Thinking reset to the model default." : "")
      );
    },
    [agents, detail, patch]
  );

  const changePolicy = useCallback(
    (value: string, confirmTrust: boolean) =>
      patch(
        confirmTrust ? { policy: value, confirm_trust: true } : { policy: value },
        "Policy changed to " + value + "."
      ),
    [patch]
  );

  /* REQ-MDL-001: "Model default" clears the session effort; a named effort
     sets it. A model change keeps the session effort (REQ-MDL-020). */
  const changeEffort = useCallback(
    (value: string) =>
      patch(
        { reasoning_effort: value },
        value
          ? "Thinking changed to " + effortName(value) + "."
          : "Thinking reset to the model default."
      ),
    [patch]
  );

  const handleApprovalAnswer = useCallback(
    async (
      approval: ApprovalEvent,
      outcome: string,
      proof?: ApprovalProof
    ): Promise<ApprovalAnswerResult> => {
      clearError();
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === approval.request_id
            ? { ...item, deciding: true, error: "" }
            : item
        )
      );
      try {
        await answerApproval(approval.request_id, outcome, undefined, proof);
        markApprovalDecided(
          approval.request_id,
          outcome,
          "Approval decided: " + approvalResultWords(outcome) + "."
        );
        return { ok: true };
      } catch (failure) {
        if (failure instanceof ApiError && failure.code === "approval_closed") {
          markApprovalDecided(
            approval.request_id,
            "approval_closed",
            "Approval closed. Another tab answered first or the approval expired."
          );
          return { ok: true };
        }
        const code = failure instanceof ApiError ? failure.code : "";
        /* A block-tier step-up error (API-HLF-005) is shown inline by the card;
           every other failure keeps the previous inline and toast report. */
        if (approval.hardline && approval.hardline.tier === "block") {
          setApprovals((current) =>
            current.map((item) =>
              item.request_id === approval.request_id
                ? { ...item, deciding: false }
                : item
            )
          );
        } else {
          setApprovals((current) =>
            current.map((item) =>
              item.request_id === approval.request_id
                ? { ...item, deciding: false, error: errorMessage(failure) }
                : item
            )
          );
          reportError(failure, "Could not answer the approval.");
        }
        return { ok: false, code, message: errorMessage(failure) };
      }
    },
    [clearError, markApprovalDecided, reportError]
  );

  const handleApprovalExpired = useCallback(
    (approval: ApprovalEvent) => {
      markApprovalDecided(
        approval.request_id,
        "unavailable",
        "Approval expired. The tool will not run."
      );
    },
    [markApprovalDecided]
  );

  const handleApprovalDismiss = useCallback(() => {
    setApprovals((current) => current.filter((item) => !item.decided));
    window.setTimeout(() => {
      const input = document.querySelector<HTMLTextAreaElement>(
        ".composer-input"
      );
      if (input) {
        input.focus();
      }
    }, 0);
  }, []);

  /* API reply: the Reply action of a sourced message sets the composer target. */
  const handleReply = useCallback((target: ReplyTarget) => {
    setReplyTarget(target);
  }, []);

  if (loading) {
    return (
      <main id="main" className="chat-view chat-state">
        <Loading label="Loading the session." />
      </main>
    );
  }

  if (loadError) {
    return (
      <main id="main" className="chat-view chat-state">
        <h1 className="chat-title">Session</h1>
        <ErrorNotice message={loadError} details={loadDetails} />
        <p>
          <a href="#/sessions">Back to sessions</a>
        </p>
      </main>
    );
  }

  if (!detail) {
    return (
      <main id="main" className="chat-view chat-state">
        <p className="muted">Session not found.</p>
        <p>
          <a href="#/sessions">Back to sessions</a>
        </p>
      </main>
    );
  }

  const agent = detail.agent || "agent";
  const team = detail.team || null;
  const agentEntry = agents.find((entry) => entry.name === agent) || null;
  const agentLabel = displayLabel(agentEntry) || agent;
  const teamEntry =
    team && team.name
      ? agents.find((entry) => entry.name === String(team.name)) || null
      : null;
  const teamLabel = team ? displayLabel(teamEntry) || String(team.name || "") : "";
  /* The display names of every loaded agent, so the team block names its
     members without another request. */
  const agentLabels: Record<string, string> = {};
  for (const entry of agents) {
    if (entry && entry.name) {
      agentLabels[entry.name] = displayLabel(entry);
    }
  }
  const activeApproval =
    approvals.find((approval) => !approval.decided) ||
    (approvals.length > 0 ? approvals[approvals.length - 1] || null : null);
  const channel = String(detail.channel || "");
  /* The responder of a question is the session's channel (REQ-ASK-015). The
     named question SSE event is web-only, so a web session gets the
     interactive card and every other channel gets the read-only one. */
  const questionResponder = channel || "web";
  const questionReadOnly = questionResponder !== "web";
  const questionResponderPid = questionResponder === "cli" ? holderPid : null;
  const waitingForInput =
    Boolean(activeQuestion) || detail.status === "waiting_input";
  const turnCount = Number(detail.turn_count) || 0;
  const hasThread =
    thread.length > 0 || pendingText !== "" || pendingFiles.length > 0;
  /* REQ-TFB-004: the end card shows under the session's last turn only. */
  const lastTurnKey = thread.reduce(
    (found: string, item) => (item.kind === "turn" ? item.key : found),
    ""
  );

  return (
    <main
      id="main"
      className="chat-view"
      {...fileDrop.handlers}
    >
      <AttachmentDropOverlay active={fileDrop.active} />
      <header className="chat-header">
        <div className="chat-header-title">
          <h1 className="chat-title">
            {editingTitle ? (
              <TitleInput
                initial={detail.title || ""}
                label="Session title"
                onSave={saveTitle}
                onCancel={endTitleEdit}
              />
            ) : (
              <button
                type="button"
                ref={titleButtonRef}
                className="chat-title-button"
                title="Rename session"
                aria-label={
                  "Rename session: " +
                  (detail.title ||
                    (detail.title_pending ? NAMING_SESSION : UNTITLED_SESSION))
                }
                onClick={() => setEditingTitle(true)}
              >
                <span
                  className={
                    "chat-title-text" +
                    (!detail.title && detail.title_pending ? " is-naming" : "") +
                    (!detail.title && !detail.title_pending ? " is-untitled" : "")
                  }
                >
                  {detail.title ||
                    (detail.title_pending ? NAMING_SESSION : UNTITLED_SESSION)}
                </span>
                <Ic name="pencil" size={14} className="chat-title-edit" />
              </button>
            )}
          </h1>
          <div className="chat-meta">
            <span
              className={
                "badge-ui" + (channel === "telegram" ? " badge-ui-brand" : "")
              }
            >
              <span className="badge-ui-dot" aria-hidden="true"></span>
              {channelLabel(channel)}
            </span>
            <span className="chat-turns">{turnCountText(turnCount)}</span>
            {readOnly ? (
              <span className="chat-holder">
                Read-only · {holderText(holder, holderPid)}
              </span>
            ) : null}
          </div>
        </div>
        <div className="chat-actions">
          <div className="chat-tabs" role="tablist" aria-label="Session view">
            <button
              type="button"
              role="tab"
              className="chat-tab"
              aria-selected={tab === "chat"}
              onClick={() => switchTab("chat")}
            >
              Chat
            </button>
            <button
              type="button"
              role="tab"
              className="chat-tab"
              aria-selected={tab === "trajectory"}
              onClick={() => switchTab("trajectory")}
            >
              Trajectory
            </button>
          </div>
          <button
            type="button"
            className="button-ui button-ui-tertiary"
            disabled={busySession}
            title={
              busySession
                ? "Compact is available on an idle session."
                : "Compact this session"
            }
            onClick={compactSession}
          >
            Compact
          </button>
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busySession}
            title={
              busySession
                ? "Fork is available on an idle session."
                : "Fork at the last completed turn"
            }
            onClick={forkSession}
          >
            Fork
          </button>
        </div>
      </header>

      <div className="chat-notices">
        {readOnly ? (
          <p className="notice notice-warn">
            Read-only: {holderText(holder, holderPid)} holds the session lease.
            Sending, stopping, and settings are disabled.
          </p>
        ) : null}
        {streamState === "reconnecting" ? (
          <p className="notice notice-warn">
            The live stream is reconnecting. History still updates from the event
            log.
          </p>
        ) : null}
        {notice ? (
          <p className={"notice notice-" + notice.tone}>{notice.text}</p>
        ) : null}
        {queued ? (
          <p className="notice notice-warn">
            Message queued. It will be claimed at the next step.
          </p>
        ) : null}
        {actionError ? (
          <ErrorNotice message={actionError} details={actionDetails} />
        ) : null}
      </div>

      {tab === "trajectory" ? (
        <TrajectoryView
          sessionId={id}
          events={events}
          childEvents={childEvents}
          onLoadChild={loadTrajectoryChild}
          running={turnRunning}
        />
      ) : (
        <div className="chat-messages" ref={messagesRef} onScroll={handleScroll}>
          <div className="chat-thread">
            {thread.map((item) =>
              item.kind === "user" ? (
                item.job ? (
                  <JobNoticeLine key={item.key} job={item.job} />
                ) : (
                  <UserBubble
                    key={item.key}
                    text={item.text}
                    continue={item.continue === true}
                    attachments={storedAttachments(id, item.attachments)}
                    replyTo={item.replyTo || null}
                    at={item.at || 0}
                  />
                )
              ) : (
                <TurnBlock
                  key={item.key}
                  turn={item.turn}
                  sessionId={id}
                  agent={agent}
                  agentLabel={agentLabel}
                  team={team}
                  teamLabels={agentLabels}
                  onReply={readOnly ? null : handleReply}
                  rootEvents={turnEvents.get(item.turn.id) || []}
                  childEvents={childEvents}
                  childLive={childLive}
                  onLoadChildren={loadChildEvents}
                  waitingForInput={waitingForInput}
                  showStatusCard={item.key === lastTurnKey && !turnRunning}
                  onContinue={continueLastTurn}
                  continueBusy={continueBusy}
                  continueError={continueError}
                  streamingText={
                    item.turn.running &&
                    (liveTurnId ? item.turn.id === liveTurnId : true)
                      ? streamText
                      : ""
                  }
                  thinking={
                    item.turn.running &&
                    (liveTurnId ? item.turn.id === liveTurnId : true) &&
                    liveThinking
                  }
                  liveThought={
                    item.turn.running &&
                    (liveTurnId ? item.turn.id === liveTurnId : true)
                      ? liveThought
                      : ""
                  }
                />
              )
            )}
            {pendingText || pendingFiles.length > 0 ? (
              <UserBubble
                key="pending"
                text={pendingText}
                attachments={pendingAttachments(pendingFiles)}
              />
            ) : null}
            {!hasThread ? (
              <div className="chat-empty">
                <p className="chat-empty-title">No messages yet</p>
                <p className="chat-empty-text">
                  Send the first message to {agentLabel} with the composer below.
                </p>
              </div>
            ) : null}
          </div>
        </div>
      )}
      <div className="chat-composer">
        <div className="chat-composer-inner">
          <div className="chat-slot">
            {activeApproval ? (
              <ApprovalCard
                key={activeApproval.request_id}
                approval={activeApproval}
                workspace={detail.workspace || ""}
                busy={Boolean(activeApproval.deciding)}
                onAnswer={handleApprovalAnswer}
                onExpire={handleApprovalExpired}
                onDismiss={handleApprovalDismiss}
              />
            ) : null}
            {activeQuestion ? (
              <QuestionCard
                key={"question-" + activeQuestion.request_id}
                question={activeQuestion}
                responder={questionResponder}
                responderPid={questionResponderPid}
                readOnly={questionReadOnly}
                busy={questionBusy}
                onSubmit={handleQuestionSubmit}
                onSkipAll={handleQuestionSkipAll}
                onExpire={handleQuestionExpired}
              />
            ) : null}
            {secrets.request ? (
              <SecretRequestCard
                key={"secret-" + secrets.request.request_id}
                request={secrets.request}
                agentLabel={agentLabel}
                onSave={secrets.save}
                onCancel={secrets.cancel}
                onExpire={secrets.expire}
                onDismiss={secrets.dismiss}
              />
            ) : null}
          </div>
          <div className={"cx" + (turnRunning ? " is-live" : "")}>
          <Composer
            tray={<ComposerTray sessionId={id} refreshKey={jobRefresh} />}
            agent={agent}
            agentLabel={agentLabel}
            team={team}
            teamLabel={teamLabel}
            model={detail.model || ""}
            policy={detail.policy || "ask"}
            reasoningEffort={detail.reasoning_effort || ""}
            agents={agents}
            draft={draft}
            attachments={attachmentDraft.items}
            attachmentNotice={attachmentDraft.notice}
            uploading={uploading}
            readOnly={readOnly}
            questionOpen={Boolean(activeQuestion)}
            turnRunning={turnRunning}
            cancelling={cancelling}
            sending={sending}
            replyTarget={replyTarget}
            onDraft={setDraft}
            onFiles={attachmentDraft.addFiles}
            onRemoveAttachment={attachmentDraft.remove}
            onSend={sendMessage}
            onStop={cancelTurn}
            onCancelReply={() => setReplyTarget(null)}
            onAgent={changeAgent}
            onModel={changeModel}
            onEffort={changeEffort}
            onPolicy={changePolicy}
          />
          </div>
        </div>
      </div>
    </main>
  );
}

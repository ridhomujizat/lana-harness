/* eslint-disable react-hooks/set-state-in-effect -- the detail load resets its state before each request, as in agent-detail.js */
import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, FormEvent, KeyboardEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { duplicateAgent, listAgents, updateAgent } from "../api/agents";
import type { Agent, AgentWriteFields } from "../api/agents";
import { getModels } from "../api/models";
import type { CatalogEffort, CatalogModel } from "../api/models";
import { getConfig } from "../api/settings";
import { getRouter } from "../api/router";
import type { RouterChoice, RouterCombo, RouterProvider } from "../api/router";
import type { ConfigAgent } from "../api/settings";
import { createSession } from "../api/sessions";
import { Dialog, detailText, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import {
  isTeam,
  rosterNames,
  splitModel,
  splitTarget,
  CLAUDE_RUNTIME,
  usesClaudeCode,
  normalizePermission,
  agentHref,
  sessionSummary,
  presenceTone,
} from "./agent-common";
import type { AgentLike, Permission } from "./agent-common";
import { AgentOverview } from "./agent-overview";
import { CLAUDE_EFFORTS, ClaudeCodeNote, choiceOptions } from "./agent-runtime";
import {
  CLAUDE_CODE_HREF,
  ClaudeCodeBadge,
  claudeAccountNames,
  claudeTargetAccount,
  useClaudeAccounts,
} from "./router/claude-code";
import { gr8rColor, useRooms } from "./presence";
import {
  AGENT_ID_HINT,
  AGENT_ID_RULE,
  MAX_DISPLAY_NAME,
  displayLabel,
  slugifyAgentName,
} from "./agent-label";
import { DEFAULT_AGENT_TAB } from "../app/router";
import { PersonaTab } from "./agent-tabs/persona";
import { ToolsTab } from "./agent-tabs/tools";
import { SkillsTab } from "./agent-tabs/skills";
import { McpTab } from "./agent-tabs/mcp";
import { RulesTab } from "./agent-tabs/rules";
import { WorkspaceTab } from "./agent-tabs/workspace";
import {
  Badge,
  Button,
  Chip,
  ErrorState,
  Input,
  Radio,
  Select,
  Skeleton,
  Tabs,
  Toggle,
} from "../ui/gr8r";
import { AgentAvatar, AgentAvatarStack } from "./agent-avatar";
import { GenerateAvatarButton } from "./avatar-dialog";
import type { AvatarSavedResult } from "./avatar-dialog";
import type { SelectOption } from "../ui/gr8r";

/* REQ-WEB-047: the agent detail header, the seven-tab bar, and the General
   tab. The other tabs show a placeholder until their slices land (T-030). */

type TabId =
  | "overview"
  | "general"
  | "persona"
  | "tools"
  | "skills"
  | "mcp"
  | "rules"
  | "workspace";

interface TabEntry {
  id: TabId;
  label: string;
}

const TABS: readonly TabEntry[] = [
  { id: "overview", label: "Overview" },
  { id: "general", label: "General" },
  { id: "persona", label: "Persona & memory" },
  { id: "tools", label: "Tools" },
  { id: "skills", label: "Skills" },
  { id: "mcp", label: "MCP" },
  { id: "rules", label: "Rules" },
  { id: "workspace", label: "Workspace" },
];

type GeneralKey =
  | "display_name"
  | "description"
  | "title"
  | "color"
  | "provider"
  | "model"
  | "reasoning_effort"
  | "fallback"
  | "permission"
  | "max_steps"
  | "context_window"
  | "memory_capture"
  | "workspace_only";

const GENERAL_KEYS: readonly GeneralKey[] = [
  "display_name",
  "description",
  "title",
  "color",
  "provider",
  "model",
  "reasoning_effort",
  "fallback",
  "permission",
  "max_steps",
  "context_window",
  "memory_capture",
  "workspace_only",
];

/* REQ-ROOM-009: the six agent colors and their swatch previews. */
const AGENT_COLORS: readonly { value: string; label: string }[] = [
  { value: "brand", label: "Brand" },
  { value: "success", label: "Success" },
  { value: "warning", label: "Warning" },
  { value: "error", label: "Error" },
  { value: "blue", label: "Blue" },
  { value: "gray", label: "Gray" },
];

const TITLE_LIMIT = 60;

interface PermissionOption {
  value: Permission;
  title: string;
  note: string;
}

const PERMISSIONS: readonly PermissionOption[] = [
  {
    value: "ask",
    title: "Ask",
    note: "Ask me before each risky tool.",
  },
  {
    value: "trust",
    title: "Trust",
    note: "Run every tool without asking.",
  },
  {
    value: "never",
    title: "Never",
    note: "Block risky tools. Read-only work only.",
  },
];

/* Spec 37: the model picker values that are not router choices. */
const OTHER_MODEL = "__other__";
const LEGACY_MODEL = "__legacy__";

interface AgentValues {
  display_name: string;
  description: string;
  title: string;
  color: string;
  provider: string;
  model: string;
  reasoning_effort: string;
  fallback: string[];
  permission: Permission;
  max_steps: number;
  context_window: number;
  memory_capture: boolean;
  workspace_only: boolean;
}

interface AgentForm {
  display_name: string;
  description: string;
  title: string;
  color: string;
  provider: string;
  model: string;
  reasoning_effort: string;
  fallback: string[];
  permission: Permission;
  max_steps: string;
  context_window: string;
  memory_capture: boolean;
  workspace_only: boolean;
}

interface FallbackDraft {
  provider: string;
  model: string;
}

interface AgentDetailData {
  agent: Agent | null;
  agents: Agent[];
  /* GET /v1/router choices: combos, then models by provider (spec 37). */
  choices: RouterChoice[];
  /* GET /v1/router providers, for the logos of the model picker. */
  providers: RouterProvider[];
  /* GET /v1/router combos: a combo whose first model is Claude Code makes
     the agent a Claude Code agent (spec 39). */
  combos: RouterCombo[];
  /* The configured tool list, so General can gate the memory toggle. */
  tools: string[];
}

function pickText(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string" && value !== "") {
      return value;
    }
  }
  return "";
}

function numberValue(value: unknown): number {
  const text = String(value === undefined || value === null ? "" : value).trim();
  if (text === "") {
    return 0;
  }
  const parsed = Number(text);
  return Number.isFinite(parsed) ? parsed : 0;
}

/* REQ-ROOM-009: an agent color is one of the six names; since spec 43 it may
   also be a "#rrggbb" hex set in the avatar dialog. Anything else is brand. */
function normalizeColor(value: unknown): string {
  const name = String(value || "").trim().toLowerCase();
  if (/^#[0-9a-f]{6}$/.test(name)) {
    return name;
  }
  return AGENT_COLORS.some((entry) => entry.value === name) ? name : "brand";
}

/* The field errors of the agents API carry the config key, for example
   agents.dana.title. Pick the message for one key. */
function issueMessage(details: unknown[], key: string): string {
  for (const detail of details) {
    if (detail && typeof detail === "object") {
      const record = detail as { key?: unknown; message?: unknown };
      if (String(record.key || "") === key) {
        return record.message ? String(record.message) : "Invalid value.";
      }
    }
  }
  return "";
}

/* The Thinking option text carries the effort description of REQ-MDL-020. */
function effortLabel(effort: CatalogEffort): string {
  const value = String(effort.value || "");
  const label = value ? value.charAt(0).toUpperCase() + value.slice(1) : "";
  return effort.description ? label + " — " + effort.description : label;
}

/* POST /v1/sessions accepts "" (use the agent's model) or "<provider>:<model>". */
function sessionModel(agent: AgentLike | null | undefined): string {
  const value = agent && agent.model ? String(agent.model) : "";
  if (!value) {
    return "";
  }
  if (value.indexOf(":") > 0) {
    return value;
  }
  if (agent && agent.provider) {
    return agent.provider + ":" + value;
  }
  return "";
}

/* Spec 39 (API-CCT-002): a legacy file says runtime = "claude-code" with
   model "M"; it means model "claude-code/M". The list row already carries
   the mapped address. */
function isLegacyRuntime(agent: Agent, configAgent: ConfigAgent): boolean {
  const record = configAgent || {};
  return pickText(record.runtime, agent.runtime) === CLAUDE_RUNTIME;
}

function legacyAddress(agent: Agent, configAgent: ConfigAgent): string {
  const row = agent.model ? String(agent.model) : "";
  if (row.startsWith(CLAUDE_RUNTIME + "/")) {
    return row;
  }
  const model = pickText((configAgent || {}).model, row) || "default";
  return model.startsWith(CLAUDE_RUNTIME + "/") ? model : CLAUDE_RUNTIME + "/" + model;
}

function initialValues(agent: Agent, configAgent: ConfigAgent): AgentValues {
  const record = configAgent || {};
  const parts = splitModel(agent);
  const legacy = isLegacyRuntime(agent, configAgent);
  return {
    display_name: pickText(record.display_name, agent.display_name),
    description: pickText(record.description, agent.description),
    title: pickText(record.title, agent.title),
    color: normalizeColor(pickText(record.color, agent.color)),
    provider: legacy ? "" : pickText(record.provider, agent.provider, parts.provider),
    model: legacy ? legacyAddress(agent, configAgent) : pickText(record.model, parts.model),
    reasoning_effort: pickText(record.reasoning_effort, agent.reasoning_effort),
    fallback: Array.isArray(record.fallback)
      ? record.fallback.map((target) => String(target))
      : [],
    permission: normalizePermission(
      pickText(record.permission, agent.permission, agent.policy)
    ),
    max_steps: Number(record.max_steps) || 0,
    context_window: Number(record.context_window) || 0,
    // REQ-MEM-010: memory_capture is on unless the config says false.
    memory_capture: record.memory_capture !== false,
    // workspace_only is off unless the config says true.
    workspace_only: record.workspace_only === true,
  };
}

function formValues(initial: AgentValues): AgentForm {
  return {
    display_name: initial.display_name || "",
    description: initial.description || "",
    title: initial.title || "",
    color: normalizeColor(initial.color),
    provider: initial.provider || "",
    model: initial.model || "",
    reasoning_effort: initial.reasoning_effort || "",
    fallback: Array.isArray(initial.fallback) ? initial.fallback.slice() : [],
    permission: normalizePermission(initial.permission),
    max_steps: initial.max_steps ? String(initial.max_steps) : "",
    context_window: initial.context_window ? String(initial.context_window) : "",
    memory_capture: initial.memory_capture !== false,
    workspace_only: initial.workspace_only === true,
  };
}

function canonical(form: AgentForm): AgentValues {
  return {
    display_name: String(form.display_name || "").trim(),
    description: String(form.description || "").trim(),
    title: String(form.title || "").trim(),
    color: normalizeColor(form.color),
    provider: form.provider || "",
    model: form.model || "",
    reasoning_effort: form.reasoning_effort || "",
    fallback: Array.isArray(form.fallback) ? form.fallback.slice() : [],
    permission: normalizePermission(form.permission),
    max_steps: numberValue(form.max_steps),
    context_window: numberValue(form.context_window),
    memory_capture: form.memory_capture === true,
    workspace_only: form.workspace_only === true,
  };
}

function sameValue(left: unknown, right: unknown): boolean {
  if (Array.isArray(left) && Array.isArray(right)) {
    return JSON.stringify(left) === JSON.stringify(right);
  }
  return left === right;
}

function dirtyKeys(
  form: AgentForm | null,
  baseline: AgentValues | null
): GeneralKey[] {
  if (!form || !baseline) {
    return [];
  }
  const current = canonical(form);
  return GENERAL_KEYS.filter((key) => !sameValue(current[key], baseline[key]));
}

function validateForm(form: AgentForm, baseline: AgentValues): string {
  if (Array.from(String(form.display_name || "")).length > MAX_DISPLAY_NAME) {
    return "The display name is at most " + MAX_DISPLAY_NAME + " characters.";
  }
  /* REQ-ROOM-009: the title is a single line of at most 60 characters. */
  if (Array.from(String(form.title || "")).length > TITLE_LIMIT) {
    return "The title is at most " + TITLE_LIMIT + " characters.";
  }

  /* REQ-LTC-040: an empty max_steps means no limit; any whole number of 0 or
     more caps the turn. */
  const stepsText = String(form.max_steps || "").trim();
  if (stepsText !== "") {
    const steps = Number(stepsText);
    if (!Number.isInteger(steps) || steps < 0) {
      return "Max steps per turn must be a whole number of 0 or more, or empty for no limit.";
    }
  }

  const contextText = String(form.context_window || "").trim();
  if (contextText === "") {
    if (baseline.context_window > 0) {
      return "Enter a context window greater than 0.";
    }
  } else {
    const context = Number(contextText);
    if (!Number.isInteger(context) || context <= 0) {
      return "Context window must be a whole number greater than 0.";
    }
  }

  for (const target of form.fallback) {
    const parts = splitTarget(target);
    if (!parts.provider || !parts.model) {
      return "Each fallback must be a provider and a model.";
    }
  }
  return "";
}

function nextDuplicateName(source: string, agents: AgentLike[]): string {
  const names = new Set(agents.map((agent) => agent.name));
  let index = 2;
  while (names.has(source + "-" + index)) {
    index += 1;
  }
  return source + "-" + index;
}

function patchFor(current: AgentValues, keys: GeneralKey[]): AgentWriteFields {
  const patch: AgentWriteFields = {};
  for (const key of keys) {
    if (key === "display_name") {
      patch.display_name = current.display_name;
    } else if (key === "description") {
      patch.description = current.description;
    } else if (key === "title") {
      patch.title = current.title;
    } else if (key === "color") {
      patch.color = current.color;
    } else if (key === "provider") {
      patch.provider = current.provider;
    } else if (key === "model") {
      patch.model = current.model;
    } else if (key === "reasoning_effort") {
      patch.reasoning_effort = current.reasoning_effort;
    } else if (key === "fallback") {
      patch.fallback = current.fallback.slice();
    } else if (key === "permission") {
      patch.permission = current.permission;
    } else if (key === "max_steps") {
      patch.max_steps = current.max_steps;
    } else if (key === "context_window") {
      patch.context_window = current.context_window;
    } else if (key === "memory_capture") {
      patch.memory_capture = current.memory_capture;
    } else if (key === "workspace_only") {
      patch.workspace_only = current.workspace_only;
    }
  }
  return patch;
}

interface DuplicateAgentDialogProps {
  source: string;
  agents: Agent[];
  onClose: () => void;
  onDuplicated?: (name: string) => void;
}

/* The error details of the agents API, rendered under an error state. */
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

function DuplicateAgentDialog({
  source,
  agents,
  onClose,
  onDuplicated,
}: DuplicateAgentDialogProps) {
  const announce = useAnnounce();
  const sourceAgent = agents.find((item) => item.name === source);
  const sourceLabel = displayLabel(sourceAgent) || source;
  const [agentId, setAgentId] = useState(() =>
    nextDuplicateName(source, agents)
  );
  const [label, setLabel] = useState(
    () => sourceLabel + " " + agentId.slice(source.length + 1)
  );
  const [idTouched, setIdTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const displayName = label.trim();
    const value = agentId.trim();
    if (!displayName) {
      setError("Enter a name for the new agent.");
      return;
    }
    if (Array.from(displayName).length > MAX_DISPLAY_NAME) {
      setError("The name must be at most " + MAX_DISPLAY_NAME + " characters.");
      return;
    }
    if (!AGENT_ID_RULE.test(value)) {
      setError("The ID is not valid. " + AGENT_ID_HINT);
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const created = await duplicateAgent(
        source,
        value,
        displayName !== value ? displayName : undefined
      );
      const createdName = created && created.name ? created.name : value;
      announce("Agent " + displayName + " created from " + sourceLabel + ".");
      if (onDuplicated) {
        onDuplicated(createdName);
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        setError(
          "An agent with the ID " + value + " already exists. Change the ID."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
      announce("Could not duplicate the agent. " + errorMessage(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title="Duplicate agent" dismissible={!busy} onClose={onClose}>
      <form className="agent-dialog-form" onSubmit={submit}>
        <p className="hint">
          Copy <strong>{sourceLabel}</strong> to a new agent. The config table
          and AGENT.md are copied; MEMORY.md is not.
        </p>
        <div className="field">
          <label className="label" htmlFor="duplicate-agent-label">
            New name
          </label>
          <Input
            id="duplicate-agent-label"
            data-autofocus
            type="text"
            value={label}
            autoComplete="off"
            maxLength={MAX_DISPLAY_NAME}
            required
            invalid={Boolean(error)}
            onChange={(event) => {
              setLabel(event.target.value);
              if (!idTouched) {
                setAgentId(slugifyAgentName(event.target.value));
              }
            }}
          />
          <span className="hint">The name people see.</span>
        </div>
        <div className="field">
          <label className="label" htmlFor="duplicate-agent-name">
            ID
          </label>
          <Input
            id="duplicate-agent-name"
            type="text"
            value={agentId}
            autoComplete="off"
            spellCheck={false}
            maxLength={32}
            required
            invalid={Boolean(error)}
            onChange={(event) => {
              setIdTouched(true);
              setAgentId(event.target.value);
            }}
          />
          <span className="hint">
            Used in links, the config file, and @mentions.
          </span>
        </div>
        {error ? (
          <div role="alert">
            <p className="err">
              <Ic name="circle-alert" size={12} />
              {error}
            </p>
            <DetailsList details={details} />
          </div>
        ) : null}
        <div className="dialog-actions">
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={busy}>
            {busy ? "Duplicating…" : "Duplicate"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export interface AgentDetailPageProps {
  name?: string;
  tab?: string;
}

export function AgentDetailPage({ name, tab }: AgentDetailPageProps) {
  const announce = useAnnounce();
  const { byName } = useRooms();
  const agentName = name ?? "";
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [page, setPage] = useState<AgentDetailData | null>(null);
  const [version, setVersion] = useState("");
  const [form, setForm] = useState<AgentForm | null>(null);
  const [baseline, setBaseline] = useState<AgentValues | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveNotice, setSaveNotice] = useState("");
  const [restartNotice, setRestartNotice] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [stale, setStale] = useState(false);
  const [fallbackDraft, setFallbackDraft] = useState<FallbackDraft | null>(null);
  const [otherModel, setOtherModel] = useState(false);
  const [catalog, setCatalog] = useState<CatalogModel[]>([]);
  const [efforts, setEfforts] = useState<CatalogEffort[]>([]);
  const [headerError, setHeaderError] = useState("");
  const [startBusy, setStartBusy] = useState(false);
  const [duplicateOpen, setDuplicateOpen] = useState(false);
  /* A legacy runtime = "claude-code" file is rewritten on the next save. */
  const [legacyRuntime, setLegacyRuntime] = useState(false);
  const { accounts } = useClaudeAccounts();
  const tabsRef = useRef<HTMLDivElement | null>(null);
  const pendingTabFocus = useRef(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setPage(null);
    setForm(null);
    setBaseline(null);
    setError("");
    setDetails([]);
    setSaveNotice("");
    setRestartNotice(false);
    setStale(false);
    setFallbackDraft(null);
    setOtherModel(false);
    setHeaderError("");
    setLegacyRuntime(false);

    Promise.all([
      listAgents(),
      getConfig().catch(() => null),
      getRouter().catch(() => null),
    ])
      .then(([agentPage, config, routerPage]) => {
        if (!alive) {
          return;
        }
        const rows =
          agentPage && Array.isArray(agentPage.agents) ? agentPage.agents : [];
        const agent = rows.find((row) => row.name === agentName) || null;
        const settings = config && config.settings ? config.settings : null;
        const configAgent: ConfigAgent = settings?.agents?.[agentName] ?? {};
        const choices = routerPage ? routerPage.choices : [];
        const initial = agent ? initialValues(agent, configAgent) : null;
        const tools = Array.isArray(configAgent.tools)
          ? configAgent.tools.map((tool) => String(tool))
          : [];
        setPage({
          agent,
          agents: rows,
          choices,
          providers: routerPage ? routerPage.providers : [],
          combos: routerPage ? routerPage.combos : [],
          tools,
        });
        setLegacyRuntime(Boolean(agent && isLegacyRuntime(agent, configAgent)));
        setVersion(config && config.version ? String(config.version) : "");
        if (initial) {
          const values = formValues(initial);
          setForm(values);
          setBaseline(canonical(values));
        }
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        announce("Could not load the agent. " + errorMessage(failure));
      });

    return () => {
      alive = false;
    };
  }, [agentName, reloadToken, announce]);

  /* Spec 39: every agent is a lana agent with every tab; a Claude Code turn
     gets the persona, memory, and lana tools through MCP. */
  const tabs = TABS;
  const activeTab: TabId =
    tabs.find((item) => item.id === tab)?.id ?? DEFAULT_AGENT_TAB;

  /* The tab bar keeps its focus when a hash change reloads the route. */
  useEffect(() => {
    if (!pendingTabFocus.current) {
      return;
    }
    pendingTabFocus.current = false;
    const node = tabsRef.current?.querySelector<HTMLElement>(
      '.tab[aria-selected="true"]'
    );
    if (node) {
      node.focus();
    }
  }, [activeTab]);

  /* API-CCT-002: saving a legacy runtime file always writes the model
     address, so the file loses runtime. */
  const dirty = useMemo(() => {
    const keys = dirtyKeys(form, baseline);
    if (legacyRuntime && form && !keys.includes("model")) {
      keys.push("model");
    }
    return keys;
  }, [form, baseline, legacyRuntime]);

  const choices = useMemo(() => (page ? page.choices : []), [page]);
  const legacy = Boolean(form && form.provider);
  const formModel = form ? form.model : "";
  const claudeNames = useMemo(
    () => claudeAccountNames(choices, page ? page.providers : null, accounts),
    [choices, page, accounts]
  );
  /* REQ-CCT-009: the Claude Code account the picked model runs on. */
  const claudeAccount = legacy
    ? ""
    : claudeTargetAccount(formModel, choices, page ? page.combos : null, claudeNames);
  const isClaudeForm = claudeAccount !== "";
  const knownChoice = choices.some((choice) => choice.value === formModel);
  const isCombo = choices.some(
    (choice) => choice.group === "Combos" && choice.value === formModel
  );
  /* A router address names its provider before the first slash; a combo
     has none, so it shows no catalog (REQ-MDL-020 keeps the Thinking menu). */
  const slash = formModel.indexOf("/");
  const addressProvider =
    !legacy && !isCombo && slash > 0 ? formModel.slice(0, slash) : "";
  const provider = isClaudeForm
    ? ""
    : legacy && form
      ? form.provider
      : addressProvider;
  const catalogId = legacy
    ? formModel
    : addressProvider
      ? formModel.slice(slash + 1)
      : "";

  /* REQ-MDL-020: the model picker lists the catalog of the agent's provider.
     The provider's base URL decides the catalog on the daemon side. */
  useEffect(() => {
    if (!provider) {
      setCatalog([]);
      setEfforts([]);
      return undefined;
    }
    let alive = true;
    getModels(provider)
      .then((data) => {
        if (!alive) {
          return;
        }
        setCatalog(data && Array.isArray(data.models) ? data.models : []);
        setEfforts(data && Array.isArray(data.efforts) ? data.efforts : []);
      })
      .catch(() => {
        if (!alive) {
          return;
        }
        setCatalog([]);
        setEfforts([]);
      });
    return () => {
      alive = false;
    };
  }, [provider]);

  /* A model that is not in the catalog stays selected through the free-text
     field, so a save never drops it. */
  const selectedCatalogModel = catalogId
    ? catalog.find((model) => model.id === catalogId) || null
    : null;
  /* A non-legacy model that is not a router choice stays in the free-text
     field, so a save never drops it. */
  const customModel = Boolean(
    !legacy && (otherModel || (formModel !== "" && !knownChoice))
  );
  const thinkingModel = Boolean(selectedCatalogModel && selectedCatalogModel.thinking);
  const legacyReplacement =
    selectedCatalogModel && selectedCatalogModel.legacy
      ? selectedCatalogModel.replacement
      : "";

  function updateField<K extends keyof AgentForm>(
    key: K,
    value: AgentForm[K]
  ) {
    setForm((current) => {
      if (!current) {
        return current;
      }
      const next = Object.assign({}, current);
      next[key] = value;
      return next;
    });
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function reload() {
    setReloadToken((current) => current + 1);
  }

  /* Spec 43: the avatar dialog writes the agent color too. The form and its
     baseline follow the saved color, and the config version is read again
     so the next General save does not trip the version check. */
  function avatarSaved(result: AvatarSavedResult) {
    const savedColor = normalizeColor(result.color);
    setPage((current) =>
      current && current.agent
        ? {
            ...current,
            agent: { ...current.agent, color: savedColor, avatar_svg: result.svg },
          }
        : current
    );
    setForm((current) => (current ? { ...current, color: savedColor } : current));
    setBaseline((current) => (current ? { ...current, color: savedColor } : current));
    if (result.restartRequired) {
      setRestartNotice(true);
    }
    getConfig()
      .then((config) => {
        if (config && config.version) {
          setVersion(String(config.version));
        }
      })
      .catch(() => {
        /* The next save reports a stale version and offers a reload. */
      });
  }

  /* Spec 37 (AC-RTR-024): picking a router choice writes model and clears a
     legacy provider; the legacy entry restores the saved pair. */
  function pickModel(value: string) {
    if (!form || !baseline) {
      return;
    }
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
    if (value === OTHER_MODEL) {
      setOtherModel(true);
      setForm({ ...form, provider: "" });
      return;
    }
    setOtherModel(false);
    if (value === LEGACY_MODEL) {
      setForm({ ...form, provider: baseline.provider, model: baseline.model });
      return;
    }
    /* Claude Code and router models take different effort values, so a
       switch across them starts from the model's default. */
    const nextClaude =
      claudeTargetAccount(value, choices, page ? page.combos : null, claudeNames) !== "";
    const effort =
      value === baseline.model
        ? baseline.reasoning_effort
        : nextClaude !== isClaudeForm
          ? ""
          : form.reasoning_effort;
    /* API-CCT-002: provider is only cleared for a legacy provider/model
       pair; a Claude Code pick writes the model address alone. */
    setForm({ ...form, provider: "", model: value, reasoning_effort: effort });
  }

  function resetForm() {
    if (!baseline) {
      return;
    }
    setForm(formValues(baseline));
    setOtherModel(false);
    setFallbackDraft(null);
    setSaveNotice("");
    setError("");
    setDetails([]);
    setStale(false);
  }

  function activateTab(id: string, focus: boolean) {
    const next = tabs.find((item) => item.id === id);
    if (!next) {
      return;
    }
    if (next.id === activeTab) {
      if (focus) {
        const node = tabsRef.current?.querySelector<HTMLElement>(
          '.tab[aria-selected="true"]'
        );
        if (node) {
          node.focus();
        }
      }
      return;
    }
    pendingTabFocus.current = Boolean(focus);
    window.location.hash = agentHref(agentName, next.id);
  }

  function handleTabKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (
      event.key !== "ArrowRight" &&
      event.key !== "ArrowLeft" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const nodes = Array.from(
      tabsRef.current?.querySelectorAll<HTMLButtonElement>(".tab") ?? []
    );
    if (nodes.length === 0) {
      return;
    }
    const current = nodes.findIndex((node) => node === document.activeElement);
    let next = current < 0 ? 0 : current;
    if (event.key === "ArrowRight") {
      next = (next + 1) % nodes.length;
    } else if (event.key === "ArrowLeft") {
      next = (next - 1 + nodes.length) % nodes.length;
    } else if (event.key === "Home") {
      next = 0;
    } else {
      next = nodes.length - 1;
    }
    event.preventDefault();
    const target = tabs[next];
    if (!target) {
      return;
    }
    if (target.id === activeTab) {
      nodes[next]?.focus();
      return;
    }
    pendingTabFocus.current = true;
    window.location.hash = agentHref(agentName, target.id);
  }

  function startFallback() {
    setFallbackDraft({ provider: "", model: "" });
  }

  function updateFallbackDraft<K extends keyof FallbackDraft>(
    key: K,
    value: FallbackDraft[K]
  ) {
    setFallbackDraft((current) => {
      if (!current) {
        return current;
      }
      const next = Object.assign({}, current);
      next[key] = value;
      return next;
    });
  }

  function addFallback() {
    if (!fallbackDraft || !form) {
      return;
    }
    const provider = fallbackDraft.provider.trim();
    const model = fallbackDraft.model.trim();
    if (!provider || !model) {
      return;
    }
    updateField("fallback", form.fallback.concat([provider + ":" + model]));
    setFallbackDraft(null);
  }

  function removeFallback(index: number) {
    if (!form) {
      return;
    }
    updateField(
      "fallback",
      form.fallback.filter((_target, position) => position !== index)
    );
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!form || !baseline || saving) {
      return;
    }
    setSaveNotice("");
    setRestartNotice(false);
    setError("");
    setDetails([]);
    setStale(false);

    const problem = validateForm(form, baseline);
    if (problem) {
      setError(problem);
      return;
    }
    if (!version) {
      setError(
        "The config version is unavailable. Reload the page and try again."
      );
      return;
    }
    if (dirty.length === 0) {
      announce("No changes to save.");
      return;
    }

    const current = canonical(form);
    const patch = patchFor(current, dirty);

    setSaving(true);
    try {
      const result = await updateAgent(agentName, patch, version);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setBaseline(current);
      setLegacyRuntime(false);
      setSaveNotice("Changes saved.");
      setRestartNotice(Boolean(result && result.restart_required));
      announce("Saved changes to " + agentName + ".");
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload to get the latest version, then save again."
        );
        announce("The save did not apply because the config changed.");
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not save changes. " + errorMessage(failure));
      }
    } finally {
      setSaving(false);
    }
  }

  async function startSession() {
    if (startBusy || !page || !page.agent) {
      return;
    }
    setStartBusy(true);
    setHeaderError("");
    try {
      const model = sessionModel(page.agent);
      const created = await createSession(agentName, model);
      if (!created || !created.id) {
        throw new ApiError(
          0,
          "invalid_response",
          "The daemon did not return a session id.",
          []
        );
      }
      announce("Session started with " + agentName + ".");
      window.location.hash = "#/sessions/" + encodeURIComponent(created.id);
    } catch (failure) {
      setHeaderError(errorMessage(failure));
      announce("Could not start a session. " + errorMessage(failure));
    } finally {
      setStartBusy(false);
    }
  }

  if (status === "loading") {
    return (
      <main id="main" className="page agent-detail-page">
        <span className="sr" role="status">
          Loading agent…
        </span>
        <Skeleton width={220} height={24} />
        <Skeleton width="100%" height={120} radius={12} />
        <Skeleton width="100%" height={220} radius={12} />
      </main>
    );
  }

  if (status === "error") {
    return (
      <main id="main" className="page agent-detail-page">
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load the agent"
            text={error}
            action={
              <Button variant="secondary" onClick={reload}>
                Try again
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
        <div className="agent-state-actions">
          <Button as="a" variant="secondary" href="#/agents">
            Back to agents
          </Button>
        </div>
      </main>
    );
  }

  const agent = page ? page.agent : null;
  if (!agent) {
    return (
      <main id="main" className="page agent-detail-page">
        <header className="ph">
          <div>
            <h1>Agent not found</h1>
            <p>
              No configured agent or team is named <code>{agentName}</code>.
            </p>
          </div>
          <div className="acts">
            <Button as="a" variant="secondary" href="#/agents">
              Back to agents
            </Button>
          </div>
        </header>
      </main>
    );
  }

  if (isTeam(agent)) {
    return (
      <main id="main" className="page agent-detail-page">
        <header className="ph agent-detail-header">
          <div className="agent-detail-identity">
            <AgentAvatarStack
              size="lg"
              max={3}
              items={rosterNames(agent).map((member) => ({
                name: member,
                color: gr8rColor(member),
              }))}
            />
            <div className="agent-detail-heading">
              <div className="agent-detail-name-row">
                <h1 className="agent-detail-name">{displayLabel(agent)}</h1>
                <Badge tone="accent" dot>
                  Team
                </Badge>
              </div>
              <p className="agent-detail-meta">{sessionSummary(agent)}.</p>
            </div>
          </div>
          <div className="acts">
            <Button
              as="a"
              variant="primary"
              href={"#/chats/" + encodeURIComponent(agent.name)}
            >
              {"Chat with " + agent.name}
            </Button>
            <Button as="a" variant="secondary" href="#/settings/config">
              Open config
            </Button>
          </div>
        </header>

        <AgentOverview name={agentName} agent={agent} />

        <section className="panel">
          <div className="panel-h">
            <h2>Read-only</h2>
          </div>
          <div className="panel-b agent-team-readonly">
            <p className="hint">
              Teams are read-only here. Edit the{" "}
              <code>[teams.{agent.name}]</code> table in the config file.
            </p>
            <Button as="a" variant="secondary" href="#/settings/config">
              Open the config file
            </Button>
          </div>
        </section>
      </main>
    );
  }

  const canSave = dirty.length > 0 && !saving;
  const knownAgents = page ? page.agents : [];
  const titleIssue = issueMessage(details, "agents." + agent.name + ".title");
  const displayNameIssue = issueMessage(
    details,
    "agents." + agent.name + ".display_name"
  );
  const colorIssue = issueMessage(details, "agents." + agent.name + ".color");
  const row = byName[agent.name];
  const presence = row ? row.presence : null;
  const activeLabel =
    tabs.find((item) => item.id === activeTab)?.label || "Agent";

  const legacyName = selectedCatalogModel
    ? selectedCatalogModel.name
    : form
      ? form.model
      : "";
  const modelOptions: SelectOption[] = [
    ...(form && !form.model && !legacy ? [{ value: "", label: "Not set" }] : []),
    ...(legacy && form
      ? [
          {
            value: LEGACY_MODEL,
            label: (legacyName || "Provider default") + " · " + form.provider,
            group: "Current",
          },
        ]
      : []),
    ...choiceOptions(choices, page ? page.providers : undefined, accounts),
    { value: OTHER_MODEL, label: "Other…" },
  ];
  const modelValue = legacy
    ? LEGACY_MODEL
    : customModel
      ? OTHER_MODEL
      : formModel;
  const thinkingOptions = [
    { value: "", label: "Model default" },
    ...efforts.map((effort) => ({
      value: effort.value,
      label: effortLabel(effort),
    })),
  ];

  const tabBar = (
    <div className="agent-detail-tabbar" ref={tabsRef}>
      <Tabs
        className="agent-detail-tabs"
        aria-label="Agent sections"
        value={activeTab}
        onChange={(value) => activateTab(value, false)}
        onKeyDown={handleTabKeyDown}
        items={tabs.map((item) => ({ value: item.id, label: item.label }))}
      />
    </div>
  );
  const panelProps = {
    className: "agent-tab-panel",
    id: "agent-tabpanel",
    role: "tabpanel",
    "aria-label": activeLabel,
    tabIndex: 0,
  };

  return (
    <main id="main" className="page agent-detail-page">
      {/* REQ-WEB-047: the alive Overview tab shows only its own hero, so the
          legacy header is hidden there. It stays on the other tabs, where the
          Chat, Settings, and Duplicate actions live. */}
      {activeTab !== "overview" ? (
        <header className="ph agent-detail-header">
          <div className="agent-detail-identity">
            <span className="avatar-edit-wrap">
              <AgentAvatar
                size="lg"
                name={agent.name}
                color={gr8rColor(agent.color)}
                presence={
                  presence
                    ? presence.state === "online" || presence.state === "working"
                    : false
                }
              />
              <GenerateAvatarButton
                compact
                name={agent.name}
                color={agent.color}
                svg={agent.avatar_svg}
                source={agent.avatar_source}
                onSaved={avatarSaved}
              />
            </span>
            <div className="agent-detail-heading">
              <div className="agent-detail-name-row">
                <h1 className="agent-detail-name">{displayLabel(agent)}</h1>
                <Badge>{agent.title || "Agent"}</Badge>
                {usesClaudeCode(agent) ? <ClaudeCodeBadge /> : null}
                {agent.default ? (
                  <Badge tone="accent" dot>
                    Default
                  </Badge>
                ) : null}
                {presence ? (
                  <Badge tone={presenceTone(presence.state)} dot>
                    {presence.label}
                  </Badge>
                ) : null}
              </div>
              <p className="agent-detail-description">
                {agent.description || "No description yet."}
              </p>
              <p className="agent-detail-meta">{sessionSummary(agent)}.</p>
            </div>
          </div>
          <div className="acts">
            <Button
              as="a"
              variant="primary"
              href={"#/chats/" + encodeURIComponent(agent.name)}
            >
              {"Chat with " + agent.name}
            </Button>
            <Button
              as="a"
              variant="secondary"
              href={agentHref(agent.name, "general")}
            >
              Settings
            </Button>
            <Button
              variant="secondary"
              disabled={startBusy}
              onClick={startSession}
            >
              {startBusy ? "Starting…" : "Start session"}
            </Button>
            <Button variant="ghost" onClick={() => setDuplicateOpen(true)}>
              Duplicate
            </Button>
          </div>
        </header>
      ) : null}

      {headerError ? (
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not start a session"
            text={headerError}
          />
        </div>
      ) : null}

      {/* On Overview the hero takes the header's place above the tabs, so the
          overview renders the tab bar and the tab panel itself. */}
      {activeTab === "overview" ? (
        <AgentOverview
          name={agentName}
          agent={agent}
          tabs={tabBar}
          panelProps={panelProps}
        />
      ) : (
        tabBar
      )}

      {activeTab !== "overview" ? (
      <section {...panelProps}>
        {activeTab === "general" && form ? (
          <form className="agent-general-form" onSubmit={save} noValidate>
            <section
              className="sblock"
              aria-labelledby="agent-profile-title"
            >
              <h2 id="agent-profile-title">Profile</h2>
              <div className="srow">
                <div className="agent-srow-text">
                  <label className="t" htmlFor="agent-display-name">
                    Display name
                  </label>
                  <div className="d">
                    The name people see. The ID {agent.name} stays the same.
                  </div>
                </div>
                <div className="agent-ctl">
                  <Input
                    id="agent-display-name"
                    type="text"
                    maxLength={MAX_DISPLAY_NAME}
                    value={form.display_name}
                    placeholder={agent.name}
                    autoComplete="off"
                    disabled={saving}
                    invalid={Boolean(displayNameIssue)}
                    onChange={(event) =>
                      updateField("display_name", event.target.value)
                    }
                  />
                  <span className="agent-counter">
                    {Array.from(form.display_name).length +
                      " / " +
                      MAX_DISPLAY_NAME}
                  </span>
                  {displayNameIssue ? (
                    <span className="err" role="alert">
                      <Ic name="circle-alert" size={12} />
                      {displayNameIssue}
                    </span>
                  ) : null}
                </div>
              </div>

              <div className="srow">
                <div className="agent-srow-text">
                  <label className="t" htmlFor="agent-title">
                    Title
                  </label>
                  <div className="d">
                    The title the room list and the profile show.
                  </div>
                </div>
                <div className="agent-ctl">
                  <Input
                    id="agent-title"
                    type="text"
                    maxLength={TITLE_LIMIT}
                    value={form.title}
                    placeholder="e.g. Finance"
                    autoComplete="off"
                    spellCheck={false}
                    disabled={saving}
                    invalid={Boolean(titleIssue)}
                    onChange={(event) =>
                      updateField("title", event.target.value)
                    }
                  />
                  <span className="agent-counter">
                    {Array.from(form.title).length + " / " + TITLE_LIMIT}
                  </span>
                  {titleIssue ? (
                    <span className="err" role="alert">
                      <Ic name="circle-alert" size={12} />
                      {titleIssue}
                    </span>
                  ) : null}
                </div>
              </div>

              <div className="srow">
                <div className="agent-srow-text">
                  <span className="t" id="agent-avatar-label">
                    Avatar
                  </span>
                  <div className="d">
                    A blob character in the agent&rsquo;s color. Every agent starts
                    with one drawn from its name; generate a new one with a model.
                  </div>
                </div>
                <div className="agent-ctl">
                  <div className="agent-avatar-edit" aria-labelledby="agent-avatar-label" role="group">
                    <AgentAvatar
                      name={agent.name}
                      color={gr8rColor(form.color)}
                      size="xl"
                    />
                    <GenerateAvatarButton
                      name={agent.name}
                      color={form.color}
                      svg={agent.avatar_svg}
                      source={agent.avatar_source}
                      disabled={saving}
                      onSaved={avatarSaved}
                    />
                  </div>
                </div>
              </div>

              <div className="srow">
                <div className="agent-srow-text">
                  <span className="t" id="agent-color-label">
                    Color
                  </span>
                  <div className="d">
                    The agent&rsquo;s color in the room list and the profile.
                  </div>
                </div>
                <div className="agent-ctl">
                  <div
                    className="agent-color-picker"
                    role="radiogroup"
                    aria-labelledby="agent-color-label"
                  >
                    {(form.color.startsWith("#")
                      ? [...AGENT_COLORS, { value: form.color, label: "Custom" }]
                      : AGENT_COLORS
                    ).map((entry) => {
                      const selected = form.color === entry.value;
                      return (
                        <label
                          key={entry.value}
                          className={
                            "agent-swatch" + (selected ? " is-on" : "")
                          }
                        >
                          <Radio
                            name="agent-color"
                            value={entry.value}
                            aria-label={entry.value}
                            checked={selected}
                            disabled={saving}
                            onChange={() =>
                              updateField("color", entry.value)
                            }
                          />
                          <span
                            className={
                              "agent-swatch-dot" +
                              (entry.value.startsWith("#")
                                ? ""
                                : " color-" + entry.value)
                            }
                            style={
                              entry.value.startsWith("#")
                                ? ({ "--sw": entry.value } as CSSProperties)
                                : undefined
                            }
                            aria-hidden="true"
                          />
                          <span className="agent-swatch-name">
                            {entry.label}
                          </span>
                        </label>
                      );
                    })}
                  </div>
                  {colorIssue ? (
                    <span className="err" role="alert">
                      <Ic name="circle-alert" size={12} />
                      {colorIssue}
                    </span>
                  ) : null}
                </div>
              </div>
            </section>

            <section className="sblock" aria-labelledby="agent-model-title">
              <h2 id="agent-model-title">Model</h2>
              <div className="srow">
                <div className="agent-srow-text">
                  <label className="t" htmlFor="agent-model-select">
                    Model
                  </label>
                  <div className="d">
                    A combo or a model from the{" "}
                    <a href="#/router/models">router</a>. Fallbacks run when it
                    fails.
                  </div>
                </div>
                <div className="agent-ctl">
                  <Select
                    id="agent-model-select"
                    aria-label="Model"
                    value={modelValue}
                    disabled={saving}
                    options={modelOptions}
                    onChange={pickModel}
                  />
                  {customModel ? (
                    <Input
                      id="agent-model-custom"
                      className="mono"
                      type="text"
                      aria-label="Custom model id"
                      placeholder="combo or provider/model"
                      autoComplete="off"
                      spellCheck={false}
                      value={form.model}
                      disabled={saving}
                      onChange={(event) =>
                        updateField("model", event.target.value)
                      }
                    />
                  ) : null}
                  {isClaudeForm ? <ClaudeCodeNote account={claudeAccount} /> : null}
                  {legacyRuntime ? (
                    <span className="hint">
                      This agent still uses the old engine setting. Saving
                      writes the model as {form.model}.
                    </span>
                  ) : null}
                </div>
              </div>

              {isClaudeForm ? (
                <>
                  <div className="srow">
                    <div className="agent-srow-text">
                      <label className="t" htmlFor="agent-claude-effort">
                        Reasoning effort
                      </label>
                      <div className="d">Passed to Claude Code as --effort.</div>
                    </div>
                    <div className="agent-ctl">
                      <Select
                        id="agent-claude-effort"
                        aria-label="Reasoning effort"
                        value={form.reasoning_effort}
                        disabled={saving}
                        options={CLAUDE_EFFORTS}
                        onChange={(value) => updateField("reasoning_effort", value)}
                      />
                    </div>
                  </div>
                  <div className="srow">
                    <div className="agent-srow-text">
                      <span className="t">Scheduled runs</span>
                      <div className="d">
                        Whether workflows and the calendar may start Claude Code
                        is set per account.
                      </div>
                    </div>
                    <div className="agent-ctl">
                      <a className="agent-cc-link" href={CLAUDE_CODE_HREF}>
                        {"Allow scheduled runs on " + claudeAccount}
                        <Ic name="arrow-up-right" size={12} />
                      </a>
                    </div>
                  </div>
                </>
              ) : null}

              {!isClaudeForm && thinkingModel ? (
                <div className="srow">
                  <div className="agent-srow-text">
                    <label className="t" htmlFor="agent-thinking">
                      Thinking
                    </label>
                    <div className="d">How much the model deliberates.</div>
                  </div>
                  <div className="agent-ctl">
                    <Select
                      id="agent-thinking"
                      aria-label="Thinking"
                      value={form.reasoning_effort}
                      disabled={saving}
                      options={thinkingOptions}
                      onChange={(value) =>
                        updateField("reasoning_effort", value)
                      }
                    />
                  </div>
                </div>
              ) : null}

              {legacyReplacement ? (
                <div className="srow">
                  <div className="agent-srow-text">
                    <div className="t">Legacy model</div>
                    <div className="d">This model is superseded.</div>
                  </div>
                  <div className="agent-ctl">
                    <div className="agent-legacy-banner" role="status">
                      <Ic name="triangle-alert" size={15} />
                      <span className="grow">
                        {"Legacy model — switch to " + legacyReplacement}
                      </span>
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={saving}
                        onClick={() => {
                          setOtherModel(false);
                          updateField(
                            "model",
                            legacy
                              ? legacyReplacement
                              : provider + "/" + legacyReplacement
                          );
                        }}
                      >
                        {"Switch to " + legacyReplacement}
                      </Button>
                    </div>
                  </div>
                </div>
              ) : null}

              <div className="srow">
                <div className="agent-srow-text">
                  <span className="t" id="agent-fallback-label">
                    Fallback
                  </span>
                  <div className="d">
                    Targets to try when the main model fails.
                  </div>
                </div>
                <div className="agent-ctl agent-ctl-wide">
                  <div
                    className="agent-fallbacks"
                    role="group"
                    aria-labelledby="agent-fallback-label"
                  >
                    {form.fallback.length === 0 ? (
                      <span className="hint">No fallbacks.</span>
                    ) : null}
                    {form.fallback.map((target, index) => {
                      const parts = splitTarget(target);
                      return (
                        <Chip
                          key={target + ":" + index}
                          removeLabel={"Remove fallback " + target}
                          onRemove={
                            saving ? undefined : () => removeFallback(index)
                          }
                        >
                          {(parts.provider ? parts.provider + " / " : "") +
                            parts.model}
                        </Chip>
                      );
                    })}
                    <Button
                      variant="ghost"
                      size="sm"
                      icon="plus"
                      disabled={saving}
                      onClick={startFallback}
                    >
                      Add fallback
                    </Button>
                  </div>

                  {fallbackDraft ? (
                    <div className="agent-fallback-draft">
                      <div className="field">
                        <label
                          className="label"
                          htmlFor="agent-fallback-provider"
                        >
                          Fallback provider
                        </label>
                        <Input
                          id="agent-fallback-provider"
                          type="text"
                          placeholder="provider"
                          autoComplete="off"
                          spellCheck={false}
                          value={fallbackDraft.provider}
                          disabled={saving}
                          onChange={(event) =>
                            updateFallbackDraft("provider", event.target.value)
                          }
                        />
                      </div>
                      <div className="field">
                        <label className="label" htmlFor="agent-fallback-model">
                          Fallback model
                        </label>
                        <Input
                          id="agent-fallback-model"
                          type="text"
                          placeholder="model"
                          autoComplete="off"
                          spellCheck={false}
                          value={fallbackDraft.model}
                          disabled={saving}
                          onChange={(event) =>
                            updateFallbackDraft("model", event.target.value)
                          }
                        />
                      </div>
                      <div className="agent-fallback-draft-actions">
                        <Button
                          variant="secondary"
                          size="sm"
                          disabled={saving}
                          onClick={() => setFallbackDraft(null)}
                        >
                          Cancel
                        </Button>
                        <Button
                          variant="primary"
                          size="sm"
                          disabled={
                            saving ||
                            !fallbackDraft.provider.trim() ||
                            !fallbackDraft.model.trim()
                          }
                          onClick={addFallback}
                        >
                          Add
                        </Button>
                      </div>
                    </div>
                  ) : null}
                </div>
              </div>
            </section>

            <section
              className="sblock"
              aria-labelledby="agent-permission-title"
            >
              <h2 id="agent-permission-title">Permission</h2>
              <div className="srow">
                <div className="agent-srow-text">
                  <span className="t" id="agent-permission-label">
                    Risky tools
                  </span>
                  <div className="d">
                    What happens when the agent wants to run a risky tool, such
                    as bash or write_file.
                  </div>
                </div>
                <div className="agent-ctl agent-ctl-wide">
                  <div
                    className="agent-permission-tiles"
                    role="radiogroup"
                    aria-labelledby="agent-permission-label"
                  >
                    {PERMISSIONS.map((permission) => {
                      const selected = form.permission === permission.value;
                      return (
                        <label
                          key={permission.value}
                          className={
                            "agent-permission-tile" +
                            (selected ? " is-selected" : "")
                          }
                        >
                          <Radio
                            name="agent-permission"
                            value={permission.value}
                            checked={selected}
                            disabled={saving}
                            onChange={() =>
                              updateField("permission", permission.value)
                            }
                          />
                          <span className="agent-permission-text">
                            <span className="agent-permission-title">
                              {permission.title}
                            </span>
                            <span className="agent-permission-note">
                              {permission.note}
                            </span>
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </div>
              </div>
            </section>

            <section className="sblock" aria-labelledby="agent-limits-title">
              <h2 id="agent-limits-title">Limits</h2>
              <div className="srow">
                <div className="agent-srow-text">
                  <label className="t" htmlFor="agent-max-steps">
                    Max steps per turn
                  </label>
                  <div className="d">Leave empty for no limit.</div>
                </div>
                <div className="agent-ctl">
                  <Input
                    id="agent-max-steps"
                    className="agent-number-input"
                    type="number"
                    min="0"
                    step="1"
                    inputMode="numeric"
                    value={form.max_steps}
                    placeholder="No limit"
                    disabled={saving}
                    onChange={(event) =>
                      updateField("max_steps", event.target.value)
                    }
                  />
                </div>
              </div>
              <div className="srow">
                <div className="agent-srow-text">
                  <label className="t" htmlFor="agent-context-window">
                    Context window
                  </label>
                  <div className="d">Tokens kept in one turn.</div>
                </div>
                <div className="agent-ctl">
                  <Input
                    id="agent-context-window"
                    className="agent-number-input"
                    type="number"
                    min="1"
                    step="1"
                    inputMode="numeric"
                    value={form.context_window}
                    placeholder="Provider default"
                    disabled={saving}
                    onChange={(event) =>
                      updateField("context_window", event.target.value)
                    }
                  />
                </div>
              </div>
            </section>

            <section className="sblock" aria-labelledby="agent-files-title">
              <h2 id="agent-files-title">Files</h2>
              <div className="srow">
                <div className="agent-srow-text">
                  <span className="t" id="agent-workspace-only-label">
                    Stay in the workspace
                  </span>
                  <div className="d">
                    When on, file tools only read and edit files inside the
                    agent's workspace. When off, they can reach any path.
                  </div>
                </div>
                <div className="agent-ctl">
                  <Toggle
                    aria-labelledby="agent-workspace-only-label"
                    checked={form.workspace_only}
                    disabled={saving}
                    onChange={(checked) =>
                      updateField("workspace_only", checked)
                    }
                  />
                </div>
              </div>
            </section>

            {page && page.tools.includes("memory_write") ? (
              <section
                className="sblock"
                aria-labelledby="agent-memory-title"
              >
                <h2 id="agent-memory-title">Memory</h2>
                <div className="srow">
                  <div className="agent-srow-text">
                    <span className="t" id="agent-memory-label">
                      Remember automatically
                    </span>
                    <div className="d">
                      After each turn, lana keeps preferences and corrections it
                      finds in the conversation.
                    </div>
                  </div>
                  <div className="agent-ctl">
                    <Toggle
                      aria-labelledby="agent-memory-label"
                      checked={form.memory_capture}
                      disabled={saving}
                      onChange={(checked) =>
                        updateField("memory_capture", checked)
                      }
                    />
                  </div>
                </div>
              </section>
            ) : null}

            {saveNotice ? (
              <p className="notice notice-ok agent-save-notice" role="status">
                {saveNotice}
              </p>
            ) : null}
            {restartNotice ? (
              <p
                className="notice notice-warn agent-save-notice"
                role="status"
              >
                Some changes need a daemon restart.{" "}
                <a href="#/settings/general">
                  Restart the daemon in Settings › General
                </a>
                .
              </p>
            ) : null}
            {error ? (
              <div className="panel agent-save-error" role="alert">
                <ErrorState
                  icon="cloud-alert"
                  title="Could not save"
                  text={error}
                  action={
                    stale ? (
                      <Button variant="secondary" onClick={reload}>
                        Reload settings
                      </Button>
                    ) : null
                  }
                >
                  <DetailsList details={details} />
                </ErrorState>
              </div>
            ) : null}

            <div className="agent-save-bar">
              <p className="agent-save-note">
                Changes apply from the next turn.
              </p>
              <div className="agent-save-actions">
                <Button
                  variant="secondary"
                  disabled={!dirty.length || saving}
                  onClick={resetForm}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  loading={saving}
                  disabled={!canSave}
                >
                  {saving ? "Saving…" : "Save changes"}
                </Button>
              </div>
            </div>
          </form>
        ) : activeTab === "persona" ? (
          <PersonaTab name={agentName} />
        ) : activeTab === "tools" ? (
          <ToolsTab name={agentName} />
        ) : activeTab === "skills" ? (
          <SkillsTab name={agentName} />
        ) : activeTab === "mcp" ? (
          <McpTab name={agentName} />
        ) : activeTab === "rules" ? (
          <RulesTab name={agentName} />
        ) : activeTab === "workspace" ? (
          <WorkspaceTab name={agentName} />
        ) : null}
      </section>
      ) : null}

      {duplicateOpen ? (
        <DuplicateAgentDialog
          source={agent.name}
          agents={knownAgents}
          onClose={() => setDuplicateOpen(false)}
          onDuplicated={(createdName) => {
            window.location.hash = agentHref(createdName);
          }}
        />
      ) : null}
    </main>
  );
}

/* eslint-disable react-hooks/set-state-in-effect -- the detail load resets its state before each request, as in agent-detail.js */
import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { duplicateAgent, listAgents, updateAgent } from "../api/agents";
import type { Agent, AgentWriteFields } from "../api/agents";
import { getModels } from "../api/models";
import type { CatalogEffort, CatalogModel } from "../api/models";
import { getConfig, listProviders } from "../api/settings";
import type { ConfigAgent } from "../api/settings";
import { createSession } from "../api/sessions";
import { Dialog, detailText, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import {
  isTeam,
  rosterNames,
  splitModel,
  splitTarget,
  normalizePermission,
  providerLabel,
  agentHref,
  sessionSummary,
  presenceTone,
} from "./agent-common";
import type { AgentLike, Permission } from "./agent-common";
import { AgentOverview } from "./agent-overview";
import { gr8rColor, useRooms } from "./presence";
import { DEFAULT_AGENT_TAB } from "../app/router";
import { PersonaTab } from "./agent-tabs/persona";
import { ToolsTab } from "./agent-tabs/tools";
import { SkillsTab } from "./agent-tabs/skills";
import { McpTab } from "./agent-tabs/mcp";
import { RulesTab } from "./agent-tabs/rules";
import { WorkspaceTab } from "./agent-tabs/workspace";
import {
  Avatar,
  AvatarStack,
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

interface AgentValues {
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
  providers: string[];
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

/* REQ-ROOM-009: an agent color is one of the six names; anything else is brand. */
function normalizeColor(value: unknown): string {
  const name = String(value || "").toLowerCase();
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

function initialValues(agent: Agent, configAgent: ConfigAgent): AgentValues {
  const record = configAgent || {};
  const parts = splitModel(agent);
  return {
    title: pickText(record.title, agent.title),
    color: normalizeColor(pickText(record.color, agent.color)),
    provider: pickText(record.provider, agent.provider, parts.provider),
    model: pickText(record.model, parts.model),
    reasoning_effort: pickText(agent.reasoning_effort),
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
    if (key === "title") {
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
  const [name, setName] = useState(() => nextDuplicateName(source, agents));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!/^[a-z0-9-]+$/.test(value)) {
      setError("The name must use lowercase letters, digits, and dashes.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const created = await duplicateAgent(source, value);
      const createdName = created && created.name ? created.name : value;
      announce("Agent " + createdName + " created from " + source + ".");
      if (onDuplicated) {
        onDuplicated(createdName);
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        setError(
          "An agent named " + value + " already exists. Pick another name."
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
          Copy <strong>{source}</strong> to a new agent. The config table and
          AGENT.md are copied; MEMORY.md is not.
        </p>
        <div className="field">
          <label className="label" htmlFor="duplicate-agent-name">
            New name
          </label>
          <Input
            id="duplicate-agent-name"
            data-autofocus
            type="text"
            value={name}
            autoComplete="off"
            spellCheck={false}
            pattern="[a-z0-9-]+"
            required
            invalid={Boolean(error)}
            onChange={(event) => setName(event.target.value)}
          />
          <span className="hint">Lowercase letters, digits, and dashes.</span>
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

    Promise.all([
      listAgents(),
      getConfig().catch(() => null),
      listProviders().catch(() => null),
    ])
      .then(([agentPage, config, providerPage]) => {
        if (!alive) {
          return;
        }
        const rows =
          agentPage && Array.isArray(agentPage.agents) ? agentPage.agents : [];
        const agent = rows.find((row) => row.name === agentName) || null;
        const settings = config && config.settings ? config.settings : null;
        const configAgent: ConfigAgent = settings?.agents?.[agentName] ?? {};
        const providers = (providerPage?.providers ?? [])
          .map((provider) => provider && provider.name)
          .filter((value): value is string => Boolean(value))
          .sort();
        const initial = agent ? initialValues(agent, configAgent) : null;
        const tools = Array.isArray(configAgent.tools)
          ? configAgent.tools.map((tool) => String(tool))
          : [];
        setPage({ agent, agents: rows, providers, tools });
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

  const activeTab: TabId =
    TABS.find((item) => item.id === tab)?.id ?? DEFAULT_AGENT_TAB;

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

  const dirty = useMemo(() => dirtyKeys(form, baseline), [form, baseline]);

  const providerOptions = useMemo(() => {
    const names = new Set<string>(page && page.providers ? page.providers : []);
    if (baseline && baseline.provider) {
      names.add(baseline.provider);
    }
    return Array.from(names).sort();
  }, [page, baseline]);

  const provider = form && form.provider ? form.provider : "";

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
  const selectedCatalogModel = form
    ? catalog.find((model) => model.id === form.model) || null
    : null;
  const customModel = Boolean(
    otherModel || (form && form.model !== "" && !selectedCatalogModel)
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
    const next = TABS.find((item) => item.id === id);
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
    const target = TABS[next];
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
            <AvatarStack
              size="lg"
              max={3}
              items={rosterNames(agent).map((member) => ({
                name: member,
                color: gr8rColor(member),
              }))}
            />
            <div className="agent-detail-heading">
              <div className="agent-detail-name-row">
                <h1 className="agent-detail-name">{agent.name}</h1>
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
  const colorIssue = issueMessage(details, "agents." + agent.name + ".color");
  const row = byName[agent.name];
  const presence = row ? row.presence : null;
  const activeLabel =
    TABS.find((item) => item.id === activeTab)?.label || "Agent";

  const modelOptions = [
    { value: "", label: "Provider default" },
    ...catalog.map((model) => ({ value: model.id, label: model.name })),
    { value: "__other__", label: "Other…" },
  ];
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
        items={TABS.map((item) => ({ value: item.id, label: item.label }))}
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
            <Avatar
              size="lg"
              name={agent.name}
              color={gr8rColor(agent.color)}
              presence={
                presence
                  ? presence.state === "online" || presence.state === "working"
                  : false
              }
            />
            <div className="agent-detail-heading">
              <div className="agent-detail-name-row">
                <h1 className="agent-detail-name">{agent.name}</h1>
                <Badge>{agent.title || "Agent"}</Badge>
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
                    {AGENT_COLORS.map((entry) => {
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
                              "agent-swatch-dot color-" + entry.value
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
                  <label className="t" htmlFor="agent-provider">
                    Provider
                  </label>
                  <div className="d">Where the model is served from.</div>
                </div>
                <div className="agent-ctl">
                  <Select
                    id="agent-provider"
                    aria-label="Provider"
                    value={form.provider}
                    disabled={saving}
                    options={[
                      { value: "", label: "Not set" },
                      ...providerOptions.map((name) => ({
                        value: name,
                        label: providerLabel(name),
                      })),
                    ]}
                    onChange={(value) => {
                      setOtherModel(false);
                      updateField("provider", value);
                    }}
                  />
                </div>
              </div>

              <div className="srow">
                <div className="agent-srow-text">
                  <label className="t" htmlFor="agent-model-select">
                    Model
                  </label>
                  <div className="d">
                    The model this agent uses. Fallbacks run when it fails.
                  </div>
                </div>
                <div className="agent-ctl">
                  <Select
                    id="agent-model-select"
                    aria-label="Model"
                    value={customModel ? "__other__" : form.model}
                    disabled={saving}
                    options={modelOptions}
                    onChange={(value) => {
                      if (value === "__other__") {
                        setOtherModel(true);
                        return;
                      }
                      setOtherModel(false);
                      updateField("model", value);
                    }}
                  />
                  {customModel ? (
                    <Input
                      id="agent-model-custom"
                      className="mono"
                      type="text"
                      aria-label="Custom model id"
                      placeholder="model id"
                      autoComplete="off"
                      spellCheck={false}
                      value={form.model}
                      disabled={saving}
                      onChange={(event) =>
                        updateField("model", event.target.value)
                      }
                    />
                  ) : null}
                </div>
              </div>

              {thinkingModel ? (
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
                          updateField("model", legacyReplacement);
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

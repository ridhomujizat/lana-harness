import type { SetupModel } from "../api/setup";
import { CLAUDE_CODE_ID, PROVIDER_PRESETS, presetById } from "./router/presets";
import type { ProviderPreset } from "./router/presets";

/* The setup wizard draft (REQ-WEB-033, Router onboarding). sessionStorage
   holds it across the step routes; a module-level copy keeps the wizard
   working when storage is disabled. Both are cleared after a successful
   save. */

const DRAFT_KEY = "lana.setup";
const SUMMARY_KEY = "lana.setup.summary";

/* The API key cards shown first; the OpenAI-compatible presets sit under
   "More providers". */
export const MAIN_PRESET_IDS = ["openai", "anthropic", "deepseek", "openrouter"];

export const MAIN_PRESETS: ProviderPreset[] = MAIN_PRESET_IDS.map(
  (id) => presetById(id) as ProviderPreset
);

export const MORE_PRESETS: ProviderPreset[] = PROVIDER_PRESETS.filter(
  (preset) => !MAIN_PRESET_IDS.includes(preset.id)
);

/* The model a new agent starts on with each preset: a capable default for
   the native kinds, the preset's test model for the compatible ones. */
const DEFAULT_MODELS: Record<string, string> = {
  openai: "gpt-5-mini",
  anthropic: "claude-sonnet-5-5",
  deepseek: "deepseek-flash",
  openrouter: "openai/gpt-5-mini",
};

/* The picker models when the daemon sends no catalog (an older daemon or a
   failed GET /v1/setup). OpenRouter lists no built-in models; a few popular
   ones are offered next to the daemon's. */
const FALLBACK_CATALOG: Record<string, SetupModel[]> = {
  openai: [
    { id: "gpt-5-mini", name: "GPT-5 mini" },
    { id: "gpt-5.1", name: "GPT-5.1" },
    { id: "gpt-5", name: "GPT-5" },
  ],
  anthropic: [
    { id: "claude-sonnet-5-5", name: "Claude Sonnet 5.5" },
    { id: "claude-opus-5-5", name: "Claude Opus 5.5" },
    { id: "claude-haiku-5-5", name: "Claude Haiku 5.5" },
  ],
  deepseek: [
    { id: "deepseek-flash", name: "DeepSeek Flash" },
    { id: "deepseek-v4-pro", name: "DeepSeek V4 Pro" },
  ],
  openrouter: [{ id: "openai/gpt-5-mini", name: "GPT-5 mini" }],
};

const OPENROUTER_EXTRA: SetupModel[] = [
  { id: "anthropic/claude-sonnet-4.5", name: "Claude Sonnet 4.5" },
  { id: "google/gemini-2.5-pro", name: "Gemini 2.5 Pro" },
];

export const CLAUDE_CODE_MODELS: SetupModel[] = [
  { id: "opus", name: "Opus" },
  { id: "sonnet", name: "Sonnet" },
  { id: "haiku", name: "Haiku" },
];

export const CLAUDE_CODE_DEFAULT = CLAUDE_CODE_ID + "/sonnet";

/* The picker models of one preset: the daemon catalog of its kind (the
   fallback list without one), or the preset's test model for an
   OpenAI-compatible preset. */
export function presetModels(
  preset: ProviderPreset,
  catalog: Record<string, SetupModel[]> | null | undefined
): SetupModel[] {
  if (preset.kind === "openai_compat") {
    return [{ id: preset.testModel, name: preset.testModel }];
  }
  const listed = catalog ? catalog[preset.kind] : undefined;
  const known: SetupModel[] = Array.isArray(listed) ? listed : [];
  const list = known.length > 0 ? known.slice() : (FALLBACK_CATALOG[preset.kind] || []).slice();
  if (preset.kind === "openrouter") {
    for (const extra of OPENROUTER_EXTRA) {
      if (!list.some((model) => model.id === extra.id)) {
        list.push(extra);
      }
    }
  }
  return list;
}

export function defaultModelFor(preset: ProviderPreset): string {
  return DEFAULT_MODELS[preset.id] || preset.testModel;
}

/* One connected provider: a preset whose key passed the test. */
export interface SetupConnection {
  /* The preset id; also the [providers.<name>] name. */
  id: string;
  apiKey: string;
  /* Empty for the preset's own URL. */
  baseUrl: string;
  latencyMs: number;
}

export interface SetupClaudeAccount {
  email: string;
  plan: string;
}

export interface SetupAgentDraft {
  name: string;
  template: string;
  workspace: string;
  permission: string;
}

export interface SetupPersonaDraft {
  agentMd: string;
  userMd: string;
}

export interface SetupDraft {
  connections: SetupConnection[];
  /* "Use Claude Code" is on: the built-in account is signed in. */
  claudeCode: boolean;
  claudeAccount: SetupClaudeAccount | null;
  /* provider/model addresses; fallback and background may be empty. */
  model: string;
  fallback: string;
  background: string;
  agent: SetupAgentDraft;
  persona: SetupPersonaDraft;
}

export interface SetupSummary {
  providers: string[];
  claudeCode: boolean;
  claudeAccount: SetupClaudeAccount | null;
  model: string;
  fallback: string;
  background: string;
  agentName: string;
  template: string;
  permission: string;
  workspace: string;
}

let memoryDraft: SetupDraft | null = null;
let memorySummary: SetupSummary | null = null;

function textValue(value: unknown, fallback: string): string {
  return typeof value === "string" ? value : fallback;
}

function objectValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

export function defaultDraft(): SetupDraft {
  return {
    connections: [],
    claudeCode: false,
    claudeAccount: null,
    model: "",
    fallback: "",
    background: "",
    agent: {
      name: "default",
      template: "assistant",
      workspace: "~/lana/workspace",
      permission: "ask",
    },
    persona: { agentMd: "", userMd: "" },
  };
}

function normalizeConnections(value: unknown): SetupConnection[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const out: SetupConnection[] = [];
  for (const raw of value) {
    const entry = objectValue(raw);
    const id = textValue(entry.id, "");
    if (!presetById(id) || out.some((connection) => connection.id === id)) {
      continue;
    }
    out.push({
      id,
      apiKey: textValue(entry.apiKey, ""),
      baseUrl: textValue(entry.baseUrl, ""),
      latencyMs: Number(entry.latencyMs) || 0,
    });
  }
  return out.filter((connection) => connection.apiKey);
}

/* A draft of the rev 2 wizard (one provider, untested key) starts over: its
   key never passed the test of this flow. */
export function normalizeDraft(value: unknown): SetupDraft {
  const fallback = defaultDraft();
  const draft = objectValue(value);
  const agent = objectValue(draft.agent);
  const persona = objectValue(draft.persona);
  const account = objectValue(draft.claudeAccount);
  const claudeCode = draft.claudeCode === true;
  return {
    connections: normalizeConnections(draft.connections),
    claudeCode,
    claudeAccount: claudeCode
      ? { email: textValue(account.email, ""), plan: textValue(account.plan, "") }
      : null,
    model: textValue(draft.model, ""),
    fallback: textValue(draft.fallback, ""),
    background: textValue(draft.background, ""),
    agent: {
      name: textValue(agent.name, fallback.agent.name),
      template: textValue(agent.template, fallback.agent.template),
      workspace: textValue(agent.workspace, fallback.agent.workspace),
      permission: textValue(agent.permission, fallback.agent.permission),
    },
    persona: {
      agentMd: textValue(persona.agentMd, ""),
      userMd: textValue(persona.userMd, ""),
    },
  };
}

export function readDraft(): SetupDraft {
  if (memoryDraft) {
    return memoryDraft;
  }
  let parsed: unknown = null;
  try {
    const raw = window.sessionStorage.getItem(DRAFT_KEY);
    if (raw) {
      parsed = JSON.parse(raw);
    }
  } catch {
    /* Storage can be disabled; the in-memory copy still carries this page. */
  }
  memoryDraft = normalizeDraft(parsed);
  return memoryDraft;
}

export function writeDraft(next: SetupDraft): SetupDraft {
  memoryDraft = normalizeDraft(next);
  try {
    window.sessionStorage.setItem(DRAFT_KEY, JSON.stringify(memoryDraft));
  } catch {
    /* Keep the in-memory copy when storage is unavailable. */
  }
  return memoryDraft;
}

/* Merges a change into the stored draft and returns the result. */
export function updateDraft(change: Partial<SetupDraft>): SetupDraft {
  return writeDraft({ ...readDraft(), ...change });
}

export function clearDraft(): void {
  memoryDraft = null;
  try {
    window.sessionStorage.removeItem(DRAFT_KEY);
  } catch {
    /* Nothing else to clear. */
  }
}

/* Forgets the in-memory copies so the next read comes from storage. Tests
   use it between renders. */
export function resetSetupMemory(): void {
  memoryDraft = null;
  memorySummary = null;
}

export function readSummary(): SetupSummary | null {
  if (memorySummary) {
    return memorySummary;
  }
  try {
    const raw = window.sessionStorage.getItem(SUMMARY_KEY);
    if (raw) {
      const parsed = objectValue(JSON.parse(raw));
      if (Array.isArray(parsed.providers)) {
        memorySummary = parsed as unknown as SetupSummary;
        return memorySummary;
      }
    }
  } catch {
    /* Fall through to the draft. */
  }
  return null;
}

export function writeSummary(summary: SetupSummary): void {
  memorySummary = summary;
  try {
    window.sessionStorage.setItem(SUMMARY_KEY, JSON.stringify(summary));
  } catch {
    /* The current page still has the summary in memory. */
  }
}

export function summaryFor(draft: SetupDraft): SetupSummary {
  return {
    providers: draft.connections.map((connection) => connection.id),
    claudeCode: draft.claudeCode,
    claudeAccount: draft.claudeCode ? draft.claudeAccount : null,
    model: draft.model,
    fallback: draft.fallback,
    background: draft.background,
    agentName: draft.agent.name.trim(),
    template: draft.agent.template,
    permission: draft.agent.permission,
    workspace: draft.agent.workspace.trim() || "~/lana/workspace",
  };
}

/* ------------------------------------------------------------------ */
/* Addresses                                                           */
/* ------------------------------------------------------------------ */

/* The provider part of "provider/model". */
export function addressProvider(address: string): string {
  const slash = String(address || "").indexOf("/");
  return slash > 0 ? address.slice(0, slash) : "";
}

/* The model part of "provider/model". */
export function addressModel(address: string): string {
  const provider = addressProvider(address);
  return provider ? address.slice(provider.length + 1) : "";
}

export function isClaudeAddress(address: string): boolean {
  return addressProvider(address) === CLAUDE_CODE_ID;
}

/* A complete address: a provider and a model ID on one line. */
export function validAddress(address: string): boolean {
  const model = addressModel(address);
  return Boolean(model.trim()) && model.trim() === model && !/[\r\n]/.test(model);
}

/* The providers the draft can pick models from: the connected presets, and
   Claude Code when it is in use. */
export function availableProviders(draft: SetupDraft): string[] {
  const out = draft.connections.map((connection) => connection.id);
  if (draft.claudeCode) {
    out.unshift(CLAUDE_CODE_ID);
  }
  return out;
}

/* The background model the wizard suggests: the fallback when it is an API
   model, else the first connected provider's cheapest known model. */
export function suggestedBackground(draft: SetupDraft): string {
  if (draft.fallback && !isClaudeAddress(draft.fallback) && validAddress(draft.fallback)) {
    return draft.fallback;
  }
  const first = draft.connections[0];
  const preset = first ? presetById(first.id) : undefined;
  return preset ? preset.id + "/" + preset.testModel : "";
}

/* The model picks of the draft that still fit what is connected: a pick on
   a provider that is gone is reset, the main model gets a default, and a
   Claude Code main model gets the suggested background model. */
export function reconcileModels(draft: SetupDraft): Pick<SetupDraft, "model" | "fallback" | "background"> {
  const available = availableProviders(draft);
  const fits = (address: string) => Boolean(address) && available.includes(addressProvider(address));
  let model = fits(draft.model) ? draft.model : "";
  if (!model) {
    if (draft.claudeCode) {
      model = CLAUDE_CODE_DEFAULT;
    } else if (draft.connections[0]) {
      const preset = presetById(draft.connections[0].id);
      model = preset ? preset.id + "/" + defaultModelFor(preset) : "";
    }
  }
  let fallback = fits(draft.fallback) ? draft.fallback : "";
  if (fallback && addressProvider(fallback) === addressProvider(model)) {
    fallback = "";
  }
  let background = "";
  if (isClaudeAddress(model)) {
    background =
      fits(draft.background) && !isClaudeAddress(draft.background)
        ? draft.background
        : suggestedBackground({ ...draft, fallback });
  }
  return { model, fallback, background };
}

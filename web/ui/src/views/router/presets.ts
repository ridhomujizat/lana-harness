/* The provider presets of the Router Providers page (spec 37, Router
   Providers revision 2). A preset is a card that is always shown; each
   [providers.<name>] entry is one connection of the card it matches:

   - a native kind (openai, anthropic, deepseek, openrouter) matches by kind;
   - an openai_compat entry matches the preset whose base URL host it uses
     (Gemini, Grok, Groq, …, and the native presets' own hosts);
   - any other openai_compat entry is a custom "compatible" provider. */

export type PresetSection = "api" | "compat";

export interface ProviderPreset {
  /* The card id, the route segment, and the default connection name. */
  id: string;
  label: string;
  /* The kind a new connection is saved with. */
  kind: string;
  /* The kind's default base URL (native) or the preset's base URL (compat). */
  baseUrl: string;
  keyEnv: string;
  /* A key of the logo registry in provider-logo.tsx. */
  logo: string;
  /* Where the owner creates an API key. */
  keysUrl: string;
  /* The model Test All uses when no model list is known. */
  testModel: string;
  /* One line under the name on the detail page. */
  blurb: string;
}

export const NATIVE_KINDS = ["openai", "anthropic", "deepseek", "openrouter"];

export const PROVIDER_PRESETS: ProviderPreset[] = [
  {
    id: "openai",
    label: "OpenAI",
    kind: "openai",
    baseUrl: "https://api.openai.com/v1",
    keyEnv: "OPENAI_API_KEY",
    logo: "openai",
    keysUrl: "https://platform.openai.com/api-keys",
    testModel: "gpt-5-mini",
    blurb: "GPT models through the OpenAI API.",
  },
  {
    id: "anthropic",
    label: "Anthropic",
    kind: "anthropic",
    baseUrl: "https://api.anthropic.com",
    keyEnv: "ANTHROPIC_API_KEY",
    logo: "anthropic",
    keysUrl: "https://console.anthropic.com/settings/keys",
    testModel: "claude-haiku-5-5",
    blurb: "Claude models through the Anthropic API, billed per token.",
  },
  {
    id: "deepseek",
    label: "DeepSeek",
    kind: "deepseek",
    baseUrl: "https://api.deepseek.com/v1",
    keyEnv: "DEEPSEEK_API_KEY",
    logo: "deepseek",
    keysUrl: "https://platform.deepseek.com/api_keys",
    testModel: "deepseek-flash",
    blurb: "DeepSeek chat and reasoning models.",
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    kind: "openrouter",
    baseUrl: "https://openrouter.ai/api/v1",
    keyEnv: "OPENROUTER_API_KEY",
    logo: "openrouter",
    keysUrl: "https://openrouter.ai/settings/keys",
    testModel: "openai/gpt-5-mini",
    blurb: "Hundreds of models from many labs behind one key.",
  },
  {
    id: "gemini",
    label: "Google Gemini",
    kind: "openai_compat",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai",
    keyEnv: "GEMINI_API_KEY",
    logo: "gemini",
    keysUrl: "https://aistudio.google.com/apikey",
    testModel: "gemini-2.5-flash",
    blurb: "Gemini models through Google's OpenAI-compatible endpoint.",
  },
  {
    id: "xai",
    label: "xAI Grok",
    kind: "openai_compat",
    baseUrl: "https://api.x.ai/v1",
    keyEnv: "XAI_API_KEY",
    logo: "grok",
    keysUrl: "https://console.x.ai",
    testModel: "grok-3-mini",
    blurb: "Grok models from xAI.",
  },
  {
    id: "groq",
    label: "Groq",
    kind: "openai_compat",
    baseUrl: "https://api.groq.com/openai/v1",
    keyEnv: "GROQ_API_KEY",
    logo: "groq",
    keysUrl: "https://console.groq.com/keys",
    testModel: "llama-3.1-8b-instant",
    blurb: "Open models on Groq's fast inference chips.",
  },
  {
    id: "mistral",
    label: "Mistral",
    kind: "openai_compat",
    baseUrl: "https://api.mistral.ai/v1",
    keyEnv: "MISTRAL_API_KEY",
    logo: "mistral",
    keysUrl: "https://console.mistral.ai/api-keys",
    testModel: "mistral-small-latest",
    blurb: "Mistral and Codestral models.",
  },
  {
    id: "together",
    label: "Together AI",
    kind: "openai_compat",
    baseUrl: "https://api.together.xyz/v1",
    keyEnv: "TOGETHER_API_KEY",
    logo: "together",
    keysUrl: "https://api.together.ai/settings/api-keys",
    testModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
    blurb: "Open models hosted by Together AI.",
  },
  {
    id: "fireworks",
    label: "Fireworks AI",
    kind: "openai_compat",
    baseUrl: "https://api.fireworks.ai/inference/v1",
    keyEnv: "FIREWORKS_API_KEY",
    logo: "fireworks",
    keysUrl: "https://fireworks.ai/account/api-keys",
    testModel: "accounts/fireworks/models/llama-v3p3-70b-instruct",
    blurb: "Open models hosted by Fireworks AI.",
  },
  {
    id: "cerebras",
    label: "Cerebras",
    kind: "openai_compat",
    baseUrl: "https://api.cerebras.ai/v1",
    keyEnv: "CEREBRAS_API_KEY",
    logo: "cerebras",
    keysUrl: "https://cloud.cerebras.ai/platform",
    testModel: "llama3.1-8b",
    blurb: "Open models on Cerebras wafer-scale chips.",
  },
];

/* The Claude Code subscription card and its route segment (spec 39). Router
   records before spec 39 used the provider name claude-agent; since then a
   record names the Claude Code account (claude-code, or one added as
   [providers.<name>] kind = "claude-code"). */
export const CLAUDE_CODE_ID = "claude-code";
export const CLAUDE_CODE_KIND = "claude-code";
export const CLAUDE_AGENT_PROVIDER_NAME = "claude-agent";
export const CLAUDE_CODE_GROUP = "Claude Code";

/* The names of Claude Code accounts this page has seen, so a page that only
   knows a provider name (a log row, a combo chip) can still show the Claude
   logo. Pages fill it from GET /v1/router/claude-agent and /v1/providers. */
const claudeAccounts = new Set<string>([CLAUDE_CODE_ID]);

export function rememberClaudeAccounts(names: string[]): void {
  for (const name of names) {
    if (name) {
      claudeAccounts.add(name);
    }
  }
}

/* Claude Code's names: an account name, the old claude-agent records, and
   the model-picker group ("Claude Code" or "Claude Code · <account> …"). */
export function isClaudeCode(name: string): boolean {
  return (
    name === CLAUDE_AGENT_PROVIDER_NAME ||
    claudeAccounts.has(name) ||
    name === CLAUDE_CODE_GROUP ||
    name.startsWith(CLAUDE_CODE_GROUP + " · ")
  );
}

export function presetById(id: string): ProviderPreset | undefined {
  return PROVIDER_PRESETS.find((preset) => preset.id === id);
}

export function hostOf(value: string | undefined): string {
  if (!value) {
    return "";
  }
  try {
    return new URL(value).hostname.toLowerCase();
  } catch {
    return "";
  }
}

/* What a provider entry needs to be matched to a preset. */
export interface ProviderShape {
  name: string;
  kind: string;
  base_url?: string;
}

/* The preset a provider entry belongs to, or undefined for a custom
   OpenAI-compatible entry. */
export function presetFor(provider: ProviderShape): ProviderPreset | undefined {
  if (NATIVE_KINDS.includes(provider.kind)) {
    return PROVIDER_PRESETS.find((preset) => preset.kind === provider.kind);
  }
  const host = hostOf(provider.base_url);
  if (!host) {
    return undefined;
  }
  return PROVIDER_PRESETS.find((preset) => hostOf(preset.baseUrl) === host);
}

/* The logo key of a provider entry, of the claude-agent records, or "" for
   the monogram fallback. */
export function logoFor(provider: ProviderShape | undefined | null, name?: string): string {
  const key = provider ? provider.name : name || "";
  if (isClaudeCode(key) || (provider && provider.kind === CLAUDE_CODE_KIND)) {
    return "claude";
  }
  if (!provider) {
    return "";
  }
  const preset = presetFor(provider);
  return preset ? preset.logo : "";
}

/* The display label of a provider entry's family: "DeepSeek" for ds. */
export function familyLabel(provider: ProviderShape | undefined | null, name?: string): string {
  const key = provider ? provider.name : name || "";
  if (isClaudeCode(key) || (provider && provider.kind === CLAUDE_CODE_KIND)) {
    return "Claude Code";
  }
  const preset = provider ? presetFor(provider) : undefined;
  return preset ? preset.label : key;
}

/* The route segment of the card a provider entry belongs to. */
export function cardIdFor(provider: ProviderShape): string {
  if (provider.kind === CLAUDE_CODE_KIND) {
    return CLAUDE_CODE_ID;
  }
  const preset = presetFor(provider);
  return preset ? preset.id : provider.name;
}

export function providerHref(id: string): string {
  return "#/router/providers/" + encodeURIComponent(id);
}

/* The first free connection name: the preset id, then id-2, id-3, … */
export function suggestName(id: string, taken: string[]): string {
  if (!taken.includes(id)) {
    return id;
  }
  for (let index = 2; index < 1000; index += 1) {
    const candidate = id + "-" + index;
    if (!taken.includes(candidate)) {
      return candidate;
    }
  }
  return "";
}

/* <NAME>_API_KEY, the default key variable the daemon uses too. */
export function defaultKeyEnv(name: string): string {
  const value = String(name || "")
    .trim()
    .toUpperCase()
    .replace(/[^A-Z0-9_]/g, "_");
  if (!value) {
    return "";
  }
  return (/^[A-Za-z_]/.test(value) ? value : "_" + value) + "_API_KEY";
}

/* Two letters for a provider without a logo: "Local LLM" → "LL",
   "my-proxy" → "MP", "OpenAI" → "OA", "groq" → "GR". */
export function monogram(label: string): string {
  const text = String(label || "").trim();
  if (!text) {
    return "?";
  }
  const words = text.split(/[\s_\-./]+/).filter(Boolean);
  if (words.length >= 2) {
    return ((words[0] || "").charAt(0) + (words[1] || "").charAt(0)).toUpperCase();
  }
  const capitals = text.replace(/[^A-Z]/g, "");
  if (capitals.length >= 2) {
    return capitals.slice(0, 2);
  }
  const letters = text.replace(/[^A-Za-z0-9]/g, "");
  return (letters.slice(0, 2) || "?").toUpperCase();
}

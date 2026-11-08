import { useState } from "react";
import type { RouterChoice, RouterProvider } from "../api/router";
import { COMBOS_GROUP } from "../api/router";
import { Input, Radio, Select } from "../ui/gr8r";
import type { SelectOption } from "../ui/gr8r";
import { CLAUDE_RUNTIME } from "./agent-common";
import { ProviderMark, providerIndex } from "./router/provider-logo";

/* Spec 38 (REQ-CCR-009): the runtime choice of the agent forms and the
   Claude Agent controls, plus the router model options of spec 37
   (REQ-RTR-018) shared by the agent form and the session model menu. */

export type RuntimeValue = "" | typeof CLAUDE_RUNTIME;

export const RUNTIMES: Array<{ value: RuntimeValue; title: string; note: string }> = [
  {
    value: "",
    title: "lana",
    note: "lana's own loop: router models and combos, tools, skills, and MCP.",
  },
  {
    value: CLAUDE_RUNTIME,
    title: "Claude Agent",
    note: "Runs Claude Code on this machine with its own tools. Uses your Claude plan or an API key.",
  },
];

export const CLAUDE_MODELS: SelectOption[] = [
  { value: "", label: "Claude Code default" },
  { value: "opus", label: "Opus" },
  { value: "sonnet", label: "Sonnet" },
  { value: "haiku", label: "Haiku" },
];

export const CUSTOM_VALUE = "__custom__";

export const CLAUDE_EFFORTS: SelectOption[] = [
  { value: "", label: "Default" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
  { value: "xhigh", label: "Extra high" },
  { value: "max", label: "Max" },
];

export const CLAUDE_AUTH: Array<{ value: string; title: string; note: string }> = [
  { value: "subscription", title: "Claude plan", note: "Uses the login of `claude` on this machine." },
  { value: "api_key", title: "API key", note: "Bills the Anthropic API key in an env variable." },
];

export interface ClaudeValues {
  model: string;
  reasoning_effort: string;
  auth: string;
  api_key_env: string;
  binary: string;
  allow_scheduled: boolean;
}

export const DEFAULT_CLAUDE: ClaudeValues = {
  model: "",
  reasoning_effort: "",
  auth: "subscription",
  api_key_env: "",
  binary: "",
  allow_scheduled: false,
};

export function RuntimeChoice({
  value,
  disabled,
  name,
  labelledBy,
  onChange,
}: {
  value: RuntimeValue;
  disabled?: boolean;
  name: string;
  labelledBy: string;
  onChange: (value: RuntimeValue) => void;
}) {
  return (
    <div className="rt-runtime" role="radiogroup" aria-labelledby={labelledBy}>
      {RUNTIMES.map((entry) => {
        const on = value === entry.value;
        return (
          <label key={entry.value || "lana"} className={"rt-runtime-tile" + (on ? " is-on" : "")}>
            <Radio
              name={name}
              value={entry.value || "lana"}
              aria-label={entry.title}
              checked={on}
              disabled={disabled}
              onChange={() => onChange(entry.value)}
            />
            <span>
              <b>{entry.title}</b>
              <span>{entry.note}</span>
            </span>
          </label>
        );
      })}
    </div>
  );
}

/* Model: default, opus, sonnet, haiku, or a custom ID typed below. */
export function ClaudeModelControl({
  id,
  value,
  disabled,
  onChange,
}: {
  id: string;
  value: string;
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  const known = CLAUDE_MODELS.some((option) => option.value === value);
  const [custom, setCustom] = useState(!known);
  const showCustom = custom || !known;
  return (
    <>
      <Select
        id={id}
        aria-label="Claude model"
        value={showCustom ? CUSTOM_VALUE : value}
        disabled={disabled}
        options={[...CLAUDE_MODELS, { value: CUSTOM_VALUE, label: "Custom model ID…" }]}
        onChange={(next) => {
          if (next === CUSTOM_VALUE) {
            setCustom(true);
            return;
          }
          setCustom(false);
          onChange(next);
        }}
      />
      {showCustom ? (
        <Input
          className="mono"
          aria-label="Custom Claude model ID"
          placeholder="claude-opus-4-1"
          value={value}
          disabled={disabled}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
    </>
  );
}

/* Router choices as Select options: combos first, then the models of each
   provider under its name (AC-RTR-024). With the provider list, each model
   carries its provider's logo. */
export function choiceOptions(choices: RouterChoice[], providers?: RouterProvider[]): SelectOption[] {
  const index = providers ? providerIndex(providers) : null;
  const combos = choices.filter((choice) => choice.group === COMBOS_GROUP);
  const groups: string[] = [];
  for (const choice of choices) {
    if (choice.group !== COMBOS_GROUP && !groups.includes(choice.group)) {
      groups.push(choice.group);
    }
  }
  const models = groups.flatMap((group) => choices.filter((choice) => choice.group === group));
  return combos.concat(models).map((choice) => ({
    value: choice.value,
    label: choice.label || choice.value,
    group: choice.group === COMBOS_GROUP ? "Combos" : choice.group,
    icon:
      index && choice.group !== COMBOS_GROUP ? (
        <ProviderMark provider={index[choice.group]} name={choice.group} size={16} />
      ) : undefined,
  }));
}

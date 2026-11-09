import type { ClaudeAccount, RouterChoice, RouterProvider } from "../api/router";
import { COMBOS_GROUP } from "../api/router";
import type { SelectOption } from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { ProviderLogo, ProviderMark, providerIndex } from "./router/provider-logo";
import {
  CLAUDE_CODE_HREF,
  CLAUDE_CODE_NOTE,
  claudeAccountNames,
  groupClaudeChoices,
  isClaudeChoice,
} from "./router/claude-code";

/* The router model options of spec 37 (REQ-RTR-018) shared by the agent
   forms, and the Claude Code note of spec 39 (REQ-CCT-009). There is no
   engine choice: every agent is a lana agent, and a Claude Code model runs
   its turns in Claude Code. */

/* The reasoning effort values Claude Code accepts as --effort. */
export const CLAUDE_EFFORTS: SelectOption[] = [
  { value: "", label: "Default" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
  { value: "xhigh", label: "Extra high" },
  { value: "max", label: "Max" },
];

/* Router choices as Select options: combos first, then the models of each
   provider under its name (AC-RTR-024), and Claude Code models under one
   group per account with the Claude logo (REQ-CCT-009). With the provider
   list, each model carries its provider's logo. */
export function choiceOptions(
  choices: RouterChoice[],
  providers?: RouterProvider[],
  accounts?: ClaudeAccount[] | null
): SelectOption[] {
  const index = providers ? providerIndex(providers) : null;
  const names = claudeAccountNames(choices, providers, accounts);
  const grouped = groupClaudeChoices(choices, providers, accounts);
  const combos = grouped.filter((choice) => choice.group === COMBOS_GROUP);
  const groups: string[] = [];
  for (const choice of grouped) {
    if (choice.group !== COMBOS_GROUP && !groups.includes(choice.group)) {
      groups.push(choice.group);
    }
  }
  const models = groups.flatMap((group) => grouped.filter((choice) => choice.group === group));
  return combos.concat(models).map((choice) => {
    const claude = isClaudeChoice(choice, names);
    return {
      value: choice.value,
      label: choice.label || choice.value,
      group: choice.group === COMBOS_GROUP ? "Combos" : choice.group,
      icon: claude ? (
        <ProviderLogo logo="claude" name="Claude Code" size={16} />
      ) : index && choice.group !== COMBOS_GROUP ? (
        <ProviderMark provider={index[choice.group]} name={choice.group} size={16} />
      ) : undefined,
    };
  });
}

/* The inline note under a model picker when the picked model runs in Claude
   Code (AC-CCT-011). */
export function ClaudeCodeNote({ account }: { account: string }) {
  return (
    <div className="agent-cc-note" role="note">
      <ProviderLogo logo="claude" name="Claude Code" size={18} />
      <p>
        {CLAUDE_CODE_NOTE}{" "}
        <a href={CLAUDE_CODE_HREF}>
          Account {account}
          <Ic name="arrow-up-right" size={12} />
        </a>
      </p>
    </div>
  );
}

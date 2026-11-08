/* eslint-disable react-hooks/set-state-in-effect -- the hook loads the account status on mount, like the pages do. */
import { useCallback, useEffect, useState } from "react";
import { getClaudeAgent } from "../../api/router";
import type { ClaudeAccount, ClaudeAgentStatus, RouterChoice, RouterCombo } from "../../api/router";
import {
  CLAUDE_CODE_GROUP,
  CLAUDE_CODE_ID,
  CLAUDE_CODE_KIND,
  isClaudeCode,
  providerHref,
  rememberClaudeAccounts,
} from "./presets";
import type { ProviderShape } from "./presets";
import { ProviderLogo } from "./provider-logo";

/* Spec 39 (REQ-CCT-009..011): Claude Code is a router target. Every agent is
   a lana agent; an agent whose model is "<account>/<model>" of a Claude Code
   account runs its turns in Claude Code. These helpers find Claude Code
   models in the router choices and group them per account. */

export const CLAUDE_CODE_NOTE =
  "Runs in Claude Code with your Claude plan. Claude Code uses its own file and shell tools; lana's memory, knowledge, calendar, workflow and other tools come in through MCP. Max steps and compaction settings don't apply.";

export const ACCOUNTS_TERMS_NOTE =
  "Use separate accounts for separate purposes (for example personal and work). Switching between your own accounts to get around plan limits can break Anthropic's terms.";

export const CLAUDE_CODE_HREF = providerHref(CLAUDE_CODE_ID);

/* The Claude Code account status of GET /v1/router/claude-agent. A failed or
   missing call leaves an empty list; every caller works without it. */
export function useClaudeAccounts(): {
  status: ClaudeAgentStatus | null;
  accounts: ClaudeAccount[];
  failed: boolean;
  reload: () => Promise<ClaudeAgentStatus | null>;
} {
  const [status, setStatus] = useState<ClaudeAgentStatus | null>(null);
  const [failed, setFailed] = useState(false);

  const reload = useCallback(async () => {
    try {
      const value = await Promise.resolve().then(() => getClaudeAgent());
      if (!value || !Array.isArray(value.accounts)) {
        throw new Error("no status");
      }
      rememberClaudeAccounts(value.accounts.map((account) => account.name));
      setStatus(value);
      setFailed(false);
      return value;
    } catch {
      setFailed(true);
      return null;
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { status, accounts: status ? status.accounts : [], failed, reload };
}

/* The provider part of an address: "claude-work" of "claude-work/opus". */
export function addressAccount(value: string): string {
  const slash = String(value || "").indexOf("/");
  return slash > 0 ? value.slice(0, slash) : "";
}

function isClaudeGroup(group: string): boolean {
  return group === CLAUDE_CODE_GROUP || group.startsWith(CLAUDE_CODE_GROUP + " · ");
}

/* The names of every Claude Code account the caller knows: the account
   status, kind "claude-code" providers, and the prefixes of the Claude Code
   picker group. The built-in claude-code is always one. */
export function claudeAccountNames(
  choices: RouterChoice[],
  providers?: ProviderShape[] | null,
  accounts?: ClaudeAccount[] | null
): string[] {
  const names: string[] = [];
  const add = (name: string) => {
    if (name && !names.includes(name)) {
      names.push(name);
    }
  };
  for (const account of accounts || []) {
    add(account.name);
  }
  for (const provider of providers || []) {
    if (provider.kind === CLAUDE_CODE_KIND) {
      add(provider.name);
    }
  }
  for (const choice of choices) {
    if (isClaudeGroup(choice.group)) {
      add(addressAccount(choice.value));
    }
  }
  if (names.length === 0) {
    add(CLAUDE_CODE_ID);
  }
  return names;
}

/* Whether a choice is a Claude Code model. */
export function isClaudeChoice(choice: RouterChoice, names: string[]): boolean {
  if (choice.group === "Combos") {
    return false;
  }
  return isClaudeGroup(choice.group) || names.includes(addressAccount(choice.value));
}

/* The picker group of one account: "Claude Code" when there is one account,
   else "Claude Code · <account> (<email>)". */
export function claudeGroupLabel(account: string, email: string, several: boolean): string {
  if (!several) {
    return CLAUDE_CODE_GROUP;
  }
  return CLAUDE_CODE_GROUP + " · " + account + (email ? " (" + email + ")" : "");
}

/* The router choices with each Claude Code model in its account's group
   (REQ-CCT-009). Other choices keep their order and group. */
export function groupClaudeChoices(
  choices: RouterChoice[],
  providers?: ProviderShape[] | null,
  accounts?: ClaudeAccount[] | null
): RouterChoice[] {
  const names = claudeAccountNames(choices, providers, accounts);
  const used: string[] = [];
  for (const choice of choices) {
    if (isClaudeChoice(choice, names)) {
      const account = addressAccount(choice.value) || CLAUDE_CODE_ID;
      if (!used.includes(account)) {
        used.push(account);
      }
    }
  }
  const several = used.length > 1;
  const emails: Record<string, string> = {};
  for (const account of accounts || []) {
    emails[account.name] = account.email;
  }
  return choices.map((choice) => {
    if (!isClaudeChoice(choice, names)) {
      return choice;
    }
    const account = addressAccount(choice.value) || CLAUDE_CODE_ID;
    return { ...choice, group: claudeGroupLabel(account, emails[account] || "", several) };
  });
}

/* The Claude Code account an agent model runs on, or "": a Claude Code
   address, or a combo whose first model is one. */
export function claudeTargetAccount(
  value: string,
  choices: RouterChoice[],
  combos: RouterCombo[] | null | undefined,
  names: string[]
): string {
  const text = String(value || "");
  if (!text) {
    return "";
  }
  const combo = (combos || []).find((entry) => entry.name === text);
  const target = combo ? String(combo.models[0] || "") : text;
  const choice = choices.find((entry) => entry.value === target);
  if (choice && isClaudeChoice(choice, names)) {
    return addressAccount(target) || CLAUDE_CODE_ID;
  }
  const account = addressAccount(target);
  return account && (names.includes(account) || isClaudeCode(account)) ? account : "";
}

/* A small Claude mark with a label, for agent cards and headers. */
export function ClaudeCodeBadge({ label = "Claude Code" }: { label?: string }) {
  return (
    <span className="rt-cc-badge" title="Runs in Claude Code">
      <ProviderLogo logo="claude" name="Claude Code" size={14} />
      <span>{label}</span>
    </span>
  );
}

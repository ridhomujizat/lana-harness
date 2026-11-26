import { useEffect, useState } from "react";
import { getAntigravity } from "../api/router";
import type { AntigravityAccount, ClaudeAccount, RouterChoice, RouterProvider } from "../api/router";
import type { SelectOption } from "../ui/gr8r";
import { addressAccount, claudeAccountNames, isClaudeChoice } from "./router/claude-code";
import { isAntigravityGroup } from "./router/presets";

/* Spec 77 revision 6: the agent model picker lists each Claude Code and
   Antigravity model once, under one "Claude Code" or "Antigravity" group.
   The account the model runs on is a separate choice under the picker,
   shown when more than one account offers the model. The saved value stays
   the full address, "<account>/<model>". */

export type AccountFamily = "claude" | "antigravity";

export const CLAUDE_FAMILY_GROUP = "Claude Code";
export const ANTIGRAVITY_FAMILY_GROUP = "Antigravity";

/* The part of an address after the account: "opus" of "claude-work/opus". */
export function addressModel(value: string): string {
  const text = String(value || "");
  const slash = text.indexOf("/");
  return slash > 0 ? text.slice(slash + 1) : "";
}

function familyOf(choice: RouterChoice, claudeNames: string[]): AccountFamily | null {
  if (choice.group === "Combos") {
    return null;
  }
  if (isAntigravityGroup(choice.group)) {
    return "antigravity";
  }
  if (isClaudeChoice(choice, claudeNames)) {
    return "claude";
  }
  return null;
}

/* "Claude Code (claude-work) · Opus" reads "Claude Code · Opus". */
function familyLabel(choice: RouterChoice): string {
  const label = choice.label || choice.value;
  return label.replace(/^Claude Code \([^)]*\) · /, "Claude Code · ");
}

interface FamilyModel {
  family: AccountFamily;
  model: string;
  /* The accounts offering the model, in the router's order. */
  accounts: string[];
}

function familyModels(choices: RouterChoice[], claudeNames: string[]): Map<string, FamilyModel> {
  const out = new Map<string, FamilyModel>();
  for (const choice of choices) {
    const family = familyOf(choice, claudeNames);
    const account = addressAccount(choice.value);
    const model = addressModel(choice.value);
    if (!family || !account || !model) {
      continue;
    }
    const key = family + "\u0000" + model;
    const entry = out.get(key);
    if (entry) {
      if (!entry.accounts.includes(account)) {
        entry.accounts.push(account);
      }
    } else {
      out.set(key, { family, model, accounts: [account] });
    }
  }
  return out;
}

/* The router choices with the Claude Code and Antigravity models merged: one
   choice per model of each family, in the place of its first account. A
   merged choice's address uses the current model's account when that
   account offers the model, else the first account that does. */
export function mergeAccountChoices(
  choices: RouterChoice[],
  current: string,
  providers?: RouterProvider[] | null,
  accounts?: ClaudeAccount[] | null
): RouterChoice[] {
  const claudeNames = claudeAccountNames(choices, providers, accounts);
  const models = familyModels(choices, claudeNames);
  const currentChoice = choices.find((choice) => choice.value === current);
  const currentFamily = currentChoice ? familyOf(currentChoice, claudeNames) : null;
  const currentAccount = addressAccount(current);
  const seen = new Set<string>();
  const out: RouterChoice[] = [];
  for (const choice of choices) {
    const family = familyOf(choice, claudeNames);
    const model = addressModel(choice.value);
    if (!family || !model) {
      out.push(choice);
      continue;
    }
    const key = family + "\u0000" + model;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    const entry = models.get(key);
    const account =
      entry && family === currentFamily && entry.accounts.includes(currentAccount)
        ? currentAccount
        : addressAccount(choice.value);
    const value = account + "/" + model;
    const source = choices.find((item) => item.value === value) || choice;
    out.push({
      ...source,
      value,
      label: familyLabel(source),
      group: family === "claude" ? CLAUDE_FAMILY_GROUP : ANTIGRAVITY_FAMILY_GROUP,
    });
  }
  return out;
}

/* The accounts that offer the model of an address, when it is a Claude Code
   or Antigravity model; null otherwise. */
export function modelAccounts(
  value: string,
  choices: RouterChoice[],
  providers?: RouterProvider[] | null,
  accounts?: ClaudeAccount[] | null
): FamilyModel | null {
  const claudeNames = claudeAccountNames(choices, providers, accounts);
  const choice = choices.find((item) => item.value === value);
  const family = choice ? familyOf(choice, claudeNames) : null;
  const model = addressModel(value);
  if (!family || !model) {
    return null;
  }
  return familyModels(choices, claudeNames).get(family + "\u0000" + model) || null;
}

/* The address of the same model on another account. */
export function withAccount(value: string, account: string): string {
  return account + "/" + addressModel(value);
}

interface NamedAccount {
  name: string;
  label?: string;
  email?: string;
  display_name?: string;
}

/* The Account select options: each account by its label, else its email,
   else its name (REQ-WRA-013), with the email or name as a hint. */
export function accountOptions(
  entry: FamilyModel,
  claude: ClaudeAccount[] | null | undefined,
  antigravity: AntigravityAccount[] | null | undefined
): SelectOption[] {
  const list: NamedAccount[] = entry.family === "claude" ? claude || [] : antigravity || [];
  return entry.accounts.map((name) => {
    const account = list.find((item) => item.name === name);
    const shown = account ? account.label || account.email || account.name : name;
    const email = account && account.email && account.email !== shown ? account.email : "";
    const detail = [email, shown !== name ? name : ""].filter(Boolean).join(" · ");
    return { value: name, label: detail ? shown + " — " + detail : shown };
  });
}

/* The Antigravity accounts of GET /v1/router/antigravity; empty when the
   call fails, so callers fall back to account names. */
export function useAntigravityAccounts(): AntigravityAccount[] {
  const [accounts, setAccounts] = useState<AntigravityAccount[]>([]);
  useEffect(() => {
    let alive = true;
    Promise.resolve()
      .then(() => getAntigravity())
      .then((status) => {
        if (alive && status && Array.isArray(status.accounts)) {
          setAccounts(status.accounts);
        }
      })
      .catch(() => {
        /* names stand in for the labels */
      });
    return () => {
      alive = false;
    };
  }, []);
  return accounts;
}

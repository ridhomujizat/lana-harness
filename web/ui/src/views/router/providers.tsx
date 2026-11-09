/* eslint-disable react-hooks/set-state-in-effect -- the page resets its load state before each request, as the settings pages do. */
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import * as routerApi from "../../api/router";
import type { ClaudeAgentStatus, ListProvidersResponse, ProviderRow, RouterChoice } from "../../api/router";
import { Badge, Button, Skeleton } from "../../ui/gr8r";
import { AgentAvatarStack, agentFallbackColor } from "../agent-avatar";
import type { BadgeTone } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { Alert, SettingsRestartBanner, StatusLine } from "../settings-pages";
import { ConnectionDialog } from "./connection-dialog";
import {
  CLAUDE_CODE_ID,
  CLAUDE_CODE_KIND,
  PROVIDER_PRESETS,
  cardIdFor,
  presetFor,
  providerHref,
  rememberClaudeAccounts,
} from "./presets";
import type { ProviderPreset } from "./presets";
import { ProviderLogo } from "./provider-logo";
import { failureOf, ms } from "./router-common";

/* Router → Providers (REQ-RTR-014, API-RTR-006), laid out like 9router's
   Providers page: one card per provider, always shown, with how many
   connections it has. A card opens the provider's page, where its
   connections ([providers.<name>] entries), models, usage, and quota live. */

/* used_by holds "agents.<name>", "combos.<name>", or bare agent names. */
export function splitUsers(usedBy: string[] | undefined): { agents: string[]; others: string[] } {
  const agents: string[] = [];
  const others: string[] = [];
  for (const raw of Array.isArray(usedBy) ? usedBy : []) {
    const text = String(raw || "");
    if (!text) {
      continue;
    }
    if (text.startsWith("agents.")) {
      agents.push(text.slice("agents.".length));
    } else if (text.includes(".")) {
      const [kind, ...rest] = text.split(".");
      const name = rest.join(".");
      others.push((kind === "combos" ? "combo " : kind + " ") + name);
    } else {
      agents.push(text);
    }
  }
  return { agents, others };
}

export function agentColor(name: string): string {
  return agentFallbackColor(name);
}

export function UsedBy({ usedBy }: { usedBy: string[] | undefined }) {
  const { agents, others } = splitUsers(usedBy);
  if (agents.length === 0 && others.length === 0) {
    return <span className="hint">Not used</span>;
  }
  return (
    <span className="rt-used-by" title={agents.concat(others).join(", ")}>
      {agents.length > 0 ? (
        <AgentAvatarStack
          size="sm"
          max={3}
          items={agents.map((name) => ({ name, color: agentColor(name) }))}
        />
      ) : null}
      <span className="sr">{agents.concat(others).join(", ")}</span>
      {others.length > 0 ? (
        <span className="hint trunc" aria-hidden="true">
          {others.join(", ")}
        </span>
      ) : null}
    </span>
  );
}

/* The key column of a connection: whether its variable holds a value. */
export function keyState(provider: ProviderRow): { label: string; tone: BadgeTone; note: string } {
  if (!provider.api_key_env) {
    return { label: "No key needed", tone: "gray", note: "" };
  }
  if (provider.key_state === "overridden_by_env") {
    return { label: "Set", tone: "green", note: "from environment" };
  }
  if (provider.key_state === "set") {
    return { label: "Set", tone: "green", note: "" };
  }
  return { label: "Missing", tone: "amber", note: "" };
}

export function keyMissing(provider: ProviderRow): boolean {
  return (
    Boolean(provider.api_key_env) &&
    provider.key_state !== "set" &&
    provider.key_state !== "overridden_by_env"
  );
}

/* The card status of a set of connections. */
export interface CardStatus {
  count: number;
  enabled: number;
  missing: boolean;
}

export function cardStatus(connections: ProviderRow[]): CardStatus {
  return {
    count: connections.length,
    enabled: connections.filter((row) => !row.disabled).length,
    missing: connections.some((row) => !row.disabled && keyMissing(row)),
  };
}

export function StatusPills({ status }: { status: CardStatus }) {
  if (status.count === 0) {
    return <span className="rt-pc-none">No connections</span>;
  }
  return (
    <>
      {status.enabled === 0 ? (
        <Badge tone="gray" dot>
          Off
        </Badge>
      ) : (
        <Badge tone="green" dot>
          {status.count} Connected
        </Badge>
      )}
      {status.missing ? <Badge tone="amber">Missing key</Badge> : null}
    </>
  );
}

/* A Test All result on a card. */
export type CardTest =
  | { state: "busy" }
  | { state: "ok"; latency: number; model: string }
  | { state: "bad"; message: string }
  | { state: "skip"; message: string };

function CardTestLine({ test }: { test: CardTest | undefined }) {
  if (!test) {
    return null;
  }
  if (test.state === "busy") {
    return (
      <span className="rt-pc-test" role="status">
        <Ic name="loader-circle" size={12} className="spin" /> Testing…
      </span>
    );
  }
  if (test.state === "ok") {
    return (
      <span className="rt-pc-test ok" role="status" title={test.model}>
        <Ic name="circle-check" size={12} /> OK · {ms(test.latency)}
      </span>
    );
  }
  return (
    <span className={"rt-pc-test" + (test.state === "bad" ? " bad" : "")} role="status" title={test.message}>
      <Ic name={test.state === "bad" ? "circle-x" : "circle-alert"} size={12} />
      <span className="trunc">{test.state === "bad" ? "Failed: " + test.message : test.message}</span>
    </span>
  );
}

function ProviderCard({
  id,
  label,
  logo,
  status,
  test,
  extra,
}: {
  id: string;
  label: string;
  logo: string;
  status: ReactNode;
  test?: CardTest;
  extra?: string;
}) {
  return (
    <article className="rt-pc" aria-label={label}>
      <a className="rt-pc-link" href={providerHref(id)}>
        <ProviderLogo logo={logo} name={label} size={38} />
        <span className="rt-pc-text">
          <span className="rt-pc-name trunc">{label}</span>
          <span className="rt-pc-status">{status}</span>
          {extra ? <span className="rt-pc-extra mono trunc">{extra}</span> : null}
          <CardTestLine test={test} />
        </span>
        <Ic name="chevron-right" size={15} className="rt-pc-go" />
      </a>
    </article>
  );
}

function SectionHead({
  id,
  title,
  note,
  children,
}: {
  id: string;
  title: string;
  note?: string;
  children?: ReactNode;
}) {
  return (
    <div className="rt-ps-h">
      <div className="rt-ps-t">
        <h2 id={id}>{title}</h2>
        {note ? <p className="muted">{note}</p> : null}
      </div>
      {children ? <div className="rt-ps-acts">{children}</div> : null}
    </div>
  );
}

/* Claude Code's card status (REQ-CCT-009, API-CCT-011): how many accounts
   and how many of them are logged in. */
export function claudeStatus(
  status: ClaudeAgentStatus | null,
  failed: boolean
): { label: string; tone: BadgeTone } {
  if (!status) {
    return failed ? { label: "Status unavailable", tone: "gray" } : { label: "Checking…", tone: "gray" };
  }
  const accounts = status.accounts;
  if (!status.binary && !accounts.some((account) => account.binary)) {
    return { label: "Not installed", tone: "gray" };
  }
  const total = accounts.length;
  const loggedIn = accounts.filter((account) => account.logged_in).length;
  return {
    label: total + (total === 1 ? " account" : " accounts") + " · " + loggedIn + " logged in",
    tone: loggedIn === 0 ? "amber" : "green",
  };
}

/* The model Test All sends for a connection: the first model the router lists
   for it, else the preset's test model. */
function testAddress(row: ProviderRow, preset: ProviderPreset | undefined, choices: RouterChoice[]): string {
  const choice = choices.find((entry) => entry.group === row.name);
  if (choice) {
    return choice.value;
  }
  return preset ? row.name + "/" + preset.testModel : "";
}

interface TestCard {
  id: string;
  rows: ProviderRow[];
  preset?: ProviderPreset;
}

export function RouterProviders() {
  const [data, setData] = useState<ListProvidersResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [claude, setClaude] = useState<ClaudeAgentStatus | null>(null);
  const [claudeFailed, setClaudeFailed] = useState(false);
  const [adding, setAdding] = useState(false);
  const [tests, setTests] = useState<Record<string, CardTest>>({});
  const [testing, setTesting] = useState("");
  const [reloadToken, setReloadToken] = useState(0);

  const reload = useCallback(() => setReloadToken((current) => current + 1), []);

  useEffect(() => {
    let alive = true;
    setState("loading");
    setError("");
    setDetails([]);
    routerApi
      .listProviders()
      .then((value) => {
        if (alive) {
          setData(value);
          setState("ready");
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          const info = failureOf(failure);
          setError(info.message);
          setDetails(info.details);
          setState("error");
        }
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  useEffect(() => {
    let alive = true;
    routerApi
      .getClaudeAgent()
      .then((value) => {
        if (alive) {
          rememberClaudeAccounts(value.accounts.map((account) => account.name));
          setClaude(value);
        }
      })
      .catch(() => {
        if (alive) {
          setClaudeFailed(true);
        }
      });
    return () => {
      alive = false;
    };
  }, []);

  const providers = useMemo(() => (data && Array.isArray(data.providers) ? data.providers : []), [data]);
  const byCard = useMemo(() => {
    const groups: Record<string, ProviderRow[]> = {};
    for (const row of providers) {
      const id = cardIdFor(row);
      (groups[id] = groups[id] || []).push(row);
    }
    return groups;
  }, [providers]);

  /* Test All (POST /v1/router/test): one model of the first enabled
     connection of each connected card in the section. */
  async function testAll(section: string, cards: TestCard[]) {
    if (testing) {
      return;
    }
    const targets = cards.filter((card) => card.rows.some((row) => !row.disabled));
    if (targets.length === 0) {
      setMessage("Nothing to test yet. Connect a provider first.");
      return;
    }
    setTesting(section);
    setMessage("");
    setTests((current) => {
      const next = { ...current };
      for (const card of targets) {
        next[card.id] = { state: "busy" };
      }
      return next;
    });
    let choices: RouterChoice[] = [];
    try {
      choices = (await routerApi.getRouter()).choices;
    } catch {
      choices = [];
    }
    await Promise.all(
      targets.map(async (card) => {
        const row = card.rows.find((entry) => !entry.disabled) as ProviderRow;
        const address = testAddress(row, card.preset, choices);
        let result: CardTest;
        if (!address) {
          result = { state: "skip", message: "No model to test. Open it and refresh its models." };
        } else {
          try {
            const answer = await routerApi.testRouterModel(address);
            if (answer && answer.ok) {
              result = { state: "ok", latency: Number(answer.latency_ms) || 0, model: address };
            } else {
              const parts = [
                answer?.class,
                answer?.status ? "HTTP " + answer.status : "",
                answer?.message,
              ].filter(Boolean);
              result = { state: "bad", message: parts.join(" · ") || "no answer" };
            }
          } catch (failure) {
            result = { state: "bad", message: failureOf(failure).message };
          }
        }
        setTests((current) => ({ ...current, [card.id]: result }));
      })
    );
    setTesting("");
  }

  const apiCards = PROVIDER_PRESETS.map((preset) => ({ id: preset.id, preset, rows: byCard[preset.id] || [] }));
  const customCards: TestCard[] = providers
    .filter((row) => row.kind !== CLAUDE_CODE_KIND && !presetFor(row))
    .map((row) => ({ id: row.name, rows: [row] }));
  const claudeState = claudeStatus(claude, claudeFailed);
  const claudeEmail = claude ? (claude.accounts.find((account) => account.email) || { email: "" }).email : "";
  const canAdd = Boolean(data && data.version);

  return (
    <div className="rt-providers">
      <SettingsRestartBanner restartRequired={Boolean(data && data.restart_required)} onRestarted={reload} />
      <StatusLine text={message} />
      <Alert tone="danger" message={error} details={details} />
      {state === "error" ? (
        <div className="settings-actions-bar">
          <Button variant="secondary" onClick={reload}>
            Reload providers
          </Button>
        </div>
      ) : null}

      <section className="rt-ps" aria-labelledby="rt-ps-subs">
        <SectionHead id="rt-ps-subs" title="Subscriptions" note="Plans you already pay for, used through their own app." />
        <div className="rt-pgrid">
          <ProviderCard
            id={CLAUDE_CODE_ID}
            label="Claude Code"
            logo="claude"
            status={
              <Badge tone={claudeState.tone} dot={claudeState.tone !== "gray"}>
                {claudeState.label}
              </Badge>
            }
            extra={claudeEmail}
          />
        </div>
      </section>

      <section className="rt-ps" aria-labelledby="rt-ps-api">
        <SectionHead
          id="rt-ps-api"
          title="API Key Providers"
          note="Paste a key once. Agents and combos can then use every model it unlocks."
        >
          <Button
            variant="secondary"
            size="sm"
            icon="play"
            loading={testing === "api"}
            disabled={Boolean(testing) || state !== "ready"}
            onClick={() => void testAll("api", apiCards)}
          >
            Test All
          </Button>
        </SectionHead>
        <div className="rt-pgrid">
          {state === "loading"
            ? PROVIDER_PRESETS.map((preset) => (
                <div key={preset.id} className="rt-pc is-loading" aria-hidden="true">
                  <span className="rt-pc-link">
                    <Skeleton width={38} height={38} radius={9} />
                    <span className="rt-pc-text">
                      <Skeleton width="60%" height={12} />
                      <Skeleton width="40%" height={10} />
                    </span>
                  </span>
                </div>
              ))
            : apiCards.map((card) => (
                <ProviderCard
                  key={card.id}
                  id={card.id}
                  label={card.preset.label}
                  logo={card.preset.logo}
                  status={<StatusPills status={cardStatus(card.rows)} />}
                  test={tests[card.id]}
                />
              ))}
        </div>
      </section>

      <section className="rt-ps" aria-labelledby="rt-ps-compat">
        <SectionHead
          id="rt-ps-compat"
          title="Compatible Providers"
          note="Your own servers and gateways that speak the OpenAI API."
        >
          {customCards.length > 0 ? (
            <Button
              variant="secondary"
              size="sm"
              icon="play"
              loading={testing === "compat"}
              disabled={Boolean(testing)}
              onClick={() => void testAll("compat", customCards)}
            >
              Test All
            </Button>
          ) : null}
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            disabled={!canAdd}
            title={data && !data.version ? "config.toml is missing. Run lana init first." : undefined}
            onClick={() => setAdding(true)}
          >
            Add OpenAI Compatible
          </Button>
        </SectionHead>
        {customCards.length === 0 ? (
          <p className="rt-ps-empty">
            None yet. Add a local Ollama, vLLM, or LM Studio server, or any gateway with an
            OpenAI-style API.
          </p>
        ) : (
          <div className="rt-pgrid">
            {customCards.map((card) => {
              const row = card.rows[0] as ProviderRow;
              return (
                <ProviderCard
                  key={card.id}
                  id={card.id}
                  label={row.name}
                  logo=""
                  status={<StatusPills status={cardStatus(card.rows)} />}
                  test={tests[card.id]}
                  extra={row.base_url}
                />
              );
            })}
          </div>
        )}
      </section>

      <p className="settings-footer-note">
        Agents pick a model on their General tab. Keys live in <a href="#/settings/secrets">Secrets</a>.
      </p>

      {adding && data ? (
        <ConnectionDialog
          version={data.version}
          takenNames={providers.map((row) => row.name)}
          onClose={() => setAdding(false)}
          onSaved={(_text, _restart, name) => {
            setAdding(false);
            window.location.hash = providerHref(name);
          }}
        />
      ) : null}
    </div>
  );
}

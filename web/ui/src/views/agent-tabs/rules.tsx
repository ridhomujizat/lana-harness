/* eslint-disable react-hooks/set-state-in-effect -- the legacy tab resets its load state before each request, as in rules.js */
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, errorMessage } from "../../api/client";
import { isErrorCode, loadAgentTab, updateAgent } from "../../api/agents";
import { ErrorNotice, Loading } from "../../ui/ui";
import { Ic } from "../../ui/icons";

/* REQ-WEB-047: the allow and deny pattern lists. Each row is one pattern;
   saving writes rules as {"allow": [...], "deny": [...]}. */

interface Rules {
  allow: string[];
  deny: string[];
}

type RuleKind = "allow" | "deny";

interface RuleSection {
  kind: RuleKind;
  title: string;
  description: string;
  empty: string;
  add: string;
}

function normalizeRules(value: unknown): Rules {
  const source =
    value && typeof value === "object"
      ? (value as Record<string, unknown>)
      : {};
  return {
    allow: Array.isArray(source.allow) ? source.allow.map(String) : [],
    deny: Array.isArray(source.deny) ? source.deny.map(String) : [],
  };
}

function cloneRules(value: Rules): Rules {
  return {
    allow: value.allow.slice(),
    deny: value.deny.slice(),
  };
}

const RULE_SECTIONS: readonly RuleSection[] = [
  {
    kind: "allow",
    title: "Allow",
    description:
      "Patterns that skip the approval step. One pattern per row, for example bash(go test*).",
    empty: "No allow rules.",
    add: "Add allow rule",
  },
  {
    kind: "deny",
    title: "Deny",
    description:
      "Patterns that are always refused. One pattern per row, for example bash(rm -rf*).",
    empty: "No deny rules.",
    add: "Add deny rule",
  },
];

export interface RulesTabProps {
  name: string;
}

export function RulesTab({ name }: RulesTabProps) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [form, setForm] = useState<Rules>({ allow: [], deny: [] });
  const [baseline, setBaseline] = useState<Rules>({ allow: [], deny: [] });
  const [version, setVersion] = useState("");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [stale, setStale] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    setStale(false);
    setNotice("");
    loadAgentTab(name)
      .then((config) => {
        if (!alive) {
          return;
        }
        const rules = normalizeRules(config.agent.rules);
        setForm(cloneRules(rules));
        setBaseline(cloneRules(rules));
        setVersion(config.version);
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
      });
    return () => {
      alive = false;
    };
  }, [name, reloadToken]);

  const dirty = JSON.stringify(form) !== JSON.stringify(baseline);

  function reload() {
    setReloadToken((value) => value + 1);
  }

  function updateRule(kind: RuleKind, index: number, value: string) {
    setForm((current) => {
      const next = cloneRules(current);
      next[kind][index] = value;
      return next;
    });
    setNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function addRule(kind: RuleKind) {
    setForm((current) => {
      const next = cloneRules(current);
      next[kind].push("");
      return next;
    });
    setNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function removeRule(kind: RuleKind, index: number) {
    setForm((current) => {
      const next = cloneRules(current);
      next[kind].splice(index, 1);
      return next;
    });
    setNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function revert() {
    setForm(cloneRules(baseline));
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving) {
      return;
    }
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
    if (!version) {
      setError(
        "The config version is unavailable. Reload the page and try again."
      );
      return;
    }
    const cleaned: Rules = {
      allow: form.allow.map((value) => value.trim()).filter(Boolean),
      deny: form.deny.map((value) => value.trim()).filter(Boolean),
    };
    setSaving(true);
    try {
      const result = await updateAgent(name, { rules: cleaned }, version);
      setForm(cloneRules(cleaned));
      setBaseline(cloneRules(cleaned));
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setNotice("Changes saved. Restart the daemon for them to take effect.");
    } catch (failure) {
      if (isErrorCode(failure, "stale_version")) {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload the rules to get the latest version, then save again."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
    } finally {
      setSaving(false);
    }
  }

  if (status === "loading") {
    return (
      <div className="agent-tabs-state">
        <Loading label="Loading rules…" />
      </div>
    );
  }

  if (status === "error") {
    return (
      <div className="agent-tabs-state">
        <div role="alert">
          <ErrorNotice message={error} details={details} />
        </div>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={reload}
        >
          Try again
        </button>
      </div>
    );
  }

  return (
    <form className="agent-rules-form" onSubmit={save} noValidate>
      {RULE_SECTIONS.map((section) => (
        <section
          key={section.kind}
          className="agent-form-section"
          aria-labelledby={"agent-rules-" + section.kind + "-title"}
        >
          <div className="agent-form-copy">
            <h2 id={"agent-rules-" + section.kind + "-title"}>
              {section.title}
            </h2>
            <p>{section.description}</p>
          </div>
          <div className="agent-form-controls">
            {form[section.kind].length === 0 ? (
              <p className="agent-rules-empty">{section.empty}</p>
            ) : null}
            {form[section.kind].map((value, index) => {
              const inputId = "agent-rule-" + section.kind + "-" + index;
              return (
                <div
                  className="agent-rules-row"
                  key={section.kind + "-" + index}
                >
                  <label className="sr-only" htmlFor={inputId}>
                    {section.title} rule {index + 1}
                  </label>
                  <input
                    id={inputId}
                    className="agent-rules-input"
                    type="text"
                    value={value}
                    placeholder={
                      section.kind === "allow"
                        ? "bash(go test*)"
                        : "bash(rm -rf*)"
                    }
                    autoComplete="off"
                    spellCheck={false}
                    disabled={saving}
                    onChange={(event) =>
                      updateRule(section.kind, index, event.target.value)
                    }
                  />
                  <button
                    type="button"
                    className="agent-tabs-icon-button"
                    aria-label={
                      "Remove " +
                      section.title.toLowerCase() +
                      " rule " +
                      (index + 1)
                    }
                    disabled={saving}
                    onClick={() => removeRule(section.kind, index)}
                  >
                    <Ic name="x" size={14} />
                  </button>
                </div>
              );
            })}
            <button
              type="button"
              className="btn btn-ghost btn-sm agent-rules-add"
              disabled={saving}
              onClick={() => addRule(section.kind)}
            >
              <Ic name="plus" size={13} />
              {section.add}
            </button>
          </div>
        </section>
      ))}

      <div className="agent-tabs-live" role="status" aria-live="polite">
        {notice ? (
          <p className="notice notice-warn agent-tabs-notice">
            {notice}{" "}
            <a href="#/settings/general">
              Restart the daemon in Settings › General
            </a>
            .
          </p>
        ) : null}
      </div>

      {error ? (
        <div className="agent-tabs-error" role="alert">
          <ErrorNotice message={error} details={details} />
          {stale ? (
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={reload}
            >
              Reload rules
            </button>
          ) : null}
        </div>
      ) : null}

      <div className="agent-save-bar">
        <p className="agent-save-note">
          Rules apply after a daemon restart. Write one pattern per row.
        </p>
        <div className="agent-save-actions">
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            disabled={!dirty || saving}
            onClick={revert}
          >
            Revert
          </button>
          <button
            type="submit"
            className="btn btn-primary btn-sm"
            disabled={!dirty || saving}
          >
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </form>
  );
}

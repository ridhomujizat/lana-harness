import { html, useState, useEffect } from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
import { ErrorNotice, Loading } from "/assets/ui.js";
import { Icon } from "/assets/views/agent-common.js";
import {
  loadAgentTab,
  updateAgent,
  isErrorCode,
} from "/assets/api/agent-tabs.js";

/* REQ-WEB-047: the allow and deny pattern lists. Each row is one pattern;
   saving writes rules as {"allow": [...], "deny": [...]}. */

function normalizeRules(value) {
  const source = value || {};
  return {
    allow: Array.isArray(source.allow) ? source.allow.map(String) : [],
    deny: Array.isArray(source.deny) ? source.deny.map(String) : [],
  };
}

function cloneRules(value) {
  return {
    allow: value.allow.slice(),
    deny: value.deny.slice(),
  };
}

const RULE_SECTIONS = [
  {
    kind: "allow",
    title: "Allow",
    description:
      "Patterns that skip the approval step. One pattern per row, for example bash(go test*).",
    empty: "No allow rules.",
    add: "+ Add allow rule",
  },
  {
    kind: "deny",
    title: "Deny",
    description:
      "Patterns that are always refused. One pattern per row, for example bash(rm -rf*).",
    empty: "No deny rules.",
    add: "+ Add deny rule",
  },
];

export function RulesTab({ name }) {
  const [status, setStatus] = useState("loading");
  const [form, setForm] = useState({ allow: [], deny: [] });
  const [baseline, setBaseline] = useState({ allow: [], deny: [] });
  const [version, setVersion] = useState("");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
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

  function updateRule(kind, index, value) {
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

  function addRule(kind) {
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

  function removeRule(kind, index) {
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

  async function save(event) {
    event.preventDefault();
    if (saving) {
      return;
    }
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
    if (!version) {
      setError("The config version is unavailable. Reload the page and try again.");
      return;
    }
    const cleaned = {
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
    return html`<div class="agent-tabs-state">
      <${Loading} label="Loading rules…" />
    </div>`;
  }

  if (status === "error") {
    return html`<div class="agent-tabs-state">
      <div role="alert">
        <${ErrorNotice} message=${error} details=${details} />
      </div>
      <button type="button" class="button-ui button-ui-secondary" onClick=${reload}>
        Try again
      </button>
    </div>`;
  }

  return html`<form class="agent-rules-form" onSubmit=${save} noValidate>
    ${RULE_SECTIONS.map((section) => html`<section
      key=${section.kind}
      class="agent-form-section"
      aria-labelledby=${"agent-rules-" + section.kind + "-title"}
    >
      <div class="agent-form-copy">
        <h2 id=${"agent-rules-" + section.kind + "-title"}>${section.title}</h2>
        <p>${section.description}</p>
      </div>
      <div class="agent-form-controls">
        ${form[section.kind].length === 0
          ? html`<p class="agent-rules-empty">${section.empty}</p>`
          : null}
        ${form[section.kind].map((value, index) => {
          const inputId = "agent-rule-" + section.kind + "-" + index;
          return html`<div class="agent-rules-row" key=${section.kind + "-" + index}>
            <label class="sr-only" for=${inputId}>
              ${section.title} rule ${index + 1}
            </label>
            <input
              id=${inputId}
              class="agent-rules-input"
              type="text"
              value=${value}
              placeholder=${section.kind === "allow" ? "bash(go test*)" : "bash(rm -rf*)"}
              autocomplete="off"
              spellcheck="false"
              disabled=${saving}
              onInput=${(event) =>
                updateRule(section.kind, index, event.target.value)}
            />
            <button
              type="button"
              class="agent-tabs-icon-button"
              aria-label=${"Remove " + section.title.toLowerCase() + " rule " + (index + 1)}
              disabled=${saving}
              onClick=${() => removeRule(section.kind, index)}
            >
              <${Icon} name="close" className="icon-16" />
            </button>
          </div>`;
        })}
        <button
          type="button"
          class="agent-rules-add"
          disabled=${saving}
          onClick=${() => addRule(section.kind)}
        >${section.add}</button>
      </div>
    </section>`)}

    <div class="agent-tabs-live" role="status" aria-live="polite">
      ${notice
        ? html`<p class="notice notice-warn agent-tabs-notice">
            ${notice}
            <a href="#/settings/general">Restart the daemon in Settings › General</a>.
          </p>`
        : null}
    </div>

    ${error
      ? html`<div class="agent-tabs-error" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          ${stale
            ? html`<button
                type="button"
                class="button-ui button-ui-secondary"
                onClick=${reload}
              >Reload rules</button>`
            : null}
        </div>`
      : null}

    <div class="agent-save-bar">
      <p class="agent-save-note">
        Rules apply after a daemon restart. Write one pattern per row.
      </p>
      <div class="agent-save-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${!dirty || saving}
          onClick=${revert}
        >Revert</button>
        <button
          type="submit"
          class="button-ui button-ui-primary"
          disabled=${!dirty || saving}
        >${saving ? "Saving…" : "Save"}</button>
      </div>
    </div>
  </form>`;
}

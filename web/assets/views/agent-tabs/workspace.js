import { html, useState, useEffect } from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
import { ErrorNotice, Loading } from "/assets/ui.js";
import {
  loadAgentTab,
  updateAgent,
  isErrorCode,
} from "/assets/api/agent-tabs.js";

/* REQ-WEB-047: the workspace path. An empty value is allowed and leaves
   workspace unset in the agent table. */

export function WorkspaceTab({ name }) {
  const [status, setStatus] = useState("loading");
  const [value, setValue] = useState("");
  const [baseline, setBaseline] = useState("");
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
        const workspace = config.agent.workspace
          ? String(config.agent.workspace)
          : config.row.workspace
          ? String(config.row.workspace)
          : "";
        setValue(workspace);
        setBaseline(workspace);
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

  const dirty = value !== baseline;

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function updateValue(next) {
    setValue(next);
    setNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function revert() {
    setValue(baseline);
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
    const next = value.trim();
    setSaving(true);
    try {
      const result = await updateAgent(name, { workspace: next }, version);
      setValue(next);
      setBaseline(next);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setNotice(
        next
          ? "Workspace saved. Restart the daemon for the change to take effect."
          : "Workspace cleared. Restart the daemon for the change to take effect."
      );
    } catch (failure) {
      if (isErrorCode(failure, "stale_version")) {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload the workspace to get the latest version, then save again."
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
      <${Loading} label="Loading workspace…" />
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

  return html`<form class="agent-workspace-form" onSubmit=${save} noValidate>
    <section class="agent-form-section" aria-labelledby="agent-workspace-title">
      <div class="agent-form-copy">
        <h2 id="agent-workspace-title">Workspace</h2>
        <p>
          The folder this agent works in. Leave empty to use the daemon's
          default workspace.
        </p>
      </div>
      <div class="agent-form-controls">
        <label class="agent-form-field" for="agent-workspace-input">
          <span class="agent-form-field-label">Workspace path</span>
          <input
            id="agent-workspace-input"
            class="agent-workspace-input"
            type="text"
            value=${value}
            placeholder="~/code/project"
            autocomplete="off"
            spellcheck="false"
            disabled=${saving}
            onInput=${(event) => updateValue(event.target.value)}
          />
        </label>
        <p class="hint">
          An absolute path or a path starting with ~. Empty leaves workspace
          unset.
        </p>
      </div>
    </section>

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
              >Reload workspace</button>`
            : null}
        </div>`
      : null}

    <div class="agent-save-bar">
      <p class="agent-save-note">
        The workspace applies after a daemon restart.
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

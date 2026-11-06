/* eslint-disable react-hooks/set-state-in-effect -- the legacy tab resets its load state before each request, as in workspace.js */
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, errorMessage } from "../../api/client";
import { isErrorCode, loadAgentTab, updateAgent } from "../../api/agents";
import { ErrorNotice, Loading } from "../../ui/ui";

/* REQ-WEB-047: the workspace path. An empty value is allowed and leaves
   workspace unset in the agent table. */

export interface WorkspaceTabProps {
  name: string;
}

export function WorkspaceTab({ name }: WorkspaceTabProps) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [value, setValue] = useState("");
  const [baseline, setBaseline] = useState("");
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

  function updateValue(next: string) {
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
    return (
      <div className="agent-tabs-state">
        <Loading label="Loading workspace…" />
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
    <form className="agent-workspace-form" onSubmit={save} noValidate>
      <section
        className="agent-form-section"
        aria-labelledby="agent-workspace-title"
      >
        <div className="agent-form-copy">
          <h2 id="agent-workspace-title">Workspace</h2>
          <p>
            The folder this agent works in. Leave empty to use the daemon's
            default workspace.
          </p>
        </div>
        <div className="agent-form-controls">
          <label className="agent-form-field" htmlFor="agent-workspace-input">
            <span className="agent-form-field-label">Workspace path</span>
            <input
              id="agent-workspace-input"
              className="agent-workspace-input"
              type="text"
              value={value}
              placeholder="~/code/project"
              autoComplete="off"
              spellCheck={false}
              disabled={saving}
              onChange={(event) => updateValue(event.target.value)}
            />
          </label>
          <p className="hint">
            An absolute path or a path starting with ~. Empty leaves workspace
            unset.
          </p>
        </div>
      </section>

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
              Reload workspace
            </button>
          ) : null}
        </div>
      ) : null}

      <div className="agent-save-bar">
        <p className="agent-save-note">
          The workspace applies after a daemon restart.
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

import { useEffect, useState } from "react";

import { errorMessage } from "../api/client";
import { getAgentHook, setAgentHook, type AgentHookFolder, type AgentHookState } from "../api/workbench";
import { Button, Skeleton, Toggle } from "../ui/gr8r";

/* Spec 69 / spec 77 REQ-WRA-009: the Workbench hooks Lana adds to Claude Code
   and Antigravity (SessionStart and the pre-edit hook), so an agent knows it
   runs in a Workbench pane and reports the worktree its edits land in. The
   switch lives in Settings > General; the Workbench keeps its install notice
   and the Accounts popup a one-line summary that links here. */

export const AGENT_HOOK_TITLE = "Connect Claude Code and Antigravity to the Workbench";
export const AGENT_HOOK_TEXT = "Lana's hooks in Claude Code and Antigravity, so the Workbench can follow what they work on.";
export const AGENT_HOOK_SETTINGS_HREF = "#/settings/general";

/* Spec 77 REQ-WRA-009: "Installed in 3 of 3 folders (Claude 2, Antigravity 1)".
   A kind with no folder is left out of the brackets. */
export function agentHookCount(folders: AgentHookFolder[]): string {
  const installed = folders.filter((f) => f.installed).length;
  const agy = folders.filter((f) => f.kind === "antigravity").length;
  const parts: string[] = [];
  if (folders.length - agy > 0) parts.push("Claude " + (folders.length - agy));
  if (agy > 0) parts.push("Antigravity " + agy);
  return (
    "Installed in " + installed + " of " + folders.length + " folders" + (parts.length ? " (" + parts.join(", ") + ")" : "") + "."
  );
}

/* The Accounts popup footer: "Hooks: 2/3 folders", or "Hooks off". */
export function agentHookSummary(state: AgentHookState): string {
  if (!state.enabled) return "Hooks off";
  return "Hooks: " + state.folders.filter((f) => f.installed).length + "/" + state.folders.length + " folders";
}

/* Settings > General > Workbench: the hook switch with the folder count, and
   Install while a folder lacks the hooks (the Workbench notice's action, so
   the owner can install here after dismissing the notice there). A folder
   that could not be updated is listed under the row. */
export function SettingsWorkbenchSection() {
  const [state, setState] = useState<AgentHookState | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    getAgentHook()
      .then((s) => !cancelled && setState(s))
      .catch((err) => !cancelled && setLoadError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, []);

  const write = (enabled: boolean) => {
    setBusy(true);
    setError("");
    setAgentHook(enabled)
      .then(setState)
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setBusy(false));
  };

  let body;
  if (!state) {
    body = loadError ? (
      <p className="settings-update-error" role="alert">
        {loadError}
      </p>
    ) : (
      <div className="settings-loading-inline" aria-busy="true">
        <span className="sr">Loading the Workbench hooks…</span>
        <Skeleton width={200} height={12} />
      </div>
    );
  } else {
    const broken = state.folders.filter((f) => f.error);
    body = (
      <>
        <div className="srow">
          <div>
            <div className="t">{AGENT_HOOK_TITLE}</div>
            <div className="d">
              {AGENT_HOOK_TEXT}
              {state.enabled ? " " + agentHookCount(state.folders) : ""}
            </div>
          </div>
          <div className="settings-update-control">
            {state.needs_install ? (
              <Button variant="secondary" disabled={busy} loading={busy} onClick={() => write(true)}>
                {busy ? "Installing…" : "Install"}
              </Button>
            ) : null}
            <Toggle aria-label={AGENT_HOOK_TITLE} checked={state.enabled} disabled={busy} onChange={write} />
          </div>
        </div>
        {broken.length ? (
          <div className="settings-hook-broken" role="status">
            <p className="settings-update-error">Some agent folders could not be updated</p>
            {broken.map((f) => (
              <p key={f.dir} className="settings-update-error mono">
                {f.dir}: {f.error}
              </p>
            ))}
          </div>
        ) : null}
        {error ? (
          <p className="settings-update-error" role="alert">
            {error}
          </p>
        ) : null}
      </>
    );
  }

  return (
    <section className="sblock" aria-labelledby="settings-workbench-title">
      <h2 id="settings-workbench-title">Workbench</h2>
      {body}
    </section>
  );
}

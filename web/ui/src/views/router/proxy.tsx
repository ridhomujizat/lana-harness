/* eslint-disable react-hooks/set-state-in-effect -- each tab loads its data on mount and on a filter change, like the other pages. */
import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import {
  createProxyKey,
  getRouter,
  listProxyKeys,
  revokeProxyKey,
  setProxyEnabled,
} from "../../api/router";
import type { CreatedProxyKey, ProxyKey, RouterOverview } from "../../api/router";
import { Button, EmptyState, ErrorState, Field, IconButton, Input, Modal, Panel, Toggle } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { Alert, StatusLine } from "../settings-pages";
import { CopyButton, agoText, dateLabel, failureOf, isStale } from "./router-common";

/* Router → Proxy (REQ-RTR-015, REQ-RTR-016, API-RTR-014): an OpenAI-compatible
   endpoint other tools can point at, guarded by proxy keys. A new key is
   shown once. */

export const KEY_PLACEHOLDER = "lrk_your_key_here";

export function curlExample(baseUrl: string): string {
  const base = (baseUrl || "http://127.0.0.1:18300/router/v1").replace(/\/+$/, "");
  return [
    "curl " + base + "/chat/completions \\",
    '  -H "Authorization: Bearer ' + KEY_PLACEHOLDER + '" \\',
    '  -H "Content-Type: application/json" \\',
    "  -d '{\"model\": \"coding\", \"stream\": true,",
    "       \"messages\": [{\"role\": \"user\", \"content\": \"Hello\"}]}'",
  ].join("\n");
}

function CreateKeyDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<CreatedProxyKey | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = name.trim();
    if (!value || busy) {
      if (!value) {
        setError("Name the key after the tool that uses it, for example cursor.");
      }
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await createProxyKey(value);
      setCreated(result);
      onCreated();
    } catch (failure) {
      setError(failureOf(failure).message);
    } finally {
      setBusy(false);
    }
  }

  if (created) {
    return (
      <Modal
        title={"Key " + created.name + " created"}
        dismissible
        onClose={onClose}
        footer={
          <>
            <span className="sp" />
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </>
        }
      >
        <div className="alert warn" role="status">
          <Ic name="triangle-alert" size={15} />
          <p className="grow">
            Copy this key now. It is shown only once; lana keeps a hash, not the key. If you lose it,
            revoke it and create a new one.
          </p>
        </div>
        <div className="rt-secret">
          <code className="mono" data-testid="new-proxy-key">
            {created.key}
          </code>
          <CopyButton text={created.key} label="Copy key" />
        </div>
      </Modal>
    );
  }

  return (
    <Modal
      title="Create proxy key"
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="router-key-form" variant="primary" loading={busy} disabled={busy}>
            {busy ? "Creating…" : "Create key"}
          </Button>
        </>
      }
    >
      <form id="router-key-form" className="settings-dialog-form" onSubmit={submit}>
        <Field label="Name" hint="Which tool will use this key. Shown in the key list.">
          <Input
            data-autofocus
            value={name}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            placeholder="cursor"
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Alert tone="danger" message={error} />
      </form>
    </Modal>
  );
}

export function RouterProxy() {
  const [data, setData] = useState<RouterOverview | null>(null);
  const [keys, setKeys] = useState<ProxyKey[] | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [toggling, setToggling] = useState(false);
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState("");
  const [confirmRevoke, setConfirmRevoke] = useState("");

  const load = useCallback(async () => {
    const [routerResult, keyResult] = await Promise.allSettled([getRouter(), listProxyKeys()]);
    if (routerResult.status === "fulfilled") {
      setData(routerResult.value);
    }
    if (keyResult.status === "fulfilled") {
      setKeys(keyResult.value);
    }
    const failed = [routerResult, keyResult].find((result) => result.status === "rejected");
    setError(failed && failed.status === "rejected" ? failureOf(failed.reason).message : "");
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function toggle(enabled: boolean) {
    if (!data || toggling) {
      return;
    }
    setToggling(true);
    setMessage("");
    try {
      const result = await setProxyEnabled(enabled, data.version);
      setData({ ...data, version: result.version || data.version, proxy: { ...data.proxy, enabled } });
      setMessage(
        (enabled ? "Proxy on." : "Proxy off.") +
          (result.restart_required ? " Restart the daemon for it to take effect." : "")
      );
    } catch (failure) {
      setError(
        isStale(failure)
          ? "The config changed since this page loaded. Reload and try again."
          : failureOf(failure).message
      );
    } finally {
      setToggling(false);
    }
  }

  async function revoke(key: ProxyKey) {
    setRevoking(key.id);
    setConfirmRevoke("");
    try {
      await revokeProxyKey(key.id);
      setMessage("Key " + key.name + " revoked. Tools using it stop working now.");
      setKeys((current) => (current || []).filter((entry) => entry.id !== key.id));
    } catch (failure) {
      setError(failureOf(failure).message);
    } finally {
      setRevoking("");
    }
  }

  const enabled = Boolean(data && data.proxy.enabled);
  const baseUrl = data ? data.proxy.base_url : "";

  return (
    <div className="rt-proxy">
      <StatusLine text={message} />
      {error ? (
        <div className="home-alert" role="alert">
          <ErrorState icon="cloud-alert" title="Something went wrong" text={error} />
        </div>
      ) : null}

      <Panel title="OpenAI-compatible endpoint">
        <div className="srow rt-proxy-row">
          <div>
            <span className="t" id="rt-proxy-label">
              Serve the proxy
            </span>
            <div className="d">
              Lets tools such as Cursor or scripts call your models and combos with fallback and
              cooldown. Every request needs a proxy key.
            </div>
          </div>
          <Toggle
            aria-labelledby="rt-proxy-label"
            checked={enabled}
            disabled={!data || toggling}
            onChange={(checked) => void toggle(checked)}
          />
        </div>
        <div className="srow rt-proxy-row">
          <div>
            <span className="t">Base URL</span>
            <div className="d">{enabled ? "Point the tool's OpenAI base URL here." : "Available when the proxy is on."}</div>
          </div>
          <div className="rt-secret">
            <code className="mono">{baseUrl || "—"}</code>
            {baseUrl ? <CopyButton text={baseUrl} label="Copy base URL" /> : null}
          </div>
        </div>
        <details className="home-chart-table rt-curl">
          <summary>Example request</summary>
          <div className="rt-code">
            <pre className="mono">{curlExample(baseUrl)}</pre>
            <CopyButton text={curlExample(baseUrl)} label="Copy example" />
          </div>
          <p className="hint">
            Replace {KEY_PLACEHOLDER} with a key from below and coding with a combo or a
            provider/model address.
          </p>
        </details>
      </Panel>

      <Panel
        title="Proxy keys"
        actions={
          <Button variant="primary" size="sm" icon="plus" onClick={() => setCreating(true)}>
            Create key
          </Button>
        }
      >
        {keys === null ? (
          <div className="home-empty">Loading keys…</div>
        ) : keys.length === 0 ? (
          <EmptyState size="sm" icon="key-round" text="No keys yet. Create one for each tool that calls the proxy." />
        ) : (
          <div className="home-table-wrap">
            <table className="perm-t home-table rt-keys-table">
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Key</th>
                  <th scope="col">Created</th>
                  <th scope="col">Last used</th>
                  <th scope="col">
                    <span className="sr">Revoke</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {keys.map((key) => (
                  <tr key={key.id}>
                    <td>{key.name}</td>
                    <td className="mono">{key.prefix}…</td>
                    <td className="num">{dateLabel(key.created_at)}</td>
                    <td className="num">{key.last_used_at ? agoText(key.last_used_at) : "Never"}</td>
                    <td className="is-num rt-key-acts">
                      {confirmRevoke === key.id ? (
                        <>
                          <Button size="sm" variant="ghost" onClick={() => setConfirmRevoke("")}>
                            Keep
                          </Button>
                          <Button
                            size="sm"
                            variant="danger"
                            loading={revoking === key.id}
                            disabled={revoking === key.id}
                            onClick={() => void revoke(key)}
                          >
                            Revoke
                          </Button>
                        </>
                      ) : (
                        <IconButton
                          icon="trash-2"
                          size="sm"
                          label={"Revoke " + key.name}
                          onClick={() => setConfirmRevoke(key.id)}
                        />
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {creating ? (
        <CreateKeyDialog
          onClose={() => setCreating(false)}
          onCreated={() => {
            void listProxyKeys()
              .then(setKeys)
              .catch(() => {});
          }}
        />
      ) : null}
    </div>
  );
}

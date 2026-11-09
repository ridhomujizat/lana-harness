/* eslint-disable react-hooks/set-state-in-effect -- each tab loads its data on mount and on a filter change, like the other pages. */
import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { COMBOS_GROUP, deleteCombo, getRouter, saveCombo } from "../../api/router";
import type { RouterCombo, RouterOverview, RouterProvider, ValidationIssue } from "../../api/router";
import {
  Button,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  Input,
  Modal,
  Select,
  Textarea,
} from "../../ui/gr8r";
import type { SelectOption } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { Alert, StatusLine } from "../settings-pages";
import { UsedBy } from "./providers";
import { ProviderLogo, ProviderMark, providerIndex } from "./provider-logo";
import { ACCOUNTS_TERMS_NOTE, claudeAccountNames, groupClaudeChoices, isClaudeChoice, useClaudeAccounts } from "./claude-code";
import { failureOf, inUseText, isStale } from "./router-common";

/* Router → Combos (REQ-RTR-004, REQ-RTR-014, API-RTR-007), laid out like
   9router's Combos page: one card per combo with its models as an ordered
   chain of chips. The first model that answers serves the call. */

/* The provider of a "provider/model" address. */
export function addressProvider(address: string): string {
  const index = address.search(/[/:]/);
  return index > 0 ? address.slice(0, index) : "";
}

function AddressChip({
  address,
  providers,
}: {
  address: string;
  providers: Record<string, RouterProvider>;
}) {
  const provider = addressProvider(address);
  return (
    <span className="rt-chip" title={address}>
      <ProviderMark provider={providers[provider]} name={provider || address} size={16} />
      <span className="mono trunc">{address}</span>
    </span>
  );
}

const COMBO_NAME_RE = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const MAX_MODELS = 10;

/* 422 issues carry a path such as "models[1]" or "combos.coding.models[1]". */
function issueIndex(path: string): number {
  const match = /models\[(\d+)\]/.exec(path || "");
  return match ? Number(match[1]) : -1;
}

function asIssues(details: unknown[]): ValidationIssue[] {
  return details
    .filter((detail): detail is Record<string, unknown> => typeof detail === "object" && detail !== null)
    .map((detail) => ({
      path: String(detail.path || detail.key || detail.field || ""),
      message: String(detail.message || ""),
    }));
}

interface ComboDialogProps {
  combo: RouterCombo | null;
  version: string;
  addresses: SelectOption[];
  providers?: Record<string, RouterProvider>;
  /* The Claude Code account names, for the several-accounts note
     (REQ-CCT-010). */
  claudeAccounts?: string[];
  takenNames: string[];
  onClose: () => void;
  onSaved: (text: string) => void;
}

export function ComboDialog({
  combo,
  version,
  addresses,
  providers = {},
  claudeAccounts = [],
  takenNames,
  onClose,
  onSaved,
}: ComboDialogProps) {
  const editing = Boolean(combo);
  const [name, setName] = useState(combo ? combo.name : "");
  const [description, setDescription] = useState(combo ? combo.description : "");
  const [models, setModels] = useState<string[]>(combo ? combo.models.slice() : []);
  const [pick, setPick] = useState("");
  const [custom, setCustom] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const [issues, setIssues] = useState<ValidationIssue[]>([]);

  const available = addresses.filter((entry) => !models.includes(entry.value));
  const full = models.length >= MAX_MODELS;
  /* REQ-CCT-010: lana never rotates accounts; a combo of several Claude
     Code accounts gets the terms note. */
  const claudeInCombo = new Set(
    models.map((address) => addressProvider(address)).filter((name) => claudeAccounts.includes(name))
  );

  function add(value: string) {
    const address = value.trim();
    if (!address || models.includes(address) || full) {
      return;
    }
    setModels((current) => current.concat(address));
    setIssues([]);
  }

  function move(index: number, delta: number) {
    setModels((current) => {
      const next = current.slice();
      const target = index + delta;
      if (target < 0 || target >= next.length) {
        return current;
      }
      const [item] = next.splice(index, 1);
      next.splice(target, 0, item as string);
      return next;
    });
    setIssues([]);
  }

  function remove(index: number) {
    setModels((current) => current.filter((_value, position) => position !== index));
    setIssues([]);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!editing && !COMBO_NAME_RE.test(value)) {
      setError("The name starts with a lowercase letter or digit, then letters, digits, dashes, or underscores.");
      return;
    }
    if (!editing && takenNames.includes(value)) {
      setError("A provider or combo named " + value + " already exists. Pick another name.");
      return;
    }
    if (models.length === 0) {
      setError("Add at least one model.");
      return;
    }
    setBusy(true);
    setError("");
    setIssues([]);
    try {
      await saveCombo(value, { description: description.trim(), models, expected_version: version });
      onSaved("Combo " + value + " saved. It applies now.");
    } catch (failure) {
      const info = failureOf(failure);
      if (isStale(failure)) {
        setError("The config changed since this list loaded. Close the dialog and reload before saving.");
      } else if (info.status === 422) {
        setError(info.message || "Fix the marked models.");
        setIssues(asIssues(info.details));
      } else {
        setError(info.message);
      }
    } finally {
      setBusy(false);
    }
  }

  async function removeCombo() {
    if (!combo || busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await deleteCombo(combo.name, version);
      onSaved("Combo " + combo.name + " deleted.");
    } catch (failure) {
      const inUse = inUseText(failure, combo.name);
      setConfirmDelete(false);
      if (inUse) {
        setError(inUse);
      } else if (isStale(failure)) {
        setError("The config changed since this list loaded. Close the dialog and reload before deleting.");
      } else {
        setError(failureOf(failure).message);
      }
    } finally {
      setBusy(false);
    }
  }

  const rowIssues = (index: number) => issues.filter((issue) => issueIndex(issue.path) === index);
  const otherIssues = issues.filter((issue) => issueIndex(issue.path) < 0);

  return (
    <Modal
      title={editing ? "Edit combo " + (combo ? combo.name : "") : "New combo"}
      subtitle="The router tries the models in order. When one fails or is cooling down, the next one answers."
      dismissible={!busy}
      onClose={onClose}
      footer={
        confirmDelete ? (
          <>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <span className="sp" />
            <Button variant="danger" loading={busy} disabled={busy} onClick={() => void removeCombo()}>
              {busy ? "Deleting…" : "Delete combo"}
            </Button>
          </>
        ) : (
          <>
            {editing ? (
              <Button variant="danger-ghost" disabled={busy} onClick={() => setConfirmDelete(true)}>
                Delete combo
              </Button>
            ) : null}
            <span className="sp" />
            <Button variant="secondary" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" form="router-combo-form" variant="primary" loading={busy} disabled={busy}>
              {busy ? "Saving…" : "Save combo"}
            </Button>
          </>
        )
      }
    >
      <form
        id="router-combo-form"
        className="settings-dialog-form"
        onSubmit={confirmDelete ? (event) => event.preventDefault() : submit}
      >
        <Field label="Name" hint="Agents pick the combo by this name.">
          <Input
            data-autofocus={editing ? undefined : true}
            value={name}
            disabled={editing || busy}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="Description (optional)">
          <Textarea
            rows={2}
            value={description}
            disabled={busy}
            placeholder="Claude first, DeepSeek when it is down"
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>

        <div className="field">
          <span className="label" id="rt-combo-models-label">
            Models, in the order they are tried
          </span>
          {models.length === 0 ? (
            <p className="hint">No models yet. Add the first one below.</p>
          ) : (
            <ol className="rt-combo-models" aria-labelledby="rt-combo-models-label">
              {models.map((address, index) => {
                const problems = rowIssues(index);
                return (
                  <li key={address} className={problems.length ? "is-invalid" : undefined}>
                    <span className="rt-combo-n num">{index + 1}</span>
                    <span className="rt-combo-addr" title={address}>
                      <ProviderMark
                        provider={providers[addressProvider(address)]}
                        name={addressProvider(address) || address}
                        size={18}
                      />
                      <span className="mono trunc">{address}</span>
                    </span>
                    <span className="rt-combo-acts">
                      <IconButton
                        icon="arrow-up"
                        size="xs"
                        label={"Move " + address + " up"}
                        disabled={busy || index === 0}
                        onClick={() => move(index, -1)}
                      />
                      <IconButton
                        icon="arrow-down"
                        size="xs"
                        label={"Move " + address + " down"}
                        disabled={busy || index === models.length - 1}
                        onClick={() => move(index, 1)}
                      />
                      <IconButton
                        icon="x"
                        size="xs"
                        label={"Remove " + address}
                        disabled={busy}
                        onClick={() => remove(index)}
                      />
                    </span>
                    {problems.map((issue, position) => (
                      <span key={position} className="err rt-combo-issue" role="alert">
                        <Ic name="circle-alert" size={12} />
                        {issue.message}
                      </span>
                    ))}
                  </li>
                );
              })}
            </ol>
          )}
          <div className="rt-combo-add">
            <Select
              aria-label="Model to add"
              placeholder={available.length ? "Pick a model" : "No more models"}
              options={available}
              value={pick}
              disabled={busy || full || available.length === 0}
              onChange={(value) => {
                setPick("");
                add(value);
              }}
            />
            <div className="rt-combo-custom">
              <Input
                aria-label="Model address"
                className="mono"
                placeholder="provider/model"
                value={custom}
                disabled={busy || full}
                autoComplete="off"
                spellCheck={false}
                onChange={(event) => setCustom(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    add(custom);
                    setCustom("");
                  }
                }}
              />
              <Button
                size="sm"
                disabled={busy || full || !custom.trim()}
                onClick={() => {
                  add(custom);
                  setCustom("");
                }}
              >
                Add
              </Button>
            </div>
          </div>
          {full ? <span className="hint">A combo holds at most {MAX_MODELS} models.</span> : null}
          {claudeInCombo.size > 1 ? (
            <p className="hint rt-cc-terms" role="note">
              {ACCOUNTS_TERMS_NOTE}
            </p>
          ) : null}
        </div>

        <Alert
          tone="danger"
          message={error}
          details={otherIssues.map((issue) => ({ path: issue.path, message: issue.message }))}
        />
        {confirmDelete ? (
          <div className="settings-confirm">
            <p>
              Delete <strong>{combo ? combo.name : ""}</strong>? A combo that an agent uses cannot be
              deleted.
            </p>
          </div>
        ) : null}
      </form>
    </Modal>
  );
}

export function RouterCombos() {
  const [data, setData] = useState<RouterOverview | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [dialog, setDialog] = useState<{ combo: RouterCombo | null } | null>(null);

  const load = useCallback(async () => {
    try {
      setData(await getRouter());
      setError("");
    } catch (failure) {
      setError(failureOf(failure).message);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const index = useMemo(() => providerIndex(data?.providers || []), [data]);
  /* REQ-CCT-009: Claude Code models in one group per account. */
  const { accounts } = useClaudeAccounts();
  const claudeNames = useMemo(
    () => claudeAccountNames(data?.choices || [], data?.providers || [], accounts),
    [data, accounts]
  );
  const addresses = useMemo(
    () =>
      groupClaudeChoices(data?.choices || [], data?.providers || [], accounts)
        .filter((choice) => choice.group !== COMBOS_GROUP)
        .map((choice) => ({
          value: choice.value,
          label:
            choice.label && choice.label !== choice.value
              ? choice.label + " (" + choice.value + ")"
              : choice.value,
          group: choice.group,
          icon: isClaudeChoice(choice, claudeNames) ? (
            <ProviderLogo logo="claude" name="Claude Code" size={16} />
          ) : (
            <ProviderMark provider={index[choice.group]} name={choice.group} size={16} />
          ),
        })),
    [data, index, accounts, claudeNames]
  );
  const combos = data?.combos || [];

  return (
    <div className="rt-combos">
      <div className="rt-ps-h">
        <div className="rt-ps-t">
          <h2>Your combos</h2>
          <p className="muted">
            Point an agent at a combo and the router moves down the list when a model fails or is
            cooling down.
          </p>
        </div>
        <div className="rt-ps-acts">
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            disabled={!data || !data.version}
            onClick={() => setDialog({ combo: null })}
          >
            New combo
          </Button>
        </div>
      </div>
      <StatusLine text={message} />
      {error ? (
        <div className="home-alert" role="alert">
          <ErrorState icon="cloud-alert" title="Could not load combos" text={error} />
        </div>
      ) : null}
      {!data && !error ? <div className="home-empty">Loading combos…</div> : null}
      {data && combos.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="list-ordered"
            title="No combos yet"
            text="Create one to give an agent a backup model when its first choice is down."
          />
        </div>
      ) : null}
      {combos.length > 0 ? (
        <div className="rt-combo-grid" aria-label="Combos">
          {combos.map((combo) => (
            <article key={combo.name} className="rt-combo-card" aria-label={"Combo " + combo.name}>
              <header className="rt-combo-h">
                <span className="rt-combo-icon" aria-hidden="true">
                  <Ic name="layers" size={16} />
                </span>
                <span className="rt-combo-t">
                  <span className="rt-combo-name mono trunc">{combo.name}</span>
                  {combo.description ? <span className="rt-combo-d">{combo.description}</span> : null}
                </span>
                <IconButton
                  icon="pencil"
                  size="sm"
                  label={"Edit " + combo.name}
                  onClick={() => setDialog({ combo })}
                />
              </header>
              <ol className="rt-chain" aria-label={"Models of " + combo.name}>
                {combo.models.map((address, position) => (
                  <li key={address} className="rt-chain-item">
                    <span className="rt-chain-n num" aria-hidden="true">
                      {position + 1}
                    </span>
                    <AddressChip address={address} providers={index} />
                  </li>
                ))}
              </ol>
              <footer className="rt-combo-f">
                <span className="hint">Used by</span>
                <UsedBy usedBy={combo.used_by} />
              </footer>
            </article>
          ))}
        </div>
      ) : null}
      {dialog && data ? (
        <ComboDialog
          combo={dialog.combo}
          version={data.version}
          addresses={addresses}
          providers={index}
          claudeAccounts={claudeNames}
          takenNames={combos.map((combo) => combo.name).concat(data.providers.map((provider) => provider.name))}
          onClose={() => setDialog(null)}
          onSaved={(text) => {
            setDialog(null);
            setMessage(text);
            void load();
          }}
        />
      ) : null}
    </div>
  );
}

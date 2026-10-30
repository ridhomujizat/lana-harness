/* eslint-disable react-hooks/set-state-in-effect -- the legacy views reset
   their loading state inside the load effect; the port keeps that 1:1. */
/* eslint-disable react-hooks/immutability -- routing is a window.location
   hash assignment, as in the legacy views. */
import { useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent, FormEvent, MouseEvent } from "react";
import { errorMessage, ApiError } from "../api/client";
import { Dialog, ErrorNotice, Loading } from "../ui/ui";
import { AgentAvatar, Icon, splitModel } from "./agent-common";
import {
  listMcpServers,
  saveMcpServer,
  testMcpServer,
  restartMcpServer,
  listAgents,
  saveAgentMcp,
} from "../api/mcp";
import type {
  McpDefinition,
  McpServerRow,
  McpToolWire,
} from "../api/mcp";
import type { Agent } from "../api/agents";

/* T-032: the MCP list (REQ-WEB-050), the MCP detail page, and the add-server
   dialog with the inline test (REQ-WEB-051). The daemon never returns env or
   header values; "***" is the mask that keeps a stored value on save. */

const PAGE_SIZE = 8;

const TYPE_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: "command", label: "Command" },
  { value: "url", label: "URL" },
];

const STATUS_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: "", label: "All status" },
  { value: "connected", label: "Connected" },
  { value: "unreachable", label: "Unreachable" },
  { value: "idle", label: "Idle" },
];

interface KeyValueRow {
  key: string;
  value: string;
}

type KeyValueKind = "env" | "headers";

type TextField = "type" | "command" | "args" | "url";

interface FormValue {
  type: string;
  command: string;
  args: string;
  url: string;
  env: KeyValueRow[];
  headers: KeyValueRow[];
}

interface ToolRow {
  name: string;
  description: string;
}

interface RestartStatus {
  tone: string;
  text: string;
}

interface TestResult {
  ok: boolean;
  message: string;
}

type LoadStatus = "loading" | "ready" | "error" | "not-found";

type McpStatus = "connected" | "unreachable" | "idle";

type AgentRow = Agent | { name: string; mcp: string[] };

function countLabel(
  value: number | null | undefined,
  singular: string,
  plural?: string
): string {
  const number = Number(value) || 0;
  return number + " " + (number === 1 ? singular : plural || singular + "s");
}

function formatDuration(milliseconds: number | null | undefined): string {
  const value = Number(milliseconds);
  if (!Number.isFinite(value) || value < 0) {
    return "0 s";
  }
  const seconds = Math.floor(value / 1000);
  if (seconds < 60) {
    return seconds + " s";
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return minutes + " min";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return hours + " h";
  }
  return Math.floor(hours / 24) + " d";
}

/* The API status is authoritative when it is one of the three real values.
   A reserved value falls back to Idle without showing its label. */
function effectiveStatus(
  server: McpServerRow | null | undefined
): McpStatus {
  const status = server && server.status ? String(server.status) : "";
  if (status === "connected" || status === "unreachable" || status === "idle") {
    return status;
  }
  if (status !== "") {
    /* A reserved or unknown status never leaks its label into the table. */
    return "idle";
  }
  const state = server && server.state ? String(server.state) : "";
  if (state === "running") {
    return "connected";
  }
  if (state === "unavailable") {
    return "unreachable";
  }
  return "idle";
}

function statusMeta(status: McpStatus): { label: string; className: string } {
  if (status === "connected") {
    return { label: "Connected", className: "badge-ui mcp-badge-success" };
  }
  if (status === "unreachable") {
    return { label: "Unreachable", className: "badge-ui badge-ui-error" };
  }
  return { label: "Idle", className: "badge-ui" };
}

function StatusBadge({ server }: { server: McpServerRow }) {
  const meta = statusMeta(effectiveStatus(server));
  return (
    <span className={meta.className}>
      <span className="badge-ui-dot" aria-hidden="true"></span>
      {meta.label}
    </span>
  );
}

function serverType(
  server: McpServerRow | null | undefined
): "url" | "command" {
  if (server && server.type === "url") {
    return "url";
  }
  if (server && server.url) {
    return "url";
  }
  return "command";
}

function typeLabel(type: string): string {
  return type === "url" ? "URL" : "Command";
}

function serverHref(name: string): string {
  return "#/mcp/" + encodeURIComponent(name);
}

function serverTarget(server: McpServerRow): string {
  if (serverType(server) === "url") {
    return server && server.url ? String(server.url) : "";
  }
  const command = server && server.command ? String(server.command) : "";
  const args = server && Array.isArray(server.args) ? server.args : [];
  return [command]
    .concat(args.map((arg) => String(arg)))
    .filter(Boolean)
    .join(" ");
}

function matchesQuery(server: McpServerRow, query: string): boolean {
  if (!query) {
    return true;
  }
  const fields: Array<string | null | undefined> = [
    server.name,
    server.command,
    server.url,
  ];
  if (Array.isArray(server.args)) {
    fields.push(server.args.join(" "));
  }
  return fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

function parseArgs(text: string): string[] {
  if (!text) {
    return [];
  }
  return String(text)
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

function rowsFromKeys(keys: string[] | undefined): KeyValueRow[] {
  if (!Array.isArray(keys)) {
    return [];
  }
  return keys.map((key) => ({ key: String(key), value: "***" }));
}

function formFromServer(
  server: McpServerRow | null | undefined
): FormValue {
  const type = serverType(server);
  return {
    type,
    command: server && server.command ? String(server.command) : "",
    args:
      server && Array.isArray(server.args)
        ? server.args.map(String).join("\n")
        : "",
    url: server && server.url ? String(server.url) : "",
    env: rowsFromKeys(server ? server.env_keys : []),
    headers: rowsFromKeys(server ? server.header_keys : []),
  };
}

function emptyForm(): FormValue {
  return {
    type: "command",
    command: "",
    args: "",
    url: "",
    env: [],
    headers: [],
  };
}

function rowsToObject(
  rows: KeyValueRow[],
  dropMask: boolean
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of Array.isArray(rows) ? rows : []) {
    const key = String(row && row.key ? row.key : "").trim();
    if (!key) {
      continue;
    }
    const value = row && row.value !== undefined ? String(row.value) : "";
    if (dropMask && value === "***") {
      continue;
    }
    out[key] = value;
  }
  return out;
}

function definitionFromForm(
  form: FormValue,
  dropMask: boolean
): McpDefinition {
  const definition: McpDefinition = { type: form.type };
  if (form.type === "command") {
    definition.command = String(form.command || "").trim();
    definition.args = parseArgs(form.args);
    definition.env = rowsToObject(form.env, dropMask);
  } else {
    definition.url = String(form.url || "").trim();
    definition.headers = rowsToObject(form.headers, dropMask);
  }
  return definition;
}

function validHttpUrl(value: string): boolean {
  try {
    const parsed = new URL(String(value || ""));
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      Boolean(parsed.host)
    );
  } catch {
    return false;
  }
}

function validateDefinition(
  definition: McpDefinition | null | undefined
): string {
  if (
    !definition ||
    (definition.type !== "command" && definition.type !== "url")
  ) {
    return "Pick Command or URL.";
  }
  if (definition.type === "command") {
    if (!definition.command) {
      return "Enter the command to run.";
    }
    return "";
  }
  if (!definition.url) {
    return "Enter the server URL.";
  }
  if (!validHttpUrl(definition.url)) {
    return "The URL must be a full http or https address.";
  }
  return "";
}

function toolEntries(
  server: McpServerRow | null | undefined
): ToolRow[] {
  const raw: McpToolWire[] = server
    ? Array.isArray(server.tools)
      ? server.tools
      : Array.isArray(server.tool_names)
        ? server.tool_names
        : []
    : [];
  return raw
    .map((tool): ToolRow | null => {
      if (typeof tool === "string") {
        return { name: tool, description: "" };
      }
      if (!tool || typeof tool !== "object") {
        return null;
      }
      const record = tool as {
        name?: unknown;
        id?: unknown;
        description?: unknown;
      };
      return {
        name: String(record.name || record.id || ""),
        description: String(record.description || ""),
      };
    })
    .filter((tool): tool is ToolRow => Boolean(tool && tool.name));
}

function stateSummary(server: McpServerRow): string {
  const state = server && server.state ? String(server.state) : "";
  const agents =
    server && Array.isArray(server.agents) ? server.agents.length : 0;
  if (state === "running") {
    return (
      "Running for " +
      formatDuration(server.running_for_ms) +
      " · " +
      countLabel(server.tools_count, "tool") +
      " · used by " +
      countLabel(agents, "agent")
    );
  }
  if (state === "unavailable") {
    return "Unavailable" + (server.error ? ": " + server.error : "");
  }
  if (state === "stopped") {
    return "Stopped";
  }
  return "Idle";
}

function restartLine(state: string, error: string): string {
  const name = String(state || "");
  if (name === "running") {
    return "Restarted. The server is connected.";
  }
  if (name === "unavailable") {
    return "Restarted. Unavailable" + (error ? ": " + error : ".");
  }
  if (name === "stopped") {
    return "Restarted. The server is stopped.";
  }
  return "Restarted. State: " + (name || "unknown") + ".";
}

function modelLabel(agent: AgentRow): string {
  const parts = splitModel(agent);
  return parts.model || "Agent default";
}

function withField(form: FormValue, key: TextField, value: string): FormValue {
  const next: FormValue = { ...form };
  next[key] = value;
  return next;
}

function withRows(
  form: FormValue,
  kind: KeyValueKind,
  rows: KeyValueRow[]
): FormValue {
  return kind === "env" ? { ...form, env: rows } : { ...form, headers: rows };
}

interface TypeControlProps {
  idPrefix: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}

function TypeControl({
  idPrefix,
  value,
  disabled,
  onChange,
}: TypeControlProps) {
  return (
    <fieldset className="mcp-segmented" disabled={disabled}>
      <legend className="sr-only">Type</legend>
      {TYPE_OPTIONS.map((option) => (
        <label
          key={option.value}
          className={
            "mcp-segment" + (value === option.value ? " is-selected" : "")
          }
        >
          <input
            className="sr-only"
            type="radio"
            name={idPrefix}
            value={option.value}
            checked={value === option.value}
            disabled={disabled}
            onChange={() => onChange(option.value)}
          />
          <span>{option.label}</span>
        </label>
      ))}
    </fieldset>
  );
}

interface KeyValueRowsProps {
  idPrefix: string;
  kind: KeyValueKind;
  rows: KeyValueRow[];
  disabled: boolean;
  onAdd: () => void;
  onChange: (index: number, field: keyof KeyValueRow, value: string) => void;
  onRemove: (index: number) => void;
}

function KeyValueRows({
  idPrefix,
  kind,
  rows,
  disabled,
  onAdd,
  onChange,
  onRemove,
}: KeyValueRowsProps) {
  const noun = kind === "headers" ? "header" : "variable";
  const group = kind === "headers" ? "Header" : "Environment variable";
  return (
    <div className="mcp-key-list">
      {rows.length === 0 ? (
        <p className="mcp-key-empty">No {noun}s yet.</p>
      ) : null}
      {rows.map((row, index) => {
        const key = String(row.key || "").trim();
        const masked = row.value === "***";
        const removeLabel = "Remove " + noun + (key ? " " + key : "");
        return (
          <div className="mcp-key-row" key={idPrefix + "-" + index}>
            <label className="sr-only" htmlFor={idPrefix + "-key-" + index}>
              {group + " key " + (index + 1)}
            </label>
            <input
              id={idPrefix + "-key-" + index}
              className="mcp-mono-input"
              type="text"
              value={row.key}
              placeholder="KEY"
              autoComplete="off"
              spellCheck={false}
              disabled={disabled}
              onChange={(event: ChangeEvent<HTMLInputElement>) =>
                onChange(index, "key", event.target.value)
              }
            />
            <label className="sr-only" htmlFor={idPrefix + "-value-" + index}>
              {group + " value " + (index + 1)}
            </label>
            <input
              id={idPrefix + "-value-" + index}
              className="mcp-key-value mcp-mono-input"
              type="password"
              value={masked ? "" : row.value}
              placeholder={masked ? "••••••••" : "Value"}
              autoComplete="off"
              spellCheck={false}
              disabled={disabled}
              onChange={(event: ChangeEvent<HTMLInputElement>) =>
                onChange(index, "value", event.target.value)
              }
            />
            <button
              type="button"
              className="mcp-key-remove"
              aria-label={removeLabel}
              disabled={disabled}
              onClick={() => onRemove(index)}
            >
              <Icon name="close" className="icon-16" />
            </button>
          </div>
        );
      })}
      <button
        type="button"
        className="mcp-add-key"
        disabled={disabled}
        onClick={onAdd}
      >
        <Icon name="plus" className="icon-16" />
        <span>{kind === "headers" ? "Add header" : "Add variable"}</span>
      </button>
    </div>
  );
}

interface AddServerDialogProps {
  version: string;
  onClose: () => void;
  onAdded?: (name: string) => void;
}

function AddServerDialog({
  version,
  onClose,
  onAdded,
}: AddServerDialogProps) {
  const [name, setName] = useState("");
  const [form, setForm] = useState<FormValue>(emptyForm);
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [tested, setTested] = useState(false);
  const [testing, setTesting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const testId = "mcp-add-test-result";

  function updateField(key: TextField, value: string) {
    setForm((current) => withField(current, key, value));
    setTestResult(null);
    setTested(false);
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  function updateRow(
    kind: KeyValueKind,
    index: number,
    field: keyof KeyValueRow,
    value: string
  ) {
    setForm((current) => {
      const rows = current[kind].map((row, position) =>
        position === index ? { ...row, [field]: value } : row
      );
      return withRows(current, kind, rows);
    });
    setTestResult(null);
    setTested(false);
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  function addRow(kind: KeyValueKind) {
    setForm((current) =>
      withRows(current, kind, current[kind].concat([{ key: "", value: "" }]))
    );
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  function removeRow(kind: KeyValueKind, index: number) {
    setForm((current) =>
      withRows(
        current,
        kind,
        current[kind].filter((_row, position) => position !== index)
      )
    );
    setTestResult(null);
    setTested(false);
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  async function runTest() {
    if (testing || busy) {
      return;
    }
    const definition = definitionFromForm(form, true);
    const problem = validateDefinition(definition);
    if (problem) {
      setTestResult({ ok: false, message: problem });
      return;
    }
    setTesting(true);
    setTestResult(null);
    try {
      const result = await testMcpServer(definition);
      if (result && result.ok) {
        setTestResult({
          ok: true,
          message:
            "Connected · found " + countLabel(result.tool_count, "tool"),
        });
      } else {
        setTestResult({
          ok: false,
          message:
            (result && result.message) || "The server did not report tools.",
        });
      }
    } catch (failure) {
      setTestResult({ ok: false, message: errorMessage(failure) });
    } finally {
      setTesting(false);
      setTested(true);
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!/^[a-z0-9-]+$/.test(value)) {
      setError(
        "The name must use lowercase letters, digits, and dashes, for example docs."
      );
      setDetails([]);
      return;
    }
    const definition = definitionFromForm(form, true);
    const problem = validateDefinition(definition);
    if (problem) {
      setError(problem);
      setDetails([]);
      return;
    }
    if (!version) {
      setError(
        "The config version is unavailable. Reload the page and try again."
      );
      setDetails([]);
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await saveMcpServer(value, definition, version);
      if (onAdded) {
        onAdded(value);
      }
    } catch (failure) {
      setError(errorMessage(failure));
      setDetails(failure instanceof ApiError ? failure.details : []);
    } finally {
      setBusy(false);
    }
  }

  const testClass =
    "mcp-test-line" +
    (testing
      ? " is-testing"
      : testResult
        ? testResult.ok
          ? " is-ok"
          : " is-error"
        : "");

  return (
    <Dialog
      title="Add MCP server"
      className="mcp-add-dialog"
      dismissible={!busy}
      onClose={onClose}
    >
      <form className="mcp-dialog-form" onSubmit={submit}>
        <p className="mcp-dialog-subtitle">
          Run a local command or connect to a server URL.
        </p>

        <label className="mcp-field">
          <span className="mcp-field-label">Name</span>
          <input
            data-autofocus
            type="text"
            name="name"
            value={name}
            autoComplete="off"
            spellCheck={false}
            pattern="[a-z0-9-]+"
            required
            disabled={busy}
            aria-describedby="mcp-add-name-hint"
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setName(event.target.value);
              setError("");
              setDetails([]);
            }}
          />
          <span id="mcp-add-name-hint" className="hint">
            Lowercase letters, digits, and dashes.
          </span>
        </label>

        <div className="mcp-field">
          <span className="mcp-field-label" aria-hidden="true">
            Type
          </span>
          <TypeControl
            idPrefix="mcp-add-type"
            value={form.type}
            disabled={busy}
            onChange={(value) => updateField("type", value)}
          />
        </div>

        {form.type === "command" ? (
          <div className="mcp-command-fields">
            <label className="mcp-field">
              <span className="mcp-field-label">Command</span>
              <input
                className="mcp-mono-input"
                type="text"
                value={form.command}
                placeholder="npx"
                autoComplete="off"
                spellCheck={false}
                disabled={busy}
                onChange={(event: ChangeEvent<HTMLInputElement>) =>
                  updateField("command", event.target.value)
                }
              />
            </label>
            <label className="mcp-field">
              <span className="mcp-field-label">Arguments</span>
              <textarea
                className="mcp-mono-input"
                rows={3}
                value={form.args}
                placeholder="One argument per line"
                spellCheck={false}
                disabled={busy}
                onChange={(event: ChangeEvent<HTMLTextAreaElement>) =>
                  updateField("args", event.target.value)
                }
              ></textarea>
              <span className="hint">Put each argument on its own line.</span>
            </label>
            <div className="mcp-field">
              <span className="mcp-field-label">Environment</span>
              <KeyValueRows
                idPrefix="mcp-add-env"
                kind="env"
                rows={form.env}
                disabled={busy}
                onAdd={() => addRow("env")}
                onChange={(index, field, value) =>
                  updateRow("env", index, field, value)
                }
                onRemove={(index) => removeRow("env", index)}
              />
            </div>
          </div>
        ) : (
          <div className="mcp-command-fields">
            <label className="mcp-field">
              <span className="mcp-field-label">URL</span>
              <input
                className="mcp-mono-input"
                type="text"
                value={form.url}
                placeholder="Server URL"
                autoComplete="off"
                spellCheck={false}
                disabled={busy}
                onChange={(event: ChangeEvent<HTMLInputElement>) =>
                  updateField("url", event.target.value)
                }
              />
              <span className="hint">Use the full http or https address.</span>
            </label>
            <div className="mcp-field">
              <span className="mcp-field-label">Headers</span>
              <KeyValueRows
                idPrefix="mcp-add-header"
                kind="headers"
                rows={form.headers}
                disabled={busy}
                onAdd={() => addRow("headers")}
                onChange={(index, field, value) =>
                  updateRow("headers", index, field, value)
                }
                onRemove={(index) => removeRow("headers", index)}
              />
            </div>
          </div>
        )}

        <div className="mcp-test-row">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy || testing}
            aria-describedby={testId}
            onClick={runTest}
          >
            {testing ? "Testing…" : tested ? "Test again" : "Test"}
          </button>
          <p
            id={testId}
            className={testClass}
            role="status"
            aria-live="polite"
          >
            {testing
              ? "Testing the server…"
              : testResult
                ? testResult.message
                : ""}
          </p>
        </div>

        {error ? <ErrorNotice message={error} details={details} /> : null}

        <div className="mcp-dialog-footer">
          <p className="mcp-dialog-note">You pick agents after adding it.</p>
          <div className="dialog-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={busy}
              onClick={onClose}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={busy}
            >
              {busy ? "Adding…" : "Add server"}
            </button>
          </div>
        </div>
      </form>
    </Dialog>
  );
}

export function McpPage() {
  const [status, setStatus] = useState<LoadStatus>("loading");
  const [servers, setServers] = useState<McpServerRow[]>([]);
  const [version, setVersion] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("");
  const [page, setPage] = useState(1);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [live, setLive] = useState("");
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    listMcpServers()
      .then((result) => {
        if (!alive) {
          return;
        }
        setServers(
          result && Array.isArray(result.servers) ? result.servers : []
        );
        setVersion(result && result.version ? String(result.version) : "");
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        setLive("Could not load MCP servers. " + errorMessage(failure));
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  useEffect(() => {
    setPage(1);
  }, [query, filter]);

  const search = query.trim().toLowerCase();
  const filtered = useMemo(() => {
    return servers.filter((server) => {
      if (filter && effectiveStatus(server) !== filter) {
        return false;
      }
      return matchesQuery(server, search);
    });
  }, [servers, filter, search]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const currentPage = Math.min(page, pageCount);
  const start = (currentPage - 1) * PAGE_SIZE;
  const rows = filtered.slice(start, start + PAGE_SIZE);
  const unreachable = servers.filter(
    (server) => effectiveStatus(server) === "unreachable"
  ).length;
  const summary =
    countLabel(servers.length, "server") +
    " · " +
    unreachable +
    " unreachable";

  function handleAdded() {
    setDialogOpen(false);
    setLive("Server added");
    setReloadToken((current) => current + 1);
  }

  function openRow(event: MouseEvent<HTMLTableRowElement>, server: McpServerRow) {
    const target = event.target;
    if (
      target instanceof Element &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    window.location.hash = serverHref(server.name);
  }

  return (
    <main id="main" className="page mcp-page">
      <header className="agents-header">
        <div className="agents-heading">
          <h1 className="agents-title">MCP</h1>
          <p className="agents-subtitle">
            {status === "ready"
              ? summary
              : "External tools from Model Context Protocol servers."}
          </p>
        </div>
        <button
          type="button"
          className="button-ui button-ui-primary"
          disabled={status !== "ready" || !version}
          onClick={() => setDialogOpen(true)}
        >
          + Add server
        </button>
      </header>

      <div className="agents-toolbar">
        <div className="sessions-search agents-search">
          <span className="sessions-search-icon">
            <Icon name="search" />
          </span>
          <label className="sr-only" htmlFor="mcp-search-input">
            Search servers
          </label>
          <input
            id="mcp-search-input"
            className="sessions-search-input"
            type="search"
            placeholder="Search servers"
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(event: ChangeEvent<HTMLInputElement>) =>
              setQuery(event.target.value)
            }
          />
        </div>
        <div className="mcp-filter">
          <Icon name="filter" />
          <label className="sr-only" htmlFor="mcp-status-filter">
            Status
          </label>
          <select
            id="mcp-status-filter"
            className="mcp-filter-select"
            value={filter}
            onChange={(event: ChangeEvent<HTMLSelectElement>) =>
              setFilter(event.target.value)
            }
          >
            {STATUS_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </div>
        <span className="agents-toolbar-spacer"></span>
      </div>

      <p
        className={live ? "mcp-live" : "sr-only"}
        role="status"
        aria-live="polite"
      >
        {live}
      </p>

      {status === "loading" ? <Loading label="Loading MCP servers…" /> : null}

      {status === "error" ? (
        <div className="agents-state" role="alert">
          <ErrorNotice message={error} details={details} />
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={() => setReloadToken((current) => current + 1)}
          >
            Try again
          </button>
        </div>
      ) : null}

      {status === "ready" && servers.length === 0 ? (
        <div className="agents-empty">
          <h2 className="agents-empty-title">No MCP servers yet.</h2>
          <p className="agents-empty-text">
            Add a server once, then pick which agents use it.
          </p>
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={!version}
            onClick={() => setDialogOpen(true)}
          >
            + Add server
          </button>
        </div>
      ) : null}

      {status === "ready" && servers.length > 0 && filtered.length === 0 ? (
        <p className="agents-state muted" role="status">
          {query.trim()
            ? "No servers match “" + query.trim() + "”."
            : "No servers match this status filter."}
        </p>
      ) : null}

      {status === "ready" && rows.length > 0 ? (
        <div className="mcp-table-card">
          <div className="mcp-table-scroll">
            <table className="agents-table mcp-table">
              <caption className="sr-only">MCP servers</caption>
              <thead>
                <tr>
                  <th scope="col">Server</th>
                  <th scope="col">Type</th>
                  <th scope="col">Tools</th>
                  <th scope="col">Agents</th>
                  <th scope="col">Status</th>
                  <th scope="col">
                    <span className="sr-only">Open</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((server) => {
                  const target = serverTarget(server);
                  const agentNames = Array.isArray(server.agents)
                    ? server.agents.map(String)
                    : [];
                  const shown = agentNames.slice(0, 4);
                  return (
                    <tr
                      key={server.name}
                      onClick={(event) => openRow(event, server)}
                    >
                      <td>
                        <span className="mcp-server-cell">
                          <span className="mcp-server-tile" aria-hidden="true">
                            <Icon name="mcp" />
                          </span>
                          <span className="mcp-server-text">
                            <a
                              className="mcp-server-name"
                              href={serverHref(server.name)}
                            >
                              {server.name}
                            </a>
                            <span className="mcp-server-target">
                              {target || "—"}
                            </span>
                          </span>
                        </span>
                      </td>
                      <td>
                        <span className="badge-ui mcp-type-badge">
                          <span className="badge-ui-dot" aria-hidden="true"></span>
                          {typeLabel(serverType(server))}
                        </span>
                      </td>
                      <td className="agents-number">
                        {Number(server.tools_count) || 0}
                      </td>
                      <td>
                        {agentNames.length === 0 ? (
                          <span className="mcp-muted">No agents</span>
                        ) : (
                          <span className="mcp-agent-cell">
                            <span
                              className="agent-avatar-group"
                              aria-hidden="true"
                            >
                              {shown.map((agentName) => (
                                <AgentAvatar
                                  key={agentName}
                                  name={agentName}
                                  size="24"
                                />
                              ))}
                            </span>
                            {agentNames.length > shown.length ? (
                              <span className="mcp-agent-more">
                                +{agentNames.length - shown.length}
                              </span>
                            ) : null}
                            <span className="sr-only">
                              Used by {agentNames.join(", ")}
                            </span>
                          </span>
                        )}
                      </td>
                      <td>
                        <StatusBadge server={server} />
                      </td>
                      <td className="agents-chevron">
                        <Icon name="chevron-right" />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <nav className="mcp-pagination" aria-label="Pagination">
            <p className="mcp-pagination-text">
              Showing {start + 1}–{start + rows.length} of {filtered.length}
            </p>
            <div className="mcp-pagination-actions">
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={currentPage <= 1}
                onClick={() => setPage(Math.max(1, currentPage - 1))}
              >
                Previous
              </button>
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={currentPage >= pageCount}
                onClick={() => setPage(Math.min(pageCount, currentPage + 1))}
              >
                Next
              </button>
            </div>
          </nav>
        </div>
      ) : null}

      {dialogOpen ? (
        <AddServerDialog
          version={version}
          onClose={() => setDialogOpen(false)}
          onAdded={handleAdded}
        />
      ) : null}
    </main>
  );
}

function DetailBreadcrumb({ name }: { name: string }) {
  return (
    <nav className="agent-breadcrumb" aria-label="Breadcrumb">
      <a href="#/mcp">MCP</a>
      <span aria-hidden="true">/</span>
      <span aria-current="page">{name}</span>
    </nav>
  );
}

export interface McpDetailPageProps {
  name: string;
}

export function McpDetailPage({ name }: McpDetailPageProps) {
  const [status, setStatus] = useState<LoadStatus>("loading");
  const [server, setServer] = useState<McpServerRow | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [version, setVersion] = useState("");
  const [form, setForm] = useState<FormValue | null>(null);
  const [baseline, setBaseline] = useState<FormValue | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restartStatus, setRestartStatus] = useState<RestartStatus | null>(
    null
  );
  const [saveNotice, setSaveNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [stale, setStale] = useState(false);
  const [agentBusy, setAgentBusy] = useState("");
  const [agentError, setAgentError] = useState("");
  const [agentDetails, setAgentDetails] = useState<unknown[]>([]);
  const [agentStale, setAgentStale] = useState(false);
  const [agentLive, setAgentLive] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerValue, setPickerValue] = useState("");
  const [showAllTools, setShowAllTools] = useState(false);
  const pickerRef = useRef<HTMLSelectElement>(null);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setServer(null);
    setAgents([]);
    setVersion("");
    setForm(null);
    setBaseline(null);
    setError("");
    setDetails([]);
    setStale(false);
    setSaveNotice("");
    setRestartStatus(null);
    setAgentError("");
    setAgentDetails([]);
    setAgentStale(false);
    setAgentLive("");
    setPickerOpen(false);
    setShowAllTools(false);

    Promise.all([listMcpServers(), listAgents()])
      .then(([mcpPage, agentPage]) => {
        if (!alive) {
          return;
        }
        const rows =
          mcpPage && Array.isArray(mcpPage.servers) ? mcpPage.servers : [];
        const found = rows.find((row) => row.name === name) || null;
        const agentRows =
          agentPage && Array.isArray(agentPage.agents) ? agentPage.agents : [];
        setVersion(mcpPage && mcpPage.version ? String(mcpPage.version) : "");
        setAgents(agentRows);
        setServer(found);
        if (found) {
          const initial = formFromServer(found);
          setForm(initial);
          setBaseline(initial);
        }
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        if (failure instanceof ApiError && failure.status === 404) {
          setStatus("not-found");
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        setRestartStatus({
          tone: "error",
          text: "Could not load the server. " + errorMessage(failure),
        });
      });

    return () => {
      alive = false;
    };
  }, [name, reloadToken]);

  useEffect(() => {
    if (pickerOpen && pickerRef.current) {
      pickerRef.current.focus();
    }
  }, [pickerOpen]);

  const dirty = useMemo(() => {
    if (!form || !baseline) {
      return false;
    }
    return (
      JSON.stringify(definitionFromForm(form, false)) !==
      JSON.stringify(definitionFromForm(baseline, false))
    );
  }, [form, baseline]);

  const availableAgents = useMemo(() => {
    return agents.filter((agent) => {
      const list = Array.isArray(agent.mcp) ? agent.mcp : [];
      return !list.includes(name);
    });
  }, [agents, name]);

  const entries = server ? toolEntries(server) : [];
  const visibleTools = showAllTools ? entries : entries.slice(0, 6);

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function updateField(key: TextField, value: string) {
    setForm((current) =>
      current ? withField(current, key, value) : current
    );
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function updateRow(
    kind: KeyValueKind,
    index: number,
    field: keyof KeyValueRow,
    value: string
  ) {
    setForm((current) => {
      if (!current) {
        return current;
      }
      const rows = current[kind].map((row, position) =>
        position === index ? { ...row, [field]: value } : row
      );
      return withRows(current, kind, rows);
    });
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function addRow(kind: KeyValueKind) {
    setForm((current) =>
      current
        ? withRows(current, kind, current[kind].concat([{ key: "", value: "" }]))
        : current
    );
    setSaveNotice("");
  }

  function removeRow(kind: KeyValueKind, index: number) {
    setForm((current) =>
      current
        ? withRows(
            current,
            kind,
            current[kind].filter((_row, position) => position !== index)
          )
        : current
    );
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  async function save(
    event?: FormEvent<HTMLFormElement> | MouseEvent<HTMLButtonElement>
  ) {
    if (event) {
      event.preventDefault();
    }
    if (!form || !baseline || saving) {
      return;
    }
    setSaveNotice("");
    setError("");
    setDetails([]);
    setStale(false);
    const definition = definitionFromForm(form, false);
    const problem = validateDefinition(definition);
    if (problem) {
      setError(problem);
      return;
    }
    if (!version) {
      setError(
        "The config version is unavailable. Reload the page and try again."
      );
      return;
    }
    if (!dirty) {
      setSaveNotice("No changes to save.");
      return;
    }
    setSaving(true);
    try {
      const result = await saveMcpServer(name, definition, version);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setBaseline(form);
      setServer((current) => {
        if (!current) {
          return current;
        }
        const next: McpServerRow = {
          ...current,
          type: definition.type || "",
          command: definition.command || "",
          args: definition.args || [],
          url: definition.url || "",
          env_keys: Object.keys(definition.env || {}),
          header_keys: Object.keys(definition.headers || {}),
        };
        return next;
      });
      if (result && result.restart_required) {
        setSaveNotice("Restart the server to apply changes.");
      } else {
        setSaveNotice("Changes saved.");
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setStale(true);
        setError(
          "This server changed since you opened it. Reload to get the latest version, then save again."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
    } finally {
      setSaving(false);
    }
  }

  async function restart() {
    if (restarting) {
      return;
    }
    setRestarting(true);
    setRestartStatus(null);
    try {
      const result = await restartMcpServer(name);
      const nextState = result && result.state ? String(result.state) : "";
      const nextError = result && result.error ? String(result.error) : "";
      setServer((current) => {
        if (!current) {
          return current;
        }
        const status: string =
          nextState === "running"
            ? "connected"
            : nextState === "unavailable"
              ? "unreachable"
              : "idle";
        return {
          ...current,
          state: nextState || "stopped",
          error: nextError,
          status,
        };
      });
      setRestartStatus({
        tone: nextState === "unavailable" ? "error" : "ok",
        text: restartLine(nextState, nextError),
      });
      try {
        const page = await listMcpServers();
        const rows =
          page && Array.isArray(page.servers) ? page.servers : [];
        const fresh = rows.find((row) => row.name === name) || null;
        if (fresh) {
          setServer(fresh);
        }
        if (page && page.version) {
          setVersion(String(page.version));
        }
      } catch {
        /* The restart result is already shown; a refresh failure is not fatal. */
      }
    } catch (failure) {
      setRestartStatus({
        tone: "error",
        text: "Could not restart the server. " + errorMessage(failure),
      });
    } finally {
      setRestarting(false);
    }
  }

  async function changeAgent(
    agentName: string,
    nextList: string[],
    nextServerAgents: string[],
    message: string
  ) {
    if (agentBusy) {
      return;
    }
    if (!version) {
      setAgentError(
        "The config version is unavailable. Reload the page and try again."
      );
      setAgentDetails([]);
      setAgentStale(false);
      return;
    }
    setAgentBusy(agentName);
    setAgentError("");
    setAgentDetails([]);
    setAgentStale(false);
    setAgentLive("");
    try {
      const result = await saveAgentMcp(agentName, nextList, version);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setAgents((current) =>
        current.map((agent) =>
          agent.name === agentName
            ? { ...agent, mcp: nextList.slice() }
            : agent
        )
      );
      setServer((current) =>
        current ? { ...current, agents: nextServerAgents.slice() } : current
      );
      setAgentLive(
        message +
          (result && result.restart_required
            ? " Restart the daemon to apply the change."
            : "")
      );
      setPickerOpen(false);
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setAgentStale(true);
        setAgentError(
          "The configuration changed since you opened it. Reload to get the latest version, then try again."
        );
      } else {
        setAgentError(errorMessage(failure));
        setAgentDetails(failure instanceof ApiError ? failure.details : []);
      }
    } finally {
      setAgentBusy("");
    }
  }

  function removeAgent(agent: AgentRow) {
    const list = Array.isArray(agent.mcp) ? agent.mcp : [];
    if (!list.includes(name)) {
      return;
    }
    const nextList = list.filter((serverName) => serverName !== name);
    const nextServerAgents = server
      ? (Array.isArray(server.agents) ? server.agents : []).filter(
          (agentName) => agentName !== agent.name
        )
      : [];
    changeAgent(
      agent.name,
      nextList,
      nextServerAgents,
      "Removed " + agent.name + " from " + name + "."
    );
  }

  function openPicker() {
    const first = availableAgents[0];
    if (!first) {
      return;
    }
    setPickerValue(first.name);
    setPickerOpen(true);
  }

  function addAgent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const selected = agents.find((agent) => agent.name === pickerValue);
    if (!selected) {
      return;
    }
    const list = Array.isArray(selected.mcp) ? selected.mcp : [];
    if (list.includes(name)) {
      setPickerOpen(false);
      return;
    }
    const nextServerAgents = server
      ? (Array.isArray(server.agents) ? server.agents : [])
          .concat([selected.name])
          .sort((left, right) => left.localeCompare(right))
      : [selected.name];
    changeAgent(
      selected.name,
      list.concat([name]),
      nextServerAgents,
      "Added " + selected.name + " to " + name + "."
    );
  }

  if (status === "loading") {
    return (
      <main id="main" className="page mcp-detail-page">
        <DetailBreadcrumb name={name} />
        <Loading label="Loading MCP server…" />
      </main>
    );
  }

  if (status === "not-found" || (status === "ready" && !server)) {
    return (
      <main id="main" className="page mcp-detail-page">
        <DetailBreadcrumb name={name} />
        <div className="agents-empty">
          <h1 className="agents-empty-title">Server not found</h1>
          <p className="agents-empty-text">
            No MCP server named <code>{name}</code> is configured.
          </p>
          <a className="button-ui button-ui-secondary" href="#/mcp">
            Back to MCP servers
          </a>
        </div>
      </main>
    );
  }

  if (status === "error" || !form || !server) {
    return (
      <main id="main" className="page mcp-detail-page">
        <DetailBreadcrumb name={name} />
        <div role="alert">
          <ErrorNotice message={error} details={details} />
        </div>
        <div className="agent-state-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={reload}
          >
            Try again
          </button>
          <a className="button-ui button-ui-secondary" href="#/mcp">
            Back to MCP servers
          </a>
        </div>
      </main>
    );
  }

  const serverAgents = Array.isArray(server.agents)
    ? server.agents.map(String)
    : [];
  const toolsCount = Number(server.tools_count) || 0;
  const totalTools = Math.max(toolsCount, entries.length);
  const agentRows: AgentRow[] = serverAgents.map((agentName) => {
    return (
      agents.find((agent) => agent.name === agentName) || {
        name: agentName,
        mcp: [name],
      }
    );
  });

  return (
    <main id="main" className="page mcp-detail-page">
      <DetailBreadcrumb name={name} />

      <header className="mcp-detail-header">
        <div className="mcp-detail-identity">
          <span className="mcp-detail-tile" aria-hidden="true">
            <Icon name="mcp" />
          </span>
          <div className="mcp-detail-heading">
            <div className="mcp-detail-name-row">
              <h1 className="mcp-detail-name">{server.name}</h1>
              <StatusBadge server={server} />
            </div>
            <p className="mcp-detail-meta">{stateSummary(server)}</p>
          </div>
        </div>
        <div className="mcp-detail-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={restarting}
            onClick={restart}
          >
            {restarting ? "Restarting…" : "Restart"}
          </button>
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={!dirty || saving}
            onClick={save}
          >
            {saving ? "Saving…" : "Save changes"}
          </button>
        </div>
      </header>

      <p
        className={
          "mcp-live" +
          (restartStatus && restartStatus.tone === "error" ? " is-error" : "") +
          (restartStatus ? "" : " sr-only")
        }
        role="status"
        aria-live="polite"
      >
        {restartStatus ? restartStatus.text : ""}
      </p>

      <div
        className={saveNotice ? "mcp-save-notice" : "sr-only"}
        role="status"
        aria-live="polite"
      >
        {saveNotice ? (
          <p className="notice notice-ok">{saveNotice}</p>
        ) : null}
      </div>

      {error ? (
        <div className="mcp-error" role="alert">
          <ErrorNotice message={error} details={details} />
          {stale ? (
            <button
              type="button"
              className="button-ui button-ui-secondary"
              onClick={reload}
            >
              Reload settings
            </button>
          ) : null}
        </div>
      ) : null}

      <div className="mcp-detail-grid">
        <form
          id="mcp-connection-form"
          className="mcp-connection"
          aria-labelledby="mcp-connection-title"
          onSubmit={save}
        >
          <h2 className="mcp-section-title" id="mcp-connection-title">
            Connection
          </h2>

          <div className="mcp-field">
            <span className="mcp-field-label" aria-hidden="true">
              Type
            </span>
            <TypeControl
              idPrefix="mcp-detail-type"
              value={form.type}
              disabled={saving}
              onChange={(value) => updateField("type", value)}
            />
          </div>

          {form.type === "command" ? (
            <div className="mcp-command-fields">
              <label className="mcp-field">
                <span className="mcp-field-label">Command</span>
                <input
                  className="mcp-mono-input"
                  type="text"
                  value={form.command}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={saving}
                  onChange={(event: ChangeEvent<HTMLInputElement>) =>
                    updateField("command", event.target.value)
                  }
                />
              </label>
              <label className="mcp-field">
                <span className="mcp-field-label">Arguments</span>
                <textarea
                  className="mcp-mono-input"
                  rows={3}
                  value={form.args}
                  placeholder="One argument per line"
                  spellCheck={false}
                  disabled={saving}
                  onChange={(event: ChangeEvent<HTMLTextAreaElement>) =>
                    updateField("args", event.target.value)
                  }
                ></textarea>
                <span className="hint">Put each argument on its own line.</span>
              </label>
              <div className="mcp-field">
                <span className="mcp-field-label">Environment</span>
                <KeyValueRows
                  idPrefix="mcp-detail-env"
                  kind="env"
                  rows={form.env}
                  disabled={saving}
                  onAdd={() => addRow("env")}
                  onChange={(index, field, value) =>
                    updateRow("env", index, field, value)
                  }
                  onRemove={(index) => removeRow("env", index)}
                />
              </div>
              <p className="hint mcp-connection-note">
                Values are hidden after you save. Use secrets for tokens.
              </p>
            </div>
          ) : (
            <div className="mcp-command-fields">
              <label className="mcp-field">
                <span className="mcp-field-label">URL</span>
                <input
                  className="mcp-mono-input"
                  type="text"
                  value={form.url}
                  autoComplete="off"
                  spellCheck={false}
                  disabled={saving}
                  onChange={(event: ChangeEvent<HTMLInputElement>) =>
                    updateField("url", event.target.value)
                  }
                />
                <span className="hint">Use the full http or https address.</span>
              </label>
              <div className="mcp-field">
                <span className="mcp-field-label">Headers</span>
                <KeyValueRows
                  idPrefix="mcp-detail-header"
                  kind="headers"
                  rows={form.headers}
                  disabled={saving}
                  onAdd={() => addRow("headers")}
                  onChange={(index, field, value) =>
                    updateRow("headers", index, field, value)
                  }
                  onRemove={(index) => removeRow("headers", index)}
                />
              </div>
              <p className="hint mcp-connection-note">
                Values are hidden after you save. Use secrets for tokens.
              </p>
            </div>
          )}
        </form>

        <div className="mcp-detail-side">
          <section className="mcp-section" aria-labelledby="mcp-agents-title">
            <div className="mcp-section-head">
              <h2 className="mcp-section-title" id="mcp-agents-title">
                Agents
              </h2>
              <span className="mcp-section-meta">
                {serverAgents.length} of {agents.length}
              </span>
            </div>

            <div
              className={agentLive ? "mcp-live" : "sr-only"}
              role="status"
              aria-live="polite"
            >
              {agentLive}
            </div>

            {agentError ? (
              <div className="mcp-agent-error" role="alert">
                <ErrorNotice message={agentError} details={agentDetails} />
                {agentStale ? (
                  <button
                    type="button"
                    className="button-ui button-ui-secondary"
                    onClick={reload}
                  >
                    Reload settings
                  </button>
                ) : null}
              </div>
            ) : null}

            <div className="mcp-agent-list">
              {agentRows.length === 0 ? (
                <p className="mcp-agent-empty">
                  No agents use this server yet.
                </p>
              ) : (
                agentRows.map((agent) => (
                  <div className="mcp-agent-row" key={agent.name}>
                    <AgentAvatar name={agent.name} size="28" />
                    <span className="mcp-agent-text">
                      <span className="mcp-agent-name">{agent.name}</span>
                      <span className="mcp-agent-model">
                        {modelLabel(agent)}
                      </span>
                    </span>
                    <button
                      type="button"
                      className="mcp-icon-button"
                      aria-label={"Remove agent " + agent.name}
                      disabled={Boolean(agentBusy)}
                      onClick={() => removeAgent(agent)}
                    >
                      <Icon name="close" className="icon-16" />
                    </button>
                  </div>
                ))
              )}

              {pickerOpen ? (
                <form className="mcp-agent-picker" onSubmit={addAgent}>
                  <label
                    className="sr-only"
                    htmlFor="mcp-agent-picker-select"
                  >
                    Agent
                  </label>
                  <select
                    id="mcp-agent-picker-select"
                    ref={pickerRef}
                    value={pickerValue}
                    disabled={Boolean(agentBusy)}
                    onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                      setPickerValue(event.target.value)
                    }
                  >
                    {availableAgents.map((agent) => (
                      <option key={agent.name} value={agent.name}>
                        {agent.name}
                      </option>
                    ))}
                  </select>
                  <button
                    type="submit"
                    className="button-ui button-ui-primary"
                    disabled={Boolean(agentBusy) || !pickerValue}
                  >
                    Add
                  </button>
                  <button
                    type="button"
                    className="button-ui button-ui-secondary"
                    disabled={Boolean(agentBusy)}
                    onClick={() => setPickerOpen(false)}
                  >
                    Cancel
                  </button>
                </form>
              ) : (
                <button
                  type="button"
                  className="mcp-add-agent"
                  disabled={
                    Boolean(agentBusy) || availableAgents.length === 0
                  }
                  onClick={openPicker}
                >
                  <span className="mcp-add-agent-tile" aria-hidden="true">
                    <Icon name="plus" className="icon-16" />
                  </span>
                  <span>
                    {availableAgents.length === 0
                      ? "Every agent uses this server"
                      : "Add agent"}
                  </span>
                </button>
              )}
            </div>
          </section>

          <section className="mcp-section" aria-labelledby="mcp-tools-title">
            <div className="mcp-section-head">
              <h2 className="mcp-section-title" id="mcp-tools-title">
                Tools
              </h2>
              <span className="mcp-section-meta">
                {toolsCount} from this server
              </span>
            </div>

            {entries.length > 0 ? (
              <div className="mcp-tool-list">
                {visibleTools.map((tool) => (
                  <div className="mcp-tool-row" key={tool.name}>
                    <span className="mcp-tool-name">{tool.name}</span>
                    <span className="mcp-tool-description">
                      {tool.description}
                    </span>
                  </div>
                ))}
                {entries.length > 6 ? (
                  <button
                    type="button"
                    className="mcp-tool-more"
                    onClick={() => setShowAllTools((current) => !current)}
                  >
                    {showAllTools
                      ? "Show fewer tools"
                      : "Show all " + totalTools + " tools"}
                  </button>
                ) : null}
              </div>
            ) : (
              <div className="mcp-tool-list">
                <p className="mcp-tool-empty">
                  This daemon reports the tool count but not the tool names.
                  {" "}
                  {toolsCount === 0
                    ? "No tools were reported yet."
                    : countLabel(toolsCount, "tool") + " reported."}
                </p>
              </div>
            )}

            <p className="mcp-tool-hint">
              Turn tools on or off per agent in the agent's Tools tab.
            </p>
          </section>
        </div>
      </div>
    </main>
  );
}

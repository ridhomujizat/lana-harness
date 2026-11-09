/* eslint-disable react-hooks/set-state-in-effect -- the legacy views reset
   their loading state inside the load effect; the port keeps that 1:1. */
import { useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent, FormEvent, MouseEvent, ReactNode } from "react";
import { errorMessage, ApiError } from "../api/client";
import { detailText } from "../ui/ui";
import { Ic } from "../ui/icons";
import { splitModel } from "./agent-common";
import { displayLabel, useAgentLabel } from "./agent-label";
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
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  Input,
  Lbl,
  Menu,
  MenuItem,
  Modal,
  Panel,
  SearchInput,
  Seg,
  Select,
  Skeleton,
  Tabs,
  Textarea,
} from "../ui/gr8r";
import { AgentAvatar, AgentAvatarStack, agentFallbackColor } from "./agent-avatar";
import type { BadgeTone } from "../ui/gr8r";

/* T-032: the MCP list (REQ-WEB-050), the MCP detail page, and the add-server
   dialog with the inline test (REQ-WEB-051). The daemon never returns env or
   header values; "***" is the mask that keeps a stored value on save. */

const PAGE_SIZE = 8;

const TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "command", label: "Command" },
  { value: "url", label: "URL" },
];

const STATUS_OPTIONS: Array<{ value: string; label: string }> = [
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

function agentColor(name: string): string {
  return agentFallbackColor(name);
}

function statusTone(status: McpStatus): BadgeTone {
  if (status === "connected") {
    return "green";
  }
  if (status === "unreachable") {
    return "red";
  }
  return "gray";
}

function statusLabel(status: McpStatus): string {
  if (status === "connected") {
    return "Connected";
  }
  if (status === "unreachable") {
    return "Unreachable";
  }
  return "Idle";
}

function StatusBadge({ server }: { server: McpServerRow }) {
  const status = effectiveStatus(server);
  return (
    <Badge tone={statusTone(status)} dot>
      {statusLabel(status)}
    </Badge>
  );
}

function DetailsList({ details }: { details: unknown[] }) {
  if (!details || details.length === 0) {
    return null;
  }
  return (
    <ul className="mcp-details">
      {details.map((detail, index) => (
        <li key={index}>{detailText(detail)}</li>
      ))}
    </ul>
  );
}

function ErrorAlert({
  message,
  details,
  action,
}: {
  message: string;
  details?: unknown[];
  action?: ReactNode;
}) {
  return (
    <div className="alert danger" role="alert">
      <Ic name="circle-alert" size={15} />
      <div className="grow">
        <p className="mcp-alert-msg">{message}</p>
        <DetailsList details={details || []} />
      </div>
      {action}
    </div>
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
    <div className="mcp-kv">
      {rows.length === 0 ? (
        <p className="mcp-kv-empty">No {noun}s yet.</p>
      ) : null}
      {rows.map((row, index) => {
        const key = String(row.key || "").trim();
        const masked = row.value === "***";
        const removeLabel = "Remove " + noun + (key ? " " + key : "");
        return (
          <div className="mcp-kv-row" key={idPrefix + "-" + index}>
            <label className="sr" htmlFor={idPrefix + "-key-" + index}>
              {group + " key " + (index + 1)}
            </label>
            <Input
              id={idPrefix + "-key-" + index}
              className="mono"
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
            <label className="sr" htmlFor={idPrefix + "-value-" + index}>
              {group + " value " + (index + 1)}
            </label>
            <Input
              id={idPrefix + "-value-" + index}
              className="mono mcp-kv-value"
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
            <IconButton
              size="sm"
              icon="x"
              label={removeLabel}
              disabled={disabled}
              onClick={() => onRemove(index)}
            />
          </div>
        );
      })}
      <Button
        variant="ghost"
        size="sm"
        icon="plus"
        disabled={disabled}
        onClick={onAdd}
      >
        {kind === "headers" ? "Add header" : "Add variable"}
      </Button>
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
    <Modal
      title="Add MCP server"
      dismissible={!busy}
      onClose={onClose}
      footer={
        <div className="mcp-dialog-footer">
          <p className="mcp-dialog-note">You pick agents after adding it.</p>
          <div className="dialog-actions">
            <Button variant="secondary" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              form="mcp-add-form"
              variant="primary"
              disabled={busy}
              loading={busy}
            >
              {busy ? "Adding…" : "Add server"}
            </Button>
          </div>
        </div>
      }
    >
      <form id="mcp-add-form" className="mcp-dialog-form" onSubmit={submit}>
        <p className="mcp-dialog-subtitle">
          Run a local command or connect to a server URL.
        </p>

        <Field label="Name" hint="Lowercase letters, digits, and dashes.">
          <Input
            data-autofocus
            type="text"
            name="name"
            value={name}
            autoComplete="off"
            spellCheck={false}
            pattern="[a-z0-9-]+"
            required
            disabled={busy}
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setName(event.target.value);
              setError("");
              setDetails([]);
            }}
          />
        </Field>

        <div className="field">
          <span className="label" id="mcp-add-type-label">
            Type
          </span>
          <Seg
            className="mcp-type-seg"
            aria-labelledby="mcp-add-type-label"
            options={TYPE_OPTIONS}
            value={form.type}
            onChange={(value) => {
              if (!busy) {
                updateField("type", value);
              }
            }}
          />
        </div>

        {form.type === "command" ? (
          <div className="mcp-command-fields">
            <Field label="Command">
              <Input
                className="mono"
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
            </Field>
            <Field label="Arguments" hint="Put each argument on its own line.">
              <Textarea
                className="mono"
                rows={3}
                value={form.args}
                placeholder="One argument per line"
                spellCheck={false}
                disabled={busy}
                onChange={(event: ChangeEvent<HTMLTextAreaElement>) =>
                  updateField("args", event.target.value)
                }
              ></Textarea>
            </Field>
            <div className="field">
              <span className="label">Environment</span>
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
            <Field label="URL" hint="Use the full http or https address.">
              <Input
                className="mono"
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
            </Field>
            <div className="field">
              <span className="label">Headers</span>
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

        <div className="mcp-test">
          <Button
            variant="secondary"
            disabled={busy || testing}
            aria-describedby={testId}
            onClick={runTest}
          >
            {testing ? "Testing…" : tested ? "Test again" : "Test"}
          </Button>
          <p id={testId} className={testClass} role="status" aria-live="polite">
            {testing
              ? "Testing the server…"
              : testResult
                ? testResult.message
                : ""}
          </p>
        </div>

        {error ? <ErrorAlert message={error} details={details} /> : null}
      </form>
    </Modal>
  );
}

interface ServerRowProps {
  server: McpServerRow;
  restarting: boolean;
  onRestart: () => void;
  onOpen: (event: MouseEvent<HTMLElement>, server: McpServerRow) => void;
}

/* One server row. The name opens the detail page; the ellipsis opens the row
   actions (open or restart). */
function ServerRow({ server, restarting, onRestart, onOpen }: ServerRowProps) {
  const label = useAgentLabel();
  const moreRef = useRef<HTMLSpanElement>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const target = serverTarget(server);
  const agentNames = Array.isArray(server.agents)
    ? server.agents.map(String)
    : [];
  const shown = agentNames.slice(0, 4);

  return (
    <div className="mini mcp-row" onClick={(event) => onOpen(event, server)}>
      <span className="mcp-tile" aria-hidden="true">
        <Ic name="server" size={16} />
      </span>
      <span className="mcp-row-main">
        <a className="mcp-row-name" href={serverHref(server.name)}>
          {server.name}
        </a>
        <span className="mcp-row-target mono">{target || "—"}</span>
      </span>
      <span className="mcp-row-transport">
        <Lbl color="var(--text-4)">{typeLabel(serverType(server))}</Lbl>
      </span>
      <span className="mcp-row-tools num">
        {Number(server.tools_count) || 0}
      </span>
      <span className="mcp-row-agents">
        {agentNames.length === 0 ? (
          <span className="mcp-muted">No agents</span>
        ) : (
          <>
            <AgentAvatarStack
              size="sm"
              max={4}
              items={shown.map((agentName) => ({
                name: agentName,
                color: agentColor(agentName),
              }))}
            />
            {agentNames.length > shown.length ? (
              <span className="mcp-agent-more">
                +{agentNames.length - shown.length}
              </span>
            ) : null}
            <span className="sr">Used by {agentNames.map((agentName) => label(agentName)).join(", ")}</span>
          </>
        )}
      </span>
      <span className="mcp-row-status">
        <StatusBadge server={server} />
      </span>
      <span className="mcp-row-actions">
        <span className="mcp-row-more" ref={moreRef}>
          <IconButton
            icon="ellipsis"
            size="sm"
            label={"Actions for " + server.name}
            on={menuOpen}
            onMouseDown={(event) => event.stopPropagation()}
            onClick={(event) => {
              event.stopPropagation();
              setMenuOpen((current) => !current);
            }}
          />
          <Menu
            anchorRef={moreRef}
            open={menuOpen}
            onClose={() => setMenuOpen(false)}
          >
            <MenuItem
              icon="arrow-up-right"
              onClick={() => {
                setMenuOpen(false);
                window.location.hash = serverHref(server.name);
              }}
            >
              Open server
            </MenuItem>
            <MenuItem
              icon="refresh-cw"
              disabled={restarting}
              onClick={() => {
                setMenuOpen(false);
                onRestart();
              }}
            >
              Restart server
            </MenuItem>
          </Menu>
        </span>
      </span>
    </div>
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
  const [restarting, setRestarting] = useState("");
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

  function openRow(event: MouseEvent<HTMLElement>, server: McpServerRow) {
    const target = event.target;
    if (
      target instanceof Element &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    window.location.hash = serverHref(server.name);
  }

  async function restartServer(serverName: string) {
    if (restarting) {
      return;
    }
    setRestarting(serverName);
    setLive("");
    try {
      const result = await restartMcpServer(serverName);
      const nextState = result && result.state ? String(result.state) : "";
      const nextError = result && result.error ? String(result.error) : "";
      setServers((current) =>
        current.map((server) =>
          server.name === serverName
            ? {
                ...server,
                state: nextState || "stopped",
                error: nextError,
                status:
                  nextState === "running"
                    ? "connected"
                    : nextState === "unavailable"
                      ? "unreachable"
                      : "idle",
              }
            : server
        )
      );
      setLive(serverName + ". " + restartLine(nextState, nextError));
    } catch (failure) {
      setLive("Could not restart " + serverName + ". " + errorMessage(failure));
    } finally {
      setRestarting("");
    }
  }

  return (
    <main id="main" className="page mcp-page">
      <header className="ph">
        <div>
          <h1>MCP</h1>
          <p>
            {status === "ready"
              ? summary
              : "External tools from Model Context Protocol servers."}
          </p>
        </div>
        <div className="acts">
          <Button
            variant="primary"
            icon="plus"
            disabled={status !== "ready" || !version}
            onClick={() => setDialogOpen(true)}
          >
            Add server
          </Button>
        </div>
      </header>

      <div className="mcp-toolbar">
        <SearchInput
          wrapClassName="mcp-search"
          aria-label="Search servers"
          placeholder="Search servers"
          autoComplete="off"
          spellCheck={false}
          kbd={null}
          value={query}
          onChange={(event: ChangeEvent<HTMLInputElement>) =>
            setQuery(event.target.value)
          }
        />
        <Seg
          className="mcp-status-seg"
          aria-label="Filter by status"
          options={STATUS_OPTIONS}
          value={filter}
          onChange={setFilter}
        />
      </div>

      <p className={live ? "mcp-live" : "sr"} role="status" aria-live="polite">
        {live}
      </p>

      {status === "loading" ? (
        <section className="panel mcp-list" aria-busy="true">
          <span className="sr">Loading MCP servers…</span>
          {Array.from({ length: 3 }).map((_, index) => (
            <div key={index} className="mini mcp-row mcp-row-skeleton">
              <Skeleton width={30} height={30} radius={8} />
              <span className="mcp-row-main">
                <Skeleton width="40%" height={12} />
                <Skeleton width="70%" height={10} />
              </span>
            </div>
          ))}
        </section>
      ) : null}

      {status === "error" ? (
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load MCP servers"
            text={error}
            action={
              <Button
                variant="secondary"
                onClick={() => setReloadToken((current) => current + 1)}
              >
                Try again
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
      ) : null}

      {status === "ready" && servers.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="server"
            title="No MCP servers yet."
            text="Add a server once, then pick which agents use it."
            action={
              <Button
                variant="primary"
                icon="plus"
                disabled={!version}
                onClick={() => setDialogOpen(true)}
              >
                Add server
              </Button>
            }
          />
        </div>
      ) : null}

      {status === "ready" && servers.length > 0 && filtered.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="search-x"
            size="sm"
            title="No servers match"
            text={
              query.trim()
                ? "No servers match “" + query.trim() + "”."
                : "No servers match this status filter."
            }
            action={
              <Button
                variant="secondary"
                onClick={() => {
                  setQuery("");
                  setFilter("");
                }}
              >
                Clear filters
              </Button>
            }
            role="status"
          />
        </div>
      ) : null}

      {status === "ready" && rows.length > 0 ? (
        <section className="panel mcp-list" aria-label="MCP servers">
          <div className="mcp-list-head" aria-hidden="true">
            <span>Server</span>
            <span>Transport</span>
            <span>Tools</span>
            <span>Agents</span>
            <span>Status</span>
            <span></span>
          </div>
          {rows.map((server) => (
            <ServerRow
              key={server.name}
              server={server}
              restarting={restarting === server.name}
              onRestart={() => restartServer(server.name)}
              onOpen={openRow}
            />
          ))}
          <nav className="mcp-pagination" aria-label="Pagination">
            <p className="mcp-pagination-text">
              Showing {start + 1}–{start + rows.length} of {filtered.length}
            </p>
            <div className="mcp-pagination-actions">
              <Button
                size="sm"
                variant="secondary"
                disabled={currentPage <= 1}
                onClick={() => setPage(Math.max(1, currentPage - 1))}
              >
                Previous
              </Button>
              <Button
                size="sm"
                variant="secondary"
                disabled={currentPage >= pageCount}
                onClick={() => setPage(Math.min(pageCount, currentPage + 1))}
              >
                Next
              </Button>
            </div>
          </nav>
        </section>
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

function DetailBack() {
  return (
    <a className="mcp-back" href="#/mcp">
      <Ic name="arrow-left" size={14} />
      MCP
    </a>
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
  const [tab, setTab] = useState("connection");
  const pickerRef = useRef<HTMLFormElement>(null);

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
      "Removed " + displayLabel(agent) + " from " + name + "."
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
      "Added " + displayLabel(selected) + " to " + name + "."
    );
  }

  if (status === "loading") {
    return (
      <main id="main" className="page mcp-detail-page">
        <div className="ph">
          <div>
            <DetailBack />
          </div>
        </div>
        <Panel bodyClassName="mcp-panel-body">
          <div className="mcp-loading" aria-busy="true">
            <span className="sr">Loading MCP server…</span>
            <Skeleton width={180} height={14} />
            <Skeleton width="100%" height={34} radius={6} />
            <Skeleton width="100%" height={34} radius={6} />
            <Skeleton width="100%" height={96} radius={6} />
          </div>
        </Panel>
      </main>
    );
  }

  if (status === "not-found" || (status === "ready" && !server)) {
    return (
      <main id="main" className="page mcp-detail-page">
        <div className="ph">
          <div>
            <DetailBack />
          </div>
        </div>
        <div className="panel">
          <EmptyState
            icon="server-off"
            title="Server not found"
            text={
              <>
                No MCP server named <code>{name}</code> is configured.
              </>
            }
            action={
              <Button as="a" variant="secondary" href="#/mcp">
                Back to MCP servers
              </Button>
            }
          />
        </div>
      </main>
    );
  }

  if (status === "error" || !form || !server) {
    return (
      <main id="main" className="page mcp-detail-page">
        <div className="ph">
          <div>
            <DetailBack />
            <h1>{name}</h1>
          </div>
        </div>
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load the server"
            text={error}
            action={
              <div className="mcp-state-actions">
                <Button variant="secondary" onClick={reload}>
                  Try again
                </Button>
                <Button as="a" variant="secondary" href="#/mcp">
                  Back to MCP servers
                </Button>
              </div>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
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
      <header className="ph">
        <div>
          <DetailBack />
          <h1>{server.name}</h1>
          <p>{stateSummary(server)}</p>
        </div>
        <div className="acts">
          <StatusBadge server={server} />
          <Button
            variant="secondary"
            icon="refresh-cw"
            disabled={restarting}
            onClick={restart}
          >
            {restarting ? "Restarting…" : "Restart"}
          </Button>
          <Button
            variant="primary"
            disabled={!dirty || saving}
            loading={saving}
            onClick={save}
          >
            {saving ? "Saving…" : "Save changes"}
          </Button>
        </div>
      </header>

      <p
        className={
          "mcp-live" +
          (restartStatus && restartStatus.tone === "error" ? " is-error" : "") +
          (restartStatus ? "" : " sr")
        }
        role="status"
        aria-live="polite"
      >
        {restartStatus ? restartStatus.text : ""}
      </p>

      <p
        className={saveNotice ? "mcp-saved" : "sr"}
        role="status"
        aria-live="polite"
      >
        {saveNotice}
      </p>

      {error ? (
        <ErrorAlert
          message={error}
          details={details}
          action={
            stale ? (
              <Button variant="secondary" size="sm" onClick={reload}>
                Reload settings
              </Button>
            ) : null
          }
        />
      ) : null}

      <Tabs
        className="mcp-tabs"
        value={tab}
        onChange={setTab}
        items={[
          { value: "connection", label: "Connection", icon: "plug" },
          { value: "agents", label: "Agents", count: serverAgents.length },
          { value: "tools", label: "Tools", count: toolsCount },
        ]}
      />

      <div className="mcp-tab-body">
        {tab === "connection" ? (
          <div className="mcp-tab-stack">
            <Panel bodyClassName="mcp-panel-body">
              <form id="mcp-connection-form" className="mcp-conn" onSubmit={save}>
                <div className="srow">
                  <div className="mcp-srow-text">
                    <div className="t" id="mcp-detail-type-label">
                      Type
                    </div>
                    <div className="d">How this server is reached.</div>
                  </div>
                  <div className="mcp-ctl">
                    <Seg
                      className="mcp-type-seg"
                      aria-labelledby="mcp-detail-type-label"
                      options={TYPE_OPTIONS}
                      value={form.type}
                      onChange={(value) => {
                        if (!saving) {
                          updateField("type", value);
                        }
                      }}
                    />
                  </div>
                </div>

                {form.type === "command" ? (
                  <>
                    <div className="srow">
                      <div className="mcp-srow-text">
                        <label className="t" htmlFor="mcp-command">
                          Command
                        </label>
                        <div className="d">The executable to run.</div>
                      </div>
                      <div className="mcp-ctl">
                        <Input
                          id="mcp-command"
                          className="mono"
                          type="text"
                          value={form.command}
                          autoComplete="off"
                          spellCheck={false}
                          disabled={saving}
                          onChange={(event: ChangeEvent<HTMLInputElement>) =>
                            updateField("command", event.target.value)
                          }
                        />
                      </div>
                    </div>
                    <div className="srow">
                      <div className="mcp-srow-text">
                        <label className="t" htmlFor="mcp-args">
                          Arguments
                        </label>
                        <div className="d">One argument per line.</div>
                      </div>
                      <div className="mcp-ctl">
                        <Textarea
                          id="mcp-args"
                          className="mono"
                          rows={3}
                          value={form.args}
                          placeholder="One argument per line"
                          spellCheck={false}
                          disabled={saving}
                          onChange={(
                            event: ChangeEvent<HTMLTextAreaElement>
                          ) => updateField("args", event.target.value)}
                        ></Textarea>
                      </div>
                    </div>
                    <div className="srow">
                      <div className="mcp-srow-text">
                        <div className="t">Environment</div>
                        <div className="d">Values are hidden after you save.</div>
                      </div>
                      <div className="mcp-ctl">
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
                    </div>
                  </>
                ) : (
                  <>
                    <div className="srow">
                      <div className="mcp-srow-text">
                        <label className="t" htmlFor="mcp-url">
                          URL
                        </label>
                        <div className="d">The full http or https address.</div>
                      </div>
                      <div className="mcp-ctl">
                        <Input
                          id="mcp-url"
                          className="mono"
                          type="text"
                          value={form.url}
                          autoComplete="off"
                          spellCheck={false}
                          disabled={saving}
                          onChange={(event: ChangeEvent<HTMLInputElement>) =>
                            updateField("url", event.target.value)
                          }
                        />
                      </div>
                    </div>
                    <div className="srow">
                      <div className="mcp-srow-text">
                        <div className="t">Headers</div>
                        <div className="d">Values are hidden after you save.</div>
                      </div>
                      <div className="mcp-ctl">
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
                    </div>
                  </>
                )}
              </form>
            </Panel>

            {server.error ? (
              <Panel title="Daemon output" bodyClassName="mcp-panel-body">
                <pre className="mcp-mono-pre mono">{server.error}</pre>
              </Panel>
            ) : null}
          </div>
        ) : null}

        {tab === "agents" ? (
          <Panel bodyClassName="mcp-panel-body">
            <p className="mcp-section-meta">
              {serverAgents.length} of {agents.length} agents use this server
            </p>

            <p
              className={agentLive ? "mcp-live" : "sr"}
              role="status"
              aria-live="polite"
            >
              {agentLive}
            </p>

            {agentError ? (
              <ErrorAlert
                message={agentError}
                details={agentDetails}
                action={
                  agentStale ? (
                    <Button variant="secondary" size="sm" onClick={reload}>
                      Reload settings
                    </Button>
                  ) : null
                }
              />
            ) : null}

            <div className="mcp-agents">
              {agentRows.length === 0 ? (
                <p className="mcp-agent-empty">
                  No agents use this server yet.
                </p>
              ) : (
                agentRows.map((agent) => (
                  <div className="mcp-agent-row" key={agent.name}>
                    <AgentAvatar
                      name={agent.name}
                      color={agentColor(agent.name)}
                      size="md"
                    />
                    <span className="mcp-agent-text">
                      <span className="mcp-agent-name">{displayLabel(agent)}</span>
                      <span className="mcp-agent-model">
                        {modelLabel(agent)}
                      </span>
                    </span>
                    <IconButton
                      size="sm"
                      icon="x"
                      label={"Remove agent " + displayLabel(agent)}
                      disabled={Boolean(agentBusy)}
                      onClick={() => removeAgent(agent)}
                    />
                  </div>
                ))
              )}

              {pickerOpen ? (
                <form
                  className="mcp-picker"
                  ref={pickerRef}
                  tabIndex={-1}
                  onSubmit={addAgent}
                >
                  <span className="mcp-picker-select">
                    <Select
                      id="mcp-agent-picker-select"
                      aria-label="Agent"
                      options={availableAgents.map((agent) => ({
                        value: agent.name,
                        label: displayLabel(agent),
                      }))}
                      value={pickerValue}
                      disabled={Boolean(agentBusy)}
                      onChange={setPickerValue}
                    />
                  </span>
                  <Button
                    type="submit"
                    variant="primary"
                    size="sm"
                    disabled={Boolean(agentBusy) || !pickerValue}
                  >
                    Add
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={Boolean(agentBusy)}
                    onClick={() => setPickerOpen(false)}
                  >
                    Cancel
                  </Button>
                </form>
              ) : (
                <Button
                  variant="ghost"
                  size="sm"
                  icon="plus"
                  block
                  disabled={Boolean(agentBusy) || availableAgents.length === 0}
                  onClick={openPicker}
                >
                  {availableAgents.length === 0
                    ? "Every agent uses this server"
                    : "Add agent"}
                </Button>
              )}
            </div>
          </Panel>
        ) : null}

        {tab === "tools" ? (
          <Panel bodyClassName="mcp-panel-body">
            <p className="mcp-section-meta">{toolsCount} from this server</p>

            {entries.length > 0 ? (
              <div className="mcp-tools">
                <table className="perm-t">
                  <caption className="sr">
                    Tools reported by this server
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Tool</th>
                      <th scope="col">Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleTools.map((tool) => (
                      <tr key={tool.name}>
                        <td className="mcp-tool-name">{tool.name}</td>
                        <td className="mcp-muted">{tool.description}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {entries.length > 6 ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setShowAllTools((current) => !current)}
                  >
                    {showAllTools
                      ? "Show fewer tools"
                      : "Show all " + totalTools + " tools"}
                  </Button>
                ) : null}
              </div>
            ) : (
              <p className="mcp-muted">
                This daemon reports the tool count but not the tool names.{" "}
                {toolsCount === 0
                  ? "No tools were reported yet."
                  : countLabel(toolsCount, "tool") + " reported."}
              </p>
            )}

            <p className="mcp-hint">
              Turn tools on or off per agent in the agent's Tools tab.
            </p>
          </Panel>
        ) : null}
      </div>
    </main>
  );
}

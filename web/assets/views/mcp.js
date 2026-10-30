import {
  html,
  useState,
  useEffect,
  useMemo,
  useRef,
} from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
import { Dialog, ErrorNotice, Loading } from "/assets/ui.js";
import {
  Icon,
  AgentAvatar,
  splitModel,
} from "/assets/views/agent-common.js";
import {
  listMcpServers,
  saveMcpServer,
  testMcpServer,
  restartMcpServer,
  listAgents,
  saveAgentMcp,
} from "/assets/api/mcp.js";

/* T-032: the MCP list (REQ-WEB-050), the MCP detail page, and the add-server
   dialog with the inline test (REQ-WEB-051). The daemon never returns env or
   header values; "***" is the mask that keeps a stored value on save. */

const PAGE_SIZE = 8;

const TYPE_OPTIONS = [
  { value: "command", label: "Command" },
  { value: "url", label: "URL" },
];

const STATUS_OPTIONS = [
  { value: "", label: "All status" },
  { value: "connected", label: "Connected" },
  { value: "unreachable", label: "Unreachable" },
  { value: "idle", label: "Idle" },
];

function countLabel(value, singular, plural) {
  const number = Number(value) || 0;
  return number + " " + (number === 1 ? singular : plural || singular + "s");
}

function formatDuration(milliseconds) {
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
function effectiveStatus(server) {
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

function statusMeta(status) {
  if (status === "connected") {
    return { label: "Connected", className: "badge-ui mcp-badge-success" };
  }
  if (status === "unreachable") {
    return { label: "Unreachable", className: "badge-ui badge-ui-error" };
  }
  return { label: "Idle", className: "badge-ui" };
}

function StatusBadge({ server }) {
  const meta = statusMeta(effectiveStatus(server));
  return html`<span class=${meta.className}>
    <span class="badge-ui-dot" aria-hidden="true"></span>
    ${meta.label}
  </span>`;
}

function serverType(server) {
  if (server && server.type === "url") {
    return "url";
  }
  if (server && server.url) {
    return "url";
  }
  return "command";
}

function typeLabel(type) {
  return type === "url" ? "URL" : "Command";
}

function serverHref(name) {
  return "#/mcp/" + encodeURIComponent(name);
}

function serverTarget(server) {
  if (serverType(server) === "url") {
    return server && server.url ? String(server.url) : "";
  }
  const command = server && server.command ? String(server.command) : "";
  const args = server && Array.isArray(server.args) ? server.args : [];
  return [command].concat(args.map((arg) => String(arg))).filter(Boolean).join(" ");
}

function matchesQuery(server, query) {
  if (!query) {
    return true;
  }
  const fields = [server.name, server.command, server.url];
  if (Array.isArray(server.args)) {
    fields.push(server.args.join(" "));
  }
  return fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

function parseArgs(text) {
  if (!text) {
    return [];
  }
  return String(text)
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

function rowsFromKeys(keys) {
  if (!Array.isArray(keys)) {
    return [];
  }
  return keys.map((key) => ({ key: String(key), value: "***" }));
}

function formFromServer(server) {
  const type = serverType(server);
  return {
    type,
    command: server && server.command ? String(server.command) : "",
    args:
      server && Array.isArray(server.args) ? server.args.map(String).join("\n") : "",
    url: server && server.url ? String(server.url) : "",
    env: rowsFromKeys(server ? server.env_keys : []),
    headers: rowsFromKeys(server ? server.header_keys : []),
  };
}

function emptyForm() {
  return {
    type: "command",
    command: "",
    args: "",
    url: "",
    env: [],
    headers: [],
  };
}

function rowsToObject(rows, dropMask) {
  const out = {};
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

function definitionFromForm(form, dropMask) {
  const definition = { type: form.type };
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

function validHttpUrl(value) {
  try {
    const parsed = new URL(String(value || ""));
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      Boolean(parsed.host)
    );
  } catch (cause) {
    return false;
  }
}

function validateDefinition(definition) {
  if (!definition || (definition.type !== "command" && definition.type !== "url")) {
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

function toolEntries(server) {
  const raw = server
    ? Array.isArray(server.tools)
      ? server.tools
      : Array.isArray(server.tool_names)
        ? server.tool_names
        : []
    : [];
  return raw
    .map((tool) => {
      if (typeof tool === "string") {
        return { name: tool, description: "" };
      }
      if (!tool || typeof tool !== "object") {
        return null;
      }
      return {
        name: String(tool.name || tool.id || ""),
        description: String(tool.description || ""),
      };
    })
    .filter((tool) => tool && tool.name);
}

function stateSummary(server) {
  const state = server && server.state ? String(server.state) : "";
  const agents = server && Array.isArray(server.agents) ? server.agents.length : 0;
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

function restartLine(state, error) {
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

function modelLabel(agent) {
  const parts = splitModel(agent);
  return parts.model || "Agent default";
}

function TypeControl({ idPrefix, value, disabled, onChange }) {
  return html`<fieldset class="mcp-segmented" disabled=${disabled}>
    <legend class="sr-only">Type</legend>
    ${TYPE_OPTIONS.map(
      (option) => html`<label
        key=${option.value}
        class=${"mcp-segment" + (value === option.value ? " is-selected" : "")}
      >
        <input
          class="sr-only"
          type="radio"
          name=${idPrefix}
          value=${option.value}
          checked=${value === option.value}
          disabled=${disabled}
          onChange=${() => onChange(option.value)}
        />
        <span>${option.label}</span>
      </label>`
    )}
  </fieldset>`;
}

function KeyValueRows({
  idPrefix,
  kind,
  rows,
  disabled,
  onAdd,
  onChange,
  onRemove,
}) {
  const noun = kind === "headers" ? "header" : "variable";
  const group = kind === "headers" ? "Header" : "Environment variable";
  return html`<div class="mcp-key-list">
    ${rows.length === 0
      ? html`<p class="mcp-key-empty">No ${noun}s yet.</p>`
      : null}
    ${rows.map((row, index) => {
      const key = String(row.key || "").trim();
      const masked = row.value === "***";
      const removeLabel =
        "Remove " + noun + (key ? " " + key : "");
      return html`<div class="mcp-key-row" key=${idPrefix + "-" + index}>
        <label class="sr-only" for=${idPrefix + "-key-" + index}>
          ${group + " key " + (index + 1)}
        </label>
        <input
          id=${idPrefix + "-key-" + index}
          class="mcp-mono-input"
          type="text"
          value=${row.key}
          placeholder="KEY"
          autocomplete="off"
          spellcheck="false"
          disabled=${disabled}
          onInput=${(event) => onChange(index, "key", event.target.value)}
        />
        <label class="sr-only" for=${idPrefix + "-value-" + index}>
          ${group + " value " + (index + 1)}
        </label>
        <input
          id=${idPrefix + "-value-" + index}
          class="mcp-key-value mcp-mono-input"
          type="password"
          value=${masked ? "" : row.value}
          placeholder=${masked ? "••••••••" : "Value"}
          autocomplete="off"
          spellcheck="false"
          disabled=${disabled}
          onInput=${(event) => onChange(index, "value", event.target.value)}
        />
        <button
          type="button"
          class="mcp-key-remove"
          aria-label=${removeLabel}
          disabled=${disabled}
          onClick=${() => onRemove(index)}
        >
          <${Icon} name="close" className="icon-16" />
        </button>
      </div>`;
    })}
    <button
      type="button"
      class="mcp-add-key"
      disabled=${disabled}
      onClick=${onAdd}
    >
      <${Icon} name="plus" className="icon-16" />
      <span>${kind === "headers" ? "Add header" : "Add variable"}</span>
    </button>
  </div>`;
}

function AddServerDialog({ version, onClose, onAdded }) {
  const [name, setName] = useState("");
  const [form, setForm] = useState(emptyForm);
  const [testResult, setTestResult] = useState(null);
  const [tested, setTested] = useState(false);
  const [testing, setTesting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const testId = "mcp-add-test-result";

  function updateField(key, value) {
    setForm((current) => Object.assign({}, current, { [key]: value }));
    setTestResult(null);
    setTested(false);
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  function updateRow(kind, index, field, value) {
    setForm((current) => {
      const rows = current[kind].map((row, position) =>
        position === index ? Object.assign({}, row, { [field]: value }) : row
      );
      return Object.assign({}, current, { [kind]: rows });
    });
    setTestResult(null);
    setTested(false);
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  function addRow(kind) {
    setForm((current) =>
      Object.assign({}, current, {
        [kind]: current[kind].concat([{ key: "", value: "" }]),
      })
    );
    if (!busy) {
      setError("");
      setDetails([]);
    }
  }

  function removeRow(kind, index) {
    setForm((current) =>
      Object.assign({}, current, {
        [kind]: current[kind].filter((row, position) => position !== index),
      })
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

  async function submit(event) {
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
      setError("The config version is unavailable. Reload the page and try again.");
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

  return html`<${Dialog}
    title="Add MCP server"
    className="mcp-add-dialog"
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form class="mcp-dialog-form" onSubmit=${submit}>
      <p class="mcp-dialog-subtitle">
        Run a local command or connect to a server URL.
      </p>

      <label class="mcp-field">
        <span class="mcp-field-label">Name</span>
        <input
          data-autofocus
          type="text"
          name="name"
          value=${name}
          autocomplete="off"
          spellcheck="false"
          pattern="[a-z0-9-]+"
          required
          disabled=${busy}
          aria-describedby="mcp-add-name-hint"
          onInput=${(event) => {
            setName(event.target.value);
            setError("");
            setDetails([]);
          }}
        />
        <span id="mcp-add-name-hint" class="hint">
          Lowercase letters, digits, and dashes.
        </span>
      </label>

      <div class="mcp-field">
        <span class="mcp-field-label" aria-hidden="true">Type</span>
        <${TypeControl}
          idPrefix="mcp-add-type"
          value=${form.type}
          disabled=${busy}
          onChange=${(value) => updateField("type", value)}
        />
      </div>

      ${form.type === "command"
        ? html`<div class="mcp-command-fields">
            <label class="mcp-field">
              <span class="mcp-field-label">Command</span>
              <input
                class="mcp-mono-input"
                type="text"
                value=${form.command}
                placeholder="npx"
                autocomplete="off"
                spellcheck="false"
                disabled=${busy}
                onInput=${(event) => updateField("command", event.target.value)}
              />
            </label>
            <label class="mcp-field">
              <span class="mcp-field-label">Arguments</span>
              <textarea
                class="mcp-mono-input"
                rows="3"
                value=${form.args}
                placeholder="One argument per line"
                spellcheck="false"
                disabled=${busy}
                onInput=${(event) => updateField("args", event.target.value)}
              ></textarea>
              <span class="hint">Put each argument on its own line.</span>
            </label>
            <div class="mcp-field">
              <span class="mcp-field-label">Environment</span>
              <${KeyValueRows}
                idPrefix="mcp-add-env"
                kind="env"
                rows=${form.env}
                disabled=${busy}
                onAdd=${() => addRow("env")}
                onChange=${(index, field, value) =>
                  updateRow("env", index, field, value)}
                onRemove=${(index) => removeRow("env", index)}
              />
            </div>
          </div>`
        : html`<div class="mcp-command-fields">
            <label class="mcp-field">
              <span class="mcp-field-label">URL</span>
              <input
                class="mcp-mono-input"
                type="text"
                value=${form.url}
                placeholder="Server URL"
                autocomplete="off"
                spellcheck="false"
                disabled=${busy}
                onInput=${(event) => updateField("url", event.target.value)}
              />
              <span class="hint">Use the full http or https address.</span>
            </label>
            <div class="mcp-field">
              <span class="mcp-field-label">Headers</span>
              <${KeyValueRows}
                idPrefix="mcp-add-header"
                kind="headers"
                rows=${form.headers}
                disabled=${busy}
                onAdd=${() => addRow("headers")}
                onChange=${(index, field, value) =>
                  updateRow("headers", index, field, value)}
                onRemove=${(index) => removeRow("headers", index)}
              />
            </div>
          </div>`}

      <div class="mcp-test-row">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${busy || testing}
          aria-describedby=${testId}
          onClick=${runTest}
        >${testing ? "Testing…" : tested ? "Test again" : "Test"}</button>
        <p
          id=${testId}
          class=${"mcp-test-line" +
          (testing
            ? " is-testing"
            : testResult
              ? testResult.ok
                ? " is-ok"
                : " is-error"
              : "")}
          role="status"
          aria-live="polite"
        >${testing ? "Testing the server…" : testResult ? testResult.message : ""}</p>
      </div>

      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}

      <div class="mcp-dialog-footer">
        <p class="mcp-dialog-note">You pick agents after adding it.</p>
        <div class="dialog-actions">
          <button
            type="button"
            class="button-ui button-ui-secondary"
            disabled=${busy}
            onClick=${onClose}
          >Cancel</button>
          <button
            type="submit"
            class="button-ui button-ui-primary"
            disabled=${busy}
          >${busy ? "Adding…" : "Add server"}</button>
        </div>
      </div>
    </form>
  </${Dialog}>`;
}

export function McpPage() {
  const [status, setStatus] = useState("loading");
  const [servers, setServers] = useState([]);
  const [version, setVersion] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
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
    countLabel(servers.length, "server") + " · " + unreachable + " unreachable";

  function handleAdded() {
    setDialogOpen(false);
    setLive("Server added");
    setReloadToken((current) => current + 1);
  }

  function openRow(event, server) {
    const target = event.target;
    if (
      target &&
      typeof target.closest === "function" &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    window.location.hash = serverHref(server.name);
  }

  return html`<main id="main" class="page mcp-page">
    <header class="agents-header">
      <div class="agents-heading">
        <h1 class="agents-title">MCP</h1>
        <p class="agents-subtitle">
          ${status === "ready"
            ? summary
            : "External tools from Model Context Protocol servers."}
        </p>
      </div>
      <button
        type="button"
        class="button-ui button-ui-primary"
        disabled=${status !== "ready" || !version}
        onClick=${() => setDialogOpen(true)}
      >+ Add server</button>
    </header>

    <div class="agents-toolbar">
      <div class="sessions-search agents-search">
        <span class="sessions-search-icon">
          <${Icon} name="search" />
        </span>
        <label class="sr-only" for="mcp-search-input">Search servers</label>
        <input
          id="mcp-search-input"
          class="sessions-search-input"
          type="search"
          placeholder="Search servers"
          autocomplete="off"
          spellcheck="false"
          value=${query}
          onInput=${(event) => setQuery(event.target.value)}
        />
      </div>
      <div class="mcp-filter">
        <${Icon} name="filter" />
        <label class="sr-only" for="mcp-status-filter">Status</label>
        <select
          id="mcp-status-filter"
          class="mcp-filter-select"
          value=${filter}
          onChange=${(event) => setFilter(event.target.value)}
        >
          ${STATUS_OPTIONS.map(
            (option) => html`<option key=${option.value} value=${option.value}>
              ${option.label}
            </option>`
          )}
        </select>
      </div>
      <span class="agents-toolbar-spacer"></span>
    </div>

    <p
      class=${live ? "mcp-live" : "sr-only"}
      role="status"
      aria-live="polite"
    >${live}</p>

    ${status === "loading"
      ? html`<${Loading} label="Loading MCP servers…" />`
      : null}

    ${status === "error"
      ? html`<div class="agents-state" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          <button
            type="button"
            class="button-ui button-ui-secondary"
            onClick=${() => setReloadToken((current) => current + 1)}
          >Try again</button>
        </div>`
      : null}

    ${status === "ready" && servers.length === 0
      ? html`<div class="agents-empty">
          <h2 class="agents-empty-title">No MCP servers yet.</h2>
          <p class="agents-empty-text">
            Add a server once, then pick which agents use it.
          </p>
          <button
            type="button"
            class="button-ui button-ui-primary"
            disabled=${!version}
            onClick=${() => setDialogOpen(true)}
          >+ Add server</button>
        </div>`
      : null}

    ${status === "ready" && servers.length > 0 && filtered.length === 0
      ? html`<p class="agents-state muted" role="status">
          ${query.trim()
            ? "No servers match “" + query.trim() + "”."
            : "No servers match this status filter."}
        </p>`
      : null}

    ${status === "ready" && rows.length > 0
      ? html`<div class="mcp-table-card">
          <div class="mcp-table-scroll">
            <table class="agents-table mcp-table">
              <caption class="sr-only">MCP servers</caption>
              <thead>
                <tr>
                  <th scope="col">Server</th>
                  <th scope="col">Type</th>
                  <th scope="col">Tools</th>
                  <th scope="col">Agents</th>
                  <th scope="col">Status</th>
                  <th scope="col"><span class="sr-only">Open</span></th>
                </tr>
              </thead>
              <tbody>
                ${rows.map((server) => {
                  const target = serverTarget(server);
                  const agentNames = Array.isArray(server.agents)
                    ? server.agents.map(String)
                    : [];
                  const shown = agentNames.slice(0, 4);
                  return html`<tr
                    key=${server.name}
                    onClick=${(event) => openRow(event, server)}
                  >
                    <td>
                      <span class="mcp-server-cell">
                        <span class="mcp-server-tile" aria-hidden="true">
                          <${Icon} name="mcp" />
                        </span>
                        <span class="mcp-server-text">
                          <a
                            class="mcp-server-name"
                            href=${serverHref(server.name)}
                          >${server.name}</a>
                          <span class="mcp-server-target">${target || "—"}</span>
                        </span>
                      </span>
                    </td>
                    <td>
                      <span class="badge-ui mcp-type-badge">
                        <span class="badge-ui-dot" aria-hidden="true"></span>
                        ${typeLabel(serverType(server))}
                      </span>
                    </td>
                    <td class="agents-number">
                      ${Number(server.tools_count) || 0}
                    </td>
                    <td>
                      ${agentNames.length === 0
                        ? html`<span class="mcp-muted">No agents</span>`
                        : html`<span class="mcp-agent-cell">
                            <span
                              class="agent-avatar-group"
                              aria-hidden="true"
                            >
                              ${shown.map(
                                (agentName) => html`<${AgentAvatar}
                                  key=${agentName}
                                  name=${agentName}
                                  size="24"
                                />`
                              )}
                            </span>
                            ${agentNames.length > shown.length
                              ? html`<span class="mcp-agent-more">
                                  +${agentNames.length - shown.length}
                                </span>`
                              : null}
                            <span class="sr-only">
                              Used by ${agentNames.join(", ")}
                            </span>
                          </span>`}
                    </td>
                    <td><${StatusBadge} server=${server} /></td>
                    <td class="agents-chevron">
                      <${Icon} name="chevron-right" />
                    </td>
                  </tr>`;
                })}
              </tbody>
            </table>
          </div>
          <nav class="mcp-pagination" aria-label="Pagination">
            <p class="mcp-pagination-text">
              Showing ${start + 1}–${start + rows.length} of ${filtered.length}
            </p>
            <div class="mcp-pagination-actions">
              <button
                type="button"
                class="button-ui button-ui-secondary"
                disabled=${currentPage <= 1}
                onClick=${() => setPage(Math.max(1, currentPage - 1))}
              >Previous</button>
              <button
                type="button"
                class="button-ui button-ui-secondary"
                disabled=${currentPage >= pageCount}
                onClick=${() => setPage(Math.min(pageCount, currentPage + 1))}
              >Next</button>
            </div>
          </nav>
        </div>`
      : null}

    ${dialogOpen
      ? html`<${AddServerDialog}
          version=${version}
          onClose=${() => setDialogOpen(false)}
          onAdded=${handleAdded}
        />`
      : null}
  </main>`;
}

function DetailBreadcrumb({ name }) {
  return html`<nav class="agent-breadcrumb" aria-label="Breadcrumb">
    <a href="#/mcp">MCP</a>
    <span aria-hidden="true">/</span>
    <span aria-current="page">${name}</span>
  </nav>`;
}

export function McpDetailPage({ name }) {
  const [status, setStatus] = useState("loading");
  const [server, setServer] = useState(null);
  const [agents, setAgents] = useState([]);
  const [version, setVersion] = useState("");
  const [form, setForm] = useState(null);
  const [baseline, setBaseline] = useState(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restartStatus, setRestartStatus] = useState(null);
  const [saveNotice, setSaveNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [stale, setStale] = useState(false);
  const [agentBusy, setAgentBusy] = useState("");
  const [agentError, setAgentError] = useState("");
  const [agentDetails, setAgentDetails] = useState([]);
  const [agentStale, setAgentStale] = useState(false);
  const [agentLive, setAgentLive] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerValue, setPickerValue] = useState("");
  const [showAllTools, setShowAllTools] = useState(false);
  const pickerRef = useRef(null);

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

  function updateField(key, value) {
    setForm((current) =>
      current ? Object.assign({}, current, { [key]: value }) : current
    );
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function updateRow(kind, index, field, value) {
    setForm((current) => {
      if (!current) {
        return current;
      }
      const rows = current[kind].map((row, position) =>
        position === index ? Object.assign({}, row, { [field]: value }) : row
      );
      return Object.assign({}, current, { [kind]: rows });
    });
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function addRow(kind) {
    setForm((current) =>
      current
        ? Object.assign({}, current, {
            [kind]: current[kind].concat([{ key: "", value: "" }]),
          })
        : current
    );
    setSaveNotice("");
  }

  function removeRow(kind, index) {
    setForm((current) =>
      current
        ? Object.assign({}, current, {
            [kind]: current[kind].filter(
              (row, position) => position !== index
            ),
          })
        : current
    );
    setSaveNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  async function save(event) {
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
      setError("The config version is unavailable. Reload the page and try again.");
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
        const next = Object.assign({}, current, {
          type: definition.type,
          command: definition.command || "",
          args: definition.args || [],
          url: definition.url || "",
          env_keys: Object.keys(definition.env || {}),
          header_keys: Object.keys(definition.headers || {}),
        });
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
        const status =
          nextState === "running"
            ? "connected"
            : nextState === "unavailable"
              ? "unreachable"
              : "idle";
        return Object.assign({}, current, {
          state: nextState || "stopped",
          error: nextError,
          status,
        });
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
      } catch (failure) {
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

  async function changeAgent(agentName, nextList, nextServerAgents, message) {
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
            ? Object.assign({}, agent, { mcp: nextList.slice() })
            : agent
        )
      );
      setServer((current) =>
        current
          ? Object.assign({}, current, { agents: nextServerAgents.slice() })
          : current
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

  function removeAgent(agent) {
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
    if (availableAgents.length === 0) {
      return;
    }
    setPickerValue(availableAgents[0].name);
    setPickerOpen(true);
  }

  function addAgent(event) {
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
    return html`<main id="main" class="page mcp-detail-page">
      <${DetailBreadcrumb} name=${name} />
      <${Loading} label="Loading MCP server…" />
    </main>`;
  }

  if (status === "not-found" || (status === "ready" && !server)) {
    return html`<main id="main" class="page mcp-detail-page">
      <${DetailBreadcrumb} name=${name} />
      <div class="agents-empty">
        <h1 class="agents-empty-title">Server not found</h1>
        <p class="agents-empty-text">
          No MCP server named <code>${name}</code> is configured.
        </p>
        <a class="button-ui button-ui-secondary" href="#/mcp">
          Back to MCP servers
        </a>
      </div>
    </main>`;
  }

  if (status === "error" || !form) {
    return html`<main id="main" class="page mcp-detail-page">
      <${DetailBreadcrumb} name=${name} />
      <div role="alert">
        <${ErrorNotice} message=${error} details=${details} />
      </div>
      <div class="agent-state-actions">
        <button type="button" class="button-ui button-ui-secondary" onClick=${reload}>
          Try again
        </button>
        <a class="button-ui button-ui-secondary" href="#/mcp">
          Back to MCP servers
        </a>
      </div>
    </main>`;
  }

  const serverAgents = Array.isArray(server.agents)
    ? server.agents.map(String)
    : [];
  const toolsCount = Number(server.tools_count) || 0;
  const totalTools = Math.max(toolsCount, entries.length);
  const agentRows = serverAgents.map((agentName) => {
    return (
      agents.find((agent) => agent.name === agentName) || {
        name: agentName,
        mcp: [name],
      }
    );
  });

  return html`<main id="main" class="page mcp-detail-page">
    <${DetailBreadcrumb} name=${name} />

    <header class="mcp-detail-header">
      <div class="mcp-detail-identity">
        <span class="mcp-detail-tile" aria-hidden="true">
          <${Icon} name="mcp" />
        </span>
        <div class="mcp-detail-heading">
          <div class="mcp-detail-name-row">
            <h1 class="mcp-detail-name">${server.name}</h1>
            <${StatusBadge} server=${server} />
          </div>
          <p class="mcp-detail-meta">${stateSummary(server)}</p>
        </div>
      </div>
      <div class="mcp-detail-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${restarting}
          onClick=${restart}
        >${restarting ? "Restarting…" : "Restart"}</button>
        <button
          type="button"
          class="button-ui button-ui-primary"
          disabled=${!dirty || saving}
          onClick=${save}
        >${saving ? "Saving…" : "Save changes"}</button>
      </div>
    </header>

    <p
      class=${"mcp-live" + (restartStatus && restartStatus.tone === "error" ? " is-error" : "") +
      (restartStatus ? "" : " sr-only")}
      role="status"
      aria-live="polite"
    >${restartStatus ? restartStatus.text : ""}</p>

    <div
      class=${saveNotice ? "mcp-save-notice" : "sr-only"}
      role="status"
      aria-live="polite"
    >
      ${saveNotice
        ? html`<p class="notice notice-ok">${saveNotice}</p>`
        : null}
    </div>

    ${error
      ? html`<div class="mcp-error" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          ${stale
            ? html`<button
                type="button"
                class="button-ui button-ui-secondary"
                onClick=${reload}
              >Reload settings</button>`
            : null}
        </div>`
      : null}

    <div class="mcp-detail-grid">
      <form
        id="mcp-connection-form"
        class="mcp-connection"
        aria-labelledby="mcp-connection-title"
        onSubmit=${save}
      >
        <h2 class="mcp-section-title" id="mcp-connection-title">Connection</h2>

        <div class="mcp-field">
          <span class="mcp-field-label" aria-hidden="true">Type</span>
          <${TypeControl}
            idPrefix="mcp-detail-type"
            value=${form.type}
            disabled=${saving}
            onChange=${(value) => updateField("type", value)}
          />
        </div>

        ${form.type === "command"
          ? html`<div class="mcp-command-fields">
              <label class="mcp-field">
                <span class="mcp-field-label">Command</span>
                <input
                  class="mcp-mono-input"
                  type="text"
                  value=${form.command}
                  autocomplete="off"
                  spellcheck="false"
                  disabled=${saving}
                  onInput=${(event) =>
                    updateField("command", event.target.value)}
                />
              </label>
              <label class="mcp-field">
                <span class="mcp-field-label">Arguments</span>
                <textarea
                  class="mcp-mono-input"
                  rows="3"
                  value=${form.args}
                  placeholder="One argument per line"
                  spellcheck="false"
                  disabled=${saving}
                  onInput=${(event) => updateField("args", event.target.value)}
                ></textarea>
                <span class="hint">Put each argument on its own line.</span>
              </label>
              <div class="mcp-field">
                <span class="mcp-field-label">Environment</span>
                <${KeyValueRows}
                  idPrefix="mcp-detail-env"
                  kind="env"
                  rows=${form.env}
                  disabled=${saving}
                  onAdd=${() => addRow("env")}
                  onChange=${(index, field, value) =>
                    updateRow("env", index, field, value)}
                  onRemove=${(index) => removeRow("env", index)}
                />
              </div>
              <p class="hint mcp-connection-note">
                Values are hidden after you save. Use secrets for tokens.
              </p>
            </div>`
          : html`<div class="mcp-command-fields">
              <label class="mcp-field">
                <span class="mcp-field-label">URL</span>
                <input
                  class="mcp-mono-input"
                  type="text"
                  value=${form.url}
                  autocomplete="off"
                  spellcheck="false"
                  disabled=${saving}
                  onInput=${(event) => updateField("url", event.target.value)}
                />
                <span class="hint">Use the full http or https address.</span>
              </label>
              <div class="mcp-field">
                <span class="mcp-field-label">Headers</span>
                <${KeyValueRows}
                  idPrefix="mcp-detail-header"
                  kind="headers"
                  rows=${form.headers}
                  disabled=${saving}
                  onAdd=${() => addRow("headers")}
                  onChange=${(index, field, value) =>
                    updateRow("headers", index, field, value)}
                  onRemove=${(index) => removeRow("headers", index)}
                />
              </div>
              <p class="hint mcp-connection-note">
                Values are hidden after you save. Use secrets for tokens.
              </p>
            </div>`}
      </form>

      <div class="mcp-detail-side">
        <section class="mcp-section" aria-labelledby="mcp-agents-title">
          <div class="mcp-section-head">
            <h2 class="mcp-section-title" id="mcp-agents-title">Agents</h2>
            <span class="mcp-section-meta">
              ${serverAgents.length} of ${agents.length}
            </span>
          </div>

          <div
            class=${agentLive ? "mcp-live" : "sr-only"}
            role="status"
            aria-live="polite"
          >${agentLive}</div>

          ${agentError
            ? html`<div class="mcp-agent-error" role="alert">
                <${ErrorNotice} message=${agentError} details=${agentDetails} />
                ${agentStale
                  ? html`<button
                      type="button"
                      class="button-ui button-ui-secondary"
                      onClick=${reload}
                    >Reload settings</button>`
                  : null}
              </div>`
            : null}

          <div class="mcp-agent-list">
            ${agentRows.length === 0
              ? html`<p class="mcp-agent-empty">
                  No agents use this server yet.
                </p>`
              : agentRows.map(
                  (agent) => html`<div class="mcp-agent-row" key=${agent.name}>
                    <${AgentAvatar} name=${agent.name} size="28" />
                    <span class="mcp-agent-text">
                      <span class="mcp-agent-name">${agent.name}</span>
                      <span class="mcp-agent-model">${modelLabel(agent)}</span>
                    </span>
                    <button
                      type="button"
                      class="mcp-icon-button"
                      aria-label=${"Remove agent " + agent.name}
                      disabled=${Boolean(agentBusy)}
                      onClick=${() => removeAgent(agent)}
                    >
                      <${Icon} name="close" className="icon-16" />
                    </button>
                  </div>`
                )}

            ${pickerOpen
              ? html`<form class="mcp-agent-picker" onSubmit=${addAgent}>
                  <label class="sr-only" for="mcp-agent-picker-select">
                    Agent
                  </label>
                  <select
                    id="mcp-agent-picker-select"
                    ref=${pickerRef}
                    value=${pickerValue}
                    disabled=${Boolean(agentBusy)}
                    onChange=${(event) => setPickerValue(event.target.value)}
                  >
                    ${availableAgents.map(
                      (agent) => html`<option key=${agent.name} value=${agent.name}>
                        ${agent.name}
                      </option>`
                    )}
                  </select>
                  <button
                    type="submit"
                    class="button-ui button-ui-primary"
                    disabled=${Boolean(agentBusy) || !pickerValue}
                  >Add</button>
                  <button
                    type="button"
                    class="button-ui button-ui-secondary"
                    disabled=${Boolean(agentBusy)}
                    onClick=${() => setPickerOpen(false)}
                  >Cancel</button>
                </form>`
              : html`<button
                  type="button"
                  class="mcp-add-agent"
                  disabled=${Boolean(agentBusy) || availableAgents.length === 0}
                  onClick=${openPicker}
                >
                  <span class="mcp-add-agent-tile" aria-hidden="true">
                    <${Icon} name="plus" className="icon-16" />
                  </span>
                  <span>
                    ${availableAgents.length === 0
                      ? "Every agent uses this server"
                      : "Add agent"}
                  </span>
                </button>`}
          </div>
        </section>

        <section class="mcp-section" aria-labelledby="mcp-tools-title">
          <div class="mcp-section-head">
            <h2 class="mcp-section-title" id="mcp-tools-title">Tools</h2>
            <span class="mcp-section-meta">
              ${toolsCount} from this server
            </span>
          </div>

          ${entries.length > 0
            ? html`<div class="mcp-tool-list">
                ${visibleTools.map(
                  (tool) => html`<div class="mcp-tool-row" key=${tool.name}>
                    <span class="mcp-tool-name">${tool.name}</span>
                    <span class="mcp-tool-description">
                      ${tool.description}
                    </span>
                  </div>`
                )}
                ${entries.length > 6
                  ? html`<button
                      type="button"
                      class="mcp-tool-more"
                      onClick=${() => setShowAllTools((current) => !current)}
                    >${showAllTools
                      ? "Show fewer tools"
                      : "Show all " + totalTools + " tools"}</button>`
                  : null}
              </div>`
            : html`<div class="mcp-tool-list">
                <p class="mcp-tool-empty">
                  This daemon reports the tool count but not the tool names.
                  ${toolsCount === 0
                    ? " No tools were reported yet."
                    : " " + countLabel(toolsCount, "tool") + " reported."}
                </p>
              </div>`}

          <p class="mcp-tool-hint">
            Turn tools on or off per agent in the agent's Tools tab.
          </p>
        </section>
      </div>
    </div>
  </main>`;
}

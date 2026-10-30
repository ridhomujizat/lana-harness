import {
  html,
  useState,
  useEffect,
  useMemo,
} from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
import { ErrorNotice, Loading } from "/assets/ui.js";
import { Icon } from "/assets/views/agent-common.js";
import {
  loadAgentTab,
  listMcp,
  updateAgent,
  isErrorCode,
} from "/assets/api/agent-tabs.js";

/* REQ-WEB-047: the MCP servers with tool count and status. The On switch
   edits the agent's mcp list; adding a server lives on the MCP page. */

function statusMeta(server) {
  let value = String(server && server.status ? server.status : "");
  if (!value) {
    const state = String(server && server.state ? server.state : "");
    if (state === "running") {
      value = "connected";
    } else if (state === "unavailable") {
      value = "unreachable";
    } else if (state === "stopped") {
      value = "idle";
    }
  }
  if (value === "connected") {
    return { label: "Connected", tone: "success" };
  }
  if (value === "unreachable") {
    return { label: "Unreachable", tone: "error" };
  }
  if (value === "needs_sign_in") {
    return { label: "Needs sign-in", tone: "warning" };
  }
  if (value === "idle") {
    return { label: "Idle", tone: "" };
  }
  if (value) {
    return { label: value.charAt(0).toUpperCase() + value.slice(1), tone: "" };
  }
  return { label: "Idle", tone: "" };
}

function serverDescription(server) {
  if (server && server.type === "url") {
    return server.url || "No URL set.";
  }
  const args = Array.isArray(server && server.args) ? server.args : [];
  const parts = [server && server.command ? String(server.command) : ""]
    .concat(args.map(String))
    .filter(Boolean);
  return parts.length > 0 ? parts.join(" ") : "No command set.";
}

function uniqueNames(values) {
  const names = [];
  for (const value of values) {
    const name = String(value || "");
    if (name && names.indexOf(name) < 0) {
      names.push(name);
    }
  }
  return names;
}

function domId(value) {
  return "agent-mcp-" + String(value).replace(/[^a-zA-Z0-9_-]/g, "-");
}

export function McpTab({ name }) {
  const [status, setStatus] = useState("loading");
  const [servers, setServers] = useState([]);
  const [selected, setSelected] = useState([]);
  const [version, setVersion] = useState("");
  const [query, setQuery] = useState("");
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
    Promise.all([loadAgentTab(name), listMcp()])
      .then(([config, page]) => {
        if (!alive) {
          return;
        }
        const names = Array.isArray(config.agent.mcp)
          ? config.agent.mcp
          : Array.isArray(config.row.mcp)
          ? config.row.mcp
          : [];
        setSelected(uniqueNames(names));
        setServers(page && Array.isArray(page.servers) ? page.servers : []);
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

  const selectedSet = useMemo(() => new Set(selected), [selected]);

  const visibleServers = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) {
      return servers;
    }
    return servers.filter((server) => {
      return (
        String(server.name || "").toLowerCase().includes(needle) ||
        serverDescription(server).toLowerCase().includes(needle) ||
        statusMeta(server).label.toLowerCase().includes(needle)
      );
    });
  }, [servers, query]);

  const onCount = useMemo(
    () => servers.filter((server) => selectedSet.has(server.name)).length,
    [servers, selectedSet]
  );

  function reload() {
    setReloadToken((value) => value + 1);
  }

  async function toggleServer(serverName, enabled) {
    if (saving) {
      return;
    }
    setError("");
    setDetails([]);
    setStale(false);
    setNotice("");
    if (!version) {
      setError("The config version is unavailable. Reload the page and try again.");
      return;
    }
    const previous = selected;
    const next = enabled
      ? uniqueNames(previous.concat([serverName]))
      : previous.filter((item) => item !== serverName);
    setSaving(true);
    setSelected(next);
    try {
      const result = await updateAgent(name, { mcp: next }, version);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setNotice("Changes saved. Restart the daemon for them to take effect.");
    } catch (failure) {
      setSelected(previous);
      if (isErrorCode(failure, "stale_version")) {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload the servers to get the latest version, then try again."
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
      <${Loading} label="Loading MCP servers…" />
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

  return html`<div class="agent-tabs-pane">
    <div class="agent-tabs-toolbar">
      <label class="agent-tabs-search">
        <span class="sr-only">Search servers</span>
        <${Icon} name="search" />
        <input
          type="search"
          placeholder="Search servers"
          autocomplete="off"
          value=${query}
          onInput=${(event) => setQuery(event.target.value)}
        />
      </label>
      <span class="agent-tabs-toolbar-spacer"></span>
      <p class="agent-tabs-summary">
        ${servers.length} ${servers.length === 1 ? "server" : "servers"} · ${onCount} on
      </p>
      <a class="button-ui button-ui-primary" href="#/mcp">+ Add server</a>
    </div>

    ${servers.length === 0
      ? html`<p class="agent-tabs-empty">
          No MCP servers configured. Add one on the MCP page.
        </p>`
      : visibleServers.length === 0
      ? html`<p class="agent-tabs-empty">No servers match “${query}”.</p>`
      : html`<div class="agent-tabs-table-wrap">
          <table class="agent-tabs-table agent-mcp-table">
            <thead>
              <tr>
                <th scope="col">Server</th>
                <th scope="col" class="agent-mcp-col-tools">Tools</th>
                <th scope="col" class="agent-mcp-col-status">Status</th>
                <th scope="col" class="agent-tabs-col-switch">On</th>
                <th scope="col" class="agent-tabs-col-action">
                  <span class="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              ${visibleServers.map((server) => {
                const meta = statusMeta(server);
                const labelId = domId(server.name);
                const enabled = selectedSet.has(server.name);
                return html`<tr key=${server.name}>
                  <th scope="row" class="agent-mcp-name-cell">
                    <span class="agent-tabs-tile">
                      <${Icon} name="mcp" />
                    </span>
                    <span class="agent-mcp-text">
                      <span class="agent-mcp-name">${server.name}</span>
                      <span class="agent-mcp-command">${serverDescription(server)}</span>
                    </span>
                  </th>
                  <td class="agent-mcp-tools">
                    ${Number(server.tools_count) || 0}
                  </td>
                  <td>
                    <span
                      class=${"badge-ui" +
                        (meta.tone ? " agent-tabs-badge-" + meta.tone : "")}
                    >
                      <span class="badge-ui-dot" aria-hidden="true"></span>
                      ${meta.label}
                    </span>
                  </td>
                  <td class="agent-tabs-switch-cell">
                    <label class="agent-switch">
                      <input
                        class="agent-switch-input"
                        type="checkbox"
                        role="switch"
                        aria-labelledby=${labelId}
                        checked=${enabled}
                        disabled=${saving}
                        onChange=${(event) =>
                          toggleServer(server.name, event.target.checked)}
                      />
                      <span class="agent-switch-track" aria-hidden="true">
                        <span class="agent-switch-thumb"></span>
                      </span>
                      <span class="agent-switch-text">
                        ${enabled ? "On" : "Off"}
                      </span>
                    </label>
                  </td>
                  <td class="agent-tabs-col-action">
                    <a
                      class="agent-tabs-row-link"
                      href=${"#/mcp/" + encodeURIComponent(server.name)}
                      aria-label=${"Open " + server.name + " on the MCP page"}
                    >
                      <${Icon} name="chevron-right" className="icon-16" />
                    </a>
                  </td>
                </tr>`;
              })}
            </tbody>
          </table>
        </div>`}

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
              >Reload servers</button>`
            : null}
        </div>`
      : null}
  </div>`;
}

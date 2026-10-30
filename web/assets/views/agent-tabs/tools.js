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
  getToolCatalog,
  updateAgent,
  isErrorCode,
} from "/assets/api/agent-tabs.js";

/* REQ-WEB-047, API-WEB-026: tool groups with On switches. A switch writes
   disabled_tools. MCP tool names are "<server>/<tool>" in the config. */

function isMcpGroup(group) {
  return group.id.indexOf("mcp:") === 0;
}

function qualifiedName(group, tool) {
  return isMcpGroup(group)
    ? group.id.slice("mcp:".length) + "/" + tool.name
    : tool.name;
}

function collectDisabled(groups, previousDisabled) {
  const names = [];
  const known = new Set();
  for (const group of groups) {
    for (const tool of group.tools) {
      const name = qualifiedName(group, tool);
      known.add(name);
      if (!tool.enabled) {
        names.push(name);
      }
    }
  }
  for (const name of previousDisabled) {
    if (!known.has(name)) {
      names.push(name);
    }
  }
  return names;
}

function domId(value) {
  return "agent-tool-" + String(value).replace(/[^a-zA-Z0-9_-]/g, "-");
}

function riskLabel(value) {
  const text = String(value || "");
  if (!text) {
    return "—";
  }
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function normalizeGroups(value) {
  const groups = Array.isArray(value) ? value : [];
  return groups.map((group) => ({
    id: String(group && group.id ? group.id : ""),
    label: String(group && group.label ? group.label : ""),
    tools: (Array.isArray(group && group.tools) ? group.tools : []).map(
      (tool) => ({
        name: String(tool && tool.name ? tool.name : ""),
        risk: String(tool && tool.risk ? tool.risk : ""),
        asks_first: Boolean(tool && tool.asks_first),
        enabled: Boolean(tool && tool.enabled),
      })
    ),
  }));
}

export function ToolsTab({ name }) {
  const [status, setStatus] = useState("loading");
  const [groups, setGroups] = useState([]);
  const [disabledList, setDisabledList] = useState([]);
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
    Promise.all([loadAgentTab(name), getToolCatalog(name)])
      .then(([config, catalog]) => {
        if (!alive) {
          return;
        }
        setVersion(config.version);
        setDisabledList(
          Array.isArray(config.row.disabled_tools)
            ? config.row.disabled_tools.map(String)
            : []
        );
        setGroups(normalizeGroups(catalog && catalog.groups));
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

  const visibleGroups = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) {
      return groups;
    }
    return groups
      .map((group) => {
        const labelMatches = group.label.toLowerCase().indexOf(needle) >= 0;
        return Object.assign({}, group, {
          tools: labelMatches
            ? group.tools
            : group.tools.filter((tool) =>
                tool.name.toLowerCase().includes(needle)
              ),
        });
      })
      .filter((group) => group.tools.length > 0);
  }, [groups, query]);

  const counts = useMemo(() => {
    const result = {
      builtinOn: 0,
      builtinTotal: 0,
      mcpOn: 0,
      mcpTotal: 0,
    };
    for (const group of groups) {
      const mcp = isMcpGroup(group);
      for (const tool of group.tools) {
        if (mcp) {
          result.mcpTotal += 1;
          if (tool.enabled) {
            result.mcpOn += 1;
          }
        } else {
          result.builtinTotal += 1;
          if (tool.enabled) {
            result.builtinOn += 1;
          }
        }
      }
    }
    return result;
  }, [groups]);

  function reload() {
    setReloadToken((value) => value + 1);
  }

  async function persist(nextGroups, nextDisabled, previousGroups, previousDisabled) {
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
    setSaving(true);
    setGroups(nextGroups);
    setDisabledList(nextDisabled);
    try {
      const result = await updateAgent(
        name,
        { disabled_tools: nextDisabled },
        version
      );
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setNotice("Changes saved. Restart the daemon for them to take effect.");
    } catch (failure) {
      setGroups(previousGroups);
      setDisabledList(previousDisabled);
      if (isErrorCode(failure, "stale_version")) {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload the tools to get the latest version, then try again."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
    } finally {
      setSaving(false);
    }
  }

  function toggleTool(groupId, toolName, enabled) {
    if (saving) {
      return;
    }
    const previousGroups = groups;
    const previousDisabled = disabledList;
    const nextGroups = groups.map((group) => {
      if (group.id !== groupId) {
        return group;
      }
      return Object.assign({}, group, {
        tools: group.tools.map((tool) =>
          tool.name === toolName ? Object.assign({}, tool, { enabled }) : tool
        ),
      });
    });
    persist(
      nextGroups,
      collectDisabled(nextGroups, previousDisabled),
      previousGroups,
      previousDisabled
    );
  }

  function turnAllOff(groupId) {
    if (saving) {
      return;
    }
    const previousGroups = groups;
    const previousDisabled = disabledList;
    const nextGroups = groups.map((group) => {
      if (group.id !== groupId) {
        return group;
      }
      return Object.assign({}, group, {
        tools: group.tools.map((tool) =>
          Object.assign({}, tool, { enabled: false })
        ),
      });
    });
    persist(
      nextGroups,
      collectDisabled(nextGroups, previousDisabled),
      previousGroups,
      previousDisabled
    );
  }

  if (status === "loading") {
    return html`<div class="agent-tabs-state">
      <${Loading} label="Loading tools…" />
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
        <span class="sr-only">Search tools</span>
        <${Icon} name="search" />
        <input
          type="search"
          placeholder="Search tools"
          autocomplete="off"
          value=${query}
          onInput=${(event) => setQuery(event.target.value)}
        />
      </label>
      <span class="agent-tabs-toolbar-spacer"></span>
      <p class="agent-tabs-summary">
        ${counts.builtinOn} of ${counts.builtinTotal} built-in on${counts.mcpTotal
          ? " · " + counts.mcpOn + " from MCP"
          : ""}
      </p>
    </div>

    ${visibleGroups.length === 0
      ? html`<p class="agent-tabs-empty">
          ${query.trim()
            ? "No tools match “" + query + "”."
            : "No tools are configured for this agent."}
        </p>`
      : html`<div class="agent-tabs-table-wrap">
          <table class="agent-tabs-table">
            <thead>
              <tr>
                <th scope="col">Tool</th>
                <th scope="col" class="agent-tabs-col-risk">Risk</th>
                <th scope="col" class="agent-tabs-col-approval">Approval</th>
                <th scope="col" class="agent-tabs-col-switch">On</th>
              </tr>
            </thead>
            ${visibleGroups.map((group) => html`<tbody
              key=${group.id}
              class="agent-tabs-group"
            >
              <tr class="agent-tabs-group-row">
                <th scope="rowgroup" colspan="2">
                  <span class="agent-tabs-group-title">
                    ${group.label}
                    <span class="badge-ui agent-tabs-count">
                      <span class="badge-ui-dot" aria-hidden="true"></span>
                      ${group.tools.length}
                    </span>
                    ${isMcpGroup(group)
                      ? html`<span class="agent-tabs-group-kind">MCP server</span>`
                      : null}
                  </span>
                </th>
                <td colspan="2" class="agent-tabs-group-action">
                  <button
                    type="button"
                    class="agent-tabs-link-button"
                    disabled=${saving || group.tools.every((tool) => !tool.enabled)}
                    onClick=${() => turnAllOff(group.id)}
                  >Turn all off</button>
                </td>
              </tr>
              ${group.tools.map((tool) => {
                const labelId = domId(group.id + "-" + tool.name);
                return html`<tr key=${group.id + "/" + tool.name} class="agent-tabs-tool-row">
                  <th scope="row" id=${labelId}>
                    <span class="agent-tabs-tool-name">${tool.name}</span>
                  </th>
                  <td class="agent-tabs-risk">${riskLabel(tool.risk)}</td>
                  <td>
                    ${tool.asks_first
                      ? html`<span class="badge-ui agent-tabs-badge-warning">
                          <span class="badge-ui-dot" aria-hidden="true"></span>
                          Asks first
                        </span>`
                      : html`<span class="agent-tabs-none">—</span>`}
                  </td>
                  <td class="agent-tabs-switch-cell">
                    <label class="agent-switch">
                      <input
                        class="agent-switch-input"
                        type="checkbox"
                        role="switch"
                        aria-labelledby=${labelId}
                        checked=${tool.enabled}
                        disabled=${saving}
                        onChange=${(event) =>
                          toggleTool(group.id, tool.name, event.target.checked)}
                      />
                      <span class="agent-switch-track" aria-hidden="true">
                        <span class="agent-switch-thumb"></span>
                      </span>
                      <span class="agent-switch-text">
                        ${tool.enabled ? "On" : "Off"}
                      </span>
                    </label>
                  </td>
                </tr>`;
              })}
            </tbody>`)}
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
              >Reload tools</button>`
            : null}
        </div>`
      : null}
  </div>`;
}

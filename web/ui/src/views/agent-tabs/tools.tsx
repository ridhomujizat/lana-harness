/* eslint-disable react-hooks/set-state-in-effect -- the legacy tab resets its load state before each request, as in tools.js */
import { useEffect, useMemo, useState } from "react";
import { ApiError, errorMessage } from "../../api/client";
import { isErrorCode, loadAgentTab, updateAgent } from "../../api/agents";
import { getToolCatalog } from "../../api/tools";
import type { ToolEntry, ToolGroup } from "../../api/tools";
import { ErrorNotice, Loading } from "../../ui/ui";
import { Icon } from "../agent-common";

/* REQ-WEB-047, API-WEB-026: tool groups with On switches. A switch writes
   disabled_tools. MCP tool names are "<server>/<tool>" in the config. */

function isMcpGroup(group: ToolGroup): boolean {
  return group.id.indexOf("mcp:") === 0;
}

function qualifiedName(group: ToolGroup, tool: ToolEntry): string {
  return isMcpGroup(group)
    ? group.id.slice("mcp:".length) + "/" + tool.name
    : tool.name;
}

function collectDisabled(
  groups: ToolGroup[],
  previousDisabled: string[]
): string[] {
  const names: string[] = [];
  const known = new Set<string>();
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

function domId(value: unknown): string {
  return "agent-tool-" + String(value).replace(/[^a-zA-Z0-9_-]/g, "-");
}

function riskLabel(value: unknown): string {
  const text = String(value || "");
  if (!text) {
    return "—";
  }
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function normalizeGroups(value: unknown): ToolGroup[] {
  const groups = Array.isArray(value) ? value : [];
  return groups.map((group: unknown) => {
    const record =
      group && typeof group === "object"
        ? (group as Record<string, unknown>)
        : {};
    const tools = Array.isArray(record.tools) ? record.tools : [];
    return {
      id: String(record.id ? record.id : ""),
      label: String(record.label ? record.label : ""),
      tools: tools.map((tool: unknown) => {
        const item =
          tool && typeof tool === "object"
            ? (tool as Record<string, unknown>)
            : {};
        return {
          name: String(item.name ? item.name : ""),
          risk: String(item.risk ? item.risk : ""),
          asks_first: Boolean(item.asks_first),
          enabled: Boolean(item.enabled),
        };
      }),
    };
  });
}

export interface ToolsTabProps {
  name: string;
}

export function ToolsTab({ name }: ToolsTabProps) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [groups, setGroups] = useState<ToolGroup[]>([]);
  const [disabledList, setDisabledList] = useState<string[]>([]);
  const [version, setVersion] = useState("");
  const [query, setQuery] = useState("");
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

  async function persist(
    nextGroups: ToolGroup[],
    nextDisabled: string[],
    previousGroups: ToolGroup[],
    previousDisabled: string[]
  ) {
    if (saving) {
      return;
    }
    setError("");
    setDetails([]);
    setStale(false);
    setNotice("");
    if (!version) {
      setError(
        "The config version is unavailable. Reload the page and try again."
      );
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

  function toggleTool(groupId: string, toolName: string, enabled: boolean) {
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
    void persist(
      nextGroups,
      collectDisabled(nextGroups, previousDisabled),
      previousGroups,
      previousDisabled
    );
  }

  function turnAllOff(groupId: string) {
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
    void persist(
      nextGroups,
      collectDisabled(nextGroups, previousDisabled),
      previousGroups,
      previousDisabled
    );
  }

  if (status === "loading") {
    return (
      <div className="agent-tabs-state">
        <Loading label="Loading tools…" />
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
          className="button-ui button-ui-secondary"
          onClick={reload}
        >
          Try again
        </button>
      </div>
    );
  }

  const summaryText =
    counts.builtinOn +
    " of " +
    counts.builtinTotal +
    " built-in on" +
    (counts.mcpTotal ? " · " + counts.mcpOn + " from MCP" : "");

  return (
    <div className="agent-tabs-pane">
      <div className="agent-tabs-toolbar">
        <label className="agent-tabs-search">
          <span className="sr-only">Search tools</span>
          <Icon name="search" />
          <input
            type="search"
            placeholder="Search tools"
            autoComplete="off"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <span className="agent-tabs-toolbar-spacer"></span>
        <p className="agent-tabs-summary">{summaryText}</p>
      </div>

      {visibleGroups.length === 0 ? (
        <p className="agent-tabs-empty">
          {query.trim()
            ? "No tools match “" + query + "”."
            : "No tools are configured for this agent."}
        </p>
      ) : (
        <div className="agent-tabs-table-wrap">
          <table className="agent-tabs-table">
            <thead>
              <tr>
                <th scope="col">Tool</th>
                <th scope="col" className="agent-tabs-col-risk">
                  Risk
                </th>
                <th scope="col" className="agent-tabs-col-approval">
                  Approval
                </th>
                <th scope="col" className="agent-tabs-col-switch">
                  On
                </th>
              </tr>
            </thead>
            {visibleGroups.map((group) => (
              <tbody key={group.id} className="agent-tabs-group">
                <tr className="agent-tabs-group-row">
                  <th scope="rowgroup" colSpan={2}>
                    <span className="agent-tabs-group-title">
                      {group.label}{" "}
                      <span className="badge-ui agent-tabs-count">
                        <span className="badge-ui-dot" aria-hidden="true"></span>
                        {group.tools.length}
                      </span>{" "}
                      {isMcpGroup(group) ? (
                        <span className="agent-tabs-group-kind">MCP server</span>
                      ) : null}
                    </span>
                  </th>
                  <td colSpan={2} className="agent-tabs-group-action">
                    <button
                      type="button"
                      className="agent-tabs-link-button"
                      disabled={
                        saving || group.tools.every((tool) => !tool.enabled)
                      }
                      onClick={() => turnAllOff(group.id)}
                    >
                      Turn all off
                    </button>
                  </td>
                </tr>
                {group.tools.map((tool) => {
                  const labelId = domId(group.id + "-" + tool.name);
                  return (
                    <tr
                      key={group.id + "/" + tool.name}
                      className="agent-tabs-tool-row"
                    >
                      <th scope="row" id={labelId}>
                        <span className="agent-tabs-tool-name">{tool.name}</span>
                      </th>
                      <td className="agent-tabs-risk">{riskLabel(tool.risk)}</td>
                      <td>
                        {tool.asks_first ? (
                          <span className="badge-ui agent-tabs-badge-warning">
                            <span
                              className="badge-ui-dot"
                              aria-hidden="true"
                            ></span>
                            Asks first
                          </span>
                        ) : (
                          <span className="agent-tabs-none">—</span>
                        )}
                      </td>
                      <td className="agent-tabs-switch-cell">
                        <label className="agent-switch">
                          <input
                            className="agent-switch-input"
                            type="checkbox"
                            role="switch"
                            aria-labelledby={labelId}
                            checked={tool.enabled}
                            disabled={saving}
                            onChange={(event) =>
                              toggleTool(
                                group.id,
                                tool.name,
                                event.target.checked
                              )
                            }
                          />
                          <span
                            className="agent-switch-track"
                            aria-hidden="true"
                          >
                            <span className="agent-switch-thumb"></span>
                          </span>
                          <span className="agent-switch-text">
                            {tool.enabled ? "On" : "Off"}
                          </span>
                        </label>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            ))}
          </table>
        </div>
      )}

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
              className="button-ui button-ui-secondary"
              onClick={reload}
            >
              Reload tools
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

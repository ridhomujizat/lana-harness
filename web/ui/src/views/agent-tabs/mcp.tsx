/* eslint-disable react-hooks/set-state-in-effect -- the legacy tab resets its load state before each request, as in mcp.js */
import { useEffect, useMemo, useState } from "react";
import { ApiError, errorMessage } from "../../api/client";
import { isErrorCode, loadAgentTab, updateAgent } from "../../api/agents";
import { listMcp } from "../../api/mcp";
import type { McpServerRow } from "../../api/mcp";
import { ErrorNotice, Loading } from "../../ui/ui";
import { Ic } from "../../ui/icons";

/* REQ-WEB-047: the MCP servers with tool count and status. The On switch
   edits the agent's mcp list; adding a server lives on the MCP page. */

function statusMeta(server: McpServerRow): { label: string; tone: string } {
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
    return { label: "Connected", tone: "green" };
  }
  if (value === "unreachable") {
    return { label: "Unreachable", tone: "red" };
  }
  if (value === "needs_sign_in") {
    return { label: "Needs sign-in", tone: "amber" };
  }
  if (value === "idle") {
    return { label: "Idle", tone: "" };
  }
  if (value) {
    return { label: value.charAt(0).toUpperCase() + value.slice(1), tone: "" };
  }
  return { label: "Idle", tone: "" };
}

function serverDescription(server: McpServerRow): string {
  if (server && server.type === "url") {
    return server.url || "No URL set.";
  }
  const args = Array.isArray(server && server.args) ? server.args : [];
  const parts = [server && server.command ? String(server.command) : ""]
    .concat(args.map(String))
    .filter(Boolean);
  return parts.length > 0 ? parts.join(" ") : "No command set.";
}

function uniqueNames(values: unknown[]): string[] {
  const names: string[] = [];
  for (const value of values) {
    const name = String(value || "");
    if (name && names.indexOf(name) < 0) {
      names.push(name);
    }
  }
  return names;
}

function domId(value: unknown): string {
  return "agent-mcp-" + String(value).replace(/[^a-zA-Z0-9_-]/g, "-");
}

export interface McpTabProps {
  name: string;
}

export function McpTab({ name }: McpTabProps) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [servers, setServers] = useState<McpServerRow[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
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

  async function toggleServer(serverName: string, enabled: boolean) {
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
    return (
      <div className="agent-tabs-state">
        <Loading label="Loading MCP servers…" />
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

  const summaryText =
    servers.length +
    " " +
    (servers.length === 1 ? "server" : "servers") +
    " · " +
    onCount +
    " on";

  return (
    <div className="agent-tabs-pane">
      <div className="agent-tabs-toolbar">
        <label className="agent-tabs-search">
          <span className="sr-only">Search servers</span>
          <Ic name="search" size={13} />
          <input
            type="search"
            placeholder="Search servers"
            autoComplete="off"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <span className="agent-tabs-toolbar-spacer"></span>
        <p className="agent-tabs-summary">{summaryText}</p>
        <a className="btn btn-primary btn-sm" href="#/mcp">
          <Ic name="plus" size={13} />
          Add server
        </a>
      </div>

      {servers.length === 0 ? (
        <p className="agent-tabs-empty">
          No MCP servers configured. Add one on the MCP page.
        </p>
      ) : visibleServers.length === 0 ? (
        <p className="agent-tabs-empty">No servers match “{query}”.</p>
      ) : (
        <div className="perm-t agent-tabs-table-wrap">
          <table className="perm-t agent-tabs-table agent-mcp-table">
            <thead>
              <tr>
                <th scope="col">Server</th>
                <th scope="col" className="agent-mcp-col-tools">
                  Tools
                </th>
                <th scope="col" className="agent-mcp-col-status">
                  Status
                </th>
                <th scope="col" className="agent-tabs-col-switch">
                  On
                </th>
                <th scope="col" className="agent-tabs-col-action">
                  <span className="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {visibleServers.map((server) => {
                const meta = statusMeta(server);
                const labelId = domId(server.name);
                const enabled = selectedSet.has(server.name);
                return (
                  <tr key={server.name}>
                    <th scope="row" className="agent-mcp-name-cell">
                      <span className="agent-tabs-namecell">
                      <span className="agent-tabs-tile">
                        <Ic name="plug" size={15} />
                      </span>
                      <span className="agent-mcp-text">
                        <span className="agent-mcp-name">{server.name}</span>
                        <span className="agent-mcp-command">
                          {serverDescription(server)}
                        </span>
                      </span>
                      </span>
                    </th>
                    <td className="agent-mcp-tools">
                      {Number(server.tools_count) || 0}
                    </td>
                    <td>
                      <span
                        className={
                          "badge" +
                          (meta.tone ? " " + meta.tone : "")
                        }
                      >
                        <span
                          className="dot"
                          aria-hidden="true"
                        ></span>
                        {meta.label}
                      </span>
                    </td>
                    <td className="agent-tabs-switch-cell">
                      <label className="agent-switch">
                        <input
                          className="toggle"
                          type="checkbox"
                          role="switch"
                          aria-labelledby={labelId}
                          checked={enabled}
                          disabled={saving}
                          onChange={(event) =>
                            toggleServer(server.name, event.target.checked)
                          }
                        />
                        <span className="agent-switch-text">
                          {enabled ? "On" : "Off"}
                        </span>
                      </label>
                    </td>
                    <td className="agent-tabs-col-action">
                      <a
                        className="agent-tabs-row-link"
                        href={"#/mcp/" + encodeURIComponent(server.name)}
                        aria-label={
                          "Open " + server.name + " on the MCP page"
                        }
                      >
                        <Ic name="chevron-right" size={15} />
                      </a>
                    </td>
                  </tr>
                );
              })}
            </tbody>
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
              className="btn btn-secondary btn-sm"
              onClick={reload}
            >
              Reload servers
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

const state = {
  csrf: "",
};

let unauthorizedHandler = null;

export class ApiError extends Error {
  constructor(status, code, message, details) {
    super(message || "The request failed.");
    this.name = "ApiError";
    this.status = status;
    this.code = code || "internal_error";
    this.details = Array.isArray(details) ? details : [];
  }

  get pid() {
    for (const detail of this.details) {
      if (detail && typeof detail.pid === "number") {
        return detail.pid;
      }
    }
    return null;
  }
}

export function setCSRF(value) {
  state.csrf = typeof value === "string" ? value : "";
}

export function setUnauthorizedHandler(handler) {
  unauthorizedHandler = typeof handler === "function" ? handler : null;
}

export function errorMessage(error) {
  if (error instanceof ApiError) {
    if (error.status === 401) {
      return "Please log in again.";
    }
    return error.message || "The request failed.";
  }
  return "Could not reach the daemon. Check that it is still running.";
}

function queryString(params) {
  const search = new URLSearchParams();
  for (const key of Object.keys(params || {})) {
    const value = params[key];
    if (value === undefined || value === null || value === "") {
      continue;
    }
    search.set(key, String(value));
  }
  const text = search.toString();
  return text ? "?" + text : "";
}

export async function request(method, path, body) {
  const headers = { Accept: "application/json" };
  const options = {
    method,
    headers,
    credentials: "same-origin",
  };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  if (method !== "GET" && method !== "HEAD" && state.csrf) {
    headers["X-Lana-CSRF"] = state.csrf;
  }

  let response;
  try {
    response = await fetch(path, options);
  } catch (cause) {
    throw new ApiError(0, "network_error", "Could not reach the daemon.", []);
  }

  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch (cause) {
      data = null;
    }
  }

  if (!response.ok) {
    const envelope = data && data.error ? data.error : null;
    const error = new ApiError(
      response.status,
      envelope ? envelope.code : "http_" + response.status,
      envelope ? envelope.message : text || "The request failed with status " + response.status + ".",
      envelope ? envelope.details : []
    );
    if (response.status === 401 && unauthorizedHandler) {
      unauthorizedHandler(error);
    }
    throw error;
  }
  return data;
}

export const api = {
  login(token) {
    return request("POST", "/v1/auth/login", { token });
  },

  authSession() {
    return request("GET", "/v1/auth/session");
  },

  daemonInfo() {
    return request("GET", "/v1/daemon");
  },

  logout() {
    return request("POST", "/v1/auth/logout");
  },

  listSessions(params) {
    return request("GET", "/v1/sessions" + queryString(params));
  },

  getSession(id) {
    return request("GET", "/v1/sessions/" + encodeURIComponent(id));
  },

  listEvents(id, fromSeq) {
    return request(
      "GET",
      "/v1/sessions/" + encodeURIComponent(id) + "/events" + queryString({ from_seq: fromSeq })
    );
  },

  listAgents() {
    return request("GET", "/v1/agents");
  },

  createAgent(body) {
    return request("POST", "/v1/agents", body);
  },

  updateAgent(name, fields, expectedVersion) {
    const body = Object.assign({}, fields, {
      expected_version: expectedVersion,
    });
    return request("PUT", "/v1/agents/" + encodeURIComponent(name), body);
  },

  duplicateAgent(name, newName) {
    return request(
      "POST",
      "/v1/agents/" + encodeURIComponent(name) + "/duplicate",
      { name: newName }
    );
  },

  listProviders() {
    return request("GET", "/v1/providers");
  },

  createSession(agent, model) {
    return request("POST", "/v1/sessions", { agent, model: model || "" });
  },

  patchSession(id, patch) {
    return request("PATCH", "/v1/sessions/" + encodeURIComponent(id), patch);
  },

  sendMessage(id, text) {
    return request("POST", "/v1/sessions/" + encodeURIComponent(id) + "/messages", { text });
  },

  cancelTurn(id) {
    return request("POST", "/v1/sessions/" + encodeURIComponent(id) + "/cancel");
  },

  compactSession(id) {
    return request("POST", "/v1/sessions/" + encodeURIComponent(id) + "/compact");
  },

  forkSession(id) {
    return request("POST", "/v1/sessions/" + encodeURIComponent(id) + "/fork");
  },

  answerApproval(requestId, outcome, reason) {
    const body = { outcome };
    if (reason) {
      body.reason = reason;
    }
    return request("POST", "/v1/approvals/" + encodeURIComponent(requestId), body);
  },

  getConfig() {
    return request("GET", "/v1/config");
  },

  saveConfig(raw, expectedVersion) {
    return request("PUT", "/v1/config", {
      raw,
      expected_version: expectedVersion,
    });
  },

  listSecrets() {
    return request("GET", "/v1/secrets");
  },

  setSecret(name, value, expectedVersion) {
    return request("PUT", "/v1/secrets/" + encodeURIComponent(name), {
      value,
      expected_version: expectedVersion,
    });
  },

  deleteSecret(name, expectedVersion) {
    return request("DELETE", "/v1/secrets/" + encodeURIComponent(name), {
      expected_version: expectedVersion,
    });
  },

  getAgentFile(agent, file) {
    return request(
      "GET",
      "/v1/agents/" + encodeURIComponent(agent) + "/files/" + encodeURIComponent(file)
    );
  },

  saveAgentFile(agent, file, content, expectedVersion) {
    return request(
      "PUT",
      "/v1/agents/" + encodeURIComponent(agent) + "/files/" + encodeURIComponent(file),
      { content, expected_version: expectedVersion }
    );
  },

  listSkills(scope) {
    return request("GET", "/v1/skills" + queryString({ scope }));
  },

  getSkill(scope, name) {
    return request(
      "GET",
      "/v1/skills/" + encodeURIComponent(scope) + "/" + encodeURIComponent(name)
    );
  },

  saveSkill(scope, name, content, expectedVersion) {
    return request(
      "PUT",
      "/v1/skills/" + encodeURIComponent(scope) + "/" + encodeURIComponent(name),
      { content, expected_version: expectedVersion || "" }
    );
  },
};

function dispatchBlock(block, handlers, stateRef) {
  const lines = block.split("\n");
  let eventName = "message";
  let eventId = "";
  const dataLines = [];

  for (const rawLine of lines) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
    if (!line || line.startsWith(":")) {
      continue;
    }
    const colon = line.indexOf(":");
    const field = colon >= 0 ? line.slice(0, colon) : line;
    let value = colon >= 0 ? line.slice(colon + 1) : "";
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    if (field === "event") {
      eventName = value;
    } else if (field === "id") {
      eventId = value;
    } else if (field === "data") {
      dataLines.push(value);
    }
  }

  if (dataLines.length === 0) {
    return;
  }

  let payload;
  try {
    payload = JSON.parse(dataLines.join("\n"));
  } catch (cause) {
    return;
  }

  if (eventName === "event") {
    if (eventId) {
      const seq = Number(eventId);
      if (Number.isFinite(seq) && seq > stateRef.lastSeq) {
        stateRef.lastSeq = seq;
      }
    }
    if (handlers.onEvent) {
      handlers.onEvent(payload);
    }
    return;
  }
  if (eventName === "frame") {
    if (handlers.onFrame) {
      handlers.onFrame(payload);
    }
    return;
  }
  if (eventName === "status") {
    if (handlers.onStatus) {
      handlers.onStatus(payload);
    }
    return;
  }
  if (eventName === "turn") {
    if (handlers.onTurn) {
      handlers.onTurn(payload);
    }
    return;
  }
  if (eventName === "approval") {
    if (handlers.onApproval) {
      handlers.onApproval(payload);
    }
    return;
  }
  if (eventName === "question") {
    if (handlers.onQuestion) {
      handlers.onQuestion(payload);
    }
    return;
  }
  if (eventName === "approval_closed") {
    if (handlers.onApprovalClosed) {
      handlers.onApprovalClosed(payload);
    }
    return;
  }
  if (eventName === "shutdown") {
    if (handlers.onShutdown) {
      handlers.onShutdown(payload);
    }
  }
}

export function openStream(id, options) {
  const handlers = options || {};
  const stateRef = { lastSeq: Number(handlers.fromSeq) || 0 };
  let closed = false;
  let controller = null;
  let retryTimer = null;

  function scheduleReconnect() {
    if (closed) {
      return;
    }
    if (handlers.onState) {
      handlers.onState("reconnecting");
    }
    retryTimer = setTimeout(connect, 1000);
  }

  async function consume(response) {
    if (!response.body) {
      scheduleReconnect();
      return;
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    try {
      while (!closed) {
        const result = await reader.read();
        if (result.done) {
          break;
        }
        buffer += decoder.decode(result.value, { stream: true });
        let index = buffer.indexOf("\n\n");
        while (index >= 0) {
          const block = buffer.slice(0, index);
          buffer = buffer.slice(index + 2);
          dispatchBlock(block, handlers, stateRef);
          index = buffer.indexOf("\n\n");
        }
      }
    } catch (cause) {
      if (!closed && handlers.onState) {
        handlers.onState("reconnecting");
      }
    } finally {
      try {
        reader.releaseLock();
      } catch (cause) {
        /* The stream is already closed. */
      }
    }
    if (!closed) {
      scheduleReconnect();
    }
  }

  async function connect() {
    if (closed) {
      return;
    }
    controller = new AbortController();
    const headers = { Accept: "text/event-stream" };
    if (stateRef.lastSeq > 0) {
      headers["Last-Event-ID"] = String(stateRef.lastSeq);
    }
    const path =
      "/v1/sessions/" +
      encodeURIComponent(id) +
      "/stream" +
      (stateRef.lastSeq > 0 ? "?from_seq=" + stateRef.lastSeq : "");

    let response;
    try {
      response = await fetch(path, {
        method: "GET",
        headers,
        credentials: "same-origin",
        signal: controller.signal,
      });
    } catch (cause) {
      if (!closed) {
        scheduleReconnect();
      }
      return;
    }

    if (response.status === 401) {
      if (handlers.onUnauthorized) {
        handlers.onUnauthorized();
      }
      return;
    }
    if (!response.ok) {
      if (handlers.onError) {
        handlers.onError(response.status);
      }
      if (response.status !== 404) {
        scheduleReconnect();
      }
      return;
    }
    if (handlers.onState) {
      handlers.onState("connected");
    }
    await consume(response);
  }

  connect();

  return {
    close() {
      closed = true;
      if (retryTimer !== null) {
        clearTimeout(retryTimer);
        retryTimer = null;
      }
      if (controller) {
        controller.abort();
      }
    },
  };
}

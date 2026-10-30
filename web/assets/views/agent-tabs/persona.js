import { html, useState, useEffect } from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
import { ErrorNotice, Loading } from "/assets/ui.js";
import {
  getAgentFile,
  saveAgentFile,
  isErrorCode,
} from "/assets/api/agent-tabs.js";

/* REQ-WEB-047: AGENT.md, USER.md, and MEMORY.md with a counter, Revert, and
   Save. USER.md is the one shared file; the API labels it "user". */

const FILES = [
  {
    id: "agent",
    name: "AGENT.md",
    title: "Persona",
    note: "Persona. How this agent talks and works.",
    help: "Written in Markdown. {{name}} becomes the agent name.",
    fallbackLimit: null,
    saveMessage: "Saved AGENT.md. The next turn uses the new text.",
  },
  {
    id: "user",
    name: "USER.md",
    title: "About you",
    note: "About you. Shared by every agent.",
    help: "About you. Shared by every agent.",
    fallbackLimit: 2000,
    saveMessage:
      "Saved USER.md. Every agent uses the new text from its next turn.",
  },
  {
    id: "memory",
    name: "MEMORY.md",
    title: "Memory",
    note: "Memory. Notes the agent keeps.",
    help: "Notes the agent keeps between turns.",
    fallbackLimit: 4000,
    saveMessage: "Saved MEMORY.md. The next turn uses the new memory.",
  },
];

function characterCount(text) {
  return Array.from(String(text === undefined || text === null ? "" : text))
    .length;
}

function formatNumber(value) {
  return String(Number(value) || 0).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

function fileFromResponse(file, data) {
  const content = data && typeof data.content === "string" ? data.content : "";
  const reported = Number(data && data.characters);
  const reportedLimit = Number(data && data.limit);
  return {
    content,
    baseline: content,
    version: data && data.version ? String(data.version) : "",
    characters:
      Number.isFinite(reported) && reported >= 0
        ? reported
        : characterCount(content),
    limit:
      Number.isFinite(reportedLimit) && reportedLimit > 0
        ? reportedLimit
        : file.fallbackLimit,
  };
}

export function PersonaTab({ name }) {
  const [fileId, setFileId] = useState("agent");
  const [files, setFiles] = useState(null);
  const [status, setStatus] = useState("loading");
  const [reloadToken, setReloadToken] = useState(0);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [stale, setStale] = useState(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    setStale(false);
    setNotice("");
    Promise.all(
      FILES.map((file) =>
        getAgentFile(name, file.id).then((data) => [
          file.id,
          fileFromResponse(file, data),
        ])
      )
    )
      .then((entries) => {
        if (!alive) {
          return;
        }
        const next = {};
        for (const entry of entries) {
          next[entry[0]] = entry[1];
        }
        setFiles(next);
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

  const meta = FILES.find((file) => file.id === fileId) || FILES[0];
  const current = files ? files[fileId] : null;

  function reload() {
    setReloadToken((value) => value + 1);
  }

  function selectFile(nextId) {
    setFileId(nextId);
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
  }

  function updateContent(value) {
    setFiles((existing) => {
      if (!existing) {
        return existing;
      }
      const entry = existing[fileId];
      return Object.assign({}, existing, {
        [fileId]: Object.assign({}, entry, {
          content: value,
          characters: characterCount(value),
        }),
      });
    });
    setNotice("");
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function revert() {
    if (!current) {
      return;
    }
    setFiles((existing) =>
      Object.assign({}, existing, {
        [fileId]: Object.assign({}, existing[fileId], {
          content: existing[fileId].baseline,
          characters: characterCount(existing[fileId].baseline),
        }),
      })
    );
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
  }

  async function save(event) {
    event.preventDefault();
    if (!current || saving) {
      return;
    }
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
    if (!current.version) {
      setError("The file version is unavailable. Reload the file and try again.");
      return;
    }
    if (current.content === current.baseline) {
      setNotice("No changes to save.");
      return;
    }
    setSaving(true);
    try {
      const result = await saveAgentFile(
        name,
        fileId,
        current.content,
        current.version
      );
      const characters = Number(result && result.characters);
      setFiles((existing) =>
        Object.assign({}, existing, {
          [fileId]: Object.assign({}, existing[fileId], {
            baseline: existing[fileId].content,
            version:
              result && result.version
                ? String(result.version)
                : existing[fileId].version,
            characters:
              Number.isFinite(characters) && characters >= 0
                ? characters
                : characterCount(existing[fileId].content),
          }),
        })
      );
      setNotice(meta.saveMessage);
    } catch (failure) {
      if (isErrorCode(failure, "over_limit")) {
        const limit = current.limit || meta.fallbackLimit || 0;
        setError(
          "This file has " +
            formatNumber(current.characters) +
            " characters; the limit is " +
            formatNumber(limit) +
            ". Shorten it, then save again."
        );
      } else if (isErrorCode(failure, "stale_version")) {
        setStale(true);
        setError(
          meta.name +
            " changed since you loaded it. Reload the file to get the latest version, then save again."
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
      <${Loading} label="Loading persona files…" />
    </div>`;
  }

  if (status === "error" || !current) {
    return html`<div class="agent-tabs-state">
      <div role="alert">
        <${ErrorNotice} message=${error} details=${details} />
      </div>
      <button type="button" class="button-ui button-ui-secondary" onClick=${reload}>
        Try again
      </button>
    </div>`;
  }

  const dirty = current.content !== current.baseline;
  const over = current.limit !== null && current.characters > current.limit;

  return html`<div class="agent-persona">
    <div class="agent-persona-files" role="group" aria-label="Persona files">
      ${FILES.map((file) => {
        const entry = files[file.id];
        const active = file.id === fileId;
        const count =
          entry.limit !== null
            ? formatNumber(entry.characters) + " / " + formatNumber(entry.limit)
            : formatNumber(entry.characters);
        return html`<button
          key=${file.id}
          type="button"
          class=${"agent-persona-file" + (active ? " is-active" : "")}
          aria-pressed=${active ? "true" : "false"}
          disabled=${saving}
          onClick=${() => selectFile(file.id)}
        >
          <span class="agent-persona-file-row">
            <span class="agent-persona-file-name">${file.name}</span>
            <span class="agent-persona-file-count">${count}</span>
          </span>
          <span class="agent-persona-file-note">${file.note}</span>
        </button>`;
      })}
    </div>

    <form class="agent-persona-editor" onSubmit=${save} noValidate>
      <div class="agent-persona-heading">
        <h2 id="agent-persona-title">${meta.title}</h2>
        <p id="agent-persona-help">${meta.help}</p>
      </div>
      <label class="sr-only" for="agent-persona-editor">${meta.name} text</label>
      <textarea
        id="agent-persona-editor"
        class="agent-persona-textarea"
        aria-labelledby="agent-persona-title"
        aria-describedby="agent-persona-help agent-persona-count"
        spellcheck="false"
        value=${current.content}
        disabled=${saving}
        onInput=${(event) => updateContent(event.target.value)}
      ></textarea>

      <div class="agent-persona-footer">
        <p
          id="agent-persona-count"
          class=${"agent-persona-count" + (over ? " is-over" : "")}
        >
          ${formatNumber(current.characters)} characters${current.limit !== null
            ? " / " + formatNumber(current.limit)
            : ""}${dirty ? " · Unsaved changes" : ""}
        </p>
        <div class="agent-persona-actions">
          <button
            type="button"
            class="button-ui button-ui-secondary"
            disabled=${!dirty || saving}
            onClick=${revert}
          >Revert</button>
          <button
            type="submit"
            class="button-ui button-ui-primary"
            disabled=${!dirty || saving}
          >${saving ? "Saving…" : "Save"}</button>
        </div>
      </div>

      ${over
        ? html`<p class="agent-persona-over" role="alert">
            Over the limit by
            ${formatNumber(current.characters - current.limit)} characters.
            Shorten it before saving.
          </p>`
        : null}

      <div class="agent-tabs-live" role="status" aria-live="polite">
        ${notice
          ? html`<p class="notice notice-ok agent-tabs-notice">${notice}</p>`
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
                  disabled=${saving}
                >Reload file</button>`
              : null}
          </div>`
        : null}
    </form>
  </div>`;
}

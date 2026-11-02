/* eslint-disable react-hooks/set-state-in-effect -- the legacy tab resets its load state before each request, as in persona.js */
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, errorMessage } from "../../api/client";
import { getAgentFile, saveAgentFile, isErrorCode } from "../../api/agents";
import type { AgentFileResponse } from "../../api/agents";
import { ErrorNotice, Loading } from "../../ui/ui";

/* REQ-WEB-047: AGENT.md, USER.md, and MEMORY.md with a counter, Revert, and
   Save. USER.md is the one shared file; the API labels it "user". */

interface PersonaFile {
  id: string;
  name: string;
  title: string;
  note: string;
  help: string;
  fallbackLimit: number | null;
  saveMessage: string;
}

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
  {
    id: "job",
    name: "JOB.md",
    title: "Job",
    note: "Job. What this agent is responsible for.",
    help: "Written in Markdown. The agent reads it on every turn.",
    fallbackLimit: 4000,
    saveMessage: "Saved JOB.md. The next turn uses the new job.",
  },
] as const satisfies readonly PersonaFile[];

interface PersonaFileState {
  content: string;
  baseline: string;
  version: string;
  characters: number;
  limit: number | null;
}

const EMPTY_FILE: PersonaFileState = {
  content: "",
  baseline: "",
  version: "",
  characters: 0,
  limit: null,
};

function characterCount(text: unknown): number {
  return Array.from(String(text === undefined || text === null ? "" : text))
    .length;
}

function formatNumber(value: unknown): string {
  return String(Number(value) || 0).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

function fileFromResponse(
  file: PersonaFile,
  data: AgentFileResponse
): PersonaFileState {
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

export interface PersonaTabProps {
  name: string;
}

export function PersonaTab({ name }: PersonaTabProps) {
  const [fileId, setFileId] = useState("agent");
  const [files, setFiles] = useState<Record<string, PersonaFileState> | null>(
    null
  );
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [reloadToken, setReloadToken] = useState(0);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
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
        getAgentFile(name, file.id).then(
          (data) => [file.id, fileFromResponse(file, data)] as const
        )
      )
    )
      .then((entries) => {
        if (!alive) {
          return;
        }
        const next: Record<string, PersonaFileState> = {};
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
  const current = files ? files[fileId] ?? null : null;

  function reload() {
    setReloadToken((value) => value + 1);
  }

  function selectFile(nextId: string) {
    setFileId(nextId);
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
  }

  function updateContent(value: string) {
    setFiles((existing) => {
      if (!existing) {
        return existing;
      }
      const entry = existing[fileId] ?? EMPTY_FILE;
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
    setFiles((existing) => {
      if (!existing) {
        return existing;
      }
      const entry = existing[fileId];
      if (!entry) {
        return existing;
      }
      return Object.assign({}, existing, {
        [fileId]: Object.assign({}, entry, {
          content: entry.baseline,
          characters: characterCount(entry.baseline),
        }),
      });
    });
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!current || saving) {
      return;
    }
    setNotice("");
    setError("");
    setDetails([]);
    setStale(false);
    if (!current.version) {
      setError(
        "The file version is unavailable. Reload the file and try again."
      );
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
      setFiles((existing) => {
        if (!existing) {
          return existing;
        }
        const entry = existing[fileId];
        if (!entry) {
          return existing;
        }
        return Object.assign({}, existing, {
          [fileId]: Object.assign({}, entry, {
            baseline: entry.content,
            version:
              result && result.version ? String(result.version) : entry.version,
            characters:
              Number.isFinite(characters) && characters >= 0
                ? characters
                : characterCount(entry.content),
          }),
        });
      });
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
    return (
      <div className="agent-tabs-state">
        <Loading label="Loading persona files…" />
      </div>
    );
  }

  if (status === "error" || !current) {
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

  const dirty = current.content !== current.baseline;
  const over = current.limit !== null && current.characters > current.limit;
  const overBy =
    current.limit === null ? 0 : current.characters - current.limit;
  const countLabel =
    formatNumber(current.characters) +
    (current.limit !== null ? " / " + formatNumber(current.limit) : "") +
    (dirty ? " · Unsaved changes" : "");

  return (
    <div className="agent-persona">
      <div
        className="agent-persona-files"
        role="group"
        aria-label="Persona files"
      >
        {FILES.map((file) => {
          const entry = files?.[file.id];
          if (!entry) {
            return null;
          }
          const active = file.id === fileId;
          const count =
            entry.limit !== null
              ? formatNumber(entry.characters) +
                " / " +
                formatNumber(entry.limit)
              : formatNumber(entry.characters);
          return (
            <button
              key={file.id}
              type="button"
              className={"agent-persona-file" + (active ? " is-active" : "")}
              aria-pressed={active ? "true" : "false"}
              disabled={saving}
              onClick={() => selectFile(file.id)}
            >
              <span className="agent-persona-file-row">
                <span className="agent-persona-file-name">{file.name}</span>
                <span className="agent-persona-file-count">{count}</span>
              </span>
              <span className="agent-persona-file-note">{file.note}</span>
            </button>
          );
        })}
      </div>

      <form className="agent-persona-editor" onSubmit={save} noValidate>
        <div className="agent-persona-heading">
          <h2 id="agent-persona-title">{meta.title}</h2>
          <p id="agent-persona-help">{meta.help}</p>
        </div>
        <label className="sr-only" htmlFor="agent-persona-editor">
          {meta.name} text
        </label>
        <textarea
          id="agent-persona-editor"
          className="agent-persona-textarea"
          aria-labelledby="agent-persona-title"
          aria-describedby="agent-persona-help agent-persona-count"
          spellCheck={false}
          value={current.content}
          disabled={saving}
          onChange={(event) => updateContent(event.target.value)}
        ></textarea>

        <div className="agent-persona-footer">
          <p
            id="agent-persona-count"
            className={"agent-persona-count" + (over ? " is-over" : "")}
          >
            {countLabel}
          </p>
          <div className="agent-persona-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={!dirty || saving}
              onClick={revert}
            >
              Revert
            </button>
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={!dirty || saving}
            >
              {saving ? "Saving…" : "Save"}
            </button>
          </div>
        </div>

        {over ? (
          <p className="agent-persona-over" role="alert">
            Over the limit by {formatNumber(overBy)} characters. Shorten it
            before saving.
          </p>
        ) : null}

        <div className="agent-tabs-live" role="status" aria-live="polite">
          {notice ? (
            <p className="notice notice-ok agent-tabs-notice">{notice}</p>
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
                disabled={saving}
              >
                Reload file
              </button>
            ) : null}
          </div>
        ) : null}
      </form>
    </div>
  );
}

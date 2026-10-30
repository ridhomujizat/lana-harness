import {
  html,
  useState,
  useEffect,
  useMemo,
} from "/assets/vendor/preact-htm.js";
import { errorMessage, ApiError } from "/assets/api.js";
import { Dialog, ErrorNotice, Loading } from "/assets/ui.js";
import {
  Icon,
  AgentAvatar,
  agoLabel,
} from "/assets/views/agent-common.js";
import {
  loadAgentTab,
  listSkills,
  updateAgent,
  isErrorCode,
} from "/assets/api/agent-tabs.js";

/* REQ-WEB-047: the agent's skills list. Removing a name changes only the
   agent's config; the skill files stay. Adding picks global skills. */

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

function AddSkillsDialog({ agent, taken, catalog, version, onClose, onAdded }) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const takenSet = useMemo(() => new Set(taken), [taken]);

  const options = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const globals = (catalog.valid || []).filter(
      (skill) => skill && skill.scope === "global"
    );
    const filtered = needle
      ? globals.filter((skill) => {
          return (
            String(skill.name || "").toLowerCase().includes(needle) ||
            String(skill.description || "").toLowerCase().includes(needle)
          );
        })
      : globals;
    return filtered.slice().sort((left, right) =>
      String(left.name).localeCompare(String(right.name))
    );
  }, [catalog, query]);

  function toggleSkill(name) {
    setSelected((current) => {
      if (current.indexOf(name) >= 0) {
        return current.filter((item) => item !== name);
      }
      return current.concat([name]);
    });
  }

  async function submit(event) {
    event.preventDefault();
    if (busy || selected.length === 0) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    if (!version) {
      setError("The config version is unavailable. Close the dialog and reload the tab.");
      setBusy(false);
      return;
    }
    try {
      const next = uniqueNames(taken.concat(selected));
      const result = await updateAgent(agent, { skills: next }, version);
      onAdded({
        skills: next,
        version: result && result.version ? String(result.version) : "",
        added: selected.length,
      });
      onClose();
    } catch (failure) {
      if (isErrorCode(failure, "stale_version")) {
        setError(
          "This agent changed since you opened it. Close the dialog, reload the tab, and try again."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
    } finally {
      setBusy(false);
    }
  }

  const addLabel =
    selected.length === 0
      ? "Add skills"
      : "Add " + selected.length + (selected.length === 1 ? " skill" : " skills");

  return html`<${Dialog}
    title=${"Add skills to " + agent}
    className="agent-tabs-dialog agent-skills-dialog"
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form class="agent-skills-add" onSubmit=${submit}>
      <p class="hint">
        Pick global skills from the library. ${agent} loads them when a task
        needs them.
      </p>

      <label class="agent-tabs-search agent-skills-search">
        <span class="sr-only">Search skills</span>
        <${Icon} name="search" />
        <input
          data-autofocus
          type="search"
          placeholder="Search skills"
          autocomplete="off"
          value=${query}
          onInput=${(event) => setQuery(event.target.value)}
        />
      </label>

      <fieldset class="agent-skills-options">
        <legend class="sr-only">Global skills</legend>
        ${options.length === 0
          ? html`<p class="agent-tabs-empty">
              ${query.trim()
                ? "No skills match “" + query + "”."
                : "No global skills yet."}
            </p>`
          : options.map((skill) => {
              const added = takenSet.has(skill.name);
              const checked = added || selected.indexOf(skill.name) >= 0;
              return html`<label
                key=${skill.name}
                class=${"agent-skills-option" +
                  (checked ? " is-checked" : "") +
                  (added ? " is-added" : "")}
              >
                <input
                  type="checkbox"
                  checked=${checked}
                  disabled=${added || busy}
                  onChange=${() => toggleSkill(skill.name)}
                />
                <span class="agent-skills-option-text">
                  <span class="agent-skills-option-name">${skill.name}</span>
                  <span class="agent-skills-option-desc">
                    ${skill.description || "No description yet."}
                  </span>
                </span>
                ${added
                  ? html`<span class="agent-skills-option-state">Added</span>`
                  : null}
              </label>`;
            })}
      </fieldset>

      ${error
        ? html`<div class="agent-tabs-error" role="alert">
            <${ErrorNotice} message=${error} details=${details} />
          </div>`
        : null}

      <div class="agent-skills-dialog-footer">
        <a class="agent-skills-create" href="#/skills/new">+ Create new skill</a>
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
            disabled=${busy || selected.length === 0}
          >${busy ? "Adding…" : addLabel}</button>
        </div>
      </div>
    </form>
  </${Dialog}>`;
}

export function SkillsTab({ name }) {
  const [status, setStatus] = useState("loading");
  const [catalog, setCatalog] = useState({ valid: [], invalid: [] });
  const [skillNames, setSkillNames] = useState([]);
  const [version, setVersion] = useState("");
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [stale, setStale] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [addOpen, setAddOpen] = useState(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    setStale(false);
    setNotice("");
    Promise.all([loadAgentTab(name), listSkills()])
      .then(([config, page]) => {
        if (!alive) {
          return;
        }
        const names = Array.isArray(config.agent.skills)
          ? config.agent.skills
          : Array.isArray(config.row.skills)
          ? config.row.skills
          : [];
        setSkillNames(uniqueNames(names));
        setCatalog({
          valid: page && Array.isArray(page.skills) ? page.skills : [],
          invalid: page && Array.isArray(page.invalid) ? page.invalid : [],
        });
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

  const entries = useMemo(() => {
    const global = new Map();
    const all = new Map();
    for (const skill of catalog.valid) {
      if (!skill || !skill.name) {
        continue;
      }
      if (!all.has(skill.name)) {
        all.set(skill.name, skill);
      }
      if (skill.scope === "global") {
        global.set(skill.name, skill);
      }
    }
    for (const skill of catalog.invalid) {
      if (skill && skill.name && !all.has(skill.name)) {
        all.set(skill.name, skill);
      }
    }
    return { global, all };
  }, [catalog]);

  const rows = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return skillNames
      .map((skillName) => ({
        name: skillName,
        entry: entries.global.get(skillName) || entries.all.get(skillName) || null,
      }))
      .filter((row) => {
        if (!needle) {
          return true;
        }
        const description = row.entry ? String(row.entry.description || "") : "";
        return (
          row.name.toLowerCase().includes(needle) ||
          description.toLowerCase().includes(needle)
        );
      });
  }, [skillNames, entries, query]);

  function reload() {
    setReloadToken((value) => value + 1);
  }

  async function removeSkill(skillName) {
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
    const next = skillNames.filter((item) => item !== skillName);
    setSaving(true);
    try {
      const result = await updateAgent(name, { skills: next }, version);
      setSkillNames(next);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setNotice(
        "Removed " +
          skillName +
          ". The skill files stay. Restart the daemon for the new skill list to take effect."
      );
    } catch (failure) {
      if (isErrorCode(failure, "stale_version")) {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload the skills to get the latest version, then try again."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
    } finally {
      setSaving(false);
    }
  }

  function handleAdded(result) {
    setSkillNames(result.skills);
    if (result.version) {
      setVersion(result.version);
    }
    setNotice(
      "Added " +
        result.added +
        (result.added === 1 ? " skill" : " skills") +
        ". Restart the daemon for the new skill list to take effect."
    );
  }

  if (status === "loading") {
    return html`<div class="agent-tabs-state">
      <${Loading} label="Loading skills…" />
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
        <span class="sr-only">Search skills</span>
        <${Icon} name="search" />
        <input
          type="search"
          placeholder="Search skills"
          autocomplete="off"
          value=${query}
          onInput=${(event) => setQuery(event.target.value)}
        />
      </label>
      <span class="agent-tabs-toolbar-spacer"></span>
      <p class="agent-tabs-summary">
        ${skillNames.length} ${skillNames.length === 1 ? "skill" : "skills"}
      </p>
      <button
        type="button"
        class="button-ui button-ui-primary"
        disabled=${saving}
        onClick=${() => setAddOpen(true)}
      >+ Add skill</button>
    </div>

    ${skillNames.length === 0
      ? html`<p class="agent-tabs-empty">
          No skills yet. Add one from the global library.
        </p>`
      : rows.length === 0
      ? html`<p class="agent-tabs-empty">No skills match “${query}”.</p>`
      : html`<div class="agent-tabs-table-wrap">
          <table class="agent-tabs-table agent-skills-table">
            <thead>
              <tr>
                <th scope="col">Skill</th>
                <th scope="col" class="agent-skills-col-scope">Scope</th>
                <th scope="col" class="agent-skills-col-updated">Updated</th>
                <th scope="col" class="agent-tabs-col-action">
                  <span class="sr-only">Remove</span>
                </th>
              </tr>
            </thead>
            <tbody>
              ${rows.map((row) => {
                const entry = row.entry;
                const description = !entry
                  ? "Not in the skill library."
                  : entry.valid === false
                  ? "Can't load: " + (entry.error || "unknown error")
                  : entry.description || "No description yet.";
                const scopeAgent =
                  entry && String(entry.scope || "").indexOf("agent:") === 0
                    ? String(entry.scope).slice("agent:".length)
                    : name;
                return html`<tr key=${row.name}>
                  <th scope="row" class="agent-skills-name-cell">
                    <span class="agent-tabs-tile">
                      <${Icon} name="skills" />
                    </span>
                    <span class="agent-skills-text">
                      <span class="agent-skills-name">${row.name}</span>
                      <span class="agent-skills-desc">${description}</span>
                    </span>
                  </th>
                  <td>
                    ${!entry
                      ? html`<span class="badge-ui">Missing</span>`
                      : entry.scope === "global"
                      ? html`<span class="badge-ui badge-ui-brand">
                          <span class="badge-ui-dot" aria-hidden="true"></span>
                          Global
                        </span>`
                      : html`<span class="agent-skills-scope">
                          <${AgentAvatar} name=${scopeAgent} size="20" />
                          <span>Only ${scopeAgent}</span>
                        </span>`}
                  </td>
                  <td class="agent-skills-updated">
                    ${entry && entry.updated_at ? agoLabel(entry.updated_at) : "—"}
                  </td>
                  <td class="agent-tabs-col-action">
                    <button
                      type="button"
                      class="agent-tabs-icon-button"
                      aria-label=${"Remove " + row.name + " from " + name}
                      disabled=${saving}
                      onClick=${() => removeSkill(row.name)}
                    >
                      <${Icon} name="close" className="icon-16" />
                    </button>
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
              >Reload skills</button>`
            : null}
        </div>`
      : null}

    ${addOpen
      ? html`<${AddSkillsDialog}
          agent=${name}
          taken=${skillNames}
          catalog=${catalog}
          version=${version}
          onClose=${() => setAddOpen(false)}
          onAdded=${handleAdded}
        />`
      : null}
  </div>`;
}

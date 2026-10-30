/* REQ-WEB-048, REQ-WEB-049; AC-WEB-051, AC-WEB-052: the skills list and the
   skill editor. Skills are markdown files with a small frontmatter block. The
   list shows both the readable skills and the folders that fail to parse, and
   the editor rebuilds the file without dropping unknown frontmatter keys. */

import {
  html,
  useState,
  useEffect,
  useMemo,
} from "/assets/vendor/preact-htm.js";
import { useAnnounce, Dialog, ErrorNotice, Loading, Notice } from "/assets/ui.js";
import { errorMessage, ApiError } from "/assets/api.js";
import {
  listSkills,
  listAgents,
  getSkill,
  putSkill,
  deleteSkill,
  putAgentSkills,
  getConfig,
} from "/assets/api/skills.js";
import {
  Icon,
  AgentAvatar,
  TeamAvatars,
  isTeam,
  rosterNames,
  splitModel,
  agoLabel,
} from "/assets/views/agent-common.js";

const PAGE_SIZE = 8;
const DESCRIPTION_LIMIT = 300;
const INSTRUCTIONS_LIMIT = 20000;
const SKILL_NAME_RE = /^[a-z][a-z0-9_-]{0,63}$/;

function skillsHref(scope, name) {
  return "#/skills/" + encodeURIComponent(scope) + "/" + encodeURIComponent(name);
}

function scopeLabel(scope) {
  return scope === "global" ? "Global" : String(scope || "");
}

function scopeAgent(scope) {
  return String(scope || "").startsWith("agent:") ? String(scope).slice(6) : "";
}

function ScopeBadge({ scope }) {
  const global = scope === "global";
  return html`<span class=${"badge-ui" + (global ? " badge-ui-brand" : "")}>
    <span class="badge-ui-dot" aria-hidden="true"></span>
    ${scopeLabel(scope)}
  </span>`;
}

function formatCount(value) {
  return Number(value || 0).toLocaleString("en-US");
}

function formatSize(value) {
  if (value === null || value === undefined) {
    return "–";
  }
  return formatCount(value) + " chars";
}

function textLength(text) {
  return Array.from(String(text || "")).length;
}

function skillMatches(skill, query) {
  if (!query) {
    return true;
  }
  const haystack = [skill.name, skill.description]
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

function compareSkills(left, right) {
  if (left.scope !== right.scope) {
    if (left.scope === "global") {
      return -1;
    }
    if (right.scope === "global") {
      return 1;
    }
    return String(left.scope).localeCompare(String(right.scope));
  }
  const leftValid = left.valid !== false;
  const rightValid = right.valid !== false;
  if (leftValid !== rightValid) {
    return leftValid ? -1 : 1;
  }
  return String(left.name).localeCompare(String(right.name));
}

function folderPath(scope, name, apiPath) {
  const agent = scopeAgent(scope);
  const leaf = name || "";
  const fallback = agent
    ? "$LANA_HOME/agents/" + agent + "/skills/" + (leaf || "") + (leaf ? "/" : "")
    : "$LANA_HOME/skills/" + (leaf || "") + (leaf ? "/" : "");
  if (!apiPath) {
    return fallback;
  }
  const withoutFile = String(apiPath)
    .replace(/[\\/]SKILL\.md$/i, "")
    .replace(/\\/g, "/");
  const marker = agent ? "/agents/" + agent + "/skills/" : "/skills/";
  const index = withoutFile.indexOf(marker);
  if (index >= 0) {
    return "$LANA_HOME" + withoutFile.slice(index) + "/";
  }
  return withoutFile + "/";
}

/* The frontmatter parser mirrors internal/agenthome.ParseSkill: the block is
   fenced by --- lines, a key ends at the first colon, and matching quotes are
   stripped. Unparsed lines are kept so a save does not drop unknown keys. */
function parseSkillContent(text) {
  const normalized = String(text || "").replace(/\r\n/g, "\n");
  const lines = normalized.split("\n");
  if (lines[0] !== "---") {
    return {
      frontmatter: [],
      body: normalized,
      name: "",
      description: "",
    };
  }
  let close = -1;
  for (let index = 1; index < lines.length; index += 1) {
    if (lines[index] === "---") {
      close = index;
      break;
    }
  }
  if (close < 0) {
    return {
      frontmatter: [],
      body: normalized,
      name: "",
      description: "",
    };
  }
  const frontmatter = lines.slice(1, close);
  let name = "";
  let description = "";
  for (const line of frontmatter) {
    const colon = line.indexOf(":");
    if (colon < 0) {
      continue;
    }
    const key = line.slice(0, colon).trim();
    let value = line.slice(colon + 1).trim();
    if (value.length >= 2) {
      const first = value[0];
      const last = value[value.length - 1];
      if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
        value = value.slice(1, -1);
      }
    }
    if (key === "name" && !name) {
      name = value;
    }
    if (key === "description" && !description) {
      description = value;
    }
  }
  return {
    frontmatter,
    body: lines.slice(close + 1).join("\n"),
    name,
    description,
  };
}

function frontmatterValue(text) {
  const value = String(text || "").replace(/\r?\n/g, " ").trim();
  if (value.length >= 2) {
    const first = value[0];
    if ((first === '"' || first === "'") && value[value.length - 1] === first) {
      const quote = first === '"' ? "'" : '"';
      return quote + value + quote;
    }
  }
  return value;
}

function setFrontmatterField(lines, key, value) {
  const next = lines.slice();
  const rendered = key + ": " + frontmatterValue(value);
  for (let index = 0; index < next.length; index += 1) {
    const colon = next[index].indexOf(":");
    if (colon >= 0 && next[index].slice(0, colon).trim() === key) {
      next[index] = rendered;
      return next;
    }
  }
  next.push(rendered);
  return next;
}

function buildSkillContent(frontmatter, name, description, instructions) {
  let lines = setFrontmatterField(frontmatter, "name", name);
  lines = setFrontmatterField(lines, "description", description);
  return "---\n" + lines.join("\n") + "\n---\n" + instructions;
}

function skillLinkTarget(row) {
  return skillsHref(row.scope, row.name);
}

function DeleteSkillDialog({ skill, onClose, onDeleted, onReload }) {
  const announce = useAnnounce();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);

  const path = folderPath(skill.scope, skill.name, "");

  async function confirm() {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      /* DELETE needs the version of the file the owner is looking at; the
         list does not carry it, so read the row first (API-WEB-017). */
      const loaded = await getSkill(skill.scope, skill.name);
      const version = loaded && loaded.version ? String(loaded.version) : "";
      await deleteSkill(skill.scope, skill.name, version);
      announce("Deleted skill " + skill.name + ".");
      onDeleted();
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "This skill changed since the list loaded, so the delete did not apply. The list has been refreshed; try again."
        );
        announce("This skill changed on disk. Reloaded the skill list.");
        onReload();
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not delete " + skill.name + ". " + errorMessage(failure));
      }
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title=${"Delete “" + skill.name + "”?"}
    dismissible=${!busy}
    onClose=${onClose}
  >
    <p>
      The skill folder <code>${path}</code> is removed and nothing else changes.
      An agent that still lists <strong>${skill.name}</strong> keeps the name in its
      <code>skills</code> list. The runtime skips a listed skill that has no folder
      (Q-026).
    </p>
    ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
    <div class="dialog-actions">
      <button
        type="button"
        class="button-ui button-ui-secondary"
        disabled=${busy}
        onClick=${onClose}
      >Cancel</button>
      <button
        type="button"
        class="button-ui skills-danger"
        disabled=${busy}
        onClick=${confirm}
      >${busy ? "Deleting…" : "Delete skill"}</button>
    </div>
  </${Dialog}>`;
}

function SkillRow({ skill, onOpen, onDelete }) {
  const valid = skill.valid !== false;
  const canDelete = SKILL_NAME_RE.test(String(skill.name || ""));
  return html`<tr class=${"agents-row skills-row" + (valid ? "" : " is-invalid")} onClick=${onOpen}>
    <td>
      <span class="agents-name-cell">
        <span class=${"skills-tile" + (valid ? "" : " is-invalid")} aria-hidden="true">
          <${Icon} name="skills" />
        </span>
        <span class="agents-name-text">
          <span class="agents-name-row">
            <a class="agents-name-action skills-name" href=${skillLinkTarget(skill)}>${skill.name}</a>
            ${valid
              ? null
              : html`<span class="badge-ui badge-ui-error">
                  <span class="badge-ui-dot" aria-hidden="true"></span>
                  Can't load
                </span>`}
          </span>
          ${valid
            ? html`<span class="agents-description">${skill.description || ""}</span>`
            : html`<span class="skills-error-text">${skill.error || "This skill cannot be loaded."}</span>`}
        </span>
      </span>
    </td>
    <td><${ScopeBadge} scope=${skill.scope} /></td>
    <td class="agents-number">${valid ? formatSize(skill.size) : "–"}</td>
    <td class="skills-updated">${agoLabel(skill.updated_at) || "—"}</td>
    <td class="skills-actions">
      ${valid || canDelete
        ? html`<button
            type="button"
            class="skills-delete"
            aria-label=${"Delete skill " + skill.name}
            onClick=${(event) => {
              event.stopPropagation();
              onDelete();
            }}
          >Delete</button>`
        : null}
      <a
        class="skills-open"
        href=${skillLinkTarget(skill)}
        aria-label=${"Open skill " + skill.name}
        onClick=${(event) => event.stopPropagation()}
      ><${Icon} name="chevron-right" /></a>
    </td>
  </tr>`;
}

export function SkillsPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState("loading");
  const [page, setPage] = useState(null);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState("all");
  const [pageIndex, setPageIndex] = useState(0);
  const [reloadToken, setReloadToken] = useState(0);
  const [deleteTarget, setDeleteTarget] = useState(null);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    listSkills()
      .then((result) => {
        if (!alive) {
          return;
        }
        setPage({
          skills: result && Array.isArray(result.skills) ? result.skills : [],
          invalid: result && Array.isArray(result.invalid) ? result.invalid : [],
        });
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        const message = errorMessage(failure);
        setError(message);
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        announce("Could not load skills. " + message);
      });
    return () => {
      alive = false;
    };
  }, [reloadToken, announce]);

  const validCount = page ? (page.skills || []).length : 0;
  const invalidCount = page ? (page.invalid || []).length : 0;

  const rows = useMemo(() => {
    if (!page) {
      return [];
    }
    return (page.skills || [])
      .map((row) => Object.assign({}, row, { valid: true }))
      .concat((page.invalid || []).map((row) => Object.assign({}, row, { valid: false })))
      .sort(compareSkills);
  }, [page]);

  const scopeOptions = useMemo(() => {
    const names = new Set();
    for (const row of rows) {
      if (row.scope && row.scope !== "global") {
        names.add(String(row.scope));
      }
    }
    return Array.from(names).sort((left, right) => left.localeCompare(right));
  }, [rows]);

  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      rows.filter(
        (row) => (scope === "all" || row.scope === scope) && skillMatches(row, search)
      ),
    [rows, scope, search]
  );

  const pageCount = Math.max(1, Math.ceil(visible.length / PAGE_SIZE));
  const currentPage = Math.min(pageIndex, pageCount - 1);
  const pageRows = visible.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE);
  const firstRow = visible.length === 0 ? 0 : currentPage * PAGE_SIZE + 1;
  const lastRow = Math.min((currentPage + 1) * PAGE_SIZE, visible.length);

  function changeQuery(value) {
    setQuery(value);
    setPageIndex(0);
  }

  function changeScope(value) {
    setScope(value);
    setPageIndex(0);
  }

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function openRow(event, row) {
    const target = event.target;
    if (
      target &&
      typeof target.closest === "function" &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    window.location.hash = skillLinkTarget(row);
  }

  const summary =
    validCount +
    " skill" + (validCount === 1 ? "" : "s") +
    " · " +
    invalidCount +
    " can't load";

  return html`<main id="main" class="page skills-page">
    <header class="agents-header">
      <div class="agents-heading">
        <h1 class="agents-title">Skills</h1>
        <p class="agents-subtitle">
          Instructions an agent loads when a task needs them. Global skills work for every agent.
        </p>
      </div>
      <a class="button-ui button-ui-primary" href="#/skills/new">+ New skill</a>
    </header>

    <div class="agents-toolbar">
      <div class="sessions-search agents-search">
        <span class="sessions-search-icon">
          <${Icon} name="search" />
        </span>
        <label class="sr-only" for="skills-search-input">Search skills</label>
        <input
          id="skills-search-input"
          class="sessions-search-input"
          type="search"
          placeholder="Search skills"
          autocomplete="off"
          spellcheck="false"
          value=${query}
          onInput=${(event) => changeQuery(event.target.value)}
        />
      </div>
      <div class="skills-scope-filter">
        <span class="skills-scope-icon" aria-hidden="true"><${Icon} name="filter" /></span>
        <label class="sr-only" for="skills-scope-select">Filter by scope</label>
        <select
          id="skills-scope-select"
          class="skills-scope-select"
          value=${scope}
          onChange=${(event) => changeScope(event.target.value)}
        >
          <option value="all">All scopes</option>
          <option value="global">Global</option>
          ${scopeOptions.map(
            (value) => html`<option key=${value} value=${value}>${value}</option>`
          )}
        </select>
      </div>
      <span class="agents-toolbar-spacer"></span>
      ${status === "ready"
        ? html`<p class="agents-summary" aria-live="polite">${summary}</p>`
        : null}
    </div>

    ${status === "loading" ? html`<${Loading} label="Loading skills…" />` : null}

    ${status === "error"
      ? html`<div class="skills-state" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          <button type="button" class="button-ui button-ui-secondary" onClick=${reload}>
            Try again
          </button>
        </div>`
      : null}

    ${status === "ready" && rows.length === 0
      ? html`<div class="skills-empty">
          <h2 class="empty-title">No skills yet</h2>
          <p class="empty-text">
            A skill is a folder with a SKILL.md that an agent loads when a task needs it.
          </p>
          <a class="button-ui button-ui-primary" href="#/skills/new">+ New skill</a>
        </div>`
      : null}

    ${status === "ready" && rows.length > 0 && visible.length === 0
      ? html`<p class="skills-state muted" role="status">
          No skills match the current search and scope.
        </p>`
      : null}

    ${status === "ready" && visible.length > 0
      ? html`<div class="agents-table-wrap">
          <table class="agents-table skills-table">
            <caption class="sr-only">Skills</caption>
            <thead>
              <tr>
                <th scope="col">Skill</th>
                <th scope="col">Scope</th>
                <th scope="col">Size</th>
                <th scope="col">Updated</th>
                <th scope="col"><span class="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              ${pageRows.map(
                (row) => html`<${SkillRow}
                  key=${row.scope + "/" + row.name}
                  skill=${row}
                  onOpen=${(event) => openRow(event, row)}
                  onDelete=${() => setDeleteTarget(row)}
                />`
              )}
            </tbody>
          </table>
          <div class="skills-pagination">
            <p class="skills-pagination-info">
              Showing ${firstRow}–${lastRow} of ${visible.length}
            </p>
            <span class="skills-pagination-page">Page ${currentPage + 1} of ${pageCount}</span>
            <div class="skills-pager">
              <button
                type="button"
                class="button-ui button-ui-secondary"
                disabled=${currentPage === 0}
                onClick=${() => setPageIndex(Math.max(0, currentPage - 1))}
              >Previous</button>
              <button
                type="button"
                class="button-ui button-ui-secondary"
                disabled=${currentPage >= pageCount - 1}
                onClick=${() => setPageIndex(Math.min(pageCount - 1, currentPage + 1))}
              >Next</button>
            </div>
          </div>
        </div>`
      : null}

    ${deleteTarget
      ? html`<${DeleteSkillDialog}
          skill=${deleteTarget}
          onClose=${() => setDeleteTarget(null)}
          onDeleted=${() => {
            setDeleteTarget(null);
            reload();
          }}
          onReload=${reload}
        />`
      : null}
  </main>`;
}

function agentModelLabel(agent) {
  if (!agent) {
    return "";
  }
  return splitModel(agent).model || String(agent.model || "");
}

/* The team roster is the lead plus the members (REQ-TEAM-023). It comes from
   the shared helper so the order matches the agents list; a team with no
   roster has no agents to add. */
function teamMembers(team) {
  if (!team) {
    return [];
  }
  const teamName = String(team.name || "");
  return rosterNames(team).filter((name) => name !== teamName);
}

function AddAgentsDialog({ skillName, agents, selected, onClose, onAdd }) {
  const [query, setQuery] = useState("");
  const [picks, setPicks] = useState([]);
  const selectedSet = new Set(selected);
  const search = query.trim().toLowerCase();

  const agentOptions = agents.filter((agent) => !isTeam(agent));
  const teamOptions = agents.filter(isTeam);

  function matches(value) {
    if (!search) {
      return true;
    }
    const haystack = [
      value.name,
      value.description,
      value.model,
      teamMembers(value).join(" "),
    ]
      .filter((item) => item !== null && item !== undefined)
      .join(" ")
      .toLowerCase();
    return haystack.includes(search);
  }

  function toggleAgent(name) {
    setPicks((current) =>
      current.includes(name)
        ? current.filter((item) => item !== name)
        : current.concat([name])
    );
  }

  function toggleTeam(team) {
    const members = teamMembers(team);
    const every = members.length > 0 && members.every(
      (name) => selectedSet.has(name) || picks.includes(name)
    );
    if (every) {
      setPicks((current) => current.filter((name) => !members.includes(name)));
      return;
    }
    setPicks((current) => {
      const next = current.slice();
      for (const name of members) {
        if (!selectedSet.has(name) && !next.includes(name)) {
          next.push(name);
        }
      }
      return next;
    });
  }

  const pending = picks.filter((name) => !selectedSet.has(name));

  return html`<${Dialog}
    title=${"Add agents to " + skillName}
    onClose=${onClose}
  >
    <p class="skills-dialog-subtitle">
      Picked agents can load this skill when a task needs it.
    </p>
    <div class="sessions-search skills-dialog-search">
      <span class="sessions-search-icon">
        <${Icon} name="search" />
      </span>
      <label class="sr-only" for="skills-add-agent-search">Search agents</label>
      <input
        id="skills-add-agent-search"
        data-autofocus
        class="sessions-search-input"
        type="search"
        placeholder="Search agents"
        autocomplete="off"
        spellcheck="false"
        value=${query}
        onInput=${(event) => setQuery(event.target.value)}
      />
    </div>
    <div class="skills-add-list">
      ${agentOptions.filter(matches).map((agent) => {
        const added = selectedSet.has(agent.name);
        const checked = added || picks.includes(agent.name);
        const model = agentModelLabel(agent);
        return html`<label
          key=${agent.name}
          class=${"skills-add-option" + (checked ? " is-checked" : "") + (added ? " is-added" : "")}
        >
          <input
            type="checkbox"
            class="skills-add-check"
            checked=${checked}
            disabled=${added}
            onChange=${() => toggleAgent(agent.name)}
          />
          <${AgentAvatar} name=${agent.name} size="32" />
          <span class="skills-add-text">
            <span class="skills-add-name">${agent.name}</span>
            ${model ? html`<span class="skills-add-meta">${model}</span>` : null}
          </span>
          ${added ? html`<span class="skills-add-state">Added</span>` : null}
        </label>`;
      })}
      ${teamOptions.filter(matches).map((team) => {
        const members = teamMembers(team);
        const allAdded = members.length > 0 && members.every((name) => selectedSet.has(name));
        const allPicked =
          members.length > 0 &&
          members.every((name) => selectedSet.has(name) || picks.includes(name));
        return html`<label
          key=${team.name}
          class=${"skills-add-option skills-add-team" + (allAdded ? " is-added" : "")}
        >
          <input
            type="checkbox"
            class="skills-add-check"
            checked=${allPicked}
            disabled=${allAdded || members.length === 0}
            onChange=${() => toggleTeam(team)}
          />
          <${TeamAvatars} agent=${team} size="24" />
          <span class="skills-add-text">
            <span class="skills-add-name-row">
              <span class="skills-add-name">${team.name}</span>
              <span class="badge-ui badge-ui-brand">
                <span class="badge-ui-dot" aria-hidden="true"></span>
                Team
              </span>
            </span>
            <span class="skills-add-meta">
              ${members.length === 0
                ? "No members yet"
                : "Adds it to all " + members.length + " agents in the team"}
            </span>
          </span>
          ${allAdded ? html`<span class="skills-add-state">Added</span>` : null}
        </label>`;
      })}
      ${agentOptions.filter(matches).length === 0 && teamOptions.filter(matches).length === 0
        ? html`<p class="skills-add-empty" role="status">No agents match “${query.trim()}”.</p>`
        : null}
    </div>
    <div class="dialog-actions">
      <button type="button" class="button-ui button-ui-secondary" onClick=${onClose}>
        Cancel
      </button>
      <button
        type="button"
        class="button-ui button-ui-primary"
        disabled=${pending.length === 0}
        onClick=${() => {
          onAdd(pending);
          onClose();
        }}
      >${pending.length === 0
        ? "Add agents"
        : pending.length === 1
          ? "Add 1 agent"
          : "Add " + pending.length + " agents"}</button>
    </div>
  </${Dialog}>`;
}

function AgentPickerRow({ agent, skillName, readOnly, onRemove }) {
  const model = agentModelLabel(agent);
  return html`<div class="skills-agent-row">
    <${AgentAvatar} name=${agent.name} size="28" />
    <span class="skills-agent-text">
      <span class="skills-agent-name">${agent.name}</span>
      ${model ? html`<span class="skills-agent-model">${model}</span>` : null}
    </span>
    ${readOnly
      ? html`<span class="skills-agent-owner">Owner</span>`
      : html`<button
          type="button"
          class="skills-agent-remove"
          aria-label=${"Remove " + agent.name + " from " + skillName}
          onClick=${onRemove}
        ><${Icon} name="close" className="icon-16" /></button>`}
  </div>`;
}

export function SkillEditorPage({ scope, name, isNew }) {
  const announce = useAnnounce();
  const newSkill = Boolean(isNew);
  const [status, setStatus] = useState("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [stale, setStale] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  const [frontmatter, setFrontmatter] = useState([]);
  const [skillName, setSkillName] = useState("");
  const [skillScope, setSkillScope] = useState("global");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");
  const [version, setVersion] = useState("");
  const [loadedPath, setLoadedPath] = useState("");
  const [updatedAt, setUpdatedAt] = useState(0);
  const [created, setCreated] = useState(false);

  const [agents, setAgents] = useState([]);
  const [agentsError, setAgentsError] = useState("");
  const [configVersion, setConfigVersion] = useState("");
  const [selectedAgents, setSelectedAgents] = useState([]);
  const [baselineAgents, setBaselineAgents] = useState([]);

  const [addOpen, setAddOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveState, setSaveState] = useState(null);
  const [agentResults, setAgentResults] = useState(null);
  const [nameError, setNameError] = useState("");

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    setStale(false);
    setSaveState(null);
    setAgentResults(null);
    setNameError("");
    setCreated(false);

    const skillLoad = newSkill
      ? Promise.resolve(null)
      : getSkill(scope, name);
    const agentsLoad = listAgents()
      .then((result) => ({
        ok: true,
        agents: result && Array.isArray(result.agents) ? result.agents : [],
      }))
      .catch((failure) => ({ ok: false, error: failure }));
    const configLoad = getConfig()
      .then((result) => ({ ok: true, version: result && result.version ? String(result.version) : "" }))
      .catch(() => ({ ok: false, version: "" }));
    const listLoad = newSkill ? Promise.resolve(null) : listSkills().catch(() => null);

    Promise.all([skillLoad, agentsLoad, configLoad, listLoad])
      .then(([loaded, agentResult, configResult, listed]) => {
        if (!alive) {
          return;
        }
        const rows = agentResult.ok ? agentResult.agents : [];
        setAgents(rows);
        setAgentsError(agentResult.ok ? "" : errorMessage(agentResult.error));
        setConfigVersion(configResult.version || "");

        if (newSkill) {
          setFrontmatter([]);
          setSkillName("");
          setSkillScope("global");
          setDescription("");
          setInstructions("");
          setVersion("");
          setLoadedPath("");
          setUpdatedAt(0);
          setSelectedAgents([]);
          setBaselineAgents([]);
          setStatus("ready");
          return;
        }

        const parsed = parseSkillContent(loaded && loaded.content ? loaded.content : "");
        setFrontmatter(parsed.frontmatter);
        setSkillName(name);
        setSkillScope(scope);
        setDescription(parsed.description);
        setInstructions(parsed.body);
        setVersion(loaded && loaded.version ? String(loaded.version) : "");
        setLoadedPath(loaded && loaded.path ? String(loaded.path) : "");
        let stamp = 0;
        if (listed) {
          const all = (listed.skills || []).concat(listed.invalid || []);
          const match = all.find((row) => row.scope === scope && row.name === name);
          if (match) {
            stamp = Number(match.updated_at) || 0;
          }
        }
        setUpdatedAt(stamp);
        const listedAgents = rows
          .filter((agent) => !isTeam(agent))
          .filter(
            (agent) =>
              Array.isArray(agent.skills) && agent.skills.map(String).includes(name)
          )
          .map((agent) => agent.name);
        setSelectedAgents(scope === "global" ? listedAgents : []);
        setBaselineAgents(scope === "global" ? listedAgents : []);
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        announce("Could not load the skill. " + errorMessage(failure));
      });

    return () => {
      alive = false;
    };
  }, [scope, name, newSkill, reloadToken, announce]);

  const agentsByName = useMemo(() => {
    const map = new Map();
    for (const agent of agents) {
      map.set(agent.name, agent);
    }
    return map;
  }, [agents]);

  const selectedRows = selectedAgents.map(
    (agentName) => agentsByName.get(agentName) || { name: agentName, kind: "agent" }
  );
  const agentTotal = agents.filter((agent) => !isTeam(agent)).length;
  const readOnlyName = !newSkill || created;
  const readOnlyScope = !newSkill || created;
  const savedName = newSkill ? skillName.trim() : name;
  const folder = folderPath(skillScope, savedName, loadedPath);
  const descriptionLength = textLength(description);
  const instructionsLength = textLength(instructions);
  const descriptionOver = descriptionLength > DESCRIPTION_LIMIT;
  const instructionsOver = instructionsLength > INSTRUCTIONS_LIMIT;

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function updateSelected(next) {
    setSelectedAgents(next);
    setSaveState(null);
    setAgentResults(null);
  }

  function removeAgent(agentName) {
    updateSelected(selectedAgents.filter((item) => item !== agentName));
  }

  function addAgents(names) {
    const next = selectedAgents.slice();
    for (const agentName of names) {
      if (!next.includes(agentName)) {
        next.push(agentName);
      }
    }
    updateSelected(next);
    const count = next.length - selectedAgents.length;
    announce(
      "Added " + (count === 1 ? "1 agent" : count + " agents") +
        " to the list. Save the skill to apply."
    );
  }

  function agentChanges() {
    const before = new Set(baselineAgents);
    const after = new Set(selectedAgents);
    const names = new Set(baselineAgents.concat(selectedAgents));
    const changes = [];
    for (const agentName of names) {
      const was = before.has(agentName);
      const is = after.has(agentName);
      if (was === is) {
        continue;
      }
      const agent = agentsByName.get(agentName);
      if (!agent) {
        continue;
      }
      const current = Array.isArray(agent.skills) ? agent.skills.map(String) : [];
      let nextSkills;
      if (is) {
        nextSkills = current.includes(savedName)
          ? current.slice()
          : current.concat([savedName]);
      } else {
        nextSkills = current.filter((skill) => skill !== savedName);
      }
      changes.push({ name: agentName, skills: nextSkills });
    }
    return changes;
  }

  async function save(event) {
    event.preventDefault();
    if (saving || status !== "ready") {
      return;
    }
    const expectedName = newSkill ? skillName.trim() : name;
    if (newSkill && !SKILL_NAME_RE.test(expectedName)) {
      setNameError(
        "Use lowercase letters, digits, hyphens, or underscores, starting with a letter."
      );
      announce("The skill name is not valid.");
      return;
    }
    setNameError("");
    setError("");
    setDetails([]);
    setStale(false);
    setSaveState(null);
    setAgentResults(null);
    if (!created && !newSkill && !version) {
      setError("The skill version is unavailable. Reload the page and try again.");
      return;
    }

    const content = buildSkillContent(
      frontmatter,
      expectedName,
      description.trim(),
      instructions
    );
    setSaving(true);
    try {
      const expectedVersion = created || !newSkill ? version : "";
      const result = await putSkill(skillScope, expectedName, content, expectedVersion);
      const nextVersion = result && result.version ? String(result.version) : "";
      setVersion(nextVersion);
      if (result && result.path) {
        setLoadedPath(String(result.path));
      }
      if (newSkill && !created) {
        setCreated(true);
      }

      const changes = skillScope === "global" ? agentChanges() : [];
      const results = [];
      let currentVersion = configVersion;
      for (const change of changes) {
        if (!currentVersion) {
          results.push({
            name: change.name,
            ok: false,
            message: "The config version is unavailable. Reload the editor to try again.",
          });
          continue;
        }
        try {
          const saved = await putAgentSkills(change.name, change.skills, currentVersion);
          if (saved && saved.version) {
            currentVersion = String(saved.version);
          }
          results.push({
            name: change.name,
            ok: true,
            message: change.skills.includes(savedName)
              ? "now lists " + savedName
              : "no longer lists " + savedName,
          });
        } catch (failure) {
          let message = errorMessage(failure);
          if (failure instanceof ApiError && failure.code === "stale_version") {
            message = "The config changed since the editor loaded. Reload and try again.";
          }
          results.push({ name: change.name, ok: false, message });
        }
      }
      setConfigVersion(currentVersion);

      if (results.length > 0) {
        setAgentResults(results);
        let nextBaseline = baselineAgents.slice();
        for (const result of results) {
          if (!result.ok) {
            continue;
          }
          const member = selectedAgents.includes(result.name);
          if (member && !nextBaseline.includes(result.name)) {
            nextBaseline = nextBaseline.concat([result.name]);
          }
          if (!member) {
            nextBaseline = nextBaseline.filter((item) => item !== result.name);
          }
        }
        setBaselineAgents(nextBaseline);
      } else {
        setBaselineAgents(selectedAgents.slice());
      }

      const failed = results.filter((result) => !result.ok).length;
      if (failed > 0) {
        const message =
          "Skill saved. " + (results.length - failed) + " of " + results.length +
          " agent updates applied.";
        setSaveState({ tone: "warn", message });
        announce(message);
      } else if (results.length > 0) {
        const message =
          (newSkill ? "Skill created. " : "Skill saved. ") +
          results.length + (results.length === 1 ? " agent updated." : " agents updated.");
        setSaveState({ tone: "ok", message });
        announce(message);
      } else {
        const message = newSkill
          ? "Skill " + expectedName + " created."
          : "Saved changes to " + expectedName + ".";
        setSaveState({ tone: "ok", message });
        announce(message);
      }

      if (newSkill && failed === 0) {
        window.location.hash = skillsHref(skillScope, expectedName);
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setStale(true);
        setError(
          newSkill
            ? "A skill with this name already exists in this scope, or the file changed since you loaded it. Reload to see the latest version."
            : "This skill changed since you loaded it. Reload to get the latest version, then save again."
        );
        announce("The save did not apply because the skill changed.");
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not save the skill. " + errorMessage(failure));
      }
    } finally {
      setSaving(false);
    }
  }

  if (status === "loading") {
    return html`<main id="main" class="page skills-editor-page">
      <nav class="agent-breadcrumb" aria-label="Breadcrumb">
        <a href="#/skills">Skills</a>
        <span aria-hidden="true">/</span>
        <span aria-current="page">${newSkill ? "New skill" : name}</span>
      </nav>
      <${Loading} label="Loading skill…" />
    </main>`;
  }

  if (status === "error") {
    return html`<main id="main" class="page skills-editor-page">
      <nav class="agent-breadcrumb" aria-label="Breadcrumb">
        <a href="#/skills">Skills</a>
        <span aria-hidden="true">/</span>
        <span aria-current="page">${newSkill ? "New skill" : name}</span>
      </nav>
      <div role="alert">
        <${ErrorNotice} message=${error} details=${details} />
      </div>
      <div class="agent-state-actions">
        <button type="button" class="button-ui button-ui-secondary" onClick=${reload}>
          Try again
        </button>
        <a class="button-ui button-ui-secondary" href="#/skills">Back to skills</a>
      </div>
    </main>`;
  }

  const headingName = newSkill ? skillName.trim() || "New skill" : name;
  const showGlobalAgents = skillScope === "global";
  const scopeChoices = agents.filter((agent) => !isTeam(agent));
  const subtitle = newSkill
    ? "Write the instructions once, then choose who can load them."
    : updatedAt
      ? "Edited " + agoLabel(updatedAt) + " · loaded on demand with skill_load."
      : "Loaded on demand with skill_load when a task needs it.";

  return html`<main id="main" class="page skills-editor-page">
    <nav class="agent-breadcrumb" aria-label="Breadcrumb">
      <a href="#/skills">Skills</a>
      <span aria-hidden="true">/</span>
      <span aria-current="page">${newSkill ? "New skill" : name}</span>
    </nav>

    <header class="skills-editor-header">
      <div class="skills-editor-heading">
        <div class="skills-editor-title-row">
          <h1 class="skills-editor-title">${headingName}</h1>
          <${ScopeBadge} scope=${skillScope} />
        </div>
        <p class="skills-editor-subtitle">${subtitle}</p>
      </div>
      <div class="skills-editor-actions">
        <a class="button-ui button-ui-secondary" href="#/skills">Cancel</a>
        <button
          type="submit"
          form="skill-editor-form"
          class="button-ui button-ui-primary"
          disabled=${saving}
        >${saving ? "Saving…" : "Save skill"}</button>
      </div>
    </header>

    ${error
      ? html`<div class="skills-save-feedback" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          ${stale
            ? html`<button
                type="button"
                class="button-ui button-ui-secondary"
                onClick=${created ? () => {
                  window.location.hash = skillsHref(skillScope, savedName);
                } : reload}
              >${created ? "Open the saved skill" : "Reload"}</button>`
            : null}
        </div>`
      : null}

    <div class="skills-save-feedback" aria-live="polite">
      ${saveState
        ? html`<${Notice} tone=${saveState.tone}>${saveState.message}</${Notice}>`
        : null}
      ${agentResults && agentResults.length > 0
        ? html`<ul class="skills-agent-results">
            ${agentResults.map(
              (result) => html`<li
                key=${result.name}
                class=${result.ok ? "skills-result-ok" : "skills-result-error"}
              >
                <strong>${result.name}</strong>: ${result.message}
              </li>`
            )}
          </ul>`
        : null}
    </div>

    <form id="skill-editor-form" class="skills-editor-body" onSubmit=${save} novalidate>
      <div class="skills-editor-details">
        <label class="field skills-field">
          <span class="field-label">Name</span>
          ${readOnlyName
            ? html`<input
                class="skills-readonly skills-mono"
                type="text"
                value=${newSkill ? skillName : name}
                readonly
                aria-describedby="skill-name-hint"
              />`
            : html`<input
                class="skills-mono"
                type="text"
                name="name"
                value=${skillName}
                maxlength="64"
                autocomplete="off"
                spellcheck="false"
                pattern="[a-z][a-z0-9_-]{0,63}"
                required
                aria-invalid=${Boolean(nameError)}
                aria-describedby=${"skill-name-hint" + (nameError ? " skill-name-error" : "")}
                onInput=${(event) => {
                  setSkillName(event.target.value);
                  setNameError("");
                }}
              />`}
          <span id="skill-name-hint" class="hint">
            Lowercase letters, digits, - and _.
          </span>
          ${nameError
            ? html`<span id="skill-name-error" class="skills-field-error">${nameError}</span>`
            : null}
        </label>

        <label class="field skills-field">
          <span class="field-label">Scope</span>
          ${readOnlyScope
            ? html`<input
                class="skills-readonly"
                type="text"
                value=${scopeLabel(skillScope)}
                readonly
                aria-describedby="skill-scope-hint"
              />`
            : html`<select
                name="scope"
                value=${skillScope}
                aria-describedby="skill-scope-hint"
                onChange=${(event) => setSkillScope(event.target.value)}
              >
                <option value="global">Global</option>
                ${scopeChoices.map(
                  (agent) => html`<option key=${agent.name} value=${"agent:" + agent.name}>
                    ${agent.name}
                  </option>`
                )}
              </select>`}
          <span id="skill-scope-hint" class="hint">
            ${readOnlyScope
              ? "The scope cannot change after the skill is created."
              : "Shared library. Or pick one agent to keep it private."}
          </span>
        </label>

        <label class="field skills-field">
          <span class="field-label">Description</span>
          <textarea
            class="skills-description"
            name="description"
            rows="5"
            value=${description}
            aria-invalid=${descriptionOver}
            aria-describedby=${"skill-description-hint skill-description-counter" + (descriptionOver ? " skill-description-error" : "")}
            onInput=${(event) => {
              setDescription(event.target.value);
              setSaveState(null);
            }}
          ></textarea>
          <span class="skills-counter-row">
            <span id="skill-description-hint" class="hint">
              Agents read this to decide when to load the skill.
            </span>
            <span
              id="skill-description-counter"
              class=${"skills-counter" + (descriptionOver ? " is-over" : "")}
            >${formatCount(descriptionLength)} / ${formatCount(DESCRIPTION_LIMIT)}</span>
          </span>
          ${descriptionOver
            ? html`<span id="skill-description-error" class="skills-field-error">
                Description is over the ${formatCount(DESCRIPTION_LIMIT)}-character limit.
                Saving shows the server error; nothing is written.
              </span>`
            : null}
        </label>

        <div class="field skills-field skills-agents-field">
          <div class="skills-agents-head">
            <span class="field-label">Agents</span>
            <span class="skills-agents-count">
              ${showGlobalAgents
                ? selectedAgents.length + " of " + agentTotal
                : "Owner"}
            </span>
          </div>
          <div class="skills-agent-list">
            ${showGlobalAgents
              ? selectedRows.map(
                  (agent) => html`<${AgentPickerRow}
                    key=${agent.name}
                    agent=${agent}
                    skillName=${savedName || "this skill"}
                    readOnly=${false}
                    onRemove=${() => removeAgent(agent.name)}
                  />`
                )
              : html`<${AgentPickerRow}
                  agent=${agentsByName.get(scopeAgent(skillScope)) || {
                    name: scopeAgent(skillScope),
                    kind: "agent",
                  }}
                  skillName=${savedName || "this skill"}
                  readOnly=${true}
                />`}
            ${showGlobalAgents
              ? html`<button
                  type="button"
                  class="skills-add-agent"
                  disabled=${agentsError !== ""}
                  onClick=${() => setAddOpen(true)}
                >
                  <span class="skills-add-icon" aria-hidden="true"><${Icon} name="plus" /></span>
                  <span>Add agent</span>
                </button>`
              : null}
          </div>
          <span class="hint">
            ${showGlobalAgents
              ? "Only agents in this list load the skill."
              : "This skill belongs to one agent."}
          </span>
          ${agentsError
            ? html`<span class="skills-field-error">${agentsError}</span>`
            : null}
        </div>

        <div class="field skills-field">
          <span class="field-label">Folder</span>
          <code class="skills-folder">${folder}</code>
        </div>
      </div>

      <div class="skills-editor-pane">
        <div class="skills-pane-head">
          <label class="field-label" for="skill-instructions">Instructions</label>
          <span class="skills-markdown">Markdown</span>
        </div>
        <textarea
          id="skill-instructions"
          class="skills-instructions"
          name="instructions"
          value=${instructions}
          aria-invalid=${instructionsOver}
          aria-describedby=${"skill-instructions-hint skill-instructions-counter"}
          onInput=${(event) => {
            setInstructions(event.target.value);
            setSaveState(null);
          }}
        ></textarea>
        <div class="skills-pane-foot">
          <span id="skill-instructions-hint" class="hint">
            Loaded on demand with skill_load. Keep it short and specific.
          </span>
          <span
            id="skill-instructions-counter"
            class=${"skills-counter" + (instructionsOver ? " is-over" : "")}
          >${formatCount(instructionsLength)} / ${formatCount(INSTRUCTIONS_LIMIT)}</span>
        </div>
      </div>
    </form>

    ${addOpen
      ? html`<${AddAgentsDialog}
          skillName=${savedName || "this skill"}
          agents=${agents}
          selected=${selectedAgents}
          onClose=${() => setAddOpen(false)}
          onAdd=${addAgents}
        />`
      : null}
  </main>`;
}

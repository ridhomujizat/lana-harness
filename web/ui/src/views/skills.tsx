/* eslint-disable react-hooks/set-state-in-effect -- the legacy views reset
   their loading state inside the load effect; the port keeps that 1:1. */
/* eslint-disable react-hooks/immutability -- routing is a window.location
   hash assignment, as in the legacy views. */
import { useEffect, useMemo, useState } from "react";
import type { ChangeEvent, FormEvent, MouseEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import {
  listSkills,
  listAgents,
  getSkill,
  putSkill,
  deleteSkill,
  putAgentSkills,
  getConfig,
} from "../api/skills";
import type {
  ListSkillsResponse,
  SkillContentResponse,
  SkillRow as SkillRowData,
} from "../api/skills";
import type { Agent } from "../api/agents";
import { Dialog, detailText, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { agoLabel, isTeam, rosterNames, splitModel } from "./agent-common";
import type { AgentLike } from "./agent-common";
import {
  Badge,
  Button,
  Checkbox,
  EmptyState,
  ErrorState,
  IconButton,
  Input,
  SearchInput,
  Seg,
  Select,
  Skeleton,
  Textarea,
} from "../ui/gr8r";
import { AgentAvatar, AgentAvatarStack, agentFallbackColor } from "./agent-avatar";

/* REQ-WEB-048, REQ-WEB-049; AC-WEB-051, AC-WEB-052: the skills list and the
   skill editor. Skills are markdown files with a small frontmatter block. The
   list shows both the readable skills and the folders that fail to parse, and
   the editor rebuilds the file without dropping unknown frontmatter keys. */

const PAGE_SIZE = 8;
const DESCRIPTION_LIMIT = 300;
const INSTRUCTIONS_LIMIT = 20000;
const SKILL_NAME_RE = /^[a-z][a-z0-9_-]{0,63}$/;

function skillsHref(scope: string, name: string): string {
  return "#/skills/" + encodeURIComponent(scope) + "/" + encodeURIComponent(name);
}

function scopeLabel(scope: string | undefined): string {
  return scope === "global" ? "Global" : String(scope || "");
}

function scopeAgent(scope: string | undefined): string {
  return String(scope || "").startsWith("agent:")
    ? String(scope).slice(6)
    : "";
}

function agentColor(name: string): string {
  return agentFallbackColor(name);
}

function ScopeBadge({ scope }: { scope: string }) {
  const global = scope === "global";
  return (
    <Badge tone={global ? "accent" : "gray"} dot>
      {scopeLabel(scope)}
    </Badge>
  );
}

function DetailsList({ details }: { details: unknown[] }) {
  if (!details || details.length === 0) {
    return null;
  }
  return (
    <ul className="err-list">
      {details.map((detail, index) => (
        <li key={index}>{detailText(detail)}</li>
      ))}
    </ul>
  );
}

function formatCount(value: number | null | undefined): string {
  return Number(value || 0).toLocaleString("en-US");
}

function formatSize(value: number | null | undefined): string {
  if (value === null || value === undefined) {
    return "–";
  }
  return formatCount(value) + " chars";
}

function textLength(text: string | null | undefined): number {
  return Array.from(String(text || "")).length;
}

function skillMatches(skill: SkillRowData, query: string): boolean {
  if (!query) {
    return true;
  }
  const haystack = [skill.name, skill.description]
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

function compareSkills(left: SkillRowData, right: SkillRowData): number {
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

function folderPath(
  scope: string | undefined,
  name: string | undefined,
  apiPath: string
): string {
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

interface ParsedSkill {
  frontmatter: string[];
  body: string;
  name: string;
  description: string;
}

/* The frontmatter parser mirrors internal/agenthome.ParseSkill: the block is
   fenced by --- lines, a key ends at the first colon, and matching quotes are
   stripped. Unparsed lines are kept so a save does not drop unknown keys. */
function parseSkillContent(text: string | null | undefined): ParsedSkill {
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
      const first = value.charAt(0);
      const last = value.charAt(value.length - 1);
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

function frontmatterValue(text: string): string {
  const value = String(text || "").replace(/\r?\n/g, " ").trim();
  if (value.length >= 2) {
    const first = value.charAt(0);
    if ((first === '"' || first === "'") && value.charAt(value.length - 1) === first) {
      const quote = first === '"' ? "'" : '"';
      return quote + value + quote;
    }
  }
  return value;
}

function setFrontmatterField(
  lines: string[],
  key: string,
  value: string
): string[] {
  const next = lines.slice();
  const rendered = key + ": " + frontmatterValue(value);
  for (let index = 0; index < next.length; index += 1) {
    const line = next[index];
    if (line === undefined) {
      continue;
    }
    const colon = line.indexOf(":");
    if (colon >= 0 && line.slice(0, colon).trim() === key) {
      next[index] = rendered;
      return next;
    }
  }
  next.push(rendered);
  return next;
}

function buildSkillContent(
  frontmatter: string[],
  name: string,
  description: string,
  instructions: string
): string {
  let lines = setFrontmatterField(frontmatter, "name", name);
  lines = setFrontmatterField(lines, "description", description);
  return "---\n" + lines.join("\n") + "\n---\n" + instructions;
}

function skillLinkTarget(row: SkillRowData): string {
  return skillsHref(row.scope, row.name);
}

interface DeleteSkillDialogProps {
  skill: SkillRowData;
  onClose: () => void;
  onDeleted: () => void;
  onReload: () => void;
}

function DeleteSkillDialog({
  skill,
  onClose,
  onDeleted,
  onReload,
}: DeleteSkillDialogProps) {
  const announce = useAnnounce();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

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

  return (
    <Dialog
      title={"Delete “" + skill.name + "”?"}
      dismissible={!busy}
      onClose={onClose}
    >
      <p>
        The skill folder <code>{path}</code> is removed and nothing else changes.
        An agent that still lists <strong>{skill.name}</strong> keeps the name in
        its <code>skills</code> list. The runtime skips a listed skill that has
        no folder (Q-026).
      </p>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <div className="grow">
            <p className="skills-alert-msg">{error}</p>
            <DetailsList details={details} />
          </div>
        </div>
      ) : null}
      <div className="dialog-actions">
        <Button variant="secondary" disabled={busy} onClick={onClose}>
          Cancel
        </Button>
        <Button variant="danger" disabled={busy} loading={busy} onClick={confirm}>
          {busy ? "Deleting…" : "Delete skill"}
        </Button>
      </div>
    </Dialog>
  );
}

interface SkillRowProps {
  skill: SkillRowData;
  onOpen: (event: MouseEvent<HTMLDivElement>) => void;
  onDelete: () => void;
}

function SkillRow({ skill, onOpen, onDelete }: SkillRowProps) {
  const valid = skill.valid !== false;
  const canDelete = SKILL_NAME_RE.test(String(skill.name || ""));
  return (
    <div className={"mini skills-row" + (valid ? "" : " is-invalid")} onClick={onOpen}>
      <span
        className={"skills-tile" + (valid ? "" : " is-invalid")}
        aria-hidden="true"
      >
        <Ic name="file-code-2" size={15} />
      </span>
      <span className="skills-row-main">
        <span className="skills-row-title">
          <a className="skills-name" href={skillLinkTarget(skill)}>
            {skill.name}
          </a>
          {valid ? null : (
            <Badge tone="red" dot>
              Can't load
            </Badge>
          )}
        </span>
        {valid ? (
          <span className="skills-row-desc">{skill.description || ""}</span>
        ) : (
          <span className="skills-row-desc is-error">
            {skill.error || "This skill cannot be loaded."}
          </span>
        )}
      </span>
      <span className="skills-row-scope">
        <ScopeBadge scope={skill.scope} />
      </span>
      <span className="skills-row-size num">
        {valid ? formatSize(skill.size) : "–"}
      </span>
      <span className="skills-row-updated">
        {agoLabel(skill.updated_at) || "—"}
      </span>
      <span className="skills-row-actions">
        {valid || canDelete ? (
          <IconButton
            className="skills-del"
            icon="trash-2"
            size="sm"
            label={"Delete skill " + skill.name}
            onClick={(event: MouseEvent<HTMLButtonElement>) => {
              event.stopPropagation();
              onDelete();
            }}
          />
        ) : null}
        <a
          className="ibtn ibtn-sm"
          href={skillLinkTarget(skill)}
          aria-label={"Open skill " + skill.name}
          onClick={(event: MouseEvent<HTMLAnchorElement>) =>
            event.stopPropagation()
          }
        >
          <Ic name="chevron-right" size={16} />
        </a>
      </span>
    </div>
  );
}

interface SkillsPageData {
  skills: SkillRowData[];
  invalid: SkillRowData[];
}

export function SkillsPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading"
  );
  const [page, setPage] = useState<SkillsPageData | null>(null);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState("all");
  const [pageIndex, setPageIndex] = useState(0);
  const [reloadToken, setReloadToken] = useState(0);
  const [deleteTarget, setDeleteTarget] = useState<SkillRowData | null>(null);

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
      .concat(
        (page.invalid || []).map((row) =>
          Object.assign({}, row, { valid: false })
        )
      )
      .sort(compareSkills);
  }, [page]);

  const scopeOptions = useMemo(() => {
    const names = new Set<string>();
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
        (row) =>
          (scope === "all" || row.scope === scope) &&
          skillMatches(row, search)
      ),
    [rows, scope, search]
  );

  const pageCount = Math.max(1, Math.ceil(visible.length / PAGE_SIZE));
  const currentPage = Math.min(pageIndex, pageCount - 1);
  const pageRows = visible.slice(
    currentPage * PAGE_SIZE,
    (currentPage + 1) * PAGE_SIZE
  );
  const firstRow = visible.length === 0 ? 0 : currentPage * PAGE_SIZE + 1;
  const lastRow = Math.min((currentPage + 1) * PAGE_SIZE, visible.length);

  function changeQuery(value: string) {
    setQuery(value);
    setPageIndex(0);
  }

  function changeScope(value: string) {
    setScope(value);
    setPageIndex(0);
  }

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function openRow(event: MouseEvent<HTMLDivElement>, row: SkillRowData) {
    const target = event.target;
    if (
      target instanceof Element &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    window.location.hash = skillLinkTarget(row);
  }

  const summary =
    validCount +
    " skill" +
    (validCount === 1 ? "" : "s") +
    " · " +
    invalidCount +
    " can't load";

  return (
    <main id="main" className="page skills-page">
      <header className="ph">
        <div>
          <h1>Skills</h1>
          <p>
            Instructions an agent loads when a task needs them. Global skills
            work for every agent.
          </p>
        </div>
        <div className="acts">
          <Button as="a" variant="primary" icon="plus" href="#/skills/new">
            New skill
          </Button>
        </div>
      </header>

      <div className="skills-toolbar">
        <SearchInput
          wrapClassName="skills-search"
          aria-label="Search skills"
          placeholder="Search skills"
          autoComplete="off"
          spellCheck={false}
          kbd={null}
          value={query}
          onChange={(event: ChangeEvent<HTMLInputElement>) =>
            changeQuery(event.target.value)
          }
        />
        <Seg
          className="skills-scope-seg"
          aria-label="Filter by scope"
          options={[
            { value: "all", label: "All scopes" },
            { value: "global", label: "Global" },
            ...scopeOptions.map((value) => ({ value, label: value })),
          ]}
          value={scope}
          onChange={changeScope}
        />
        {status === "ready" ? (
          <p className="skills-summary" aria-live="polite">
            {summary}
          </p>
        ) : null}
      </div>

      {status === "loading" ? (
        <div className="panel skills-list">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="mini skills-row skills-row-skeleton">
              <Skeleton width={30} height={30} radius={8} />
              <span className="skills-row-main">
                <Skeleton width="40%" height={12} />
                <Skeleton width="70%" height={10} />
              </span>
            </div>
          ))}
        </div>
      ) : null}

      {status === "error" ? (
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load skills"
            text={error}
            action={
              <Button variant="secondary" onClick={reload}>
                Try again
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
      ) : null}

      {status === "ready" && rows.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="sparkles"
            title="No skills yet"
            text="A skill is a folder with a SKILL.md that an agent loads when a task needs it."
            action={
              <Button as="a" variant="primary" icon="plus" href="#/skills/new">
                New skill
              </Button>
            }
          />
        </div>
      ) : null}

      {status === "ready" && rows.length > 0 && visible.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="search-x"
            size="sm"
            title="No skills match"
            text="No skills match the current search and scope."
            role="status"
          />
        </div>
      ) : null}

      {status === "ready" && visible.length > 0 ? (
        <section className="panel skills-list" aria-label="Skills">
          <div className="skills-list-head" aria-hidden="true">
            <span>Skill</span>
            <span>Scope</span>
            <span>Size</span>
            <span>Updated</span>
            <span></span>
          </div>
          {pageRows.map((row) => (
            <SkillRow
              key={row.scope + "/" + row.name}
              skill={row}
              onOpen={(event) => openRow(event, row)}
              onDelete={() => setDeleteTarget(row)}
            />
          ))}
          <div className="skills-pagination">
            <p className="skills-pagination-info">
              Showing {firstRow}–{lastRow} of {visible.length}
            </p>
            <span className="skills-pagination-page">
              Page {currentPage + 1} of {pageCount}
            </span>
            <div className="skills-pager">
              <Button
                size="sm"
                variant="secondary"
                disabled={currentPage === 0}
                onClick={() => setPageIndex(Math.max(0, currentPage - 1))}
              >
                Previous
              </Button>
              <Button
                size="sm"
                variant="secondary"
                disabled={currentPage >= pageCount - 1}
                onClick={() =>
                  setPageIndex(Math.min(pageCount - 1, currentPage + 1))
                }
              >
                Next
              </Button>
            </div>
          </div>
        </section>
      ) : null}

      {deleteTarget ? (
        <DeleteSkillDialog
          skill={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={() => {
            setDeleteTarget(null);
            reload();
          }}
          onReload={reload}
        />
      ) : null}
    </main>
  );
}

function agentModelLabel(agent: AgentLike | null | undefined): string {
  if (!agent) {
    return "";
  }
  return splitModel(agent).model || String(agent.model || "");
}

/* The team roster is the lead plus the members (REQ-TEAM-023). It comes from
   the shared helper so the order matches the agents list; a team with no
   roster has no agents to add. */
function teamMembers(team: AgentLike | null | undefined): string[] {
  if (!team) {
    return [];
  }
  const teamName = String(team.name || "");
  return rosterNames(team).filter((name) => name !== teamName);
}

interface AddAgentsDialogProps {
  skillName: string;
  agents: Agent[];
  selected: string[];
  onClose: () => void;
  onAdd: (names: string[]) => void;
}

function AddAgentsDialog({
  skillName,
  agents,
  selected,
  onClose,
  onAdd,
}: AddAgentsDialogProps) {
  const [query, setQuery] = useState("");
  const [picks, setPicks] = useState<string[]>([]);
  const selectedSet = new Set(selected);
  const search = query.trim().toLowerCase();

  const agentOptions = agents.filter((agent) => !isTeam(agent));
  const teamOptions = agents.filter(isTeam);

  function matches(value: Agent): boolean {
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

  function toggleAgent(name: string) {
    setPicks((current) =>
      current.includes(name)
        ? current.filter((item) => item !== name)
        : current.concat([name])
    );
  }

  function toggleTeam(team: Agent) {
    const members = teamMembers(team);
    const every =
      members.length > 0 &&
      members.every(
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

  return (
    <Dialog title={"Add agents to " + skillName} onClose={onClose}>
      <p className="skills-dialog-subtitle">
        Picked agents can load this skill when a task needs it.
      </p>
      <SearchInput
        wrapClassName="skills-dialog-search"
        id="skills-add-agent-search"
        data-autofocus
        aria-label="Search agents"
        placeholder="Search agents"
        autoComplete="off"
        spellCheck={false}
        kbd={null}
        value={query}
        onChange={(event: ChangeEvent<HTMLInputElement>) =>
          setQuery(event.target.value)
        }
      />
      <div className="skills-add-list">
        {agentOptions.filter(matches).map((agent) => {
          const added = selectedSet.has(agent.name);
          const checked = added || picks.includes(agent.name);
          const model = agentModelLabel(agent);
          return (
            <label
              key={agent.name}
              className={
                "skills-add-option" +
                (checked ? " is-checked" : "") +
                (added ? " is-added" : "")
              }
            >
              <Checkbox
                checked={checked}
                disabled={added}
                onChange={() => toggleAgent(agent.name)}
              />
              <AgentAvatar
                name={agent.name}
                color={agentColor(agent.name)}
                size="lg"
              />
              <span className="skills-add-text">
                <span className="skills-add-name">{agent.name}</span>
                {model ? <span className="skills-add-meta">{model}</span> : null}
              </span>
              {added ? <span className="skills-add-state">Added</span> : null}
            </label>
          );
        })}
        {teamOptions.filter(matches).map((team) => {
          const members = teamMembers(team);
          const allAdded =
            members.length > 0 &&
            members.every((name) => selectedSet.has(name));
          const allPicked =
            members.length > 0 &&
            members.every(
              (name) => selectedSet.has(name) || picks.includes(name)
            );
          return (
            <label
              key={team.name}
              className={
                "skills-add-option skills-add-team" +
                (allAdded ? " is-added" : "")
              }
            >
              <Checkbox
                checked={allPicked}
                disabled={allAdded || members.length === 0}
                onChange={() => toggleTeam(team)}
              />
              <AgentAvatarStack
                size="sm"
                max={3}
                items={rosterNames(team).map((member) => ({
                  name: member,
                  color: agentColor(member),
                }))}
              />
              <span className="skills-add-text">
                <span className="skills-add-name-row">
                  <span className="skills-add-name">{team.name}</span>
                  <Badge tone="accent" dot>
                    Team
                  </Badge>
                </span>
                <span className="skills-add-meta">
                  {members.length === 0
                    ? "No members yet"
                    : "Adds it to all " +
                      members.length +
                      " agents in the team"}
                </span>
              </span>
              {allAdded ? <span className="skills-add-state">Added</span> : null}
            </label>
          );
        })}
        {agentOptions.filter(matches).length === 0 &&
        teamOptions.filter(matches).length === 0 ? (
          <p className="skills-add-empty" role="status">
            No agents match “{query.trim()}”.
          </p>
        ) : null}
      </div>
      <div className="dialog-actions">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button
          variant="primary"
          disabled={pending.length === 0}
          onClick={() => {
            onAdd(pending);
            onClose();
          }}
        >
          {pending.length === 0
            ? "Add agents"
            : pending.length === 1
              ? "Add 1 agent"
              : "Add " + pending.length + " agents"}
        </Button>
      </div>
    </Dialog>
  );
}

interface AgentPickerRowProps {
  agent: AgentLike & { name: string };
  skillName: string;
  readOnly: boolean;
  onRemove?: () => void;
}

function AgentPickerRow({
  agent,
  skillName,
  readOnly,
  onRemove,
}: AgentPickerRowProps) {
  const model = agentModelLabel(agent);
  return (
    <div className="mini skills-agent-row">
      <AgentAvatar name={agent.name} color={agentColor(agent.name)} size="md" />
      <span className="skills-agent-text">
        <span className="skills-agent-name">{agent.name}</span>
        {model ? <span className="skills-agent-model">{model}</span> : null}
      </span>
      {readOnly ? (
        <span className="skills-agent-owner">Owner</span>
      ) : (
        <IconButton
          className="skills-agent-remove"
          size="sm"
          icon="x"
          label={"Remove " + agent.name + " from " + skillName}
          onClick={onRemove}
        />
      )}
    </div>
  );
}

interface AgentLoadResult {
  ok: boolean;
  agents: Agent[];
  error?: unknown;
}

interface ConfigLoadResult {
  ok: boolean;
  version: string;
}

interface AgentUpdateResult {
  name: string;
  ok: boolean;
  message: string;
}

interface AgentChange {
  name: string;
  skills: string[];
}

export interface SkillEditorPageProps {
  scope?: string;
  name?: string;
  isNew?: boolean;
}

export function SkillEditorPage({ scope, name, isNew }: SkillEditorPageProps) {
  const announce = useAnnounce();
  const newSkill = Boolean(isNew);
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading"
  );
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [stale, setStale] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  const [frontmatter, setFrontmatter] = useState<string[]>([]);
  const [skillName, setSkillName] = useState("");
  const [skillScope, setSkillScope] = useState("global");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");
  const [version, setVersion] = useState("");
  const [loadedPath, setLoadedPath] = useState("");
  const [updatedAt, setUpdatedAt] = useState(0);
  const [created, setCreated] = useState(false);

  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentsError, setAgentsError] = useState("");
  const [configVersion, setConfigVersion] = useState("");
  const [selectedAgents, setSelectedAgents] = useState<string[]>([]);
  const [baselineAgents, setBaselineAgents] = useState<string[]>([]);

  const [addOpen, setAddOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveState, setSaveState] = useState<{
    tone: string;
    message: string;
  } | null>(null);
  const [agentResults, setAgentResults] = useState<AgentUpdateResult[] | null>(
    null
  );
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

    const skillLoad: Promise<SkillContentResponse | null> = newSkill
      ? Promise.resolve(null)
      : getSkill(scope || "", name || "");
    const agentsLoad: Promise<AgentLoadResult> = listAgents()
      .then((result) => ({
        ok: true,
        agents: result && Array.isArray(result.agents) ? result.agents : [],
      }))
      .catch((failure) => ({ ok: false, agents: [], error: failure }));
    const configLoad: Promise<ConfigLoadResult> = getConfig()
      .then((result) => ({
        ok: true,
        version: result && result.version ? String(result.version) : "",
      }))
      .catch(() => ({ ok: false, version: "" }));
    const listLoad: Promise<ListSkillsResponse | null> = newSkill
      ? Promise.resolve(null)
      : listSkills().catch(() => null);

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

        const parsed = parseSkillContent(
          loaded && loaded.content ? loaded.content : ""
        );
        setFrontmatter(parsed.frontmatter);
        setSkillName(name || "");
        setSkillScope(scope || "");
        setDescription(parsed.description);
        setInstructions(parsed.body);
        setVersion(loaded && loaded.version ? String(loaded.version) : "");
        setLoadedPath(loaded && loaded.path ? String(loaded.path) : "");
        let stamp = 0;
        if (listed) {
          const all = (listed.skills || []).concat(listed.invalid || []);
          const match = all.find(
            (row) => row.scope === scope && row.name === name
          );
          if (match) {
            stamp = Number(match.updated_at) || 0;
          }
        }
        setUpdatedAt(stamp);
        const listedAgents = rows
          .filter((agent) => !isTeam(agent))
          .filter(
            (agent) =>
              Array.isArray(agent.skills) &&
              agent.skills.map(String).includes(name || "")
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
    const map = new Map<string, Agent>();
    for (const agent of agents) {
      map.set(agent.name, agent);
    }
    return map;
  }, [agents]);

  const selectedRows = selectedAgents.map(
    (agentName) =>
      agentsByName.get(agentName) || { name: agentName, kind: "agent" }
  );
  const agentTotal = agents.filter((agent) => !isTeam(agent)).length;
  const readOnlyName = !newSkill || created;
  const readOnlyScope = !newSkill || created;
  const savedName = newSkill ? skillName.trim() : name || "";
  const folder = folderPath(skillScope, savedName, loadedPath);
  const descriptionLength = textLength(description);
  const instructionsLength = textLength(instructions);
  const descriptionOver = descriptionLength > DESCRIPTION_LIMIT;
  const instructionsOver = instructionsLength > INSTRUCTIONS_LIMIT;

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function updateSelected(next: string[]) {
    setSelectedAgents(next);
    setSaveState(null);
    setAgentResults(null);
  }

  function removeAgent(agentName: string) {
    updateSelected(selectedAgents.filter((item) => item !== agentName));
  }

  function addAgents(names: string[]) {
    const next = selectedAgents.slice();
    for (const agentName of names) {
      if (!next.includes(agentName)) {
        next.push(agentName);
      }
    }
    updateSelected(next);
    const count = next.length - selectedAgents.length;
    announce(
      "Added " +
        (count === 1 ? "1 agent" : count + " agents") +
        " to the list. Save the skill to apply."
    );
  }

  function agentChanges(): AgentChange[] {
    const before = new Set(baselineAgents);
    const after = new Set(selectedAgents);
    const names = new Set(baselineAgents.concat(selectedAgents));
    const changes: AgentChange[] = [];
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
      const current = Array.isArray(agent.skills)
        ? agent.skills.map(String)
        : [];
      let nextSkills: string[];
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

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving || status !== "ready") {
      return;
    }
    const expectedName = newSkill ? skillName.trim() : name || "";
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
      const result = await putSkill(
        skillScope,
        expectedName,
        content,
        expectedVersion
      );
      const nextVersion = result && result.version ? String(result.version) : "";
      setVersion(nextVersion);
      if (result && result.path) {
        setLoadedPath(String(result.path));
      }
      if (newSkill && !created) {
        setCreated(true);
      }

      const changes = skillScope === "global" ? agentChanges() : [];
      const results: AgentUpdateResult[] = [];
      let currentVersion = configVersion;
      for (const change of changes) {
        if (!currentVersion) {
          results.push({
            name: change.name,
            ok: false,
            message:
              "The config version is unavailable. Reload the editor to try again.",
          });
          continue;
        }
        try {
          const saved = await putAgentSkills(
            change.name,
            change.skills,
            currentVersion
          );
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
            message =
              "The config changed since the editor loaded. Reload and try again.";
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
            nextBaseline = nextBaseline.filter(
              (item) => item !== result.name
            );
          }
        }
        setBaselineAgents(nextBaseline);
      } else {
        setBaselineAgents(selectedAgents.slice());
      }

      const failed = results.filter((result) => !result.ok).length;
      if (failed > 0) {
        const message =
          "Skill saved. " +
          (results.length - failed) +
          " of " +
          results.length +
          " agent updates applied.";
        setSaveState({ tone: "warn", message });
        announce(message);
      } else if (results.length > 0) {
        const message =
          (newSkill ? "Skill created. " : "Skill saved. ") +
          results.length +
          (results.length === 1 ? " agent updated." : " agents updated.");
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
    return (
      <main id="main" className="page skills-editor-page">
        <div className="ph">
          <div>
            <a className="skills-back" href="#/skills">
              <Ic name="arrow-left" size={14} />
              Skills
            </a>
          </div>
        </div>
        <div className="panel skills-editor-loading" aria-busy="true">
          <span className="sr">Loading skill…</span>
          <Skeleton width={180} height={14} />
          <Skeleton width="100%" height={30} radius={6} />
          <Skeleton width="100%" height={30} radius={6} />
          <Skeleton width="100%" height={120} radius={6} />
        </div>
      </main>
    );
  }

  if (status === "error") {
    return (
      <main id="main" className="page skills-editor-page">
        <header className="ph">
          <div>
            <a className="skills-back" href="#/skills">
              <Ic name="arrow-left" size={14} />
              Skills
            </a>
            <h1>{newSkill ? "New skill" : name}</h1>
          </div>
          <div className="acts">
            <Button as="a" variant="secondary" href="#/skills">
              Back to skills
            </Button>
          </div>
        </header>
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load the skill"
            text={error}
            action={
              <Button variant="secondary" onClick={reload}>
                Try again
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
      </main>
    );
  }

  const headingName = newSkill ? skillName.trim() || "New skill" : name || "";
  const showGlobalAgents = skillScope === "global";
  const scopeChoices = agents.filter((agent) => !isTeam(agent));
  const subtitle = newSkill
    ? "Write the instructions once, then choose who can load them."
    : updatedAt
      ? "Edited " + agoLabel(updatedAt) + " · loaded on demand with skill_load."
      : "Loaded on demand with skill_load when a task needs it.";

  return (
    <main id="main" className="page skills-editor-page">
      <header className="ph">
        <div>
          <a className="skills-back" href="#/skills">
            <Ic name="arrow-left" size={14} />
            Skills
          </a>
          <h1>{headingName}</h1>
          <p>{subtitle}</p>
        </div>
        <div className="acts">
          <ScopeBadge scope={skillScope} />
          <Button as="a" variant="secondary" href="#/skills">
            Cancel
          </Button>
          <Button
            type="submit"
            form="skill-editor-form"
            variant="primary"
            loading={saving}
            disabled={saving}
          >
            {saving ? "Saving…" : "Save skill"}
          </Button>
        </div>
      </header>

      {error || saveState || (agentResults && agentResults.length > 0) ? (
        <div className="skills-feedback">
          {error ? (
            <div className="alert danger" role="alert">
              <Ic name="circle-alert" size={15} />
              <div className="grow">
                <p className="skills-alert-msg">{error}</p>
                <DetailsList details={details} />
              </div>
              {stale ? (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={
                    created
                      ? () => {
                          window.location.hash = skillsHref(
                            skillScope,
                            savedName
                          );
                        }
                      : reload
                  }
                >
                  {created ? "Open the saved skill" : "Reload"}
                </Button>
              ) : null}
            </div>
          ) : null}
          {saveState ? (
            <div
              className={"alert " + (saveState.tone === "warn" ? "warn" : "ok")}
              aria-live="polite"
            >
              <Ic
                name={
                  saveState.tone === "warn" ? "triangle-alert" : "circle-check"
                }
                size={15}
              />
              <span>{saveState.message}</span>
            </div>
          ) : null}
          {agentResults && agentResults.length > 0 ? (
            <ul className="skills-agent-results">
              {agentResults.map((result) => (
                <li
                  key={result.name}
                  className={
                    result.ok ? "skills-result-ok" : "skills-result-error"
                  }
                >
                  <strong>{result.name}</strong>: {result.message}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      <form
        id="skill-editor-form"
        className="skills-form"
        onSubmit={save}
        noValidate
      >
        <section className="sblock skills-block">
          <div className="srow">
            <div className="skills-srow-text">
              <label className="t" htmlFor="skill-name">
                Name
              </label>
              <div className="d" id="skill-name-hint">
                Lowercase letters, digits, - and _.
              </div>
            </div>
            <div className="skills-ctl">
              {readOnlyName ? (
                <Input
                  id="skill-name"
                  className="skills-readonly mono"
                  type="text"
                  value={newSkill ? skillName : name || ""}
                  readOnly
                  aria-describedby="skill-name-hint"
                />
              ) : (
                <Input
                  id="skill-name"
                  className="mono"
                  type="text"
                  name="name"
                  value={skillName}
                  maxLength={64}
                  autoComplete="off"
                  spellCheck={false}
                  pattern="[a-z][a-z0-9_-]{0,63}"
                  required
                  invalid={Boolean(nameError)}
                  aria-describedby={
                    "skill-name-hint" + (nameError ? " skill-name-error" : "")
                  }
                  onChange={(event: ChangeEvent<HTMLInputElement>) => {
                    setSkillName(event.target.value);
                    setNameError("");
                  }}
                />
              )}
              {nameError ? (
                <span id="skill-name-error" className="err">
                  <Ic name="circle-alert" size={12} />
                  {nameError}
                </span>
              ) : null}
            </div>
          </div>

          <div className="srow">
            <div className="skills-srow-text">
              <label className="t" htmlFor="skill-scope">
                Scope
              </label>
              <div className="d" id="skill-scope-hint">
                {readOnlyScope
                  ? "The scope cannot change after the skill is created."
                  : "Shared library. Or pick one agent to keep it private."}
              </div>
            </div>
            <div className="skills-ctl">
              {readOnlyScope ? (
                <Input
                  id="skill-scope"
                  className="skills-readonly"
                  type="text"
                  value={scopeLabel(skillScope)}
                  readOnly
                  aria-describedby="skill-scope-hint"
                />
              ) : (
                <Select
                  id="skill-scope"
                  name="scope"
                  aria-describedby="skill-scope-hint"
                  options={[
                    { value: "global", label: "Global" },
                    ...scopeChoices.map((agent) => ({
                      value: "agent:" + agent.name,
                      label: agent.name,
                    })),
                  ]}
                  value={skillScope}
                  onChange={setSkillScope}
                />
              )}
            </div>
          </div>

          <div className="srow">
            <div className="skills-srow-text">
              <div className="t">Folder</div>
              <div className="d">Where the SKILL.md file lives.</div>
            </div>
            <div className="skills-ctl">
              <code className="skills-folder mono">{folder}</code>
            </div>
          </div>
        </section>

        <section className="sblock skills-block">
          <h2>Description</h2>
          <div className="field skills-field">
            <Textarea
              id="skill-description"
              className="skills-description"
              aria-label="Description"
              name="description"
              rows={4}
              value={description}
              invalid={descriptionOver}
              aria-describedby={
                "skill-description-hint skill-description-counter" +
                (descriptionOver ? " skill-description-error" : "")
              }
              onChange={(event: ChangeEvent<HTMLTextAreaElement>) => {
                setDescription(event.target.value);
                setSaveState(null);
              }}
            ></Textarea>
            <span className="skills-counter-row">
              <span id="skill-description-hint" className="hint">
                Agents read this to decide when to load the skill.
              </span>
              <span
                id="skill-description-counter"
                className={
                  "skills-counter" + (descriptionOver ? " is-over" : "")
                }
              >
                {formatCount(descriptionLength)} /{" "}
                {formatCount(DESCRIPTION_LIMIT)}
              </span>
            </span>
            {descriptionOver ? (
              <span id="skill-description-error" className="err">
                <Ic name="circle-alert" size={12} />
                {"Description is over the " +
                  formatCount(DESCRIPTION_LIMIT) +
                  "-character limit. Saving shows the server error; nothing is written."}
              </span>
            ) : null}
          </div>
        </section>

        <section className="sblock skills-block">
          <h2 className="skills-block-h">
            Agents
            <span className="skills-agents-count">
              {showGlobalAgents
                ? selectedAgents.length + " of " + agentTotal
                : "Owner"}
            </span>
          </h2>
          <div className="panel skills-agents-panel">
            {showGlobalAgents ? (
              selectedRows.map((agent) => (
                <AgentPickerRow
                  key={agent.name}
                  agent={agent}
                  skillName={savedName || "this skill"}
                  readOnly={false}
                  onRemove={() => removeAgent(agent.name)}
                />
              ))
            ) : (
              <AgentPickerRow
                agent={
                  agentsByName.get(scopeAgent(skillScope)) || {
                    name: scopeAgent(skillScope),
                    kind: "agent",
                  }
                }
                skillName={savedName || "this skill"}
                readOnly={true}
              />
            )}
            {showGlobalAgents ? (
              <button
                type="button"
                className="mini skills-add-agent"
                disabled={agentsError !== ""}
                onClick={() => setAddOpen(true)}
              >
                <span className="skills-add-icon" aria-hidden="true">
                  <Ic name="plus" size={16} />
                </span>
                <span>Add agent</span>
              </button>
            ) : null}
          </div>
          <span className="hint">
            {showGlobalAgents
              ? "Only agents in this list load the skill."
              : "This skill belongs to one agent."}
          </span>
          {agentsError ? (
            <span className="err">
              <Ic name="circle-alert" size={12} />
              {agentsError}
            </span>
          ) : null}
        </section>

        <section className="sblock skills-block">
          <h2 className="skills-block-h">
            Instructions
            <span className="skills-markdown">Markdown</span>
          </h2>
          <Textarea
            id="skill-instructions"
            className="mono skills-instructions"
            aria-label="Instructions"
            name="instructions"
            value={instructions}
            invalid={instructionsOver}
            aria-describedby="skill-instructions-hint skill-instructions-counter"
            onChange={(event: ChangeEvent<HTMLTextAreaElement>) => {
              setInstructions(event.target.value);
              setSaveState(null);
            }}
          ></Textarea>
          <div className="skills-pane-foot">
            <span id="skill-instructions-hint" className="hint">
              Loaded on demand with skill_load. Keep it short and specific.
            </span>
            <span
              id="skill-instructions-counter"
              className={
                "skills-counter" + (instructionsOver ? " is-over" : "")
              }
            >
              {formatCount(instructionsLength)} /{" "}
              {formatCount(INSTRUCTIONS_LIMIT)}
            </span>
          </div>
        </section>
      </form>

      {addOpen ? (
        <AddAgentsDialog
          skillName={savedName || "this skill"}
          agents={agents}
          selected={selectedAgents}
          onClose={() => setAddOpen(false)}
          onAdd={addAgents}
        />
      ) : null}
    </main>
  );
}

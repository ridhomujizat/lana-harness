import { useCallback, useEffect, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import {
  checkPackage,
  deletePackage,
  getPackage,
  installPackage,
  jobLog,
  runCommand,
  setAgentUse,
  uninstallPackage,
  updatePackage,
} from "../api/toolbox";
import type { CommandView, PackageDetail, RunResult } from "../api/toolbox";
import { Ic } from "../ui/icons";
import { Badge, Button, ErrorState, Input, Modal, Panel, Select, Textarea, Toggle, useToast } from "../ui/gr8r";
import { AppIcon, StateBadge, isInstalled, manifestIssues, useToolboxStream } from "./tools";
import type { ManifestIssue } from "./tools";
import { AgentAvatar } from "./agent-avatar";

/* Tool detail (spec #34 r2): install with one click, turn the tool on for
   agents, try its commands; methods and the manifest sit under Advanced. */

function TryIt({ pkgId, command, disabled }: { pkgId: string; command: CommandView; disabled: boolean }) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      command.input.map((field) => [
        field.name,
        field.default === undefined || field.default === null ? "" : String(field.default),
      ])
    )
  );
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<RunResult | null>(null);
  const [error, setError] = useState("");

  async function run() {
    setBusy(true);
    setError("");
    setResult(null);
    const input: Record<string, unknown> = {};
    for (const field of command.input) {
      const raw = values[field.name] ?? "";
      if (raw === "") {
        continue;
      }
      input[field.name] =
        field.type === "number" || field.type === "integer" ? Number(raw) : field.type === "boolean" ? raw === "true" : raw;
    }
    try {
      setResult(await runCommand(pkgId, command.name, input));
    } catch (failure) {
      setError(errorMessage(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="tl-try">
      <div className="tl-try-fields">
        {command.input.map((field) => (
          <label key={field.name} className="tl-field">
            <span>
              {field.description || field.name}
              {field.required ? <i className="req">*</i> : null}
            </span>
            {field.enum && field.enum.length > 0 ? (
              <Select
                value={values[field.name] || ""}
                onChange={(next) => setValues({ ...values, [field.name]: next })}
                options={field.enum.map((option) => ({ value: option, label: option }))}
                aria-label={field.name}
              />
            ) : (
              <Input
                value={values[field.name] || ""}
                onChange={(event) => setValues({ ...values, [field.name]: event.target.value })}
                aria-label={field.name}
              />
            )}
          </label>
        ))}
      </div>
      <div className="tl-try-acts">
        <Button size="sm" icon="play" loading={busy} disabled={disabled} onClick={() => void run()}>
          Try it
        </Button>
        {disabled ? <span className="muted">Install it first.</span> : null}
      </div>
      {error ? <pre className="tl-out err">{error}</pre> : null}
      {result ? (
        <pre className="tl-out">
          <span className="muted">
            exit {result.exit_code} · {result.duration_ms} ms
          </span>
          {"\n"}
          {result.output || "(no output)"}
        </pre>
      ) : null}
    </div>
  );
}

function JobLog({ pkgId, jobId }: { pkgId: string; jobId: string }) {
  const [text, setText] = useState("");
  const [done, setDone] = useState(false);
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const poll = async () => {
      try {
        const log = await jobLog(pkgId, jobId);
        if (!live) {
          return;
        }
        setText(log.text);
        setDone(log.done);
        if (!log.done) {
          timer = setTimeout(poll, 1000);
        }
      } catch {
        if (live) {
          timer = setTimeout(poll, 2000);
        }
      }
    };
    void poll();
    return () => {
      live = false;
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [pkgId, jobId]);
  useEffect(() => {
    if (box.current) {
      box.current.scrollTop = box.current.scrollHeight;
    }
  }, [text]);
  return <pre ref={box} className={"tl-log" + (done ? "" : " live")}>{text || (done ? "(empty log)" : "Starting…")}</pre>;
}

function CommandRow({ pkgId, command, installed }: { pkgId: string; command: CommandView; installed: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="tl-cmd">
      <button type="button" className="tl-cmd-h" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="tl-cmd-text">
          <b>{command.description}</b>
          <code>{command.tool}</code>
        </span>
        <span className={"risk r-" + command.risk}>{command.risk}</span>
        <Ic name={open ? "chevron-up" : "chevron-down"} size={15} />
      </button>
      {open ? <TryIt pkgId={pkgId} command={command} disabled={!installed} /> : null}
    </li>
  );
}

export function ToolDetailPage({ id }: { id: string }) {
  const toast = useToast();
  const [pkg, setPkg] = useState<PackageDetail | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [manifest, setManifest] = useState("");
  const [editing, setEditing] = useState(false);
  const [issues, setIssues] = useState<ManifestIssue[]>([]);
  const [confirm, setConfirm] = useState<"" | "uninstall" | "remove">("");

  const load = useCallback(() => {
    getPackage(id)
      .then((next) => {
        setPkg(next);
        setManifest((current) => current || next.manifest);
        setError("");
      })
      .catch((failure) => setError(errorMessage(failure)));
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);
  useToolboxStream(
    useCallback(
      (changed: string) => {
        if (!changed || changed === id) {
          load();
        }
      },
      [id, load]
    )
  );

  async function act(kind: string, fn: () => Promise<unknown>) {
    setBusy(kind);
    try {
      await fn();
      load();
    } catch (failure) {
      toast.show({ title: "Could not " + kind, body: errorMessage(failure), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  async function saveManifest() {
    setIssues([]);
    setBusy("save");
    try {
      await updatePackage(id, manifest);
      toast.show({ title: "Saved", tone: "ok" });
      setEditing(false);
      load();
    } catch (failure) {
      setIssues(manifestIssues(failure));
      toast.show({ title: "Could not save", body: errorMessage(failure), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  if (error && !pkg) {
    return (
      <main id="main" className="page tl-page">
        <ErrorState title="Could not open this tool" text={error} action={<Button as="a" href="#/tools">All tools</Button>} />
      </main>
    );
  }
  if (!pkg) {
    return (
      <main id="main" className="page tl-page" aria-busy="true">
        <p className="muted">Loading…</p>
      </main>
    );
  }

  const installed = isInstalled(pkg);
  const job = pkg.jobs[0];
  const showJob = job && (job.status === "running" || job.status === "failed" || pkg.installing);
  const canInstall = (pkg.auto_method ?? 0) >= 0 && pkg.install.length > 0;
  const agents = pkg.agents || [];
  const commands = pkg.commands;

  return (
    <main id="main" className="page tl-page tl-detail">
      <a className="tl-back" href="#/tools">
        <Ic name="arrow-left" size={14} /> Tools
      </a>
      <header className="tl-hero">
        <AppIcon pkg={pkg} size="lg" />
        <div className="tl-hero-text">
          <h1>
            {pkg.name} <StateBadge pkg={pkg} />
          </h1>
          <p>{pkg.description}</p>
          <p className="tl-hero-meta">
            {pkg.category ? <span>{pkg.category}</span> : null}
            {pkg.source === "custom" ? <span>Custom tool</span> : <span>From the lana catalog</span>}
            {pkg.homepage ? (
              <a href={pkg.homepage} target="_blank" rel="noreferrer">
                {pkg.homepage.replace(/^https?:\/\//, "")} <Ic name="external-link" size={12} />
              </a>
            ) : null}
          </p>
        </div>
        <div className="tl-hero-acts">
          {installed ? (
            <>
              <Button icon="refresh-cw" loading={busy === "check"} onClick={() => void act("check", () => checkPackage(id))}>
                Check again
              </Button>
              <Button variant="danger-ghost" disabled={pkg.installing} onClick={() => setConfirm("uninstall")}>
                Uninstall
              </Button>
            </>
          ) : canInstall ? (
            <Button
              variant="primary"
              icon="download"
              loading={busy === "install" || pkg.installing}
              onClick={() => void act("install", () => installPackage(id))}
            >
              {pkg.installing ? "Installing…" : "Install"}
            </Button>
          ) : (
            <Button icon="refresh-cw" loading={busy === "check"} onClick={() => void act("check", () => checkPackage(id))}>
              Check again
            </Button>
          )}
        </div>
      </header>

      {!installed && !canInstall && !pkg.installing ? (
        <div className="tl-note">
          <Ic name="info" size={15} />
          No install method fits this machine
          {pkg.install.find((entry) => entry.reason) ? " (" + pkg.install.find((entry) => entry.reason)!.reason + ")" : ""}. Install{" "}
          <b>{pkg.name}</b> yourself, then press Check again.
        </div>
      ) : null}

      {showJob && job ? (
        <Panel
          title={
            <span className="tl-job-h">
              {job.kind === "install" ? "Installing" : "Uninstalling"} · {job.label}
              <Badge tone={job.status === "succeeded" ? "green" : job.status === "running" ? "accent" : "red"} dot>
                {job.status}
              </Badge>
            </span>
          }
        >
          <JobLog key={job.id} pkgId={id} jobId={job.id} />
          {job.error ? <p className="tl-err">{job.error}</p> : null}
        </Panel>
      ) : null}

      <Panel title="Use with agents">
        <p className="tl-lead">
          {pkg.mcp
            ? "Agents you turn on here can drive the browser: open pages, read them, click, type, log in and take screenshots. Logins are kept in a private browser profile for lana."
            : "Agents you turn on here can run this tool's commands in chats."}
        </p>
        {agents.length === 0 ? <p className="muted">No agents yet.</p> : null}
        <ul className="tl-agents">
          {agents.map((agent) => (
            <li key={agent.name}>
              <AgentAvatar name={agent.name} size="md" />
              <span className="tl-agent-name">{agent.name}</span>
              <Toggle
                checked={agent.enabled}
                disabled={!installed || busy === "agent-" + agent.name}
                onChange={(on) =>
                  void act("agent-" + agent.name, async () => {
                    setPkg(await setAgentUse(id, agent.name, on));
                  })
                }
                aria-label={"Use " + pkg.name + " with " + agent.name}
              />
            </li>
          ))}
        </ul>
        {!installed ? <p className="muted tl-small">Install {pkg.name} first.</p> : null}
      </Panel>

      {commands.length > 0 ? (
        <Panel title="What it can do">
          <ul className="tl-cmds-list">
            {commands.map((command) => (
              <CommandRow key={command.name} pkgId={id} command={command} installed={installed} />
            ))}
          </ul>
          <p className="muted tl-small">Workflows can use these with the Tool node (palette → Installed tools).</p>
        </Panel>
      ) : null}

      {pkg.used_by.workflows.length > 0 ? (
        <Panel title="Used in workflows">
          <ul className="tl-plain">
            {pkg.used_by.workflows.map((wf) => (
              <li key={wf.id}>
                <Ic name="workflow" size={14} />
                <a href={"#/workflows/" + encodeURIComponent(wf.id)}>{wf.name}</a>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      <details className="tl-advanced">
        <summary>Advanced</summary>
        <div className="tl-adv-b">
          {pkg.binary_path ? (
            <p className="tl-kv">
              <span>Program</span>
              <code>{pkg.binary_path}</code>
            </p>
          ) : null}
          {pkg.error ? <p className="tl-err">{pkg.error}</p> : null}
          <h4>Install methods</h4>
          <ul className="tl-methods-list">
            {pkg.install.map((entry, index) => (
              <li key={index} className={entry.fits === false ? "off" : undefined}>
                <span>
                  <b>{entry.label}</b>
                  <code className="trunc">{entry.detail}</code>
                  {entry.fits === false && entry.reason ? <small>{entry.reason}</small> : null}
                </span>
                <Button
                  size="sm"
                  disabled={entry.fits === false || pkg.installing}
                  onClick={() => void act("install", () => installPackage(id, index))}
                >
                  {installed ? "Reinstall" : "Install"}
                </Button>
              </li>
            ))}
            {pkg.install.length === 0 ? <li className="muted">No install method.</li> : null}
          </ul>
          <h4>
            Manifest
            {pkg.source === "catalog" ? <small> · editing saves a custom copy</small> : null}
          </h4>
          {editing ? (
            <>
              <Textarea
                className="mono tl-manifest"
                rows={20}
                spellCheck={false}
                value={manifest}
                onChange={(event) => setManifest(event.target.value)}
                aria-label="Manifest (TOML)"
              />
              {issues.length > 0 ? (
                <ul className="tl-issues">
                  {issues.map((issue, index) => (
                    <li key={index}>
                      {issue.field ? <code>{issue.field}</code> : null} {issue.message}
                    </li>
                  ))}
                </ul>
              ) : null}
              <div className="tl-row-acts">
                <Button onClick={() => setEditing(false)}>Cancel</Button>
                <Button variant="primary" loading={busy === "save"} onClick={() => void saveManifest()}>
                  Save
                </Button>
              </div>
            </>
          ) : (
            <>
              <pre className="tl-out tl-manifest-view">{pkg.manifest}</pre>
              <div className="tl-row-acts">
                <Button size="sm" icon="pencil" onClick={() => setEditing(true)}>
                  Edit manifest
                </Button>
                {pkg.source === "custom" ? (
                  <Button size="sm" variant="danger-ghost" onClick={() => setConfirm("remove")}>
                    Remove custom tool
                  </Button>
                ) : null}
              </div>
            </>
          )}
        </div>
      </details>

      {confirm ? (
        <Modal
          title={confirm === "uninstall" ? "Uninstall " + pkg.name + "?" : "Remove " + pkg.name + "?"}
          subtitle={
            confirm === "uninstall"
              ? "Agents and workflows that use it will show it as missing. Its browser profile and data stay unless you remove the tool."
              : "Removes your custom manifest" + (installed ? " and uninstalls it" : "") + "."
          }
          size="sm"
          onClose={() => setConfirm("")}
          footer={
            <>
              <Button onClick={() => setConfirm("")}>Cancel</Button>
              <Button
                variant="danger"
                loading={busy === "uninstall" || busy === "remove"}
                onClick={() =>
                  void act(confirm, async () => {
                    if (confirm === "uninstall") {
                      await uninstallPackage(id);
                    } else {
                      await deletePackage(id, installed);
                      window.location.hash = "#/tools";
                    }
                    setConfirm("");
                  })
                }
              >
                {confirm === "uninstall" ? "Uninstall" : "Remove"}
              </Button>
            </>
          }
        />
      ) : null}
    </main>
  );
}


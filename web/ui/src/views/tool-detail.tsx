import { useCallback, useEffect, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import {
  checkPackage,
  deletePackage,
  getPackage,
  installPackage,
  jobLog,
  runCommand,
  uninstallPackage,
  updatePackage,
} from "../api/toolbox";
import type { CommandView, PackageDetail, RunResult } from "../api/toolbox";
import { Ic } from "../ui/icons";
import { Badge, Button, ErrorState, Input, Modal, Panel, Select, Tabs, Textarea, useToast } from "../ui/gr8r";
import { StateBadge, manifestIssues, useToolboxStream } from "./tools";
import type { ManifestIssue } from "./tools";

/* Tool detail (spec #34): install methods with a live log, the commands with a
   Try it form, the manifest editor, and who uses the tool. */

function TryIt({ pkgId, command, disabled }: { pkgId: string; command: CommandView; disabled: boolean }) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(command.input.map((field) => [field.name, field.default === undefined || field.default === null ? "" : String(field.default)]))
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
        field.type === "number" || field.type === "integer"
          ? Number(raw)
          : field.type === "boolean"
            ? raw === "true"
            : raw;
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
              {field.name}
              {field.required ? <i className="req">*</i> : null}
              {field.description ? <small> · {field.description}</small> : null}
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
                className="mono"
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
        {disabled ? <span className="muted">Install the tool first.</span> : null}
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
  return (
    <pre ref={box} className={"tl-log" + (done ? "" : " live")}>
      {text || (done ? "(empty log)" : "Starting…")}
    </pre>
  );
}

export function ToolDetailPage({ id }: { id: string }) {
  const toast = useToast();
  const [pkg, setPkg] = useState<PackageDetail | null>(null);
  const [error, setError] = useState("");
  const [tab, setTab] = useState("overview");
  const [method, setMethod] = useState("0");
  const [busy, setBusy] = useState("");
  const [manifest, setManifest] = useState("");
  const [issues, setIssues] = useState<ManifestIssue[]>([]);
  const [confirmDelete, setConfirmDelete] = useState(false);

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
      toast.show({ title: "Manifest saved", tone: "ok" });
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

  const installed = pkg.status === "installed" || pkg.status === "too_old";
  const runningJob = pkg.jobs.find((job) => job.status === "running");
  const lastJob = pkg.jobs[0];

  return (
    <main id="main" className="page tl-page">
      <header className="ph">
        <div>
          <h1 className="tl-title">
            {pkg.name} <StateBadge pkg={pkg} />
          </h1>
          <p>
            {pkg.description}
            {pkg.homepage ? (
              <>
                {" · "}
                <a href={pkg.homepage} target="_blank" rel="noreferrer">
                  {pkg.homepage.replace(/^https?:\/\//, "")}
                </a>
              </>
            ) : null}
          </p>
        </div>
        <div className="acts">
          <Button loading={busy === "check"} icon="refresh-cw" onClick={() => void act("check", () => checkPackage(id))}>
            Check again
          </Button>
        </div>
      </header>

      <Tabs
        className="tl-tabs"
        value={tab}
        onChange={setTab}
        items={[
          { value: "overview", label: "Overview" },
          { value: "commands", label: "Commands", count: pkg.commands.length },
          { value: "manifest", label: "Manifest" },
        ]}
      />

      {tab === "overview" ? (
        <div className="tl-grid">
          <Panel title="Install">
            {pkg.binary_path ? (
              <p className="tl-kv">
                <span>Binary</span>
                <code>{pkg.binary_path}</code>
              </p>
            ) : null}
            {pkg.error ? <p className="tl-err">{pkg.error}</p> : null}
            {pkg.install.length === 0 ? (
              <p className="muted">The manifest has no install method. Install the program yourself, then press Check again.</p>
            ) : (
              <>
                <div className="tl-methods" role="radiogroup" aria-label="Install method">
                  {pkg.install.map((entry, index) => (
                    <label key={index} className={"tl-method" + (method === String(index) ? " on" : "")}>
                      <input
                        type="radio"
                        name="tl-method"
                        checked={method === String(index)}
                        onChange={() => setMethod(String(index))}
                      />
                      <span>
                        <b>{entry.label || (entry.kind === "download" ? "Download" : "Install command")}</b>
                        <code className="trunc">{entry.detail}</code>
                      </span>
                      <Badge tone="gray">{entry.kind}</Badge>
                    </label>
                  ))}
                </div>
                <div className="tl-acts">
                  <Button
                    variant="primary"
                    icon="download"
                    loading={busy === "install" || Boolean(runningJob)}
                    disabled={Boolean(runningJob)}
                    onClick={() => void act("install", () => installPackage(id, Number(method)))}
                  >
                    {installed ? "Reinstall" : "Install"}
                  </Button>
                  {installed ? (
                    <Button
                      variant="danger-ghost"
                      loading={busy === "uninstall"}
                      disabled={Boolean(runningJob)}
                      onClick={() => void act("uninstall", () => uninstallPackage(id))}
                    >
                      Uninstall
                    </Button>
                  ) : null}
                </div>
              </>
            )}
            {lastJob ? (
              <div className="tl-job">
                <div className="tl-job-h">
                  <b>
                    {lastJob.kind === "install" ? "Install" : "Uninstall"} · {lastJob.label}
                  </b>
                  <Badge
                    tone={lastJob.status === "succeeded" ? "green" : lastJob.status === "running" ? "accent" : "red"}
                    dot
                  >
                    {lastJob.status}
                  </Badge>
                </div>
                <JobLog key={lastJob.id} pkgId={id} jobId={lastJob.id} />
                {lastJob.error ? <p className="tl-err">{lastJob.error}</p> : null}
              </div>
            ) : null}
          </Panel>

          <div className="tl-side">
            <Panel title="Provides">
              {pkg.commands.length === 0 ? <p className="muted">No commands. Add [[commands]] to the manifest.</p> : null}
              <ul className="tl-provides">
                {pkg.commands.map((cmd) => (
                  <li key={cmd.name}>
                    <code>{cmd.tool}</code>
                    <span className={"risk r-" + cmd.risk}>{cmd.risk}</span>
                    <small>{cmd.description}</small>
                  </li>
                ))}
              </ul>
            </Panel>
            <Panel title="Used by">
              {pkg.used_by.agents.length + pkg.used_by.workflows.length === 0 ? (
                <p className="muted">
                  Not used yet. Turn a command on in an agent's Tools tab, or add it to a workflow with the Tool node.
                </p>
              ) : null}
              <ul className="tl-provides">
                {pkg.used_by.agents.map((name) => (
                  <li key={"a" + name}>
                    <Ic name="bot" size={14} />
                    <a href={"#/agents/" + encodeURIComponent(name) + "/tools"}>{name}</a>
                  </li>
                ))}
                {pkg.used_by.workflows.map((wf) => (
                  <li key={"w" + wf.id}>
                    <Ic name="workflow" size={14} />
                    <a href={"#/workflows/" + encodeURIComponent(wf.id)}>{wf.name}</a>
                  </li>
                ))}
              </ul>
            </Panel>
          </div>
        </div>
      ) : null}

      {tab === "commands" ? (
        <div className="tl-commands">
          {pkg.commands.length === 0 ? (
            <div className="panel">
              <p className="muted tl-pad">No commands yet. Add [[commands]] to the manifest.</p>
            </div>
          ) : null}
          {pkg.commands.map((cmd) => (
            <Panel
              key={cmd.name}
              title={
                <span className="tl-cmd-title">
                  <code>{cmd.tool}</code>
                  <span className={"risk r-" + cmd.risk}>{cmd.risk}</span>
                </span>
              }
            >
              <p className="tl-desc">{cmd.description}</p>
              <TryIt pkgId={id} command={cmd} disabled={!installed} />
            </Panel>
          ))}
        </div>
      ) : null}

      {tab === "manifest" ? (
        <Panel
          title="tool.toml"
          actions={
            <>
              <Button variant="danger-ghost" size="sm" onClick={() => setConfirmDelete(true)}>
                Remove tool
              </Button>
              <Button variant="primary" size="sm" loading={busy === "save"} onClick={() => void saveManifest()}>
                Save
              </Button>
            </>
          }
        >
          <Textarea
            className="mono tl-manifest"
            rows={24}
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
        </Panel>
      ) : null}

      {confirmDelete ? (
        <Modal
          title={"Remove " + pkg.name + "?"}
          subtitle={
            installed
              ? "Lana uninstalls it first, then removes the manifest and its data. Agents and workflows that use it show it as missing."
              : "Removes the manifest. Agents and workflows that use it show it as missing."
          }
          size="sm"
          onClose={() => setConfirmDelete(false)}
          footer={
            <>
              <Button onClick={() => setConfirmDelete(false)}>Cancel</Button>
              <Button
                variant="danger"
                loading={busy === "remove"}
                onClick={() =>
                  void act("remove", async () => {
                    await deletePackage(id, installed);
                    window.location.hash = "#/tools";
                  })
                }
              >
                Remove
              </Button>
            </>
          }
        />
      ) : null}
    </main>
  );
}

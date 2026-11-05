import { useCallback, useEffect, useState } from "react";
import { ApiError, errorMessage } from "../api/client";
import { MANIFEST_TEMPLATE, addPackage, listPackages, openToolboxStream } from "../api/toolbox";
import type { PackageState, PackageView } from "../api/toolbox";
import { Ic } from "../ui/icons";
import { Badge, Button, EmptyState, ErrorState, Modal, SearchInput, Textarea } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";

/* Configure → Tools (spec #34): programs the owner installs once and then uses
   from agents in chats and from workflows. The catalog starts empty. */

export const STATE_TONES: Record<PackageState, BadgeTone> = {
  installed: "green",
  installing: "accent",
  missing: "gray",
  too_old: "amber",
  broken: "red",
  failed: "red",
};

export const STATE_WORDS: Record<PackageState, string> = {
  installed: "Installed",
  installing: "Installing",
  missing: "Not installed",
  too_old: "Too old",
  broken: "Broken",
  failed: "Install failed",
};

export function StateBadge({ pkg }: { pkg: PackageView }) {
  const state = pkg.installing ? "installing" : pkg.status;
  return (
    <Badge tone={STATE_TONES[state] || "gray"} dot>
      {STATE_WORDS[state] || state}
      {state === "installed" && pkg.version ? " · " + pkg.version : ""}
    </Badge>
  );
}

export interface ManifestIssue {
  field: string;
  message: string;
}

export function manifestIssues(error: unknown): ManifestIssue[] {
  if (!(error instanceof ApiError)) {
    return [];
  }
  const first = error.details[0] as { issues?: ManifestIssue[] } | undefined;
  return first && Array.isArray(first.issues) ? first.issues : [];
}

export function useToolboxStream(onChange: (id: string) => void) {
  useEffect(() => {
    const stream = openToolboxStream(onChange);
    return () => stream.close();
  }, [onChange]);
}

function AddToolModal({ onClose, onAdded }: { onClose: () => void; onAdded: (pkg: PackageView) => void }) {
  const [text, setText] = useState(MANIFEST_TEMPLATE);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [issues, setIssues] = useState<ManifestIssue[]>([]);

  async function save() {
    setBusy(true);
    setError("");
    setIssues([]);
    try {
      onAdded(await addPackage(text));
    } catch (failure) {
      setIssues(manifestIssues(failure));
      setError(errorMessage(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="Add a tool"
      subtitle="Describe the program in a manifest: how to find it, how to install it, and the commands agents and workflows may call."
      size="lg"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} onClick={() => void save()}>
            Add tool
          </Button>
        </>
      }
    >
      <Textarea
        className="mono tl-manifest"
        rows={22}
        value={text}
        spellCheck={false}
        onChange={(event) => setText(event.target.value)}
        aria-label="Manifest (TOML)"
      />
      <p className="tl-hint">
        The example is a starting point: change the install command to what works on your machine (Homebrew, npm,
        pipx, a curl script, nix) or use a download URL. Nothing is installed until you press Install.
      </p>
      {error ? (
        <div className="tl-issues" role="alert">
          <b>{issues.length > 0 ? "Fix the manifest" : error}</b>
          {issues.length > 0 ? (
            <ul>
              {issues.map((issue, index) => (
                <li key={index}>
                  {issue.field ? <code>{issue.field}</code> : null} {issue.message}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </Modal>
  );
}

export function ToolsPage() {
  const [packages, setPackages] = useState<PackageView[] | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);

  const load = useCallback(() => {
    listPackages()
      .then((response) => {
        setPackages(response.packages);
        setError("");
      })
      .catch((failure) => setError(errorMessage(failure)));
  }, []);

  useEffect(() => {
    load();
  }, [load]);
  useToolboxStream(useCallback(() => load(), [load]));

  const shown = (packages || []).filter((pkg) =>
    (pkg.name + " " + pkg.id + " " + pkg.description).toLowerCase().includes(query.trim().toLowerCase())
  );
  const installed = (packages || []).filter((pkg) => pkg.status === "installed").length;

  return (
    <main id="main" className="page tl-page">
      <header className="ph">
        <div>
          <h1>Tools</h1>
          <p>Programs lana can run for you. Install one once, then turn its commands on for agents or use them in workflows.</p>
        </div>
        <div className="acts">
          <Button variant="primary" icon="plus" onClick={() => setAdding(true)}>
            Add tool
          </Button>
        </div>
      </header>

      {error && !packages ? (
        <ErrorState title="Could not load tools" text={error} action={<Button onClick={load}>Try again</Button>} />
      ) : null}

      {packages && packages.length > 0 ? (
        <div className="tl-toolbar">
          <SearchInput
            value={query}
            kbd={null}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search tools"
            aria-label="Search tools"
          />
          <span className="muted">
            {packages.length} tools · {installed} installed
          </span>
        </div>
      ) : null}

      {packages && packages.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="package"
            title="No tools yet"
            text="Add a program such as Obscura (headless browser), yt-dlp or ffmpeg. Lana installs it on this machine and offers its commands to agents and workflows."
            action={
              <Button variant="primary" icon="plus" onClick={() => setAdding(true)}>
                Add tool
              </Button>
            }
          />
        </div>
      ) : null}

      {shown.length > 0 ? (
        <section className="tl-list" aria-label="Tools">
          {shown.map((pkg) => (
            <a key={pkg.id} className="tl-row" href={"#/tools/" + encodeURIComponent(pkg.id)}>
              <span className={"tl-ico s-" + pkg.status}>
                <Ic name="package" size={18} />
              </span>
              <span className="tl-main">
                <span className="tl-name">
                  <b>{pkg.name}</b>
                  <span className="muted mono">{pkg.id}</span>
                </span>
                <span className="tl-desc trunc">{pkg.description || "No description"}</span>
              </span>
              <span className="tl-cmds">
                {pkg.commands.slice(0, 3).map((cmd) => (
                  <code key={cmd.name}>{cmd.tool}</code>
                ))}
                {pkg.commands.length > 3 ? <span className="muted">+{pkg.commands.length - 3}</span> : null}
                {pkg.commands.length === 0 ? <span className="muted">no commands</span> : null}
              </span>
              <span>
                <StateBadge pkg={pkg} />
              </span>
              <Ic name="chevron-right" size={16} className="tl-chev" />
            </a>
          ))}
        </section>
      ) : null}

      {packages && packages.length > 0 && shown.length === 0 ? (
        <div className="panel">
          <EmptyState size="sm" icon="search-x" text="No tool matches the search." />
        </div>
      ) : null}

      {adding ? (
        <AddToolModal
          onClose={() => setAdding(false)}
          onAdded={(pkg) => {
            setAdding(false);
            window.location.hash = "#/tools/" + encodeURIComponent(pkg.id);
          }}
        />
      ) : null}
    </main>
  );
}

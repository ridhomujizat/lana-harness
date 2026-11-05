import { useCallback, useEffect, useState } from "react";
import type { MouseEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { MANIFEST_TEMPLATE, addPackage, installPackage, listPackages, openToolboxStream } from "../api/toolbox";
import type { PackageState, PackageView } from "../api/toolbox";
import { Ic } from "../ui/icons";
import { Badge, Button, EmptyState, ErrorState, Modal, SearchInput, Textarea, useToast } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";

/* Configure → Tools (spec #34 r2): an app list. Lana ships a small catalog
   (REQ-TB-001); the owner installs an app with one click (REQ-TB-009). Custom
   manifests stay behind "Add custom tool". */

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
  too_old: "Update needed",
  broken: "Broken",
  failed: "Install failed",
};

export function isInstalled(pkg: PackageView): boolean {
  return pkg.status === "installed" || pkg.status === "too_old";
}

export function StateBadge({ pkg }: { pkg: PackageView }) {
  const state = pkg.installing ? "installing" : pkg.status;
  return (
    <Badge tone={STATE_TONES[state] || "gray"} dot>
      {STATE_WORDS[state] || state}
      {state === "installed" && pkg.version ? " · " + pkg.version : ""}
    </Badge>
  );
}

export function AppIcon({ pkg, size = "md" }: { pkg: PackageView; size?: "md" | "lg" }) {
  return (
    <span className={"tl-app-ico " + size + (isInstalled(pkg) ? " on" : "")}>
      <Ic name={pkg.icon || "package"} size={size === "lg" ? 24 : 20} />
    </span>
  );
}

/* What an app gives lana, in one line. */
export function provides(pkg: PackageView): string {
  const parts: string[] = [];
  if (pkg.mcp) {
    parts.push("Browser control for agents");
  }
  const commands = pkg.commands.filter((cmd) => !(pkg.mcp && cmd.name === "fetch"));
  if (pkg.mcp && pkg.commands.some((cmd) => cmd.name === "fetch")) {
    parts.push("page fetch in workflows");
  }
  if (commands.length > 0) {
    parts.push(commands.map((cmd) => cmd.name).join(", "));
  }
  return parts.join(" · ");
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
  const [text, setText] = useState(MANIFEST_TEMPLATE.replace('id = "obscura"', 'id = "my-tool"'));
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
      title="Add a custom tool"
      subtitle="For a program that is not in the list: describe how to find it, how to install it, and the commands lana may run."
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
        rows={20}
        value={text}
        spellCheck={false}
        onChange={(event) => setText(event.target.value)}
        aria-label="Manifest (TOML)"
      />
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

function AppCard({ pkg, onInstall, busy }: { pkg: PackageView; onInstall: (pkg: PackageView) => void; busy: boolean }) {
  const href = "#/tools/" + encodeURIComponent(pkg.id);
  const installed = isInstalled(pkg);
  const canInstall = (pkg.auto_method ?? 0) >= 0 && pkg.install.length > 0;
  const click = (event: MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    onInstall(pkg);
  };
  return (
    <a className={"tl-card" + (installed ? " on" : "")} href={href}>
      <div className="tl-card-top">
        <AppIcon pkg={pkg} />
        <div className="tl-card-title">
          <b>{pkg.name}</b>
          <span>{pkg.category || (pkg.source === "custom" ? "Custom" : "Tool")}</span>
        </div>
      </div>
      <p className="tl-card-desc">{pkg.description}</p>
      <p className="tl-card-gives">{provides(pkg)}</p>
      <div className="tl-card-foot">
        {installed || pkg.installing || pkg.status === "failed" || pkg.status === "broken" ? (
          <StateBadge pkg={pkg} />
        ) : (
          <span className="muted tl-card-note">{canInstall ? "Ready to install" : "Install it yourself"}</span>
        )}
        {installed ? (
          <span className="tl-open">
            Open <Ic name="arrow-right" size={13} />
          </span>
        ) : pkg.installing ? null : canInstall ? (
          <Button size="sm" variant="primary" icon="download" loading={busy} onClick={click}>
            Install
          </Button>
        ) : null}
      </div>
    </a>
  );
}

export function ToolsPage() {
  const toast = useToast();
  const [packages, setPackages] = useState<PackageView[] | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState("");

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

  async function install(pkg: PackageView) {
    setBusy(pkg.id);
    try {
      await installPackage(pkg.id);
      toast.show({ title: "Installing " + pkg.name, body: "You can keep working; the card updates when it is done.", tone: "info" });
      load();
    } catch (failure) {
      toast.show({ title: "Could not install " + pkg.name, body: errorMessage(failure), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  const q = query.trim().toLowerCase();
  const shown = (packages || []).filter((pkg) =>
    (pkg.name + " " + pkg.id + " " + pkg.description + " " + (pkg.category || "")).toLowerCase().includes(q)
  );
  const installed = shown.filter(isInstalled);
  const available = shown.filter((pkg) => !isInstalled(pkg));

  return (
    <main id="main" className="page tl-page">
      <header className="ph">
        <div>
          <h1>Tools</h1>
          <p>Apps lana can use for you. Install one with a click, then let agents and workflows use it.</p>
        </div>
        <div className="acts">
          <Button icon="plus" onClick={() => setAdding(true)}>
            Add custom tool
          </Button>
        </div>
      </header>

      {error && !packages ? (
        <ErrorState title="Could not load tools" text={error} action={<Button onClick={load}>Try again</Button>} />
      ) : null}

      {packages && packages.length > 4 ? (
        <div className="tl-toolbar">
          <SearchInput
            value={query}
            kbd={null}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search tools"
            aria-label="Search tools"
          />
        </div>
      ) : null}

      {installed.length > 0 ? (
        <>
          <h2 className="tl-h2">Installed</h2>
          <div className="tl-grid-apps">
            {installed.map((pkg) => (
              <AppCard key={pkg.id} pkg={pkg} onInstall={(item) => void install(item)} busy={busy === pkg.id} />
            ))}
          </div>
        </>
      ) : null}

      {available.length > 0 ? (
        <>
          <h2 className="tl-h2">Available</h2>
          <div className="tl-grid-apps">
            {available.map((pkg) => (
              <AppCard key={pkg.id} pkg={pkg} onInstall={(item) => void install(item)} busy={busy === pkg.id} />
            ))}
          </div>
        </>
      ) : null}

      {packages && shown.length === 0 ? (
        <div className="panel">
          <EmptyState size="sm" icon="search-x" text={packages.length === 0 ? "No tools yet." : "No tool matches the search."} />
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

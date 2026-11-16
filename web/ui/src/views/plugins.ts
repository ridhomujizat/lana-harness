import { useEffect, useState } from "react";

import { listPackages, openToolboxStream, type PackageView } from "../api/toolbox";

/* Spec 56: plugins add features to Lana pages. A plugin needs one installed
   tool (spec 34); it turns on by itself once that tool is installed. */

export interface PluginDef {
  id: string;
  name: string;
  description: string;
  /* The tool (Toolbox package id) the plugin runs. */
  requires: string;
  /* Where the plugin shows up. */
  where: string;
  href: string;
  /* An agent logo key (src/views/agent-logo.tsx) or a Lucide icon name. */
  logo?: string;
  icon?: string;
}

export const PLUGINS: PluginDef[] = [
  {
    id: "claude-accounts",
    name: "Claude accounts",
    description:
      "Every Claude Code account in one popup: 5-hour and weekly usage, switch the default login, open Claude Code on any account in a new tab or beside the focused pane.",
    requires: "claude-swap",
    where: "Workbench",
    href: "#/workbench",
    logo: "claude",
  },
];

export function toolReady(pkg: PackageView | undefined): boolean {
  return Boolean(pkg && (pkg.status === "installed" || pkg.status === "too_old") && pkg.binary_path);
}

/* The Toolbox package a plugin needs, kept live with the Toolbox stream. */
export function usePluginTool(pluginId: string): PackageView | undefined {
  const plugin = PLUGINS.find((p) => p.id === pluginId);
  const [pkg, setPkg] = useState<PackageView | undefined>(undefined);
  useEffect(() => {
    if (!plugin) return;
    let alive = true;
    const load = () =>
      listPackages()
        .then((r) => alive && setPkg((r.packages || []).find((p) => p.id === plugin.requires)))
        .catch(() => alive && setPkg(undefined));
    load();
    const stream = openToolboxStream((id) => {
      if (!id || id === plugin.requires) load();
    });
    return () => {
      alive = false;
      stream.close();
    };
  }, [plugin]);
  return pkg;
}

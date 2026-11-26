import type { PackageView } from "../api/toolbox";

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

/* Spec 77 REQ-WRA-007: the claude-swap "Claude accounts" plugin left; the
   Workbench popup reads the Router's Claude Code accounts instead. The Tools
   page hides the Plugins section while this list is empty. */
export const PLUGINS: PluginDef[] = [];

export function toolReady(pkg: PackageView | undefined): boolean {
  return Boolean(pkg && (pkg.status === "installed" || pkg.status === "too_old") && pkg.binary_path);
}

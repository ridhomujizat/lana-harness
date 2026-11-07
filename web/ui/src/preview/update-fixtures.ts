import type { UpdateRelease, UpdateStatus } from "../api/update";

/* Preview fixtures for the self-update card and Settings > Updates (spec 36).
   Pick a card face with ?update=<name>, for example
   preview.html?update=waiting#/home. Without the parameter no update is
   offered, so the other screenshots stay as they were. The vitest tests of the
   card reuse these statuses. */

const NOW = Date.now();
const MIN = 60_000;
const HOUR = 60 * MIN;

const NOTES = [
  "### Added",
  "- Self-update from the dashboard: one click downloads, checks, and installs the new version.",
  "- Settings > Updates with the release channel and the automatic check.",
  "",
  "### Fixed",
  "- The chat scrolls to the newest message after sending.",
  "- Pinch zoom on the workflow canvas zooms the canvas, not the page.",
].join("\n");

const UPGRADE_NOTES = [
  "### Upgrade notes",
  "- `[web] addr` moved to `[web] listen`. lana rewrites old configs on the first start.",
  "",
  NOTES,
].join("\n");

export const releaseFixture: UpdateRelease = {
  version: "0.1.0",
  tag: "v0.1.0",
  name: "lana 0.1.0",
  published_at: NOW - 3 * HOUR,
  notes: NOTES,
  url: "https://github.com/ridhomujizat/lana-harness/releases/tag/v0.1.0",
  size: 31_000_000,
  prerelease: false,
  upgrade_notes: false,
};

export const baseUpdateStatus: UpdateStatus = {
  current: "0.1.0-beta.1",
  channel: "stable",
  check: true,
  supported: true,
  can_apply: true,
  manual_reason: "",
  manual_command:
    "curl -fsSL https://raw.githubusercontent.com/ridhomujizat/lana-harness/main/install.sh | bash",
  checked_at: NOW - 12 * MIN,
  check_error: "",
  available: null,
  skipped: "",
  job: null,
  last_result: null,
};

function withJob(
  state: NonNullable<UpdateStatus["job"]>["state"],
  extra: Partial<NonNullable<UpdateStatus["job"]>> = {}
): UpdateStatus {
  return {
    ...baseUpdateStatus,
    available: releaseFixture,
    job: {
      state,
      version: releaseFixture.version,
      progress: null,
      running_turns: 0,
      deadline: null,
      error: "",
      ...extra,
    },
  };
}

export const updateFixtures: Record<string, UpdateStatus> = {
  none: baseUpdateStatus,
  offer: { ...baseUpdateStatus, available: releaseFixture },
  "offer-notes": {
    ...baseUpdateStatus,
    channel: "beta",
    available: {
      ...releaseFixture,
      version: "0.2.0-beta.1",
      tag: "v0.2.0-beta.1",
      name: "lana 0.2.0-beta.1",
      notes: UPGRADE_NOTES,
      prerelease: true,
      upgrade_notes: true,
    },
  },
  downloading: withJob("downloading", {
    progress: { done: 12_400_000, total: 31_000_000 },
  }),
  verifying: withJob("verifying"),
  probing: withJob("probing"),
  waiting: withJob("waiting", { running_turns: 2, deadline: NOW + 45_000 }),
  installing: withJob("installing"),
  restarting: withJob("restarting"),
  failed: withJob("failed", {
    error:
      "checking the new version: lana-0.1.0 update-probe exited 1:\nconfig: update.channel: must be \"stable\" or \"beta\"",
  }),
  manual: {
    ...baseUpdateStatus,
    can_apply: false,
    manual_reason: "lana runs from /nix/store, which is read-only",
    available: releaseFixture,
  },
  updated: {
    ...baseUpdateStatus,
    current: "0.1.0",
    last_result: {
      outcome: "updated",
      from: "0.1.0-beta.1",
      to: "0.1.0",
      reason: "",
      at: NOW - 2 * MIN,
    },
  },
  "rolled-back": {
    ...baseUpdateStatus,
    available: releaseFixture,
    last_result: {
      outcome: "rolled_back",
      from: "0.1.0-beta.1",
      to: "0.1.0",
      reason: "the new version exited during its trial boot: open lana.db: database disk image is malformed",
      at: NOW - 2 * MIN,
    },
  },
  "result-failed": {
    ...baseUpdateStatus,
    available: releaseFixture,
    last_result: {
      outcome: "failed",
      from: "0.1.0-beta.1",
      to: "0.1.0",
      reason: "verifying: SHA-256 of lana-darwin-arm64 does not match SHA256SUMS",
      at: NOW - 5 * MIN,
    },
  },
  "check-error": {
    ...baseUpdateStatus,
    check_error: "GET https://api.github.com/repos/ridhomujizat/lana-harness/releases: dial tcp: i/o timeout",
  },
  unsupported: {
    ...baseUpdateStatus,
    current: "dev",
    supported: false,
    can_apply: false,
    checked_at: null,
  },
};

function previewUpdateStatus(): UpdateStatus {
  const name = new URLSearchParams(window.location.search).get("update") || "none";
  return updateFixtures[name] || baseUpdateStatus;
}

/* /v1/update routes. Writes answer with the same status so a screenshot keeps
   its face; DELETE /v1/update/apply answers as the contract says. */
export function mockUpdate(
  method: string,
  parts: string[]
): { status: number; body: unknown } {
  const sub = parts[2] || "";
  const status = previewUpdateStatus();
  if (sub === "apply" && method === "DELETE") {
    return { status: 200, body: { cancelled: true } };
  }
  if (sub === "result" && method === "DELETE") {
    /* The daemon answers 204; a mocked Response cannot carry a body with
       204, so the preview answers 200 with null, which the client treats the
       same. */
    return { status: 200, body: null };
  }
  if (sub === "apply" && method === "POST") {
    return {
      status: 202,
      body: {
        ...status,
        job: {
          state: "downloading",
          version: status.available ? status.available.version : "",
          progress: { done: 0, total: 31_000_000 },
          running_turns: 0,
          deadline: null,
          error: "",
        },
      },
    };
  }
  return { status: 200, body: status };
}

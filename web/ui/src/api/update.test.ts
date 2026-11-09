import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setCSRF } from "./client";
import {
  applyUpdate,
  cancelUpdate,
  checkUpdate,
  dismissUpdateResult,
  getUpdate,
  isJobActive,
  isJobCancellable,
  saveUpdateSettings,
  skipUpdate,
} from "./update";
import type { UpdateJob } from "./update";
import { updateFixtures } from "../preview/update-fixtures";

/* API-UPD-003..009: each call hits the contract's method and path with the
   contract's body, and writes carry the CSRF header. */

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => (body === null ? "" : JSON.stringify(body)),
  } as Response;
}

describe("api/update", () => {
  const fetchMock = vi.fn();
  const status = updateFixtures.offer;

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    setCSRF("csrf-1");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    setCSRF("");
  });

  function call(index = 0): { method: string; path: string; body: unknown; headers: Record<string, string> } {
    const [path, init] = fetchMock.mock.calls[index] as [string, RequestInit];
    return {
      method: String(init.method),
      path,
      body: init.body ? JSON.parse(String(init.body)) : undefined,
      headers: init.headers as Record<string, string>,
    };
  }

  it("reads the status with GET /v1/update", async () => {
    fetchMock.mockResolvedValueOnce(response(200, status));
    const result = await getUpdate();
    expect(result.available?.version).toBe("0.1.0");
    expect(call()).toMatchObject({ method: "GET", path: "/v1/update", body: undefined });
    expect(call().headers["X-Lana-CSRF"]).toBeUndefined();
  });

  it("checks now with POST /v1/update/check", async () => {
    fetchMock.mockResolvedValueOnce(response(200, status));
    await checkUpdate();
    expect(call()).toMatchObject({ method: "POST", path: "/v1/update/check" });
    expect(call().headers["X-Lana-CSRF"]).toBe("csrf-1");
  });

  it("applies with confirm and force", async () => {
    fetchMock.mockResolvedValue(response(202, updateFixtures.downloading));
    await applyUpdate("0.1.0");
    await applyUpdate("0.1.0", true);
    expect(call(0)).toMatchObject({
      method: "POST",
      path: "/v1/update/apply",
      body: { version: "0.1.0", confirm: true, force: false },
    });
    expect(call(1).body).toEqual({ version: "0.1.0", confirm: true, force: true });
  });

  it("cancels with DELETE /v1/update/apply", async () => {
    fetchMock.mockResolvedValueOnce(response(200, { cancelled: true }));
    const result = await cancelUpdate();
    expect(result.cancelled).toBe(true);
    expect(call()).toMatchObject({ method: "DELETE", path: "/v1/update/apply" });
  });

  it("skips a version and clears the skip", async () => {
    fetchMock.mockResolvedValue(response(200, status));
    await skipUpdate("0.1.0");
    await skipUpdate("");
    expect(call(0)).toMatchObject({
      method: "POST",
      path: "/v1/update/skip",
      body: { version: "0.1.0" },
    });
    expect(call(1).body).toEqual({ version: "" });
  });

  it("saves settings with PATCH /v1/update/settings", async () => {
    fetchMock.mockResolvedValueOnce(response(200, { ...status, channel: "beta" }));
    const result = await saveUpdateSettings({ channel: "beta" });
    expect(result.channel).toBe("beta");
    expect(call()).toMatchObject({
      method: "PATCH",
      path: "/v1/update/settings",
      body: { channel: "beta" },
    });
  });

  it("dismisses the result with DELETE /v1/update/result and a 204", async () => {
    fetchMock.mockResolvedValueOnce(response(204, null));
    await expect(dismissUpdateResult()).resolves.toBeUndefined();
    expect(call()).toMatchObject({ method: "DELETE", path: "/v1/update/result" });
  });

  it("maps a 409 to an ApiError with the contract code", async () => {
    fetchMock.mockResolvedValueOnce(
      response(409, { error: { code: "update_unavailable", message: "cannot update" } })
    );
    await expect(applyUpdate("0.1.0")).rejects.toMatchObject({
      status: 409,
      code: "update_unavailable",
    });
  });

  it("knows which job states are active and cancellable", () => {
    const job = (state: UpdateJob["state"]): UpdateJob => ({
      state,
      version: "0.1.0",
      progress: null,
      running_turns: 0,
      deadline: null,
      error: "",
    });
    expect(isJobActive(null)).toBe(false);
    for (const state of ["downloading", "verifying", "probing", "waiting", "installing", "restarting"] as const) {
      expect(isJobActive(job(state))).toBe(true);
    }
    expect(isJobActive(job("failed"))).toBe(false);
    expect(isJobActive(job("cancelled"))).toBe(false);
    expect(isJobCancellable(job("waiting"))).toBe(true);
    expect(isJobCancellable(job("installing"))).toBe(false);
    expect(isJobCancellable(job("restarting"))).toBe(false);
  });
});

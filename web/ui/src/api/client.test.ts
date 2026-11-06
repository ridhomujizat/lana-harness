import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  errorMessage,
  request,
  setCSRF,
  setUnauthorizedHandler,
} from "./client";

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () =>
      typeof body === "string" ? body : body === null ? "" : JSON.stringify(body),
  } as Response;
}

describe("api client", () => {
  const fetchMock = vi.fn();

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    setCSRF("");
    setUnauthorizedHandler(null);
  });

  it("maps an error status and envelope to ApiError", async () => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockResolvedValueOnce(
      response(409, {
        error: {
          code: "stale_version",
          message: "config.toml changed",
          details: [{ pid: 1234 }],
        },
      })
    );

    let failure: unknown = null;
    try {
      await request("GET", "/v1/config");
    } catch (error) {
      failure = error;
    }

    expect(failure).toBeInstanceOf(ApiError);
    const apiError = failure as ApiError;
    expect(apiError.status).toBe(409);
    expect(apiError.code).toBe("stale_version");
    expect(apiError.message).toBe("config.toml changed");
    expect(apiError.details).toEqual([{ pid: 1234 }]);
    expect(apiError.pid).toBe(1234);
    expect(errorMessage(apiError)).toBe("config.toml changed");
  });

  it("maps a status without an envelope", async () => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockResolvedValueOnce(response(500, "plain failure"));

    let failure: unknown = null;
    try {
      await request("GET", "/v1/config");
    } catch (error) {
      failure = error;
    }

    expect(failure).toBeInstanceOf(ApiError);
    const apiError = failure as ApiError;
    expect(apiError.status).toBe(500);
    expect(apiError.code).toBe("http_500");
    expect(apiError.message).toBe("plain failure");
  });

  it("maps a network failure to status 0", async () => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockRejectedValueOnce(new Error("offline"));

    let failure: unknown = null;
    try {
      await request("GET", "/v1/config");
    } catch (error) {
      failure = error;
    }

    expect(failure).toBeInstanceOf(ApiError);
    const apiError = failure as ApiError;
    expect(apiError.status).toBe(0);
    expect(apiError.code).toBe("network_error");
  });

  it("calls the unauthorized handler for a 401", async () => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockResolvedValueOnce(
      response(401, {
        error: { code: "unauthenticated", message: "authentication required" },
      })
    );
    const handler = vi.fn();
    setUnauthorizedHandler(handler);

    await expect(request("GET", "/v1/auth/session")).rejects.toBeInstanceOf(
      ApiError
    );
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler.mock.calls[0]?.[0]).toBeInstanceOf(ApiError);
    expect(errorMessage(new ApiError(401, "unauthenticated", "no"))).toBe(
      "Please log in again."
    );
  });

  it("sends the CSRF header on mutating requests", async () => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockResolvedValueOnce(response(200, { ok: true }));
    setCSRF("csrf-token");

    await request("POST", "/v1/sessions", { agent: "coder" });

    const call = fetchMock.mock.calls[0]!;
    expect(call[0]).toBe("/v1/sessions");
    const init = call[1] as RequestInit;
    const headers = init.headers as Record<string, string>;
    expect(init.method).toBe("POST");
    expect(headers["X-Lana-CSRF"]).toBe("csrf-token");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(init.body).toBe(JSON.stringify({ agent: "coder" }));
  });

  it("does not send the CSRF header on GET", async () => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockResolvedValueOnce(response(200, {}));
    setCSRF("csrf-token");

    await request("GET", "/v1/sessions");

    const init = fetchMock.mock.calls[0]![1] as RequestInit;
    const headers = init.headers as Record<string, string>;
    expect(headers["X-Lana-CSRF"]).toBeUndefined();
  });
});

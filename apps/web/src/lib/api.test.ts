import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { __test } from "./api";

const { tokenExpiresSoon, request } = __test;

// Build a structurally valid JWT with an arbitrary payload (signature is never
// verified client-side; only the exp claim is read).
function makeToken(payload: Record<string, unknown>): string {
  const b64url = (s: string) =>
    window
      .btoa(s)
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
  return `e30.${b64url(JSON.stringify(payload))}.sig`;
}

const nowSec = () => Math.floor(Date.now() / 1000);

describe("tokenExpiresSoon", () => {
  it("is false for a token far from expiry", () => {
    expect(tokenExpiresSoon(makeToken({ exp: nowSec() + 3600 }))).toBe(false);
  });

  it("is true inside the 60s leeway window", () => {
    expect(tokenExpiresSoon(makeToken({ exp: nowSec() + 30 }))).toBe(true);
  });

  it("is true for an already-expired token", () => {
    expect(tokenExpiresSoon(makeToken({ exp: nowSec() - 10 }))).toBe(true);
  });

  it("is true when exp is missing or the payload is malformed", () => {
    expect(tokenExpiresSoon(makeToken({ sub: "x" }))).toBe(true);
    expect(tokenExpiresSoon("not-a-jwt")).toBe(true);
    expect(tokenExpiresSoon("")).toBe(true);
    expect(tokenExpiresSoon("a.@@@invalid-base64@@@.c")).toBe(true);
  });

  it("decodes base64url payloads (unpadded, -/_ alphabet)", () => {
    // Payload chosen so standard base64 contains '+' and '/' which the
    // base64url form replaces; the decoder must map them back.
    const exp = nowSec() + 3600;
    const token = makeToken({ exp, note: "??>>??>>??" });
    expect(tokenExpiresSoon(token)).toBe(false);
  });
});

describe("request auth retry", () => {
  const okJson = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });

  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("access_token", "stale-token");
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("refreshes once for concurrent 401s (single-flight)", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/auth/refresh")) {
        return okJson({ access_token: makeToken({ exp: nowSec() + 900 }) });
      }
      const auth = new Headers(init?.headers).get("Authorization") ?? "";
      if (auth === "Bearer stale-token") {
        return okJson({ error: "unauthorized" }, 401);
      }
      return okJson({ ok: true });
    });

    const results = await Promise.all([
      request<{ ok: boolean }>("GET", "/servers"),
      request<{ ok: boolean }>("GET", "/overview"),
      request<{ ok: boolean }>("GET", "/auth/me"),
    ]);

    expect(results).toEqual([{ ok: true }, { ok: true }, { ok: true }]);
    const refreshCalls = fetchMock.mock.calls.filter(([u]) =>
      String(u).endsWith("/auth/refresh"),
    );
    expect(refreshCalls).toHaveLength(1);
    // The retried requests carried the refreshed token.
    expect(localStorage.getItem("access_token")).not.toBe("stale-token");
  });

  it("performs a fresh refresh on a later 401 round (promise resets)", async () => {
    let round = 0;
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/auth/refresh")) {
        round += 1;
        return okJson({ access_token: makeToken({ exp: nowSec() + 900, round }) });
      }
      const auth = new Headers(init?.headers).get("Authorization") ?? "";
      const current = `Bearer ${localStorage.getItem("access_token")}`;
      // 401 until the caller presents the freshest token.
      return auth === current && round > 0
        ? okJson({ ok: true })
        : okJson({ error: "unauthorized" }, 401);
    });

    await request("GET", "/servers");
    // Simulate the next access token going stale again.
    localStorage.setItem("access_token", "stale-token");
    round = 0;
    await request("GET", "/servers");

    const refreshCalls = fetchMock.mock.calls.filter(([u]) =>
      String(u).endsWith("/auth/refresh"),
    );
    expect(refreshCalls).toHaveLength(2);
  });

  it("does not loop when a 401 persists after a successful refresh", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/auth/refresh")) {
        return okJson({ access_token: makeToken({ exp: nowSec() + 900 }) });
      }
      return okJson({ error: "unauthorized" }, 401);
    });

    await expect(request("GET", "/servers")).rejects.toThrow();
    // One original call + one refresh + one retry: no further attempts.
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("clears the stored session when refresh fails", async () => {
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/auth/refresh")) {
        return okJson({ error: "invalid or expired refresh token" }, 401);
      }
      return okJson({ error: "unauthorized" }, 401);
    });

    await expect(request("GET", "/servers")).rejects.toThrow("Unauthorized");
    expect(localStorage.getItem("access_token")).toBeNull();
  });
});

/**
 * Session refresh: an expired access token rotates silently off the stored
 * refresh token, concurrent 401s share one rotation, and a dead refresh
 * surfaces instead of looping.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { refreshAccessToken, usage } from "./api";
import { getRefreshToken, getToken } from "./auth";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("refreshAccessToken", () => {
  it("rotates the pair and stores both", async () => {
    localStorage.setItem("refresh_token", "old-refresh");
    const fetchMock = vi.fn(async () => jsonResponse({ token: "new-access", refresh_token: "new-refresh" }));
    vi.stubGlobal("fetch", fetchMock);

    const token = await refreshAccessToken();
    expect(token).toBe("new-access");
    expect(getToken()).toBe("new-access");
    expect(getRefreshToken()).toBe("new-refresh");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain("/v1/auth/refresh");
    expect(JSON.parse(init.body as string)).toEqual({ refresh_token: "old-refresh" });
  });

  it("shares one rotation across concurrent callers", async () => {
    localStorage.setItem("refresh_token", "shared");
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        calls++;
        await new Promise((r) => setTimeout(r, 10));
        return jsonResponse({ token: "t", refresh_token: "r2" });
      }),
    );

    const [a, b] = await Promise.all([refreshAccessToken(), refreshAccessToken()]);
    expect(a).toBe("t");
    expect(b).toBe("t");
    expect(calls).toBe(1);
  });

  it("rejects when no refresh token is stored", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    await expect(refreshAccessToken()).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("request retry", () => {
  it("retries once after a silent rotation", async () => {
    localStorage.setItem("token", "expired-access");
    localStorage.setItem("refresh_token", "live-refresh");
    // First usage call 401s on the expired token, retry succeeds.
    let usageCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (String(url).endsWith("/v1/auth/refresh")) {
          return jsonResponse({ token: "fresh-access", refresh_token: "fresh-refresh" });
        }
        if (String(url).endsWith("/v1/usage")) {
          usageCalls++;
          if (usageCalls === 1) {
            const auth = (init?.headers as Record<string, string>)?.Authorization;
            expect(auth).toBe("Bearer expired-access");
            return jsonResponse({ error: "unauthorized" }, 401);
          }
          const auth = (init?.headers as Record<string, string>)?.Authorization;
          expect(auth).toBe("Bearer fresh-access");
          return jsonResponse({ prompts_used: 0, prompts_limit: 10, device_minutes: 0, device_limit: 60, tier: "free" });
        }
        return jsonResponse({}, 404);
      }),
    );
    const u = await usage.get();
    expect(u.prompts_limit).toBe(10);
    expect(getToken()).toBe("fresh-access");
    expect(getRefreshToken()).toBe("fresh-refresh");
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiClient, ApiRequestTimeoutError } from "./apiClient";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("ApiClient", () => {
  it("aborts a request that exceeds its bounded timeout", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("fetch", vi.fn((_input: RequestInfo | URL, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      })));
    const request = new ApiClient("https://api.example.test").request(
      "/v1/meeting-quota",
      {},
      { timeoutMs: 100 },
    );
    const expectation = expect(request).rejects.toBeInstanceOf(ApiRequestTimeoutError);

    await vi.advanceTimersByTimeAsync(100);

    await expectation;
  });

  it("reauthenticates once and retries an unauthorized request", async () => {
    const authorizationHeaders: Array<string | null> = [];
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      authorizationHeaders.push(new Headers(init?.headers).get("Authorization"));
      if (authorizationHeaders.length === 1) {
        return Promise.resolve(new Response(JSON.stringify({ message: "expired" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }));
      }
      return Promise.resolve(new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");
    client.setAccessToken("expired-token");
    const refresh = vi.fn(async () => client.setAccessToken("fresh-token"));
    client.setUnauthorizedHandler(refresh);

    await expect(client.request("/v1/meetings/meeting-id")).resolves.toEqual({ ok: true });
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(authorizationHeaders).toEqual(["Bearer expired-token", "Bearer fresh-token"]);
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { createFetch } from "../src/index.ts";
import type { FetchOptions } from "../src/types.ts";

const ORIGIN_A = "https://a.example";
const ORIGIN_B = "https://b.example";

function jsonResponse(body?: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(body ?? { ok: true }), {
    status: init?.status ?? 200,
    headers: { "content-type": "application/json", ...init?.headers },
  });
}

function statusResponse(status: number, body = ""): Response {
  return new Response(body, { status });
}

function createClient(
  fetchImpl: typeof fetch,
  circuitBreaker: FetchOptions["circuitBreaker"] = true
) {
  return createFetch({
    fetch: fetchImpl,
    defaults: {
      retry: false,
      circuitBreaker,
    },
  });
}

describe("circuit breaker", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("does not track or block when circuitBreaker is omitted or falsey", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const disabled = createFetch({
      fetch: fetchImpl,
      defaults: { retry: false },
    });
    const off = createFetch({
      fetch: fetchImpl,
      defaults: { retry: false, circuitBreaker: false },
    });

    for (let i = 0; i < 8; i++) {
      await expect(disabled("https://a.example/x")).rejects.toThrow();
      await expect(off("https://a.example/y")).rejects.toThrow();
    }
    expect(fetchImpl).toHaveBeenCalledTimes(16);
  });

  it("opens after the default threshold of 5 consecutive failures", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createClient(fetchImpl, true);

    for (let i = 0; i < 5; i++) {
      await expect($fetch(`${ORIGIN_A}/fail`)).rejects.toThrow();
    }
    expect(fetchImpl).toHaveBeenCalledTimes(5);

    await expect($fetch(`${ORIGIN_A}/fail`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(5);
  });

  it("uses custom threshold, cooldown, halfOpenMaxRequests, and failureStatusCodes", async () => {
    vi.useFakeTimers();
    const fetchImpl = vi.fn(async () => statusResponse(418));
    const $fetch = createClient(fetchImpl, {
      threshold: 2,
      cooldown: 1000,
      halfOpenMaxRequests: 1,
      failureStatusCodes: [418],
    });

    await expect($fetch(`${ORIGIN_A}/teapot`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/teapot`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/teapot`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(1000);
    fetchImpl.mockResolvedValueOnce(jsonResponse());
    await expect($fetch(`${ORIGIN_A}/teapot`)).resolves.toEqual({ ok: true });
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("keys state by origin, not path, and isolates different origins", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith(ORIGIN_B)) {
        return jsonResponse({ origin: "b" });
      }
      return statusResponse(503);
    });
    const $fetch = createClient(fetchImpl, { threshold: 2, cooldown: 30_000 });

    await expect($fetch(`${ORIGIN_A}/one`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/two`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/three`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect($fetch(`${ORIGIN_B}/ok`)).resolves.toEqual({ origin: "b" });
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("resolves origins from string, URL, and Request inputs", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(500));
    const $fetch = createClient(fetchImpl, { threshold: 3, cooldown: 30_000 });

    await expect($fetch(`${ORIGIN_A}/s`)).rejects.toThrow();
    await expect($fetch(new URL(`${ORIGIN_A}/u`))).rejects.toThrow();
    await expect($fetch(new Request(`${ORIGIN_A}/r`))).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/blocked`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("keys relative string requests by the origin after baseURL resolution", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createClient(fetchImpl, { threshold: 2, cooldown: 30_000 });

    await expect(
      $fetch("/one", { baseURL: `${ORIGIN_A}/api` })
    ).rejects.toThrow();
    await expect(
      $fetch("/two", { baseURL: `${ORIGIN_A}/api` })
    ).rejects.toThrow();
    await expect(
      $fetch("/three", { baseURL: `${ORIGIN_A}/api` })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it("keys by the effective request after onRequest mutation and URL rewriting", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createClient(fetchImpl, { threshold: 2, cooldown: 30_000 });

    await expect(
      $fetch("/first", {
        baseURL: ORIGIN_B,
        onRequest(ctx) {
          ctx.options.baseURL = ORIGIN_A;
        },
      })
    ).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/second`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/third`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect($fetch(`${ORIGIN_B}/still-ok`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("shares circuit state between a parent client and .create() children", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const parent = createClient(fetchImpl, { threshold: 2, cooldown: 30_000 });
    const child = parent.create({ retry: false });

    await expect(parent(`${ORIGIN_A}/p`)).rejects.toThrow();
    await expect(child(`${ORIGIN_A}/c`)).rejects.toThrow();
    await expect(parent(`${ORIGIN_A}/blocked`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect(child(`${ORIGIN_A}/blocked`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it("works for $fetch, createFetch({ fetch }), and derived clients", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(500));
    const created = createFetch({
      fetch: fetchImpl,
      defaults: {
        retry: false,
        circuitBreaker: { threshold: 1, cooldown: 30_000 },
      },
    });
    const derived = created.create({});

    await expect(created(`${ORIGIN_A}/x`)).rejects.toThrow();
    await expect(created.raw(`${ORIGIN_A}/y`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect(derived(`${ORIGIN_A}/z`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it("moves open -> half-open after cooldown using Date.now()", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createClient(fetchImpl, { threshold: 1, cooldown: 5000 });

    await expect($fetch(`${ORIGIN_A}/down`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/down`)).rejects.toThrow(
      /Circuit breaker is open/
    );

    vi.setSystemTime(4999);
    await expect($fetch(`${ORIGIN_A}/down`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    vi.setSystemTime(5000);
    await expect($fetch(`${ORIGIN_A}/down`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it("closes on a successful half-open probe and reopens on a failed probe", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createClient(fetchImpl, { threshold: 1, cooldown: 1000 });

    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();
    vi.setSystemTime(1000);
    fetchImpl.mockResolvedValueOnce(jsonResponse());
    await expect($fetch(`${ORIGIN_A}/x`)).resolves.toEqual({ ok: true });

    fetchImpl.mockResolvedValueOnce(statusResponse(503));
    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();
    const openedAt = Date.now();

    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    vi.setSystemTime(openedAt + 999);
    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    vi.setSystemTime(openedAt + 1000);
    fetchImpl.mockResolvedValueOnce(statusResponse(503));
    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(4);
  });

  it("allows at most halfOpenMaxRequests concurrent probes and fail-fasts extras", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    let release!: (value: Response) => void;
    const fetchImpl = vi.fn(
      async () =>
        new Promise<Response>((resolve) => {
          release = resolve;
        })
    );
    const $fetch = createClient(fetchImpl, {
      threshold: 1,
      cooldown: 1000,
      halfOpenMaxRequests: 1,
    });

    fetchImpl.mockResolvedValueOnce(statusResponse(503));
    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();

    vi.setSystemTime(1000);
    const probe = $fetch(`${ORIGIN_A}/probe`);
    await vi.waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(2));

    await expect($fetch(`${ORIGIN_A}/extra`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(2);

    release(jsonResponse());
    await expect(probe).resolves.toEqual({ ok: true });
  });

  it("keeps a half-open probe slot for the full logical request including retries", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createFetch({
      fetch: fetchImpl,
      defaults: {
        retry: false,
        circuitBreaker: { threshold: 1, cooldown: 1000 },
      },
    });

    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    vi.setSystemTime(1000);
    const probe = $fetch(`${ORIGIN_A}/probe`, { retry: 1, retryDelay: 50 });
    const probeFinished = expect(probe).rejects.toThrow();
    await Promise.resolve();
    await Promise.resolve();
    expect(fetchImpl).toHaveBeenCalledTimes(2);

    await expect($fetch(`${ORIGIN_A}/extra`)).rejects.toThrow(
      /Circuit breaker is open/
    );

    await vi.advanceTimersByTimeAsync(50);
    await probeFinished;
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("counts network, body-read, parse, and hook exceptions as failures", async () => {
    const cases: Array<{
      name: string;
      fetchImpl: typeof fetch;
      options?: Record<string, unknown>;
    }> = [
      {
        name: "network",
        fetchImpl: async () => {
          throw new TypeError("network down");
        },
      },
      {
        name: "body-read",
        fetchImpl: async () => {
          const response = jsonResponse();
          await response.text();
          return response;
        },
      },
      {
        name: "parseResponse",
        fetchImpl: async () => jsonResponse(),
        options: {
          parseResponse() {
            throw new Error("bad json");
          },
        },
      },
      {
        name: "onRequestError",
        fetchImpl: async () => {
          throw new TypeError("network down");
        },
        options: {
          onRequestError() {
            throw new Error("hook boom");
          },
        },
      },
      {
        name: "onResponse",
        fetchImpl: async () => jsonResponse(),
        options: {
          onResponse() {
            throw new Error("onResponse boom");
          },
        },
      },
      {
        name: "onResponseError",
        fetchImpl: async () => statusResponse(404),
        options: {
          onResponseError() {
            throw new Error("onResponseError boom");
          },
        },
      },
    ];

    for (const testCase of cases) {
      const fetchImpl = vi.fn(testCase.fetchImpl);
      const $fetch = createClient(fetchImpl, {
        threshold: 1,
        cooldown: 30_000,
      });
      await expect(
        $fetch(`${ORIGIN_A}/${testCase.name}`, testCase.options as any)
      ).rejects.toThrow();
      await expect($fetch(`${ORIGIN_A}/${testCase.name}`)).rejects.toThrow(
        /Circuit breaker is open/
      );
      expect(fetchImpl, testCase.name).toHaveBeenCalledTimes(1);
    }
  });

  it("does not increment or reset on rejected statuses outside failureStatusCodes", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("not-found")) {
        return statusResponse(404);
      }
      if (url.includes("ok")) {
        return jsonResponse();
      }
      return statusResponse(503);
    });
    const $fetch = createClient(fetchImpl, { threshold: 2, cooldown: 30_000 });

    await expect($fetch(`${ORIGIN_A}/fail-1`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/not-found`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/fail-2`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/blocked`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("does not close half-open state on a rejected non-listed status", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("missing")) {
        return statusResponse(404);
      }
      return statusResponse(503);
    });
    const $fetch = createClient(fetchImpl, { threshold: 1, cooldown: 1000 });

    await expect($fetch(`${ORIGIN_A}/down`)).rejects.toThrow();
    vi.setSystemTime(1000);
    await expect($fetch(`${ORIGIN_A}/missing`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/still-half-open`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it("increments listed status failures when ignoreResponseError is true", async () => {
    const fetchImpl = vi.fn(async () =>
      jsonResponse({ error: "unavailable" }, { status: 503 })
    );
    const $fetch = createClient(fetchImpl, { threshold: 2, cooldown: 30_000 });

    await expect(
      $fetch(`${ORIGIN_A}/a`, { ignoreResponseError: true })
    ).resolves.toEqual({ error: "unavailable" });
    await expect(
      $fetch(`${ORIGIN_A}/b`, { ignoreResponseError: true })
    ).resolves.toEqual({ error: "unavailable" });
    await expect($fetch(`${ORIGIN_A}/c`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it("records a single failure for a logical request that retries internally", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createFetch({
      fetch: fetchImpl,
      defaults: {
        retry: 2,
        retryDelay: 0,
        circuitBreaker: { threshold: 2, cooldown: 30_000 },
      },
    });

    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(3);
    await expect($fetch(`${ORIGIN_A}/y`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(6);
    await expect($fetch(`${ORIGIN_A}/z`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).toHaveBeenCalledTimes(6);
  });

  it("does not retry parse or hook failures via status retry logic", async () => {
    const fetchImpl = vi.fn(async () => jsonResponse());
    const $fetch = createFetch({
      fetch: fetchImpl,
      defaults: {
        retry: 2,
        retryDelay: 0,
        circuitBreaker: { threshold: 1, cooldown: 30_000 },
      },
    });

    await expect(
      $fetch(`${ORIGIN_A}/parse`, {
        parseResponse() {
          throw new Error("parse failed");
        },
      })
    ).rejects.toThrow("parse failed");
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    await expect($fetch(`${ORIGIN_A}/next`)).rejects.toThrow(
      /Circuit breaker is open/
    );
  });

  it("resets consecutive failures after a successful logical request", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("ok")) {
        return jsonResponse();
      }
      return statusResponse(503);
    });
    const $fetch = createClient(fetchImpl, { threshold: 3, cooldown: 30_000 });

    await expect($fetch(`${ORIGIN_A}/fail`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/fail`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/ok`)).resolves.toEqual({ ok: true });
    await expect($fetch(`${ORIGIN_A}/fail`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/fail`)).rejects.toThrow();
    await expect($fetch(`${ORIGIN_A}/still-closed`)).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(6);
  });

  it("rejects immediately without calling fetch when the circuit is open", async () => {
    const fetchImpl = vi.fn(async () => statusResponse(503));
    const $fetch = createClient(fetchImpl, { threshold: 1, cooldown: 30_000 });
    await expect($fetch(`${ORIGIN_A}/x`)).rejects.toThrow();
    fetchImpl.mockClear();
    await expect($fetch(`${ORIGIN_A}/y`)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchImpl).not.toHaveBeenCalled();
  });
});

import { describe, it, expect, vi, afterEach } from "vitest";
import { $fetch, createFetch } from "../src/index.ts";

const ORIGIN = "https://example.test";

function jsonResponse(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("circuit breaker", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("does not track failures when circuitBreaker is omitted or false", async () => {
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 503));
    const client = createFetch({ fetch });

    for (let i = 0; i < 6; i++) {
      await expect(client(`${ORIGIN}/a`, { retry: 0 })).rejects.toThrow();
    }
    await expect(
      client(`${ORIGIN}/a`, { retry: 0, circuitBreaker: false })
    ).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(7);
  });

  it("opens after the default threshold and cools down after 30s", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 503));
    const client = createFetch({ fetch });

    for (let i = 0; i < 5; i++) {
      await expect(
        client(`${ORIGIN}/down`, { circuitBreaker: true, retry: 0 })
      ).rejects.toThrow();
    }
    expect(fetch).toHaveBeenCalledTimes(5);

    await expect(
      client(`${ORIGIN}/down`, { circuitBreaker: true, retry: 0 })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetch).toHaveBeenCalledTimes(5);

    vi.setSystemTime(Date.now() + 29_999);
    await expect(
      client(`${ORIGIN}/down`, { circuitBreaker: true, retry: 0 })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetch).toHaveBeenCalledTimes(5);

    vi.setSystemTime(Date.now() + 1);
    fetch.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(
      client(`${ORIGIN}/down`, { circuitBreaker: true, retry: 0 })
    ).resolves.toEqual({ ok: true });
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("keys state by origin across paths, input types, baseURL, and onRequest", async () => {
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 500));
    const client = createFetch({ fetch });
    const breaker = {
      circuitBreaker: { threshold: 2, cooldown: 60_000 },
      retry: 0,
    };

    await expect(client(`${ORIGIN}/a`, breaker)).rejects.toThrow();
    await expect(client(new URL(`${ORIGIN}/b`), breaker)).rejects.toThrow();
    await expect(
      client(`${ORIGIN}/c`, { ...breaker, circuitBreaker: { threshold: 2 } })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetch).toHaveBeenCalledTimes(2);

    await expect(client(new Request(`${ORIGIN}/d`), breaker)).rejects.toThrow(
      /Circuit breaker is open/
    );

    const other = vi.fn(async () => jsonResponse({ ok: true }));
    const otherClient = createFetch({ fetch: other });
    await expect(
      otherClient("/item", {
        baseURL: `${ORIGIN}/api`,
        retry: 0,
        circuitBreaker: { threshold: 1 },
        onRequest(ctx) {
          ctx.request = "https://rewritten.test/z";
        },
      })
    ).resolves.toEqual({ ok: true });

    other.mockResolvedValueOnce(jsonResponse({ ok: false }, 500));
    await expect(
      otherClient("https://rewritten.test/other", {
        retry: 0,
        circuitBreaker: { threshold: 1 },
      })
    ).rejects.toThrow();
    await expect(
      otherClient("/ignored", {
        baseURL: "https://rewritten.test/api",
        retry: 0,
        circuitBreaker: { threshold: 1 },
      })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(other).toHaveBeenCalledTimes(2);
  });

  it("shares circuit state between .create() clients and $fetch", async () => {
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 502));
    const parent = createFetch({ fetch });
    const child = parent.create({ retry: 0 });
    const grandchild = child.create({});
    const breaker = { circuitBreaker: { threshold: 2, cooldown: 1000 } };

    await expect(
      parent(`${ORIGIN}/x`, { ...breaker, retry: 0 })
    ).rejects.toThrow();
    await expect(child(`${ORIGIN}/y`, breaker)).rejects.toThrow();
    await expect(grandchild(`${ORIGIN}/z`, breaker)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetch).toHaveBeenCalledTimes(2);

    const globalFetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(jsonResponse({ ok: false }, 500));
    for (let i = 0; i < 5; i++) {
      await expect(
        $fetch("https://shared.test/a", { circuitBreaker: true, retry: 0 })
      ).rejects.toThrow();
    }
    const derived = $fetch.create({});
    await expect(
      derived.raw("https://shared.test/b", {
        circuitBreaker: true,
        retry: 0,
      })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(globalFetch).toHaveBeenCalledTimes(5);
  });

  it("counts one failure per logical request when retries are exhausted", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(0);
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 500));
    const client = createFetch({ fetch });
    const options = {
      retry: 2,
      circuitBreaker: { threshold: 2, cooldown: 5000 },
    };

    await expect(client(`${ORIGIN}/r`, options)).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(3);
    await expect(
      client(`${ORIGIN}/r`, { ...options, retry: 0 })
    ).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(4);
    await expect(client(`${ORIGIN}/r`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it("holds a half-open slot for the whole logical request, including retries", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(0);
    const retryGate = deferred<Response>();
    let calls = 0;
    const fetch = vi.fn(() => {
      calls++;
      if (calls === 1) {
        return Promise.resolve(jsonResponse({ ok: false }, 500));
      }
      if (calls === 2) {
        return Promise.resolve(jsonResponse({ ok: false }, 500));
      }
      if (calls === 3) {
        return retryGate.promise;
      }
      return Promise.resolve(jsonResponse({ ok: true }));
    });
    const client = createFetch({ fetch });
    const options = {
      retry: 1,
      circuitBreaker: {
        threshold: 1,
        cooldown: 1000,
        halfOpenMaxRequests: 1,
      },
    };

    await expect(client(`${ORIGIN}/h`, options)).rejects.toThrow();
    vi.setSystemTime(1000);

    const probe = client(`${ORIGIN}/h`, options);
    await vi.waitUntil(() => calls === 3);
    await expect(client(`${ORIGIN}/h`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(calls).toBe(3);

    retryGate.resolve(jsonResponse({ ok: true }));
    await expect(probe).resolves.toEqual({ ok: true });

    await expect(
      client(`${ORIGIN}/h`, { ...options, retry: 0 })
    ).resolves.toEqual({
      ok: true,
    });
  });

  it("limits concurrent half-open probes and reopens on a failed probe", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(0);
    const gates = [deferred<Response>(), deferred<Response>()];
    let calls = 0;
    const fetch = vi.fn(() => {
      if (calls === 0) {
        calls++;
        return Promise.resolve(jsonResponse({ ok: false }, 500));
      }
      const gate = gates[calls - 1];
      calls++;
      return gate.promise;
    });
    const client = createFetch({ fetch });
    const options = {
      retry: 0,
      circuitBreaker: {
        threshold: 1,
        cooldown: 1000,
        halfOpenMaxRequests: 2,
      },
    };

    await expect(client(`${ORIGIN}/p`, options)).rejects.toThrow();
    vi.setSystemTime(1000);

    const first = client(`${ORIGIN}/p`, options);
    const second = client(`${ORIGIN}/p`, options);
    await vi.waitUntil(() => calls === 3);
    await expect(client(`${ORIGIN}/p`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(calls).toBe(3);

    gates[0].resolve(jsonResponse({ ok: false }, 503));
    await expect(first).rejects.toThrow();
    vi.setSystemTime(1999);
    await expect(client(`${ORIGIN}/p`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(calls).toBe(3);

    gates[1].reject(new Error("probe failed"));
    await expect(second).rejects.toThrow(/probe failed/);
    vi.setSystemTime(1999 + 1000);
    fetch.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(client(`${ORIGIN}/p`, options)).resolves.toEqual({ ok: true });
  });

  it("does not let non-listed statuses reset or close the circuit", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(0);
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 500));
    const client = createFetch({ fetch });
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 5, cooldown: 1000 },
    };

    for (let i = 0; i < 4; i++) {
      await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow();
    }
    fetch.mockResolvedValueOnce(jsonResponse({ missing: true }, 404));
    await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(5);

    await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );

    vi.setSystemTime(1000);
    fetch.mockResolvedValueOnce(jsonResponse({ missing: true }, 404));
    await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow();
    fetch.mockResolvedValueOnce(jsonResponse({ ok: false }, 500));
    await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/n`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
  });

  it("counts listed statuses when ignoreResponseError is set, and counts hook and parse failures", async () => {
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 429));
    const client = createFetch({ fetch });
    const breaker = { threshold: 1, cooldown: 60_000 };

    await expect(
      client(`${ORIGIN}/i`, {
        retry: 0,
        ignoreResponseError: true,
        circuitBreaker: breaker,
      })
    ).resolves.toEqual({ ok: false });
    await expect(
      client(`${ORIGIN}/i`, {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetch).toHaveBeenCalledTimes(1);

    const isolated = createFetch({
      fetch: vi.fn(async () => jsonResponse({ ok: false }, 418)),
    });
    await expect(
      isolated("https://codes.test/a", {
        retry: 0,
        circuitBreaker: { ...breaker, failureStatusCodes: [418] },
      })
    ).rejects.toThrow();
    await expect(
      isolated("https://codes.test/a", {
        retry: 0,
        circuitBreaker: { ...breaker, failureStatusCodes: [418] },
      })
    ).rejects.toThrow(/Circuit breaker is open/);

    const parseFetch = vi.fn(async () => jsonResponse({ ok: true }));
    const parseClient = createFetch({ fetch: parseFetch });
    await expect(
      parseClient("https://parse.test/a", {
        retry: 2,
        circuitBreaker: breaker,
        parseResponse() {
          throw new Error("bad parse");
        },
      })
    ).rejects.toThrow(/bad parse/);
    expect(parseFetch).toHaveBeenCalledTimes(1);
    await expect(
      parseClient("https://parse.test/a", {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/Circuit breaker is open/);

    const body = jsonResponse({ ok: true });
    vi.spyOn(body, "text").mockRejectedValueOnce(new Error("reused body"));
    const bodyFetch = vi.fn(async () => body);
    const bodyClient = createFetch({ fetch: bodyFetch });
    await expect(
      bodyClient("https://body.test/a", {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/reused body/);
    await expect(
      bodyClient("https://body.test/a", {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(bodyFetch).toHaveBeenCalledTimes(1);

    const hookFetch = vi.fn(async () => jsonResponse({ ok: true }));
    const hookClient = createFetch({ fetch: hookFetch });
    await expect(
      hookClient("https://hook.test/a", {
        retry: 0,
        circuitBreaker: breaker,
        onResponse() {
          throw new Error("response hook");
        },
      })
    ).rejects.toThrow(/response hook/);
    await expect(
      hookClient("https://hook.test/a", {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/Circuit breaker is open/);

    const requestErrorFetch = vi.fn(async () => {
      throw new Error("offline");
    });
    const requestErrorClient = createFetch({ fetch: requestErrorFetch });
    await expect(
      requestErrorClient("https://req.test/a", {
        retry: 0,
        circuitBreaker: breaker,
        onRequestError() {
          throw new Error("request hook");
        },
      })
    ).rejects.toThrow(/request hook/);
    await expect(
      requestErrorClient("https://req.test/a", {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(requestErrorFetch).toHaveBeenCalledTimes(1);

    const responseErrorFetch = vi.fn(async () =>
      jsonResponse({ missing: true }, 404)
    );
    const responseErrorClient = createFetch({ fetch: responseErrorFetch });
    await expect(
      responseErrorClient("https://res.test/a", {
        retry: 0,
        circuitBreaker: breaker,
        onResponseError() {
          throw new Error("response error hook");
        },
      })
    ).rejects.toThrow(/response error hook/);
    await expect(
      responseErrorClient("https://res.test/a", {
        retry: 0,
        circuitBreaker: breaker,
      })
    ).rejects.toThrow(/Circuit breaker is open/);
  });

  it("resets the failure streak after a successful logical request", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ ok: false }, 500))
      .mockResolvedValueOnce(jsonResponse({ ok: false }, 500))
      .mockResolvedValueOnce(jsonResponse({ ok: true }))
      .mockResolvedValue(jsonResponse({ ok: false }, 500));
    const client = createFetch({ fetch });
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 3, cooldown: 1000 },
    };

    await expect(client(`${ORIGIN}/s`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/s`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/s`, options)).resolves.toEqual({ ok: true });
    await expect(client(`${ORIGIN}/s`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/s`, options)).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(5);
    await expect(client(`${ORIGIN}/s`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/s`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
  });

  it("still runs pre-fetch hooks when the circuit is open", async () => {
    const fetch = vi.fn(async () => jsonResponse({ ok: false }, 500));
    const client = createFetch({ fetch });
    const onRequest = vi.fn();
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 60_000 },
      onRequest,
    };

    await expect(client(`${ORIGIN}/pre`, options)).rejects.toThrow();
    await expect(client(`${ORIGIN}/pre`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(onRequest).toHaveBeenCalledTimes(2);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

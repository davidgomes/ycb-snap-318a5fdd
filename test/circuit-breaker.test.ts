import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { $fetch, createFetch } from "../src/index.ts";
import type { FetchOptions } from "../src/index.ts";

const ORIGIN = "https://api.test";

function respond(status: number) {
  return new Response(JSON.stringify({ status }), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function mockFetch(handler: (url: string) => Response | Promise<Response>) {
  return vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) =>
    handler((input as Request).url || String(input))
  );
}

const reject = (promise: Promise<unknown>) =>
  promise.then(
    () => undefined,
    (error: Error) => error
  );

describe("circuit breaker", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date", "setTimeout", "clearTimeout"] });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("is disabled when omitted or falsey", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    for (let i = 0; i < 10; i++) {
      await reject($f(ORIGIN, { retry: 0 }));
    }
    await reject($f(ORIGIN, { retry: 0, circuitBreaker: false }));
    expect(fetch).toHaveBeenCalledTimes(11);
  });

  it("opens after `threshold` consecutive failures and fails fast", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 3, cooldown: 1000 },
    };

    for (let i = 0; i < 3; i++) {
      const error = await reject($f(`${ORIGIN}/a`, options));
      expect(error?.message).toContain("503");
    }
    expect(fetch).toHaveBeenCalledTimes(3);

    const error = await reject($f(`${ORIGIN}/b`, options));
    expect(error?.name).toBe("FetchError");
    expect(error?.message).toContain("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("uses defaults for `circuitBreaker: true`", async () => {
    const fetch = mockFetch(() => respond(429));
    const $f = createFetch({ fetch });
    const options: FetchOptions = { retry: 0, circuitBreaker: true };

    for (let i = 0; i < 5; i++) {
      await reject($f(ORIGIN, options));
    }
    expect(fetch).toHaveBeenCalledTimes(5);
    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );

    vi.advanceTimersByTime(29_999);
    await reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(5);

    vi.advanceTimersByTime(1);
    fetch.mockImplementation(async () => respond(200));
    expect(await $f(ORIGIN, options)).toEqual({ status: 200 });
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("keys circuits by origin, not path", async () => {
    const fetch = mockFetch((url) =>
      respond(url.startsWith(ORIGIN + "/") ? 503 : 200)
    );
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };

    await reject($f(`${ORIGIN}/a`, options));
    expect((await reject($f(`${ORIGIN}/b?x=1`, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(1);

    await $f("https://other.test/a", options);
    await $f(`${ORIGIN}:8443/a`, options);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("resolves the origin of string, URL and Request inputs", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };

    await reject($f(new URL(`${ORIGIN}/url`) as unknown as string, options));
    expect(fetch).toHaveBeenCalledTimes(1);

    for (const input of [
      `${ORIGIN}/string`,
      new URL(`${ORIGIN}/url`) as unknown as string,
      new Request(`${ORIGIN}/request`),
    ]) {
      expect((await reject($f(input, options)))?.message).toContain(
        "Circuit breaker is open"
      );
    }
    expect(fetch).toHaveBeenCalledTimes(1);

    await reject($f(new Request("https://other.test/request"), options));
    expect(fetch).toHaveBeenCalledTimes(2);
    await reject($f("https://other.test/string", options));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("keys relative requests by the origin resolved from `baseURL`", async () => {
    const fetch = mockFetch((url) =>
      respond(url.startsWith(ORIGIN) ? 503 : 200)
    );
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };

    await reject($f("/users", { ...options, baseURL: `${ORIGIN}/v1` }));
    expect(fetch).toHaveBeenLastCalledWith(
      `${ORIGIN}/v1/users`,
      expect.anything()
    );

    expect((await reject($f(`${ORIGIN}/health`, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    await $f("/users", { ...options, baseURL: "https://other.test" });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("keys by the request after `onRequest` rewrites it", async () => {
    const fetch = mockFetch((url) =>
      respond(url.startsWith("https://rewritten.test") ? 503 : 200)
    );
    const $f = createFetch({ fetch });
    const onRequest = vi.fn((ctx: { request: RequestInfo }) => {
      ctx.request = "https://rewritten.test/x";
    });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };

    await reject($f(`${ORIGIN}/x`, { ...options, onRequest }));
    await $f(`${ORIGIN}/x`, options);
    expect(fetch).toHaveBeenCalledTimes(2);

    // Blocked requests still run pre-fetch hooks but never reach fetch.
    const error = await reject($f(`${ORIGIN}/x`, { ...options, onRequest }));
    expect(error?.message).toContain("Circuit breaker is open");
    expect(onRequest).toHaveBeenCalledTimes(2);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("shares state between clients created from the same parent", async () => {
    const fetch = mockFetch(() => respond(503));
    const parent = createFetch({ fetch });
    const defaults: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };
    const a = parent.create(defaults);
    const b = parent.create(defaults);

    await reject(a(ORIGIN));
    expect(fetch).toHaveBeenCalledTimes(1);

    for (const client of [
      b,
      a.create({}),
      parent.create(defaults, { fetch }),
    ]) {
      expect((await reject(client(ORIGIN)))?.message).toContain(
        "Circuit breaker is open"
      );
    }
    expect((await reject(parent(ORIGIN, defaults)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(1);

    await reject(createFetch({ fetch })(ORIGIN, defaults));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("works with the default `$fetch`", async () => {
    const origin = "https://default-fetch.test";
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async () => respond(503));
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    };

    await reject($fetch(`${origin}/a`, options));
    await reject($fetch(`${origin}/b`, options));
    expect(fetch).toHaveBeenCalledTimes(2);

    expect((await reject($fetch(`${origin}/c`, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(
      (await reject($fetch.create(options)(`${origin}/d`)))?.message
    ).toContain("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("closes after a successful half-open probe", async () => {
    let status = 503;
    const fetch = mockFetch(() => respond(status));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    };

    await reject($f(ORIGIN, options));
    await reject($f(ORIGIN, options));

    vi.advanceTimersByTime(999);
    await reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(1);
    status = 200;
    await $f(ORIGIN, options);
    await $f(ORIGIN, options);
    expect(fetch).toHaveBeenCalledTimes(4);

    // Closing resets the streak, so one failure stays below the threshold.
    status = 503;
    await reject($f(ORIGIN, options));
    status = 200;
    await $f(ORIGIN, options);
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("reopens after a failed probe, restarting the cooldown from the failure", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };

    await reject($f(ORIGIN, options));
    vi.advanceTimersByTime(1000);

    const probe = Promise.withResolvers<Response>();
    fetch.mockImplementationOnce(() => probe.promise);
    const probeResult = reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(500);
    probe.resolve(respond(503));
    expect((await probeResult)?.message).toContain("503");

    vi.advanceTimersByTime(999);
    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(1);
    await reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("limits concurrent half-open probes", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000, halfOpenMaxRequests: 2 },
    };

    await reject($f(ORIGIN, options));
    vi.advanceTimersByTime(1000);

    const probes = [
      Promise.withResolvers<Response>(),
      Promise.withResolvers<Response>(),
    ];
    for (const probe of probes) {
      fetch.mockImplementationOnce(() => probe.promise);
    }
    const results = [$f(ORIGIN, options), $f(ORIGIN, options)];
    expect(fetch).toHaveBeenCalledTimes(3);

    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(3);

    for (const probe of probes) {
      probe.resolve(respond(200));
    }
    await Promise.all(results);

    fetch.mockImplementation(async () => respond(200));
    await $f(ORIGIN, options);
    await $f(ORIGIN, options);
    await $f(ORIGIN, options);
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("keeps the probe slot for all retries of the probe", async () => {
    const statuses = [503, 503, 503, 200];
    const fetch = mockFetch(() => respond(statuses.shift() ?? 200));
    const $f = createFetch({ fetch });
    const breaker = { threshold: 1, cooldown: 1000 };

    await reject($f(ORIGIN, { retry: 0, circuitBreaker: breaker }));
    vi.advanceTimersByTime(1000);

    const probe = $f(ORIGIN, {
      retry: 2,
      retryDelay: 100,
      circuitBreaker: breaker,
    });
    await vi.advanceTimersByTimeAsync(50);
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(
      (await reject($f(ORIGIN, { retry: 0, circuitBreaker: breaker })))?.message
    ).toContain("Circuit breaker is open");

    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(
      (await reject($f(ORIGIN, { retry: 0, circuitBreaker: breaker })))?.message
    ).toContain("Circuit breaker is open");

    await vi.advanceTimersByTimeAsync(100);
    expect(await probe).toEqual({ status: 200 });
    expect(fetch).toHaveBeenCalledTimes(4);

    await $f(ORIGIN, { retry: 0, circuitBreaker: breaker });
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it("counts a logical request with retries as one failure", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 3,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    };

    await reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(4);

    await reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(8);

    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(8);
  });

  it("treats a request that succeeds after retries as a success", async () => {
    const statuses = [503, 503, 200, 503];
    const fetch = mockFetch(() => respond(statuses.shift() ?? 503));
    const $f = createFetch({ fetch });
    const breaker = { threshold: 2, cooldown: 1000 };

    await reject($f(ORIGIN, { retry: 0, circuitBreaker: breaker }));
    await $f(ORIGIN, { retry: 1, circuitBreaker: breaker });
    await reject($f(ORIGIN, { retry: 0, circuitBreaker: breaker }));
    await reject($f(ORIGIN, { retry: 0, circuitBreaker: breaker }));
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it("ignores rejections by statuses outside `failureStatusCodes`", async () => {
    const statuses: number[] = [];
    const fetch = mockFetch(() => respond(statuses.shift() ?? 404));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    };

    for (let i = 0; i < 5; i++) {
      expect((await reject($f(ORIGIN, options)))?.message).toContain("404");
    }
    expect(fetch).toHaveBeenCalledTimes(5);

    // A non-listed rejection does not reset the streak either.
    statuses.push(503, 404, 503);
    await reject($f(ORIGIN, options));
    await reject($f(ORIGIN, options));
    await reject($f(ORIGIN, options));
    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(8);
  });

  it("does not close a half-open circuit on non-listed rejections", async () => {
    const statuses = [503, 503, 503, 404, 503];
    const fetch = mockFetch(() => respond(statuses.shift() ?? 200));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 3, cooldown: 1000 },
    };

    for (let i = 0; i < 3; i++) {
      await reject($f(ORIGIN, options));
    }
    vi.advanceTimersByTime(1000);

    expect((await reject($f(ORIGIN, options)))?.message).toContain("404");
    // Still half-open: a single failed probe reopens despite `threshold: 3`.
    await reject($f(ORIGIN, options));
    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it("counts listed statuses with `ignoreResponseError`", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      ignoreResponseError: true,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    };

    expect(await $f(ORIGIN, options)).toEqual({ status: 503 });
    expect(await $f(ORIGIN, options)).toEqual({ status: 503 });
    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("uses custom `failureStatusCodes`", async () => {
    const statuses = [503, 503, 503, 404];
    const fetch = mockFetch(() => respond(statuses.shift() ?? 200));
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: {
        threshold: 1,
        cooldown: 1000,
        failureStatusCodes: [404],
      },
    };

    for (let i = 0; i < 4; i++) {
      await reject($f(ORIGIN, options));
    }
    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  const failures: Record<
    string,
    { fetch: () => Promise<Response>; options?: FetchOptions }
  > = {
    "network errors": {
      fetch: () => Promise.reject(new TypeError("fetch failed")),
    },
    "invalid JSON": {
      fetch: async () =>
        new Response("{invalid", {
          headers: { "content-type": "application/json" },
        }),
    },
    "reused body": (() => {
      const response = respond(200);
      void response.text();
      return { fetch: async () => response };
    })(),
    "parseResponse exceptions": {
      fetch: async () => respond(200),
      options: {
        parseResponse: () => {
          throw new Error("parse");
        },
      },
    },
    "onRequestError exceptions": {
      fetch: () => Promise.reject(new TypeError("fetch failed")),
      options: {
        onRequestError: () => {
          throw new Error("hook");
        },
      },
    },
    "onResponse exceptions": {
      fetch: async () => respond(200),
      options: {
        onResponse: () => {
          throw new Error("hook");
        },
      },
    },
    "onResponseError exceptions": {
      fetch: async () => respond(404),
      options: {
        onResponseError: () => {
          throw new Error("hook");
        },
      },
    },
  };

  for (const [name, failure] of Object.entries(failures)) {
    it(`counts ${name} as failures`, async () => {
      const fetch = vi.fn(failure.fetch);
      const $f = createFetch({ fetch });
      const options: FetchOptions = {
        ...failure.options,
        retry: 0,
        circuitBreaker: { threshold: 1, cooldown: 1000 },
      };

      expect(await reject($f(ORIGIN, options))).toBeInstanceOf(Error);
      expect((await reject($f(ORIGIN, options)))?.message).toContain(
        "Circuit breaker is open"
      );
      expect(fetch).toHaveBeenCalledTimes(1);
    });
  }

  it("does not retry parse or hook failures", async () => {
    const fetch = mockFetch(
      async () =>
        new Response("{invalid", {
          status: 503,
          headers: { "content-type": "application/json" },
        })
    );
    const $f = createFetch({ fetch });
    const options: FetchOptions = {
      retry: 3,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    };

    await reject($f(ORIGIN, options));
    expect(fetch).toHaveBeenCalledTimes(1);

    fetch.mockImplementation(async () => respond(200));
    await reject(
      $f(ORIGIN, {
        ...options,
        onResponse: () => {
          throw new Error("hook");
        },
      })
    );
    expect(fetch).toHaveBeenCalledTimes(2);

    expect((await reject($f(ORIGIN, options)))?.message).toContain(
      "Circuit breaker is open"
    );
  });

  it("lets request-level `circuitBreaker: false` bypass defaults", async () => {
    const fetch = mockFetch(() => respond(503));
    const $f = createFetch({ fetch }).create({
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    });

    await reject($f(ORIGIN));
    expect((await reject($f(ORIGIN)))?.message).toContain(
      "Circuit breaker is open"
    );
    await reject($f(ORIGIN, { circuitBreaker: false }));
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

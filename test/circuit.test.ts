import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createFetch } from "../src/index.ts";

type Handler = (url: string) => Response | Promise<Response>;

function setup(handler: Handler = () => new Response("ok")) {
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url =
      typeof input === "string" || input instanceof URL
        ? String(input)
        : input.url;
    return handler(url);
  });
  const $fetch = createFetch({ fetch: fetch as typeof globalThis.fetch });
  return { fetch, $fetch };
}

const status = (code: number) => () => new Response("err", { status: code });

const invalidJSON = () =>
  new Response("{invalid", { headers: { "content-type": "application/json" } });

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe("circuit breaker", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const cb = { threshold: 2, cooldown: 1000 };

  it("is disabled when omitted or falsey", async () => {
    const { fetch, $fetch } = setup(status(503));
    for (let i = 0; i < 10; i++) {
      await $fetch("http://a.test/", { retry: 0 }).catch(() => {});
      await $fetch("http://a.test/", {
        retry: 0,
        circuitBreaker: false,
      }).catch(() => {});
    }
    expect(fetch).toHaveBeenCalledTimes(20);
  });

  it("opens after default threshold with `true`", async () => {
    const { fetch, $fetch } = setup(status(500));
    for (let i = 0; i < 5; i++) {
      await expect(
        $fetch("http://a.test/", { retry: 0, circuitBreaker: true })
      ).rejects.toThrow("500");
    }
    await expect(
      $fetch("http://a.test/", { retry: 0, circuitBreaker: true })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(5);

    vi.advanceTimersByTime(29_999);
    await expect(
      $fetch("http://a.test/", { retry: 0, circuitBreaker: true })
    ).rejects.toThrow("Circuit breaker is open");
    vi.advanceTimersByTime(1);
    await expect(
      $fetch("http://a.test/", { retry: 0, circuitBreaker: true })
    ).rejects.toThrow("500");
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("keys state by origin, not path", async () => {
    const { fetch, $fetch } = setup((url) =>
      url.startsWith("http://a.test") ? status(503)() : new Response("ok")
    );
    await $fetch("http://a.test/x", { retry: 0, circuitBreaker: cb }).catch(
      () => {}
    );
    await $fetch("http://a.test/y", { retry: 0, circuitBreaker: cb }).catch(
      () => {}
    );
    await expect(
      $fetch("http://a.test/z", { circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
    expect(await $fetch("http://b.test/x", { circuitBreaker: cb })).toBe("ok");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("resolves origin for string, URL, Request, baseURL and onRequest", async () => {
    const { fetch, $fetch } = setup(status(503));
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await $fetch(new URL("http://a.test/1") as unknown as string, opts).catch(
      () => {}
    );
    await $fetch(new Request("http://a.test/2"), opts).catch(() => {});
    await expect($fetch("http://a.test/3", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );

    await $fetch("/x", { ...opts, baseURL: "http://b.test/api" }).catch(
      () => {}
    );
    await $fetch("http://placeholder.test/", {
      ...opts,
      onRequest(ctx) {
        ctx.request = "http://b.test/rewritten";
      },
    }).catch(() => {});
    await expect(
      $fetch("/y", { ...opts, baseURL: "http://b.test" })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it("shares state across clients derived via create()", async () => {
    const { fetch, $fetch } = setup(status(503));
    const a = $fetch.create({ circuitBreaker: cb, retry: 0 });
    const b = $fetch.create({ circuitBreaker: cb, retry: 0 });
    await a("http://a.test/").catch(() => {});
    await b("http://a.test/").catch(() => {});
    await expect(a.create({})("http://a.test/")).rejects.toThrow(
      "Circuit breaker is open"
    );
    await expect(
      $fetch("http://a.test/", { circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("closes after a successful half-open probe", async () => {
    let fail = true;
    const { fetch, $fetch } = setup(() =>
      fail ? status(503)() : new Response("ok")
    );
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await $fetch("http://a.test/", opts).catch(() => {});
    await $fetch("http://a.test/", opts).catch(() => {});

    vi.advanceTimersByTime(1000);
    fail = false;
    expect(await $fetch("http://a.test/", opts)).toBe("ok");

    // Closed with a fresh streak: one failure does not reopen
    fail = true;
    await $fetch("http://a.test/", opts).catch(() => {});
    fail = false;
    expect(await $fetch("http://a.test/", opts)).toBe("ok");
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it("reopens on failed probe and restarts cooldown from failure time", async () => {
    const { fetch, $fetch } = setup(status(503));
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await $fetch("http://a.test/", opts).catch(() => {});
    await $fetch("http://a.test/", opts).catch(() => {});

    vi.advanceTimersByTime(1500);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("503");
    expect(fetch).toHaveBeenCalledTimes(3);

    vi.advanceTimersByTime(999);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    vi.advanceTimersByTime(1);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("503");
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it("limits concurrent half-open probes", async () => {
    const gate = deferred<void>();
    let fail = true;
    const { fetch, $fetch } = setup(() =>
      fail ? status(503)() : gate.promise.then(() => new Response("ok"))
    );
    const opts = {
      retry: 0 as const,
      circuitBreaker: { ...cb, halfOpenMaxRequests: 2 },
    };
    await $fetch("http://a.test/", opts).catch(() => {});
    await $fetch("http://a.test/", opts).catch(() => {});
    vi.advanceTimersByTime(1000);
    fail = false;

    const p1 = $fetch("http://a.test/", opts);
    const p2 = $fetch("http://a.test/", opts);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    gate.resolve();
    expect(await p1).toBe("ok");
    expect(await p2).toBe("ok");
    expect(await $fetch("http://a.test/", opts)).toBe("ok");
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it("counts retries as one logical request and keeps probe slot", async () => {
    let calls = 0;
    const gate = deferred<void>();
    const retrying = deferred<void>();
    const { fetch, $fetch } = setup(async () => {
      calls++;
      if (calls === 8) {
        retrying.resolve();
        await gate.promise;
      }
      if (calls >= 8) {
        return new Response("ok");
      }
      return status(503)();
    });
    const opts = { retry: 2, circuitBreaker: cb };

    await expect($fetch("http://a.test/", opts)).rejects.toThrow("503");
    expect(fetch).toHaveBeenCalledTimes(3);
    // A single exhausted logical request is one failure, below threshold 2
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("503");
    expect(fetch).toHaveBeenCalledTimes(6);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );

    vi.advanceTimersByTime(1000);
    // Probe: first attempt 503, retry waits on gate
    const probe = $fetch("http://a.test/", { retry: 1, circuitBreaker: cb });
    await retrying.promise;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    gate.resolve();
    expect(await probe).toBe("ok");
    expect(await $fetch("http://a.test/", opts)).toBe("ok");
  });

  it("treats non-listed statuses as neutral", async () => {
    let code = 503;
    const { fetch, $fetch } = setup(() =>
      code === 200 ? new Response("ok") : status(code)()
    );
    const opts = { retry: 0 as const, circuitBreaker: cb };

    await $fetch("http://a.test/", opts).catch(() => {});
    code = 404;
    // 404 neither counts nor resets the streak
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("404");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("404");
    code = 503;
    await $fetch("http://a.test/", opts).catch(() => {});
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );

    // 404 probe does not close half-open, but frees the slot
    vi.advanceTimersByTime(1000);
    code = 404;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("404");
    code = 503;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("503");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("respects custom failureStatusCodes", async () => {
    const { fetch, $fetch } = setup(status(418));
    const opts = {
      retry: 0 as const,
      circuitBreaker: { ...cb, failureStatusCodes: [418] },
    };
    await $fetch("http://a.test/", opts).catch(() => {});
    await $fetch("http://a.test/", opts).catch(() => {});
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("counts listed statuses with ignoreResponseError", async () => {
    const { fetch, $fetch } = setup(status(503));
    const opts = { circuitBreaker: cb, ignoreResponseError: true };
    expect(await $fetch("http://a.test/", opts)).toBe("err");
    expect(await $fetch("http://a.test/", opts)).toBe("err");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("resets consecutive failures on success", async () => {
    let fail = true;
    const { fetch, $fetch } = setup(() =>
      fail ? status(503)() : new Response("ok")
    );
    const opts = { retry: 0 as const, circuitBreaker: cb };
    for (let i = 0; i < 3; i++) {
      fail = true;
      await $fetch("http://a.test/", opts).catch(() => {});
      fail = false;
      await $fetch("http://a.test/", opts);
    }
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it.each([
    [
      "network rejection",
      () => Promise.reject(new TypeError("fetch failed")),
      {},
    ],
    [
      "body read error",
      async () => {
        const res = new Response("used");
        await res.text();
        return res;
      },
      {},
    ],
    ["parse error", invalidJSON, {}],
    [
      "parseResponse exception",
      () => new Response("ok"),
      {
        parseResponse: () => {
          throw new Error("parse");
        },
      },
    ],
    [
      "onResponse exception",
      () => new Response("ok"),
      {
        onResponse: () => {
          throw new Error("hook");
        },
      },
    ],
    [
      "onResponseError exception",
      status(404),
      {
        onResponseError: () => {
          throw new Error("hook");
        },
      },
    ],
    [
      "onRequestError exception",
      () => Promise.reject(new TypeError("fetch failed")),
      {
        onRequestError: () => {
          throw new Error("hook");
        },
      },
    ],
  ] as [string, Handler, object][])(
    "counts %s as a failure",
    async (_, handler, extra) => {
      const { fetch, $fetch } = setup(handler);
      const opts = { retry: 0 as const, circuitBreaker: cb, ...extra };
      await expect($fetch("http://a.test/", opts)).rejects.toThrow();
      await expect($fetch("http://a.test/", opts)).rejects.toThrow();
      await expect($fetch("http://a.test/", opts)).rejects.toThrow(
        "Circuit breaker is open"
      );
      expect(fetch).toHaveBeenCalledTimes(2);
    }
  );

  it("does not retry parse or hook failures", async () => {
    const { fetch, $fetch } = setup(invalidJSON);
    await expect(
      $fetch("http://a.test/", { retry: 3, circuitBreaker: cb })
    ).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("runs pre-fetch hooks but skips fetch when blocked", async () => {
    const { fetch, $fetch } = setup(status(503));
    const onRequest = vi.fn();
    const onRequestError = vi.fn();
    const opts = {
      retry: 0 as const,
      circuitBreaker: cb,
      onRequest,
      onRequestError,
    };
    await $fetch("http://a.test/", opts).catch(() => {});
    await $fetch("http://a.test/", opts).catch(() => {});
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(onRequest).toHaveBeenCalledTimes(3);
    expect(onRequestError).not.toHaveBeenCalled();
  });
});

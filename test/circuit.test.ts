import { describe, it, expect, vi, afterEach } from "vitest";
import { createFetch } from "../src/index.ts";

type Handler = (request: Request) => Response | Promise<Response>;

function setup(handler: Handler) {
  const fetch = vi.fn(async (input: any, init?: any) =>
    handler(new Request(input, init))
  );
  const $fetch = createFetch({ fetch: fetch as typeof globalThis.fetch });
  return { fetch, $fetch };
}

const status = (code: number) => () =>
  new Response(JSON.stringify({ code }), {
    status: code,
    headers: { "content-type": "application/json" },
  });

const cb = { threshold: 2, cooldown: 1000 };

describe("circuit breaker", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("does nothing when disabled", async () => {
    const { fetch, $fetch } = setup(status(500));
    for (let i = 0; i < 10; i++) {
      await expect($fetch("http://a.test/x", { retry: 0 })).rejects.toThrow(
        "500"
      );
    }
    expect(fetch).toHaveBeenCalledTimes(10);
  });

  it("opens after threshold and fails fast without calling fetch", async () => {
    const { fetch, $fetch } = setup(status(503));
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/1", opts)).rejects.toThrow("503");
    await expect($fetch("http://a.test/2", opts)).rejects.toThrow("503");
    await expect($fetch("http://a.test/3", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
    // Other origins are unaffected
    await expect($fetch("http://b.test/1", opts)).rejects.toThrow("503");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("uses defaults with `true`", async () => {
    const { fetch, $fetch } = setup(status(500));
    const opts = { retry: 0 as const, circuitBreaker: true };
    for (let i = 0; i < 5; i++) {
      await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    }
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(5);
  });

  it("half-open probe closes on success and reopens on failure", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    let code = 500;
    const { fetch, $fetch } = setup(() => status(code)());
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();

    vi.setSystemTime(999);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );

    // Failed probe reopens and restarts cooldown
    vi.setSystemTime(1000);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    expect(fetch).toHaveBeenCalledTimes(3);
    vi.setSystemTime(1999);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );

    // Successful probe closes
    vi.setSystemTime(2000);
    code = 200;
    expect(await $fetch("http://a.test/", opts)).toEqual({ code: 200 });
    code = 500;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it("limits concurrent half-open probes", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    let release!: () => void;
    let pending = false;
    const { fetch, $fetch } = setup(() => {
      if (!pending) {
        return status(500)();
      }
      return new Promise<Response>((resolve) => {
        release = () => resolve(status(200)());
      });
    });
    const opts = {
      retry: 0 as const,
      circuitBreaker: { ...cb, halfOpenMaxRequests: 1 },
    };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    vi.setSystemTime(1000);
    pending = true;
    const probe = $fetch("http://a.test/", opts);
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(3);
    release();
    expect(await probe).toEqual({ code: 200 });
  });

  it("probe keeps its slot across internal retries", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    let calls = 0;
    let concurrent: Promise<unknown> | undefined;
    const { fetch, $fetch } = setup(() => {
      calls++;
      if (calls === 4) {
        concurrent = $fetch("http://a.test/", opts).catch((error) => error);
      }
      return calls <= 3 ? status(500)() : status(200)();
    });
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    vi.setSystemTime(1000);
    // Probe: fails once, then retry succeeds
    expect(await $fetch("http://a.test/", { ...opts, retry: 1 })).toEqual({
      code: 200,
    });
    expect(String(await concurrent)).toContain("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it("counts one failure per logical request with retries", async () => {
    const { fetch, $fetch } = setup(status(500));
    const opts = { retry: 3, circuitBreaker: cb };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    expect(fetch).toHaveBeenCalledTimes(4);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    expect(fetch).toHaveBeenCalledTimes(8);
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(8);
  });

  it("does not count non-listed statuses, nor treat them as success", async () => {
    let code = 500;
    const { fetch, $fetch } = setup(() => status(code)());
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    code = 404;
    for (let i = 0; i < 5; i++) {
      await expect($fetch("http://a.test/", opts)).rejects.toThrow("404");
    }
    code = 500;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(7);
  });

  it("non-listed rejection does not close half-open", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    let code = 500;
    const { $fetch } = setup(() => status(code)());
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    vi.setSystemTime(1000);
    code = 404;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("404");
    // Still half-open: a single failed probe reopens it
    code = 500;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
  });

  it("success resets consecutive failures", async () => {
    let code = 500;
    const { fetch, $fetch } = setup(() => status(code)());
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow();
    code = 200;
    await $fetch("http://a.test/", opts);
    code = 500;
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it("counts listed statuses with ignoreResponseError", async () => {
    const { fetch, $fetch } = setup(status(503));
    const opts = { circuitBreaker: cb, ignoreResponseError: true };
    expect(await $fetch("http://a.test/", opts)).toEqual({ code: 503 });
    expect(await $fetch("http://a.test/", opts)).toEqual({ code: 503 });
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("respects custom failureStatusCodes", async () => {
    const { fetch, $fetch } = setup(status(418));
    const opts = {
      retry: 0 as const,
      circuitBreaker: { ...cb, failureStatusCodes: [418] },
    };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("418");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("418");
    await expect($fetch("http://a.test/", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("counts network, parse, body and hook failures", async () => {
    const cases: [Handler, Record<string, any>][] = [
      [
        () => {
          throw new TypeError("fetch failed");
        },
        { retry: 0 },
      ],
      [
        () =>
          new Response("{invalid", {
            headers: { "content-type": "application/json" },
          }),
        {},
      ],
      [
        () => new Response("ok"),
        {
          parseResponse: () => {
            throw new Error("parse");
          },
        },
      ],
      [
        () => new Response("ok"),
        {
          onResponse: () => {
            throw new Error("hook");
          },
        },
      ],
      [
        status(404),
        {
          onResponseError: () => {
            throw new Error("hook");
          },
        },
      ],
      [
        () => {
          throw new TypeError("fetch failed");
        },
        {
          retry: 0,
          onRequestError: () => {
            throw new Error("hook");
          },
        },
      ],
      [
        () => {
          const res = new Response("ok");
          void res.text();
          return res;
        },
        { responseType: "text" },
      ],
    ];
    for (const [handler, extra] of cases) {
      const { fetch, $fetch } = setup(handler);
      const opts = { ...extra, circuitBreaker: cb };
      await expect($fetch("http://a.test/", opts)).rejects.toThrow();
      await expect($fetch("http://a.test/", opts)).rejects.toThrow();
      await expect($fetch("http://a.test/", opts)).rejects.toThrow(
        "Circuit breaker is open"
      );
      expect(fetch).toHaveBeenCalledTimes(2);
    }
  });

  it("does not retry parse failures", async () => {
    const { fetch, $fetch } = setup(
      () =>
        new Response("{invalid", {
          headers: { "content-type": "application/json" },
        })
    );
    await expect(
      $fetch("http://a.test/", { retry: 3, circuitBreaker: cb })
    ).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("keys by origin for string, URL, Request and baseURL", async () => {
    const { fetch, $fetch } = setup(status(500));
    const opts = { retry: 0 as const, circuitBreaker: cb };
    await expect($fetch("http://a.test/x", opts)).rejects.toThrow("500");
    await expect($fetch(new URL("http://a.test/y"), opts)).rejects.toThrow(
      "500"
    );
    await expect($fetch(new Request("http://a.test/z"), opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    await expect(
      $fetch("/z", { ...opts, baseURL: "http://a.test/api" })
    ).rejects.toThrow("Circuit breaker is open");
    await expect(
      $fetch("/z", { ...opts, baseURL: "http://b.test/api" })
    ).rejects.toThrow("500");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("keys by effective request after onRequest mutation", async () => {
    const { fetch, $fetch } = setup(status(500));
    const opts = {
      retry: 0 as const,
      circuitBreaker: cb,
      onRequest(ctx: any) {
        ctx.request = "http://b.test/rewritten";
      },
    };
    await expect($fetch("http://a.test/", opts)).rejects.toThrow("500");
    await expect($fetch("http://c.test/", opts)).rejects.toThrow("500");
    await expect(
      $fetch("http://a.test/", { retry: 0, circuitBreaker: cb })
    ).rejects.toThrow("500");
    await expect(
      $fetch("http://b.test/", { retry: 0, circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("shares state across .create() clients", async () => {
    const { fetch, $fetch } = setup(status(500));
    const a = $fetch.create({ retry: 0, circuitBreaker: cb });
    const b = $fetch.create({ retry: 0, circuitBreaker: cb });
    const c = a.create({});
    await expect(a("http://a.test/")).rejects.toThrow("500");
    await expect(b("http://a.test/")).rejects.toThrow("500");
    await expect(c("http://a.test/")).rejects.toThrow(
      "Circuit breaker is open"
    );
    await expect(
      $fetch("http://a.test/", { circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

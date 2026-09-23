import { afterEach, describe, expect, it, vi } from "vitest";
import { $fetch as globalFetch, createFetch } from "../src/index.ts";
import type { FetchOptions } from "../src/types.ts";

const ORIGIN = "https://circuit.example";

function jsonResponse(status: number, body?: unknown) {
  return new Response(JSON.stringify(body ?? { ok: status < 400 }), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function client(fetchImpl: typeof fetch, defaults?: FetchOptions) {
  return createFetch({
    fetch: fetchImpl,
    defaults,
  });
}

async function expectOpen(promise: Promise<unknown>) {
  await expect(promise).rejects.toThrow("Circuit breaker is open");
}

describe("circuit breaker", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function useClock() {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(0));
  }

  it("does not track or block when circuitBreaker is omitted or false", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(500));
    const api = client(fetchMock);

    for (let i = 0; i < 6; i++) {
      await api(`${ORIGIN}/fail`, { retry: 0 }).catch(() => {});
    }
    await api(`${ORIGIN}/fail`, { retry: 0, circuitBreaker: false }).catch(
      () => {}
    );

    expect(fetchMock).toHaveBeenCalledTimes(7);
  });

  it("opens after the default threshold and skips fetch", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(503));
    const api = client(fetchMock);
    const options = { circuitBreaker: true, retry: 0 } as const;

    for (let i = 0; i < 5; i++) {
      await api(`${ORIGIN}/down`, options).catch(() => {});
    }
    expect(fetchMock).toHaveBeenCalledTimes(5);

    const onRequest = vi.fn();
    await expectOpen(api(`${ORIGIN}/down/again`, { ...options, onRequest }));
    expect(fetchMock).toHaveBeenCalledTimes(5);
    expect(onRequest).toHaveBeenCalledOnce();
  });

  it("uses custom threshold, cooldown, and failure status codes", async () => {
    useClock();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const status = url.endsWith("/teapot") ? 418 : 500;
      return jsonResponse(status);
    });
    const api = client(fetchMock);
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: {
        threshold: 2,
        cooldown: 1000,
        failureStatusCodes: [418],
      },
    };

    await api(`${ORIGIN}/err`, options).catch(() => {});
    await api(`${ORIGIN}/err`, options).catch(() => {});
    await api(`${ORIGIN}/err`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(3);

    await api(`${ORIGIN}/teapot`, options).catch(() => {});
    await api(`${ORIGIN}/teapot`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(5);

    await expectOpen(api(`${ORIGIN}/teapot`, options));
    expect(fetchMock).toHaveBeenCalledTimes(5);

    vi.setSystemTime(new Date(999));
    await expectOpen(api(`${ORIGIN}/later`, options));
    expect(fetchMock).toHaveBeenCalledTimes(5);

    vi.setSystemTime(new Date(1000));
    await api(`${ORIGIN}/later`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("keys state by origin across paths, URL inputs, and Request inputs", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(500));
    const api = client(fetchMock);
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 1 },
    } as const;

    await api(`${ORIGIN}/a?x=1`, options).catch(() => {});
    await expectOpen(api(new URL(`${ORIGIN}/b`), options));
    await expectOpen(api(new Request(`${ORIGIN}/c`), options));
    await api("https://other.example/a", options).catch(() => {});

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keys relative requests by the effective origin after baseURL resolution", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) =>
      jsonResponse(500)
    );
    const api = client(fetchMock);
    const options = {
      retry: 0,
      baseURL: `${ORIGIN}/api/`,
      circuitBreaker: { threshold: 1 },
    } as const;

    await api("/health", options).catch(() => {});
    await expectOpen(api("status", options));
    await api("/health", {
      ...options,
      baseURL: "https://healthy.example",
    }).catch(() => {});

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(String(fetchMock.mock.calls[0][0])).toBe(`${ORIGIN}/api/health`);
    expect(String(fetchMock.mock.calls[1][0])).toBe(
      "https://healthy.example/health"
    );
  });

  it("keys the origin after onRequest rewrites the request", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(500));
    const api = client(fetchMock);
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1 },
      onRequest(ctx) {
        ctx.request = "https://rewritten.example/path";
      },
    };

    await api(`${ORIGIN}/original`, options).catch(() => {});
    await api(`${ORIGIN}/still-original`, {
      retry: 0,
      circuitBreaker: { threshold: 1 },
    }).catch(() => {});
    await expectOpen(
      api("https://rewritten.example/other", {
        retry: 0,
        circuitBreaker: { threshold: 1 },
      })
    );

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("shares circuit state between a parent and clients derived with create()", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(500));
    const parent = client(fetchMock);
    const child = parent.create({ retry: 0, circuitBreaker: { threshold: 1 } });
    const sibling = parent.create({});

    await child(`${ORIGIN}/from-child`).catch(() => {});
    await expectOpen(
      sibling(`${ORIGIN}/from-sibling`, {
        retry: 0,
        circuitBreaker: { threshold: 1 },
      })
    );
    await expectOpen(
      parent(`${ORIGIN}/from-parent`, {
        retry: 0,
        circuitBreaker: { threshold: 1 },
      })
    );

    const isolated = createFetch({ fetch: fetchMock });
    await isolated(`${ORIGIN}/from-child`, {
      retry: 0,
      circuitBreaker: { threshold: 1 },
    }).catch(() => {});

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("works through the default $fetch client", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(jsonResponse(500));
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 1 },
    } as const;
    const url = "https://default-fetch-circuit.example/item";

    await globalFetch(url, options).catch(() => {});
    await expectOpen(globalFetch.raw(url, options));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    fetchMock.mockRestore();
  });

  it("resets consecutive failures after a successful logical request", async () => {
    const statuses = [500, 500, 200, 500, 500, 500];
    const fetchMock = vi.fn(async () => jsonResponse(statuses.shift() ?? 200));
    const api = client(fetchMock);
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 3 },
    } as const;

    for (let i = 0; i < 5; i++) {
      await api(`${ORIGIN}/streak`, options).catch(() => {});
    }
    expect(fetchMock).toHaveBeenCalledTimes(5);

    await api(`${ORIGIN}/streak`, options).catch(() => {});
    await expectOpen(api(`${ORIGIN}/streak`, options));
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("counts listed statuses once per logical request, including retries and ignoreResponseError", async () => {
    const fetchMock = vi.fn(async () => jsonResponse(502));
    const api = client(fetchMock);
    const options: FetchOptions = {
      retry: 3,
      circuitBreaker: { threshold: 2, cooldown: 60_000 },
    };

    await api(`${ORIGIN}/retry`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(4);

    await api(`${ORIGIN}/retry`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(8);

    await expectOpen(api(`${ORIGIN}/retry`, { ...options, retry: 0 }));
    expect(fetchMock).toHaveBeenCalledTimes(8);

    const ignored = vi.fn(async () => jsonResponse(504));
    const ignoring = client(ignored);
    await ignoring(`${ORIGIN}/ignored`, {
      retry: 0,
      ignoreResponseError: true,
      circuitBreaker: { threshold: 1 },
    });
    await expectOpen(
      ignoring(`${ORIGIN}/ignored`, {
        retry: 0,
        circuitBreaker: { threshold: 1 },
      })
    );
    expect(ignored).toHaveBeenCalledTimes(1);
  });

  it("does not treat rejected non-listed statuses as success or failure", async () => {
    const statuses = [500, 500, 500, 500, 404, 500];
    const fetchMock = vi.fn(async () => jsonResponse(statuses.shift() ?? 200));
    const api = client(fetchMock);
    const options = {
      retry: 0,
      circuitBreaker: { threshold: 5 },
    } as const;

    for (let i = 0; i < 6; i++) {
      await api(`${ORIGIN}/mixed`, options).catch(() => {});
    }
    expect(fetchMock).toHaveBeenCalledTimes(6);
    await expectOpen(api(`${ORIGIN}/mixed`, options));
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("treats an ignored non-listed status as success and a listed one as failure", async () => {
    const statuses = [500, 500, 404, 500, 500, 500, 500, 500];
    const fetchMock = vi.fn(async () => jsonResponse(statuses.shift() ?? 200));
    const api = client(fetchMock);
    const options: FetchOptions = {
      retry: 0,
      ignoreResponseError: true,
      circuitBreaker: { threshold: 5 },
    };

    await api(`${ORIGIN}/ignored-404`, options).catch(() => {});
    await api(`${ORIGIN}/ignored-404`, options).catch(() => {});
    expect(await api(`${ORIGIN}/ignored-404`, options)).toMatchObject({
      ok: false,
    });
    for (let i = 0; i < 5; i++) {
      await api(`${ORIGIN}/ignored-404`, options).catch(() => {});
    }
    expect(fetchMock).toHaveBeenCalledTimes(8);
    await expectOpen(api(`${ORIGIN}/ignored-404`, options));
    expect(fetchMock).toHaveBeenCalledTimes(8);
  });

  it("keeps half-open state when a probe is rejected with a non-listed status", async () => {
    useClock();
    let phase: "burn" | "neutral" | "fail-probe" = "burn";
    const fetchMock = vi.fn(async () => {
      if (phase === "neutral") {
        return jsonResponse(404);
      }
      return jsonResponse(500);
    });
    const api = client(fetchMock);
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 5, cooldown: 500 },
    };

    for (let i = 0; i < 5; i++) {
      await api(`${ORIGIN}/half`, options).catch(() => {});
    }
    phase = "neutral";
    vi.setSystemTime(new Date(500));
    await api(`${ORIGIN}/half`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(6);

    phase = "fail-probe";
    await api(`${ORIGIN}/half`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(7);
    await expectOpen(api(`${ORIGIN}/half`, options));
    expect(fetchMock).toHaveBeenCalledTimes(7);
  });

  it("closes on a successful half-open probe and reopens when a probe fails", async () => {
    useClock();
    const statuses = [500, 200, 500];
    const fetchMock = vi.fn(async () => jsonResponse(statuses.shift() ?? 200));
    const api = client(fetchMock);
    const options: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000 },
    };

    await api(`${ORIGIN}/probe`, options).catch(() => {});
    vi.setSystemTime(new Date(1000));
    expect(await api(`${ORIGIN}/probe`, options)).toEqual({ ok: true });

    await api(`${ORIGIN}/probe`, options).catch(() => {});
    vi.setSystemTime(new Date(1999));
    await expectOpen(api(`${ORIGIN}/probe`, options));
    expect(fetchMock).toHaveBeenCalledTimes(3);

    vi.setSystemTime(new Date(2000));
    await api(`${ORIGIN}/probe`, options).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("limits concurrent half-open probes and holds a slot across retries", async () => {
    useClock();
    let phase: "burn" | "hold" | "ok" = "burn";
    let releaseFirst: ((response: Response) => void) | undefined;
    let releaseRetry: ((response: Response) => void) | undefined;
    const fetchMock = vi.fn(() => {
      if (phase === "burn") {
        return Promise.resolve(jsonResponse(500));
      }
      if (phase === "ok") {
        return Promise.resolve(jsonResponse(200, { ok: true }));
      }
      return new Promise<Response>((resolve) => {
        if (releaseFirst) {
          releaseRetry = resolve;
        } else {
          releaseFirst = resolve;
        }
      });
    });
    const api = client(fetchMock);
    const openOptions: FetchOptions = {
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 1000, halfOpenMaxRequests: 1 },
    };

    await api(`${ORIGIN}/slot`, openOptions).catch(() => {});
    expect(fetchMock).toHaveBeenCalledTimes(1);

    phase = "hold";
    vi.setSystemTime(new Date(1000));
    const probe = api(`${ORIGIN}/slot`, {
      ...openOptions,
      retry: 1,
    });
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    await expectOpen(api(`${ORIGIN}/slot`, openOptions));
    expect(fetchMock).toHaveBeenCalledTimes(2);

    releaseFirst!(jsonResponse(500));
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(3);
    });
    await expectOpen(api(`${ORIGIN}/slot`, openOptions));
    expect(fetchMock).toHaveBeenCalledTimes(3);

    releaseRetry!(jsonResponse(200, { ok: true }));
    await expect(probe).resolves.toEqual({ ok: true });

    phase = "ok";
    await api(`${ORIGIN}/slot`, openOptions);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("counts network, parse, body-read, and hook failures once", async () => {
    const cases: Array<{
      name: string;
      fetchImpl: typeof fetch;
      options: FetchOptions;
      calls?: number;
    }> = [
      {
        name: "network",
        fetchImpl: vi.fn(async () => {
          throw new TypeError("fetch failed");
        }),
        options: { retry: 4 },
        calls: 5,
      },
      {
        name: "parseResponse",
        fetchImpl: vi.fn(async () => jsonResponse(200, { ok: true })),
        options: {
          retry: 4,
          parseResponse() {
            throw new Error("bad parse");
          },
        },
      },
      {
        name: "invalid json",
        fetchImpl: vi.fn(async () => {
          return new Response("nope", {
            status: 200,
            headers: { "content-type": "application/json" },
          });
        }),
        options: { retry: 4 },
      },
      {
        name: "body read",
        fetchImpl: vi.fn(async () => {
          const response = new Response("hello", {
            status: 200,
            headers: { "content-type": "text/plain" },
          });
          vi.spyOn(response, "text").mockRejectedValue(
            new TypeError("body already used")
          );
          return response;
        }),
        options: { retry: 4 },
      },
      {
        name: "onRequestError",
        fetchImpl: vi.fn(async () => {
          throw new TypeError("fetch failed");
        }),
        options: {
          retry: 4,
          onRequestError() {
            throw new Error("error in onRequestError");
          },
        },
      },
      {
        name: "onResponse",
        fetchImpl: vi.fn(async () => jsonResponse(200, { ok: true })),
        options: {
          retry: 4,
          onResponse() {
            throw new Error("error in onResponse");
          },
        },
      },
      {
        name: "onResponseError",
        fetchImpl: vi.fn(async () => jsonResponse(404)),
        options: {
          retry: 4,
          onResponseError() {
            throw new Error("error in onResponseError");
          },
        },
      },
    ];

    for (const entry of cases) {
      const fetchMock = entry.fetchImpl as ReturnType<typeof vi.fn>;
      const api = client(entry.fetchImpl);
      const options: FetchOptions = {
        ...entry.options,
        circuitBreaker: { threshold: 1 },
      };
      await api(`${ORIGIN}/${entry.name}`, options).catch(() => {});
      expect(fetchMock, entry.name).toHaveBeenCalledTimes(entry.calls ?? 1);
      await expectOpen(api(`${ORIGIN}/${entry.name}`, options));
      expect(fetchMock, entry.name).toHaveBeenCalledTimes(entry.calls ?? 1);
    }
  });
});

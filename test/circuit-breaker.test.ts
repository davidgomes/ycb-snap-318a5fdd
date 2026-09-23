import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createFetch, FetchError, $fetch } from "../src/index.ts";
import type { $Fetch, CircuitBreakerOptions } from "../src/index.ts";

let now = 1_000_000;
let seq = 0;

function host(label = "app"): string {
  seq += 1;
  return `https://${label}-${seq}.example`;
}

function jsonResponse(
  body: unknown,
  status = 200,
  statusText = "OK"
): Response {
  return new Response(JSON.stringify(body), {
    status,
    statusText,
    headers: { "content-type": "application/json" },
  });
}

function textResponse(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: { "content-type": "text/plain" },
  });
}

function makeApi(
  fetchImpl: typeof fetch = async () =>
    jsonResponse({ ok: false }, 500, "Server Error")
): { api: $Fetch; fetchMock: ReturnType<typeof vi.fn> } {
  const fetchMock = vi.fn(fetchImpl);
  const api = createFetch({ fetch: fetchMock });
  return { api, fetchMock };
}

function breaker(overrides: CircuitBreakerOptions = {}): CircuitBreakerOptions {
  return {
    threshold: 1,
    cooldown: 60_000,
    halfOpenMaxRequests: 1,
    ...overrides,
  };
}

describe("circuit breaker", () => {
  beforeEach(() => {
    now = 1_000_000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not track or block when circuitBreaker is omitted or false", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/item`;

    await expect(api(url, { retry: 0 })).rejects.toThrow(/500/);
    await expect(api(url, { retry: 0, circuitBreaker: false })).rejects.toThrow(
      /500/
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);

    await expect(
      api(url, { retry: 0, circuitBreaker: breaker({ threshold: 1 }) })
    ).rejects.toThrow(/500/);
    fetchMock.mockClear();

    await expect(api(url, { retry: 0 })).rejects.toThrow(/500/);
    await expect(api(url, { retry: 0, circuitBreaker: false })).rejects.toThrow(
      /500/
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("opens after consecutive failures and fails fast without calling fetch", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/down`;
    const options = { retry: 0, circuitBreaker: breaker({ threshold: 3 }) };

    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    await expect(api(`${host("other")}/down`, options)).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    await expect(api(url, options)).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(4);

    const error = await api(url, { ...options, retry: 8 }).catch(
      (error_: unknown) => error_
    );
    expect(error).toBeInstanceOf(FetchError);
    expect((error as Error).message).toContain("Circuit breaker is open");
    expect((error as FetchError).request).toBe(url);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("resets the failure streak after a successful logical request", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/mix`;
    const options = { retry: 0, circuitBreaker: breaker({ threshold: 3 }) };

    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);

    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(api(url, options)).resolves.toEqual({ ok: true });

    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(5);

    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("counts only configured failure statuses and ignores other rejected statuses", async () => {
    const statuses = [408, 409, 425, 429, 500, 502, 503, 504];
    for (const status of statuses) {
      const { api, fetchMock } = makeApi(async () =>
        jsonResponse({ status }, status, "Error")
      );
      const url = `${host("status")}/x`;
      const options = { retry: 0, circuitBreaker: breaker() };
      await expect(api(url, options)).rejects.toThrow(String(status));
      await expect(api(url, options)).rejects.toThrow(
        /Circuit breaker is open/
      );
      expect(fetchMock).toHaveBeenCalledTimes(1);
    }

    for (const status of [400, 401, 403, 404, 418, 501]) {
      const { api, fetchMock } = makeApi(async () =>
        jsonResponse({ status }, status, "Error")
      );
      const url = `${host("benign")}/x`;
      const options = { retry: 0, circuitBreaker: breaker({ threshold: 1 }) };
      await expect(api(url, options)).rejects.toThrow(String(status));
      await expect(api(url, options)).rejects.toThrow(String(status));
      expect(fetchMock).toHaveBeenCalledTimes(2);
    }
  });

  it("does not reset a failure streak when a non-listed status is rejected", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/streak`;
    const options = { retry: 0, circuitBreaker: breaker({ threshold: 3 }) };

    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);

    fetchMock.mockResolvedValueOnce(
      jsonResponse({ missing: true }, 404, "Not Found")
    );
    await expect(api(url, options)).rejects.toThrow(/404/);

    fetchMock.mockResolvedValueOnce(
      jsonResponse({ nope: true }, 501, "Not Implemented")
    );
    await expect(api(url, options)).rejects.toThrow(/501/);

    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(5);
  });

  it("counts listed statuses when ignoreResponseError is set", async () => {
    const { api, fetchMock } = makeApi(async () =>
      jsonResponse({ err: true }, 503, "Unavailable")
    );
    const url = `${host()}/ignored`;
    const circuitBreaker = breaker({ threshold: 2 });

    await expect(
      api(url, { retry: 0, ignoreResponseError: true, circuitBreaker })
    ).resolves.toEqual({ err: true });
    await expect(
      api(url, { retry: 0, ignoreResponseError: true, circuitBreaker })
    ).resolves.toEqual({ err: true });
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("treats a non-listed ignored status as success and resets the streak", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/reset-by-404`;
    const circuitBreaker = breaker({ threshold: 2 });

    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);

    fetchMock.mockResolvedValueOnce(
      jsonResponse({ missing: true }, 404, "Not Found")
    );
    await expect(
      api(url, { retry: 0, ignoreResponseError: true, circuitBreaker })
    ).resolves.toEqual({ missing: true });

    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("uses a custom failureStatusCodes list instead of the defaults", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/custom-codes`;
    const circuitBreaker = breaker({ failureStatusCodes: [404] });

    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);

    fetchMock.mockResolvedValueOnce(
      jsonResponse({ missing: true }, 404, "Not Found")
    );
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/404/);
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("counts one failure when retries are exhausted", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/retry-fail`;
    const circuitBreaker = breaker({ threshold: 3 });

    await expect(
      api(url, { retry: 2, retryDelay: 0, circuitBreaker })
    ).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(5);

    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(5);
  });

  it("does not count intermediate attempts when a later retry succeeds", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/retry-ok`;
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ ok: false }, 500, "Server Error"))
      .mockResolvedValueOnce(jsonResponse({ ok: false }, 502, "Bad Gateway"))
      .mockResolvedValueOnce(jsonResponse({ ok: true }));

    await expect(
      api(url, {
        retry: 2,
        retryDelay: 0,
        circuitBreaker: breaker({ threshold: 1 }),
      })
    ).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(3);

    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(
      api(url, { retry: 0, circuitBreaker: breaker({ threshold: 1 }) })
    ).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("does not count an intermediate failure when the final attempt is a non-listed rejection", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/retry-neutral`;
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ ok: false }, 500, "Server Error"))
      .mockResolvedValueOnce(jsonResponse({ missing: true }, 404, "Not Found"));

    await expect(
      api(url, {
        retry: 1,
        retryDelay: 0,
        circuitBreaker: breaker({ threshold: 1 }),
      })
    ).rejects.toThrow(/404/);

    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(
      api(url, { retry: 0, circuitBreaker: breaker({ threshold: 1 }) })
    ).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("counts network failures once per logical request", async () => {
    const { api, fetchMock } = makeApi(async () => {
      throw new TypeError("network down");
    });
    const url = `${host()}/net`;

    await expect(
      api(url, {
        retry: 2,
        retryDelay: 0,
        circuitBreaker: breaker({ threshold: 2 }),
      })
    ).rejects.toThrow(/network down/);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    await expect(
      api(url, { retry: 0, circuitBreaker: breaker({ threshold: 2 }) })
    ).rejects.toThrow(/network down/);
    await expect(
      api(url, { retry: 0, circuitBreaker: breaker({ threshold: 2 }) })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("counts parse and body-read failures without retrying them", async () => {
    const parseUrl = `${host()}/parse`;
    const { api, fetchMock } = makeApi(async () => jsonResponse({ ok: true }));
    await expect(
      api(parseUrl, {
        retry: 4,
        circuitBreaker: breaker(),
        parseResponse() {
          throw new Error("parse boom");
        },
      })
    ).rejects.toThrow("parse boom");
    await expect(
      api(parseUrl, { retry: 4, circuitBreaker: breaker() })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const invalidUrl = `${host()}/invalid-json`;
    const invalid = makeApi(
      async () =>
        new Response("not-json", {
          status: 200,
          headers: { "content-type": "application/json" },
        })
    );
    await expect(
      invalid.api(invalidUrl, { retry: 3, circuitBreaker: breaker() })
    ).rejects.toThrow(SyntaxError);
    await expect(
      invalid.api(invalidUrl, { retry: 0, circuitBreaker: breaker() })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(invalid.fetchMock).toHaveBeenCalledTimes(1);

    const consumed = textResponse("hello");
    await consumed.text();
    const body = makeApi(async () => consumed);
    const bodyUrl = `${host()}/body`;
    await expect(
      body.api(bodyUrl, {
        retry: 3,
        responseType: "text",
        circuitBreaker: breaker(),
      })
    ).rejects.toThrow(/body/i);
    await expect(
      body.api(bodyUrl, { retry: 0, circuitBreaker: breaker() })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(body.fetchMock).toHaveBeenCalledTimes(1);
  });

  it("counts hook failures and does not retry them", async () => {
    const cases: Array<{
      name: string;
      init: () => Promise<Response>;
      options: Record<string, unknown>;
      message: string;
    }> = [
      {
        name: "onResponse",
        init: async () => jsonResponse({ ok: true }),
        options: {
          onResponse() {
            throw new Error("response hook");
          },
        },
        message: "response hook",
      },
      {
        name: "onResponseError",
        init: async () => jsonResponse({ missing: true }, 404, "Not Found"),
        options: {
          onResponseError() {
            throw new Error("response error hook");
          },
        },
        message: "response error hook",
      },
      {
        name: "onRequestError",
        init: async () => {
          throw new Error("net");
        },
        options: {
          onRequestError() {
            throw new Error("request error hook");
          },
        },
        message: "request error hook",
      },
    ];

    for (const item of cases) {
      const { api, fetchMock } = makeApi(item.init);
      const url = `${host(item.name)}/hook`;
      const circuitBreaker = breaker();
      await expect(
        api(url, { retry: 5, circuitBreaker, ...item.options })
      ).rejects.toThrow(item.message);
      await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(
        /Circuit breaker is open/
      );
      expect(fetchMock).toHaveBeenCalledTimes(1);
    }
  });

  it("keeps payload methods on a single attempt and still records the failure", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/post`;
    await expect(
      api(url, {
        method: "POST",
        body: { a: 1 },
        circuitBreaker: breaker(),
      })
    ).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(init.body))).toEqual({ a: 1 });

    await expect(
      api(url, { method: "POST", circuitBreaker: breaker() })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("transitions to half-open after cooldown and closes on a successful probe", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/recover`;
    const circuitBreaker = breaker({ threshold: 2, cooldown: 5000 });
    const options = { retry: 0, circuitBreaker };

    now = 10_000;
    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);

    now = 14_999;
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    now = 15_000;
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(api(url, options)).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(3);

    fetchMock.mockImplementation(async () =>
      jsonResponse({ ok: false }, 500, "Server Error")
    );
    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(5);
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(5);
  });

  it("reopens from a failed probe and restarts cooldown at the failure time", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/reopen`;
    const circuitBreaker = breaker({ threshold: 2, cooldown: 5000 });
    const options = { retry: 0, circuitBreaker };

    now = 1000;
    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/500/);

    now = 6000;
    fetchMock.mockImplementation(async () => {
      now = 8000;
      return jsonResponse({ ok: false }, 500, "Server Error");
    });
    await expect(api(url, options)).rejects.toThrow(/500/);

    now = 11_000;
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);

    now = 12_999;
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    now = 13_000;
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(api(url, options)).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("does not let fast-fail calls restart cooldown", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/fast-fail-time`;
    const options = {
      retry: 0,
      circuitBreaker: breaker({ threshold: 1, cooldown: 10_000 }),
    };

    now = 0;
    await expect(api(url, options)).rejects.toThrow(/500/);
    now = 100;
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);

    now = 10_000;
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(api(url, options)).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("allows a probe immediately when cooldown is zero", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/zero-cooldown`;
    const options = {
      retry: 0,
      circuitBreaker: breaker({ threshold: 1, cooldown: 0 }),
    };

    await expect(api(url, options)).rejects.toThrow(/500/);
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(api(url, options)).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("limits concurrent half-open probes and fails the rest immediately", async () => {
    const { api, fetchMock } = makeApi(async () => jsonResponse({ ok: true }));
    const url = `${host()}/quota`;
    const circuitBreaker = breaker({
      threshold: 1,
      cooldown: 1000,
      halfOpenMaxRequests: 2,
    });

    fetchMock.mockResolvedValueOnce(
      jsonResponse({ ok: false }, 500, "Server Error")
    );
    now = 0;
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);

    now = 1000;
    fetchMock.mockImplementation(async () => jsonResponse({ ok: true }));
    const first = api(url, { retry: 0, circuitBreaker });
    const second = api(url, { retry: 0, circuitBreaker });
    const third = api(url, { retry: 0, circuitBreaker });

    await expect(third).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    await expect(first).resolves.toEqual({ ok: true });
    await expect(second).resolves.toEqual({ ok: true });

    await expect(api(url, { retry: 0, circuitBreaker })).resolves.toEqual({
      ok: true,
    });
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("keeps a half-open slot for the whole logical request, including retries", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/slot`;
    const circuitBreaker = breaker({
      threshold: 1,
      cooldown: 1000,
      halfOpenMaxRequests: 1,
    });

    now = 5000;
    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(/500/);

    now = 6000;
    let calls = 0;
    let releaseSecond: ((value: Response) => void) | undefined;
    let markSecondStarted: (() => void) | undefined;
    const secondStarted = new Promise<void>((resolve) => {
      markSecondStarted = resolve;
    });
    fetchMock.mockImplementation(() => {
      calls += 1;
      if (calls === 1) {
        return Promise.resolve(
          jsonResponse({ ok: false }, 500, "Server Error")
        );
      }
      markSecondStarted?.();
      return new Promise<Response>((resolve) => {
        releaseSecond = resolve;
      });
    });

    const probe = api(url, { retry: 1, retryDelay: 0, circuitBreaker });
    await secondStarted;

    await expect(api(url, { retry: 0, circuitBreaker })).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(calls).toBe(2);

    releaseSecond?.(jsonResponse({ ok: true }));
    await expect(probe).resolves.toEqual({ ok: true });

    fetchMock.mockResolvedValue(
      jsonResponse({ ok: false }, 500, "Server Error")
    );
    await expect(
      api(url, {
        retry: 0,
        circuitBreaker: breaker({ threshold: 2, cooldown: 60_000 }),
      })
    ).rejects.toThrow(/500/);
  });

  it("stays half-open when a probe is rejected with a non-listed status", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/neutral-probe`;
    const circuitBreaker = breaker({
      threshold: 5,
      cooldown: 1000,
      halfOpenMaxRequests: 1,
    });
    const options = { retry: 0, circuitBreaker };

    now = 0;
    for (let index = 0; index < 5; index += 1) {
      await expect(api(url, options)).rejects.toThrow(/500/);
    }

    now = 1000;
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ missing: true }, 404, "Not Found")
    );
    await expect(api(url, options)).rejects.toThrow(/404/);

    let releaseProbe: ((value: Response) => void) | undefined;
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          releaseProbe = resolve;
        })
    );

    const probe = api(url, options);
    const extra = api(url, options);
    await expect(extra).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(7);

    releaseProbe?.(jsonResponse({ ok: false }, 500, "Server Error"));
    await expect(probe).rejects.toThrow(/500/);

    fetchMock.mockResolvedValue(jsonResponse({ ok: true }));
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(7);
  });

  it("runs pre-fetch hooks before blocking and keys the origin after them", async () => {
    const { api, fetchMock } = makeApi();
    const bad = `${host("bad")}/start`;
    const good = `${host("good")}/start`;
    const options = { retry: 0, circuitBreaker: breaker() };

    await expect(api(bad, options)).rejects.toThrow(/500/);
    fetchMock.mockClear();

    const onRequest = vi.fn();
    await expect(api(bad, { ...options, onRequest })).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(onRequest).toHaveBeenCalledOnce();
    expect(fetchMock).not.toHaveBeenCalled();

    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(
      api(bad, {
        ...options,
        onRequest(ctx) {
          ctx.request = `${good}/rewritten`;
        },
      })
    ).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await expect(
      api(good, {
        ...options,
        onRequest(ctx) {
          ctx.request = bad;
        },
      })
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("keys string, URL, and Request inputs by origin, including baseURL", async () => {
    const { api, fetchMock } = makeApi();
    const base = host("keyed");
    const options = { retry: 0, circuitBreaker: breaker() };

    await expect(api(`${base}/a?x=1`, options)).rejects.toThrow(/500/);
    await expect(api(new URL(`${base}/b#hash`), options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect(api(new Request(`${base}/c`), options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const otherPort = base
      .replace("https://", "https://")
      .replace(".example", ".example:8443");
    await expect(api(`${otherPort}/a`, options)).rejects.toThrow(/500/);
    await expect(
      api(`${base.replace("https://", "http://")}/a`, options)
    ).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    const upper = `${base.toUpperCase()}/upper`;
    await expect(api(upper, options)).rejects.toThrow(
      /Circuit breaker is open/
    );

    const withDefaultPort = base
      .replace("https://", "https://")
      .replace(".example", ".example:443");
    await expect(api(`${withDefaultPort}/port`, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("keys relative requests by the origin of the resolved baseURL", async () => {
    const { api, fetchMock } = makeApi();
    const options = { retry: 0, circuitBreaker: breaker() };

    await expect(
      api("/health", {
        ...options,
        baseURL: "https://api.example/v1",
        query: { q: 1 },
      })
    ).rejects.toThrow(/500/);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example/v1/health?q=1"
    );

    await expect(api("https://api.example/status", options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect(
      api("health", { ...options, baseURL: "https://other.example/api" })
    ).rejects.toThrow(/500/);
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe(
      "https://other.example/api/health"
    );

    await expect(
      api("/health", {
        retry: 0,
        circuitBreaker: breaker(),
        onRequest(ctx) {
          ctx.request = "/live";
          ctx.options.baseURL = "https://hooks.example/root";
        },
      })
    ).rejects.toThrow(/500/);
    await expect(api("https://hooks.example/other", options)).rejects.toThrow(
      /Circuit breaker is open/
    );
  });

  it("applies circuitBreaker: true defaults", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/defaults`;

    now = 50_000;
    for (let index = 0; index < 4; index += 1) {
      await expect(
        api(url, { retry: 0, circuitBreaker: true })
      ).rejects.toThrow(/500/);
    }
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ missing: true }, 404, "Not Found")
    );
    await expect(api(url, { retry: 0, circuitBreaker: true })).rejects.toThrow(
      /404/
    );
    await expect(api(url, { retry: 0, circuitBreaker: true })).rejects.toThrow(
      /500/
    );
    expect(fetchMock).toHaveBeenCalledTimes(6);

    now = 79_999;
    await expect(api(url, { retry: 0, circuitBreaker: true })).rejects.toThrow(
      /Circuit breaker is open/
    );

    now = 80_000;
    const first = api(url, { retry: 0, circuitBreaker: true });
    const second = api(url, { retry: 0, circuitBreaker: true });
    await expect(second).rejects.toThrow(/Circuit breaker is open/);
    await expect(first).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(7);
  });

  it("fills omitted object fields from the defaults", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/partial`;
    const options = { retry: 0, circuitBreaker: { threshold: 1 } };

    now = 0;
    await expect(api(url, options)).rejects.toThrow(/500/);
    now = 29_999;
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);
    now = 30_000;
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true }));
    await expect(api(url, options)).resolves.toEqual({ ok: true });
  });

  it("shares circuit state across clients created from the same parent", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({ ok: false }, 500, "Server Error")
    );
    const parent = createFetch({
      fetch: fetchMock,
      defaults: { retry: 0 },
    });
    const child = parent.create({
      circuitBreaker: breaker(),
    });
    const sibling = parent.create({});
    const grandchild = child.create({});
    const url = `${host()}/shared`;

    await expect(child(url)).rejects.toThrow(/500/);
    await expect(sibling(url, { circuitBreaker: breaker() })).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect(parent(url, { circuitBreaker: breaker() })).rejects.toThrow(
      /Circuit breaker is open/
    );
    await expect(grandchild(url)).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await expect(parent(url)).rejects.toThrow(/500/);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("shares state when a child replaces fetch", async () => {
    const parentFetch = vi.fn(async () =>
      jsonResponse({ ok: false }, 500, "Server Error")
    );
    const childFetch = vi.fn(async () => jsonResponse({ ok: true }));
    const parent = createFetch({ fetch: parentFetch });
    const child = parent.create({}, { fetch: childFetch });
    const url = `${host()}/replaced-fetch`;
    const options = { retry: 0, circuitBreaker: breaker() };

    await expect(parent(url, options)).rejects.toThrow(/500/);
    await expect(child(url, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(parentFetch).toHaveBeenCalledTimes(1);
    expect(childFetch).not.toHaveBeenCalled();
  });

  it("keeps separate registries for independent createFetch clients and $fetch", async () => {
    const { api, fetchMock } = makeApi();
    const url = `${host()}/isolated`;
    const options = { retry: 0, circuitBreaker: breaker() };
    await expect(api(url, options)).rejects.toThrow(/500/);
    await expect(api(url, options)).rejects.toThrow(/Circuit breaker is open/);

    const other = makeApi(async () => jsonResponse({ ok: true }));
    await expect(other.api(url, options)).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const globalFetch = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async () => jsonResponse({ ok: true }));
    const child = $fetch.create({});
    await expect($fetch(url, options)).resolves.toEqual({ ok: true });
    await expect(child.raw(url, options)).resolves.toMatchObject({
      status: 200,
    });
    expect(globalFetch).toHaveBeenCalledTimes(2);

    globalFetch.mockImplementation(async () =>
      jsonResponse({ ok: false }, 500, "Server Error")
    );
    const shared = `${host()}/global-share`;
    await expect($fetch(shared, options)).rejects.toThrow(/500/);
    await expect(child(shared, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(globalFetch).toHaveBeenCalledTimes(3);
  });

  it("supports raw responses and still stringifies json bodies", async () => {
    const { api, fetchMock } = makeApi(async () => jsonResponse({ ok: true }));
    const url = `${host()}/raw`;
    const response = await api.raw(url, {
      retry: 0,
      method: "POST",
      body: { n: 2 },
      circuitBreaker: true,
    });
    expect(response.status).toBe(200);
    expect(response._data).toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

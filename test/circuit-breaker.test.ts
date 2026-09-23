import { afterEach, describe, expect, it, vi } from "vitest";
import { $fetch, createFetch, FetchError, type $Fetch } from "../src/index.ts";
import type { CircuitBreakerOptions, FetchOptions } from "../src/types.ts";

const failureStatuses = [408, 409, 425, 429, 500, 502, 503, 504];
const neutralStatuses = [400, 401, 403, 404, 418, 422, 501];
const openFast: CircuitBreakerOptions = { threshold: 1, cooldown: 60_000 };

function jsonResponse(status = 200, body?: unknown): Response {
  return new Response(
    JSON.stringify(body === undefined ? { ok: true } : body),
    {
      status,
      headers: { "content-type": "application/json" },
    }
  );
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe("circuit breaker", () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;
  let client: $Fetch;
  let sequence = 0;

  afterEach(() => {
    vi.restoreAllMocks();
  });

  function installFetch(
    impl: typeof fetch = async () => jsonResponse(500)
  ): void {
    fetchMock = vi.fn(impl);
    client = createFetch({ fetch: fetchMock });
  }

  function origin(label = "api"): string {
    sequence += 1;
    return `http://${label}-${sequence}.test`;
  }

  function opts(
    circuitBreaker: FetchOptions["circuitBreaker"],
    extra?: FetchOptions
  ): FetchOptions {
    return { retry: 0, circuitBreaker, ...extra };
  }

  async function fail(url: string, options?: FetchOptions): Promise<unknown> {
    return client(url, options ?? opts(openFast)).then(
      () => {
        throw new Error("expected request to fail");
      },
      (error: unknown) => error
    );
  }

  it("does nothing when circuitBreaker is omitted or falsey", async () => {
    installFetch();
    const url = `${origin()}/item`;
    const disabled = [
      undefined,
      false,
      0,
      // eslint-disable-next-line unicorn/no-null
      null,
      "",
    ] as const;
    for (const circuitBreaker of disabled) {
      await expect(
        client(url, opts(circuitBreaker as FetchOptions["circuitBreaker"]))
      ).rejects.toThrow();
    }
    expect(fetchMock).toHaveBeenCalledTimes(disabled.length);
    await expect(
      client(url, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(disabled.length + 1);
    await expect(
      client(url, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(disabled.length + 1);
  });

  it("opens after the default threshold and uses the default cooldown", async () => {
    installFetch();
    const clock = mockNow();
    const url = `${origin()}/default`;
    const options = opts(true);

    for (let attempt = 0; attempt < 4; attempt++) {
      await expect(client(url, options)).rejects.toThrow();
    }
    expect(fetchMock).toHaveBeenCalledTimes(4);

    await expect(client(url, options)).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(5);

    await expect(client(url, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(5);

    clock.advance(29_999);
    await expect(client(url, options)).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(5);

    clock.advance(1);
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { recovered: true }));
    await expect(client(url, options)).resolves.toEqual({ recovered: true });
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("counts only configured failure statuses and ignores other 4xx/5xx", async () => {
    installFetch();
    const url = `${origin("listed")}/x`;
    const threshold = failureStatuses.length;
    for (const status of failureStatuses) {
      fetchMock.mockResolvedValueOnce(jsonResponse(status));
      await expect(
        client(url, opts({ threshold, cooldown: 60_000 }))
      ).rejects.toThrow();
    }
    await expect(
      client(url, opts({ threshold, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(threshold);

    const neutralURL = `${origin("neutral")}/x`;
    for (const status of neutralStatuses) {
      fetchMock.mockResolvedValueOnce(jsonResponse(status, { status }));
      await expect(client(neutralURL, opts(true))).rejects.toThrow();
    }
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    await expect(client(neutralURL, opts(true))).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(
      threshold + neutralStatuses.length + 1
    );
  });

  it("uses a custom failure status list without inheriting the defaults", async () => {
    installFetch();
    const url = `${origin("custom")}/x`;
    const circuitBreaker: CircuitBreakerOptions = {
      threshold: 1,
      cooldown: 60_000,
      failureStatusCodes: [404],
    };

    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    fetchMock.mockResolvedValueOnce(jsonResponse(200));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      ok: true,
    });

    fetchMock.mockResolvedValueOnce(jsonResponse(404));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    fetchMock.mockResolvedValueOnce(jsonResponse(200));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("resets consecutive failures after a successful logical request", async () => {
    installFetch();
    const url = `${origin("streak")}/x`;
    const circuitBreaker = { threshold: 3, cooldown: 60_000 };

    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    fetchMock.mockResolvedValueOnce(jsonResponse(502));
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { again: true }));

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      ok: true,
    });
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("does not reset or increment for rejected statuses outside the failure list", async () => {
    installFetch();
    const url = `${origin("neutral-streak")}/x`;
    const circuitBreaker = { threshold: 3, cooldown: 60_000 };

    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { missing: true }));
    fetchMock.mockResolvedValueOnce(jsonResponse(400, { bad: true }));
    fetchMock.mockResolvedValueOnce(jsonResponse(503));
    fetchMock.mockResolvedValueOnce(jsonResponse(504));
    fetchMock.mockResolvedValueOnce(jsonResponse(200));

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(5);
  });

  it("counts listed statuses when ignoreResponseError is set and does not treat other errors as success", async () => {
    installFetch();
    const url = `${origin("ignore")}/x`;
    const circuitBreaker = { threshold: 2, cooldown: 60_000 };

    fetchMock.mockResolvedValueOnce(jsonResponse(500, { err: 1 }));
    await expect(
      client(url, opts(circuitBreaker, { ignoreResponseError: true }))
    ).resolves.toEqual({ err: 1 });

    fetchMock.mockResolvedValueOnce(jsonResponse(404, { missing: true }));
    await expect(
      client(url, opts(circuitBreaker, { ignoreResponseError: true }))
    ).resolves.toEqual({ missing: true });

    fetchMock.mockResolvedValueOnce(jsonResponse(503, { err: 2 }));
    await expect(
      client(url, opts(circuitBreaker, { ignoreResponseError: true }))
    ).resolves.toEqual({ err: 2 });

    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("records one failure for a logical request that exhausts retries", async () => {
    installFetch();
    const url = `${origin("retry")}/x`;
    const circuitBreaker = { threshold: 2, cooldown: 60_000 };
    const seenFlags: unknown[] = [];

    await expect(
      client(
        url,
        opts(circuitBreaker, {
          retry: 4,
          onRequest(context) {
            seenFlags.push(
              (context.options as { __ofetchCircuitRetry?: boolean })
                .__ofetchCircuitRetry
            );
          },
        })
      )
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(5);
    expect(seenFlags).toEqual([
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
    ]);
    for (const call of fetchMock.mock.calls) {
      expect(call[1]).not.toHaveProperty("__ofetchCircuitRetry");
    }

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(6);
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  it("treats a retried logical request as success when a later attempt succeeds", async () => {
    installFetch(async () => jsonResponse(200, { ok: true }));
    const url = `${origin("retry-ok")}/x`;
    const circuitBreaker = { threshold: 1, cooldown: 60_000 };
    fetchMock
      .mockResolvedValueOnce(jsonResponse(500))
      .mockResolvedValueOnce(jsonResponse(500))
      .mockResolvedValueOnce(jsonResponse(200, { recovered: true }));

    await expect(
      client(url, opts(circuitBreaker, { retry: 2 }))
    ).resolves.toEqual({ recovered: true });
    expect(fetchMock).toHaveBeenCalledTimes(3);

    fetchMock.mockResolvedValueOnce(jsonResponse(200, { still: true }));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      still: true,
    });
  });

  it("counts network, parse, body, and hook failures once and does not retry parse or hook failures", async () => {
    installFetch(async () => {
      throw new Error("network down");
    });
    const networkURL = `${origin("network")}/x`;
    await expect(
      client(networkURL, opts({ threshold: 1, cooldown: 60_000 }, { retry: 3 }))
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(4);
    await expect(
      client(networkURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(4);

    installFetch(async () => jsonResponse(200));
    const parseURL = `${origin("parse")}/x`;
    fetchMock.mockResolvedValue(
      new Response("not-json", {
        status: 200,
        headers: { "content-type": "application/json" },
      })
    );
    await expect(
      client(parseURL, opts({ threshold: 1, cooldown: 60_000 }, { retry: 5 }))
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await expect(
      client(parseURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    installFetch(async () => jsonResponse(200));
    const parserURL = `${origin("parser")}/x`;
    await expect(
      client(
        parserURL,
        opts(
          { threshold: 1, cooldown: 60_000 },
          {
            retry: 5,
            parseResponse() {
              throw new Error("parse failed");
            },
          }
        )
      )
    ).rejects.toThrow(/parse failed/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await expect(
      client(parserURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);

    installFetch(async () => {
      const response = jsonResponse(200, { ok: true });
      await response.text();
      return response;
    });
    const bodyURL = `${origin("body")}/x`;
    await expect(
      client(bodyURL, opts({ threshold: 1, cooldown: 60_000 }, { retry: 4 }))
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await expect(
      client(bodyURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    installFetch(async () => jsonResponse(200, { ok: true }));
    const responseHookURL = `${origin("on-response")}/x`;
    await expect(
      client(
        responseHookURL,
        opts(
          { threshold: 1, cooldown: 60_000 },
          {
            retry: 4,
            onResponse() {
              throw new Error("onResponse failed");
            },
          }
        )
      )
    ).rejects.toThrow(/onResponse failed/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await expect(
      client(responseHookURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    installFetch(async () => jsonResponse(404, { missing: true }));
    const responseErrorURL = `${origin("on-response-error")}/x`;
    await expect(
      client(
        responseErrorURL,
        opts(
          { threshold: 1, cooldown: 60_000 },
          {
            retry: 4,
            onResponseError() {
              throw new Error("onResponseError failed");
            },
          }
        )
      )
    ).rejects.toThrow(/onResponseError failed/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await expect(
      client(responseErrorURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);

    installFetch(async () => {
      throw new Error("network down");
    });
    const requestErrorURL = `${origin("on-request-error")}/x`;
    await expect(
      client(
        requestErrorURL,
        opts(
          { threshold: 1, cooldown: 60_000 },
          {
            retry: 4,
            onRequestError() {
              throw new Error("onRequestError failed");
            },
          }
        )
      )
    ).rejects.toThrow(/onRequestError failed/);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await expect(
      client(requestErrorURL, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
  });

  it("does not count onRequest exceptions as circuit failures", async () => {
    installFetch(async () => jsonResponse(200, { ok: true }));
    const url = `${origin("on-request")}/x`;
    await expect(
      client(
        url,
        opts(
          { threshold: 1, cooldown: 60_000 },
          {
            onRequest() {
              throw new Error("onRequest failed");
            },
          }
        )
      )
    ).rejects.toThrow(/onRequest failed/);
    expect(fetchMock).not.toHaveBeenCalled();
    await expect(
      client(url, opts({ threshold: 1, cooldown: 60_000 }))
    ).resolves.toEqual({
      ok: true,
    });
  });

  it("fails fast without calling fetch and still runs onRequest", async () => {
    installFetch();
    const url = `${origin("open")}/x`;
    const circuitBreaker = { threshold: 1, cooldown: 60_000 };
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();

    fetchMock.mockImplementation(() => new Promise(() => {}));
    const onRequest = vi.fn();
    const error = await fail(
      url,
      opts(circuitBreaker, { retry: 5, onRequest })
    );
    expect(error).toBeInstanceOf(FetchError);
    expect((error as Error).message).toContain("Circuit breaker is open");
    expect(onRequest).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not let fast-fail rejections refresh the cooldown", async () => {
    installFetch();
    const clock = mockNow();
    const url = `${origin("cooldown")}/x`;
    const circuitBreaker = { threshold: 1, cooldown: 10_000 };

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    clock.advance(5000);
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);

    clock.advance(4999);
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    clock.advance(1);
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { probed: true }));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      probed: true,
    });
  });

  it("reopens from a failed half-open probe and restarts cooldown at that failure", async () => {
    installFetch();
    const clock = mockNow();
    const url = `${origin("reopen")}/x`;
    const circuitBreaker = { threshold: 1, cooldown: 10_000 };

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    clock.advance(10_000);
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(2);

    clock.advance(9999);
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);

    clock.advance(1);
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { back: true }));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      back: true,
    });
  });

  it("keeps half-open state for non-listed errors and closes it after a successful probe", async () => {
    installFetch();
    const clock = mockNow();
    const url = `${origin("half")}/x`;
    const circuitBreaker = { threshold: 3, cooldown: 5000 };

    fetchMock.mockResolvedValue(jsonResponse(500));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(3);

    clock.advance(5000);
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { missing: true }));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(4);

    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(5);
    await expect(client(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(fetchMock).toHaveBeenCalledTimes(5);

    clock.advance(5000);
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { closed: true }));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      closed: true,
    });
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { next: true }));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      next: true,
    });
  });

  it("limits concurrent half-open probes and holds a probe slot through retries", async () => {
    installFetch();
    const clock = mockNow();
    const url = `${origin("quota")}/x`;
    const circuitBreaker = {
      threshold: 1,
      cooldown: 1000,
      halfOpenMaxRequests: 2,
    };

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    clock.advance(1000);

    const pending: Array<(response: Response) => void> = [];
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          pending.push(resolve);
        })
    );

    const first = client(url, opts(circuitBreaker));
    const second = client(url, opts(circuitBreaker));
    const third = await fail(url, opts(circuitBreaker));
    expect(third).toBeInstanceOf(FetchError);
    expect((third as Error).message).toContain("Circuit breaker is open");
    expect(pending).toHaveLength(2);

    pending[0]!(jsonResponse(200, { a: 1 }));
    pending[1]!(jsonResponse(200, { b: 2 }));
    await expect(first).resolves.toEqual({ a: 1 });
    await expect(second).resolves.toEqual({ b: 2 });

    const heldURL = `${origin("held")}/x`;
    const heldBreaker = {
      threshold: 1,
      cooldown: 1000,
      halfOpenMaxRequests: 1,
    };
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    await expect(client(heldURL, opts(heldBreaker))).rejects.toThrow();
    clock.advance(1000);

    let probeCalls = 0;
    const extras: Array<Promise<unknown>> = [];
    fetchMock.mockImplementation(async () => {
      probeCalls += 1;
      if (probeCalls === 1) {
        return jsonResponse(500);
      }
      extras.push(client(heldURL, opts(heldBreaker)));
      return jsonResponse(200, { retried: true });
    });

    await expect(
      client(heldURL, opts(heldBreaker, { retry: 1 }))
    ).resolves.toEqual({ retried: true });
    expect(probeCalls).toBe(2);
    await expect(extras[0]).rejects.toThrow(/Circuit breaker is open/);
  });

  it("releases a half-open slot after a neutral probe so another probe can run", async () => {
    installFetch();
    const clock = mockNow();
    const url = `${origin("release")}/x`;
    const circuitBreaker = {
      threshold: 1,
      cooldown: 1000,
      halfOpenMaxRequests: 1,
    };

    await expect(client(url, opts(circuitBreaker))).rejects.toThrow();
    clock.advance(1000);

    const gate = deferred<Response>();
    fetchMock.mockImplementation(() => gate.promise);
    const probe = client(url, opts(circuitBreaker));
    await expect(fail(url, opts(circuitBreaker))).resolves.toMatchObject({
      message: expect.stringContaining("Circuit breaker is open"),
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);

    gate.resolve(jsonResponse(404, { missing: true }));
    await expect(probe).rejects.toThrow();

    fetchMock.mockResolvedValueOnce(jsonResponse(200, { next: true }));
    await expect(client(url, opts(circuitBreaker))).resolves.toEqual({
      next: true,
    });
  });

  it("keys state by origin across paths, input types, baseURL, and onRequest rewrites", async () => {
    installFetch(async () => jsonResponse(500));
    const root = origin("key");
    const circuitBreaker = { threshold: 2, cooldown: 60_000 };

    await expect(
      client(`${root}/a?x=1`, opts(circuitBreaker))
    ).rejects.toThrow();
    await expect(
      client(new URL(`${root}/b?y=2`), opts(circuitBreaker))
    ).rejects.toThrow();
    await expect(
      client(new Request(`${root}/c`), opts(circuitBreaker))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(2);

    const callsBeforePort = fetchMock.mock.calls.length;
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { port: true }));
    await expect(
      client(`${root}:8080/a`, opts(circuitBreaker))
    ).resolves.toEqual({
      port: true,
    });
    expect(fetchMock).toHaveBeenCalledTimes(callsBeforePort + 1);

    const base = origin("base");
    fetchMock.mockResolvedValue(jsonResponse(500));
    const callsBeforeBase = fetchMock.mock.calls.length;
    await expect(
      client("/items", opts(circuitBreaker, { baseURL: `${base}/v1/` }))
    ).rejects.toThrow();
    await expect(
      client("", opts(circuitBreaker, { baseURL: `${base}/v1` }))
    ).rejects.toThrow();
    await expect(
      client(`${base}/elsewhere`, opts(circuitBreaker))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(callsBeforeBase + 2);

    const dead = origin("dead");
    const healthy = origin("healthy");
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    await expect(
      client(`${dead}/down`, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow();

    const callsBeforeRewrite = fetchMock.mock.calls.length;
    const onRequest = vi.fn((context: { request: unknown }) => {
      context.request = `${dead}/rewritten`;
    });
    await expect(
      client(
        `${healthy}/start`,
        opts({ threshold: 1, cooldown: 60_000 }, { onRequest })
      )
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(onRequest).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(callsBeforeRewrite);

    let observed = "";
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { moved: true }));
    await expect(
      client(`${dead}/start`, {
        ...opts({ threshold: 1, cooldown: 60_000 }),
        async onRequest(context) {
          await Promise.resolve();
          context.request = "/live";
          context.options.baseURL = `${healthy}/api`;
        },
      })
    ).resolves.toEqual({ moved: true });
    observed = String(fetchMock.mock.calls.at(-1)?.[0]);
    expect(observed).toBe(`${healthy}/api/live`);
  });

  it("shares circuit state between .create() clients and not between roots", async () => {
    installFetch();
    const parent = createFetch({ fetch: fetchMock });
    const child = parent.create({
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 60_000 },
    });
    const grandchild = child.create({
      headers: { "x-child": "1" },
    });
    const sibling = parent.create({
      retry: 0,
      circuitBreaker: { threshold: 1, cooldown: 60_000 },
    });
    const url = `${origin("shared")}/x`;

    await expect(child(url)).rejects.toThrow();
    await expect(sibling(url)).rejects.toThrow(/Circuit breaker is open/);
    await expect(grandchild(url)).rejects.toThrow(/Circuit breaker is open/);
    await expect(
      parent(url, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow(/Circuit breaker is open/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const otherFetch = vi.fn(async () => jsonResponse(200, { other: true }));
    const retargeted = parent.create(
      { retry: 0, circuitBreaker: { threshold: 1, cooldown: 60_000 } },
      { fetch: otherFetch as typeof fetch }
    );
    await expect(retargeted(url)).rejects.toThrow(/Circuit breaker is open/);
    expect(otherFetch).not.toHaveBeenCalled();

    const separate = createFetch({
      fetch: fetchMock,
    });
    fetchMock.mockResolvedValueOnce(jsonResponse(500));
    await expect(
      separate(url, opts({ threshold: 1, cooldown: 60_000 }))
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("applies the same breaker to $fetch and clients created from it", async () => {
    const globalFetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(jsonResponse(500));
    const url = `${origin("global")}/x`;
    const circuitBreaker = { threshold: 1, cooldown: 60_000 };
    const child = $fetch.create({ retry: 0, circuitBreaker });

    await expect($fetch(url, opts(circuitBreaker))).rejects.toThrow();
    await expect(child(url)).rejects.toThrow(/Circuit breaker is open/);
    await expect($fetch.raw(url, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
    expect(globalFetch).toHaveBeenCalledTimes(1);

    const localFetch = vi.fn(async () => jsonResponse(200, { local: true }));
    const local = createFetch({ fetch: localFetch });
    await expect(local(url, opts(circuitBreaker))).resolves.toEqual({
      local: true,
    });
    expect(localFetch).toHaveBeenCalledTimes(1);

    globalFetch.mockResolvedValueOnce(jsonResponse(500, { raw: true }));
    const rawURL = `${origin("raw")}/x`;
    const response = await $fetch.raw(
      rawURL,
      opts({ threshold: 1, cooldown: 60_000 }, { ignoreResponseError: true })
    );
    expect(response.status).toBe(500);
    await expect($fetch(rawURL, opts(circuitBreaker))).rejects.toThrow(
      /Circuit breaker is open/
    );
  });
});

function mockNow(start = 1_000_000): {
  advance: (ms: number) => void;
} {
  let now = start;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  return {
    advance(ms: number) {
      now += ms;
    },
  };
}

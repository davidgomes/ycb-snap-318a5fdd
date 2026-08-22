import { afterEach, describe, expect, it, vi } from "vitest";
import { createFetch } from "../src/index.ts";

const origin = "https://example.test";
const otherOrigin = "https://other.example.test";

function response(status = 200, body = "ok"): Response {
  return new Response(body, {
    status,
    headers: {
      "content-type": "text/plain",
    },
  });
}

const circuitBreaker = {
  threshold: 2,
  cooldown: 100,
};

afterEach(() => {
  vi.useRealTimers();
});

describe("circuit breaker", () => {
  it("blocks an origin after its failure threshold", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch.mockRejectedValue(new Error("offline"));
    const client = createFetch({ fetch });

    for (let i = 0; i < 2; i++) {
      await expect(
        client(`${origin}/health`, {
          circuitBreaker,
          retry: false,
        })
      ).rejects.toThrow("offline");
    }

    await expect(
      client(`${origin}/health`, {
        circuitBreaker,
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("does not track requests when the option is falsey", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch.mockRejectedValue(new Error("offline"));
    const client = createFetch({ fetch });

    for (let i = 0; i < 3; i++) {
      await expect(
        client(`${origin}/health`, {
          circuitBreaker: false,
          retry: false,
        })
      ).rejects.toThrow("offline");
    }

    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("shares state across paths, request input types, and derived clients", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch.mockRejectedValue(new Error("offline"));
    const parent = createFetch({ fetch });
    const child = parent.create({ circuitBreaker });

    await expect(
      child(new URL(`${origin}/one`), { retry: false })
    ).rejects.toThrow("offline");
    await expect(
      parent(new Request(`${origin}/two`), {
        circuitBreaker,
        retry: false,
      })
    ).rejects.toThrow("offline");
    await expect(
      child(`${origin}/three`, { retry: false })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("uses the rewritten request origin after onRequest", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch.mockRejectedValue(new Error("offline"));
    const client = createFetch({ fetch });

    await expect(
      client(`${origin}/before-rewrite`, {
        circuitBreaker: { threshold: 1 },
        retry: false,
        onRequest(context) {
          context.request = `${otherOrigin}/rewritten`;
        },
      })
    ).rejects.toThrow("offline");

    await expect(
      client(`${otherOrigin}/direct`, {
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("resolves relative requests against baseURL", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch.mockRejectedValue(new Error("offline"));
    const client = createFetch({ fetch });

    await expect(
      client("/one", {
        baseURL: `${origin}/api`,
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("offline");
    await expect(
      client("/two", {
        baseURL: `${origin}/api`,
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("holds a half-open slot for the full logical request", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);

    let resolveProbe!: (value: Response) => void;
    const pendingProbe = new Promise<Response>((resolve) => {
      resolveProbe = resolve;
    });
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch
      .mockRejectedValueOnce(new Error("offline"))
      .mockImplementationOnce(() => pendingProbe)
      .mockResolvedValue(response());
    const client = createFetch({ fetch });
    const options = {
      circuitBreaker: {
        threshold: 1,
        cooldown: 100,
        halfOpenMaxRequests: 1,
      },
      retry: false,
    };

    await expect(client(`${origin}/health`, options)).rejects.toThrow("offline");
    vi.setSystemTime(100);

    const probe = client(`${origin}/health`, options);
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    await expect(client(`${origin}/health`, options)).rejects.toThrow(
      "Circuit breaker is open"
    );

    resolveProbe(response());
    await expect(probe).resolves.toBe("ok");
    await expect(client(`${origin}/health`, options)).resolves.toBe("ok");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("reopens a circuit after a failed half-open probe", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);

    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockRejectedValueOnce(new Error("offline"))
      .mockRejectedValueOnce(new Error("still offline"))
      .mockResolvedValue(response());
    const client = createFetch({ fetch });
    const options = {
      circuitBreaker: {
        threshold: 1,
        cooldown: 100,
      },
      retry: false,
    };

    await expect(client(`${origin}/health`, options)).rejects.toThrow(
      "offline"
    );
    vi.setSystemTime(100);
    await expect(client(`${origin}/health`, options)).rejects.toThrow(
      "still offline"
    );
    vi.setSystemTime(199);
    await expect(client(`${origin}/health`, options)).rejects.toThrow(
      "Circuit breaker is open"
    );
    vi.setSystemTime(200);
    await expect(client(`${origin}/health`, options)).resolves.toBe("ok");
  });

  it("records one failure for a logical request with retries", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>();
    fetch.mockRejectedValue(new Error("offline"));
    const client = createFetch({ fetch });

    await expect(
      client(`${origin}/health`, {
        circuitBreaker: { threshold: 1 },
        retry: 2,
      })
    ).rejects.toThrow("offline");
    expect(fetch).toHaveBeenCalledTimes(3);
    await expect(
      client(`${origin}/health`, {
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("does not count rejected non-listed statuses as success", async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(response(500, "listed"))
      .mockResolvedValueOnce(response(501, "not listed"))
      .mockResolvedValueOnce(response(500, "listed again"))
      .mockResolvedValue(response());
    const client = createFetch({ fetch });
    const options = {
      circuitBreaker: {
        threshold: 2,
        failureStatusCodes: [500],
      },
      retry: false,
    };

    await expect(client(`${origin}/health`, options)).rejects.toThrow();
    await expect(client(`${origin}/health`, options)).rejects.toThrow();
    await expect(client(`${origin}/health`, options)).rejects.toThrow();
    await expect(client(`${origin}/health`, options)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("counts listed statuses when response errors are ignored", async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(500));
    const client = createFetch({ fetch });

    await expect(
      client(`${origin}/health`, {
        circuitBreaker: { threshold: 1 },
        ignoreResponseError: true,
        retry: false,
      })
    ).resolves.toBe("ok");
    await expect(
      client(`${origin}/health`, {
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("records body-read and hook errors", async () => {
    const bodyReadError = new Error("body read failed");
    const body = response();
    vi.spyOn(body, "text").mockRejectedValue(bodyReadError);
    const bodyFetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(body);
    const bodyClient = createFetch({ fetch: bodyFetch });

    await expect(
      bodyClient(`${origin}/body`, {
        circuitBreaker: { threshold: 1 },
        responseType: "text",
        retry: false,
      })
    ).rejects.toThrow("body read failed");
    await expect(
      bodyClient(`${origin}/body`, {
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");

    const hookFetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      response()
    );
    const hookClient = createFetch({ fetch: hookFetch });
    await expect(
      hookClient(`${origin}/hook`, {
        circuitBreaker: { threshold: 1 },
        retry: 3,
        onResponse: () => {
          throw new Error("response hook failed");
        },
      })
    ).rejects.toThrow("response hook failed");
    expect(hookFetch).toHaveBeenCalledTimes(1);
    await expect(
      hookClient(`${origin}/hook`, {
        circuitBreaker: { threshold: 1 },
        retry: false,
      })
    ).rejects.toThrow("Circuit breaker is open");
  });
});

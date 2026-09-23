import { describe, it, expect, vi, afterEach } from "vitest";
import { createFetch } from "../src/index.ts";

function mockFetch(statuses: (number | Error)[]) {
  const calls: string[] = [];
  const fetch = vi.fn(async (input: any) => {
    calls.push(typeof input === "string" ? input : input.url || String(input));
    const next = statuses.length > 1 ? statuses.shift()! : statuses[0];
    if (next instanceof Error) {
      throw next;
    }
    return new Response(JSON.stringify({ ok: true }), {
      status: next,
      headers: { "content-type": "application/json" },
    });
  });
  return {
    fetch: fetch as unknown as typeof globalThis.fetch,
    calls,
    spy: fetch,
  };
}

describe("circuit breaker", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("opens after threshold, half-opens after cooldown, closes on success", async () => {
    vi.useFakeTimers();
    const m = mockFetch([500, 500, 200]);
    const $fetch = createFetch({ fetch: m.fetch }).create({
      baseURL: "http://a.test",
      retry: 0,
      circuitBreaker: { threshold: 2, cooldown: 1000 },
    });
    await expect($fetch("/x")).rejects.toThrow();
    await expect($fetch("/y")).rejects.toThrow();
    await expect($fetch("/z")).rejects.toThrow("Circuit breaker is open");
    expect(m.spy).toHaveBeenCalledTimes(2);
    vi.advanceTimersByTime(1000);
    await expect($fetch("/z")).resolves.toEqual({ ok: true });
    await expect($fetch("/z")).resolves.toEqual({ ok: true });
  });

  it("counts one failure per logical request with retries", async () => {
    const m = mockFetch([503]);
    const $fetch = createFetch({ fetch: m.fetch });
    const opts = { retry: 2, circuitBreaker: { threshold: 2, cooldown: 1e6 } };
    await expect($fetch("http://b.test", opts)).rejects.toThrow();
    expect(m.spy).toHaveBeenCalledTimes(3);
    await expect($fetch("http://b.test/other", opts)).rejects.toThrow();
    await expect($fetch("http://b.test", opts)).rejects.toThrow(
      "Circuit breaker is open"
    );
    expect(m.spy).toHaveBeenCalledTimes(6);
  });

  it("ignores non-listed statuses and counts listed ones with ignoreResponseError", async () => {
    const m = mockFetch([404, 404, 500]);
    const $fetch = createFetch({ fetch: m.fetch });
    const cb = { threshold: 1, cooldown: 1e6 };
    await expect(
      $fetch("http://c.test", { circuitBreaker: cb })
    ).rejects.toThrow();
    await expect(
      $fetch("http://c.test", { circuitBreaker: cb })
    ).rejects.toThrow();
    await $fetch("http://c.test", {
      circuitBreaker: cb,
      ignoreResponseError: true,
    });
    await expect(
      $fetch("http://c.test", { circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
  });

  it("limits half-open probes and reopens on failed probe", async () => {
    vi.useFakeTimers();
    let release!: () => void;
    let calls = 0;
    const fetch = (async () => {
      calls++;
      if (calls === 2) {
        await new Promise<void>((r) => (release = r));
      }
      throw new Error("net");
    }) as unknown as typeof globalThis.fetch;
    const $fetch = createFetch({ fetch });
    const cb = { threshold: 1, cooldown: 100 };
    await expect(
      $fetch("http://d.test", { circuitBreaker: cb, retry: 0 })
    ).rejects.toThrow("net");
    vi.advanceTimersByTime(100);
    const probe = $fetch("http://d.test", { circuitBreaker: cb, retry: 0 });
    await vi.waitFor(() => expect(calls).toBe(2));
    await expect(
      $fetch("http://d.test", { circuitBreaker: cb, retry: 0 })
    ).rejects.toThrow("Circuit breaker is open");
    release();
    await expect(probe).rejects.toThrow("net");
    expect(calls).toBe(2);
    vi.advanceTimersByTime(99);
    await expect(
      $fetch("http://d.test", { circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
  });

  it("keys by origin after onRequest rewrite and supports Request/URL", async () => {
    const m = mockFetch([500]);
    const $fetch = createFetch({ fetch: m.fetch });
    const cb = { threshold: 1, cooldown: 1e6 };
    await expect(
      $fetch("http://e.test", {
        retry: 0,
        circuitBreaker: cb,
        onRequest(ctx) {
          ctx.request = "http://f.test/p";
        },
      })
    ).rejects.toThrow();
    await expect(
      $fetch(new Request("http://f.test/q"), { circuitBreaker: cb })
    ).rejects.toThrow("Circuit breaker is open");
    await expect(
      $fetch(new URL("http://e.test/q") as any, {
        circuitBreaker: cb,
        retry: 0,
      })
    ).rejects.not.toThrow("Circuit breaker is open");
  });

  it("counts hook and parse errors, and is disabled by default", async () => {
    const m = mockFetch([200]);
    const $fetch = createFetch({ fetch: m.fetch });
    await expect(
      $fetch("http://g.test", {
        circuitBreaker: { threshold: 1, cooldown: 1e6 },
        parseResponse: () => {
          throw new Error("parse");
        },
      })
    ).rejects.toThrow("parse");
    await expect(
      $fetch("http://g.test", { circuitBreaker: true })
    ).rejects.toThrow("Circuit breaker is open");
    await expect($fetch("http://g.test")).resolves.toEqual({ ok: true });
  });
});

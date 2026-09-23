import type { CircuitBreakerOptions, FetchRequest } from "./types.ts";

export type CircuitState = "closed" | "open" | "half-open";

export interface ResolvedCircuitBreakerOptions {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: number[];
}

interface CircuitEntry {
  state: CircuitState;
  failures: number;
  openedAt: number;
  probes: number;
  generation: number;
}

export interface CircuitTicket {
  key: string;
  options: ResolvedCircuitBreakerOptions;
  probe: boolean;
  generation: number;
}

export type CircuitStore = Map<string, CircuitEntry>;

const defaultFailureStatusCodes = [408, 409, 425, 429, 500, 502, 503, 504];

export function resolveCircuitBreakerOptions(
  input: boolean | CircuitBreakerOptions | undefined | null
): ResolvedCircuitBreakerOptions | undefined {
  if (!input) {
    return undefined;
  }
  const opts: Partial<CircuitBreakerOptions> = input === true ? {} : input;
  return {
    threshold: opts.threshold ?? 5,
    cooldown: opts.cooldown ?? 30_000,
    halfOpenMaxRequests: opts.halfOpenMaxRequests ?? 1,
    failureStatusCodes: opts.failureStatusCodes ?? defaultFailureStatusCodes,
  };
}

export function resolveRequestOrigin(
  request: FetchRequest | URL
): string | undefined {
  try {
    if (request instanceof URL) {
      return request.origin;
    }
    const url = typeof request === "string" ? request : request.url;
    return new URL(url, (globalThis as any).location?.href).origin;
  } catch {
    return undefined;
  }
}

export function acquireCircuit(
  store: CircuitStore,
  key: string,
  options: ResolvedCircuitBreakerOptions
): CircuitTicket | undefined {
  let entry = store.get(key);
  if (!entry) {
    entry = {
      state: "closed",
      failures: 0,
      openedAt: 0,
      probes: 0,
      generation: 0,
    };
    store.set(key, entry);
  }

  if (
    entry.state === "open" &&
    Date.now() - entry.openedAt >= options.cooldown
  ) {
    entry.state = "half-open";
    entry.probes = 0;
    entry.generation++;
  }

  if (entry.state === "open") {
    return undefined;
  }

  if (entry.state === "half-open") {
    if (entry.probes >= options.halfOpenMaxRequests) {
      return undefined;
    }
    entry.probes++;
    return { key, options, probe: true, generation: entry.generation };
  }

  return { key, options, probe: false, generation: entry.generation };
}

export function releaseCircuit(
  store: CircuitStore,
  ticket: CircuitTicket,
  outcome: "success" | "failure" | "neutral"
): void {
  const entry = store.get(ticket.key);
  if (!entry) {
    return;
  }

  const isActiveProbe =
    ticket.probe &&
    entry.state === "half-open" &&
    entry.generation === ticket.generation;

  if (isActiveProbe) {
    entry.probes = Math.max(0, entry.probes - 1);
  }

  if (outcome === "success") {
    entry.failures = 0;
    if (isActiveProbe) {
      entry.state = "closed";
      entry.probes = 0;
      entry.generation++;
    }
  } else if (outcome === "failure") {
    entry.failures++;
    if (
      isActiveProbe ||
      (entry.state === "closed" && entry.failures >= ticket.options.threshold)
    ) {
      entry.state = "open";
      entry.openedAt = Date.now();
      entry.probes = 0;
      entry.generation++;
    }
  }
}

import type { FetchOptions, FetchRequest } from "./types.ts";

export const CIRCUIT_CONTINUATION = Symbol("ofetch.circuitContinuation");

const DEFAULT_FAILURE_STATUS_CODES = [408, 409, 425, 429, 500, 502, 503, 504];

export interface ResolvedCircuitBreaker {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: Set<number>;
}

interface CircuitEntry {
  state: "closed" | "open" | "half-open";
  failures: number;
  openedAt: number;
  inflight: number;
}

export type CircuitRegistry = Map<string, CircuitEntry>;

export interface CircuitHandle {
  entry: CircuitEntry;
  config: ResolvedCircuitBreaker;
  probe: boolean;
}

export function resolveCircuitConfig(
  option: FetchOptions["circuitBreaker"]
): ResolvedCircuitBreaker | undefined {
  if (!option) {
    return undefined;
  }

  const value = option === true ? {} : option;
  return {
    threshold: value.threshold ?? 5,
    cooldown: value.cooldown ?? 30_000,
    halfOpenMaxRequests: value.halfOpenMaxRequests ?? 1,
    failureStatusCodes: new Set(
      value.failureStatusCodes ?? DEFAULT_FAILURE_STATUS_CODES
    ),
  };
}

export function isCircuitContinuation(options: FetchOptions): boolean {
  return Boolean((options as Record<symbol, unknown>)[CIRCUIT_CONTINUATION]);
}

export function resolveRequestOrigin(request: FetchRequest): string {
  try {
    if (typeof request === "string") {
      return new URL(request).origin;
    }
    if (request instanceof URL) {
      return request.origin;
    }
    return new URL(request.url).origin;
  } catch {
    return "";
  }
}

export function gateCircuit(
  registry: CircuitRegistry,
  origin: string,
  config: ResolvedCircuitBreaker
): CircuitHandle {
  let entry = registry.get(origin);
  if (!entry) {
    entry = { state: "closed", failures: 0, openedAt: 0, inflight: 0 };
    registry.set(origin, entry);
  }

  if (
    entry.state === "open" &&
    Date.now() - entry.openedAt >= config.cooldown
  ) {
    entry.state = "half-open";
  }

  if (entry.state === "open") {
    throw new Error("Circuit breaker is open");
  }

  if (entry.state === "half-open") {
    if (entry.inflight >= config.halfOpenMaxRequests) {
      throw new Error("Circuit breaker is open");
    }
    entry.inflight++;
    return { entry, config, probe: true };
  }

  return { entry, config, probe: false };
}

export function recordCircuitSuccess(handle: CircuitHandle): void {
  handle.entry.failures = 0;
  if (handle.probe) {
    handle.entry.state = "closed";
  }
}

export function recordCircuitFailure(handle: CircuitHandle): void {
  const { entry, config } = handle;
  entry.failures++;
  if (handle.probe || entry.failures >= config.threshold) {
    entry.state = "open";
    entry.openedAt = Date.now();
  }
}

export function settleCircuitError(
  handle: CircuitHandle,
  error: unknown
): void {
  const status = readErrorStatus(error);
  if (
    typeof status === "number" &&
    !handle.config.failureStatusCodes.has(status)
  ) {
    return;
  }
  recordCircuitFailure(handle);
}

export function releaseCircuit(handle: CircuitHandle): void {
  if (!handle.probe) {
    return;
  }
  handle.probe = false;
  handle.entry.inflight = Math.max(0, handle.entry.inflight - 1);
}

function readErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== "object") {
    return undefined;
  }
  const value = error as { statusCode?: unknown; status?: unknown };
  if (typeof value.statusCode === "number") {
    return value.statusCode;
  }
  if (typeof value.status === "number") {
    return value.status;
  }
  return undefined;
}

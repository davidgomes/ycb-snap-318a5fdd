import type { FetchOptions, FetchRequest } from "./types.ts";

/** Status codes that count as circuit failures when `circuitBreaker` is `true`. */
export const DEFAULT_CIRCUIT_FAILURE_STATUS_CODES = [
  408, // Request Timeout
  409, // Conflict
  425, // Too Early
  429, // Too Many Requests
  500, // Internal Server Error
  502, // Bad Gateway
  503, // Service Unavailable
  504, // Gateway Timeout
] as const;

const DEFAULT_THRESHOLD = 5;
const DEFAULT_COOLDOWN = 30_000;
const DEFAULT_HALF_OPEN_MAX_REQUESTS = 1;

export type CircuitOutcome = "success" | "failure" | "neutral";

interface CircuitConfig {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: ReadonlySet<number>;
}

interface CircuitEntry {
  state: "closed" | "open" | "half-open";
  consecutiveFailures: number;
  openedAt: number;
  halfOpenInFlight: number;
  generation: number;
}

export type CircuitStore = Map<string, CircuitEntry>;

export interface CircuitHandle {
  blocked: boolean;
  failureStatusCodes: ReadonlySet<number>;
  complete(outcome: CircuitOutcome): void;
}

const circuitContinuations = new WeakMap<object, true>();

/** Marks an internal retry as part of an in-flight logical request. */
export function markCircuitContinuation(options: object): void {
  circuitContinuations.set(options, true);
}

export function isCircuitContinuation(options: object): boolean {
  return circuitContinuations.has(options);
}

export function createCircuitStore(): CircuitStore {
  return new Map();
}

/**
 * Admit one logical request into the origin circuit.
 * Returns undefined when the breaker is disabled or the origin cannot be resolved.
 */
export function admitCircuit(
  store: CircuitStore,
  request: FetchRequest,
  circuitBreaker: FetchOptions["circuitBreaker"]
): CircuitHandle | undefined {
  if (!circuitBreaker) {
    return undefined;
  }

  const origin = resolveCircuitOrigin(request);
  if (!origin) {
    return undefined;
  }

  const config = resolveCircuitConfig(circuitBreaker);
  const now = Date.now();
  let entry = store.get(origin);
  if (!entry) {
    entry = {
      state: "closed",
      consecutiveFailures: 0,
      openedAt: 0,
      halfOpenInFlight: 0,
      generation: 0,
    };
    store.set(origin, entry);
  }

  if (entry.state === "open") {
    if (now - entry.openedAt >= config.cooldown) {
      entry.state = "half-open";
      entry.halfOpenInFlight = 0;
      entry.generation += 1;
    } else {
      return blockedHandle(config);
    }
  }

  if (entry.state === "half-open") {
    if (entry.halfOpenInFlight >= config.halfOpenMaxRequests) {
      return blockedHandle(config);
    }
    entry.halfOpenInFlight += 1;
    const generation = entry.generation;
    return {
      blocked: false,
      failureStatusCodes: config.failureStatusCodes,
      complete(outcome: CircuitOutcome) {
        finishProbe(store, origin, generation, outcome, config);
      },
    };
  }

  return {
    blocked: false,
    failureStatusCodes: config.failureStatusCodes,
    complete(outcome: CircuitOutcome) {
      finishClosed(store, origin, outcome, config);
    },
  };
}

export function classifyCircuitResponse(
  status: number | undefined,
  failureStatusCodes: ReadonlySet<number>
): CircuitOutcome {
  // A returned response was not rejected. Listed statuses still fail the
  // circuit when `ignoreResponseError` is set; every other status is a success.
  if (typeof status === "number" && failureStatusCodes.has(status)) {
    return "failure";
  }
  return "success";
}

export function classifyCircuitError(
  error: unknown,
  failureStatusCodes: ReadonlySet<number>
): CircuitOutcome {
  const status = readErrorStatus(error);
  if (status !== undefined && status >= 400 && status < 600) {
    return failureStatusCodes.has(status) ? "failure" : "neutral";
  }
  return "failure";
}

function blockedHandle(config: CircuitConfig): CircuitHandle {
  return {
    blocked: true,
    failureStatusCodes: config.failureStatusCodes,
    complete() {
      // Fast-fail does not change circuit counters.
    },
  };
}

function finishClosed(
  store: CircuitStore,
  origin: string,
  outcome: CircuitOutcome,
  config: CircuitConfig
): void {
  const entry = store.get(origin);
  if (!entry || entry.state !== "closed") {
    return;
  }

  if (outcome === "success") {
    entry.consecutiveFailures = 0;
    return;
  }

  if (outcome === "failure") {
    entry.consecutiveFailures += 1;
    if (entry.consecutiveFailures >= config.threshold) {
      entry.state = "open";
      entry.openedAt = Date.now();
    }
  }
}

function finishProbe(
  store: CircuitStore,
  origin: string,
  generation: number,
  outcome: CircuitOutcome,
  config: CircuitConfig
): void {
  const entry = store.get(origin);
  if (!entry || entry.generation !== generation) {
    return;
  }

  entry.halfOpenInFlight = Math.max(0, entry.halfOpenInFlight - 1);

  if (entry.state !== "half-open") {
    return;
  }

  if (outcome === "success") {
    entry.state = "closed";
    entry.consecutiveFailures = 0;
    entry.halfOpenInFlight = 0;
    entry.generation += 1;
    return;
  }

  if (outcome === "failure") {
    entry.state = "open";
    entry.openedAt = Date.now();
    entry.consecutiveFailures = config.threshold;
    entry.halfOpenInFlight = 0;
    entry.generation += 1;
  }
}

function resolveCircuitConfig(
  circuitBreaker:
    | true
    | Exclude<NonNullable<FetchOptions["circuitBreaker"]>, boolean>
): CircuitConfig {
  if (circuitBreaker === true) {
    return {
      threshold: DEFAULT_THRESHOLD,
      cooldown: DEFAULT_COOLDOWN,
      halfOpenMaxRequests: DEFAULT_HALF_OPEN_MAX_REQUESTS,
      failureStatusCodes: new Set(DEFAULT_CIRCUIT_FAILURE_STATUS_CODES),
    };
  }

  return {
    threshold: circuitBreaker.threshold ?? DEFAULT_THRESHOLD,
    cooldown: circuitBreaker.cooldown ?? DEFAULT_COOLDOWN,
    halfOpenMaxRequests:
      circuitBreaker.halfOpenMaxRequests ?? DEFAULT_HALF_OPEN_MAX_REQUESTS,
    failureStatusCodes: new Set(
      circuitBreaker.failureStatusCodes ?? DEFAULT_CIRCUIT_FAILURE_STATUS_CODES
    ),
  };
}

function resolveCircuitOrigin(request: FetchRequest): string | undefined {
  try {
    if (typeof request === "string") {
      return new URL(request).origin;
    }
    if (typeof URL !== "undefined" && request instanceof URL) {
      return request.origin;
    }
    if (typeof Request !== "undefined" && request instanceof Request) {
      return new URL(request.url).origin;
    }
    if (
      request &&
      typeof request === "object" &&
      "url" in request &&
      typeof request.url === "string"
    ) {
      return new URL(request.url).origin;
    }
  } catch {
    return undefined;
  }
  return undefined;
}

function readErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== "object") {
    return undefined;
  }
  const candidate = error as { status?: unknown; statusCode?: unknown };
  if (
    typeof candidate.status === "number" &&
    Number.isFinite(candidate.status)
  ) {
    return candidate.status;
  }
  if (
    typeof candidate.statusCode === "number" &&
    Number.isFinite(candidate.statusCode)
  ) {
    return candidate.statusCode;
  }
  return undefined;
}

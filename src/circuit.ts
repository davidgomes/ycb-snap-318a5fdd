import type { CircuitBreakerOptions, FetchRequest } from "./types.ts";

const DEFAULT_FAILURE_STATUS_CODES = [
  408, 409, 425, 429, 500, 502, 503, 504,
] as const;

const DEFAULT_FAILURE_STATUS_SET: ReadonlySet<number> = new Set(
  DEFAULT_FAILURE_STATUS_CODES
);

export interface ResolvedCircuitBreaker {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: ReadonlySet<number>;
}

export type CircuitStateName = "closed" | "open" | "half-open";

export interface CircuitEntry {
  state: CircuitStateName;
  failures: number;
  openedAt: number;
  halfOpenInFlight: number;
}

export type CircuitOutcome = "success" | "failure" | "neutral";

export interface CircuitAdmission {
  allowed: boolean;
  release: () => void;
}

export function resolveCircuitBreaker(
  option: boolean | CircuitBreakerOptions | null | undefined
): ResolvedCircuitBreaker | undefined {
  if (!option) {
    return undefined;
  }

  const config = option === true ? {} : option;
  const codes = config.failureStatusCodes;

  return {
    threshold: config.threshold ?? 5,
    cooldown: config.cooldown ?? 30_000,
    halfOpenMaxRequests: config.halfOpenMaxRequests ?? 1,
    failureStatusCodes: codes ? new Set(codes) : DEFAULT_FAILURE_STATUS_SET,
  };
}

export function getRequestOrigin(request: FetchRequest): string | undefined {
  try {
    if (typeof request === "string") {
      return normalizeOrigin(new URL(request).origin);
    }

    if (typeof request !== "object" || request === null) {
      return undefined;
    }

    if (isURL(request)) {
      return normalizeOrigin(request.origin);
    }

    if (typeof request.url === "string") {
      return normalizeOrigin(new URL(request.url).origin);
    }
  } catch {
    return undefined;
  }

  return undefined;
}

function isURL(value: object): value is URL {
  if (typeof URL !== "undefined" && value instanceof URL) {
    return true;
  }

  return (
    "href" in value &&
    "origin" in value &&
    !("method" in value) &&
    typeof (value as URL).origin === "string"
  );
}

function normalizeOrigin(origin: string): string | undefined {
  if (!origin || origin === "null") {
    return undefined;
  }
  return origin;
}

export function admitCircuit(
  states: Map<string, CircuitEntry>,
  origin: string,
  config: ResolvedCircuitBreaker
): CircuitAdmission {
  const entry = states.get(origin);
  if (!entry || entry.state === "closed") {
    return { allowed: true, release() {} };
  }

  const now = Date.now();
  if (entry.state === "open") {
    if (now - entry.openedAt < config.cooldown) {
      return { allowed: false, release() {} };
    }
    entry.state = "half-open";
  }

  if (entry.halfOpenInFlight >= config.halfOpenMaxRequests) {
    return { allowed: false, release() {} };
  }

  entry.halfOpenInFlight += 1;
  let released = false;
  return {
    allowed: true,
    release() {
      if (released) {
        return;
      }
      released = true;
      entry.halfOpenInFlight = Math.max(0, entry.halfOpenInFlight - 1);
    },
  };
}

export function recordCircuitOutcome(
  states: Map<string, CircuitEntry>,
  origin: string,
  config: ResolvedCircuitBreaker,
  outcome: CircuitOutcome
): void {
  if (outcome === "neutral") {
    return;
  }

  if (outcome === "success") {
    const entry = states.get(origin);
    if (!entry) {
      return;
    }
    entry.failures = 0;
    entry.state = "closed";
    return;
  }

  const entry = getOrCreate(states, origin);
  entry.failures += 1;
  const openFromHalfOpen = entry.state === "half-open";
  const openFromClosed =
    entry.state === "closed" && entry.failures >= config.threshold;
  if (openFromHalfOpen || openFromClosed) {
    entry.state = "open";
    entry.openedAt = Date.now();
  }
}

export function classifyCircuitResponse(
  status: number,
  failureStatusCodes: ReadonlySet<number>
): CircuitOutcome {
  if (failureStatusCodes.has(status)) {
    return "failure";
  }
  if (status >= 400 && status < 600) {
    return "neutral";
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

function readErrorStatus(error: unknown): number | undefined {
  if (typeof error !== "object" || error === null) {
    return undefined;
  }

  const record = error as {
    status?: unknown;
    response?: { status?: unknown };
  };
  if (typeof record.status === "number" && Number.isFinite(record.status)) {
    return record.status;
  }
  if (
    record.response &&
    typeof record.response.status === "number" &&
    Number.isFinite(record.response.status)
  ) {
    return record.response.status;
  }
  return undefined;
}

function getOrCreate(
  states: Map<string, CircuitEntry>,
  origin: string
): CircuitEntry {
  const existing = states.get(origin);
  if (existing) {
    return existing;
  }
  const created: CircuitEntry = {
    state: "closed",
    failures: 0,
    openedAt: 0,
    halfOpenInFlight: 0,
  };
  states.set(origin, created);
  return created;
}

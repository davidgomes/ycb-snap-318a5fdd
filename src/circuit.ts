import type { FetchRequest } from "./types.ts";

export const DEFAULT_CIRCUIT_THRESHOLD = 5;
export const DEFAULT_CIRCUIT_COOLDOWN = 30_000;
export const DEFAULT_CIRCUIT_HALF_OPEN_MAX_REQUESTS = 1;
export const DEFAULT_CIRCUIT_FAILURE_STATUS_CODES: readonly number[] = [
  408, 409, 425, 429, 500, 502, 503, 504,
];

export interface CircuitBreakerConfig {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests?: number;
  failureStatusCodes?: number[];
}

export type CircuitBreakerOption = boolean | CircuitBreakerConfig;

export interface ResolvedCircuitBreaker {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: Set<number>;
}

export type CircuitStatus = "closed" | "open" | "half-open";

export interface CircuitOriginState {
  status: CircuitStatus;
  consecutiveFailures: number;
  openedAt: number;
  halfOpenInFlight: number;
}

export type CircuitStore = Map<string, CircuitOriginState>;

export interface CircuitAdmission {
  allowed: boolean;
  origin: string;
  isProbe: boolean;
}

export function createCircuitStore(): CircuitStore {
  return new Map();
}

export function resolveCircuitBreaker(
  input: CircuitBreakerOption | undefined
): ResolvedCircuitBreaker | undefined {
  if (!input) {
    return undefined;
  }

  const config: Partial<CircuitBreakerConfig> = input === true ? {} : input;

  return {
    threshold: config.threshold ?? DEFAULT_CIRCUIT_THRESHOLD,
    cooldown: config.cooldown ?? DEFAULT_CIRCUIT_COOLDOWN,
    halfOpenMaxRequests:
      config.halfOpenMaxRequests ?? DEFAULT_CIRCUIT_HALF_OPEN_MAX_REQUESTS,
    failureStatusCodes: new Set(
      config.failureStatusCodes ?? DEFAULT_CIRCUIT_FAILURE_STATUS_CODES
    ),
  };
}

export function resolveRequestOrigin(
  request: FetchRequest
): string | undefined {
  try {
    if (typeof request === "string") {
      return new URL(request).origin;
    }
    if (request instanceof URL) {
      return request.origin;
    }
    const url =
      request instanceof Request
        ? request.url
        : ((request as { url?: unknown }).url as string | undefined);
    if (typeof url === "string" && url) {
      return new URL(url).origin;
    }
  } catch {
    return undefined;
  }
}

function getOriginState(
  store: CircuitStore,
  origin: string
): CircuitOriginState {
  let state = store.get(origin);
  if (!state) {
    state = {
      status: "closed",
      consecutiveFailures: 0,
      openedAt: 0,
      halfOpenInFlight: 0,
    };
    store.set(origin, state);
  }
  return state;
}

export function admitCircuitRequest(
  store: CircuitStore,
  origin: string,
  options: ResolvedCircuitBreaker
): CircuitAdmission {
  const state = getOriginState(store, origin);
  const now = Date.now();

  if (state.status === "open") {
    if (now - state.openedAt >= options.cooldown) {
      state.status = "half-open";
      state.halfOpenInFlight = 0;
    } else {
      return { allowed: false, origin, isProbe: false };
    }
  }

  if (state.status === "half-open") {
    if (state.halfOpenInFlight >= options.halfOpenMaxRequests) {
      return { allowed: false, origin, isProbe: false };
    }
    state.halfOpenInFlight++;
    return { allowed: true, origin, isProbe: true };
  }

  return { allowed: true, origin, isProbe: false };
}

export function releaseCircuitProbe(store: CircuitStore, origin: string): void {
  const state = store.get(origin);
  if (state && state.halfOpenInFlight > 0) {
    state.halfOpenInFlight--;
  }
}

export function recordCircuitSuccess(
  store: CircuitStore,
  origin: string
): void {
  const state = getOriginState(store, origin);
  state.status = "closed";
  state.consecutiveFailures = 0;
}

export function recordCircuitFailure(
  store: CircuitStore,
  origin: string,
  options: ResolvedCircuitBreaker
): void {
  const state = getOriginState(store, origin);
  state.consecutiveFailures++;

  if (
    state.status === "half-open" ||
    state.consecutiveFailures >= options.threshold
  ) {
    state.status = "open";
    state.openedAt = Date.now();
  }
}

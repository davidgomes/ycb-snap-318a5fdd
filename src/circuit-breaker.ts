import type { CircuitBreakerOptions, FetchRequest } from "./types.ts";

export const DEFAULT_CIRCUIT_FAILURE_STATUS_CODES = [
  408, 409, 425, 429, 500, 502, 503, 504,
] as const;

export interface NormalizedCircuitBreaker {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: readonly number[];
}

export type CircuitPhase = "closed" | "open" | "half-open";

export interface CircuitState {
  phase: CircuitPhase;
  consecutiveFailures: number;
  openedAt: number;
  halfOpenInFlight: number;
}

export type CircuitRegistry = Map<string, CircuitState>;

export type CircuitOutcome = "success" | "failure" | "neutral";

export interface CircuitAttempt {
  settled: boolean;
  probe: boolean;
  origin: string;
  config: NormalizedCircuitBreaker;
}

/** Carried on retry options so one logical request keeps a single admission. */
export const CIRCUIT_ATTEMPT: unique symbol = Symbol("ofetch.circuitAttempt");

/** Marks FetchErrors produced by response-status rejection (not hook/parse/network). */
export const STATUS_REJECTION: unique symbol = Symbol("ofetch.statusRejection");

const registries = new WeakMap<object, CircuitRegistry>();

export function createCircuitRegistry(): CircuitRegistry {
  return new Map();
}

export function getCircuitRegistry(options: object): CircuitRegistry {
  let registry = registries.get(options);
  if (!registry) {
    registry = createCircuitRegistry();
    registries.set(options, registry);
  }
  return registry;
}

export function shareCircuitRegistry(
  options: object,
  registry: CircuitRegistry
): void {
  registries.set(options, registry);
}

export function normalizeCircuitBreaker(
  option: boolean | CircuitBreakerOptions | null | undefined
): NormalizedCircuitBreaker | undefined {
  if (!option) {
    return undefined;
  }

  const failureStatusCodes = [...DEFAULT_CIRCUIT_FAILURE_STATUS_CODES];
  if (option === true) {
    return {
      threshold: 5,
      cooldown: 30_000,
      halfOpenMaxRequests: 1,
      failureStatusCodes,
    };
  }

  if (typeof option !== "object" || Array.isArray(option)) {
    return undefined;
  }

  return {
    threshold: option.threshold ?? 5,
    cooldown: option.cooldown ?? 30_000,
    halfOpenMaxRequests: option.halfOpenMaxRequests ?? 1,
    failureStatusCodes: option.failureStatusCodes
      ? [...option.failureStatusCodes]
      : failureStatusCodes,
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
    if (typeof request === "object" && "url" in request) {
      const url = (request as { url?: unknown }).url;
      if (typeof url === "string" && url.length > 0) {
        return new URL(url).origin;
      }
    }
  } catch {
    return undefined;
  }
  return undefined;
}

function getState(registry: CircuitRegistry, origin: string): CircuitState {
  let state = registry.get(origin);
  if (!state) {
    state = {
      phase: "closed",
      consecutiveFailures: 0,
      openedAt: 0,
      halfOpenInFlight: 0,
    };
    registry.set(origin, state);
  }
  return state;
}

export interface CircuitGate {
  blocked: boolean;
  probe: boolean;
}

/**
 * Admit one logical request, or block it.
 * Half-open probe slots are reserved synchronously before any await.
 */
export function gateCircuit(
  registry: CircuitRegistry,
  origin: string,
  config: NormalizedCircuitBreaker,
  now: number = Date.now()
): CircuitGate {
  const state = getState(registry, origin);

  if (state.phase === "open" && now - state.openedAt >= config.cooldown) {
    state.phase = "half-open";
  }

  if (state.phase === "open") {
    return { blocked: true, probe: false };
  }

  if (state.phase === "half-open") {
    if (state.halfOpenInFlight >= config.halfOpenMaxRequests) {
      return { blocked: true, probe: false };
    }
    state.halfOpenInFlight++;
    return { blocked: false, probe: true };
  }

  return { blocked: false, probe: false };
}

export function releaseProbe(registry: CircuitRegistry, origin: string): void {
  const state = registry.get(origin);
  if (state && state.halfOpenInFlight > 0) {
    state.halfOpenInFlight--;
  }
}

export function recordCircuitOutcome(
  registry: CircuitRegistry,
  origin: string,
  config: NormalizedCircuitBreaker,
  outcome: CircuitOutcome,
  probe: boolean,
  now: number = Date.now()
): void {
  if (outcome === "neutral") {
    return;
  }

  const state = registry.get(origin);
  if (!state) {
    return;
  }

  if (outcome === "success") {
    state.consecutiveFailures = 0;
    if (probe && state.phase === "half-open") {
      state.phase = "closed";
    }
    return;
  }

  state.consecutiveFailures++;
  if (probe || state.consecutiveFailures >= config.threshold) {
    state.phase = "open";
    state.openedAt = now;
  }
}

export function circuitOutcomeFromStatus(
  status: number | undefined,
  failureStatusCodes: readonly number[]
): CircuitOutcome {
  if (status !== undefined && failureStatusCodes.includes(status)) {
    return "failure";
  }
  return "success";
}

export function classifyCircuitThrow(
  error: unknown,
  status: number | undefined,
  failureStatusCodes: readonly number[]
): CircuitOutcome {
  if (
    typeof error === "object" &&
    error !== null &&
    (error as { [STATUS_REJECTION]?: boolean })[STATUS_REJECTION] === true &&
    status !== undefined &&
    status >= 400 &&
    status < 600
  ) {
    return failureStatusCodes.includes(status) ? "failure" : "neutral";
  }
  return "failure";
}

export function markStatusRejection(error: object): void {
  (error as { [STATUS_REJECTION]?: boolean })[STATUS_REJECTION] = true;
}

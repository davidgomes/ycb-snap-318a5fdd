import type { CircuitBreakerOptions, FetchRequest } from "./types.ts";

export type CircuitState = "closed" | "open" | "half-open";

export type CircuitOutcome = "success" | "failure" | "neutral";

export interface ResolvedCircuitBreakerOptions {
  threshold: number;
  cooldown: number;
  halfOpenMaxRequests: number;
  failureStatusCodes: number[];
}

interface Circuit {
  state: CircuitState;
  failures: number;
  openedAt: number;
  probes: number;
  generation: number;
}

export type CircuitRegistry = Map<string, Circuit>;

export interface CircuitTicket {
  settle(outcome: CircuitOutcome): void;
}

const defaultCircuitBreakerOptions: ResolvedCircuitBreakerOptions = {
  threshold: 5,
  cooldown: 30_000,
  halfOpenMaxRequests: 1,
  failureStatusCodes: [408, 409, 425, 429, 500, 502, 503, 504],
};

export function resolveCircuitBreakerOptions(
  input: true | CircuitBreakerOptions
): ResolvedCircuitBreakerOptions {
  if (input === true) {
    return defaultCircuitBreakerOptions;
  }
  return {
    threshold: input.threshold ?? defaultCircuitBreakerOptions.threshold,
    cooldown: input.cooldown ?? defaultCircuitBreakerOptions.cooldown,
    halfOpenMaxRequests:
      input.halfOpenMaxRequests ??
      defaultCircuitBreakerOptions.halfOpenMaxRequests,
    failureStatusCodes:
      input.failureStatusCodes ??
      defaultCircuitBreakerOptions.failureStatusCodes,
  };
}

export function getRequestOrigin(request: FetchRequest): string | undefined {
  const url =
    typeof request === "string" || (request as unknown) instanceof URL
      ? String(request)
      : request.url;
  try {
    return new URL(url, (globalThis as any).location?.href).origin;
  } catch {
    return undefined;
  }
}

/**
 * Admits a logical request to the circuit of `origin`.
 * Returns `undefined` when the circuit is open or the half-open probe quota is exhausted.
 */
export function acquireCircuit(
  registry: CircuitRegistry,
  origin: string,
  options: ResolvedCircuitBreakerOptions
): CircuitTicket | undefined {
  let circuit = registry.get(origin);
  if (!circuit) {
    circuit = {
      state: "closed",
      failures: 0,
      openedAt: 0,
      probes: 0,
      generation: 0,
    };
    registry.set(origin, circuit);
  }

  if (circuit.state === "open") {
    if (Date.now() - circuit.openedAt < options.cooldown) {
      return undefined;
    }
    circuit.state = "half-open";
    circuit.probes = 0;
    circuit.generation++;
  }

  let probeGeneration: number | undefined;
  if (circuit.state === "half-open") {
    if (circuit.probes >= options.halfOpenMaxRequests) {
      return undefined;
    }
    circuit.probes++;
    probeGeneration = circuit.generation;
  }

  const c = circuit;
  let settled = false;
  return {
    settle(outcome) {
      if (settled) {
        return;
      }
      settled = true;

      // A probe only speaks for the half-open window it was admitted in
      const isProbe =
        probeGeneration !== undefined && probeGeneration === c.generation;
      if (isProbe && c.probes > 0) {
        c.probes--;
      }

      if (outcome === "success") {
        c.failures = 0;
        if (isProbe && c.state === "half-open") {
          c.state = "closed";
        }
      } else if (outcome === "failure") {
        if (isProbe && c.state !== "closed") {
          c.state = "open";
          c.openedAt = Date.now();
          return;
        }
        c.failures++;
        if (c.state === "closed" && c.failures >= options.threshold) {
          c.state = "open";
          c.openedAt = Date.now();
        }
      }
    },
  };
}

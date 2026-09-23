import type {
  CircuitBreakerOptions,
  FetchOptions,
  FetchRequest,
} from "./types.ts";

// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status
const defaultFailureStatusCodes = [
  408, // Request Timeout
  409, // Conflict
  425, // Too Early (Experimental)
  429, // Too Many Requests
  500, // Internal Server Error
  502, // Bad Gateway
  503, // Service Unavailable
  504, // Gateway Timeout
];

type ResolvedCircuitBreakerOptions = Required<CircuitBreakerOptions>;

interface Circuit {
  state: "closed" | "open" | "half-open";
  failures: number;
  openedAt: number;
  probes: number;
  /** Incremented on every transition to half-open, so late probes from an earlier round are ignored. */
  round: number;
}

export type CircuitRegistry = Map<string, Circuit>;

export interface CircuitTicket {
  circuit: Circuit;
  options: ResolvedCircuitBreakerOptions;
  /** Half-open round in which this request holds a probe slot. */
  probeRound?: number;
}

/** Circuit tracking for one logical request, shared by all of its retries. */
export interface CircuitCall {
  checked?: boolean;
  ticket?: CircuitTicket;
  /** Status of the response that rejected the request, if it was rejected by status. */
  rejectedStatus?: number;
}

export function resolveCircuitBreakerOptions(
  input: FetchOptions["circuitBreaker"]
): ResolvedCircuitBreakerOptions | undefined {
  if (!input) {
    return undefined;
  }
  const options: Partial<CircuitBreakerOptions> = input === true ? {} : input;
  return {
    threshold: options.threshold ?? 5,
    cooldown: options.cooldown ?? 30_000,
    halfOpenMaxRequests: options.halfOpenMaxRequests ?? 1,
    failureStatusCodes: options.failureStatusCodes ?? defaultFailureStatusCodes,
  };
}

export function getRequestOrigin(request: FetchRequest): string | undefined {
  // `URL` inputs have no `url` property and stringify to their href.
  const href = (request as Request).url || String(request);
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    try {
      url = new URL(href, globalThis.location?.href);
    } catch {
      return undefined;
    }
  }
  // Opaque origins (file:, data:, ...) all serialize to "null".
  return url.origin === "null" ? undefined : url.origin;
}

/** Returns `undefined` when the circuit rejects the request. */
export function acquireCircuit(
  circuits: CircuitRegistry,
  origin: string,
  options: ResolvedCircuitBreakerOptions
): CircuitTicket | undefined {
  let circuit = circuits.get(origin);
  if (!circuit) {
    circuit = {
      state: "closed",
      failures: 0,
      openedAt: 0,
      probes: 0,
      round: 0,
    };
    circuits.set(origin, circuit);
  }

  if (circuit.state === "open") {
    if (Date.now() - circuit.openedAt < options.cooldown) {
      return undefined;
    }
    circuit.state = "half-open";
    circuit.probes = 0;
    circuit.round++;
  }

  if (circuit.state === "half-open") {
    if (circuit.probes >= options.halfOpenMaxRequests) {
      return undefined;
    }
    circuit.probes++;
    return { circuit, options, probeRound: circuit.round };
  }

  return { circuit, options };
}

/**
 * Records the outcome of a logical request. A listed status or a
 * non-status rejection is a failure, a resolved non-listed status is a
 * success, and a rejection by a non-listed status changes nothing.
 */
export function releaseCircuit(
  call: CircuitCall,
  status: number | undefined,
  rejected: boolean
): void {
  const ticket = call.ticket;
  if (!ticket) {
    return;
  }
  const { circuit, options } = ticket;
  const failed =
    status === undefined || options.failureStatusCodes.includes(status);
  const succeeded = !failed && !rejected;

  if (circuit.state === "half-open" && ticket.probeRound === circuit.round) {
    circuit.probes--;
    if (succeeded) {
      circuit.state = "closed";
      circuit.failures = 0;
    } else if (failed) {
      openCircuit(circuit);
    }
    return;
  }

  // Outcomes of requests admitted before the circuit left the closed state are stale.
  if (circuit.state !== "closed") {
    return;
  }
  if (succeeded) {
    circuit.failures = 0;
  } else if (failed && ++circuit.failures >= options.threshold) {
    openCircuit(circuit);
  }
}

function openCircuit(circuit: Circuit): void {
  circuit.state = "open";
  circuit.openedAt = Date.now();
}

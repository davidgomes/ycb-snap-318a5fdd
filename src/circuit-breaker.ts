import type {
  CircuitBreakerOptions,
  FetchRequest,
} from "./types.ts";

const defaultCircuitBreakerOptions = {
  threshold: 5,
  cooldown: 30_000,
  halfOpenMaxRequests: 1,
  failureStatusCodes: [408, 409, 425, 429, 500, 502, 503, 504],
} satisfies Required<CircuitBreakerOptions>;

export type CircuitBreakerConfig = Required<CircuitBreakerOptions>;

type CircuitState =
  | {
      status: "closed";
      failureCount: number;
    }
  | {
      status: "open";
      failureCount: number;
      openedAt: number;
    }
  | {
      status: "half-open";
      failureCount: number;
      halfOpenRequests: number;
    };

export type CircuitBreakerRegistry = Map<string, CircuitState>;

export type CircuitBreakerOutcome = "success" | "failure" | "neutral";

export interface CircuitBreakerLease {
  origin: string;
  isHalfOpen: boolean;
}

export function createCircuitBreakerRegistry(): CircuitBreakerRegistry {
  return new Map();
}

export function resolveCircuitBreakerConfig(
  options: boolean | CircuitBreakerOptions | undefined
): CircuitBreakerConfig | undefined {
  if (!options) {
    return undefined;
  }

  const customOptions = options === true ? {} : options;
  return {
    threshold:
      customOptions.threshold ?? defaultCircuitBreakerOptions.threshold,
    cooldown:
      customOptions.cooldown ?? defaultCircuitBreakerOptions.cooldown,
    halfOpenMaxRequests:
      customOptions.halfOpenMaxRequests ??
      defaultCircuitBreakerOptions.halfOpenMaxRequests,
    failureStatusCodes:
      customOptions.failureStatusCodes ??
      defaultCircuitBreakerOptions.failureStatusCodes,
  };
}

export function resolveRequestOrigin(
  request: FetchRequest
): string | undefined {
  try {
    if (request instanceof URL) {
      return request.origin;
    }
    if (request instanceof Request) {
      return new URL(request.url).origin;
    }
    return new URL(request).origin;
  } catch {
    return undefined;
  }
}

export function acquireCircuitBreaker(
  registry: CircuitBreakerRegistry,
  origin: string,
  config: CircuitBreakerConfig
): CircuitBreakerLease {
  let circuit = registry.get(origin);
  const now = Date.now();

  if (!circuit) {
    circuit = {
      status: "closed",
      failureCount: 0,
    };
    registry.set(origin, circuit);
  }

  if (
    circuit.status === "open" &&
    now - circuit.openedAt >= config.cooldown
  ) {
    circuit = {
      status: "half-open",
      failureCount: circuit.failureCount,
      halfOpenRequests: 0,
    };
    registry.set(origin, circuit);
  }

  if (circuit.status === "open") {
    throw new Error(`Circuit breaker is open for ${origin}`);
  }

  if (
    circuit.status === "half-open" &&
    circuit.halfOpenRequests >= config.halfOpenMaxRequests
  ) {
    throw new Error(`Circuit breaker is open for ${origin}`);
  }

  const isHalfOpen = circuit.status === "half-open";
  if (isHalfOpen) {
    circuit.halfOpenRequests++;
  }

  return {
    origin,
    isHalfOpen,
  };
}

export function completeCircuitBreaker(
  registry: CircuitBreakerRegistry,
  lease: CircuitBreakerLease,
  config: CircuitBreakerConfig,
  outcome: CircuitBreakerOutcome
): void {
  const circuit = registry.get(lease.origin);
  if (!circuit) {
    return;
  }

  if (lease.isHalfOpen) {
    if (circuit.status !== "half-open") {
      return;
    }

    circuit.halfOpenRequests--;
    if (outcome === "success") {
      registry.set(lease.origin, {
        status: "closed",
        failureCount: 0,
      });
    } else if (outcome === "failure") {
      registry.set(lease.origin, {
        status: "open",
        failureCount: circuit.failureCount,
        openedAt: Date.now(),
      });
    }
    return;
  }

  if (circuit.status !== "closed") {
    return;
  }

  if (outcome === "success") {
    circuit.failureCount = 0;
  } else if (outcome === "failure") {
    circuit.failureCount++;
    if (circuit.failureCount >= config.threshold) {
      registry.set(lease.origin, {
        status: "open",
        failureCount: circuit.failureCount,
        openedAt: Date.now(),
      });
    }
  }
}

export function isCircuitBreakerFailureStatus(
  status: number,
  config: CircuitBreakerConfig
): boolean {
  return config.failureStatusCodes.includes(status);
}

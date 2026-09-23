import SuperJSON from './index.js';
import { isArray, isError } from './is.js';
import { NormalizedErrorStackOptions } from './error-options.js';
import { processStackFrames, processStackString } from './error-stack.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { SerializedError } from './error-class-registry.js';

export type ErrorTypeAnnotation = 'Error' | 'Error/stack' | 'Error/frames';

const MANAGED_PROPS = ['name', 'message', 'stack', 'stackFrames', 'cause'];

// Causes are serialized eagerly by their parent, but the walker only emits
// Error annotations for Error instances. Wrapping a finished cause payload in
// this carrier lets it pass through the walker unchanged and still be revived
// as an Error on deserialization.
class SerializedCause extends Error {
  constructor(
    readonly payload: SerializedError,
    readonly annotation: ErrorTypeAnnotation
  ) {
    super();
  }
}

const causeAnnotations = new WeakMap<object, ErrorTypeAnnotation>();

const isAggregateError = (error: Error): error is AggregateError =>
  typeof AggregateError === 'function' && error instanceof AggregateError;

function matchesClassFilter(
  name: unknown,
  options: NormalizedErrorStackOptions
): boolean {
  return (
    options.classFilter.length === 0 ||
    (typeof name === 'string' && options.classFilter.includes(name))
  );
}

function annotationFor(
  name: unknown,
  options: NormalizedErrorStackOptions | undefined
): ErrorTypeAnnotation {
  if (!options || options.mode === 'off' || !matchesClassFilter(name, options)) {
    return 'Error';
  }
  return options.mode === 'string' ? 'Error/stack' : 'Error/frames';
}

export function errorTypeAnnotation(
  error: Error,
  superJson: SuperJSON
): ErrorTypeAnnotation {
  return error instanceof SerializedCause
    ? error.annotation
    : annotationFor(error.name, superJson.errorStackOptions);
}

function causeDepth(options: NormalizedErrorStackOptions): number {
  switch (options.includeCauses) {
    case 'direct':
      return 1;
    case 'deep':
      return options.maxCauseDepth;
    default:
      return 0;
  }
}

function sanitizeHeader(stack: string): string {
  const headerEnd = stack.indexOf('\n');
  return headerEnd === -1
    ? sanitizeMessage(stack)
    : sanitizeMessage(stack.slice(0, headerEnd)) + stack.slice(headerEnd);
}

function applyProcessor(
  payload: SerializedError,
  error: Error,
  superJson: SuperJSON
): SerializedError {
  const registry = superJson.errorClassRegistry;
  const processor =
    (typeof error.name === 'string' && registry.getProcessor(error.name)) ||
    registry.getProcessor(error.constructor?.name);
  if (!processor) {
    return payload;
  }

  const result = processor(payload);
  return typeof result === 'object' && result !== null ? result : payload;
}

function legacyPayload(error: Error, superJson: SuperJSON): SerializedError {
  const baseError: any = {
    name: error.name,
    message: error.message,
  };

  if ('cause' in error) {
    baseError.cause = error.cause;
  }

  superJson.allowedErrorProps.forEach(prop => {
    baseError[prop] = (error as any)[prop];
  });

  return baseError;
}

function buildPayload(
  error: Error,
  superJson: SuperJSON,
  options: NormalizedErrorStackOptions,
  remainingCauseDepth: number,
  chain: Error[]
): SerializedError {
  const matches = matchesClassFilter(error.name, options);
  const sanitize = options.sanitizeMessage && matches;
  const allowed = superJson.allowedErrorProps;

  const payload: SerializedError = {
    name: error.name,
    message: sanitize ? sanitizeMessage(error.message) : error.message,
  };

  const cause = (error as { cause?: unknown }).cause;
  if (remainingCauseDepth > 0 && isError(cause) && !chain.includes(cause)) {
    const causePayload = buildPayload(
      cause,
      superJson,
      options,
      remainingCauseDepth - 1,
      [...chain, cause]
    );
    causeAnnotations.set(causePayload, annotationFor(cause.name, options));
    payload.cause = causePayload;
  }

  allowed.forEach(prop => {
    if (!MANAGED_PROPS.includes(prop)) {
      payload[prop] = (error as any)[prop];
    }
  });

  if (matches && typeof error.stack === 'string') {
    if (options.mode === 'string' && allowed.includes('stack')) {
      const stack = processStackString(error.stack, options);
      payload.stack = sanitize ? sanitizeHeader(stack) : stack;
    } else if (options.mode === 'frames' && allowed.includes('stackFrames')) {
      const frames = processStackFrames(error.stack, options);
      if (sanitize && frames.length > 0) {
        frames[0] = { raw: sanitizeMessage(frames[0].raw) };
      }
      payload.stackFrames = frames;
    }
  }

  if (isAggregateError(error)) {
    payload.errors = error.errors;
  }

  return applyProcessor(payload, error, superJson);
}

function wrapCauses(payload: SerializedError): SerializedError {
  const cause = payload.cause as SerializedError;
  const annotation = causeAnnotations.get(cause);
  if (!annotation) {
    return payload;
  }

  return {
    ...payload,
    cause: new SerializedCause(wrapCauses(cause), annotation),
  };
}

export function serializeError(
  error: Error,
  superJson: SuperJSON
): SerializedError {
  if (error instanceof SerializedCause) {
    return error.payload;
  }

  const options = superJson.errorStackOptions;
  if (!options) {
    return applyProcessor(legacyPayload(error, superJson), error, superJson);
  }

  return wrapCauses(
    buildPayload(error, superJson, options, causeDepth(options), [error])
  );
}

export function deserializeError(
  v: SerializedError,
  superJson: SuperJSON
): Error {
  const options = 'cause' in v ? { cause: v.cause } : undefined;
  const e: any =
    v.name === 'AggregateError' &&
    isArray(v.errors) &&
    typeof AggregateError === 'function'
      ? new AggregateError(v.errors, v.message, options)
      : new Error(v.message, options);
  e.name = v.name;
  e.stack = v.stack;

  if ('stackFrames' in v) {
    e.stackFrames = v.stackFrames;
  }

  if ('errors' in v && !isAggregateError(e)) {
    e.errors = v.errors;
  }

  superJson.allowedErrorProps.forEach(prop => {
    e[prop] = v[prop];
  });

  return e;
}

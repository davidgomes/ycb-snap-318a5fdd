import { ErrorClassRegistry } from './error-class-registry.js';
import {
  NormalizedErrorStackOptions,
  matchesClassFilter,
} from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { processStackFrames, processStackString } from './error-stack.js';
import { isError } from './is.js';

const clippedErrors = new WeakSet<Error>();

function shallowCloneError(err: Error, allowedErrorProps: string[]): Error {
  const copy = new Error(err.message);
  copy.name = err.name;

  if (typeof err.stack === 'string') {
    copy.stack = err.stack;
  }

  if ('errors' in err) {
    (copy as any).errors = (err as any).errors;
  }

  for (let i = 0; i < allowedErrorProps.length; i++) {
    const prop = allowedErrorProps[i];
    if (
      prop === 'cause' ||
      prop === 'stack' ||
      prop === 'stackFrames' ||
      prop === 'errors' ||
      prop === 'name' ||
      prop === 'message'
    ) {
      continue;
    }
    if (prop in err) {
      (copy as any)[prop] = (err as any)[prop];
    }
  }

  clippedErrors.add(copy);
  return copy;
}

function wrapCause(
  err: Error,
  remaining: number,
  seen: WeakSet<Error>,
  allowedErrorProps: string[]
): Error {
  const copy = shallowCloneError(err, allowedErrorProps);

  if (seen.has(err)) {
    return copy;
  }

  seen.add(err);

  if (remaining > 0 && isError((err as any).cause)) {
    (copy as any).cause = wrapCause(
      (err as any).cause,
      remaining - 1,
      seen,
      allowedErrorProps
    );
  }

  return copy;
}

function attachCause(
  v: Error,
  baseError: Record<string, unknown>,
  options: NormalizedErrorStackOptions,
  allowedErrorProps: string[]
): void {
  if (clippedErrors.has(v)) {
    if (isError((v as any).cause)) {
      baseError.cause = (v as any).cause;
    }
    return;
  }

  const cause = (v as any).cause;
  if (!isError(cause)) {
    return;
  }

  if (options.includeCauses === 'direct') {
    baseError.cause = shallowCloneError(cause, allowedErrorProps);
    return;
  }

  if (options.includeCauses === 'deep' && options.maxCauseDepth > 0) {
    baseError.cause = wrapCause(
      cause,
      options.maxCauseDepth - 1,
      new WeakSet<Error>([v]),
      allowedErrorProps
    );
  }
}

function applyProcessor(
  serialized: Record<string, unknown>,
  name: string,
  registry: ErrorClassRegistry
): Record<string, unknown> {
  const processor = registry.getProcessor(name);
  if (!processor) {
    return serialized;
  }

  return processor(serialized) ?? serialized;
}

export type ErrorStackSerializeMode = 'legacy' | 'off' | 'string' | 'frames';

export function serializeErrorValue(
  v: Error,
  options: NormalizedErrorStackOptions | undefined,
  allowedErrorProps: string[],
  registry: ErrorClassRegistry,
  stackMode: ErrorStackSerializeMode
): Record<string, unknown> {
  if (stackMode === 'legacy' || !options) {
    const baseError: Record<string, unknown> = {
      name: v.name,
      message: v.message,
    };

    if ('cause' in v) {
      baseError.cause = (v as any).cause;
    }

    allowedErrorProps.forEach(prop => {
      baseError[prop] = (v as any)[prop];
    });

    return applyProcessor(baseError, v.name, registry);
  }

  const matchesFilter = matchesClassFilter(v.name, options.classFilter);
  let message = v.message;
  if (options.sanitizeMessage && matchesFilter) {
    message = sanitizeMessage(message);
  }

  const baseError: Record<string, unknown> = {
    name: v.name,
    message,
  };

  attachCause(v, baseError, options, allowedErrorProps);

  if ('errors' in v) {
    baseError.errors = (v as any).errors;
  }

  const skipProps = new Set(['stack', 'stackFrames', 'cause', 'errors']);

  allowedErrorProps.forEach(prop => {
    if (skipProps.has(prop)) {
      return;
    }
    baseError[prop] = (v as any)[prop];
  });

  if (
    stackMode === 'string' &&
    allowedErrorProps.indexOf('stack') !== -1 &&
    typeof v.stack === 'string'
  ) {
    baseError.stack = processStackString(v.stack, options);
  } else if (
    stackMode === 'frames' &&
    allowedErrorProps.indexOf('stackFrames') !== -1 &&
    typeof v.stack === 'string'
  ) {
    baseError.stackFrames = processStackFrames(v.stack, options);
  }

  return applyProcessor(baseError, v.name, registry);
}

export function deserializeErrorValue(
  v: any,
  allowedErrorProps: string[]
): Error {
  const hasErrorList = v && Array.isArray(v.errors);
  const errorOptions = { cause: v.cause };

  let e: Error;
  if (hasErrorList && typeof AggregateError === 'function') {
    e = new AggregateError(v.errors, v.message, errorOptions);
  } else {
    e = new Error(v.message, errorOptions);
  }

  e.name = v.name;
  e.stack = v.stack;

  if ('stackFrames' in v) {
    (e as any).stackFrames = v.stackFrames;
  }

  if (v && 'errors' in v) {
    (e as any).errors = v.errors;
  }

  allowedErrorProps.forEach(prop => {
    (e as any)[prop] = v[prop];
  });

  return e;
}

export function errorMatchesStackMode(
  v: unknown,
  options: NormalizedErrorStackOptions | undefined,
  mode: 'string' | 'frames'
): v is Error {
  return (
    isError(v) &&
    !!options &&
    options.mode === mode &&
    matchesClassFilter(v.name, options.classFilter)
  );
}

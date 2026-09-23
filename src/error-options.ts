export type ErrorStackMode = 'off' | 'string' | 'frames';

export type StripInternalFrames =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';

export type RedactPaths = 'none' | 'basename' | 'strip_cwd';

export type IncludeCauses = 'none' | 'direct' | 'deep';

/**
 * Raw `errorStack` constructor input. Invalid enum values are normalized away.
 */
export interface ErrorStackOptions {
  mode?: string;
  normalizeNewlines?: boolean;
  trimLeadingWhitespace?: boolean;
  maxStackLines?: number;
  stripInternalFrames?: string;
  redactPaths?: string;
  includeCauses?: string;
  maxCauseDepth?: number;
  sanitizeMessage?: boolean;
  classFilter?: readonly string[];
}

export interface NormalizedErrorStackOptions {
  mode: ErrorStackMode;
  normalizeNewlines: boolean;
  trimLeadingWhitespace: boolean;
  maxStackLines: number | undefined;
  stripInternalFrames: StripInternalFrames;
  redactPaths: RedactPaths;
  includeCauses: IncludeCauses;
  maxCauseDepth: number;
  sanitizeMessage: boolean;
  /** Empty means every error class name. */
  classFilter: string[];
}

const STACK_MODES: readonly ErrorStackMode[] = ['off', 'string', 'frames'];
const STRIP_MODES: readonly StripInternalFrames[] = [
  'none',
  'node',
  'superjson',
  'node_and_superjson',
];
const REDACT_MODES: readonly RedactPaths[] = ['none', 'basename', 'strip_cwd'];
const CAUSE_MODES: readonly IncludeCauses[] = ['none', 'direct', 'deep'];

function oneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
  fallback: T
): T {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback;
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value > 0;
}

function isInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value);
}

function readClassFilter(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((entry): entry is string => typeof entry === 'string');
}

/**
 * Normalizes `errorStack` once. Non-objects (`null`, `undefined`, strings, numbers, …)
 * return `undefined`, which keeps legacy Error serialization.
 */
export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (input === null || typeof input !== 'object') {
    return undefined;
  }

  const source = input as Record<string, unknown>;
  let mode = oneOf(source.mode, STACK_MODES, 'off');
  let maxStackLines: number | undefined;

  if (Object.prototype.hasOwnProperty.call(source, 'maxStackLines')) {
    const value = source.maxStackLines;
    if (value !== undefined) {
      if (isPositiveInteger(value)) {
        maxStackLines = value;
      } else {
        // Zero, negative, or non-integer disables stack serialization.
        mode = 'off';
      }
    }
  }

  let includeCauses = oneOf(source.includeCauses, CAUSE_MODES, 'none');
  let maxCauseDepth = 16;

  if (Object.prototype.hasOwnProperty.call(source, 'maxCauseDepth')) {
    const value = source.maxCauseDepth;
    if (value !== undefined) {
      if (isInteger(value)) {
        maxCauseDepth = value;
      } else {
        includeCauses = 'none';
      }
    }
  }

  return {
    mode,
    normalizeNewlines: source.normalizeNewlines === true,
    trimLeadingWhitespace: source.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames: oneOf(source.stripInternalFrames, STRIP_MODES, 'none'),
    redactPaths: oneOf(source.redactPaths, REDACT_MODES, 'none'),
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: source.sanitizeMessage === true,
    classFilter: readClassFilter(source.classFilter),
  };
}

export function matchesErrorClassFilter(
  name: string,
  classFilter: readonly string[]
): boolean {
  return classFilter.length === 0 || classFilter.includes(name);
}

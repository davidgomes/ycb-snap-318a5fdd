export type ErrorStackMode = 'off' | 'string' | 'frames';

export type StripInternalFrames =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';

export type RedactPaths = 'none' | 'basename' | 'strip_cwd';

export type IncludeCauses = 'none' | 'direct' | 'deep';

export interface ErrorStackOptionsInput {
  mode?: unknown;
  normalizeNewlines?: unknown;
  trimLeadingWhitespace?: unknown;
  maxStackLines?: unknown;
  stripInternalFrames?: unknown;
  redactPaths?: unknown;
  includeCauses?: unknown;
  maxCauseDepth?: unknown;
  sanitizeMessage?: unknown;
  classFilter?: unknown;
}

export interface NormalizedErrorStackOptions {
  mode: ErrorStackMode;
  normalizeNewlines: boolean;
  trimLeadingWhitespace: boolean;
  maxStackLines?: number;
  stripInternalFrames: StripInternalFrames;
  redactPaths: RedactPaths;
  includeCauses: IncludeCauses;
  maxCauseDepth: number;
  sanitizeMessage: boolean;
  classFilter?: string[];
}

const STRIP: readonly StripInternalFrames[] = [
  'none',
  'node',
  'superjson',
  'node_and_superjson',
];

const REDACT: readonly RedactPaths[] = ['none', 'basename', 'strip_cwd'];

const CAUSES: readonly IncludeCauses[] = ['none', 'direct', 'deep'];

function isOneOf<T extends string>(
  value: unknown,
  allowed: readonly T[]
): value is T {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value);
}

/**
 * Normalize constructor `errorStack` once.
 * Non-objects (`null`, `undefined`, strings, numbers) yield `undefined`,
 * which preserves legacy Error serialization.
 */
export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (input === null || input === undefined) return undefined;
  if (typeof input !== 'object') return undefined;

  const raw = input as ErrorStackOptionsInput;

  let mode: ErrorStackMode = 'off';
  if (raw.mode === 'off' || raw.mode === 'string' || raw.mode === 'frames') {
    mode = raw.mode;
  }

  let maxStackLines: number | undefined;
  if (raw.maxStackLines !== undefined) {
    const n = raw.maxStackLines;
    if (typeof n !== 'number' || !Number.isInteger(n) || n <= 0) {
      mode = 'off';
    } else {
      maxStackLines = n;
    }
  }

  let includeCauses: IncludeCauses = isOneOf(raw.includeCauses, CAUSES)
    ? raw.includeCauses
    : 'none';

  let maxCauseDepth = 16;
  if (raw.maxCauseDepth !== undefined) {
    if (typeof raw.maxCauseDepth !== 'number' || !Number.isInteger(raw.maxCauseDepth)) {
      includeCauses = 'none';
    } else {
      maxCauseDepth = raw.maxCauseDepth;
    }
  }

  let classFilter: string[] | undefined;
  if (Array.isArray(raw.classFilter)) {
    const names = raw.classFilter.filter(
      (name): name is string => typeof name === 'string'
    );
    if (names.length > 0) classFilter = names;
  }

  return {
    mode,
    normalizeNewlines: raw.normalizeNewlines === true,
    trimLeadingWhitespace: raw.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames: isOneOf(raw.stripInternalFrames, STRIP)
      ? raw.stripInternalFrames
      : 'none',
    redactPaths: isOneOf(raw.redactPaths, REDACT) ? raw.redactPaths : 'none',
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: raw.sanitizeMessage === true,
    classFilter,
  };
}

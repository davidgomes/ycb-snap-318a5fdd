export type ErrorStackMode = 'off' | 'string' | 'frames';

export type StripInternalFrames =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';

export type RedactPaths = 'none' | 'basename' | 'strip_cwd';

export type IncludeCauses = 'none' | 'direct' | 'deep';

export interface ErrorStackOptions {
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

const STRIP_INTERNAL_FRAMES: readonly StripInternalFrames[] = [
  'none',
  'node',
  'superjson',
  'node_and_superjson',
];

const REDACT_PATHS: readonly RedactPaths[] = ['none', 'basename', 'strip_cwd'];

const INCLUDE_CAUSES: readonly IncludeCauses[] = ['none', 'direct', 'deep'];

function isIntegerNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value);
}

function pickEnum<T extends string>(
  value: unknown,
  allowed: readonly T[],
  fallback: T
): T {
  return allowed.indexOf(value as T) === -1 ? fallback : (value as T);
}

function normalizeClassFilter(value: unknown): string[] | undefined {
  if (typeof value === 'string') {
    return value.length > 0 ? [value] : undefined;
  }

  if (!Array.isArray(value)) {
    return undefined;
  }

  const names = value.filter((item): item is string => typeof item === 'string');
  return names.length > 0 ? names : undefined;
}

export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (input === null || typeof input !== 'object') {
    return undefined;
  }

  const raw = input as ErrorStackOptions;

  let mode: ErrorStackMode = pickEnum(raw.mode, ['off', 'string', 'frames'] as const, 'off');

  let maxStackLines: number | undefined;
  if (raw.maxStackLines !== undefined) {
    if (
      typeof raw.maxStackLines !== 'number' ||
      !Number.isInteger(raw.maxStackLines) ||
      raw.maxStackLines <= 0
    ) {
      mode = 'off';
    }
    if (typeof raw.maxStackLines === 'number') {
      maxStackLines = raw.maxStackLines;
    }
  }

  let includeCauses: IncludeCauses = pickEnum(
    raw.includeCauses,
    INCLUDE_CAUSES,
    'none'
  );
  let maxCauseDepth = 16;
  if (raw.maxCauseDepth !== undefined) {
    if (!isIntegerNumber(raw.maxCauseDepth)) {
      includeCauses = 'none';
    } else {
      maxCauseDepth = raw.maxCauseDepth;
    }
  }

  const normalized: NormalizedErrorStackOptions = {
    mode,
    normalizeNewlines: raw.normalizeNewlines === true,
    trimLeadingWhitespace: raw.trimLeadingWhitespace !== false,
    stripInternalFrames: pickEnum(
      raw.stripInternalFrames,
      STRIP_INTERNAL_FRAMES,
      'none'
    ),
    redactPaths: pickEnum(raw.redactPaths, REDACT_PATHS, 'none'),
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: raw.sanitizeMessage === true,
  };

  if (maxStackLines !== undefined) {
    normalized.maxStackLines = maxStackLines;
  }

  const classFilter = normalizeClassFilter(raw.classFilter);
  if (classFilter) {
    normalized.classFilter = classFilter;
  }

  return normalized;
}

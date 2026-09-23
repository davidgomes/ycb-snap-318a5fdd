export type ErrorStackMode = 'off' | 'string' | 'frames';

export type StripInternalFrames =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';

export type RedactPaths = 'none' | 'basename' | 'strip_cwd';

export type IncludeCauses = 'none' | 'direct' | 'deep';

export interface ErrorStackOptions {
  mode?: ErrorStackMode;
  normalizeNewlines?: boolean;
  trimLeadingWhitespace?: boolean;
  maxStackLines?: number;
  stripInternalFrames?: StripInternalFrames;
  redactPaths?: RedactPaths;
  includeCauses?: IncludeCauses;
  maxCauseDepth?: number;
  sanitizeMessage?: boolean;
  classFilter?: string[];
}

export interface NormalizedErrorStackOptions {
  readonly mode: ErrorStackMode;
  readonly normalizeNewlines: boolean;
  readonly trimLeadingWhitespace: boolean;
  readonly maxStackLines: number | undefined;
  readonly stripInternalFrames: StripInternalFrames;
  readonly redactPaths: RedactPaths;
  readonly includeCauses: IncludeCauses;
  readonly maxCauseDepth: number;
  readonly sanitizeMessage: boolean;
  readonly classFilter: readonly string[];
}

export const DEFAULT_MAX_CAUSE_DEPTH = 16;

const MODES: readonly ErrorStackMode[] = ['off', 'string', 'frames'];
const STRIP_INTERNAL_FRAMES: readonly StripInternalFrames[] = [
  'none',
  'node',
  'superjson',
  'node_and_superjson',
];
const REDACT_PATHS: readonly RedactPaths[] = ['none', 'basename', 'strip_cwd'];
const INCLUDE_CAUSES: readonly IncludeCauses[] = ['none', 'direct', 'deep'];

function oneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
  fallback: T
): T {
  return allowed.indexOf(value as T) !== -1 ? (value as T) : fallback;
}

export function resolveStripInternalFrames(value: unknown): StripInternalFrames {
  return oneOf(value, STRIP_INTERNAL_FRAMES, 'none');
}

export function resolveRedactPaths(value: unknown): RedactPaths {
  return oneOf(value, REDACT_PATHS, 'none');
}

export function isPositiveInteger(value: unknown): value is number {
  return Number.isInteger(value) && (value as number) > 0;
}

export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (typeof input !== 'object' || input === null) {
    return undefined;
  }

  const options = input as Record<string, unknown>;

  let mode = oneOf(options.mode, MODES, 'off');

  let maxStackLines: number | undefined = undefined;
  if (options.maxStackLines !== undefined) {
    if (isPositiveInteger(options.maxStackLines)) {
      maxStackLines = options.maxStackLines;
    } else {
      mode = 'off';
    }
  }

  let includeCauses = oneOf(options.includeCauses, INCLUDE_CAUSES, 'none');
  let maxCauseDepth = DEFAULT_MAX_CAUSE_DEPTH;
  if (options.maxCauseDepth !== undefined) {
    if (Number.isInteger(options.maxCauseDepth)) {
      maxCauseDepth = options.maxCauseDepth as number;
    } else {
      includeCauses = 'none';
    }
  }

  const rawClassFilter = options.classFilter;
  const classFilter = (Array.isArray(rawClassFilter)
    ? rawClassFilter
    : typeof rawClassFilter === 'string'
    ? [rawClassFilter]
    : []
  ).filter((name): name is string => typeof name === 'string');

  return Object.freeze({
    mode,
    normalizeNewlines: options.normalizeNewlines === true,
    trimLeadingWhitespace: options.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames: resolveStripInternalFrames(
      options.stripInternalFrames
    ),
    redactPaths: resolveRedactPaths(options.redactPaths),
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: options.sanitizeMessage === true,
    classFilter: Object.freeze(classFilter),
  });
}

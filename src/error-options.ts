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
  readonly classFilter: readonly string[] | undefined;
}

export const DEFAULT_MAX_CAUSE_DEPTH = 16;

const MODES: readonly ErrorStackMode[] = ['off', 'string', 'frames'];
const STRIP_VALUES: readonly StripInternalFrames[] = [
  'none',
  'node',
  'superjson',
  'node_and_superjson',
];
const REDACT_VALUES: readonly RedactPaths[] = ['none', 'basename', 'strip_cwd'];
const CAUSE_VALUES: readonly IncludeCauses[] = ['none', 'direct', 'deep'];

function oneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
  fallback: T
): T {
  return allowed.indexOf(value as T) !== -1 ? (value as T) : fallback;
}

function normalizeClassFilter(value: unknown): string[] | undefined {
  const names = (Array.isArray(value) ? value : [value]).filter(
    (name): name is string => typeof name === 'string' && name.length > 0
  );
  return names.length > 0 ? names : undefined;
}

export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (typeof input !== 'object' || input === null || Array.isArray(input)) {
    return undefined;
  }

  const options = input as Record<string, unknown>;

  let mode = oneOf(options.mode, MODES, 'off');

  let maxStackLines: number | undefined;
  if (options.maxStackLines !== undefined) {
    const value = options.maxStackLines;
    if (typeof value === 'number' && Number.isInteger(value) && value > 0) {
      maxStackLines = value;
    } else {
      mode = 'off';
    }
  }

  let includeCauses = oneOf(options.includeCauses, CAUSE_VALUES, 'none');
  let maxCauseDepth = DEFAULT_MAX_CAUSE_DEPTH;
  if (options.maxCauseDepth !== undefined) {
    const value = options.maxCauseDepth;
    if (typeof value === 'number' && Number.isInteger(value)) {
      maxCauseDepth = Math.max(0, value);
    } else {
      includeCauses = 'none';
    }
  }

  return Object.freeze({
    mode,
    normalizeNewlines: options.normalizeNewlines === true,
    trimLeadingWhitespace: options.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames: oneOf(
      options.stripInternalFrames,
      STRIP_VALUES,
      'none'
    ),
    redactPaths: oneOf(options.redactPaths, REDACT_VALUES, 'none'),
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: options.sanitizeMessage === true,
    classFilter: normalizeClassFilter(options.classFilter),
  });
}

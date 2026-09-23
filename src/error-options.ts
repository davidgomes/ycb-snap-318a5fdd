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

const normalizedOptions = new WeakSet<object>();

function oneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
  fallback: T
): T {
  return allowed.includes(value as T) ? (value as T) : fallback;
}

function normalizeClassFilter(value: unknown): readonly string[] {
  if (typeof value === 'string') {
    return [value];
  }
  if (Array.isArray(value)) {
    return value.filter((name): name is string => typeof name === 'string');
  }
  return [];
}

export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (typeof input !== 'object' || input === null || Array.isArray(input)) {
    return undefined;
  }
  if (normalizedOptions.has(input)) {
    return input as NormalizedErrorStackOptions;
  }

  const raw = input as Record<string, unknown>;

  let mode = oneOf(raw.mode, MODES, 'off');
  let maxStackLines: number | undefined;
  if (raw.maxStackLines !== undefined) {
    if (Number.isInteger(raw.maxStackLines) && (raw.maxStackLines as number) > 0) {
      maxStackLines = raw.maxStackLines as number;
    } else {
      mode = 'off';
    }
  }

  let includeCauses = oneOf(raw.includeCauses, INCLUDE_CAUSES, 'none');
  let maxCauseDepth = DEFAULT_MAX_CAUSE_DEPTH;
  if (raw.maxCauseDepth !== undefined) {
    if (Number.isInteger(raw.maxCauseDepth)) {
      maxCauseDepth = raw.maxCauseDepth as number;
    } else {
      includeCauses = 'none';
    }
  }

  const options: NormalizedErrorStackOptions = Object.freeze({
    mode,
    normalizeNewlines: raw.normalizeNewlines === true,
    trimLeadingWhitespace:
      typeof raw.trimLeadingWhitespace === 'boolean'
        ? raw.trimLeadingWhitespace
        : true,
    maxStackLines,
    stripInternalFrames: oneOf(
      raw.stripInternalFrames,
      STRIP_INTERNAL_FRAMES,
      'none'
    ),
    redactPaths: oneOf(raw.redactPaths, REDACT_PATHS, 'none'),
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: raw.sanitizeMessage === true,
    classFilter: Object.freeze(normalizeClassFilter(raw.classFilter)),
  });

  normalizedOptions.add(options);
  return options;
}

export function matchesClassFilter(
  options: NormalizedErrorStackOptions,
  name: unknown
): boolean {
  return (
    options.classFilter.length === 0 ||
    options.classFilter.includes(name as string)
  );
}

export function initialCauseDepth(options: NormalizedErrorStackOptions) {
  switch (options.includeCauses) {
    case 'direct':
      return 1;
    case 'deep':
      return options.maxCauseDepth;
    default:
      return 0;
  }
}

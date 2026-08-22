export type ErrorStackMode = 'off' | 'string' | 'frames';
export type StripInternalFramesMode =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';
export type RedactPathsMode = 'none' | 'basename' | 'strip_cwd';
export type IncludeCausesMode = 'none' | 'direct' | 'deep';

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
  classFilter?: string | string[];
}

export interface NormalizedErrorStackOptions {
  mode: ErrorStackMode;
  normalizeNewlines: boolean;
  trimLeadingWhitespace: boolean;
  maxStackLines: number | undefined;
  stripInternalFrames: StripInternalFramesMode;
  redactPaths: RedactPathsMode;
  includeCauses: IncludeCausesMode;
  maxCauseDepth: number;
  sanitizeMessage: boolean;
  classFilter: string[] | undefined;
}

const MODES: ReadonlySet<string> = new Set(['off', 'string', 'frames']);

const STRIP_INTERNAL_FRAMES: ReadonlySet<string> = new Set([
  'none',
  'node',
  'superjson',
  'node_and_superjson',
]);

const REDACT_PATHS: ReadonlySet<string> = new Set([
  'none',
  'basename',
  'strip_cwd',
]);

const INCLUDE_CAUSES: ReadonlySet<string> = new Set([
  'none',
  'direct',
  'deep',
]);

function normalizeClassFilter(classFilter: unknown): string[] | undefined {
  if (typeof classFilter === 'string') {
    return classFilter === '' ? undefined : [classFilter];
  }

  if (Array.isArray(classFilter)) {
    const names = classFilter.filter(
      (name): name is string => typeof name === 'string' && name !== ''
    );
    return names.length === 0 ? undefined : names;
  }

  return undefined;
}

export function matchesClassFilter(
  name: string,
  classFilter: string[] | undefined
): boolean {
  if (!classFilter || classFilter.length === 0) {
    return true;
  }

  return classFilter.indexOf(name) !== -1;
}

export const DEFAULT_ERROR_STACK_OPTIONS: NormalizedErrorStackOptions = {
  mode: 'off',
  normalizeNewlines: false,
  trimLeadingWhitespace: true,
  maxStackLines: undefined,
  stripInternalFrames: 'none',
  redactPaths: 'none',
  includeCauses: 'none',
  maxCauseDepth: 16,
  sanitizeMessage: false,
  classFilter: undefined,
};

/**
 * Normalize constructor `errorStack` options once.
 * Returns `undefined` for any non-object input (`null`, `undefined`, strings).
 */
export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (input === null || typeof input !== 'object') {
    return undefined;
  }

  const raw = input as ErrorStackOptions;

  let mode: ErrorStackMode = 'off';
  if (typeof raw.mode === 'string' && MODES.has(raw.mode)) {
    mode = raw.mode as ErrorStackMode;
  }

  let maxStackLines: number | undefined;
  if (raw.maxStackLines !== undefined) {
    if (!Number.isInteger(raw.maxStackLines) || raw.maxStackLines <= 0) {
      mode = 'off';
      maxStackLines = undefined;
    } else {
      maxStackLines = raw.maxStackLines;
    }
  }

  let includeCauses: IncludeCausesMode = 'none';
  if (
    typeof raw.includeCauses === 'string' &&
    INCLUDE_CAUSES.has(raw.includeCauses)
  ) {
    includeCauses = raw.includeCauses as IncludeCausesMode;
  }

  let maxCauseDepth = 16;
  if (raw.maxCauseDepth !== undefined) {
    if (!Number.isInteger(raw.maxCauseDepth)) {
      includeCauses = 'none';
    } else {
      maxCauseDepth = raw.maxCauseDepth;
    }
  }

  let stripInternalFrames: StripInternalFramesMode = 'none';
  if (
    typeof raw.stripInternalFrames === 'string' &&
    STRIP_INTERNAL_FRAMES.has(raw.stripInternalFrames)
  ) {
    stripInternalFrames = raw.stripInternalFrames as StripInternalFramesMode;
  }

  let redactPaths: RedactPathsMode = 'none';
  if (typeof raw.redactPaths === 'string' && REDACT_PATHS.has(raw.redactPaths)) {
    redactPaths = raw.redactPaths as RedactPathsMode;
  }

  return {
    mode,
    normalizeNewlines: raw.normalizeNewlines === true,
    trimLeadingWhitespace: raw.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames,
    redactPaths,
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: raw.sanitizeMessage === true,
    classFilter: normalizeClassFilter(raw.classFilter),
  };
}

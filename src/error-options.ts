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
  classFilter?: string[] | string;
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
  classFilter: string[] | undefined;
}

const oneOf = <T extends string>(v: unknown, allowed: T[], fallback: T): T =>
  allowed.includes(v as T) ? (v as T) : fallback;

export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (input === null || typeof input !== 'object' || Array.isArray(input)) {
    return undefined;
  }
  const o = input as ErrorStackOptions;

  let mode = oneOf<ErrorStackMode>(o.mode, ['off', 'string', 'frames'], 'off');

  let maxStackLines: number | undefined;
  if (o.maxStackLines !== undefined) {
    if (Number.isInteger(o.maxStackLines) && o.maxStackLines > 0) {
      maxStackLines = o.maxStackLines;
    } else {
      mode = 'off';
    }
  }

  let includeCauses = oneOf<IncludeCauses>(
    o.includeCauses,
    ['none', 'direct', 'deep'],
    'none'
  );
  let maxCauseDepth = 16;
  if (o.maxCauseDepth !== undefined) {
    if (Number.isInteger(o.maxCauseDepth)) {
      maxCauseDepth = o.maxCauseDepth;
    } else {
      includeCauses = 'none';
    }
  }

  const rawFilter =
    typeof o.classFilter === 'string' ? [o.classFilter] : o.classFilter;
  const classFilter = Array.isArray(rawFilter)
    ? rawFilter.filter(n => typeof n === 'string')
    : [];

  return {
    mode,
    normalizeNewlines: o.normalizeNewlines === true,
    trimLeadingWhitespace: o.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames: oneOf<StripInternalFrames>(
      o.stripInternalFrames,
      ['none', 'node', 'superjson', 'node_and_superjson'],
      'none'
    ),
    redactPaths: oneOf<RedactPaths>(
      o.redactPaths,
      ['none', 'basename', 'strip_cwd'],
      'none'
    ),
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: o.sanitizeMessage === true,
    classFilter: classFilter.length ? classFilter : undefined,
  };
}

export function matchesClassFilter(
  options: NormalizedErrorStackOptions,
  name: unknown
): boolean {
  return !options.classFilter || options.classFilter.includes(name as string);
}

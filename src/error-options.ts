export type ErrorStackMode = 'off' | 'string' | 'frames';
export type StackPathMode = 'none' | 'basename' | 'strip_cwd';
export type StripInternalFrames =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';
export type IncludeCauses = 'none' | 'direct' | 'deep';

export interface ErrorStackOptions {
  mode: ErrorStackMode;
  normalizeNewlines: boolean;
  trimLeadingWhitespace: boolean;
  maxStackLines?: number;
  stripInternalFrames: StripInternalFrames;
  redactPaths: StackPathMode;
  includeCauses: IncludeCauses;
  maxCauseDepth: number;
  sanitizeMessage: boolean;
  classFilter?: string[];
}

export type ErrorStackOptionsInput = Partial<
  Omit<ErrorStackOptions, 'classFilter'>
> & { classFilter?: string | string[] };

export function normalizeErrorStackOptions(
  input: unknown
): ErrorStackOptions | undefined {
  if (typeof input !== 'object' || input === null) return undefined;
  const value = input as ErrorStackOptionsInput;
  const mode =
    value.mode === 'string' || value.mode === 'frames' || value.mode === 'off'
      ? value.mode
      : 'off';
  const maxStackLines =
    Number.isInteger(value.maxStackLines) && (value.maxStackLines as number) > 0
      ? value.maxStackLines
      : value.maxStackLines === undefined
        ? undefined
        : undefined;
  const invalidCauseDepth =
    value.maxCauseDepth !== undefined && !Number.isInteger(value.maxCauseDepth);
  const maxCauseDepth =
    value.maxCauseDepth === undefined
      ? 16
      : (value.maxCauseDepth as number);
  const classFilter =
    value.classFilter === undefined
      ? undefined
      : Array.isArray(value.classFilter)
        ? value.classFilter.filter(v => typeof v === 'string')
        : typeof value.classFilter === 'string'
          ? [value.classFilter]
          : [];

  return {
    mode:
      maxStackLines === undefined && value.maxStackLines !== undefined
        ? 'off'
        : mode,
    normalizeNewlines: value.normalizeNewlines === true,
    trimLeadingWhitespace: value.trimLeadingWhitespace !== false,
    maxStackLines,
    stripInternalFrames:
      value.stripInternalFrames === 'node' ||
      value.stripInternalFrames === 'superjson' ||
      value.stripInternalFrames === 'node_and_superjson'
        ? value.stripInternalFrames
        : 'none',
    redactPaths:
      value.redactPaths === 'basename' || value.redactPaths === 'strip_cwd'
        ? value.redactPaths
        : 'none',
    includeCauses:
      !invalidCauseDepth &&
      (value.includeCauses === 'direct' || value.includeCauses === 'deep')
        ? value.includeCauses
        : 'none',
    maxCauseDepth,
    sanitizeMessage: value.sanitizeMessage === true,
    classFilter: classFilter?.length ? classFilter : undefined,
  };
}

export interface ErrorStackOptions {
  mode?: 'off' | 'string' | 'frames';
  normalizeNewlines?: boolean;
  trimLeadingWhitespace?: boolean;
  maxStackLines?: number;
  stripInternalFrames?: 'none' | 'node' | 'superjson' | 'node_and_superjson';
  redactPaths?: 'none' | 'basename' | 'strip_cwd';
  includeCauses?: 'none' | 'direct' | 'deep';
  maxCauseDepth?: number;
  sanitizeMessage?: boolean;
  classFilter?: string[];
}

export interface NormalizedErrorStackOptions {
  mode: 'off' | 'string' | 'frames';
  normalizeNewlines: boolean;
  trimLeadingWhitespace: boolean;
  maxStackLines?: number;
  stripInternalFrames: 'none' | 'node' | 'superjson' | 'node_and_superjson';
  redactPaths: 'none' | 'basename' | 'strip_cwd';
  includeCauses: 'none' | 'direct' | 'deep';
  maxCauseDepth: number;
  sanitizeMessage: boolean;
  classFilter?: string[];
}

const STRIP_MODES = new Set([
  'none',
  'node',
  'superjson',
  'node_and_superjson',
]);

const REDACT_MODES = new Set(['none', 'basename', 'strip_cwd']);

const CAUSE_MODES = new Set(['none', 'direct', 'deep']);

function isPositiveInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value > 0;
}

function isInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value);
}

export function normalizeErrorStackOptions(
  input: unknown
): NormalizedErrorStackOptions | undefined {
  if (input === null || typeof input !== 'object') {
    return undefined;
  }

  const raw = input as Record<string, unknown>;

  let mode: NormalizedErrorStackOptions['mode'] =
    raw.mode === 'off' || raw.mode === 'string' || raw.mode === 'frames'
      ? raw.mode
      : 'off';

  let maxStackLines: number | undefined;
  if (raw.maxStackLines !== undefined) {
    if (!isPositiveInteger(raw.maxStackLines)) {
      mode = 'off';
    } else {
      maxStackLines = raw.maxStackLines;
    }
  }

  let includeCauses: NormalizedErrorStackOptions['includeCauses'] =
    typeof raw.includeCauses === 'string' && CAUSE_MODES.has(raw.includeCauses)
      ? (raw.includeCauses as NormalizedErrorStackOptions['includeCauses'])
      : 'none';

  let maxCauseDepth = 16;
  if (raw.maxCauseDepth !== undefined) {
    if (!isInteger(raw.maxCauseDepth)) {
      includeCauses = 'none';
    } else {
      maxCauseDepth = raw.maxCauseDepth;
    }
  }

  const stripInternalFrames =
    typeof raw.stripInternalFrames === 'string' &&
    STRIP_MODES.has(raw.stripInternalFrames)
      ? (raw.stripInternalFrames as NormalizedErrorStackOptions['stripInternalFrames'])
      : 'none';

  const redactPaths =
    typeof raw.redactPaths === 'string' && REDACT_MODES.has(raw.redactPaths)
      ? (raw.redactPaths as NormalizedErrorStackOptions['redactPaths'])
      : 'none';

  let classFilter: string[] | undefined;
  if (Array.isArray(raw.classFilter)) {
    const names = raw.classFilter.filter(
      (entry): entry is string => typeof entry === 'string'
    );
    if (names.length > 0) {
      classFilter = [...names];
    }
  }

  const normalized: NormalizedErrorStackOptions = {
    mode,
    normalizeNewlines: raw.normalizeNewlines === true,
    trimLeadingWhitespace:
      typeof raw.trimLeadingWhitespace === 'boolean'
        ? raw.trimLeadingWhitespace
        : true,
    stripInternalFrames,
    redactPaths,
    includeCauses,
    maxCauseDepth,
    sanitizeMessage: raw.sanitizeMessage === true,
  };

  if (maxStackLines !== undefined) {
    normalized.maxStackLines = maxStackLines;
  }
  if (classFilter) {
    normalized.classFilter = classFilter;
  }

  return normalized;
}

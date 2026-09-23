export interface StackFrame {
  raw: string;
}

export interface StackProcessOptions {
  normalizeNewlines?: boolean;
  trimLeadingWhitespace?: boolean;
  maxStackLines?: number;
  stripInternalFrames?: string;
  redactPaths?: string;
}

type StripInternalFrames = 'none' | 'node' | 'superjson' | 'node_and_superjson';
type RedactPaths = 'none' | 'basename' | 'strip_cwd';

const SUPERJSON_FRAME_MARKERS = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
];

/**
 * Filesystem / runtime locations inside a stack line.
 * URL paths (`https://…`) are left alone; `file://` and `node:` are included.
 */
const PATH_SEGMENT = '[\\w.@%+~[\\]-]';
const PATH_RE = new RegExp(
  [
    `file:\\/\\/\\/(?:${PATH_SEGMENT}+\\/)*${PATH_SEGMENT}+(?::\\d+(?::\\d+)?)?`,
    `(?<![\\w])node:(?:${PATH_SEGMENT}+\\/)+${PATH_SEGMENT}+(?::\\d+(?::\\d+)?)?`,
    `[A-Za-z]:[\\\\/](?:${PATH_SEGMENT}+[\\\\/])*${PATH_SEGMENT}+(?::\\d+(?::\\d+)?)?`,
    `\\.{1,2}[\\\\/](?:${PATH_SEGMENT}+[\\\\/])*${PATH_SEGMENT}+(?::\\d+(?::\\d+)?)?`,
    `(?<![:\\w/.])\\/(?:${PATH_SEGMENT}+\\/)*${PATH_SEGMENT}+(?::\\d+(?::\\d+)?)?`,
    `(?<![:\\w/.])(?:${PATH_SEGMENT}+[\\\\/])+${PATH_SEGMENT}+(?::\\d+(?::\\d+)?)?`,
  ].join('|'),
  'g'
);

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function oneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
  fallback: T
): T {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback;
}

function resolveStrip(value: unknown): StripInternalFrames {
  return oneOf(
    value,
    ['none', 'node', 'superjson', 'node_and_superjson'] as const,
    'none'
  );
}

function resolveRedact(value: unknown): RedactPaths {
  return oneOf(value, ['none', 'basename', 'strip_cwd'] as const, 'none');
}

function splitSuffix(token: string): { path: string; suffix: string } {
  const match = token.match(/:(\d+)(?::(\d+))?$/);
  if (!match || match.index === undefined) {
    return { path: token, suffix: '' };
  }
  return {
    path: token.slice(0, match.index),
    suffix: token.slice(match.index),
  };
}

function basenameOf(path: string): string {
  const parts = path.split(/[\\/]/);
  const base = parts[parts.length - 1];
  return base || path;
}

function startsWithPathPrefix(path: string, prefix: string): boolean {
  if (prefix.length === 0 || path.length <= prefix.length) {
    return false;
  }
  const head = path.slice(0, prefix.length);
  const matches =
    process.platform === 'win32'
      ? head.toLowerCase() === prefix.toLowerCase()
      : head === prefix;
  if (!matches) {
    return false;
  }
  const next = path[prefix.length];
  return next === '/' || next === '\\';
}

function stripCwdPrefix(path: string): string {
  const cwd = process.cwd().replace(/[\\/]+$/, '');
  const cwdPosix = cwd.replace(/\\/g, '/');
  const prefixes = [cwd, cwdPosix, cwd.replace(/\//g, '\\'), 'file://' + cwdPosix];
  if (!cwdPosix.startsWith('/')) {
    prefixes.push('file:///' + cwdPosix);
  }

  for (let i = 0; i < prefixes.length; i++) {
    const prefix = prefixes[i];
    if (startsWithPathPrefix(path, prefix)) {
      return path.slice(prefix.length).replace(/^[\\/]/, '');
    }
  }
  return path;
}

function redactToken(token: string, mode: RedactPaths): string {
  if (mode === 'none') {
    return token;
  }
  const { path, suffix } = splitSuffix(token);
  if (mode === 'basename') {
    return basenameOf(path) + suffix;
  }
  return stripCwdPrefix(path) + suffix;
}

function redactLine(line: string, mode: RedactPaths): string {
  if (mode === 'none' || line.length === 0) {
    return line;
  }
  PATH_RE.lastIndex = 0;
  return line.replace(PATH_RE, token => redactToken(token, mode));
}

function isInternalFrame(line: string, mode: StripInternalFrames): boolean {
  if (mode === 'none') {
    return false;
  }
  const stripNode = mode === 'node' || mode === 'node_and_superjson';
  const stripSuperjson = mode === 'superjson' || mode === 'node_and_superjson';
  if (stripNode && line.includes('node:internal')) {
    return true;
  }
  if (stripSuperjson) {
    for (let i = 0; i < SUPERJSON_FRAME_MARKERS.length; i++) {
      if (line.includes(SUPERJSON_FRAME_MARKERS[i])) {
        return true;
      }
    }
  }
  return false;
}

function initialLines(stack: string, options: StackProcessOptions | undefined): string[] {
  const normalize = options?.normalizeNewlines === true;
  const text = normalize ? normalizeStackNewlines(stack) : stack;
  const lines = text.split('\n');
  const trim = options?.trimLeadingWhitespace !== false;
  if (trim) {
    for (let i = 1; i < lines.length; i++) {
      lines[i] = lines[i].replace(/^\s+/, '');
    }
  }
  return lines;
}

function limitLines(lines: string[], max: number | undefined): string[] {
  if (max == null || !Number.isInteger(max) || max <= 0) {
    return lines;
  }
  return lines.slice(0, max);
}

function stripLines(lines: string[], mode: StripInternalFrames): string[] {
  if (mode === 'none' || lines.length === 0) {
    return lines;
  }
  const kept: string[] = [lines[0]];
  for (let i = 1; i < lines.length; i++) {
    if (!isInternalFrame(lines[i], mode)) {
      kept.push(lines[i]);
    }
  }
  return kept;
}

function redactLines(lines: string[], mode: RedactPaths): string[] {
  if (mode === 'none') {
    return lines;
  }
  // The header line is the error message; path redaction applies to frames.
  return lines.map((line, index) =>
    index === 0 ? line : redactLine(line, mode)
  );
}

function processLines(
  stack: string,
  options: StackProcessOptions | undefined,
  order: 'string' | 'frames'
): string[] {
  let lines = initialLines(stack, options);
  const strip = resolveStrip(options?.stripInternalFrames);
  const redact = resolveRedact(options?.redactPaths);
  const max = options?.maxStackLines;

  if (order === 'string') {
    // normalizeNewlines -> trimLeadingWhitespace -> redactPaths -> maxStackLines -> stripInternalFrames
    lines = redactLines(lines, redact);
    lines = limitLines(lines, max);
    lines = stripLines(lines, strip);
  } else {
    // normalizeNewlines -> trimLeadingWhitespace -> stripInternalFrames -> redactPaths -> maxStackLines
    lines = stripLines(lines, strip);
    lines = redactLines(lines, redact);
    lines = limitLines(lines, max);
  }
  return lines;
}

export function processStackString(
  stack: string,
  options?: StackProcessOptions
): string {
  return processLines(stack, options, 'string').join('\n');
}

export function processStackFrames(
  stack: string,
  options?: StackProcessOptions
): StackFrame[] {
  return processLines(stack, options, 'frames').map(raw => ({ raw }));
}

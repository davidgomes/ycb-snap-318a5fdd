import type {
  ErrorStackOptions,
  RedactPaths,
  StripInternalFrames,
} from './error-options.js';

export type StackProcessingOptions = Pick<
  ErrorStackOptions,
  | 'normalizeNewlines'
  | 'trimLeadingWhitespace'
  | 'maxStackLines'
  | 'stripInternalFrames'
  | 'redactPaths'
>;

export interface StackFrame {
  raw: string;
}

const NODE_INTERNAL_MARKERS = ['node:internal'];
const SUPERJSON_INTERNAL_MARKERS = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
  'src\\transformer.ts',
  'src\\plainer.ts',
  'src\\index.ts',
];

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n?/g, '\n');
}

function internalMarkers(strip: StripInternalFrames | undefined): string[] {
  switch (strip) {
    case 'node':
      return NODE_INTERNAL_MARKERS;
    case 'superjson':
      return SUPERJSON_INTERNAL_MARKERS;
    case 'node_and_superjson':
      return [...NODE_INTERNAL_MARKERS, ...SUPERJSON_INTERNAL_MARKERS];
    default:
      return [];
  }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function currentWorkingDirectory(): string | undefined {
  try {
    const proc = (globalThis as any).process;
    const cwd = typeof proc?.cwd === 'function' ? proc.cwd() : undefined;
    return typeof cwd === 'string' && cwd.length > 0 ? cwd : undefined;
  } catch {
    return undefined;
  }
}

// Matches the directory part of an absolute path or URL that starts a token,
// e.g. `/home/me/app/src/` in `(/home/me/app/src/a.ts:1:2)`.
const DIRECTORY_PREFIX = /(^|[\s(@])(?:[A-Za-z][A-Za-z0-9+.-]*:\/\/[^\s/()]*)?(?:[A-Za-z]:)?[\\/][^\s()]*[\\/]/g;

function redactLine(line: string, redact: RedactPaths | undefined): string {
  if (redact === 'basename') {
    return line.replace(DIRECTORY_PREFIX, '$1');
  }
  if (redact === 'strip_cwd') {
    const cwd = currentWorkingDirectory()?.replace(/[\\/]+$/, '');
    if (!cwd) return line;
    return line.replace(
      new RegExp('(?:file://)?' + escapeRegExp(cwd) + '[\\\\/]', 'g'),
      ''
    );
  }
  return line;
}

function splitLines(stack: string, normalizeNewlines: boolean | undefined) {
  return (normalizeNewlines ? normalizeStackNewlines(stack) : stack).split(
    '\n'
  );
}

function trimLines(lines: string[], trim: boolean | undefined): string[] {
  if (trim === false) return lines;
  return lines.map((line, i) => (i === 0 ? line : line.replace(/^\s+/, '')));
}

function redactLines(lines: string[], redact: RedactPaths | undefined) {
  if (!redact || redact === 'none') return lines;
  return lines.map((line, i) => (i === 0 ? line : redactLine(line, redact)));
}

function limitLines(lines: string[], max: number | undefined): string[] {
  if (typeof max !== 'number' || !Number.isInteger(max) || max <= 0) {
    return lines;
  }
  return lines.slice(0, max);
}

function stripLines(lines: string[], strip: StripInternalFrames | undefined) {
  const markers = internalMarkers(strip);
  if (markers.length === 0) return lines;
  return lines.filter(
    (line, i) => i === 0 || !markers.some(marker => line.includes(marker))
  );
}

export function processStackString(
  stack: string,
  options: StackProcessingOptions = {}
): string {
  if (typeof stack !== 'string') return stack;

  let lines = splitLines(stack, options.normalizeNewlines);
  lines = trimLines(lines, options.trimLeadingWhitespace);
  lines = redactLines(lines, options.redactPaths);
  lines = limitLines(lines, options.maxStackLines);
  lines = stripLines(lines, options.stripInternalFrames);
  return lines.join('\n');
}

export function processStackFrames(
  stack: string,
  options: StackProcessingOptions = {}
): StackFrame[] {
  if (typeof stack !== 'string') return [];

  let lines = splitLines(stack, options.normalizeNewlines);
  lines = trimLines(lines, options.trimLeadingWhitespace);
  lines = stripLines(lines, options.stripInternalFrames);
  lines = redactLines(lines, options.redactPaths);
  lines = limitLines(lines, options.maxStackLines);
  return lines.map(raw => ({ raw }));
}

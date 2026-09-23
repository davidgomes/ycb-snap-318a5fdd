import {
  ErrorStackOptions,
  isPositiveInteger,
  RedactPaths,
  resolveRedactPaths,
  resolveStripInternalFrames,
  StripInternalFrames,
} from './error-options.js';

export interface StackFrame {
  raw: string;
}

export type StackProcessingOptions = Pick<
  ErrorStackOptions,
  | 'normalizeNewlines'
  | 'trimLeadingWhitespace'
  | 'maxStackLines'
  | 'stripInternalFrames'
  | 'redactPaths'
>;

const NODE_INTERNAL_MARKER = 'node:internal';
const SUPERJSON_SOURCE_FILES = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
];

// Directory segments of a path-like token, e.g. `/a/b/`, `C:\a\`, `file:///a/`
// or `node:internal/modules/`. Anchored to a token boundary to stay linear.
const DIRECTORY_PREFIX = /(^|[\s(])(?:[^\s()\\/]*[\\/])+/g;

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n?/g, '\n');
}

function mapFrames(lines: string[], fn: (line: string) => string): string[] {
  return lines.map((line, index) => (index === 0 ? line : fn(line)));
}

function toLines(stack: string, options: StackProcessingOptions): string[] {
  const text =
    options.normalizeNewlines === true ? normalizeStackNewlines(stack) : stack;
  const lines = text.split('\n');

  return options.trimLeadingWhitespace === false
    ? lines
    : mapFrames(lines, line => line.replace(/^\s+/, ''));
}

function stripFrames(lines: string[], strip: StripInternalFrames): string[] {
  const stripNode = strip === 'node' || strip === 'node_and_superjson';
  const stripSuperjson =
    strip === 'superjson' || strip === 'node_and_superjson';

  if (!stripNode && !stripSuperjson) {
    return lines;
  }

  return lines.filter(
    (line, index) =>
      index === 0 ||
      !(
        (stripNode && line.includes(NODE_INTERNAL_MARKER)) ||
        (stripSuperjson &&
          SUPERJSON_SOURCE_FILES.some(file => line.includes(file)))
      )
  );
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function currentWorkingDirectory(): string {
  try {
    return typeof process !== 'undefined' && typeof process.cwd === 'function'
      ? process.cwd()
      : '';
  } catch {
    return '';
  }
}

function cwdPrefixPattern(cwd: string): RegExp {
  const separator = cwd.includes('\\') && !cwd.includes('/') ? '\\' : '/';
  const prefix = /[\\/]$/.test(cwd) ? cwd : cwd + separator;
  return new RegExp('(^|[\\s(@])(?:file://)?' + escapeRegExp(prefix), 'g');
}

function redactFrames(lines: string[], redact: RedactPaths): string[] {
  if (redact === 'basename') {
    return mapFrames(lines, line => line.replace(DIRECTORY_PREFIX, '$1'));
  }

  if (redact === 'strip_cwd') {
    const cwd = currentWorkingDirectory();
    if (!cwd) {
      return lines;
    }
    const pattern = cwdPrefixPattern(cwd);
    return mapFrames(lines, line => line.replace(pattern, '$1'));
  }

  return lines;
}

function limitLines(lines: string[], maxStackLines: unknown): string[] {
  return isPositiveInteger(maxStackLines)
    ? lines.slice(0, maxStackLines)
    : lines;
}

export function processStackString(
  stack: string,
  options: StackProcessingOptions = {}
): string {
  let lines = toLines(stack, options);
  lines = redactFrames(lines, resolveRedactPaths(options.redactPaths));
  lines = limitLines(lines, options.maxStackLines);
  lines = stripFrames(
    lines,
    resolveStripInternalFrames(options.stripInternalFrames)
  );
  return lines.join('\n');
}

export function processStackFrames(
  stack: string,
  options: StackProcessingOptions = {}
): StackFrame[] {
  let lines = toLines(stack, options);
  lines = stripFrames(
    lines,
    resolveStripInternalFrames(options.stripInternalFrames)
  );
  lines = redactFrames(lines, resolveRedactPaths(options.redactPaths));
  lines = limitLines(lines, options.maxStackLines);
  return lines.map(raw => ({ raw }));
}

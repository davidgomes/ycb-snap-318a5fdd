import type {
  RedactPaths,
  StripInternalFrames,
} from './error-options.js';

export interface StackProcessOptions {
  normalizeNewlines?: boolean;
  trimLeadingWhitespace?: boolean;
  maxStackLines?: number;
  stripInternalFrames?: string;
  redactPaths?: string;
}

const SUPERJSON_FRAME_MARKERS = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
];

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function splitStack(
  stack: string,
  normalizeNewlines: boolean
): { lines: string[]; newline: string } {
  const text = normalizeNewlines ? normalizeStackNewlines(stack) : stack;
  const newline = normalizeNewlines
    ? '\n'
    : text.includes('\r\n')
      ? '\r\n'
      : text.includes('\r')
        ? '\r'
        : '\n';
  return {
    lines: text.split(/\r\n|\r|\n/),
    newline,
  };
}

function trimNonHeaderLines(lines: string[], enabled: boolean): string[] {
  if (!enabled) {
    return lines;
  }

  return lines.map((line, index) =>
    index === 0 ? line : line.replace(/^\s+/, '')
  );
}

function normalizeStrip(value: string | undefined): StripInternalFrames {
  if (
    value === 'node' ||
    value === 'superjson' ||
    value === 'node_and_superjson' ||
    value === 'none'
  ) {
    return value;
  }
  return 'none';
}

function normalizeRedact(value: string | undefined): RedactPaths {
  if (value === 'basename' || value === 'strip_cwd' || value === 'none') {
    return value;
  }
  return 'none';
}

function isNodeInternalFrame(line: string): boolean {
  return line.replace(/\\/g, '/').includes('node:internal');
}

function isSuperjsonFrame(line: string): boolean {
  const normalized = line.replace(/\\/g, '/');
  return SUPERJSON_FRAME_MARKERS.some(marker => normalized.includes(marker));
}

function isInternalFrame(line: string, mode: StripInternalFrames): boolean {
  if (mode === 'node') {
    return isNodeInternalFrame(line);
  }
  if (mode === 'superjson') {
    return isSuperjsonFrame(line);
  }
  if (mode === 'node_and_superjson') {
    return isNodeInternalFrame(line) || isSuperjsonFrame(line);
  }
  return false;
}

function stripInternalFrames(
  lines: string[],
  mode: StripInternalFrames
): string[] {
  if (mode === 'none' || lines.length === 0) {
    return lines;
  }

  const [header, ...frames] = lines;
  return [header, ...frames.filter(line => !isInternalFrame(line, mode))];
}

function stripFileUrl(path: string): string {
  if (!path.startsWith('file://')) {
    return path;
  }

  let rest = path.slice('file://'.length);
  if (/^\/[A-Za-z]:\//.test(rest)) {
    rest = rest.slice(1);
  }
  return rest;
}

function basenamePath(path: string): string {
  const normalized = stripFileUrl(path).replace(/\\/g, '/');
  const slash = normalized.lastIndexOf('/');
  return slash === -1 ? path : normalized.slice(slash + 1);
}

function stripCwdPrefix(path: string): string {
  const body = stripFileUrl(path).replace(/\\/g, '/');
  const cwd = process.cwd().replace(/\\/g, '/').replace(/\/$/, '');
  if (!cwd) {
    return path;
  }
  if (body === cwd) {
    return '';
  }
  if (body.startsWith(cwd + '/')) {
    return body.slice(cwd.length + 1);
  }
  return path;
}

function redactPath(path: string, mode: Exclude<RedactPaths, 'none'>): string {
  return mode === 'basename' ? basenamePath(path) : stripCwdPrefix(path);
}

function redactLocationToken(
  token: string,
  mode: Exclude<RedactPaths, 'none'>
): string {
  const match = token.match(/^(.*?)(:\d+:\d+)$/);
  if (!match || !/[\\/]/.test(match[1])) {
    return token;
  }
  return redactPath(match[1], mode) + match[2];
}

function redactFrameLine(
  line: string,
  mode: Exclude<RedactPaths, 'none'>
): string {
  const open = line.lastIndexOf('(');
  if (open !== -1 && line.endsWith(')')) {
    const inside = line.slice(open + 1, -1);
    const redacted = redactLocationToken(inside, mode);
    if (redacted !== inside) {
      return line.slice(0, open + 1) + redacted + ')';
    }
  }

  return line.replace(
    /^(\s*at\s+)(.+)$/,
    (_match, prefix: string, rest: string) =>
      prefix + redactLocationToken(rest, mode)
  );
}

function redactLines(lines: string[], mode: RedactPaths): string[] {
  if (mode === 'none') {
    return lines;
  }

  return lines.map((line, index) =>
    index === 0 ? line : redactFrameLine(line, mode)
  );
}

function applyMaxStackLines(
  lines: string[],
  maxStackLines: number | undefined
): string[] {
  if (maxStackLines === undefined) {
    return lines;
  }
  if (!Number.isInteger(maxStackLines) || maxStackLines <= 0) {
    return [];
  }
  return lines.slice(0, maxStackLines);
}

function processLines(
  stack: string,
  options: StackProcessOptions | undefined,
  order: 'string' | 'frames'
): { lines: string[]; newline: string } {
  if (typeof stack !== 'string' || stack.length === 0) {
    return { lines: [], newline: '\n' };
  }

  const normalizeNewlines = options?.normalizeNewlines === true;
  const trimLeadingWhitespace = options?.trimLeadingWhitespace !== false;
  const stripMode = normalizeStrip(options?.stripInternalFrames);
  const redactMode = normalizeRedact(options?.redactPaths);
  const { lines: splitLines, newline } = splitStack(stack, normalizeNewlines);
  let lines = trimNonHeaderLines(splitLines, trimLeadingWhitespace);

  if (order === 'string') {
    lines = redactLines(lines, redactMode);
    lines = applyMaxStackLines(lines, options?.maxStackLines);
    lines = stripInternalFrames(lines, stripMode);
  } else {
    lines = stripInternalFrames(lines, stripMode);
    lines = redactLines(lines, redactMode);
    lines = applyMaxStackLines(lines, options?.maxStackLines);
  }

  return { lines, newline };
}

export function processStackString(
  stack: string,
  options?: StackProcessOptions
): string {
  const { lines, newline } = processLines(stack, options, 'string');
  return lines.join(newline);
}

export interface StackFrame {
  raw: string;
}

export function processStackFrames(
  stack: string,
  options?: StackProcessOptions
): StackFrame[] {
  const { lines } = processLines(stack, options, 'frames');
  return lines.map(raw => ({ raw }));
}

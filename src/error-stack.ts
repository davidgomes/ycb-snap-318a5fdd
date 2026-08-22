import {
  DEFAULT_ERROR_STACK_OPTIONS,
  NormalizedErrorStackOptions,
  RedactPathsMode,
  StripInternalFramesMode,
  normalizeErrorStackOptions,
} from './error-options.js';

export interface StackFrame {
  raw: string;
}

const SUPERJSON_INTERNAL_PATHS = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
];

export function normalizeStackNewlines(stack: string): string {
  if (typeof stack !== 'string') {
    return stack;
  }

  return stack.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function resolveOptions(options?: unknown): NormalizedErrorStackOptions {
  return (
    normalizeErrorStackOptions(options ?? {}) ?? DEFAULT_ERROR_STACK_OPTIONS
  );
}

function trimLeadingOnNonHeader(lines: string[]): string[] {
  if (lines.length === 0) {
    return lines;
  }

  const result = [lines[0]];
  for (let i = 1; i < lines.length; i++) {
    result.push(lines[i].replace(/^\s+/, ''));
  }
  return result;
}

function cwdVariants(): string[] {
  const cwd = typeof process !== 'undefined' && process.cwd ? process.cwd() : '';
  if (!cwd) {
    return [];
  }

  const forward = cwd.replace(/\\/g, '/');
  const backward = cwd.replace(/\//g, '\\');
  const variants = [cwd, forward, backward];

  for (const variant of [cwd, forward]) {
    variants.push('file://' + variant);
    variants.push('file:///' + variant.replace(/^\//, ''));
  }

  return variants.filter(Boolean);
}

function stripCwdFromLine(line: string): string {
  let result = line;
  const variants = cwdVariants();
  for (let i = 0; i < variants.length; i++) {
    const variant = variants[i];
    if (variant && result.indexOf(variant) !== -1) {
      result = result.split(variant).join('');
    }
  }
  return result;
}

function splitPathAndPosition(pathLike: string): {
  path: string;
  suffix: string;
} {
  const position = pathLike.match(/(:\d+)(:\d+)?$/);
  if (!position) {
    return { path: pathLike, suffix: '' };
  }

  return {
    path: pathLike.slice(0, -position[0].length),
    suffix: position[0],
  };
}

function basenameOfPath(pathLike: string): string {
  const { path, suffix } = splitPathAndPosition(pathLike);
  const withoutProtocol = path.replace(/^file:\/\//, '');
  const normalized = withoutProtocol.replace(/\\/g, '/');
  const parts = normalized.split('/');
  return (parts[parts.length - 1] || path) + suffix;
}

function applyBasename(line: string): string {
  const withParens = line.replace(/\(([^)]+)\)/g, (_full, inner: string) => {
    if (inner.indexOf('/') === -1 && inner.indexOf('\\') === -1) {
      return `(${inner})`;
    }
    return `(${basenameOfPath(inner)})`;
  });

  return withParens.replace(
    /(?:file:\/\/[^\s)]+|(?:[A-Za-z]:)?[/\\][^\s)]+)/g,
    basenameOfPath
  );
}

function redactLine(line: string, mode: RedactPathsMode): string {
  if (mode === 'none') {
    return line;
  }

  if (mode === 'strip_cwd') {
    return stripCwdFromLine(line);
  }

  return applyBasename(line);
}

function applyRedactPaths(lines: string[], mode: RedactPathsMode): string[] {
  if (mode === 'none') {
    return lines;
  }

  return lines.map(line => redactLine(line, mode));
}

function isInternalFrame(
  line: string,
  mode: StripInternalFramesMode
): boolean {
  if (mode === 'none') {
    return false;
  }

  const stripNode = mode === 'node' || mode === 'node_and_superjson';
  const stripSuperjson = mode === 'superjson' || mode === 'node_and_superjson';

  if (stripNode && line.indexOf('node:internal') !== -1) {
    return true;
  }

  if (stripSuperjson) {
    for (let i = 0; i < SUPERJSON_INTERNAL_PATHS.length; i++) {
      const marker = SUPERJSON_INTERNAL_PATHS[i];
      if (
        line.indexOf(marker) !== -1 ||
        line.indexOf(marker.replace(/\//g, '\\')) !== -1
      ) {
        return true;
      }
    }
  }

  return false;
}

function stripInternal(
  lines: string[],
  mode: StripInternalFramesMode
): string[] {
  if (mode === 'none' || lines.length <= 1) {
    return lines;
  }

  const result = [lines[0]];
  for (let i = 1; i < lines.length; i++) {
    if (!isInternalFrame(lines[i], mode)) {
      result.push(lines[i]);
    }
  }
  return result;
}

function applyMaxStackLines(
  lines: string[],
  maxStackLines: number | undefined
): string[] {
  if (
    maxStackLines === undefined ||
    !Number.isInteger(maxStackLines) ||
    maxStackLines < 0
  ) {
    return lines;
  }

  return lines.slice(0, maxStackLines);
}

function splitStackLines(stack: string): string[] {
  return stack.split('\n');
}

/**
 * Process a stack string (string mode).
 * Order: normalizeNewlines -> trimLeadingWhitespace -> redactPaths -> maxStackLines -> stripInternalFrames
 */
export function processStackString(stack: string, options?: unknown): string {
  if (typeof stack !== 'string') {
    return stack;
  }

  const opts = resolveOptions(options);
  let text = stack;

  if (opts.normalizeNewlines) {
    text = normalizeStackNewlines(text);
  }

  let lines = splitStackLines(text);

  if (opts.trimLeadingWhitespace) {
    lines = trimLeadingOnNonHeader(lines);
  }

  lines = applyRedactPaths(lines, opts.redactPaths);
  lines = applyMaxStackLines(lines, opts.maxStackLines);
  lines = stripInternal(lines, opts.stripInternalFrames);

  return lines.join('\n');
}

/**
 * Process a stack into `{ raw }` frames (frames mode).
 * The header line is the first entry.
 * Order: normalizeNewlines -> trimLeadingWhitespace -> stripInternalFrames -> redactPaths -> maxStackLines
 */
export function processStackFrames(
  stack: string,
  options?: unknown
): StackFrame[] {
  if (typeof stack !== 'string') {
    return [];
  }

  const opts = resolveOptions(options);
  let text = stack;

  if (opts.normalizeNewlines) {
    text = normalizeStackNewlines(text);
  }

  let lines = splitStackLines(text);

  if (opts.trimLeadingWhitespace) {
    lines = trimLeadingOnNonHeader(lines);
  }

  lines = stripInternal(lines, opts.stripInternalFrames);
  lines = applyRedactPaths(lines, opts.redactPaths);
  lines = applyMaxStackLines(lines, opts.maxStackLines);

  return lines.map(raw => ({ raw }));
}

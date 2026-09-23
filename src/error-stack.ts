import {
  ErrorStackOptions,
  NormalizedErrorStackOptions,
  normalizeErrorStackOptions,
  RedactPaths,
  StripInternalFrames,
} from './error-options.js';

export interface StackFrame {
  raw: string;
}

const SUPERJSON_INTERNAL_FILES = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
];

const defaultOptions = normalizeErrorStackOptions({})!;

function resolveOptions(
  options: ErrorStackOptions | NormalizedErrorStackOptions | undefined
): NormalizedErrorStackOptions {
  return normalizeErrorStackOptions(options) ?? defaultOptions;
}

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n?/g, '\n');
}

function splitStack(
  stack: string,
  options: NormalizedErrorStackOptions
): [string, string[]] {
  const text = options.normalizeNewlines ? normalizeStackNewlines(stack) : stack;
  const [header, ...frames] = text.split('\n');
  return [
    header,
    options.trimLeadingWhitespace
      ? frames.map(frame => frame.replace(/^\s+/, ''))
      : frames,
  ];
}

function isInternalFrame(frame: string, strip: StripInternalFrames): boolean {
  const stripNode = strip === 'node' || strip === 'node_and_superjson';
  const stripSuperjson =
    strip === 'superjson' || strip === 'node_and_superjson';

  if (stripNode && frame.includes('node:internal')) {
    return true;
  }
  if (stripSuperjson) {
    const posixFrame = frame.replace(/\\/g, '/');
    return SUPERJSON_INTERNAL_FILES.some(file => posixFrame.includes(file));
  }
  return false;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function currentWorkingDirectory(): string | undefined {
  try {
    return typeof process !== 'undefined' && typeof process.cwd === 'function'
      ? process.cwd()
      : undefined;
  } catch {
    return undefined;
  }
}

function stripCwd(frame: string): string {
  const cwd = currentWorkingDirectory();
  if (!cwd) {
    return frame;
  }
  const separator = cwd.includes('/') || !cwd.includes('\\') ? '/' : '\\';
  const prefix = /[\\/]$/.test(cwd) ? cwd : cwd + separator;
  // Only match at the start of a path token so e.g. `/app` never eats `/apple/`.
  const pattern = new RegExp(
    `(?<=^|[\\s(])(?:file://)?${escapeRegExp(prefix)}`,
    'g'
  );
  return frame.replace(pattern, '');
}

function redactFramePaths(frame: string, redact: RedactPaths): string {
  switch (redact) {
    case 'basename':
      return frame.replace(/[^\s()]*[\\/]/g, '');
    case 'strip_cwd':
      return stripCwd(frame);
    default:
      return frame;
  }
}

function limitLines(
  lines: string[],
  maxStackLines: number | undefined
): string[] {
  return maxStackLines === undefined ? lines : lines.slice(0, maxStackLines);
}

/**
 * Order: normalizeNewlines -> trimLeadingWhitespace -> redactPaths
 * -> maxStackLines -> stripInternalFrames. The header line is always kept.
 */
export function processStackString(
  stack: string,
  options?: ErrorStackOptions | NormalizedErrorStackOptions
): string {
  const resolved = resolveOptions(options);
  const [header, frames] = splitStack(stack, resolved);

  const [, ...kept] = limitLines(
    [
      header,
      ...frames.map(frame => redactFramePaths(frame, resolved.redactPaths)),
    ],
    resolved.maxStackLines
  );

  return [
    header,
    ...kept.filter(
      frame => !isInternalFrame(frame, resolved.stripInternalFrames)
    ),
  ].join('\n');
}

/**
 * Order: normalizeNewlines -> trimLeadingWhitespace -> stripInternalFrames
 * -> redactPaths -> maxStackLines. The header line is always the first frame.
 */
export function processStackFrames(
  stack: string,
  options?: ErrorStackOptions | NormalizedErrorStackOptions
): StackFrame[] {
  const resolved = resolveOptions(options);
  const [header, frames] = splitStack(stack, resolved);

  const kept = frames
    .filter(frame => !isInternalFrame(frame, resolved.stripInternalFrames))
    .map(frame => redactFramePaths(frame, resolved.redactPaths));

  return limitLines([header, ...kept], resolved.maxStackLines).map(raw => ({
    raw,
  }));
}

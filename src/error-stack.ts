export interface StackProcessOptions {
  normalizeNewlines?: boolean;
  trimLeadingWhitespace?: boolean;
  maxStackLines?: number;
  stripInternalFrames?: string;
  redactPaths?: string;
}

export type StripInternalFramesMode =
  | 'none'
  | 'node'
  | 'superjson'
  | 'node_and_superjson';

export type RedactPathsMode = 'none' | 'basename' | 'strip_cwd';

const SUPERJSON_FRAME_MARKERS = [
  'src/transformer.ts',
  'src/plainer.ts',
  'src/index.ts',
];

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function asStripMode(value: string | undefined): StripInternalFramesMode {
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

function asRedactMode(value: string | undefined): RedactPathsMode {
  if (value === 'basename' || value === 'strip_cwd' || value === 'none') {
    return value;
  }
  return 'none';
}

function splitStackLines(
  stack: string,
  normalizeNewlines: boolean
): { lines: string[]; newline: string } {
  if (normalizeNewlines) {
    return {
      lines: normalizeStackNewlines(stack).split('\n'),
      newline: '\n',
    };
  }

  const newline = stack.includes('\r\n')
    ? '\r\n'
    : stack.includes('\r')
      ? '\r'
      : '\n';

  return {
    lines: stack.split(/\r\n|\r|\n/),
    newline,
  };
}

function applyTrimLeadingWhitespace(
  lines: string[],
  trimLeadingWhitespace: boolean
): string[] {
  if (!trimLeadingWhitespace || lines.length === 0) {
    return lines;
  }

  const [header, ...rest] = lines;
  return [header!, ...rest.map(line => line.trimStart())];
}

function isNodeInternalFrame(line: string): boolean {
  return line.includes('node:internal');
}

function isSuperjsonFrame(line: string): boolean {
  return SUPERJSON_FRAME_MARKERS.some(marker => line.includes(marker));
}

function isInternalFrame(line: string, mode: StripInternalFramesMode): boolean {
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

function applyStripInternalFrames(
  lines: string[],
  mode: StripInternalFramesMode
): string[] {
  if (mode === 'none' || lines.length === 0) {
    return lines;
  }

  const header = lines[0]!;
  const rest = lines.slice(1).filter(line => !isInternalFrame(line, mode));
  return [header, ...rest];
}

function redactPath(path: string, mode: 'basename' | 'strip_cwd'): string {
  const filePrefix = 'file://';
  const bare = path.startsWith(filePrefix) ? path.slice(filePrefix.length) : path;

  if (mode === 'basename') {
    const segments = bare.split(/[/\\]/).filter(segment => segment.length > 0);
    return segments.length > 0 ? segments[segments.length - 1]! : bare;
  }

  const cwd = process.cwd().replace(/[\\/]+$/, '');
  if (bare === cwd) {
    return '';
  }
  if (bare.startsWith(`${cwd}/`) || bare.startsWith(`${cwd}\\`)) {
    return bare.slice(cwd.length + 1);
  }
  return path;
}

function redactLine(line: string, mode: RedactPathsMode): string {
  if (mode === 'none') {
    return line;
  }

  return line.replace(
    /(?:file:\/\/)?(?:\/|[A-Za-z]:[\\/])[^\s:()]+/g,
    match => redactPath(match, mode)
  );
}

function applyRedactPaths(lines: string[], mode: RedactPathsMode): string[] {
  if (mode === 'none') {
    return lines;
  }
  return lines.map(line => redactLine(line, mode));
}

function applyMaxStackLines(
  lines: string[],
  maxStackLines: number | undefined
): string[] {
  if (
    typeof maxStackLines !== 'number' ||
    !Number.isInteger(maxStackLines) ||
    maxStackLines <= 0
  ) {
    return lines;
  }
  return lines.slice(0, maxStackLines);
}

function resolvedLineOptions(options: StackProcessOptions | undefined): {
  normalizeNewlines: boolean;
  trimLeadingWhitespace: boolean;
  maxStackLines: number | undefined;
  stripInternalFrames: StripInternalFramesMode;
  redactPaths: RedactPathsMode;
} {
  return {
    normalizeNewlines: options?.normalizeNewlines === true,
    trimLeadingWhitespace: options?.trimLeadingWhitespace !== false,
    maxStackLines: options?.maxStackLines,
    stripInternalFrames: asStripMode(options?.stripInternalFrames),
    redactPaths: asRedactMode(options?.redactPaths),
  };
}

export function processStackString(
  stack: string,
  options?: StackProcessOptions
): string {
  const resolved = resolvedLineOptions(options);
  const { lines, newline } = splitStackLines(stack, resolved.normalizeNewlines);

  const trimmed = applyTrimLeadingWhitespace(
    lines,
    resolved.trimLeadingWhitespace
  );
  const redacted = applyRedactPaths(trimmed, resolved.redactPaths);
  const limited = applyMaxStackLines(redacted, resolved.maxStackLines);
  const stripped = applyStripInternalFrames(
    limited,
    resolved.stripInternalFrames
  );

  return stripped.join(newline);
}

export function processStackFrames(
  stack: string,
  options?: StackProcessOptions
): { raw: string }[] {
  const resolved = resolvedLineOptions(options);
  const { lines } = splitStackLines(stack, resolved.normalizeNewlines);

  const trimmed = applyTrimLeadingWhitespace(
    lines,
    resolved.trimLeadingWhitespace
  );
  const stripped = applyStripInternalFrames(
    trimmed,
    resolved.stripInternalFrames
  );
  const redacted = applyRedactPaths(stripped, resolved.redactPaths);
  const limited = applyMaxStackLines(redacted, resolved.maxStackLines);

  return limited.map(raw => ({ raw }));
}

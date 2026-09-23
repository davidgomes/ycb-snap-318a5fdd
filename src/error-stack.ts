import path from 'node:path';
import {
  NormalizedErrorStackOptions,
  RedactPaths,
  StripInternalFrames,
} from './error-options.js';

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function splitStackLines(
  stack: string,
  normalizeNewlines: boolean
): string[] {
  const text = normalizeNewlines ? normalizeStackNewlines(stack) : stack;
  if (text.length === 0) return [];
  return text.split('\n');
}

function applyTrim(lines: string[], trimLeadingWhitespace: boolean): string[] {
  if (!trimLeadingWhitespace) return lines;
  return lines.map((line, index) => (index === 0 ? line : line.trimStart()));
}

function isNodeInternalFrame(line: string): boolean {
  return line.includes('node:internal');
}

function isSuperjsonInternalFrame(line: string): boolean {
  return (
    line.includes('src/transformer.ts') ||
    line.includes('src/plainer.ts') ||
    line.includes('src/index.ts')
  );
}

function shouldStripFrame(line: string, mode: StripInternalFrames): boolean {
  if (mode === 'none') return false;
  if (mode === 'node') return isNodeInternalFrame(line);
  if (mode === 'superjson') return isSuperjsonInternalFrame(line);
  return isNodeInternalFrame(line) || isSuperjsonInternalFrame(line);
}

/** Drop internal frames. The first line is the header and is never removed. */
function stripInternalFrames(
  lines: string[],
  mode: StripInternalFrames
): string[] {
  if (lines.length === 0 || mode === 'none') return lines;
  const [header, ...frames] = lines;
  return [header, ...frames.filter(line => !shouldStripFrame(line, mode))];
}

function redactLocation(location: string, mode: Exclude<RedactPaths, 'none'>): string {
  const match = /^(.*):(\d+):(\d+)$/.exec(location);
  if (!match) return location;
  let file = match[1];
  const suffix = `:${match[2]}:${match[3]}`;

  if (mode === 'basename') {
    const withoutFileUrl = file.startsWith('file://')
      ? file.slice('file://'.length)
      : file;
    file = path.basename(withoutFileUrl);
    return file + suffix;
  }

  const cwd = process.cwd();
  const fileUrlPrefix = 'file://' + cwd;
  if (file.startsWith(fileUrlPrefix)) {
    file = file.slice(fileUrlPrefix.length).replace(/^[/\\]/, '');
  } else if (file.startsWith(cwd + path.sep)) {
    file = file.slice((cwd + path.sep).length);
  } else if (file === cwd) {
    file = '';
  }
  return file + suffix;
}

function redactLine(line: string, mode: RedactPaths): string {
  if (mode === 'none') return line;

  const paren = /^(.*)\(([^)]+)\)(.*)$/.exec(line);
  if (paren) {
    return (
      paren[1] + '(' + redactLocation(paren[2], mode) + ')' + paren[3]
    );
  }

  const at = /^(.*\bat\s+)(\S+)\s*$/.exec(line);
  if (at) {
    return at[1] + redactLocation(at[2], mode);
  }

  return line;
}

function redactLines(lines: string[], mode: RedactPaths): string[] {
  if (mode === 'none') return lines;
  return lines.map(line => redactLine(line, mode));
}

function limitLines(lines: string[], maxStackLines: number | undefined): string[] {
  if (maxStackLines === undefined) return lines;
  return lines.slice(0, maxStackLines);
}

type StackProcessOptions = Pick<
  NormalizedErrorStackOptions,
  | 'normalizeNewlines'
  | 'trimLeadingWhitespace'
  | 'maxStackLines'
  | 'stripInternalFrames'
  | 'redactPaths'
>;

function resolveProcessOptions(
  options?: Partial<StackProcessOptions>
): StackProcessOptions {
  return {
    normalizeNewlines: options?.normalizeNewlines === true,
    trimLeadingWhitespace: options?.trimLeadingWhitespace !== false,
    maxStackLines: options?.maxStackLines,
    stripInternalFrames:
      options?.stripInternalFrames === 'node' ||
      options?.stripInternalFrames === 'superjson' ||
      options?.stripInternalFrames === 'node_and_superjson'
        ? options.stripInternalFrames
        : 'none',
    redactPaths:
      options?.redactPaths === 'basename' || options?.redactPaths === 'strip_cwd'
        ? options.redactPaths
        : 'none',
  };
}

/**
 * String-mode pipeline:
 * normalizeNewlines -> trimLeadingWhitespace -> redactPaths -> maxStackLines -> stripInternalFrames
 */
export function processStackString(
  stack: string,
  options?: Partial<StackProcessOptions>
): string {
  const opts = resolveProcessOptions(options);
  let lines = splitStackLines(stack, opts.normalizeNewlines);
  lines = applyTrim(lines, opts.trimLeadingWhitespace);
  lines = redactLines(lines, opts.redactPaths);
  lines = limitLines(lines, opts.maxStackLines);
  lines = stripInternalFrames(lines, opts.stripInternalFrames);
  return lines.join('\n');
}

/**
 * Frames-mode pipeline:
 * normalizeNewlines -> trimLeadingWhitespace -> stripInternalFrames -> redactPaths -> maxStackLines
 */
export function processStackFrames(
  stack: string,
  options?: Partial<StackProcessOptions>
): { raw: string }[] {
  const opts = resolveProcessOptions(options);
  let lines = splitStackLines(stack, opts.normalizeNewlines);
  lines = applyTrim(lines, opts.trimLeadingWhitespace);
  lines = stripInternalFrames(lines, opts.stripInternalFrames);
  lines = redactLines(lines, opts.redactPaths);
  lines = limitLines(lines, opts.maxStackLines);
  return lines.map(raw => ({ raw }));
}

import { ErrorStackOptions } from './error-options.js';

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n?/g, '\n');
}

function trimLines(lines: string[], trim: boolean): string[] {
  return trim
    ? lines.map((line, index) => (index === 0 ? line : line.replace(/^\s+/, '')))
    : lines;
}

function redact(line: string, mode: ErrorStackOptions['redactPaths']): string {
  if (mode === 'basename') {
    return line.replace(/(?:[A-Za-z]:[\\/]|\/)[^()\s:]+[\\/]/g, '');
  }
  if (mode === 'strip_cwd') {
    const cwd = process.cwd().replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return line.replace(new RegExp(cwd + '[\\\\/]', 'g'), '');
  }
  return line;
}

function isInternal(line: string, mode: ErrorStackOptions['stripInternalFrames']) {
  return (
    (mode === 'node' || mode === 'node_and_superjson') &&
      line.includes('node:internal') ||
    (mode === 'superjson' || mode === 'node_and_superjson') &&
      /src\/(?:transformer|plainer|index)\.ts/.test(line)
  );
}

function limit(lines: string[], max?: number): string[] {
  return max === undefined ? lines : lines.slice(0, max);
}

export function processStackString(
  stack: string,
  options: ErrorStackOptions
): string {
  let lines = (options.normalizeNewlines ? normalizeStackNewlines(stack) : stack).split(
    '\n'
  );
  lines = trimLines(lines, options.trimLeadingWhitespace);
  lines = lines.map(line => redact(line, options.redactPaths));
  lines = limit(lines, options.maxStackLines);
  const header = lines[0];
  lines = [header, ...lines.slice(1).filter(line => !isInternal(line, options.stripInternalFrames))];
  return lines.join('\n');
}

export function processStackFrames(
  stack: string,
  options: ErrorStackOptions
): Array<{ raw: string }> {
  let lines = (options.normalizeNewlines ? normalizeStackNewlines(stack) : stack).split(
    '\n'
  );
  lines = trimLines(lines, options.trimLeadingWhitespace);
  lines = lines.filter((line, index) => index === 0 || !isInternal(line, options.stripInternalFrames));
  lines = lines.map(line => redact(line, options.redactPaths));
  lines = limit(lines, options.maxStackLines);
  return lines.map(raw => ({ raw }));
}

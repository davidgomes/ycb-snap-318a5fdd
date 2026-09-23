import { NormalizedErrorStackOptions } from './error-options.js';

type StackOptions = Pick<
  NormalizedErrorStackOptions,
  | 'normalizeNewlines'
  | 'trimLeadingWhitespace'
  | 'maxStackLines'
  | 'stripInternalFrames'
  | 'redactPaths'
>;

const SUPERJSON_FILES = ['src/transformer.ts', 'src/plainer.ts', 'src/index.ts'];

export function normalizeStackNewlines(stack: string): string {
  return stack.replace(/\r\n?/g, '\n');
}

function isInternal(
  line: string,
  mode: StackOptions['stripInternalFrames']
): boolean {
  const node =
    (mode === 'node' || mode === 'node_and_superjson') &&
    line.includes('node:internal');
  const sj =
    (mode === 'superjson' || mode === 'node_and_superjson') &&
    SUPERJSON_FILES.some(f => line.includes(f));
  return node || sj;
}

function stripFrames(lines: string[], opts: StackOptions): string[] {
  return lines.filter((l, i) => i === 0 || !isInternal(l, opts.stripInternalFrames));
}

function getCwd(): string | undefined {
  const p = (globalThis as any).process;
  return typeof p?.cwd === 'function' ? p.cwd() : undefined;
}

function redactLine(line: string, mode: StackOptions['redactPaths']): string {
  if (mode === 'basename') {
    return line.replace(
      /(?<=^|[\s(])(?:file:\/\/)?(?:[A-Za-z]:)?(?:[\\/][^\s()\\/]+)*[\\/]([^\s()\\/]+)/g,
      '$1'
    );
  }
  if (mode === 'strip_cwd') {
    const cwd = getCwd();
    if (!cwd) return line;
    return line.split(cwd + '/').join('').split(cwd + '\\').join('');
  }
  return line;
}

function redact(lines: string[], opts: StackOptions): string[] {
  return lines.map((l, i) => (i === 0 ? l : redactLine(l, opts.redactPaths)));
}

function limit(lines: string[], opts: StackOptions): string[] {
  return opts.maxStackLines === undefined
    ? lines
    : lines.slice(0, opts.maxStackLines);
}

function prepare(stack: string, opts: StackOptions): string[] {
  const s = opts.normalizeNewlines ? normalizeStackNewlines(stack) : stack;
  const lines = s.split('\n');
  return opts.trimLeadingWhitespace
    ? lines.map((l, i) => (i === 0 ? l : l.replace(/^\s+/, '')))
    : lines;
}

export function processStackString(stack: string, opts: StackOptions): string {
  let lines = prepare(stack, opts);
  lines = redact(lines, opts);
  lines = limit(lines, opts);
  lines = stripFrames(lines, opts);
  return lines.join('\n');
}

export function processStackFrames(
  stack: string,
  opts: StackOptions
): { raw: string }[] {
  let lines = prepare(stack, opts);
  lines = stripFrames(lines, opts);
  lines = redact(lines, opts);
  lines = limit(lines, opts);
  return lines.map(raw => ({ raw }));
}

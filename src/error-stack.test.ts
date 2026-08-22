import { describe, expect, it } from 'vitest';
import SuperJSON from './index.js';
import { ErrorClassRegistry } from './error-class-registry.js';
import { normalizeErrorStackOptions } from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import {
  normalizeStackNewlines,
  processStackFrames,
  processStackString,
} from './error-stack.js';

const sampleStack = [
  'Error: boom',
  '    at userFn (/home/app/src/user.ts:10:5)',
  '    at node:internal/modules/cjs/loader:1:1',
  '    at serialize (/home/app/src/transformer.ts:20:3)',
  '    at helper (/home/app/src/other.ts:3:1)',
].join('\n');

describe('normalizeErrorStackOptions', () => {
  it('returns undefined for non-object input', () => {
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
    expect(normalizeErrorStackOptions(1)).toBeUndefined();
    expect(normalizeErrorStackOptions(true)).toBeUndefined();
  });

  it('defaults missing or invalid mode to off', () => {
    expect(normalizeErrorStackOptions({})?.mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'nope' })?.mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'string' })?.mode).toBe('string');
    expect(normalizeErrorStackOptions({ mode: 'frames' })?.mode).toBe('frames');
    expect(normalizeErrorStackOptions({ mode: 'off' })?.mode).toBe('off');
  });

  it('treats invalid maxStackLines as mode=off', () => {
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 0 })?.mode
    ).toBe('off');
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: -2 })?.mode
    ).toBe('off');
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 1.5 })?.mode
    ).toBe('off');
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 3 })
        ?.maxStackLines
    ).toBe(3);
  });

  it('falls back includeCauses when maxCauseDepth is not an integer', () => {
    const opts = normalizeErrorStackOptions({
      includeCauses: 'deep',
      maxCauseDepth: 2.5,
    });
    expect(opts?.includeCauses).toBe('none');
  });

  it('defaults the remaining fields', () => {
    const opts = normalizeErrorStackOptions({ mode: 'string' });
    expect(opts).toMatchObject({
      normalizeNewlines: false,
      trimLeadingWhitespace: true,
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
      maxCauseDepth: 16,
      sanitizeMessage: false,
    });
    expect(opts?.classFilter).toBeUndefined();
  });

  it('falls back unknown strip/redact values', () => {
    const opts = normalizeErrorStackOptions({
      stripInternalFrames: 'wat',
      redactPaths: 'wat',
    });
    expect(opts?.stripInternalFrames).toBe('none');
    expect(opts?.redactPaths).toBe('none');
  });

  it('treats an omitted or empty classFilter as all errors', () => {
    expect(normalizeErrorStackOptions({ classFilter: '' })?.classFilter).toBeUndefined();
    expect(normalizeErrorStackOptions({ classFilter: [] })?.classFilter).toBeUndefined();
    expect(
      normalizeErrorStackOptions({ classFilter: 'TypeError' })?.classFilter
    ).toEqual(['TypeError']);
  });
});

describe('sanitizeMessage', () => {
  it('redacts urls, emails, and ipv4 addresses', () => {
    expect(
      sanitizeMessage(
        'see https://example.com/x and a@b.co and 10.0.0.1 plus http://foo.test'
      )
    ).toBe('see [redacted] and [redacted] and [redacted] plus [redacted]');
  });
});

describe('stack processors', () => {
  it('normalizes newlines', () => {
    expect(normalizeStackNewlines('a\r\nb\rc')).toBe('a\nb\nc');
  });

  it('keeps the header and applies string-mode order', () => {
    const processed = processStackString(sampleStack, {
      normalizeNewlines: true,
      trimLeadingWhitespace: true,
      redactPaths: 'basename',
      maxStackLines: 4,
      stripInternalFrames: 'node',
    });
    const lines = processed.split('\n');
    expect(lines[0]).toBe('Error: boom');
    expect(lines[1]).toBe('at userFn (user.ts:10:5)');
    expect(lines.some(line => line.includes('node:internal'))).toBe(false);
  });

  it('preserves leading whitespace when trimLeadingWhitespace is false', () => {
    const processed = processStackString(sampleStack, {
      trimLeadingWhitespace: false,
      maxStackLines: 2,
    });
    expect(processed.split('\n')[1].startsWith('    at ')).toBe(true);
  });

  it('strips the cwd prefix when redactPaths is strip_cwd', () => {
    const cwd = process.cwd();
    const stack = `Error: boom\n    at userFn (${cwd}/src/user.ts:10:5)`;
    const processed = processStackString(stack, {
      redactPaths: 'strip_cwd',
      trimLeadingWhitespace: true,
    });
    expect(processed.split('\n')[1]).toBe('at userFn (/src/user.ts:10:5)');
    expect(processed).not.toContain(cwd);
  });

  it('uses the header as the first frames entry', () => {
    const frames = processStackFrames(sampleStack, {
      trimLeadingWhitespace: true,
      stripInternalFrames: 'node_and_superjson',
      maxStackLines: 3,
    });
    expect(frames[0]).toEqual({ raw: 'Error: boom' });
    expect(frames.every(frame => typeof frame.raw === 'string')).toBe(true);
    expect(frames.some(frame => frame.raw.includes('node:internal'))).toBe(
      false
    );
    expect(frames.some(frame => frame.raw.includes('src/transformer.ts'))).toBe(
      false
    );
  });

  it('strips superjson frames without removing the header', () => {
    const processed = processStackString(sampleStack, {
      stripInternalFrames: 'superjson',
      trimLeadingWhitespace: true,
    });
    const lines = processed.split('\n');
    expect(lines[0]).toBe('Error: boom');
    expect(lines.some(line => line.includes('src/transformer.ts'))).toBe(false);
    expect(lines.some(line => line.includes('src/plainer.ts'))).toBe(false);
    expect(lines.some(line => line.includes('src/index.ts'))).toBe(false);
  });
});

describe('ErrorClassRegistry', () => {
  it('registers and looks up processors', () => {
    const registry = new ErrorClassRegistry();
    const fn = (value: Record<string, unknown>) => value;
    expect(registry.has('TypeError')).toBe(false);
    registry.register('TypeError', fn);
    expect(registry.has('TypeError')).toBe(true);
    expect(registry.getProcessor('TypeError')).toBe(fn);
    expect(registry.getProcessor('Error')).toBeUndefined();
  });
});

describe('SuperJSON errorStack option', () => {
  it('leaves existing Error behavior unchanged when omitted', () => {
    const sj = new SuperJSON();
    const input = new Error('epic fail', { cause: new Error('nested') });
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error', { cause: ['Error'] }]);
    expect((serialized.json as any).stack).toBeUndefined();
    expect((serialized.json as any).cause.message).toBe('nested');
  });

  it('never serializes stack data when mode is off, even if stack is allowed', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'off' } });
    sj.allowErrorProps('stack');
    const input = new Error('no stack');
    const serialized = sj.serialize(input);
    expect((serialized.json as any).stack).toBeUndefined();
    expect(serialized.meta?.values).toEqual(['Error']);
  });

  it('treats a missing or invalid mode like off', () => {
    const missing = new SuperJSON({ errorStack: {} });
    missing.allowErrorProps('stack');
    expect((missing.serialize(new Error('x')).json as any).stack).toBeUndefined();
    expect(missing.serialize(new Error('x')).meta?.values).toEqual(['Error']);

    const invalid = new SuperJSON({ errorStack: { mode: 'nope' } });
    invalid.allowErrorProps('stack');
    expect((invalid.serialize(new Error('x')).json as any).stack).toBeUndefined();
    expect(invalid.serialize(new Error('x')).meta?.values).toEqual(['Error']);
  });

  it('uses Error/stack when string mode matches', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'string' } });
    sj.allowErrorProps('stack');
    const input = new Error('string mode');
    input.stack = sampleStack;
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error/stack']);
    expect((serialized.json as any).stack.split('\n')[0]).toBe('Error: boom');
    expect((serialized.json as any).stack.split('\n')[1]).toBe(
      'at userFn (/home/app/src/user.ts:10:5)'
    );
  });

  it('uses Error/frames when frames mode matches', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'frames' } });
    sj.allowErrorProps('stackFrames');
    const input = new Error('frames mode');
    input.stack = sampleStack;
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error/frames']);
    expect((serialized.json as any).stackFrames[0]).toEqual({
      raw: 'Error: boom',
    });
    const parsed = sj.deserialize(serialized) as any;
    expect(parsed).toBeInstanceOf(Error);
    expect(parsed.stackFrames[0].raw).toBe('Error: boom');
  });

  it('uses Error/stack when classFilter matches', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'string', classFilter: ['TypeError'] },
    });
    sj.allowErrorProps('stack');
    const input = new TypeError('typed');
    input.stack = sampleStack.replace('Error: boom', 'TypeError: typed');
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error/stack']);
    expect((serialized.json as any).message).toBe('typed');
  });

  it('falls back to Error when classFilter misses', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'string', classFilter: ['TypeError'] },
    });
    sj.allowErrorProps('stack');
    const input = new Error('plain');
    input.stack = sampleStack;
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error']);
    expect((serialized.json as any).stack).toBeUndefined();
  });

  it('sanitizes matching messages and kept cause messages', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'off',
        sanitizeMessage: true,
        includeCauses: 'direct',
      },
    });
    const input = new Error('contact a@b.co', {
      cause: new Error('ip 8.8.8.8'),
    });
    const parsed = sj.deserialize(sj.serialize(input)) as Error;
    expect(parsed.message).toBe('contact [redacted]');
    expect((parsed.cause as Error).message).toBe('ip [redacted]');
    expect((parsed.cause as any).cause).toBeUndefined();
  });

  it('includes deep causes up to maxCauseDepth and drops non-Error causes', () => {
    const d = new Error('d');
    const c = new Error('c', { cause: d });
    const b = new Error('b', { cause: c });
    const a = new Error('a', { cause: b });
    const sj = new SuperJSON({
      errorStack: {
        mode: 'off',
        includeCauses: 'deep',
        maxCauseDepth: 2,
      },
    });
    const parsed = sj.deserialize(sj.serialize(a)) as any;
    expect(parsed.cause.message).toBe('b');
    expect(parsed.cause.cause.message).toBe('c');
    expect(parsed.cause.cause.cause).toBeUndefined();

    const withValue = new Error('top', { cause: 'not-an-error' as any });
    const parsedValue = sj.deserialize(sj.serialize(withValue)) as any;
    expect(parsedValue.cause).toBeUndefined();
  });

  it('serializes AggregateError.errors as-is', () => {
    const inner = new Error('inner');
    const aggregate = new AggregateError([inner], 'many');
    const sj = new SuperJSON({ errorStack: { mode: 'off' } });
    const parsed = sj.deserialize(sj.serialize(aggregate)) as AggregateError;
    expect(parsed.errors).toHaveLength(1);
    expect(parsed.errors[0]).toBeInstanceOf(Error);
    expect(parsed.errors[0].message).toBe('inner');
  });

  it('stops circular cause chains cleanly', () => {
    const a: any = new Error('a');
    const b: any = new Error('b');
    a.cause = b;
    b.cause = a;
    const sj = new SuperJSON({
      errorStack: { mode: 'off', includeCauses: 'deep' },
    });
    expect(() => sj.serialize(a)).not.toThrow();
    const parsed = sj.deserialize(sj.serialize(a)) as any;
    expect(parsed.message).toBe('a');
    expect(parsed.cause.message).toBe('b');
  });

  it('runs registerErrorStackProcessor after other steps', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'string',
        sanitizeMessage: true,
      },
    });
    sj.allowErrorProps('stack');
    sj.registerErrorStackProcessor('Error', serialized => ({
      ...serialized,
      message: `${serialized.message}|hook`,
      tagged: true,
    }));
    const input = new Error('see https://x.test');
    input.stack = sampleStack;
    const json = sj.serialize(input).json as any;
    expect(json.message).toBe('see [redacted]|hook');
    expect(json.tagged).toBe(true);
    expect(json.stack.split('\n')[0]).toBe('Error: boom');
  });

  it('round-trips frames through SuperJSON container types', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'frames' } });
    sj.allowErrorProps('stackFrames');
    const err = new Error('boxed');
    err.stack = sampleStack;

    const payload = {
      arr: [err],
      obj: { err },
      set: new Set([err]),
      map: new Map([['e', err]]),
    };

    const parsed = sj.deserialize(sj.serialize(payload)) as typeof payload;
    expect(parsed.arr[0]).toBeInstanceOf(Error);
    expect((parsed.arr[0] as any).stackFrames[0].raw).toBe('Error: boom');
    expect((parsed.obj.err as any).stackFrames[0].raw).toBe('Error: boom');
    expect([...(parsed.set as Set<any>)][0].stackFrames[0].raw).toBe(
      'Error: boom'
    );
    expect((parsed.map as Map<string, any>).get('e').stackFrames[0].raw).toBe(
      'Error: boom'
    );
  });

  it('normalizes options once at construction time', () => {
    const input = { mode: 'string' as const, maxStackLines: 2 };
    const sj = new SuperJSON({ errorStack: input });
    input.mode = 'frames';
    expect(sj.errorStack?.mode).toBe('string');
    expect(sj.errorStack?.maxStackLines).toBe(2);
  });
});

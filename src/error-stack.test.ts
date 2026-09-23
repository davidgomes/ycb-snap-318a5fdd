import { describe, expect, test } from 'vitest';
import SuperJSON from './index.js';
import {
  normalizeStackNewlines,
  processStackFrames,
  processStackString,
} from './error-stack.js';
import { normalizeErrorStackOptions } from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { ErrorClassRegistry } from './error-class-registry.js';

const sample = [
  'Error: boom',
  '    at user (/workspace/src/app.ts:10:2)',
  '    at hidden (/workspace/src/transformer.ts:4:1)',
  '    at nested (node:internal/process/task_queues:95:5)',
  '    at plainer (/workspace/src/plainer.ts:8:1)',
  '    at entry (/workspace/src/index.ts:3:1)',
].join('\n');

describe('normalizeErrorStackOptions', () => {
  test('non-objects', () => {
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
    expect(normalizeErrorStackOptions(1)).toBeUndefined();
  });

  test('invalid mode and maxStackLines behave as off', () => {
    expect(normalizeErrorStackOptions({}).mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'nope' }).mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'string', maxStackLines: 0 }).mode).toBe(
      'off'
    );
    expect(
      normalizeErrorStackOptions({ mode: 'frames', maxStackLines: -2 }).mode
    ).toBe('off');
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 1.5 }).mode
    ).toBe('off');
  });

  test('defaults', () => {
    const opts = normalizeErrorStackOptions({ mode: 'string' });
    expect(opts).toMatchObject({
      mode: 'string',
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

  test('invalid maxCauseDepth falls back to includeCauses none', () => {
    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth: 2.2 })
        ?.includeCauses
    ).toBe('none');
  });
});

describe('stack processors', () => {
  test('normalizeStackNewlines', () => {
    expect(normalizeStackNewlines('a\r\nb\rc')).toBe('a\nb\nc');
  });

  test('string mode order keeps header and applies stages', () => {
    const out = processStackString(sample, {
      normalizeNewlines: false,
      trimLeadingWhitespace: true,
      redactPaths: 'basename',
      maxStackLines: 4,
      stripInternalFrames: 'node_and_superjson',
    });
    // maxStackLines runs before stripInternalFrames, and basename redaction
    // removes the `src/` and `node:internal` markers those filters match.
    expect(out.split('\n')).toEqual([
      'Error: boom',
      'at user (app.ts:10:2)',
      'at hidden (transformer.ts:4:1)',
      'at nested (task_queues:95:5)',
    ]);
  });

  test('frames mode strips before redact and limit', () => {
    const frames = processStackFrames(sample, {
      trimLeadingWhitespace: true,
      redactPaths: 'basename',
      maxStackLines: 3,
      stripInternalFrames: 'superjson',
    });
    expect(frames).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at user (app.ts:10:2)' },
      { raw: 'at nested (task_queues:95:5)' },
    ]);
  });

  test('trimLeadingWhitespace false preserves indent', () => {
    const out = processStackString('Error: x\n    at a (file.ts:1:1)', {
      trimLeadingWhitespace: false,
    });
    expect(out).toBe('Error: x\n    at a (file.ts:1:1)');
  });
});

describe('sanitizeMessage', () => {
  test('redacts urls, emails, and ipv4', () => {
    expect(
      sanitizeMessage('see https://example.com/a and a@b.co from 10.0.0.8')
    ).toBe('see [redacted] and [redacted] from [redacted]');
  });
});

describe('ErrorClassRegistry', () => {
  test('register has get', () => {
    const registry = new ErrorClassRegistry();
    const fn = (plain: Record<string, any>) => plain;
    expect(registry.has('Error')).toBe(false);
    expect(registry.getProcessor('Error')).toBeUndefined();
    registry.register('Error', fn);
    expect(registry.has('Error')).toBe(true);
    expect(registry.getProcessor('Error')).toBe(fn);
  });
});

describe('SuperJSON errorStack', () => {
  test('omitting the option leaves stack opt-in raw', () => {
    const sj = new SuperJSON();
    const err = new Error('epic fail');
    const res = sj.serialize(err);
    expect(res.json).toMatchObject({ name: 'Error', message: 'epic fail' });
    expect(res.json).not.toHaveProperty('stack');
    expect(res.meta?.values).toEqual(['Error']);

    sj.allowErrorProps('stack');
    const withStack = sj.serialize(err);
    expect((withStack.json as any).stack).toBe(err.stack);
  });

  test('off ignores allowErrorProps stack', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'off' } });
    sj.allowErrorProps('stack');
    const err = new Error('x');
    const res = sj.serialize(err);
    expect(res.json).not.toHaveProperty('stack');
    expect(res.meta?.values).toEqual(['Error']);
  });

  test('string mode annotation and round trip', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'string',
        trimLeadingWhitespace: true,
        redactPaths: 'basename',
      },
    });
    sj.allowErrorProps('stack');
    const err = new Error('bad');
    err.stack = 'Error: bad\n    at run (/tmp/proj/src/app.ts:1:1)';
    const res = sj.serialize({ e: err });
    expect(res.meta?.values).toEqual({ e: ['Error/stack'] });
    expect((res.json as any).e.stack).toBe('Error: bad\nat run (app.ts:1:1)');
    const back = sj.deserialize<any>(res);
    expect(back.e).toBeInstanceOf(Error);
    expect(back.e.stack).toBe('Error: bad\nat run (app.ts:1:1)');
    expect(back.e.message).toBe('bad');
  });

  test('frames round trip inside containers', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'frames' } });
    sj.allowErrorProps('stackFrames');
    const err = new Error('bad');
    err.stack = 'Error: bad\n    at run (/tmp/proj/src/app.ts:2:3)';
    const input = {
      list: [err],
      set: new Set([err]),
      map: new Map([['k', err]]),
    };
    const back = sj.deserialize<any>(sj.serialize(input));
    const expected = [
      { raw: 'Error: bad' },
      { raw: 'at run (/tmp/proj/src/app.ts:2:3)' },
    ];
    expect(back.list[0].stackFrames).toEqual(expected);
    expect(back.list[0]).toBeInstanceOf(Error);
    const setErr = [...back.set][0];
    expect(setErr.stackFrames).toEqual(expected);
    expect(back.map.get('k').stackFrames).toEqual(expected);
    expect(sj.serialize(input).meta?.values).toBeTruthy();
  });

  test('class filter selects annotation', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'string', classFilter: ['TypeError'] },
    });
    sj.allowErrorProps('stack');
    const typeErr = new TypeError('t');
    typeErr.stack = 'TypeError: t\n    at a (file.ts:1:1)';
    const err = new Error('e');
    err.stack = 'Error: e\n    at b (file.ts:2:1)';
    const res = sj.serialize({ typeErr, err });
    expect((res.meta?.values as any).typeErr).toEqual(['Error/stack']);
    expect((res.meta?.values as any).err).toEqual(['Error']);
    expect((res.json as any).typeErr.message).toBe('t');
    expect((res.json as any).err.stack).toBe(err.stack);
  });

  test('sanitizeMessage and causes', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'off',
        sanitizeMessage: true,
        includeCauses: 'deep',
        maxCauseDepth: 2,
      },
    });
    const root = new Error('see https://secret.example/a user@ex.com 1.2.3.4');
    const mid = new Error('mid http://x.test');
    const leaf = new Error('leaf');
    const extra = new Error('too deep');
    (leaf as any).cause = extra;
    (mid as any).cause = leaf;
    (root as any).cause = mid;
    const res = sj.serialize(root);
    const json = res.json as any;
    expect(json.message).toBe('see [redacted] [redacted] [redacted]');
    expect(json.cause.message).toBe('mid [redacted]');
    expect(json.cause.cause.message).toBe('leaf');
    expect(json.cause.cause.cause).toBeUndefined();
  });

  test('direct cause drops non-errors and grandchildren', () => {
    const sj = new SuperJSON({
      errorStack: { includeCauses: 'direct', mode: 'off' },
    });
    const child = new Error('child');
    (child as any).cause = new Error('grand');
    const root = new Error('root', { cause: child });
    const json = sj.serialize(root).json as any;
    expect(json.cause.message).toBe('child');
    expect(json.cause.cause).toBeUndefined();

    const plain = new Error('root', { cause: 'nope' as any });
    expect(sj.serialize(plain).json).not.toHaveProperty('cause');
  });

  test('aggregate error errors round trip', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'off' } });
    const agg = new AggregateError([new Error('a'), new Error('b')], 'many');
    const back = sj.deserialize<AggregateError>(sj.serialize(agg));
    expect(back).toBeInstanceOf(AggregateError);
    expect(back.errors.map(e => e.message)).toEqual(['a', 'b']);
    expect(back.message).toBe('many');
  });

  test('circular cause stops', () => {
    const sj = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 8, mode: 'off' },
    });
    const a = new Error('a');
    const b = new Error('b');
    (a as any).cause = b;
    (b as any).cause = a;
    const json = sj.serialize(a).json as any;
    expect(json.message).toBe('a');
    expect(json.cause.message).toBe('b');
    expect(json.cause.cause === null || json.cause.cause === undefined).toBe(true);
  });

  test('processor runs after serialization steps', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'string',
        sanitizeMessage: true,
        includeCauses: 'direct',
      },
    });
    sj.allowErrorProps('stack');
    sj.registerErrorStackProcessor('Error', plain => {
      if (plain.message !== 'hi [redacted]') return plain;
      expect(typeof plain.stack).toBe('string');
      expect(plain.cause).toBeInstanceOf(Error);
      return { ...plain, message: 'replaced', extra: true };
    });
    const err = new Error('hi https://a.test');
    err.stack = 'Error: hi\n    at z (file.ts:1:1)';
    (err as any).cause = new Error('child');
    const json = sj.serialize(err).json as any;
    expect(json.message).toBe('replaced');
    expect(json.extra).toBe(true);
    expect(json.cause.message).toBe('child');
  });
});

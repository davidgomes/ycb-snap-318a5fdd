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

const cwd = process.cwd();

function errorWithStack(message: string, stack: string, name = 'Error') {
  const e = new Error(message);
  e.name = name;
  e.stack = stack;
  return e;
}

const STACK = [
  'Error: boom',
  '    at a (node:internal/modules/cjs/loader:1:1)',
  `    at b (${cwd}/src/transformer.ts:2:2)`,
  `    at c (${cwd}/src/app/user.ts:3:3)`,
  '    at d (/other/place/lib.js:4:4)',
].join('\n');

describe('normalizeErrorStackOptions', () => {
  test('returns undefined for non-objects', () => {
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
  });

  test('applies defaults', () => {
    expect(normalizeErrorStackOptions({})).toEqual({
      mode: 'off',
      normalizeNewlines: false,
      trimLeadingWhitespace: true,
      maxStackLines: undefined,
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
      maxCauseDepth: 16,
      sanitizeMessage: false,
      classFilter: undefined,
    });
  });

  test('invalid values fall back', () => {
    const o = normalizeErrorStackOptions({
      mode: 'bogus',
      stripInternalFrames: 'x',
      redactPaths: 'y',
      classFilter: [],
    } as any)!;
    expect(o.mode).toBe('off');
    expect(o.stripInternalFrames).toBe('none');
    expect(o.redactPaths).toBe('none');
    expect(o.classFilter).toBeUndefined();

    for (const maxStackLines of [0, -1, 1.5]) {
      expect(
        normalizeErrorStackOptions({ mode: 'string', maxStackLines })!.mode
      ).toBe('off');
    }

    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth: 1.5 })!
        .includeCauses
    ).toBe('none');
  });
});

describe('stack processing', () => {
  test('normalizeStackNewlines', () => {
    expect(normalizeStackNewlines('a\r\nb\rc\nd')).toBe('a\nb\nc\nd');
  });

  test('string mode trims and keeps header', () => {
    expect(processStackString(STACK).split('\n')[1]).toBe(
      'at a (node:internal/modules/cjs/loader:1:1)'
    );
    expect(
      processStackString(STACK, { trimLeadingWhitespace: false }).split(
        '\n'
      )[1]
    ).toBe('    at a (node:internal/modules/cjs/loader:1:1)');
  });

  test('strip internal frames never removes header', () => {
    expect(
      processStackString(STACK, { stripInternalFrames: 'node_and_superjson' })
    ).toBe(
      [
        'Error: boom',
        `at c (${cwd}/src/app/user.ts:3:3)`,
        'at d (/other/place/lib.js:4:4)',
      ].join('\n')
    );
  });

  test('redactPaths', () => {
    expect(processStackString(STACK, { redactPaths: 'basename' })).toBe(
      [
        'Error: boom',
        'at a (node:internal/modules/cjs/loader:1:1)',
        'at b (transformer.ts:2:2)',
        'at c (user.ts:3:3)',
        'at d (lib.js:4:4)',
      ].join('\n')
    );
    expect(
      processStackString(STACK, { redactPaths: 'strip_cwd' }).split('\n')[3]
    ).toBe('at c (src/app/user.ts:3:3)');
  });

  test('string order: redact before max before strip', () => {
    expect(
      processStackString(STACK, {
        redactPaths: 'basename',
        maxStackLines: 3,
        stripInternalFrames: 'superjson',
      })
    ).toBe(
      [
        'Error: boom',
        'at a (node:internal/modules/cjs/loader:1:1)',
        'at b (transformer.ts:2:2)',
      ].join('\n')
    );
  });

  test('frames order: strip before redact before max', () => {
    expect(
      processStackFrames(STACK, {
        redactPaths: 'basename',
        maxStackLines: 3,
        stripInternalFrames: 'superjson',
      })
    ).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at a (node:internal/modules/cjs/loader:1:1)' },
      { raw: 'at c (user.ts:3:3)' },
    ]);
  });

  test('normalizeNewlines is opt-in', () => {
    const crlf = 'Error: x\r\n  at a\r\n  at b';
    expect(processStackString(crlf)).toBe('Error: x\r\nat a\r\nat b');
    expect(processStackString(crlf, { normalizeNewlines: true })).toBe(
      'Error: x\nat a\nat b'
    );
  });
});

describe('sanitizeMessage', () => {
  test('redacts urls, emails and ipv4', () => {
    expect(
      sanitizeMessage(
        'see https://ex.com/a?b=1, mail me@ex.org from 10.0.0.1.'
      )
    ).toBe('see [redacted], mail [redacted] from [redacted].');
  });
});

describe('ErrorClassRegistry', () => {
  test('register / has / getProcessor', () => {
    const r = new ErrorClassRegistry();
    const fn = (v: any) => v;
    expect(r.has('X')).toBe(false);
    r.register('X', fn);
    expect(r.has('X')).toBe(true);
    expect(r.getProcessor('X')).toBe(fn);
    expect(r.getProcessor('Y')).toBeUndefined();
  });
});

describe('SuperJSON errorStack option', () => {
  test('off never serializes stack', () => {
    const s = new SuperJSON({ errorStack: { mode: 'off' } });
    s.allowErrorProps('stack', 'stackFrames');
    const res = s.serialize(errorWithStack('boom', STACK));
    expect(res.json).toEqual({ name: 'Error', message: 'boom' });
    expect(res.meta?.values).toEqual(['Error']);
  });

  test('missing mode behaves like off', () => {
    const s = new SuperJSON({ errorStack: {} });
    s.allowErrorProps('stack');
    expect((s.serialize(new Error('x')).json as any).stack).toBeUndefined();
  });

  test('string mode round-trips processed stack', () => {
    const s = new SuperJSON({
      errorStack: { mode: 'string', stripInternalFrames: 'node' },
    });
    s.allowErrorProps('stack');
    const res = s.serialize(errorWithStack('boom', STACK));
    expect(res.meta?.values).toEqual(['Error/stack']);
    const out = s.deserialize<Error>(res);
    expect(out).toBeInstanceOf(Error);
    expect(out.stack).toBe(
      processStackString(STACK, { stripInternalFrames: 'node' })
    );
  });

  test('string mode without stack allowed omits stack', () => {
    const s = new SuperJSON({ errorStack: { mode: 'string' } });
    const res = s.serialize(errorWithStack('boom', STACK));
    expect(res.meta?.values).toEqual(['Error/stack']);
    expect((res.json as any).stack).toBeUndefined();
  });

  test('frames mode round-trips in containers', () => {
    const s = new SuperJSON({ errorStack: { mode: 'frames' } });
    s.allowErrorProps('stackFrames');
    const e = errorWithStack('boom', STACK);
    const input = {
      arr: [e],
      map: new Map([['k', e]]),
      set: new Set([e]),
      nested: { e },
    };
    const out: any = s.parse(s.stringify(input));
    const frames = processStackFrames(STACK);
    expect(out.arr[0].stackFrames).toEqual(frames);
    expect(out.map.get('k').stackFrames).toEqual(frames);
    expect([...out.set][0].stackFrames).toEqual(frames);
    expect(out.nested.e.stackFrames).toEqual(frames);
    expect(out.arr[0]).toBeInstanceOf(Error);
  });

  test('classFilter miss uses Error annotation', () => {
    const s = new SuperJSON({
      errorStack: {
        mode: 'string',
        classFilter: ['TypeError'],
        sanitizeMessage: true,
      },
    });
    const res = s.serialize(new Error('at 1.2.3.4'));
    expect(res.meta?.values).toEqual(['Error']);
    expect((res.json as any).message).toBe('at 1.2.3.4');

    const res2 = s.serialize(new TypeError('at 1.2.3.4'));
    expect(res2.meta?.values).toEqual(['Error/stack']);
    expect((res2.json as any).message).toBe('at [redacted]');
  });

  test('includeCauses', () => {
    const root = new Error('root');
    const mid = new Error('mid', { cause: root });
    const top = new Error('top a@b.com', { cause: mid });

    const none = new SuperJSON({ errorStack: { mode: 'off' } });
    expect((none.serialize(top).json as any).cause).toBeUndefined();

    const direct = new SuperJSON({
      errorStack: { includeCauses: 'direct', sanitizeMessage: true },
    });
    const d: any = direct.deserialize(direct.serialize(top));
    expect(d.message).toBe('top [redacted]');
    expect(d.cause).toBeInstanceOf(Error);
    expect(d.cause.message).toBe('mid');
    expect(d.cause.cause).toBeUndefined();

    const deep = new SuperJSON({ errorStack: { includeCauses: 'deep' } });
    const r: any = deep.deserialize(deep.serialize(top));
    expect(r.cause.cause.message).toBe('root');

    const limited = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 1 },
    });
    expect(
      (limited.deserialize(limited.serialize(top)) as any).cause.cause
    ).toBeUndefined();
  });

  test('non-Error and circular causes', () => {
    const s = new SuperJSON({ errorStack: { includeCauses: 'deep' } });
    expect(
      (s.serialize(new Error('x', { cause: 'str' })).json as any).cause
    ).toBeUndefined();

    const a: any = new Error('a');
    const b: any = new Error('b', { cause: a });
    a.cause = b;
    const out: any = s.deserialize(s.serialize(a));
    expect(out.cause.message).toBe('b');
    expect(out.cause.cause).toBeUndefined();
  });

  test('AggregateError errors round-trip', () => {
    const s = new SuperJSON({ errorStack: { mode: 'off' } });
    const agg = new AggregateError([new Error('one'), new TypeError('two')], 'many');
    const out: any = s.parse(s.stringify(agg));
    expect(out).toBeInstanceOf(AggregateError);
    expect(out.errors).toHaveLength(2);
    expect(out.errors[0]).toBeInstanceOf(Error);
    expect(out.errors[1].name).toBe('TypeError');
  });

  test('registerErrorStackProcessor runs last', () => {
    const s = new SuperJSON({
      errorStack: {
        mode: 'string',
        sanitizeMessage: true,
        includeCauses: 'direct',
      },
    });
    s.allowErrorProps('stack');
    let received: any;
    s.registerErrorStackProcessor('Error', v => {
      received = v;
      return { ...v, extra: 'yes' };
    });
    const res: any = s.serialize(
      errorWithStack('x 1.1.1.1', 'Error: x\n   at a', 'Error')
    );
    expect(received.message).toBe('x [redacted]');
    expect(received.stack).toBe('Error: x\nat a');
    expect(res.json.extra).toBe('yes');
  });

  test('omitting errorStack keeps legacy behavior', () => {
    const s = new SuperJSON();
    const e = new Error('top', { cause: new Error('inner') });
    expect(s.serialize(e).meta?.values).toEqual(['Error', { cause: ['Error'] }]);
  });
});

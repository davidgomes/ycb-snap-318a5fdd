import { describe, expect, test } from 'vitest';
import SuperJSON from './index.js';
import { ErrorClassRegistry } from './error-class-registry.js';
import { normalizeErrorStackOptions } from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import {
  normalizeStackNewlines,
  processStackFrames,
  processStackString,
} from './error-stack.js';

const cwd = process.cwd();

function stackOf(frames: string[], message = 'boom'): string {
  return [`Error: ${message}`, ...frames].join('\n');
}

describe('normalizeErrorStackOptions', () => {
  test('returns undefined for non-objects', () => {
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
    expect(normalizeErrorStackOptions(1)).toBeUndefined();
    expect(normalizeErrorStackOptions(true)).toBeUndefined();
  });

  test('normalizes defaults and invalid enums once', () => {
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
      classFilter: [],
    });

    expect(
      normalizeErrorStackOptions({
        mode: 'nope',
        stripInternalFrames: 'browser',
        redactPaths: 'all',
        includeCauses: 'all',
      })?.mode
    ).toBe('off');

    expect(
      normalizeErrorStackOptions({
        mode: 'string',
        stripInternalFrames: 'node',
        redactPaths: 'basename',
        includeCauses: 'deep',
      })
    ).toMatchObject({
      mode: 'string',
      stripInternalFrames: 'node',
      redactPaths: 'basename',
      includeCauses: 'deep',
      maxCauseDepth: 16,
    });
  });

  test('invalid maxStackLines behaves like mode off', () => {
    for (const maxStackLines of [0, -1, 1.5, NaN, Infinity, '3']) {
      expect(
        normalizeErrorStackOptions({ mode: 'frames', maxStackLines })?.mode
      ).toBe('off');
    }
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 4 })
        ?.maxStackLines
    ).toBe(4);
  });

  test('non-integer maxCauseDepth falls back to includeCauses none', () => {
    expect(
      normalizeErrorStackOptions({
        includeCauses: 'deep',
        maxCauseDepth: 2.5,
      })
    ).toMatchObject({ includeCauses: 'none' });
    expect(
      normalizeErrorStackOptions({
        includeCauses: 'direct',
        maxCauseDepth: '16',
      })
    ).toMatchObject({ includeCauses: 'none' });
    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep' })?.maxCauseDepth
    ).toBe(16);
    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth: 3 })
        ?.maxCauseDepth
    ).toBe(3);
  });

  test('empty classFilter means every class', () => {
    expect(normalizeErrorStackOptions({ classFilter: [] })?.classFilter).toEqual(
      []
    );
    expect(
      normalizeErrorStackOptions({ classFilter: ['TypeError', 1] })?.classFilter
    ).toEqual(['TypeError']);
  });
});

describe('stack processors', () => {
  test('normalizeStackNewlines converts CRLF and CR', () => {
    expect(normalizeStackNewlines('a\r\nb\rc')).toBe('a\nb\nc');
  });

  test('string mode pipeline order', () => {
    const stack = stackOf([
      `    at a (node:internal/modules/cjs/loader:1:1)`,
      `    at b (${cwd}/src/app.ts:2:1)`,
      `    at c (${cwd}/src/util.ts:3:1)`,
    ]);

    const limited = processStackString(stack, {
      maxStackLines: 2,
      stripInternalFrames: 'node',
      redactPaths: 'none',
      trimLeadingWhitespace: true,
    });
    expect(limited).toBe('Error: boom');

    const framesFirst = processStackFrames(stack, {
      maxStackLines: 2,
      stripInternalFrames: 'node',
      redactPaths: 'basename',
      trimLeadingWhitespace: true,
    });
    expect(framesFirst).toEqual([
      { raw: 'Error: boom' },
      { raw: `at b (app.ts:2:1)` },
    ]);
  });

  test('basename then strip drops superjson markers in string mode only', () => {
    const stack = stackOf([
      `    at user (${cwd}/src/app.ts:1:1)`,
      `    at sj (${cwd}/node_modules/superjson/src/index.ts:2:1)`,
    ]);
    const stringed = processStackString(stack, {
      redactPaths: 'basename',
      stripInternalFrames: 'superjson',
    });
    expect(stringed).toBe(
      ['Error: boom', 'at user (app.ts:1:1)', 'at sj (index.ts:2:1)'].join('\n')
    );

    const framed = processStackFrames(stack, {
      redactPaths: 'basename',
      stripInternalFrames: 'superjson',
    });
    expect(framed).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at user (app.ts:1:1)' },
    ]);
  });

  test('stripInternalFrames never removes the header', () => {
    const stack = [
      'Error: see src/index.ts and node:internal',
      '    at inner (node:internal/process:1:1)',
      '    at sj (pkg/src/transformer.ts:2:1)',
      '    at plainer (pkg/src/plainer.ts:3:1)',
      '    at user (app.ts:4:1)',
    ].join('\n');

    expect(
      processStackString(stack, { stripInternalFrames: 'node_and_superjson' })
    ).toBe(
      ['Error: see src/index.ts and node:internal', 'at user (app.ts:4:1)'].join(
        '\n'
      )
    );
  });

  test('trimLeadingWhitespace can be disabled and newlines preserved', () => {
    const stack = 'Error: x\r\n    at foo (/a/b.ts:1:1)';
    expect(
      processStackString(stack, {
        normalizeNewlines: false,
        trimLeadingWhitespace: false,
      })
    ).toBe('Error: x\r\n    at foo (/a/b.ts:1:1)');
    expect(
      processStackString(stack, {
        normalizeNewlines: true,
        trimLeadingWhitespace: true,
      })
    ).toBe('Error: x\nat foo (/a/b.ts:1:1)');
  });

  test('basename keeps bracketed eval filenames and file URLs', () => {
    const stack = stackOf([
      '    at file:///workspace/[eval1]:3:11',
      '    at foo (file:///workspace/src/app.ts:10:5)',
    ]);
    expect(processStackString(stack, { redactPaths: 'basename' })).toBe(
      ['Error: boom', 'at [eval1]:3:11', 'at foo (app.ts:10:5)'].join('\n')
    );
  });

  test('strip_cwd removes the cwd prefix', () => {
    const stack = stackOf([`    at foo (${cwd}/src/app.ts:8:3)`]);
    expect(processStackString(stack, { redactPaths: 'strip_cwd' })).toBe(
      ['Error: boom', 'at foo (src/app.ts:8:3)'].join('\n')
    );
  });

  test('unknown strip and redact values fall back to none', () => {
    const stack = stackOf([
      '    at foo (node:internal/modules/cjs/loader:1:1)',
    ]);
    expect(
      processStackString(stack, {
        stripInternalFrames: 'browser',
        redactPaths: 'all',
        trimLeadingWhitespace: false,
      })
    ).toBe(stack);
  });
});

describe('sanitizeMessage', () => {
  test('redacts urls, emails, and ipv4', () => {
    expect(
      sanitizeMessage(
        'mail ada@example.com at https://example.com/secret, or http://10.0.0.5/x and 8.8.8.8.'
      )
    ).toBe('mail [redacted] at [redacted], or [redacted] and [redacted].');
    expect(sanitizeMessage('not an ip 1.2.3 or 999.1.1.1')).toBe(
      'not an ip 1.2.3 or 999.1.1.1'
    );
  });
});

describe('ErrorClassRegistry', () => {
  test('register, has, and getProcessor', () => {
    const registry = new ErrorClassRegistry();
    const fn = (value: Record<string, any>) => value;
    expect(registry.has('Error')).toBe(false);
    expect(registry.getProcessor('Error')).toBeUndefined();
    registry.register('Error', fn);
    expect(registry.has('Error')).toBe(true);
    expect(registry.getProcessor('Error')).toBe(fn);
  });
});

describe('SuperJSON errorStack', () => {
  test('omitting errorStack keeps legacy cause and stack behavior', () => {
    const sj = new SuperJSON();
    const input = new Error('epic fail', { cause: new Error('root') });
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual([
      'Error',
      { cause: ['Error'] },
    ]);
    expect((serialized.json as any).stack).toBeUndefined();
    expect((serialized.json as any).cause.message).toBe('root');

    sj.allowErrorProps('stack');
    input.stack = 'Error: epic fail\n    at foo (file.ts:1:1)';
    const withStack = sj.serialize(input);
    expect((withStack.json as any).stack).toBe(input.stack);
    const parsed = sj.deserialize<{ stack?: string }>(withStack);
    expect(parsed.stack).toBe(input.stack);
  });

  test('mode off suppresses stack even when stack is allowed', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'off' } });
    sj.allowErrorProps('stack');
    const input = new Error('hidden');
    input.stack = 'Error: hidden\n    at secret (file.ts:1:1)';
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error']);
    expect(serialized.json).not.toHaveProperty('stack');
    expect(JSON.stringify(serialized)).not.toContain('secret');
  });

  test('invalid mode is treated as off', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'sometimes' as 'off' },
    });
    sj.allowErrorProps('stack');
    const input = new Error('x');
    input.stack = 'Error: x\n    at foo (a.ts:1:1)';
    expect(sj.serialize(input).json).not.toHaveProperty('stack');
    expect(sj.errorStack?.mode).toBe('off');
  });

  test('string mode serializes a processed stack under Error/stack', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'string',
        redactPaths: 'basename',
        maxStackLines: 2,
      },
    });
    sj.allowErrorProps('stack');
    const input = new Error('boom');
    input.stack = stackOf([
      `    at foo (${cwd}/src/app.ts:1:1)`,
      `    at bar (${cwd}/src/util.ts:2:1)`,
    ]);
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error/stack']);
    expect((serialized.json as any).stack).toBe(
      ['Error: boom', 'at foo (app.ts:1:1)'].join('\n')
    );
    const parsed = sj.deserialize<Error>(serialized);
    expect(parsed).toBeInstanceOf(Error);
    expect(parsed.message).toBe('boom');
    expect(parsed.stack).toBe((serialized.json as any).stack);
  });

  test('string mode does not emit stack unless stack is allowed', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'string' } });
    const input = new Error('boom');
    input.stack = 'Error: boom\n    at foo (a.ts:1:1)';
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error/stack']);
    expect(serialized.json).not.toHaveProperty('stack');
  });

  test('frames mode round-trips stackFrames through containers', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'frames', trimLeadingWhitespace: true },
    });
    sj.allowErrorProps('stackFrames');
    const input = new Error('boom');
    input.stack = stackOf([`    at foo (${cwd}/src/app.ts:4:2)`]);
    const expected = [
      { raw: 'Error: boom' },
      { raw: `at foo (${cwd}/src/app.ts:4:2)` },
    ];

    const wrapped = {
      arr: [input],
      obj: { input },
      map: new Map<string, Error>([['k', input]]),
      set: new Set<Error>([input]),
    };
    const parsed = sj.deserialize<typeof wrapped>(sj.serialize(wrapped));
    expect(parsed.arr[0].stackFrames).toEqual(expected);
    expect(parsed.obj.input.stackFrames).toEqual(expected);
    expect(parsed.map.get('k')?.stackFrames).toEqual(expected);
    expect([...parsed.set][0].stackFrames).toEqual(expected);
    expect(sj.serialize(input).meta?.values).toEqual(['Error/frames']);
  });

  test('classFilter miss uses the Error annotation and raw stack', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'string', classFilter: ['TypeError'] },
    });
    sj.allowErrorProps('stack');
    const input = new Error('plain');
    input.stack = 'Error: plain\n    at foo (/secret/file.ts:1:1)';
    const serialized = sj.serialize(input);
    expect(serialized.meta?.values).toEqual(['Error']);
    expect((serialized.json as any).stack).toBe(input.stack);

    const typed = new TypeError('typed');
    typed.stack = 'TypeError: typed\n    at foo (/secret/file.ts:1:1)';
    const typedSerialized = sj.serialize(typed);
    expect(typedSerialized.meta?.values).toEqual(['Error/stack']);
    expect((typedSerialized.json as any).stack).toBe(
      'TypeError: typed\nat foo (/secret/file.ts:1:1)'
    );
  });

  test('includeCauses direct and deep, dropping non-errors', () => {
    const c = new Error('c');
    const b = new Error('b', { cause: c });
    const a = new Error('a', { cause: b });

    const direct = new SuperJSON({ errorStack: { includeCauses: 'direct' } });
    const directJson = direct.serialize(a).json as any;
    expect(directJson.cause.message).toBe('b');
    expect(directJson.cause.cause).toBeUndefined();

    const deep = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 2 },
    });
    const deepJson = deep.serialize(a).json as any;
    expect(deepJson.cause.cause.message).toBe('c');
    expect(deepJson.cause.cause.cause).toBeUndefined();

    const stringCause = new Error('top', { cause: 'nope' as any });
    expect(
      (direct.serialize(stringCause).json as any).cause
    ).toBeUndefined();

    const none = new SuperJSON({ errorStack: { mode: 'string' } });
    expect((none.serialize(a).json as any).cause).toBeUndefined();
  });

  test('circular causes stop', () => {
    const a = new Error('a');
    const b = new Error('b');
    (a as any).cause = b;
    (b as any).cause = a;
    const sj = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 16 },
    });
    const serialized = sj.serialize(a);
    expect(JSON.stringify(serialized).length).toBeGreaterThan(0);
    const parsed = sj.deserialize<Error>(serialized);
    expect(parsed.message).toBe('a');
    expect(parsed.cause).toBeTruthy();
  });

  test('AggregateError.errors round-trips when errorStack is set', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'off' } });
    const agg = new AggregateError(
      [new Error('x'), 'y', 1],
      'many',
      { cause: new Error('why') }
    );
    const parsed = sj.deserialize<AggregateError>(sj.serialize(agg));
    expect(parsed).toBeInstanceOf(AggregateError);
    expect(parsed.message).toBe('many');
    expect(parsed.errors[0]).toBeInstanceOf(Error);
    expect((parsed.errors[0] as Error).message).toBe('x');
    expect(parsed.errors[1]).toBe('y');
    expect(parsed.errors[2]).toBe(1);
    expect(parsed.cause).toBeUndefined();

    const legacy = new SuperJSON();
    const legacyJson = legacy.serialize(agg).json as any;
    expect(legacyJson.errors).toBeUndefined();
  });

  test('sanitizeMessage applies to matching errors and kept causes', () => {
    const sj = new SuperJSON({
      errorStack: {
        sanitizeMessage: true,
        includeCauses: 'direct',
        classFilter: ['Error'],
      },
    });
    const input = new Error('see https://secret.example/a and ada@example.com', {
      cause: new TypeError('from 8.8.4.4'),
    });
    const json = sj.serialize(input).json as any;
    expect(json.message).toBe('see [redacted] and [redacted]');
    expect(json.cause.message).toBe('from 8.8.4.4');
  });

  test('registerErrorStackProcessor runs after serialization steps', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'string',
        sanitizeMessage: true,
        includeCauses: 'direct',
        redactPaths: 'basename',
      },
    });
    sj.allowErrorProps('stack');
    const seen: any[] = [];
    sj.registerErrorStackProcessor('Error', plain => {
      seen.push(plain);
      return { ...plain, message: `${plain.message}!` };
    });

    const cause = new Error('mail ada@example.com');
    cause.stack = 'Error: mail ada@example.com\n    at cause (cause.ts:1:1)';
    const input = new Error('https://secret.example/a');
    input.stack = `Error: https://secret.example/a\n    at foo (${cwd}/src/app.ts:1:1)`;
    (input as any).cause = cause;

    const parsed = sj.deserialize<Error>(sj.serialize(input));
    expect(seen[0].message).toBe('mail [redacted]');
    expect(seen[0].stack).toBe('Error: mail ada@example.com\nat cause (cause.ts:1:1)');
    expect(seen[0].name).toBe('Error');
    expect(seen[1].cause.message).toBe('mail [redacted]!');
    expect(seen[1].stack).toBe(
      `Error: https://secret.example/a\nat foo (app.ts:1:1)`
    );
    expect(parsed.message).toBe('[redacted]!');
    expect((parsed.cause as Error).message).toBe('mail [redacted]!');
  });
});

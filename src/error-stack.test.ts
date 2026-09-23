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

describe('normalizeErrorStackOptions', () => {
  test('returns undefined for non-objects', () => {
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
    expect(normalizeErrorStackOptions(1)).toBeUndefined();
    expect(normalizeErrorStackOptions(false)).toBeUndefined();
  });

  test('invalid or missing mode behaves as off and applies defaults', () => {
    expect(normalizeErrorStackOptions({})).toMatchObject({
      mode: 'off',
      normalizeNewlines: false,
      trimLeadingWhitespace: true,
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
      maxCauseDepth: 16,
      sanitizeMessage: false,
    });
    expect(normalizeErrorStackOptions({ mode: 'nope' })).toMatchObject({
      mode: 'off',
    });
    expect(normalizeErrorStackOptions({ maxStackLines: 0 }).mode).toBe('off');
    expect(normalizeErrorStackOptions({ maxStackLines: -2 }).mode).toBe('off');
    expect(normalizeErrorStackOptions({ maxStackLines: 1.5 }).mode).toBe(
      'off'
    );
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 4 })
    ).toMatchObject({ mode: 'string', maxStackLines: 4 });
  });

  test('unknown enums fall back and bad maxCauseDepth disables causes', () => {
    const normalized = normalizeErrorStackOptions({
      mode: 'frames',
      stripInternalFrames: 'nope' as 'none',
      redactPaths: 'nope' as 'none',
      includeCauses: 'deep',
      maxCauseDepth: 2.2,
      classFilter: [],
    });
    expect(normalized).toMatchObject({
      mode: 'frames',
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
    });
    expect(normalized?.classFilter).toBeUndefined();
  });
});

describe('stack processors', () => {
  const cwd = process.cwd();
  const stack = [
    'Error: x',
    `    at a (${cwd}/src/index.ts:1:1)`,
    '    at b (node:internal/process/task_queues:2:2)',
    `    at c (${cwd}/src/app.ts:3:3)`,
  ].join('\n');

  test('normalizes newlines', () => {
    expect(normalizeStackNewlines('a\r\nb\rc\n')).toBe('a\nb\nc\n');
  });

  test('string mode order is redact, then limit, then strip', () => {
    expect(
      processStackString(stack, {
        trimLeadingWhitespace: true,
        redactPaths: 'basename',
        maxStackLines: 3,
        stripInternalFrames: 'node',
      })
    ).toBe(['Error: x', 'at a (index.ts:1:1)'].join('\n'));
  });

  test('frames mode order is strip, then redact, then limit', () => {
    expect(
      processStackFrames(stack, {
        trimLeadingWhitespace: true,
        redactPaths: 'basename',
        maxStackLines: 3,
        stripInternalFrames: 'node',
      })
    ).toEqual([
      { raw: 'Error: x' },
      { raw: 'at a (index.ts:1:1)' },
      { raw: 'at c (app.ts:3:3)' },
    ]);
  });

  test('preserves newline style and leading whitespace when asked', () => {
    const crlf = 'Error: x\r\n    at foo (a.js:1:1)';
    expect(
      processStackString(crlf, {
        normalizeNewlines: false,
        trimLeadingWhitespace: false,
      })
    ).toBe(crlf);
    expect(
      processStackString(crlf, {
        normalizeNewlines: true,
        trimLeadingWhitespace: false,
      })
    ).toBe('Error: x\n    at foo (a.js:1:1)');
    expect(processStackString('  Error: x\n    at foo')).toBe(
      '  Error: x\nat foo'
    );
  });

  test('strip modes keep the header and unknown values strip nothing', () => {
    const lines = [
      'node:internal in the header src/index.ts',
      '    at internal (node:internal/process/task_queues:1:1)',
      `    at sj (${cwd}/src/transformer.ts:2:2)`,
      `    at plain (${cwd}/src/plainer.ts:3:3)`,
      '    at user (/tmp/app.js:4:4)',
    ].join('\n');

    const stripped = processStackString(lines, {
      trimLeadingWhitespace: false,
      stripInternalFrames: 'node_and_superjson',
    });
    expect(stripped).toBe(
      ['node:internal in the header src/index.ts', '    at user (/tmp/app.js:4:4)'].join(
        '\n'
      )
    );

    expect(
      processStackString(lines, {
        trimLeadingWhitespace: false,
        stripInternalFrames: 'whatever',
      })
    ).toBe(lines);
  });

  test('strip_cwd removes the cwd prefix and basename keeps the filename', () => {
    const line = `    at foo (file://${cwd}/src/app.ts:3:4)`;
    expect(
      processStackString(`Error: x\n${line}`, {
        trimLeadingWhitespace: false,
        redactPaths: 'strip_cwd',
      })
    ).toBe('Error: x\n    at foo (src/app.ts:3:4)');
    expect(
      processStackString(`Error: x\n${line}`, {
        trimLeadingWhitespace: false,
        redactPaths: 'basename',
      })
    ).toBe('Error: x\n    at foo (app.ts:3:4)');
    expect(
      processStackString(`Error: x\n${line}`, {
        trimLeadingWhitespace: false,
        redactPaths: 'nope',
      })
    ).toBe(`Error: x\n${line}`);
  });
});

describe('sanitizeMessage', () => {
  test('redacts urls, emails, and ipv4 addresses', () => {
    expect(
      sanitizeMessage(
        'visit https://ex.com/a?q=1, mail a@b.co or A@B.example from 8.8.8.8.'
      )
    ).toBe(
      'visit [redacted], mail [redacted] or [redacted] from [redacted].'
    );
    expect(sanitizeMessage('http://example.com/path')).toBe('[redacted]');
  });
});

describe('ErrorClassRegistry', () => {
  test('registers processors by class name', () => {
    const registry = new ErrorClassRegistry();
    const fn = (value: { name: string; message: string }) => value;
    expect(registry.has('Error')).toBe(false);
    expect(registry.getProcessor('Error')).toBeUndefined();
    registry.register('Error', fn);
    expect(registry.has('Error')).toBe(true);
    expect(registry.getProcessor('Error')).toBe(fn);
  });
});

describe('SuperJSON errorStack', () => {
  test('omitting errorStack keeps legacy cause and stack behavior', () => {
    const cause = new Error('catastrophic failure');
    const error = new Error('subtle fail', { cause });
    (error as Error & { cause: unknown }).cause = 'plain-cause';

    const legacy = new SuperJSON();
    expect((legacy.serialize(error).json as any).cause).toBe('plain-cause');
    expect(legacy.serialize(error).meta?.values).toEqual(['Error']);

    const withStack = new Error('m');
    withStack.stack = 'Error: m\n    at foo';
    const allowing = new SuperJSON();
    allowing.allowErrorProps('stack');
    expect((allowing.serialize(withStack).json as any).stack).toBe(
      withStack.stack
    );

    const aggregate = new AggregateError([new Error('inner')], 'many');
    expect(
      (new SuperJSON().serialize(aggregate).json as any).errors
    ).toBeUndefined();
  });

  test('mode off never serializes stack data', () => {
    const error = new Error('m', { cause: new Error('c') });
    error.stack = 'Error: m\n    at foo';
    const superjson = new SuperJSON({
      errorStack: { mode: 'off', includeCauses: 'direct' },
    });
    superjson.allowErrorProps('stack', 'stackFrames');
    const { json, meta } = superjson.serialize(error);
    expect(json).not.toHaveProperty('stack');
    expect(json).not.toHaveProperty('stackFrames');
    expect((json as any).cause.message).toBe('c');
    expect(meta?.values).toEqual(['Error', { cause: ['Error'] }]);
  });

  test('invalid mode and non-positive maxStackLines act like mode off', () => {
    const error = new Error('m');
    error.stack = 'Error: m\n    at foo';
    for (const errorStack of [
      { mode: 'nope' },
      { mode: 'string', maxStackLines: 0 },
      { mode: 'frames', maxStackLines: -1 },
      { mode: 'string', maxStackLines: 2.5 },
    ] as const) {
      const superjson = new SuperJSON({ errorStack: errorStack as any });
      superjson.allowErrorProps('stack', 'stackFrames');
      const { json, meta } = superjson.serialize(error);
      expect(meta?.values).toEqual(['Error']);
      expect(json).not.toHaveProperty('stack');
      expect(json).not.toHaveProperty('stackFrames');
    }
  });

  test('string mode annotates matches and processes only when stack is allowed', () => {
    const error = new TypeError('t');
    error.stack = `TypeError: t\n    at foo (${process.cwd()}/src/app.ts:2:2)`;
    const other = new Error('e');
    other.stack = 'Error: e\n    at foo';

    const superjson = new SuperJSON({
      errorStack: {
        mode: 'string',
        classFilter: ['TypeError'],
        redactPaths: 'basename',
        trimLeadingWhitespace: true,
      },
    });
    superjson.allowErrorProps('stack');
    const { json, meta } = superjson.serialize({ error, other });
    expect(meta?.values).toEqual({
      error: ['Error/stack'],
      other: ['Error'],
    });
    expect((json as any).error.stack).toBe('TypeError: t\nat foo (app.ts:2:2)');
    expect((json as any).other.stack).toBe('Error: e\n    at foo');

    const blocked = new SuperJSON({ errorStack: { mode: 'string' } });
    const blockedResult = blocked.serialize(error);
    expect(blockedResult.meta?.values).toEqual(['Error/stack']);
    expect(blockedResult.json).not.toHaveProperty('stack');
  });

  test('frames mode round-trips stackFrames through containers', () => {
    class Box {
      constructor(public err: Error) {}
    }

    const error = new Error('e');
    error.stack = 'Error: e\n    at foo (/tmp/a.js:1:1)';
    const superjson = new SuperJSON({
      errorStack: {
        mode: 'frames',
        trimLeadingWhitespace: false,
        normalizeNewlines: false,
      },
    });
    superjson.allowErrorProps('stackFrames');
    superjson.registerClass(Box);

    const input = {
      arr: [error],
      obj: { err: error },
      map: new Map([['k', error]]),
      set: new Set([error]),
      box: new Box(error),
    };

    const serialized = superjson.serialize(input);
    const revived = superjson.deserialize(
      JSON.parse(JSON.stringify(serialized))
    ) as typeof input;

    const expected = [
      { raw: 'Error: e' },
      { raw: '    at foo (/tmp/a.js:1:1)' },
    ];
    expect((revived.arr[0] as any).stackFrames).toEqual(expected);
    expect((revived.obj.err as any).stackFrames).toEqual(expected);
    expect((revived.map.get('k') as any).stackFrames).toEqual(expected);
    expect(([...revived.set][0] as any).stackFrames).toEqual(expected);
    expect((revived.box.err as any).stackFrames).toEqual(expected);
    expect(revived.arr[0]).toBe(revived.obj.err);
    expect(revived.arr[0]).toBeInstanceOf(Error);
    expect(revived.arr[0]!.message).toBe('e');
    expect(serialized.meta?.values).toMatchObject({
      'arr.0': ['Error/frames'],
      'obj.err': ['Error/frames'],
    });
  });

  test('includeCauses direct, deep, and non-integer depth', () => {
    let deep = new Error('0');
    for (let i = 1; i <= 20; i++) {
      deep = new Error(String(i), { cause: deep });
    }

    const directJson = new SuperJSON({
      errorStack: { includeCauses: 'direct' },
    }).serialize(deep).json as any;
    expect(directJson.message).toBe('20');
    expect(directJson.cause.message).toBe('19');
    expect(directJson.cause.cause).toBeUndefined();

    const deepJson = new SuperJSON({
      errorStack: { includeCauses: 'deep' },
    }).serialize(deep).json as any;
    let node = deepJson;
    let hops = 0;
    while (node.cause) {
      hops++;
      node = node.cause;
    }
    expect(hops).toBe(16);
    expect(node.message).toBe('4');

    const limited = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 1 },
    }).serialize(deep).json as any;
    expect(limited.cause.message).toBe('19');
    expect(limited.cause.cause).toBeUndefined();

    const dropped = new Error('a', { cause: new Error('b') });
    const none = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 1.5 },
    }).serialize(dropped).json as any;
    expect(none.cause).toBeUndefined();

    const plain = new Error('a');
    (plain as { cause?: unknown }).cause = 'nope';
    expect(
      (
        new SuperJSON({
          errorStack: { includeCauses: 'deep' },
        }).serialize(plain).json as any
      ).cause
    ).toBeUndefined();
  });

  test('circular causes stop cleanly', () => {
    const a = new Error('a');
    const b = new Error('b', { cause: a });
    (a as { cause?: unknown }).cause = b;
    const superjson = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 16 },
    });
    const json = superjson.serialize(a).json as any;
    expect(json.message).toBe('a');
    expect(json.cause.message).toBe('b');
    expect(json.cause.cause).toBeNull();
    expect(() => JSON.stringify(superjson.serialize(a))).not.toThrow();
  });

  test('sanitizes matching errors and kept cause messages', () => {
    const cause = new TypeError('ping https://a.test and a@b.co');
    const error = new Error('see https://b.test', { cause });
    const superjson = new SuperJSON({
      errorStack: {
        mode: 'off',
        sanitizeMessage: true,
        includeCauses: 'direct',
        classFilter: ['TypeError'],
      },
    });
    const json = superjson.serialize(error).json as any;
    expect(json.message).toBe('see https://b.test');
    expect(json.cause.message).toBe('ping [redacted] and [redacted]');
    expect(superjson.serialize(error).meta?.values).toEqual([
      'Error',
      { cause: ['Error'] },
    ]);

    const all = new SuperJSON({
      errorStack: {
        sanitizeMessage: true,
        includeCauses: 'direct',
      },
    });
    const sanitized = all.serialize(
      new Error('from 127.0.0.1', { cause: new Error('http://x.test') })
    ).json as any;
    expect(sanitized.message).toBe('from [redacted]');
    expect(sanitized.cause.message).toBe('[redacted]');
  });

  test('AggregateError errors round-trip', () => {
    const aggregate = new AggregateError(
      [new Error('inner'), 'plain'],
      'many'
    );
    const superjson = new SuperJSON({ errorStack: { mode: 'off' } });
    const serialized = superjson.serialize(aggregate);
    const json = serialized.json as any;
    expect(json.errors[0]).toMatchObject({ name: 'Error', message: 'inner' });
    expect(json.errors[1]).toBe('plain');
    const revived = superjson.deserialize(
      JSON.parse(JSON.stringify(serialized))
    ) as AggregateError;
    expect(revived).toBeInstanceOf(AggregateError);
    expect(revived.errors[0]).toBeInstanceOf(Error);
    expect((revived.errors[0] as Error).message).toBe('inner');
    expect(revived.errors[1]).toBe('plain');
    expect(revived.message).toBe('many');
  });

  test('registerErrorStackProcessor runs after serialization steps', () => {
    const cause = new Error('https://cause.test');
    const error = new Error('https://root.test', { cause });
    error.stack = 'Error: https://root.test\n    at foo';
    const aggregate = new AggregateError([new Error('inner')], 'agg');
    aggregate.stack = 'AggregateError: agg\n    at bar';

    const superjson = new SuperJSON({
      errorStack: {
        mode: 'string',
        sanitizeMessage: true,
        includeCauses: 'direct',
        trimLeadingWhitespace: true,
      },
    });
    superjson.allowErrorProps('stack');

    let seenCauseMessage: unknown;
    superjson.registerErrorStackProcessor('Error', plain => {
      if (plain.message.startsWith('[redacted]') && plain.cause) {
        seenCauseMessage = (plain.cause as { message: string }).message;
      }
      return { ...plain, message: `wrapped:${plain.message}` };
    });
    superjson.registerErrorStackProcessor('AggregateError', plain => {
      expect(plain.name).toBe('AggregateError');
      expect(typeof plain.stack).toBe('string');
      expect(Array.isArray(plain.errors)).toBe(true);
      expect((plain.errors as { message: string }[])[0]?.message).toBe(
        'wrapped:inner'
      );
      expect(plain.errors instanceof Error).toBe(false);
      return { ...plain, message: 'changed' };
    });

    const json = superjson.serialize(error).json as any;
    expect(seenCauseMessage).toBe('wrapped:[redacted]');
    expect(json.message).toBe('wrapped:[redacted]');
    expect(json.cause.message).toBe('wrapped:[redacted]');
    expect(json.stack).toBe('Error: https://root.test\nat foo');
    expect(json.cause instanceof Error).toBe(false);

    const aggregateJson = superjson.serialize(aggregate).json as any;
    expect(aggregateJson.message).toBe('changed');
    expect(aggregateJson.errors[0].message).toBe('wrapped:inner');
  });

  test('normalizes options once at construction', () => {
    const options = {
      mode: 'string' as const,
      sanitizeMessage: false,
    };
    const superjson = new SuperJSON({ errorStack: options });
    options.sanitizeMessage = true;
    const error = new Error('https://example.com');
    expect((superjson.serialize(error).json as any).message).toBe(
      'https://example.com'
    );
    expect(superjson.errorStack?.sanitizeMessage).toBe(false);
  });
});

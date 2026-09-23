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

function stack(lines: string[]): string {
  return lines.join('\n');
}

describe('normalizeErrorStackOptions', () => {
  test('returns undefined for non-object input', () => {
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
    expect(normalizeErrorStackOptions(1)).toBeUndefined();
    expect(normalizeErrorStackOptions(true)).toBeUndefined();
  });

  test('fills defaults and treats a missing or invalid mode as off', () => {
    expect(normalizeErrorStackOptions({})).toEqual({
      mode: 'off',
      normalizeNewlines: false,
      trimLeadingWhitespace: true,
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
      maxCauseDepth: 16,
      sanitizeMessage: false,
    });

    expect(normalizeErrorStackOptions({ mode: 'nope' })?.mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'STRING' })?.mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'string' })?.mode).toBe('string');
    expect(normalizeErrorStackOptions({ mode: 'frames' })?.mode).toBe('frames');
  });

  test('invalid maxStackLines forces mode off', () => {
    expect(
      normalizeErrorStackOptions({ mode: 'string', maxStackLines: 4 })
    ).toMatchObject({ mode: 'string', maxStackLines: 4 });

    for (const maxStackLines of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, '3']) {
      expect(
        normalizeErrorStackOptions({ mode: 'frames', maxStackLines })?.mode
      ).toBe('off');
    }
  });

  test('invalid maxCauseDepth falls back to includeCauses none', () => {
    expect(normalizeErrorStackOptions({ includeCauses: 'deep' })).toMatchObject({
      includeCauses: 'deep',
      maxCauseDepth: 16,
    });
    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth: 4 })
    ).toMatchObject({ includeCauses: 'deep', maxCauseDepth: 4 });
    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth: 0 })
    ).toMatchObject({ includeCauses: 'deep', maxCauseDepth: 0 });
    expect(
      normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth: 1.5 })
    ).toMatchObject({ includeCauses: 'none', maxCauseDepth: 16 });
    expect(
      normalizeErrorStackOptions({ includeCauses: 'direct', maxCauseDepth: '2' })
    ).toMatchObject({ includeCauses: 'none' });
  });

  test('unknown enum values fall back to none', () => {
    expect(
      normalizeErrorStackOptions({
        stripInternalFrames: 'all',
        redactPaths: 'full',
        includeCauses: 'all',
      })
    ).toMatchObject({
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
    });
  });

  test('classFilter omitted or empty means all errors', () => {
    expect(normalizeErrorStackOptions({ classFilter: [] })?.classFilter).toBeUndefined();
    expect(normalizeErrorStackOptions({ classFilter: '' })?.classFilter).toBeUndefined();
    expect(normalizeErrorStackOptions({ classFilter: ['TypeError'] })?.classFilter).toEqual([
      'TypeError',
    ]);
    expect(normalizeErrorStackOptions({ classFilter: 'RangeError' })?.classFilter).toEqual([
      'RangeError',
    ]);
  });

  test('normalizes once so later input mutation is ignored', () => {
    const input = {
      mode: 'string',
      classFilter: ['TypeError'],
      normalizeNewlines: true,
      trimLeadingWhitespace: false,
      sanitizeMessage: true,
    };
    const normalized = normalizeErrorStackOptions(input);
    input.mode = 'off';
    input.classFilter.push('Error');
    input.normalizeNewlines = false;

    expect(normalized).toMatchObject({
      mode: 'string',
      classFilter: ['TypeError'],
      normalizeNewlines: true,
      trimLeadingWhitespace: false,
      sanitizeMessage: true,
    });
  });
});

describe('stack processing', () => {
  test('normalizeStackNewlines converts CRLF and CR to LF', () => {
    expect(normalizeStackNewlines('a\r\nb\rc\nd')).toBe('a\nb\nc\nd');
    expect(normalizeStackNewlines('a\nb')).toBe('a\nb');
  });

  test('string mode keeps the header and applies operations in order', () => {
    const input = stack([
      'Error: boom',
      '    at user (' + cwd + '/src/app.ts:1:1)',
      '    at internal (node:internal/process/task_queues:95:5)',
      '    at SuperJSON.serialize (' + cwd + '/src/transformer.ts:10:1)',
      '    at next (' + cwd + '/src/other.ts:2:2)',
    ]);

    expect(
      processStackString(input, {
        trimLeadingWhitespace: false,
        redactPaths: 'basename',
        maxStackLines: 3,
        stripInternalFrames: 'node_and_superjson',
      })
    ).toBe(
      stack([
        'Error: boom',
        '    at user (app.ts:1:1)',
        '    at internal (task_queues:95:5)',
      ])
    );
  });

  test('frames mode applies strip before redaction and maxStackLines', () => {
    const input = stack([
      'Error: boom',
      '    at user (' + cwd + '/src/app.ts:1:1)',
      '    at internal (node:internal/process/task_queues:95:5)',
      '    at SuperJSON.serialize (' + cwd + '/src/transformer.ts:10:1)',
      '    at next (' + cwd + '/src/other.ts:2:2)',
    ]);

    expect(
      processStackFrames(input, {
        trimLeadingWhitespace: false,
        redactPaths: 'basename',
        maxStackLines: 3,
        stripInternalFrames: 'node_and_superjson',
      })
    ).toEqual([
      { raw: 'Error: boom' },
      { raw: '    at user (app.ts:1:1)' },
      { raw: '    at next (other.ts:2:2)' },
    ]);
  });

  test('maxStackLines counts the header and interacts with strip order', () => {
    const input = stack([
      'Error: boom',
      '    at internal (node:internal/vm:1:1)',
      '    at user (app.ts:2:2)',
      '    at other (app.ts:3:3)',
    ]);
    const options = {
      trimLeadingWhitespace: false,
      maxStackLines: 2,
      stripInternalFrames: 'node',
    };

    expect(processStackString(input, options)).toBe('Error: boom');
    expect(processStackFrames(input, options)).toEqual([
      { raw: 'Error: boom' },
      { raw: '    at user (app.ts:2:2)' },
    ]);
  });

  test('does not remove the header and trims only non-header lines by default', () => {
    const input = stack([
      'node:internal header',
      '    at foo (node:internal/a:1:1)',
      '    at bar (b.js:2:2)',
    ]);

    expect(
      processStackString(input, {
        stripInternalFrames: 'node',
        trimLeadingWhitespace: true,
      })
    ).toBe(stack(['node:internal header', 'at bar (b.js:2:2)']));

    expect(
      processStackString(input, {
        stripInternalFrames: 'none',
        trimLeadingWhitespace: false,
      })
    ).toBe(input);
  });

  test('normalizeNewlines, strip_cwd, and superjson markers', () => {
    const input = [
      'Error: boom',
      '    at foo (' + cwd + '/src/app.ts:4:5)',
      '    at bar (' + cwd + '/src/plainer.ts:1:1)',
      '    at baz (' + cwd + '/src/index.ts:2:2)',
    ].join('\r\n');

    expect(
      processStackString(input, {
        normalizeNewlines: true,
        trimLeadingWhitespace: false,
        redactPaths: 'strip_cwd',
        stripInternalFrames: 'superjson',
      })
    ).toBe(stack(['Error: boom', '    at foo (src/app.ts:4:5)']));

    expect(
      processStackFrames(input, {
        normalizeNewlines: false,
        trimLeadingWhitespace: false,
        redactPaths: 'strip_cwd',
        stripInternalFrames: 'none',
      }).map(frame => frame.raw)
    ).toEqual([
      'Error: boom',
      '    at foo (src/app.ts:4:5)',
      '    at bar (src/plainer.ts:1:1)',
      '    at baz (src/index.ts:2:2)',
    ]);
  });

  test('basename and strip_cwd understand file urls', () => {
    const input = stack([
      'Error: boom',
      '    at foo (file://' + cwd + '/src/app.ts:4:5)',
    ]);

    expect(
      processStackString(input, {
        trimLeadingWhitespace: false,
        redactPaths: 'basename',
      })
    ).toBe(stack(['Error: boom', '    at foo (app.ts:4:5)']));

    expect(
      processStackString(input, {
        trimLeadingWhitespace: false,
        redactPaths: 'strip_cwd',
      })
    ).toBe(stack(['Error: boom', '    at foo (src/app.ts:4:5)']));
  });

  test('unknown strip and redact values leave frames in place', () => {
    const input = stack([
      'Error: boom',
      '    at foo (node:internal/a:1:1)',
    ]);
    expect(
      processStackString(input, {
        trimLeadingWhitespace: false,
        stripInternalFrames: 'nope',
        redactPaths: 'full',
      })
    ).toBe(input);
  });

  test('zero or negative maxStackLines yields no stack data', () => {
    const input = stack(['Error: boom', '    at foo (a.js:1:1)']);
    expect(processStackString(input, { maxStackLines: 0 })).toBe('');
    expect(processStackFrames(input, { maxStackLines: -2 })).toEqual([]);
  });
});

describe('sanitizeMessage', () => {
  test('redacts urls, emails, and ipv4 addresses', () => {
    expect(sanitizeMessage('see https://example.com/a?b=1 please')).toBe(
      'see [redacted] please'
    );
    expect(sanitizeMessage('See HTTP://Example.com.')).toBe('See [redacted].');
    expect(sanitizeMessage('(https://example.com/a)')).toBe('([redacted])');
    expect(sanitizeMessage('mail a.b+c@example.co.uk now')).toBe('mail [redacted] now');
    expect(sanitizeMessage('host 10.1.2.3 down')).toBe('host [redacted] down');
    expect(sanitizeMessage('http://192.168.0.1/x and a@b.co')).toBe(
      '[redacted] and [redacted]'
    );
    expect(sanitizeMessage('not 999.999.999.999 or v1.2.3')).toBe(
      'not 999.999.999.999 or v1.2.3'
    );
    expect(sanitizeMessage('plain')).toBe('plain');
  });
});

describe('ErrorClassRegistry', () => {
  test('registers, overwrites, and looks up processors by class name', () => {
    const registry = new ErrorClassRegistry();
    const first = (error: { name: string; message: string }) => error;
    const second = (error: { name: string; message: string }) => ({
      ...error,
      message: 'replaced',
    });

    expect(registry.has('Error')).toBe(false);
    expect(registry.getProcessor('Error')).toBeUndefined();

    registry.register('Error', first);
    expect(registry.has('Error')).toBe(true);
    expect(registry.getProcessor('Error')).toBe(first);

    registry.register('Error', second);
    expect(registry.getProcessor('Error')).toBe(second);
    expect(registry.getProcessor('TypeError')).toBeUndefined();
  });
});

describe('SuperJSON errorStack', () => {
  test('omitting errorStack keeps existing Error behavior', () => {
    const sj = new SuperJSON();
    const error = new Error('epic fail', { cause: 'not-an-error' });
    error.stack = 'STACK';

    const serialized = sj.serialize({ e: error });
    expect(serialized.json).toEqual({
      e: {
        name: 'Error',
        message: 'epic fail',
        cause: 'not-an-error',
      },
    });
    expect(serialized.meta?.values).toEqual({ e: ['Error'] });

    sj.allowErrorProps('stack');
    expect((sj.serialize(error).json as any).stack).toBe('STACK');

    const aggregate = new AggregateError([new Error('inner')], 'many');
    expect((sj.serialize(aggregate).json as any).errors).toBeUndefined();
  });

  test('mode off never serializes stack data', () => {
    const sj = new SuperJSON({ errorStack: { mode: 'off', includeCauses: 'direct' } });
    sj.allowErrorProps('stack', 'stackFrames');
    const error = new TypeError('nope', { cause: new Error('why') });
    error.stack = 'STACK';

    const serialized = sj.serialize(error);
    expect(serialized.json).toEqual({
      name: 'TypeError',
      message: 'nope',
      cause: { name: 'Error', message: 'why' },
    });
    expect(serialized.meta?.values).toEqual([
      'Error',
      { cause: ['Error'] },
    ]);
  });

  test('invalid mode and invalid maxStackLines behave as off', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'sideways', maxStackLines: 0, includeCauses: 'none' },
    });
    sj.allowErrorProps('stack');
    const error = new Error('x', { cause: new Error('y') });
    error.stack = 'STACK';
    const json = sj.serialize(error).json as any;
    expect(json.stack).toBeUndefined();
    expect(json.cause).toBeUndefined();
    expect(sj.serialize(error).meta?.values).toEqual(['Error']);
  });

  test('string mode serializes a processed stack when stack is allowed', () => {
    const options = { mode: 'string' as const, trimLeadingWhitespace: false };
    const sj = new SuperJSON({ errorStack: options });
    (options as { mode: string }).mode = 'off';
    sj.allowErrorProps('stack');

    const error = new Error('boom');
    error.stack = stack(['Error: boom', '    at foo (a.js:1:1)']);

    const serialized = sj.serialize(error);
    expect(serialized.meta?.values).toEqual(['Error/stack']);
    expect(serialized.json).toEqual({
      name: 'Error',
      message: 'boom',
      stack: error.stack,
    });

    const revived = sj.deserialize<Error>(serialized);
    expect(revived).toBeInstanceOf(Error);
    expect(revived.message).toBe('boom');
    expect(revived.stack).toBe(error.stack);

    const blocked = new SuperJSON({
      errorStack: { mode: 'string', trimLeadingWhitespace: false },
    });
    expect((blocked.serialize(error).json as any).stack).toBeUndefined();
    expect(blocked.serialize(error).meta?.values).toEqual(['Error/stack']);
  });

  test('frames mode round-trips the header and stack through containers', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'frames', trimLeadingWhitespace: false },
    });
    sj.allowErrorProps('stackFrames');

    const make = () => {
      const error = new Error('container');
      error.stack = stack(['Error: container', '    at foo (a.js:1:1)']);
      return error;
    };

    const root = make();
    const rootSerialized = sj.serialize(root);
    expect(rootSerialized.meta?.values).toEqual(['Error/frames']);
    expect((rootSerialized.json as any).stack).toBeUndefined();
    expect((rootSerialized.json as any).stackFrames).toEqual([
      { raw: 'Error: container' },
      { raw: '    at foo (a.js:1:1)' },
    ]);
    expect(sj.deserialize<Error>(rootSerialized).stack).toBe(root.stack);

    const cases: Array<[string, (error: Error) => unknown, (value: any) => Error]> = [
      ['object', error => ({ error }), value => value.error],
      ['array', error => [error], value => value[0]],
      ['nested', error => ({ items: [error] }), value => value.items[0]],
      ['map', error => new Map([['e', error]]), value => value.get('e')],
      ['set', error => new Set([error]), value => [...value][0]],
    ];

    for (const [, wrap, read] of cases) {
      const error = make();
      const revived = read(sj.deserialize(sj.serialize(wrap(error) as any)));
      expect(revived).toBeInstanceOf(Error);
      expect(revived.message).toBe('container');
      expect(revived.stack).toBe(error.stack);
    }
  });

  test('string mode round-trips through containers', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'string', trimLeadingWhitespace: true },
    });
    sj.allowErrorProps('stack');
    const error = new Error('container');
    error.stack = stack(['Error: container', '    at foo (a.js:1:1)']);
    const expected = stack(['Error: container', 'at foo (a.js:1:1)']);

    const wrapped = {
      list: [error],
      map: new Map([['e', error]]),
      set: new Set([error]),
    };
    const revived = sj.deserialize<typeof wrapped>(sj.serialize(wrapped));
    expect(revived.list[0].stack).toBe(expected);
    expect(revived.map.get('e')?.stack).toBe(expected);
    expect([...(revived.set as Set<Error>)][0].stack).toBe(expected);
  });

  test('classFilter selects annotation, stack processing, and sanitization', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'string',
        trimLeadingWhitespace: false,
        sanitizeMessage: true,
        includeCauses: 'direct',
        classFilter: ['TypeError'],
      },
    });
    sj.allowErrorProps('stack');

    const error = new Error('http://example.com', {
      cause: new TypeError('http://example.com'),
    });
    error.stack = 'Error: http://example.com\n    at raw (' + cwd + '/secret.ts:1:1)';
    (error.cause as TypeError).stack =
      'TypeError: http://example.com\n    at raw (' + cwd + '/secret.ts:2:2)';

    const serialized = sj.serialize({ error });
    const json = (serialized.json as any).error;
    expect(serialized.meta?.values).toEqual({
      error: [
        'Error',
        {
          cause: ['Error/stack'],
        },
      ],
    });
    expect(json.message).toBe('http://example.com');
    expect(json.stack).toBe(error.stack);
    expect(json.cause.message).toBe('[redacted]');
    expect(json.cause.stack).toBe((error.cause as TypeError).stack);
    expect((error.cause as TypeError).message).toBe('http://example.com');
  });

  test('sanitizes the error message and every kept cause message', () => {
    const sj = new SuperJSON({
      errorStack: { sanitizeMessage: true, includeCauses: 'deep' },
    });
    const root = new Error('go http://example.com', {
      cause: new Error('mail me user@example.com', {
        cause: new Error('ip 8.8.8.8'),
      }),
    });

    const json = sj.serialize(root).json as any;
    expect(json.message).toBe('go [redacted]');
    expect(json.cause.message).toBe('mail me [redacted]');
    expect(json.cause.cause.message).toBe('ip [redacted]');
    expect(root.message).toBe('go http://example.com');
  });

  test('includeCauses none, direct, and deep', () => {
    const chain = (depth: number) => {
      let current = new Error('0');
      for (let i = 1; i <= depth; i++) {
        current = new Error(String(i), { cause: current });
      }
      return current;
    };

    const count = (error: any) => {
      let seen = 0;
      let cursor = error;
      const visited = new Set<any>();
      while (cursor && cursor.cause && !visited.has(cursor)) {
        visited.add(cursor);
        seen++;
        cursor = cursor.cause;
      }
      return seen;
    };

    const none = new SuperJSON({ errorStack: { includeCauses: 'none' } });
    expect((none.serialize(chain(3)).json as any).cause).toBeUndefined();

    const direct = new SuperJSON({ errorStack: { includeCauses: 'direct' } });
    const directJson = direct.serialize(chain(3)).json as any;
    expect(directJson.message).toBe('3');
    expect(directJson.cause.message).toBe('2');
    expect(directJson.cause.cause).toBeUndefined();

    const deep = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 2 },
    });
    expect(count(deep.serialize(chain(5)).json)).toBe(2);

    const fallback = new SuperJSON({
      errorStack: { includeCauses: 'deep', maxCauseDepth: 2.5 },
    });
    expect((fallback.serialize(chain(3)).json as any).cause).toBeUndefined();

    const limited = new SuperJSON({ errorStack: { includeCauses: 'deep' } });
    expect(count(limited.serialize(chain(20)).json)).toBe(16);

    const dropped = new SuperJSON({ errorStack: { includeCauses: 'direct' } });
    expect(
      (dropped.serialize(new Error('a', { cause: { foo: 1 } })).json as any).cause
    ).toBeUndefined();
  });

  test('circular cause chains stop cleanly', () => {
    const a = new Error('a');
    const b = new Error('b', { cause: a });
    (a as any).cause = b;
    const sj = new SuperJSON({ errorStack: { includeCauses: 'deep' } });

    const json = sj.serialize(a).json as any;
    expect(json.message).toBe('a');
    expect(json.cause.message).toBe('b');
    expect(json.cause.cause).toBeNull();

    const self = new Error('self');
    (self as any).cause = self;
    expect((sj.serialize(self).json as any).cause).toBeNull();
  });

  test('AggregateError errors round-trip and non-errors are kept', () => {
    const sj = new SuperJSON({
      errorStack: { includeCauses: 'direct', mode: 'off' },
    });
    const aggregate = new AggregateError(
      [new Error('inner'), 'nope', 2],
      'many',
      { cause: new Error('why') }
    );

    const serialized = sj.serialize(aggregate);
    const json = serialized.json as any;
    expect(json.name).toBe('AggregateError');
    expect(json.message).toBe('many');
    expect(json.errors[0]).toMatchObject({ name: 'Error', message: 'inner' });
    expect(json.errors[1]).toBe('nope');
    expect(json.errors[2]).toBe(2);
    expect(json.cause.message).toBe('why');

    const revived = sj.deserialize<AggregateError>(serialized);
    expect(revived).toBeInstanceOf(AggregateError);
    expect(revived.message).toBe('many');
    expect(revived.errors[0]).toBeInstanceOf(Error);
    expect((revived.errors[0] as Error).message).toBe('inner');
    expect(revived.errors[1]).toBe('nope');
    expect(revived.errors[2]).toBe(2);
    expect(revived.cause).toBeInstanceOf(Error);
    expect((revived.cause as Error).message).toBe('why');

    const loop = new AggregateError([], 'loop');
    loop.errors.push(loop);
    expect(() => sj.serialize(loop)).not.toThrow();
  });

  test('allowed error props do not undo sanitization or cause policy', () => {
    const sj = new SuperJSON({
      errorStack: {
        sanitizeMessage: true,
        includeCauses: 'none',
        mode: 'string',
        trimLeadingWhitespace: false,
      },
    });
    sj.allowErrorProps('message', 'cause', 'code', 'stack');
    const error = new Error('see https://example.com', { cause: new Error('hidden') }) as Error & {
      code: string;
    };
    error.code = 'E_TEST';
    error.stack = 'Error: see https://example.com\n    at foo (a.js:1:1)';

    const json = sj.serialize(error).json as any;
    expect(json.message).toBe('see [redacted]');
    expect(json.cause).toBeUndefined();
    expect(json.code).toBe('E_TEST');
    expect(json.stack).toBe(error.stack);

    const revived = sj.deserialize<Error & { code: string }>(sj.serialize(error));
    expect(revived.message).toBe('see [redacted]');
    expect(revived.code).toBe('E_TEST');
    expect(revived.stack).toBe(error.stack);
  });

  test('stringify and parse round-trips frames', () => {
    const sj = new SuperJSON({
      errorStack: { mode: 'frames', trimLeadingWhitespace: false },
    });
    sj.allowErrorProps('stackFrames');
    const error = new Error('parse');
    error.stack = stack(['Error: parse', '    at foo (a.js:1:1)']);
    const revived = sj.parse<Map<string, Error>>(
      sj.stringify(new Map([['e', error]]))
    );
    expect(revived.get('e')).toBeInstanceOf(Error);
    expect(revived.get('e')?.stack).toBe(error.stack);
  });

  test('registerErrorStackProcessor runs after stack, sanitization, and causes', () => {
    const sj = new SuperJSON({
      errorStack: {
        mode: 'frames',
        trimLeadingWhitespace: false,
        sanitizeMessage: true,
        includeCauses: 'direct',
      },
    });
    sj.allowErrorProps('stackFrames');
    sj.registerErrorStackProcessor('TypeError', plain => ({
      ...plain,
      message: plain.message + '!',
      seenCause: (plain.cause as { message?: string } | undefined)?.message,
      seenFrames: plain.stackFrames?.length,
    }));
    sj.registerErrorStackProcessor('AggregateError', plain => ({
      ...plain,
      errorCount: Array.isArray(plain.errors) ? plain.errors.length : 0,
    }));

    const cause = new Error('because http://example.com');
    const error = new TypeError('mail me a@b.co', { cause });
    error.stack = stack([
      'TypeError: mail me a@b.co',
      '    at foo (a.js:1:1)',
    ]);

    const json = sj.serialize(error).json as any;
    expect(json.message).toBe('mail me [redacted]!');
    expect(json.seenCause).toBe('because [redacted]');
    expect(json.seenFrames).toBe(2);
    expect(json.stackFrames[0]).toEqual({ raw: 'TypeError: mail me a@b.co' });
    expect(json.cause.message).toBe('because [redacted]');
    expect(error.message).toBe('mail me a@b.co');

    const aggregate = new AggregateError([new Error('one'), new Error('two')], 'agg');
    expect((sj.serialize(aggregate).json as any).errorCount).toBe(2);
  });
});

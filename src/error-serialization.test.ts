import { describe, expect, test } from 'vitest';

import SuperJSON from './index.js';
import { ErrorStackOptions } from './error-options.js';

const cwd = process.cwd();

const STACK_FRAMES = [
  `    at userFn (${cwd}/app/user.ts:10:5)`,
  '    at Module._compile (node:internal/modules/cjs/loader:1256:14)',
  `    at walker (${cwd}/src/plainer.ts:200:3)`,
  `    at main (${cwd}/app/main.ts:3:1)`,
];

function withStack<E extends Error>(error: E): E {
  error.stack = [`${error.name}: ${error.message}`, ...STACK_FRAMES].join('\n');
  return error;
}

function instance(errorStack?: ErrorStackOptions, ...allowed: string[]) {
  const superJson = new SuperJSON({ errorStack });
  superJson.allowErrorProps(...allowed);
  return superJson;
}

function roundTrip<T>(superJson: SuperJSON, value: T): T {
  return superJson.parse(superJson.stringify(value));
}

describe('without errorStack', () => {
  test('keeps the existing Error behavior', () => {
    const superJson = instance(undefined, 'stack');
    const error = withStack(new Error('boom', { cause: 'not an error' }));

    const { json, meta } = superJson.serialize(error);

    expect(meta?.values).toEqual(['Error']);
    expect(json).toEqual({
      name: 'Error',
      message: 'boom',
      cause: 'not an error',
      stack: error.stack,
    });
    expect(roundTrip(superJson, error).stack).toBe(error.stack);
  });
});

describe('mode', () => {
  test.each<[string, ErrorStackOptions]>([
    ['empty options', {}],
    ['explicit off', { mode: 'off' }],
    ['invalid mode', { mode: 'loud' as any }],
    ['invalid maxStackLines', { mode: 'string', maxStackLines: 0 }],
  ])('%s never serializes stack data', (_, options) => {
    const superJson = instance(options, 'stack', 'stackFrames');
    const error = withStack(new Error('boom'));

    const { json, meta } = superJson.serialize(error);

    expect(meta?.values).toEqual(['Error']);
    expect(json).toEqual({ name: 'Error', message: 'boom' });
    const parsed: any = roundTrip(superJson, error);
    expect(parsed.stack).toBeUndefined();
    expect(parsed.stackFrames).toBeUndefined();
  });

  test('string mode serializes a processed stack when stack is allowed', () => {
    const superJson = instance(
      { mode: 'string', stripInternalFrames: 'node', redactPaths: 'strip_cwd' },
      'stack'
    );
    const error = withStack(new TypeError('boom'));

    const { json, meta } = superJson.serialize(error);

    const expectedStack = [
      'TypeError: boom',
      'at userFn (app/user.ts:10:5)',
      'at walker (src/plainer.ts:200:3)',
      'at main (app/main.ts:3:1)',
    ].join('\n');
    expect(meta?.values).toEqual(['Error/stack']);
    expect(json).toEqual({
      name: 'TypeError',
      message: 'boom',
      stack: expectedStack,
    });

    const parsed = roundTrip(superJson, error);
    expect(parsed).toBeInstanceOf(Error);
    expect(parsed.name).toBe('TypeError');
    expect(parsed.stack).toBe(expectedStack);
  });

  test('string mode without stack allowed omits the stack', () => {
    const superJson = instance({ mode: 'string' }, 'stackFrames');
    const { json, meta } = superJson.serialize(withStack(new Error('boom')));
    expect(meta?.values).toEqual(['Error/stack']);
    expect(json).toEqual({ name: 'Error', message: 'boom' });
  });

  test('frames mode serializes stackFrames when stackFrames is allowed', () => {
    const superJson = instance(
      { mode: 'frames', maxStackLines: 3, redactPaths: 'basename' },
      'stack',
      'stackFrames'
    );
    const error = withStack(new Error('boom'));

    const { json, meta } = superJson.serialize(error);

    const expectedFrames = [
      { raw: 'Error: boom' },
      { raw: 'at userFn (user.ts:10:5)' },
      { raw: 'at Module._compile (loader:1256:14)' },
    ];
    expect(meta?.values).toEqual(['Error/frames']);
    expect(json).toEqual({
      name: 'Error',
      message: 'boom',
      stackFrames: expectedFrames,
    });

    const parsed: any = roundTrip(superJson, error);
    expect(parsed).toBeInstanceOf(Error);
    expect(parsed.stackFrames).toEqual(expectedFrames);
    expect(parsed.stack).toBeUndefined();
  });

  test('frames mode without stackFrames allowed omits frames', () => {
    const superJson = instance({ mode: 'frames' }, 'stack');
    const { json, meta } = superJson.serialize(withStack(new Error('boom')));
    expect(meta?.values).toEqual(['Error/frames']);
    expect(json).toEqual({ name: 'Error', message: 'boom' });
  });

  test('keeps other allowed props', () => {
    const superJson = instance({ mode: 'string' }, 'code');
    const error: any = new Error('boom');
    error.code = 'E_BOOM';
    expect(roundTrip(superJson, error).code).toBe('E_BOOM');
  });
});

describe('frames round-trip through containers', () => {
  class Box {
    constructor(public error: Error) {}
  }

  test.each<[string, (e: Error) => unknown, (v: any) => any]>([
    ['object', e => ({ e }), v => v.e],
    ['array', e => [1, e], v => v[1]],
    ['Map value', e => new Map([['k', e]]), v => v.get('k')],
    ['Map key', e => new Map([[e, 1]]), v => [...v.keys()][0]],
    ['Set', e => new Set([e]), v => [...v][0]],
    ['registered class', e => new Box(e), v => v.error],
    [
      'nested',
      e => ({ a: [new Map([['k', new Set([e])]])] }),
      v => [...v.a[0].get('k')][0],
    ],
  ])('%s', (_, wrap, unwrap) => {
    const superJson = instance({ mode: 'frames' }, 'stackFrames');
    superJson.registerClass(Box);
    const error = withStack(new RangeError('boom'));

    const restored = unwrap(roundTrip(superJson, wrap(error)));

    expect(restored).toBeInstanceOf(Error);
    expect(restored.name).toBe('RangeError');
    expect(restored.stackFrames).toEqual([
      { raw: 'RangeError: boom' },
      ...STACK_FRAMES.map(line => ({ raw: line.trim() })),
    ]);
  });

  test('preserves referential equality', () => {
    const superJson = instance({ mode: 'frames' }, 'stackFrames');
    const error = withStack(new Error('boom'));
    const parsed = roundTrip(superJson, { a: error, b: [error] });
    expect(parsed.a).toBe(parsed.b[0]);
  });
});

describe('classFilter', () => {
  test('restricts stack processing and sanitization to matching names', () => {
    const superJson = instance(
      { mode: 'string', sanitizeMessage: true, classFilter: ['TypeError'] },
      'stack'
    );
    const message = 'failed https://example.com/x';

    const hit = superJson.serialize(withStack(new TypeError(message)));
    expect(hit.meta?.values).toEqual(['Error/stack']);
    expect((hit.json as any).message).toBe('failed [redacted]');
    expect((hit.json as any).stack.split('\n')[0]).toBe(
      'TypeError: failed [redacted]'
    );

    const miss = superJson.serialize(withStack(new RangeError(message)));
    expect(miss.meta?.values).toEqual(['Error']);
    expect(miss.json).toEqual({ name: 'RangeError', message });
  });

  test('empty classFilter matches every error', () => {
    const superJson = instance({ mode: 'frames', classFilter: [] });
    expect(superJson.serialize(new SyntaxError('x')).meta?.values).toEqual([
      'Error/frames',
    ]);
  });
});

describe('sanitizeMessage', () => {
  test('is off by default', () => {
    const superJson = instance({ mode: 'string' });
    const message = 'mail admin@example.com';
    expect((superJson.serialize(new Error(message)).json as any).message).toBe(
      message
    );
  });

  test('applies to the own message and kept cause messages', () => {
    const superJson = instance({
      sanitizeMessage: true,
      includeCauses: 'deep',
    });
    const error = new Error('from 10.0.0.1', {
      cause: new Error('mail admin@example.com', {
        cause: new Error('see http://internal.example.com/a'),
      }),
    });

    const parsed: any = roundTrip(superJson, error);

    expect(parsed.message).toBe('from [redacted]');
    expect(parsed.cause.message).toBe('mail [redacted]');
    expect(parsed.cause.cause.message).toBe('see [redacted]');
  });
});

describe('includeCauses', () => {
  const chain = (length: number) => {
    let error: Error | undefined;
    for (let i = length - 1; i >= 0; i--) {
      error = new Error(`e${i}`, error ? { cause: error } : undefined);
    }
    return error!;
  };

  const causeMessages = (error: any) => {
    const messages: string[] = [];
    for (let e = error.cause; e; e = e.cause) {
      messages.push(e.message);
    }
    return messages;
  };

  test('defaults to none', () => {
    const superJson = instance({ mode: 'string' });
    const { json } = superJson.serialize(chain(3));
    expect(json).toEqual({ name: 'Error', message: 'e0' });
    expect(roundTrip(superJson, chain(3))).not.toHaveProperty('cause');
  });

  test('direct keeps only the immediate cause', () => {
    const superJson = instance({ mode: 'string', includeCauses: 'direct' });
    const parsed = roundTrip(superJson, chain(4));
    expect(parsed.cause).toBeInstanceOf(Error);
    expect(causeMessages(parsed)).toEqual(['e1']);
  });

  test('deep keeps causes up to maxCauseDepth', () => {
    const superJson = instance({
      mode: 'string',
      includeCauses: 'deep',
      maxCauseDepth: 2,
    });
    expect(causeMessages(roundTrip(superJson, chain(5)))).toEqual(['e1', 'e2']);
  });

  test('deep defaults maxCauseDepth to 16', () => {
    const superJson = instance({ includeCauses: 'deep' });
    expect(causeMessages(roundTrip(superJson, chain(20)))).toHaveLength(16);
  });

  test('non-integer maxCauseDepth falls back to none', () => {
    const superJson = instance({ includeCauses: 'deep', maxCauseDepth: 2.5 });
    expect(roundTrip(superJson, chain(3)).cause).toBeUndefined();
  });

  test('drops non-Error causes', () => {
    const superJson = instance({ includeCauses: 'deep' });
    const parsed = roundTrip(
      superJson,
      new Error('outer', { cause: { reason: 'plain object' } })
    );
    expect(parsed).not.toHaveProperty('cause');
  });

  test('annotates kept causes as errors', () => {
    const superJson = instance(
      { mode: 'string', includeCauses: 'direct', classFilter: ['Error'] },
      'stack'
    );
    const error = withStack(
      new Error('outer', { cause: withStack(new TypeError('inner')) })
    );

    const { json, meta } = superJson.serialize(error);

    expect(meta?.values).toEqual(['Error/stack', { cause: ['Error'] }]);
    expect((json as any).cause).toEqual({
      name: 'TypeError',
      message: 'inner',
    });
  });

  test('processes cause stacks', () => {
    const superJson = instance(
      { mode: 'frames', includeCauses: 'direct', maxStackLines: 2 },
      'stackFrames'
    );
    const error = new Error('outer', { cause: withStack(new Error('inner')) });

    const parsed: any = roundTrip(superJson, error);

    expect(parsed.cause.stackFrames).toEqual([
      { raw: 'Error: inner' },
      { raw: `at userFn (${cwd}/app/user.ts:10:5)` },
    ]);
  });

  test('stops cleanly on circular cause chains', () => {
    const superJson = instance({ includeCauses: 'deep' });
    const a: any = new Error('a');
    const b: any = new Error('b', { cause: a });
    a.cause = b;

    const parsed = roundTrip(superJson, a);

    expect(causeMessages(parsed)).toEqual(['b']);
  });

  test('does not mutate the input', () => {
    const superJson = instance(
      { mode: 'string', includeCauses: 'deep', sanitizeMessage: true },
      'stack'
    );
    const cause = Object.freeze(withStack(new Error('inner 1.1.1.1')));
    const error = Object.freeze(
      withStack(new Error('outer 1.1.1.1', { cause }))
    );
    const stack = error.stack;

    superJson.serialize(error);

    expect(error.message).toBe('outer 1.1.1.1');
    expect(error.stack).toBe(stack);
    expect(error.cause).toBe(cause);
  });
});

describe('AggregateError', () => {
  test('serializes .errors as-is and restores it', () => {
    const superJson = instance({ mode: 'string' });
    const error = new AggregateError(
      [new TypeError('first'), new RangeError('second'), 'not an error'],
      'many'
    );

    const parsed: any = roundTrip(superJson, error);

    expect(parsed).toBeInstanceOf(AggregateError);
    expect(parsed.message).toBe('many');
    expect(parsed.errors).toHaveLength(3);
    expect(parsed.errors[0]).toBeInstanceOf(Error);
    expect(parsed.errors[0].name).toBe('TypeError');
    expect(parsed.errors[1].message).toBe('second');
    expect(parsed.errors[2]).toBe('not an error');
  });
});

describe('registerErrorStackProcessor', () => {
  test('runs last with the complete serialized error', () => {
    const superJson = instance(
      {
        mode: 'string',
        includeCauses: 'direct',
        sanitizeMessage: true,
        redactPaths: 'basename',
        maxStackLines: 2,
      },
      'stack'
    );
    const received: any[] = [];
    superJson.registerErrorStackProcessor('TypeError', serialized => {
      received.push(serialized);
      return { ...serialized, message: serialized.message.toUpperCase() };
    });

    const error = withStack(
      new TypeError('call http://x.io', {
        cause: withStack(new Error('inner admin@example.com')),
      })
    );
    const { json } = superJson.serialize(error);

    expect(received).toEqual([
      {
        name: 'TypeError',
        message: 'call [redacted]',
        stack: 'TypeError: call [redacted]\nat userFn (user.ts:10:5)',
        cause: {
          name: 'Error',
          message: 'inner [redacted]',
          stack: 'Error: inner [redacted]\nat userFn (user.ts:10:5)',
        },
      },
    ]);
    expect((json as any).message).toBe('CALL [REDACTED]');
    expect(roundTrip(superJson, error).message).toBe('CALL [REDACTED]');
  });

  test('is keyed by error class name and applies to causes', () => {
    const superJson = instance({ includeCauses: 'direct' });
    superJson.registerErrorStackProcessor('RangeError', serialized => ({
      ...serialized,
      message: 'hidden',
    }));

    const parsed = roundTrip(
      superJson,
      new Error('visible', { cause: new RangeError('secret') })
    );

    expect(parsed.message).toBe('visible');
    expect((parsed.cause as Error).message).toBe('hidden');
  });
});

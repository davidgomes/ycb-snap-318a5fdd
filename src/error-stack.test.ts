import { describe, expect, it } from 'vitest';
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

const STACK = [
  'Error: boom',
  `    at userFn (${cwd}/lib/user.ts:1:1)`,
  `    at walker (${cwd}/src/plainer.ts:2:2)`,
  '    at processTicksAndRejections (node:internal/process/task_queues:95:5)',
  `    at transformValue (${cwd}/src/transformer.ts:3:3)`,
  `    at other (/somewhere/else/other.js:4:4)`,
].join('\n');

function errorWithStack(stack: string, message = 'boom', name = 'Error') {
  const e = new Error(message);
  e.name = name;
  e.stack = stack;
  return e;
}

function instance(errorStack: any, ...allowed: string[]) {
  const sj = new SuperJSON({ errorStack });
  sj.allowErrorProps(...allowed);
  return sj;
}

describe('normalizeErrorStackOptions', () => {
  it('returns undefined for non-object input', () => {
    expect(normalizeErrorStackOptions(undefined)).toBeUndefined();
    expect(normalizeErrorStackOptions(null)).toBeUndefined();
    expect(normalizeErrorStackOptions('string')).toBeUndefined();
    expect(normalizeErrorStackOptions(42)).toBeUndefined();
  });

  it('applies defaults', () => {
    expect(normalizeErrorStackOptions({ mode: 'string' })).toEqual({
      mode: 'string',
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
  });

  it('treats missing or invalid mode as off', () => {
    expect(normalizeErrorStackOptions({})!.mode).toBe('off');
    expect(normalizeErrorStackOptions({ mode: 'nope' })!.mode).toBe('off');
  });

  it.each([0, -1, 1.5, NaN, null])(
    'treats maxStackLines=%s as mode off',
    maxStackLines => {
      expect(
        normalizeErrorStackOptions({ mode: 'string', maxStackLines })!.mode
      ).toBe('off');
    }
  );

  it('falls back to includeCauses none for non-integer maxCauseDepth', () => {
    const options = normalizeErrorStackOptions({
      includeCauses: 'deep',
      maxCauseDepth: 2.5,
    })!;
    expect(options.includeCauses).toBe('none');
  });

  it('falls back for unknown enum values', () => {
    const options = normalizeErrorStackOptions({
      stripInternalFrames: 'all',
      redactPaths: 'everything',
      includeCauses: 'some',
    })!;
    expect(options.stripInternalFrames).toBe('none');
    expect(options.redactPaths).toBe('none');
    expect(options.includeCauses).toBe('none');
  });
});

describe('stack helpers', () => {
  it('normalizes CRLF and CR to LF', () => {
    expect(normalizeStackNewlines('a\r\nb\rc\nd')).toBe('a\nb\nc\nd');
  });

  it('keeps CR when normalizeNewlines is false', () => {
    expect(processStackString('Error: x\r\n  at a', {})).toBe(
      'Error: x\r\nat a'
    );
    expect(
      processStackString('Error: x\r\n  at a', { normalizeNewlines: true })
    ).toBe('Error: x\nat a');
  });

  it('trims leading whitespace only on frame lines', () => {
    expect(processStackString('  Error: x\n    at a', {})).toBe(
      '  Error: x\nat a'
    );
    expect(
      processStackString('  Error: x\n    at a', {
        trimLeadingWhitespace: false,
      })
    ).toBe('  Error: x\n    at a');
  });

  it('strips internal frames but never the header', () => {
    const lines = processStackString(STACK, {
      stripInternalFrames: 'node_and_superjson',
    }).split('\n');
    expect(lines).toEqual([
      'Error: boom',
      `at userFn (${cwd}/lib/user.ts:1:1)`,
      'at other (/somewhere/else/other.js:4:4)',
    ]);

    expect(
      processStackString('Error: src/index.ts node:internal', {
        stripInternalFrames: 'node_and_superjson',
      })
    ).toBe('Error: src/index.ts node:internal');
  });

  it('strips only node or only superjson frames', () => {
    const node = processStackString(STACK, { stripInternalFrames: 'node' });
    expect(node).not.toContain('node:internal');
    expect(node).toContain('src/plainer.ts');

    const superjson = processStackString(STACK, {
      stripInternalFrames: 'superjson',
    });
    expect(superjson).toContain('node:internal');
    expect(superjson).not.toContain('src/plainer.ts');
    expect(superjson).not.toContain('src/transformer.ts');
  });

  it('redacts paths', () => {
    const basename = processStackString(STACK, { redactPaths: 'basename' });
    expect(basename.split('\n')[1]).toBe('at userFn (user.ts:1:1)');
    expect(basename.split('\n')[5]).toBe('at other (other.js:4:4)');

    const stripped = processStackString(STACK, { redactPaths: 'strip_cwd' });
    expect(stripped.split('\n')[1]).toBe('at userFn (lib/user.ts:1:1)');
    expect(stripped).not.toContain(cwd + '/');
    expect(stripped).toContain('/somewhere/else/other.js');
  });

  it('counts the header in maxStackLines', () => {
    expect(processStackString(STACK, { maxStackLines: 1 })).toBe(
      'Error: boom'
    );
    expect(processStackString(STACK, { maxStackLines: 3 }).split('\n')).toEqual(
      [
        'Error: boom',
        `at userFn (${cwd}/lib/user.ts:1:1)`,
        `at walker (${cwd}/src/plainer.ts:2:2)`,
      ]
    );
  });

  it('string mode redacts before stripping and limits before stripping', () => {
    // basename runs first, so `src/plainer.ts` no longer matches the superjson filter
    const redacted = processStackString(STACK, {
      redactPaths: 'basename',
      stripInternalFrames: 'superjson',
    });
    expect(redacted).toContain('at walker (plainer.ts:2:2)');

    const limited = processStackString(STACK, {
      maxStackLines: 3,
      stripInternalFrames: 'superjson',
    });
    expect(limited.split('\n')).toEqual([
      'Error: boom',
      `at userFn (${cwd}/lib/user.ts:1:1)`,
    ]);
  });

  it('frames mode strips before redacting and limiting', () => {
    const frames = processStackFrames(STACK, {
      redactPaths: 'basename',
      stripInternalFrames: 'node_and_superjson',
      maxStackLines: 3,
    });
    expect(frames).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at userFn (user.ts:1:1)' },
      { raw: 'at other (other.js:4:4)' },
    ]);
  });
});

describe('sanitizeMessage', () => {
  it('redacts urls, emails and ipv4 addresses', () => {
    expect(
      sanitizeMessage(
        'GET https://api.example.com/v1?x=1 failed for bob@example.com from 10.0.0.12.'
      )
    ).toBe('GET [redacted] failed for [redacted] from [redacted].');
    expect(sanitizeMessage('see http://1.2.3.4/path')).toBe('see [redacted]');
    expect(sanitizeMessage('version 1.2.3 is fine')).toBe(
      'version 1.2.3 is fine'
    );
  });
});

describe('ErrorClassRegistry', () => {
  it('registers processors by name', () => {
    const registry = new ErrorClassRegistry();
    const fn = (v: any) => v;
    expect(registry.has('TypeError')).toBe(false);
    registry.register('TypeError', fn);
    expect(registry.has('TypeError')).toBe(true);
    expect(registry.getProcessor('TypeError')).toBe(fn);
    expect(registry.getProcessor('Error')).toBeUndefined();
  });
});

describe('errorStack option', () => {
  it('keeps default behaviour when omitted', () => {
    const sj = new SuperJSON();
    sj.allowErrorProps('stack');
    const e = errorWithStack(STACK);
    const { json, meta } = sj.serialize({ e }) as any;
    expect(meta.values).toEqual({ e: ['Error'] });
    expect(json.e.stack).toBe(STACK);
  });

  it('never serializes stack data in off mode', () => {
    const sj = instance({ mode: 'off' }, 'stack', 'stackFrames');
    const { json, meta } = sj.serialize({ e: errorWithStack(STACK) }) as any;
    expect(meta.values).toEqual({ e: ['Error'] });
    expect(json.e).toEqual({ name: 'Error', message: 'boom' });
  });

  it('treats a provided config without a valid mode as off', () => {
    for (const errorStack of [{}, { mode: 'bogus' }]) {
      const sj = instance(errorStack, 'stack');
      const { json } = sj.serialize({ e: errorWithStack(STACK) }) as any;
      expect(json.e.stack).toBeUndefined();
    }
  });

  it('serializes a processed stack string in string mode', () => {
    const sj = instance(
      { mode: 'string', stripInternalFrames: 'node', redactPaths: 'strip_cwd' },
      'stack'
    );
    const { json, meta } = sj.serialize({ e: errorWithStack(STACK) }) as any;
    expect(meta.values).toEqual({ e: ['Error/stack'] });
    expect(json.e.stack.split('\n')[0]).toBe('Error: boom');
    expect(json.e.stack).not.toContain('node:internal');
    expect(json.e.stack).toContain('at userFn (lib/user.ts:1:1)');

    const out: any = sj.deserialize(sj.serialize({ e: errorWithStack(STACK) }));
    expect(out.e).toBeInstanceOf(Error);
    expect(out.e.stack).toBe(json.e.stack);
  });

  it('omits stack in string mode unless stack is allowed', () => {
    const sj = instance({ mode: 'string' });
    const { json, meta } = sj.serialize({ e: errorWithStack(STACK) }) as any;
    expect(meta.values).toEqual({ e: ['Error/stack'] });
    expect(json.e.stack).toBeUndefined();
  });

  it('serializes stackFrames in frames mode', () => {
    const sj = instance(
      { mode: 'frames', maxStackLines: 2, redactPaths: 'basename' },
      'stackFrames'
    );
    const { json, meta } = sj.serialize({ e: errorWithStack(STACK) }) as any;
    expect(meta.values).toEqual({ e: ['Error/frames'] });
    expect(json.e.stack).toBeUndefined();
    expect(json.e.stackFrames).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at userFn (user.ts:1:1)' },
    ]);
  });

  it('round-trips frames through every container type', () => {
    class Holder {
      constructor(public error?: Error) {}
    }
    const sj = instance({ mode: 'frames' }, 'stackFrames');
    sj.registerClass(Holder);

    const e = errorWithStack('Error: boom\n    at a (/x/a.js:1:1)');
    const expected = [{ raw: 'Error: boom' }, { raw: 'at a (/x/a.js:1:1)' }];

    const out: any = sj.parse(
      sj.stringify({
        plain: e,
        array: [e],
        object: { nested: { e } },
        map: new Map([['k', e]]),
        mapKey: new Map([[e, 1]]),
        set: new Set([e]),
        holder: new Holder(e),
      })
    );

    const found = [
      out.plain,
      out.array[0],
      out.object.nested.e,
      out.map.get('k'),
      [...out.mapKey.keys()][0],
      [...out.set][0],
      out.holder.error,
    ];
    for (const error of found) {
      expect(error).toBeInstanceOf(Error);
      expect(error.stackFrames).toEqual(expected);
    }
    expect(out.holder).toBeInstanceOf(Holder);
  });

  it('uses Error for classFilter misses', () => {
    const sj = instance(
      { mode: 'string', classFilter: ['TypeError'], sanitizeMessage: true },
      'stack'
    );
    const typeError = errorWithStack(STACK, 'at 10.0.0.1', 'TypeError');
    const other = errorWithStack(STACK, 'at 10.0.0.1');
    const { json, meta } = sj.serialize({ typeError, other }) as any;
    expect(meta.values).toEqual({
      typeError: ['Error/stack'],
      other: ['Error'],
    });
    expect(json.typeError.message).toBe('at [redacted]');
    expect(json.typeError.stack).toBeDefined();
    expect(json.other.message).toBe('at 10.0.0.1');
    expect(json.other.stack).toBeUndefined();
  });

  it('treats an empty classFilter as all errors', () => {
    const sj = instance({ mode: 'frames', classFilter: [] }, 'stackFrames');
    const { meta } = sj.serialize(new RangeError('x')) as any;
    expect(meta.values).toEqual(['Error/frames']);
  });

  describe('causes', () => {
    const chain = () =>
      new Error('one', {
        cause: new Error('two', {
          cause: new Error('three', { cause: new Error('four') }),
        }),
      });

    function depth(error: any) {
      let n = 0;
      while (error?.cause instanceof Error) {
        n++;
        error = error.cause;
      }
      return n;
    }

    it('drops causes by default', () => {
      const sj = instance({ mode: 'string' });
      const out: any = sj.deserialize(sj.serialize(chain()));
      expect(out.cause).toBeUndefined();
      expect('cause' in out).toBe(false);
    });

    it('keeps only the direct cause', () => {
      const sj = instance({ mode: 'string', includeCauses: 'direct' });
      const serialized = sj.serialize(chain()) as any;
      expect(serialized.meta.values).toEqual([
        'Error/stack',
        { cause: ['Error/stack'] },
      ]);
      const out: any = sj.deserialize(serialized);
      expect(out.cause.message).toBe('two');
      expect(depth(out)).toBe(1);
    });

    it('keeps causes up to maxCauseDepth in deep mode', () => {
      const deep = instance({ mode: 'string', includeCauses: 'deep' });
      expect(depth(deep.deserialize(deep.serialize(chain())))).toBe(3);

      const limited = instance({
        mode: 'string',
        includeCauses: 'deep',
        maxCauseDepth: 2,
      });
      const out: any = limited.deserialize(limited.serialize(chain()));
      expect(depth(out)).toBe(2);
      expect(out.cause.cause.message).toBe('three');
    });

    it('falls back to none for non-integer maxCauseDepth', () => {
      const sj = instance({
        mode: 'string',
        includeCauses: 'deep',
        maxCauseDepth: '3',
      });
      expect(depth(sj.deserialize(sj.serialize(chain())))).toBe(0);
    });

    it('drops non-Error causes', () => {
      const sj = instance({ includeCauses: 'deep' });
      const { json } = sj.serialize(
        new Error('x', { cause: { reason: 'not an error' } })
      ) as any;
      expect(json).toEqual({ name: 'Error', message: 'x' });
    });

    it('stops cleanly on circular cause chains', () => {
      const sj = instance({ mode: 'string', includeCauses: 'deep' }, 'stack');
      const a: any = new Error('a');
      const b: any = new Error('b', { cause: a });
      a.cause = b;
      const out: any = sj.parse(sj.stringify({ a }));
      expect(out.a.message).toBe('a');
      expect(out.a.cause.message).toBe('b');
    });

    it('sanitizes every kept cause message', () => {
      const sj = instance({
        mode: 'string',
        includeCauses: 'deep',
        sanitizeMessage: true,
        classFilter: ['TypeError'],
      });
      const error = new TypeError('top https://a.example', {
        cause: new Error('mid a@b.io', { cause: new Error('low 8.8.8.8') }),
      });
      const out: any = sj.deserialize(sj.serialize(error));
      expect(out.message).toBe('top [redacted]');
      expect(out.cause.message).toBe('mid [redacted]');
      expect(out.cause.cause.message).toBe('low [redacted]');
    });

    it('serializes and restores AggregateError errors', () => {
      const sj = instance({ mode: 'string' });
      const error = new AggregateError(
        [new Error('first'), new TypeError('second')],
        'many'
      );
      const out: any = sj.parse(sj.stringify({ error }));
      expect(out.error).toBeInstanceOf(AggregateError);
      expect(out.error.message).toBe('many');
      expect(out.error.errors.map((e: Error) => e.message)).toEqual([
        'first',
        'second',
      ]);
      expect(out.error.errors[1].name).toBe('TypeError');
    });
  });

  describe('registerErrorStackProcessor', () => {
    it('runs after every other serialization step', () => {
      const sj = instance(
        {
          mode: 'string',
          redactPaths: 'basename',
          sanitizeMessage: true,
          includeCauses: 'direct',
        },
        'stack'
      );
      let seen: any;
      sj.registerErrorStackProcessor('TypeError', serialized => {
        seen = serialized;
        return { ...serialized, message: serialized.message + '!', extra: 1 };
      });

      const error = errorWithStack(STACK, 'mail a@b.io', 'TypeError');
      (error as any).cause = new Error('inner');
      const { json } = sj.serialize(error) as any;

      expect(seen.name).toBe('TypeError');
      expect(seen.message).toBe('mail [redacted]');
      expect(seen.stack).toContain('at userFn (user.ts:1:1)');
      expect(seen.cause.message).toBe('inner');
      expect(json.message).toBe('mail [redacted]!');
      expect(json.extra).toBe(1);
      expect(json.cause).toMatchObject({ name: 'Error', message: 'inner' });
      expect(json.cause.stack.split('\n')[0]).toBe('Error: inner');
    });

    it('only applies to the registered class name', () => {
      const sj = instance({ mode: 'off' });
      sj.registerErrorStackProcessor('RangeError', s => ({
        ...s,
        message: 'replaced',
      }));
      const { json } = sj.serialize({
        range: new RangeError('r'),
        plain: new Error('p'),
      }) as any;
      expect(json.range.message).toBe('replaced');
      expect(json.plain.message).toBe('p');
    });
  });
});

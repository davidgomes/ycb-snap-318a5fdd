import { describe, expect, test } from 'vitest';
import { checkAsync, transform } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  intersect,
  map,
  number,
  object,
  objectAsync,
  optional,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import { parse, parseAsync } from '../parse/index.ts';
import { pipe, pipeAsync } from '../pipe/index.ts';
import { safeParse } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';
import { recursiveAsync } from './recursiveAsync.ts';

describe('recursive', () => {
  const Tree = recursive(object({ value: number(), children: array(Recur) }));

  test('should return schema object', () => {
    expect(Tree).toMatchObject({
      kind: 'schema',
      type: 'recursive',
      reference: recursive,
      expects: 'Object',
      async: false,
    });
  });

  test('should validate nested data', () => {
    const input = {
      value: 1,
      children: [{ value: 2, children: [{ value: 3, children: [] }] }],
    };
    expect(parse(Tree, input)).toStrictEqual(input);
    const result = safeParse(Tree, {
      value: 1,
      children: [{ value: 'x', children: [] }],
    });
    expect(result.success).toBe(false);
    expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
      'children',
      0,
      'value',
    ]);
  });

  test('should support record, map and set', () => {
    const Schema = recursive(
      object({
        r: optional(record(string(), Recur)),
        m: optional(map(string(), Recur)),
        s: optional(set(Recur)),
      })
    );
    const input = {
      r: { a: {} },
      m: new Map([['b', { s: new Set([{}]) }]]),
    };
    expect(parse(Schema, input)).toStrictEqual(input);
    expect(safeParse(Schema, { s: new Set([1]) }).success).toBe(false);
  });

  test('should compose with pipe and intersect', () => {
    const Schema = recursive(
      pipe(
        intersect([object({ a: number() }), object({ next: optional(Recur) })]),
        transform((input) => ({ ...input, seen: true }))
      )
    );
    expect(parse(Schema, { a: 1, next: { a: 2 } })).toStrictEqual({
      a: 1,
      seen: true,
      next: { a: 2, seen: true },
    });
  });

  test('should resolve to nearest recursive', () => {
    const Schema = recursive(
      object({
        inner: optional(recursive(object({ x: optional(Recur) }))),
        y: optional(Recur),
      })
    );
    expect(parse(Schema, { y: { inner: { x: { x: {} } } } })).toStrictEqual({
      y: { inner: { x: { x: {} } } },
    });
    expect(safeParse(Schema, { inner: { x: { y: {} } } }).output).toStrictEqual(
      { inner: { x: {} } }
    );
  });

  test('should throw for unresolved placeholder', () => {
    // @ts-expect-error
    expect(() => parse(array(Recur), [1])).toThrowError();
  });
});

describe('recursiveAsync', () => {
  test('should validate nested data asynchronously', async () => {
    const Tree = recursiveAsync(
      pipeAsync(
        objectAsync({ value: number(), children: arrayAsync(Recur) }),
        checkAsync(async (input) => input.value > 0)
      )
    );
    expect(Tree.async).toBe(true);
    const input = { value: 1, children: [{ value: 2, children: [] }] };
    await expect(parseAsync(Tree, input)).resolves.toStrictEqual(input);
    await expect(
      parseAsync(Tree, { value: 1, children: [{ value: 0, children: [] }] })
    ).rejects.toThrowError();
  });
});

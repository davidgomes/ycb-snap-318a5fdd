import { describe, expect, test } from 'vitest';
import { checkAsync, transformAsync } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  intersectAsync,
  mapAsync,
  number,
  object,
  objectAsync,
  optionalAsync,
  recordAsync,
  setAsync,
  string,
  union,
  unionAsync,
} from '../../schemas/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { safeParseAsync } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import {
  recursiveAsync,
  type SchemaWithRecursiveAsync,
} from './recursiveAsync.ts';

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

describe('recursiveAsync', () => {
  test('should return copy of passed schema', () => {
    const schema = objectAsync({ children: arrayAsync(Recur) });
    expect(recursiveAsync(schema)).toStrictEqual({
      ...(schema as Omit<typeof schema, '~types'>),
      async: true,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies SchemaWithRecursiveAsync<typeof schema>);
  });

  describe('should resolve recursion through', () => {
    test('array items', async () => {
      const schema = recursiveAsync(
        objectAsync({
          name: pipeAsync(
            string(),
            checkAsync(async (input) => input !== 'invalid', 'name')
          ),
          children: arrayAsync(Recur),
        })
      );
      const input = {
        name: 'root',
        children: [{ name: 'a', children: [{ name: 'b', children: [] }] }],
      };
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      const result = await safeParseAsync(schema, {
        name: 'root',
        children: [
          { name: 'a', children: [{ name: 'invalid', children: [] }] },
        ],
      });
      expect(result.issues?.[0].message).toBe('name');
      expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
        'children',
        0,
        'children',
        0,
        'name',
      ]);
    });

    test('record values', async () => {
      const schema = recursiveAsync(
        unionAsync([number(), recordAsync(string(), Recur)])
      );
      const input = { a: 1, b: { c: 2, d: { e: 3 } } };
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      expect((await safeParseAsync(schema, { a: { b: 'c' } })).success).toBe(
        false
      );
    });

    test('map values', async () => {
      const schema = recursiveAsync(
        objectAsync({ value: number(), children: mapAsync(string(), Recur) })
      );
      const input = {
        value: 1,
        children: new Map([
          [
            'a',
            {
              value: 2,
              children: new Map([['b', { value: 3, children: new Map() }]]),
            },
          ],
        ]),
      };
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      expect(
        (
          await safeParseAsync(schema, {
            value: 1,
            children: new Map([['a', { value: '2', children: new Map() }]]),
          })
        ).success
      ).toBe(false);
    });

    test('set values', async () => {
      const schema = recursiveAsync(unionAsync([string(), setAsync(Recur)]));
      const input = new Set(['a', new Set(['b', new Set(['c'])])]);
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      expect(
        (await safeParseAsync(schema, new Set(['a', new Set([1])]))).success
      ).toBe(false);
    });
  });

  test('should resolve sync schema', async () => {
    const schema = recursiveAsync(
      union([number(), object({ children: array(Recur) })])
    );
    const input = { children: [1, { children: [2] }] };
    expect(await parseAsync(schema, input)).toStrictEqual(input);
    expect(
      (await safeParseAsync(schema, { children: [{ children: ['3'] }] }))
        .success
    ).toBe(false);
  });

  describe('should compose with', () => {
    test('pipe inside of recursive', async () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({ value: string(), children: arrayAsync(Recur) }),
          transformAsync(async (input) => ({
            ...input,
            count: input.children.length,
          }))
        )
      );
      expect(
        await parseAsync(schema, {
          value: 'root',
          children: [{ value: 'a', children: [] }],
        })
      ).toStrictEqual({
        value: 'root',
        count: 1,
        children: [{ value: 'a', count: 0, children: [] }],
      });
    });

    test('intersect', async () => {
      const schema = recursiveAsync(
        intersectAsync([
          objectAsync({ id: string() }),
          objectAsync({ children: arrayAsync(Recur) }),
        ])
      );
      const input = {
        id: 'a',
        children: [{ id: 'b', children: [{ id: 'c', children: [] }] }],
      };
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      expect(
        (
          await safeParseAsync(schema, {
            id: 'a',
            children: [{ id: 1, children: [] }],
          })
        ).success
      ).toBe(false);
    });
  });

  test('should keep resolution of concurrent parses separate', async () => {
    const schema1 = recursiveAsync(
      objectAsync({
        a: pipeAsync(
          number(),
          checkAsync(async () => {
            await delay(5);
            return true;
          })
        ),
        next: optionalAsync(Recur),
      })
    );
    const schema2 = recursiveAsync(
      objectAsync({
        b: pipeAsync(
          string(),
          checkAsync(async () => {
            await delay(1);
            return true;
          })
        ),
        next: optionalAsync(Recur),
      })
    );
    const input1 = { a: 1, next: { a: 2, next: { a: 3 } } };
    const input2 = { b: 'x', next: { b: 'y', next: { b: 'z' } } };
    expect(
      await Promise.all([
        parseAsync(schema1, input1),
        parseAsync(schema2, input2),
      ])
    ).toStrictEqual([input1, input2]);
  });
});

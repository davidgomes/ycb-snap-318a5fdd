import { describe, expect, test } from 'vitest';
import { checkAsync, minLength, transformAsync } from '../../actions/index.ts';
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
} from '../../schemas/index.ts';
import { expectNoSchemaIssueAsync } from '../../vitest/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipe, pipeAsync } from '../pipe/index.ts';
import { safeParseAsync } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should return schema object', () => {
    const wrapped = objectAsync({
      name: string(),
      children: arrayAsync(Recur),
    });
    expect(recursiveAsync(wrapped)).toStrictEqual({
      kind: 'schema',
      type: 'recursive',
      reference: recursiveAsync,
      expects: 'Object',
      async: true,
      wrapped,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecursiveSchemaAsync<typeof wrapped>);
  });

  describe('should resolve self references', () => {
    const schema = recursiveAsync(
      objectAsync({
        name: pipeAsync(
          string(),
          checkAsync(async (input) => input !== 'invalid', 'message')
        ),
        children: arrayAsync(Recur),
      })
    );

    test('for valid input', async () => {
      await expectNoSchemaIssueAsync(schema, [
        { name: 'a', children: [] },
        {
          name: 'a',
          children: [
            { name: 'b', children: [] },
            { name: 'c', children: [{ name: 'd', children: [] }] },
          ],
        },
      ]);
    });

    test('for invalid nested input', async () => {
      const result = await safeParseAsync(schema, {
        name: 'a',
        children: [
          { name: 'b', children: [{ name: 'invalid', children: [] }] },
        ],
      });
      expect(result.success).toBe(false);
      expect(result.issues?.length).toBe(1);
      expect(result.issues?.[0].message).toBe('message');
      expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
        'children',
        0,
        'children',
        0,
        'name',
      ]);
    });

    test('for concurrent parsing', async () => {
      const inputs = Array.from({ length: 10 }, (_, index) => ({
        name: `${index}`,
        children: [{ name: 'child', children: [] }],
      }));
      expect(
        await Promise.all(inputs.map((input) => parseAsync(schema, input)))
      ).toStrictEqual(inputs);
    });

    test('for Standard Schema validation', async () => {
      expect(
        await schema['~standard'].validate({ name: 'a', children: [] })
      ).toStrictEqual({ typed: true, value: { name: 'a', children: [] } });
    });
  });

  describe('should resolve self references in value positions', () => {
    test('for arrays', async () => {
      const schema = recursiveAsync(arrayAsync(Recur));
      expect(await parseAsync(schema, [[], [[]]])).toStrictEqual([[], [[]]]);
      expect((await safeParseAsync(schema, [[], [1]])).success).toBe(false);
    });

    test('for records', async () => {
      const schema = recursiveAsync(recordAsync(string(), Recur));
      expect(await parseAsync(schema, { a: {}, b: { c: {} } })).toStrictEqual({
        a: {},
        b: { c: {} },
      });
      expect((await safeParseAsync(schema, { a: { b: 1 } })).success).toBe(
        false
      );
    });

    test('for maps', async () => {
      const schema = recursiveAsync(mapAsync(string(), Recur));
      const input = new Map([['a', new Map([['b', new Map()]])]]);
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      expect((await safeParseAsync(schema, new Map([['a', 1]]))).success).toBe(
        false
      );
    });

    test('for sets', async () => {
      const schema = recursiveAsync(setAsync(Recur));
      const input = new Set([new Set([new Set()])]);
      expect(await parseAsync(schema, input)).toStrictEqual(input);
      expect((await safeParseAsync(schema, new Set([1]))).success).toBe(false);
    });

    test('for optional values', async () => {
      const schema = recursiveAsync(
        objectAsync({ value: number(), next: optionalAsync(Recur) })
      );
      expect(
        await parseAsync(schema, {
          value: 1,
          next: { value: 2, next: { value: 3 } },
        })
      ).toStrictEqual({ value: 1, next: { value: 2, next: { value: 3 } } });
      expect(
        (await safeParseAsync(schema, { value: 1, next: { value: '2' } }))
          .success
      ).toBe(false);
    });
  });

  test('should resolve sync wrapped schemas', async () => {
    const schema = recursiveAsync(
      object({ name: string(), children: array(Recur) })
    );
    const input = { name: 'a', children: [{ name: 'b', children: [] }] };
    expect(await parseAsync(schema, input)).toStrictEqual(input);
    expect(
      (await safeParseAsync(schema, { name: 'a', children: [{ name: 1 }] }))
        .success
    ).toBe(false);
  });

  test('should compose with pipe', async () => {
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({
          id: pipe(string(), minLength(1)),
          items: arrayAsync(Recur),
        }),
        transformAsync(async (input) => ({
          ...input,
          count: input.items.length,
        }))
      )
    );
    expect(
      await parseAsync(schema, {
        id: 'a',
        items: [
          { id: 'b', items: [] },
          { id: 'c', items: [] },
        ],
      })
    ).toStrictEqual({
      id: 'a',
      items: [
        { id: 'b', items: [], count: 0 },
        { id: 'c', items: [], count: 0 },
      ],
      count: 2,
    });
  });

  test('should compose with intersect', async () => {
    const schema = recursiveAsync(
      intersectAsync([
        objectAsync({ children: arrayAsync(Recur) }),
        object({ name: pipe(string(), minLength(1)) }),
      ])
    );
    expect(
      await parseAsync(schema, {
        name: 'a',
        children: [{ name: 'b', children: [] }],
      })
    ).toStrictEqual({ name: 'a', children: [{ name: 'b', children: [] }] });
    expect(
      (
        await safeParseAsync(schema, {
          name: 'a',
          children: [{ name: '', children: [] }],
        })
      ).success
    ).toBe(false);
  });

  test('should resolve placeholders to the closest recursive schema', async () => {
    const inner = recursive(object({ label: string(), nested: array(Recur) }));
    const outer = recursiveAsync(
      objectAsync({ inner, children: arrayAsync(Recur) })
    );
    const input = {
      inner: { label: 'a', nested: [{ label: 'b', nested: [] }] },
      children: [{ inner: { label: 'c', nested: [] }, children: [] }],
    };
    expect(await parseAsync(outer, input)).toStrictEqual(input);
  });
});

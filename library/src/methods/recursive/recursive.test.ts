import { describe, expect, test } from 'vitest';
import { check, minLength, transform } from '../../actions/index.ts';
import {
  array,
  boolean,
  intersect,
  map,
  null_,
  number,
  object,
  optional,
  record,
  set,
  string,
  union,
} from '../../schemas/index.ts';
import { config } from '../config/index.ts';
import { is } from '../is/index.ts';
import { message } from '../message/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { safeParse } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursive, type SchemaWithRecursive } from './recursive.ts';

describe('recursive', () => {
  test('should return copy of passed schema', () => {
    const schema = object({ children: array(Recur) });
    expect(recursive(schema)).toStrictEqual({
      ...(schema as Omit<typeof schema, '~types'>),
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies SchemaWithRecursive<typeof schema>);
  });

  describe('should resolve recursion through', () => {
    test('array items', () => {
      const schema = recursive(
        object({ name: string(), children: array(Recur) })
      );
      const input = {
        name: 'root',
        children: [
          { name: 'a', children: [] },
          { name: 'b', children: [{ name: 'c', children: [] }] },
        ],
      };
      expect(parse(schema, input)).toStrictEqual(input);
      expect(
        is(schema, { name: 'root', children: [{ name: 1, children: [] }] })
      ).toBe(false);
    });

    test('record values', () => {
      const schema = recursive(union([number(), record(string(), Recur)]));
      const input = { a: 1, b: { c: 2, d: { e: 3 } } };
      expect(parse(schema, input)).toStrictEqual(input);
      expect(is(schema, { a: { b: 'c' } })).toBe(false);
    });

    test('map values', () => {
      const schema = recursive(
        object({ value: number(), children: map(string(), Recur) })
      );
      const input = {
        value: 1,
        children: new Map([
          ['a', { value: 2, children: new Map() }],
          [
            'b',
            {
              value: 3,
              children: new Map([['c', { value: 4, children: new Map() }]]),
            },
          ],
        ]),
      };
      expect(parse(schema, input)).toStrictEqual(input);
      expect(
        is(schema, {
          value: 1,
          children: new Map([['a', { value: '2', children: new Map() }]]),
        })
      ).toBe(false);
    });

    test('set values', () => {
      const schema = recursive(union([string(), set(Recur)]));
      const input = new Set(['a', new Set(['b', new Set(['c'])])]);
      expect(parse(schema, input)).toStrictEqual(input);
      expect(is(schema, new Set(['a', new Set([1])]))).toBe(false);
    });

    test('union of JSON values', () => {
      const schema = recursive(
        union([
          string(),
          number(),
          boolean(),
          null_(),
          array(Recur),
          record(string(), Recur),
        ])
      );
      const input = { a: [1, 'b', { c: [true, null] }], d: { e: {} } };
      expect(parse(schema, input)).toStrictEqual(input);
      expect(is(schema, { a: [1, undefined] })).toBe(false);
    });

    test('optional entries', () => {
      const schema = recursive(
        object({ value: number(), next: optional(Recur) })
      );
      const input = { value: 1, next: { value: 2, next: { value: 3 } } };
      expect(parse(schema, input)).toStrictEqual(input);
      expect(is(schema, { value: 1, next: { value: 2, next: 3 } })).toBe(false);
    });
  });

  describe('should compose with', () => {
    test('pipe inside of recursive', () => {
      const schema = recursive(
        pipe(
          object({ value: string(), children: array(Recur) }),
          check((input) => input.children.length <= 2, 'too many'),
          transform((input) => ({ ...input, count: input.children.length }))
        )
      );
      expect(
        parse(schema, {
          value: 'root',
          children: [{ value: 'a', children: [{ value: 'b', children: [] }] }],
        })
      ).toStrictEqual({
        value: 'root',
        count: 1,
        children: [
          {
            value: 'a',
            count: 1,
            children: [{ value: 'b', count: 0, children: [] }],
          },
        ],
      });
      const result = safeParse(schema, {
        value: 'root',
        children: [
          {
            value: 'a',
            children: [
              { value: 'b', children: [] },
              { value: 'c', children: [] },
              { value: 'd', children: [] },
            ],
          },
        ],
      });
      expect(result.success).toBe(false);
      expect(result.issues?.[0].message).toBe('too many');
      expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
        'children',
        0,
      ]);
    });

    test('pipe around recursive', () => {
      const schema = pipe(
        recursive(object({ value: number(), children: array(Recur) })),
        transform((input) => input.children.length)
      );
      expect(
        parse(schema, {
          value: 1,
          children: [
            { value: 2, children: [] },
            { value: 3, children: [] },
          ],
        })
      ).toBe(2);
    });

    test('pipe around placeholder', () => {
      const schema = recursive(
        object({
          value: number(),
          children: pipe(array(Recur), minLength(1, 'empty')),
        })
      );
      const result = safeParse(schema, {
        value: 1,
        children: [{ value: 2, children: [] }],
      });
      expect(result.issues?.[0].message).toBe('empty');
      expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
        'children',
        0,
        'children',
      ]);
    });

    test('intersect', () => {
      const schema = recursive(
        intersect([
          object({ id: string() }),
          object({ children: array(Recur) }),
        ])
      );
      const input = {
        id: 'a',
        children: [{ id: 'b', children: [{ id: 'c', children: [] }] }],
      };
      expect(parse(schema, input)).toStrictEqual(input);
      expect(is(schema, { id: 'a', children: [{ id: 1, children: [] }] })).toBe(
        false
      );
    });

    test('config and message', () => {
      const schema = recursive(
        object({
          value: number(),
          children: config(message(array(Recur), 'custom'), {
            abortEarly: true,
          }),
        })
      );
      expect(
        parse(schema, { value: 1, children: [{ value: 2, children: [] }] })
      ).toStrictEqual({ value: 1, children: [{ value: 2, children: [] }] });
      const result = safeParse(schema, {
        value: 1,
        children: [{ value: 2, children: 'invalid' }],
      });
      expect(result.issues?.[0].message).toBe('custom');
    });
  });

  test('should resolve placeholder to nearest recursive schema', () => {
    const schema = recursive(
      object({
        name: string(),
        inner: optional(
          recursive(object({ value: number(), next: optional(Recur) }))
        ),
        children: array(Recur),
      })
    );
    const input = {
      name: 'root',
      inner: { value: 1, next: { value: 2 } },
      children: [
        { name: 'a', inner: { value: 3 }, children: [] },
        { name: 'b', children: [] },
      ],
    };
    expect(parse(schema, input)).toStrictEqual(input);
    expect(
      is(schema, {
        name: 'root',
        inner: { value: 1, next: { name: 'a', children: [] } },
        children: [],
      })
    ).toBe(false);
  });

  test('should return issues with nested path', () => {
    const schema = recursive(
      object({ name: string('name'), children: array(Recur) })
    );
    const result = safeParse(schema, {
      name: 'root',
      children: [
        { name: 'a', children: [] },
        { name: 'b', children: [{ name: 1, children: [] }] },
      ],
    });
    expect(result.success).toBe(false);
    expect(result.issues).toHaveLength(1);
    expect(result.issues?.[0].message).toBe('name');
    expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
      'children',
      1,
      'children',
      0,
      'name',
    ]);
  });

  test('should support Standard Schema validation', () => {
    const schema = recursive(
      object({ value: number(), children: array(Recur) })
    );
    expect(
      schema['~standard'].validate({
        value: 1,
        children: [{ value: 2, children: [] }],
      })
    ).toStrictEqual({
      typed: true,
      value: { value: 1, children: [{ value: 2, children: [] }] },
    });
  });
});

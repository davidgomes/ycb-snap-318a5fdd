import { describe, expect, test } from 'vitest';
import { check, minLength, transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  map,
  number,
  object,
  optional,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import { expectNoSchemaIssue } from '../../vitest/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { safeParse } from '../safeParse/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    const wrapped = object({ name: string(), children: array(Recur) });
    expect(recursive(wrapped)).toStrictEqual({
      kind: 'schema',
      type: 'recursive',
      reference: recursive,
      expects: 'Object',
      async: false,
      wrapped,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecursiveSchema<typeof wrapped>);
  });

  test('should return Recur schema object', () => {
    expect(Recur).toStrictEqual({
      kind: 'schema',
      type: 'recur',
      reference: recursive,
      expects: 'unknown',
      async: false,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecurSchema);
  });

  test('should throw for unresolved Recur placeholder', () => {
    const schema = object({ children: array(Recur) });
    expect(() => schema['~run']({ value: { children: [{}] } }, {})).toThrow(
      'Unresolved Recur placeholder'
    );
  });

  describe('should resolve self references', () => {
    const schema = recursive(
      object({ name: string(), children: array(Recur) })
    );

    test('for valid input', () => {
      expectNoSchemaIssue(schema, [
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

    test('for invalid nested input', () => {
      const input = {
        name: 'a',
        children: [{ name: 'b', children: [{ name: 1, children: [] }] }],
      };
      const result = safeParse(schema, input);
      expect(result.success).toBe(false);
      expect(result.issues?.length).toBe(1);
      expect(result.issues?.[0].input).toBe(1);
      expect(result.issues?.[0].path?.map((item) => item.key)).toStrictEqual([
        'children',
        0,
        'children',
        0,
        'name',
      ]);
    });

    test('for Standard Schema validation', () => {
      expect(
        schema['~standard'].validate({ name: 'a', children: [] })
      ).toStrictEqual({ typed: true, value: { name: 'a', children: [] } });
    });
  });

  describe('should resolve self references in value positions', () => {
    test('for arrays', () => {
      const schema = recursive(array(Recur));
      expect(parse(schema, [[], [[]]])).toStrictEqual([[], [[]]]);
      expect(safeParse(schema, [[], [1]]).success).toBe(false);
    });

    test('for records', () => {
      const schema = recursive(record(string(), Recur));
      expect(parse(schema, { a: {}, b: { c: {} } })).toStrictEqual({
        a: {},
        b: { c: {} },
      });
      expect(safeParse(schema, { a: { b: 1 } }).success).toBe(false);
    });

    test('for maps', () => {
      const schema = recursive(map(string(), Recur));
      const input = new Map([['a', new Map([['b', new Map()]])]]);
      expect(parse(schema, input)).toStrictEqual(input);
      expect(safeParse(schema, new Map([['a', 1]])).success).toBe(false);
    });

    test('for sets', () => {
      const schema = recursive(set(Recur));
      const input = new Set([new Set([new Set()])]);
      expect(parse(schema, input)).toStrictEqual(input);
      expect(safeParse(schema, new Set([1])).success).toBe(false);
    });

    test('for optional values', () => {
      const schema = recursive(
        object({ value: number(), next: optional(Recur) })
      );
      expect(
        parse(schema, { value: 1, next: { value: 2, next: { value: 3 } } })
      ).toStrictEqual({ value: 1, next: { value: 2, next: { value: 3 } } });
      expect(
        safeParse(schema, { value: 1, next: { value: '2' } }).success
      ).toBe(false);
    });
  });

  describe('should compose with pipe', () => {
    test('for transformations of the wrapped schema', () => {
      const schema = recursive(
        pipe(
          object({
            id: pipe(string(), transform(Number)),
            items: array(Recur),
          }),
          transform((input) => ({ ...input, count: input.items.length }))
        )
      );
      expect(
        parse(schema, {
          id: '1',
          items: [
            { id: '2', items: [] },
            { id: '3', items: [] },
          ],
        })
      ).toStrictEqual({
        id: 1,
        items: [
          { id: 2, items: [], count: 0 },
          { id: 3, items: [], count: 0 },
        ],
        count: 2,
      });
    });

    test('for validations of the placeholder', () => {
      const schema = recursive(
        object({
          name: string(),
          children: array(
            pipe(
              Recur,
              check((input) => input !== null, 'message')
            )
          ),
        })
      );
      expect(
        parse(schema, { name: 'a', children: [{ name: 'b', children: [] }] })
      ).toStrictEqual({ name: 'a', children: [{ name: 'b', children: [] }] });
    });

    test('for pipes around the recursive schema', () => {
      const schema = pipe(
        recursive(object({ children: array(Recur) })),
        check((input) => input.children.length > 0, 'message')
      );
      expect(safeParse(schema, { children: [{ children: [] }] }).success).toBe(
        true
      );
      expect(safeParse(schema, { children: [] }).success).toBe(false);
    });
  });

  test('should compose with intersect', () => {
    const schema = recursive(
      intersect([
        object({ children: array(Recur) }),
        object({ name: pipe(string(), minLength(1)) }),
      ])
    );
    expect(
      parse(schema, { name: 'a', children: [{ name: 'b', children: [] }] })
    ).toStrictEqual({ name: 'a', children: [{ name: 'b', children: [] }] });
    expect(
      safeParse(schema, { name: 'a', children: [{ name: '', children: [] }] })
        .success
    ).toBe(false);
  });

  test('should resolve placeholders to the closest recursive schema', () => {
    const inner = recursive(object({ label: string(), nested: array(Recur) }));
    const outer = recursive(object({ inner, children: array(Recur) }));
    const input = {
      inner: { label: 'a', nested: [{ label: 'b', nested: [] }] },
      children: [{ inner: { label: 'c', nested: [] }, children: [] }],
    };
    expect(parse(outer, input)).toStrictEqual(input);
    expect(
      safeParse(outer, {
        inner: { label: 'a', nested: [{ inner: 1, children: [] }] },
        children: [],
      }).success
    ).toBe(false);
  });

  test('should forward config to self references', () => {
    const schema = recursive(
      object({ name: string('message'), children: array(Recur) })
    );
    const result = safeParse(
      schema,
      { name: 1, children: [{ name: 2, children: [] }] },
      { abortEarly: true }
    );
    expect(result.issues?.length).toBe(1);
  });
});

import { describe, expectTypeOf, test } from 'vitest';
import { check, readonly, transform } from '../../actions/index.ts';
import type { ArrayIssue, ArraySchema } from '../../schemas/array/index.ts';
import {
  array,
  boolean,
  date,
  intersect,
  map,
  null_,
  number,
  object,
  type ObjectIssue,
  type ObjectSchema,
  optional,
  record,
  set,
  string,
  type StringIssue,
  union,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import { recursive, type SchemaWithRecursive } from './recursive.ts';
import type { ContainsRecur } from './types.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    const schema = object({ children: array(Recur) });
    expectTypeOf(recursive(schema)).toEqualTypeOf<
      SchemaWithRecursive<typeof schema>
    >();
  });

  describe('should infer self-referencing types', () => {
    test('for array items', () => {
      const schema = recursive(
        object({ name: string(), children: array(Recur) })
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input>().toEqualTypeOf<{
        name: string;
        children: Input[];
      }>();
      expectTypeOf(parse(schema, null)).toEqualTypeOf<{
        name: string;
        children: Output[];
      }>();
      expectTypeOf<Output['children'][number]>().toEqualTypeOf<Output>();
    });

    test('for record values', () => {
      const schema = recursive(union([number(), record(string(), Recur)]));
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<
        number | { [key: string]: Output }
      >();
    });

    test('for map values', () => {
      const schema = recursive(
        object({ value: number(), children: map(string(), Recur) })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<{
        value: number;
        children: Map<string, Output>;
      }>();
    });

    test('for set values', () => {
      const schema = recursive(union([string(), set(Recur)]));
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<string | Set<Output>>();
    });

    test('for JSON values', () => {
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
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<
        string | number | boolean | null | Output[] | { [key: string]: Output }
      >();
    });

    test('for root arrays', () => {
      const schema = recursive(array(union([string(), Recur])));
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<(string | Output)[]>();
    });

    test('for optional and readonly positions', () => {
      const schema = recursive(
        object({
          next: optional(Recur),
          items: pipe(array(Recur), readonly()),
          date: date(),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<{
        next?: Output;
        readonly items: readonly Output[];
        date: Date;
      }>();
    });

    test('for nested recursive schemas', () => {
      const schema = recursive(
        object({
          inner: recursive(object({ value: number(), next: array(Recur) })),
          children: array(Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      type Inner = Output['inner'];
      expectTypeOf<Inner>().toEqualTypeOf<{ value: number; next: Inner[] }>();
      expectTypeOf(parse(schema, null)).toEqualTypeOf<{
        inner: Inner;
        children: Output[];
      }>();
    });
  });

  describe('should compose with', () => {
    test('pipe and transformed values', () => {
      const schema = recursive(
        pipe(
          object({
            value: pipe(string(), transform(Number)),
            children: array(Recur),
          }),
          check((input) => input.children.length < 10),
          transform((input) => ({ ...input, count: input.children.length }))
        )
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input>().toEqualTypeOf<{
        value: string;
        children: Input[];
      }>();
      expectTypeOf(parse(schema, null)).toEqualTypeOf<{
        value: number;
        children: Output[];
        count: number;
      }>();
    });

    test('pipe around recursive', () => {
      const schema = pipe(
        recursive(object({ children: array(Recur) })),
        transform((input) => input.children)
      );
      type Input = InferInput<typeof schema>;
      expectTypeOf<Input>().toEqualTypeOf<{ children: Input[] }>();
      expectTypeOf(parse(schema, null)).toEqualTypeOf<Input[]>();
    });

    test('intersect', () => {
      const schema = recursive(
        intersect([
          object({ id: string() }),
          object({ children: array(Recur) }),
        ])
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parse(schema, null)).toEqualTypeOf<{
        id: string;
        children: Output[];
      }>();
      expectTypeOf<Output>().toMatchTypeOf<
        { id: string } & { children: Output[] }
      >();
    });
  });

  describe('should infer correct types', () => {
    type Wrapped = ObjectSchema<
      { children: ArraySchema<RecurSchema, undefined> },
      undefined
    >;
    type Schema = SchemaWithRecursive<Wrapped>;

    test('of issue', () => {
      expectTypeOf<InferIssue<Schema>>().toEqualTypeOf<
        ObjectIssue | ArrayIssue
      >();
    });

    test('of unresolved placeholders', () => {
      expectTypeOf<ContainsRecur<Wrapped>>().toEqualTypeOf<true>();
      expectTypeOf<ContainsRecur<Schema>>().toEqualTypeOf<false>();
    });
  });

  test('should infer issue type of wrapped schema', () => {
    const schema = recursive(
      object({ name: string(), children: array(Recur) })
    );
    expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
      ObjectIssue | ArrayIssue | StringIssue
    >();
    expectTypeOf(schema.entries.children.item).toEqualTypeOf<RecurSchema>();
  });
});

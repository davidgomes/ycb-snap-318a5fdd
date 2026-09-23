import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  type ArrayIssue,
  type ArraySchema,
  intersect,
  type IntersectIssue,
  map,
  number,
  type NumberIssue,
  object,
  type ObjectIssue,
  type ObjectSchema,
  optional,
  record,
  set,
  string,
  type StringIssue,
  type StringSchema,
  union,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';
import type { HasRecur, RecurPlaceholder } from './types.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    expectTypeOf(
      recursive(object({ name: string(), children: array(Recur) }))
    ).toEqualTypeOf<
      RecursiveSchema<
        ObjectSchema<
          {
            readonly name: StringSchema<undefined>;
            readonly children: ArraySchema<RecurSchema, undefined>;
          },
          undefined
        >
      >
    >();
  });

  test('should infer placeholder types', () => {
    expectTypeOf<InferInput<RecurSchema>>().toEqualTypeOf<RecurPlaceholder>();
    expectTypeOf<InferOutput<RecurSchema>>().toEqualTypeOf<RecurPlaceholder>();
    expectTypeOf<InferIssue<RecurSchema>>().toEqualTypeOf<never>();
  });

  describe('should infer self-referencing types', () => {
    type Schema = RecursiveSchema<
      ObjectSchema<
        {
          readonly name: StringSchema<undefined>;
          readonly children: ArraySchema<RecurSchema, undefined>;
        },
        undefined
      >
    >;
    interface Node {
      name: string;
      children: Node[];
    }

    test('of input', () => {
      type Input = InferInput<Schema>;
      expectTypeOf<Input>().toEqualTypeOf<Node>();
      expectTypeOf<Input>().toEqualTypeOf<{
        name: string;
        children: Input[];
      }>();
      expectTypeOf<HasRecur<Input>>().toEqualTypeOf<false>();
    });

    test('of output', () => {
      type Output = InferOutput<Schema>;
      expectTypeOf<Output>().toEqualTypeOf<Node>();
      expectTypeOf<Output>().toEqualTypeOf<{
        name: string;
        children: Output[];
      }>();
      expectTypeOf<HasRecur<Output>>().toEqualTypeOf<false>();
    });

    test('of issue', () => {
      expectTypeOf<InferIssue<Schema>>().toEqualTypeOf<
        ObjectIssue | StringIssue | ArrayIssue
      >();
    });
  });

  test('should infer value positions', () => {
    const schema = recursive(
      object({
        array: array(Recur),
        record: record(string(), Recur),
        map: map(string(), Recur),
        set: set(Recur),
        optional: optional(Recur),
      })
    );
    type Output = InferOutput<typeof schema>;
    expectTypeOf(parse(schema, null)).toEqualTypeOf<{
      array: Output[];
      record: { [key: string]: Output };
      map: Map<string, Output>;
      set: Set<Output>;
      optional?: Output | undefined;
    }>();
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should infer top level containers', () => {
    const arraySchema = recursive(array(Recur));
    type ArrayOutput = InferOutput<typeof arraySchema>;
    expectTypeOf(parse(arraySchema, null)).toEqualTypeOf<ArrayOutput[]>();

    const recordSchema = recursive(record(string(), Recur));
    type RecordOutput = InferOutput<typeof recordSchema>;
    expectTypeOf(parse(recordSchema, null)).toEqualTypeOf<{
      [key: string]: RecordOutput;
    }>();

    const mapSchema = recursive(map(string(), Recur));
    type MapOutput = InferOutput<typeof mapSchema>;
    expectTypeOf(parse(mapSchema, null)).toEqualTypeOf<
      Map<string, MapOutput>
    >();

    const setSchema = recursive(set(Recur));
    type SetOutput = InferOutput<typeof setSchema>;
    expectTypeOf(parse(setSchema, null)).toEqualTypeOf<Set<SetOutput>>();
  });

  test('should infer unions', () => {
    const schema = recursive(
      union([string(), number(), array(Recur), record(string(), Recur)])
    );
    type Json = string | number | Json[] | { [key: string]: Json };
    expectTypeOf(parse(schema, null)).toEqualTypeOf<Json>();
  });

  test('should preserve transformed input and output types', () => {
    const schema = recursive(
      pipe(
        object({
          id: pipe(string(), transform(Number)),
          items: array(Recur),
        }),
        transform((input) => ({ ...input, count: input.items.length }))
      )
    );
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<{ id: string; items: Input[] }>();
    expectTypeOf(parse(schema, null)).toEqualTypeOf<{
      id: number;
      items: Output[];
      count: number;
    }>();
  });

  test('should compose with intersect', () => {
    const schema = recursive(
      intersect([object({ children: array(Recur) }), object({ id: number() })])
    );
    type Output = InferOutput<typeof schema>;
    expectTypeOf(parse(schema, null)).toEqualTypeOf<{
      children: Output[];
      id: number;
    }>();
    expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
      ObjectIssue | ArrayIssue | NumberIssue | IntersectIssue
    >();
  });

  test('should compose nested recursive schemas', () => {
    const inner = recursive(object({ label: string(), nested: array(Recur) }));
    const outer = recursive(object({ inner, children: array(Recur) }));
    interface Inner {
      label: string;
      nested: Inner[];
    }
    interface Outer {
      inner: Inner;
      children: Outer[];
    }
    expectTypeOf(parse(outer, null)).toEqualTypeOf<Outer>();
    expectTypeOf<HasRecur<InferOutput<typeof outer>>>().toEqualTypeOf<false>();
  });

  test('should detect unresolved placeholders', () => {
    type Unresolved = ObjectSchema<
      { readonly children: ArraySchema<RecurSchema, undefined> },
      undefined
    >;
    expectTypeOf<HasRecur<InferInput<Unresolved>>>().toEqualTypeOf<true>();
    expectTypeOf<HasRecur<InferOutput<Unresolved>>>().toEqualTypeOf<true>();
    type Mixed = ObjectSchema<
      {
        readonly inner: RecursiveSchema<Unresolved>;
        readonly children: ArraySchema<RecurSchema, undefined>;
      },
      undefined
    >;
    expectTypeOf<HasRecur<InferOutput<Mixed>>>().toEqualTypeOf<true>();
  });
});

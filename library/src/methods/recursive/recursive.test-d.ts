import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  type ArrayIssue,
  date,
  type DateIssue,
  intersect,
  type IntersectIssue,
  map,
  type MapIssue,
  type MapSchema,
  object,
  type ObjectIssue,
  record,
  type RecordIssue,
  type RecordSchema,
  set,
  type SetIssue,
  type SetSchema,
  string,
  type StringIssue,
  type StringSchema,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur, type RecurIssue, type RecurSchema } from './Recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';

describe('recursive', () => {
  const wrapped = object({
    name: string(),
    children: array(Recur),
  });
  type Wrapped = typeof wrapped;

  test('should return schema object', () => {
    expectTypeOf(recursive(wrapped)).toEqualTypeOf<RecursiveSchema<Wrapped>>();
  });

  describe('should infer correct types', () => {
    test('of object with array values', () => {
      const schema = recursive(
        object({
          name: string(),
          children: array(Recur),
        })
      );
      interface Tree {
        name: string;
        children: Tree[];
      }
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        ObjectIssue | ArrayIssue | StringIssue | RecurIssue
      >();
    });

    test('of record values', () => {
      const schema = recursive(record(string(), Recur));
      interface Tree {
        [key: string]: Tree;
      }
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        RecordIssue | StringIssue | RecurIssue
      >();
    });

    test('of map values', () => {
      const schema = recursive(map(string(), Recur));
      type Tree = Map<string, Tree>;
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        MapIssue | StringIssue | RecurIssue
      >();
    });

    test('of set values', () => {
      const schema = recursive(set(Recur));
      type Tree = Set<Tree>;
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        SetIssue | RecurIssue
      >();
    });

    test('of object with date values', () => {
      const schema = recursive(
        object({
          name: string(),
          createdAt: date(),
          children: array(Recur),
        })
      );
      interface Tree {
        name: string;
        createdAt: Date;
        children: Tree[];
      }
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        ObjectIssue | ArrayIssue | StringIssue | DateIssue | RecurIssue
      >();
    });

    test('of transformed pipe input and output', () => {
      const schema = recursive(
        pipe(
          object({
            name: string(),
            children: array(Recur),
          }),
          transform((input) => ({
            ...input,
            size: input.name.length,
          }))
        )
      );
      interface Input {
        name: string;
        children: Input[];
      }
      interface Output {
        name: string;
        children: Output[];
        size: number;
      }
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Input>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
    });

    test('of intersect composition', () => {
      const schema = recursive(
        intersect([
          object({ name: string() }),
          object({ children: array(Recur) }),
        ])
      );
      interface Tree {
        name: string;
        children: Tree[];
      }
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        IntersectIssue | ObjectIssue | ArrayIssue | StringIssue | RecurIssue
      >();
    });

    test('of record schema type', () => {
      expectTypeOf(recursive(record(string(), Recur))).toEqualTypeOf<
        RecursiveSchema<
          RecordSchema<StringSchema<undefined>, RecurSchema, undefined>
        >
      >();
    });

    test('of map schema type', () => {
      expectTypeOf(recursive(map(string(), Recur))).toEqualTypeOf<
        RecursiveSchema<
          MapSchema<StringSchema<undefined>, RecurSchema, undefined>
        >
      >();
    });

    test('of set schema type', () => {
      expectTypeOf(recursive(set(Recur))).toEqualTypeOf<
        RecursiveSchema<SetSchema<RecurSchema, undefined>>
      >();
    });
  });
});

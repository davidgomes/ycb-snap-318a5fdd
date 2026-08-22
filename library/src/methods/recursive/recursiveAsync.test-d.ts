import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  arrayAsync,
  type ArrayIssue,
  intersectAsync,
  type IntersectIssue,
  mapAsync,
  type MapIssue,
  objectAsync,
  type ObjectIssue,
  recordAsync,
  type RecordIssue,
  setAsync,
  type SetIssue,
  string,
  type StringIssue,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur, type RecurIssue } from './Recur.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  const wrapped = objectAsync({
    name: string(),
    children: arrayAsync(Recur),
  });
  type Wrapped = typeof wrapped;

  test('should return schema object', () => {
    expectTypeOf(recursiveAsync(wrapped)).toEqualTypeOf<
      RecursiveSchemaAsync<Wrapped>
    >();
  });

  describe('should infer correct types', () => {
    test('of object with array values', () => {
      const schema = recursiveAsync(
        objectAsync({
          name: string(),
          children: arrayAsync(Recur),
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
      const schema = recursiveAsync(recordAsync(string(), Recur));
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
      const schema = recursiveAsync(mapAsync(string(), Recur));
      type Tree = Map<string, Tree>;
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        MapIssue | StringIssue | RecurIssue
      >();
    });

    test('of set values', () => {
      const schema = recursiveAsync(setAsync(Recur));
      type Tree = Set<Tree>;
      expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Tree>();
      expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
        SetIssue | RecurIssue
      >();
    });

    test('of transformed pipe input and output', () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({
            name: string(),
            children: arrayAsync(Recur),
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
      const schema = recursiveAsync(
        intersectAsync([
          objectAsync({ name: string() }),
          objectAsync({ children: arrayAsync(Recur) }),
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
  });
});

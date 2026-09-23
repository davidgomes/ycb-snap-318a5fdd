import { describe, expectTypeOf, test } from 'vitest';
import { transformAsync } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  intersectAsync,
  mapAsync,
  number,
  object,
  objectAsync,
  recordAsync,
  setAsync,
  string,
  unionAsync,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur } from './recur.ts';
import {
  recursiveAsync,
  type SchemaWithRecursiveAsync,
} from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should return schema object', () => {
    const schema = objectAsync({ children: arrayAsync(Recur) });
    expectTypeOf(recursiveAsync(schema)).toEqualTypeOf<
      SchemaWithRecursiveAsync<typeof schema>
    >();
    expectTypeOf(recursiveAsync(schema).async).toEqualTypeOf<true>();
  });

  describe('should infer self-referencing types', () => {
    test('for array items', () => {
      const schema = recursiveAsync(
        objectAsync({ name: string(), children: arrayAsync(Recur) })
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input>().toEqualTypeOf<{
        name: string;
        children: Input[];
      }>();
      expectTypeOf(parseAsync(schema, null)).resolves.toEqualTypeOf<{
        name: string;
        children: Output[];
      }>();
    });

    test('for record, map and set values', () => {
      const schema = recursiveAsync(
        unionAsync([
          number(),
          recordAsync(string(), Recur),
          mapAsync(string(), Recur),
          setAsync(Recur),
        ])
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parseAsync(schema, null)).resolves.toEqualTypeOf<
        number | { [key: string]: Output } | Map<string, Output> | Set<Output>
      >();
    });

    test('for sync schemas', () => {
      const schema = recursiveAsync(object({ children: array(Recur) }));
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parseAsync(schema, null)).resolves.toEqualTypeOf<{
        children: Output[];
      }>();
    });
  });

  describe('should compose with', () => {
    test('pipe and transformed values', () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({ value: string(), children: arrayAsync(Recur) }),
          transformAsync(async (input) => ({
            ...input,
            count: input.children.length,
          }))
        )
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input>().toEqualTypeOf<{
        value: string;
        children: Input[];
      }>();
      expectTypeOf(parseAsync(schema, null)).resolves.toEqualTypeOf<{
        value: string;
        children: Output[];
        count: number;
      }>();
    });

    test('intersect', () => {
      const schema = recursiveAsync(
        intersectAsync([
          objectAsync({ id: string() }),
          objectAsync({ children: arrayAsync(Recur) }),
        ])
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf(parseAsync(schema, null)).resolves.toEqualTypeOf<{
        id: string;
        children: Output[];
      }>();
    });
  });
});

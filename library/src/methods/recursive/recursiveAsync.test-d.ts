/* eslint-disable @typescript-eslint/no-unused-vars -- values are inspected with typeof */
import { describe, expectTypeOf, test } from 'vitest';
import { transformAsync } from '../../actions/index.ts';
import {
  arrayAsync,
  intersectAsync,
  mapAsync,
  nullableAsync,
  number,
  objectAsync,
  recordAsync,
  setAsync,
  string,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { parseAsync } from '../parse/parseAsync.ts';
import { pipeAsync } from '../pipe/index.ts';
import { safeParseAsync } from '../safeParse/safeParseAsync.ts';
import { Recur } from './recur.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should return recursive schema', () => {
    const wrapped = objectAsync({ child: nullableAsync(Recur) });
    expectTypeOf(recursiveAsync(wrapped)).toEqualTypeOf<
      RecursiveSchemaAsync<typeof wrapped>
    >();
  });

  describe('should infer self-referential types', () => {
    test('for nullable child', () => {
      const schema = recursiveAsync(
        objectAsync({
          value: string(),
          child: nullableAsync(Recur),
        })
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input['value']>().toEqualTypeOf<string>();
      expectTypeOf<Input['child']>().toEqualTypeOf<Input | null>();
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | null>();
    });

    test('for array, record, map and set values', () => {
      const schema = recursiveAsync(
        objectAsync({
          values: arrayAsync(Recur),
          record: recordAsync(string(), Recur),
          map: mapAsync(string(), Recur),
          set: setAsync(Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['values']>().toEqualTypeOf<Output[]>();
      expectTypeOf<Output['record']>().toEqualTypeOf<Record<string, Output>>();
      expectTypeOf<Output['map']>().toEqualTypeOf<Map<string, Output>>();
      expectTypeOf<Output['set']>().toEqualTypeOf<Set<Output>>();
    });

    test('for pipe transformation', () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({
            value: string(),
            child: nullableAsync(Recur),
          }),
          transformAsync(async (input) => ({
            length: input.value.length,
            child: input.child,
          }))
        )
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input['value']>().toEqualTypeOf<string>();
      expectTypeOf<Input['child']>().toEqualTypeOf<Input | null>();
      expectTypeOf<Output['length']>().toEqualTypeOf<number>();
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | null>();
    });

    test('for intersect', () => {
      const schema = recursiveAsync(
        intersectAsync([
          objectAsync({
            value: string(),
            child: nullableAsync(Recur),
          }),
          objectAsync({ id: number() }),
        ])
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['value']>().toEqualTypeOf<string>();
      expectTypeOf<Output['id']>().toEqualTypeOf<number>();
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | null>();
    });
  });

  test('should accept resolved schema', () => {
    const schema = recursiveAsync(
      objectAsync({
        value: string(),
        child: nullableAsync(Recur),
      })
    );
    expectTypeOf(parseAsync(schema, { value: 'a', child: null })).toEqualTypeOf<
      Promise<InferOutput<typeof schema>>
    >();
    expectTypeOf(
      safeParseAsync(schema, { value: 'a', child: null })
    ).toEqualTypeOf<ReturnType<typeof safeParseAsync<typeof schema>>>();
  });
});

import { describe, expectTypeOf, test } from 'vitest';
import { transformAsync } from '../../actions/index.ts';
import {
  arrayAsync,
  mapAsync,
  objectAsync,
  optionalAsync,
  recordAsync,
  setAsync,
  string,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { pipeAsync } from '../pipe/pipeAsync.ts';
import { Recur } from './recur.ts';
import { recursiveAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should self-reference async array items', () => {
    const schema = recursiveAsync(
      objectAsync({
        value: string(),
        children: arrayAsync(Recur),
      })
    );
    expectTypeOf(schema.async).toEqualTypeOf<true>();
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input['children'][number]>().toEqualTypeOf<Input>();
    expectTypeOf<Output['children'][number]>().toEqualTypeOf<Output>();
    expectTypeOf<Input['value']>().toEqualTypeOf<string>();
  });

  test('should keep async transformed input and output distinct', () => {
    const schema = recursiveAsync(
      objectAsync({
        value: pipeAsync(
          string(),
          transformAsync(async (input) => input.length)
        ),
        child: optionalAsync(Recur),
      })
    );
    expectTypeOf(schema.async).toEqualTypeOf<true>();
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input['value']>().toEqualTypeOf<string>();
    expectTypeOf<Output['value']>().toEqualTypeOf<number>();
    expectTypeOf<NonNullable<Input['child']>>().toEqualTypeOf<Input>();
    expectTypeOf<NonNullable<Output['child']>>().toEqualTypeOf<Output>();
  });

  test('should self-reference async record, map, and set values', () => {
    const recordSchema = recursiveAsync(recordAsync(string(), Recur));
    expectTypeOf(recordSchema.async).toEqualTypeOf<true>();
    type RecordOutput = InferOutput<typeof recordSchema>;
    expectTypeOf<RecordOutput>().not.toEqualTypeOf<unknown>();
    expectTypeOf<RecordOutput[string]>().toExtend<RecordOutput>();
    expectTypeOf<RecordOutput[string]>().not.toEqualTypeOf<unknown>();

    const mapSchema = recursiveAsync(mapAsync(string(), Recur));
    expectTypeOf(mapSchema.async).toEqualTypeOf<true>();
    type MapValue =
      InferOutput<typeof mapSchema> extends Map<string, infer TValue>
        ? TValue
        : never;
    expectTypeOf<MapValue>().toEqualTypeOf<InferOutput<typeof mapSchema>>();

    const setSchema = recursiveAsync(setAsync(Recur));
    expectTypeOf(setSchema.async).toEqualTypeOf<true>();
    type SetValue =
      InferOutput<typeof setSchema> extends Set<infer TValue> ? TValue : never;
    expectTypeOf<SetValue>().toEqualTypeOf<InferOutput<typeof setSchema>>();
  });
});

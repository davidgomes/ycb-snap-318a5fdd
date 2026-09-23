import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
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
import type { InferInput, InferOutput } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';

describe('recursive', () => {
  test('should self-reference array items', () => {
    const schema = recursive(
      object({
        value: string(),
        children: array(Recur),
      })
    );
    expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<Output>();
    expectTypeOf<Input['children'][number]>().toEqualTypeOf<Input>();
    expectTypeOf<Input['value']>().toEqualTypeOf<string>();
  });

  test('should keep transformed input and output distinct', () => {
    const schema = recursive(
      object({
        value: pipe(
          string(),
          transform((input) => input.length)
        ),
        child: optional(Recur),
      })
    );
    expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input['value']>().toEqualTypeOf<string>();
    expectTypeOf<Output['value']>().toEqualTypeOf<number>();
    expectTypeOf<NonNullable<Input['child']>>().toEqualTypeOf<Input>();
    expectTypeOf<NonNullable<Output['child']>>().toEqualTypeOf<Output>();
  });

  test('should self-reference record, map, and set values', () => {
    const recordSchema = recursive(record(string(), Recur));
    expectTypeOf(recordSchema.type).toEqualTypeOf<'recursive'>();
    type RecordOutput = InferOutput<typeof recordSchema>;
    expectTypeOf<RecordOutput>().not.toEqualTypeOf<unknown>();
    expectTypeOf<RecordOutput[string]>().toExtend<RecordOutput>();
    expectTypeOf<RecordOutput[string]>().not.toEqualTypeOf<unknown>();

    const mapSchema = recursive(map(string(), Recur));
    expectTypeOf(mapSchema.type).toEqualTypeOf<'recursive'>();
    type MapOutput = InferOutput<typeof mapSchema>;
    type MapValue =
      MapOutput extends Map<string, infer TValue> ? TValue : never;
    expectTypeOf<MapValue>().toEqualTypeOf<MapOutput>();

    const setSchema = recursive(set(Recur));
    expectTypeOf(setSchema.type).toEqualTypeOf<'recursive'>();
    type SetOutput = InferOutput<typeof setSchema>;
    type SetValue = SetOutput extends Set<infer TValue> ? TValue : never;
    expectTypeOf<SetValue>().toEqualTypeOf<SetOutput>();
  });

  test('should compose through pipe and intersect', () => {
    const piped = recursive(
      pipe(
        object({
          name: string(),
          nodes: array(Recur),
        }),
        transform((input) => input.nodes.length)
      )
    );
    expectTypeOf(piped.type).toEqualTypeOf<'recursive'>();
    type PipedInput = InferInput<typeof piped>;
    expectTypeOf<PipedInput['nodes'][number]>().toEqualTypeOf<PipedInput>();
    expectTypeOf<PipedInput['name']>().toEqualTypeOf<string>();
    expectTypeOf<InferOutput<typeof piped>>().toEqualTypeOf<number>();

    const intersected = recursive(
      intersect([
        object({ id: string(), child: optional(Recur) }),
        object({ count: number() }),
      ])
    );
    expectTypeOf(intersected.type).toEqualTypeOf<'recursive'>();
    type Intersected = InferOutput<typeof intersected>;
    expectTypeOf<Intersected['id']>().toEqualTypeOf<string>();
    expectTypeOf<Intersected['count']>().toEqualTypeOf<number>();
    expectTypeOf<
      NonNullable<Intersected['child']>
    >().toEqualTypeOf<Intersected>();
  });
});

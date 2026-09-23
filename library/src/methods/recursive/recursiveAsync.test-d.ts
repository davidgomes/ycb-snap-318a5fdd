import { describe, expectTypeOf, test } from 'vitest';
import {
  type CheckActionAsync,
  type CheckIssue,
  transformAsync,
} from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  type ArrayIssue,
  type ArraySchemaAsync,
  intersectAsync,
  mapAsync,
  number,
  object,
  objectAsync,
  type ObjectIssue,
  type ObjectSchemaAsync,
  recordAsync,
  setAsync,
  string,
  type StringIssue,
  type StringSchema,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { parseAsync } from '../parse/index.ts';
import type { SchemaWithPipeAsync } from '../pipe/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should return schema object', () => {
    expectTypeOf(
      recursiveAsync(
        objectAsync({ name: string(), children: arrayAsync(Recur) })
      )
    ).toEqualTypeOf<
      RecursiveSchemaAsync<
        ObjectSchemaAsync<
          {
            readonly name: StringSchema<undefined>;
            readonly children: ArraySchemaAsync<RecurSchema, undefined>;
          },
          undefined
        >
      >
    >();
  });

  describe('should infer self-referencing types', () => {
    type Schema = RecursiveSchemaAsync<
      ObjectSchemaAsync<
        {
          readonly name: SchemaWithPipeAsync<
            readonly [
              StringSchema<undefined>,
              CheckActionAsync<string, undefined>,
            ]
          >;
          readonly children: ArraySchemaAsync<RecurSchema, undefined>;
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
    });

    test('of output', () => {
      type Output = InferOutput<Schema>;
      expectTypeOf<Output>().toEqualTypeOf<Node>();
      expectTypeOf<Output>().toEqualTypeOf<{
        name: string;
        children: Output[];
      }>();
    });

    test('of issue', () => {
      expectTypeOf<InferIssue<Schema>>().toEqualTypeOf<
        ObjectIssue | StringIssue | CheckIssue<string> | ArrayIssue
      >();
    });
  });

  test('should infer value positions', async () => {
    const schema = recursiveAsync(
      objectAsync({
        array: arrayAsync(Recur),
        record: recordAsync(string(), Recur),
        map: mapAsync(string(), Recur),
        set: setAsync(Recur),
      })
    );
    type Output = InferOutput<typeof schema>;
    expectTypeOf(await parseAsync(schema, null)).toEqualTypeOf<{
      array: Output[];
      record: { [key: string]: Output };
      map: Map<string, Output>;
      set: Set<Output>;
    }>();
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should infer sync wrapped schemas', async () => {
    const schema = recursiveAsync(object({ children: array(Recur) }));
    type Output = InferOutput<typeof schema>;
    expectTypeOf(await parseAsync(schema, null)).toEqualTypeOf<{
      children: Output[];
    }>();
  });

  test('should preserve transformed input and output types', async () => {
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({
          id: pipeAsync(
            string(),
            transformAsync(async (input) => Number(input))
          ),
          items: arrayAsync(Recur),
        }),
        transformAsync(async (input) => ({
          ...input,
          count: input.items.length,
        }))
      )
    );
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<{ id: string; items: Input[] }>();
    expectTypeOf(await parseAsync(schema, null)).toEqualTypeOf<{
      id: number;
      items: Output[];
      count: number;
    }>();
  });

  test('should compose with intersect', async () => {
    const schema = recursiveAsync(
      intersectAsync([
        objectAsync({ children: arrayAsync(Recur) }),
        object({ id: number() }),
      ])
    );
    type Output = InferOutput<typeof schema>;
    expectTypeOf(await parseAsync(schema, null)).toEqualTypeOf<{
      children: Output[];
      id: number;
    }>();
  });
});

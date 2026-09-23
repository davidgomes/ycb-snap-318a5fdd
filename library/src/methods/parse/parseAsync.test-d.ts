import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  arrayAsync,
  object,
  objectAsync,
  string,
  unionAsync,
} from '../../schemas/index.ts';
import type {
  GenericSchema,
  GenericSchemaAsync,
  InferOutput,
} from '../../types/index.ts';
import { pipe, pipeAsync } from '../pipe/index.ts';
import { Recur, type RecurMarker, recursiveAsync } from '../recursive/index.ts';
import { parseAsync } from './parseAsync.ts';

describe('parseAsync', () => {
  test('should return output type of schema', () => {
    expectTypeOf(
      parseAsync(
        object({
          key: pipe(
            string(),
            transform((input) => input.length)
          ),
        }),
        { key: 'foo' }
      )
    ).toEqualTypeOf<Promise<{ key: number }>>();
  });

  test('should return output type of recursive schema', () => {
    const schema = recursiveAsync(
      objectAsync({ key: string(), children: arrayAsync(Recur) })
    );
    interface Output {
      key: string;
      children: Output[];
    }
    expectTypeOf(parseAsync(schema, {})).toEqualTypeOf<Promise<Output>>();
  });

  test('should reject unresolved Recur placeholders', () => {
    const inputOnly = pipeAsync(
      arrayAsync(Recur),
      transform((input) => input.length)
    );
    const outputOnly = pipeAsync(
      string(),
      transform(() => ({}) as RecurMarker)
    );
    // @ts-expect-error
    parseAsync(Recur, {});
    // @ts-expect-error
    parseAsync(objectAsync({ children: arrayAsync(Recur) }), {});
    // @ts-expect-error
    parseAsync(inputOnly, {});
    // @ts-expect-error
    parseAsync(outputOnly, {});
    // @ts-expect-error
    parseAsync(unionAsync([string(), Recur]), {});
  });

  test('should accept schemas with generic types', () => {
    function parseGeneric<TSchema extends GenericSchema | GenericSchemaAsync>(
      schema: TSchema
    ): Promise<InferOutput<TSchema>> {
      return parseAsync(schema, {});
    }
    expectTypeOf(parseGeneric(string())).toEqualTypeOf<Promise<string>>();
  });
});

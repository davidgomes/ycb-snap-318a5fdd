import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  arrayAsync,
  object,
  objectAsync,
  string,
  type StringSchema,
  unionAsync,
} from '../../schemas/index.ts';
import type { GenericSchema, GenericSchemaAsync } from '../../types/index.ts';
import { pipe, pipeAsync } from '../pipe/index.ts';
import { Recur, type RecurMarker, recursiveAsync } from '../recursive/index.ts';
import { safeParseAsync } from './safeParseAsync.ts';
import type { SafeParseResult } from './types.ts';

describe('safeParseAsync', () => {
  test('should return safe parse result', () => {
    const schema = object({
      key: pipe(
        string(),
        transform((input) => input.length)
      ),
    });
    expectTypeOf(safeParseAsync(schema, { key: 'foo' })).toEqualTypeOf<
      Promise<SafeParseResult<typeof schema>>
    >();
  });

  test('should return safe parse result of recursive schema', async () => {
    const schema = recursiveAsync(
      objectAsync({ key: string(), children: arrayAsync(Recur) })
    );
    interface Output {
      key: string;
      children: Output[];
    }
    const result = await safeParseAsync(schema, {});
    expectTypeOf(result).toEqualTypeOf<SafeParseResult<typeof schema>>();
    if (result.success) {
      expectTypeOf(result.output).toEqualTypeOf<Output>();
    }
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
    safeParseAsync(Recur, {});
    // @ts-expect-error
    safeParseAsync(objectAsync({ children: arrayAsync(Recur) }), {});
    // @ts-expect-error
    safeParseAsync(inputOnly, {});
    // @ts-expect-error
    safeParseAsync(outputOnly, {});
    // @ts-expect-error
    safeParseAsync(unionAsync([string(), Recur]), {});
  });

  test('should accept schemas with generic types', () => {
    function safeParseGeneric<
      TSchema extends GenericSchema | GenericSchemaAsync,
    >(schema: TSchema): Promise<SafeParseResult<TSchema>> {
      return safeParseAsync(schema, {});
    }
    expectTypeOf(safeParseGeneric(string())).toEqualTypeOf<
      Promise<SafeParseResult<StringSchema<undefined>>>
    >();
  });
});

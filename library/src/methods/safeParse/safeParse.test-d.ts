import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  object,
  string,
  type StringSchema,
  union,
} from '../../schemas/index.ts';
import type { GenericSchema } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, type RecurMarker, recursive } from '../recursive/index.ts';
import { safeParse } from './safeParse.ts';
import type { SafeParseResult } from './types.ts';

describe('safeParse', () => {
  test('should return safe parse result', () => {
    const schema = object({
      key: pipe(
        string(),
        transform((input) => input.length)
      ),
    });
    expectTypeOf(safeParse(schema, { key: 'foo' })).toEqualTypeOf<
      SafeParseResult<typeof schema>
    >();
  });

  test('should return safe parse result of recursive schema', () => {
    const schema = recursive(object({ key: string(), children: array(Recur) }));
    interface Output {
      key: string;
      children: Output[];
    }
    const result = safeParse(schema, {});
    expectTypeOf(result).toEqualTypeOf<SafeParseResult<typeof schema>>();
    if (result.success) {
      expectTypeOf(result.output).toEqualTypeOf<Output>();
    }
  });

  test('should reject unresolved Recur placeholders', () => {
    const inputOnly = pipe(
      array(Recur),
      transform((input) => input.length)
    );
    const outputOnly = pipe(
      string(),
      transform(() => ({}) as RecurMarker)
    );
    // @ts-expect-error
    safeParse(Recur, {});
    // @ts-expect-error
    safeParse(object({ children: array(Recur) }), {});
    // @ts-expect-error
    safeParse(inputOnly, {});
    // @ts-expect-error
    safeParse(outputOnly, {});
    // @ts-expect-error
    safeParse(union([string(), Recur]), {});
  });

  test('should accept schemas with generic types', () => {
    function safeParseGeneric<TSchema extends GenericSchema>(
      schema: TSchema
    ): SafeParseResult<TSchema> {
      return safeParse(schema, {});
    }
    expectTypeOf(safeParseGeneric(string())).toEqualTypeOf<
      SafeParseResult<StringSchema<undefined>>
    >();
  });
});

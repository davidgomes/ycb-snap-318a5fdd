import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string } from '../../schemas/index.ts';
import type { GenericSchema } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursive, type RecurType } from '../recursive/index.ts';
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
    const schema = recursive(object({ children: array(Recur) }));
    expectTypeOf(safeParse(schema, {})).toEqualTypeOf<
      SafeParseResult<typeof schema>
    >();
  });

  test('should reject unresolved Recur placeholders', () => {
    const schema1 = object({ children: array(Recur) });
    const schema2 = pipe(
      array(Recur),
      transform(() => 1)
    );
    const schema3 = pipe(
      string(),
      transform((): RecurType[] => [])
    );
    // @ts-expect-error
    safeParse(Recur, {});
    // @ts-expect-error
    safeParse(schema1, {});
    // @ts-expect-error
    safeParse(schema2, {});
    // @ts-expect-error
    safeParse(schema3, {});
  });

  test('should accept generic schemas', () => {
    function parse<TSchema extends GenericSchema>(schema: TSchema) {
      return safeParse(schema, {});
    }
    expectTypeOf(parse).toBeFunction();
  });
});

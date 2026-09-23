import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string, unknown } from '../../schemas/index.ts';
import type { GenericSchema } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursive } from '../recursive/index.ts';
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

  test('should accept resolved recursive schemas', () => {
    const schema = recursive(object({ children: array(Recur) }));
    type Output = { children: Output[] };
    expectTypeOf(safeParse(schema, { children: [] })).toEqualTypeOf<
      SafeParseResult<typeof schema>
    >();
    const result = safeParse(schema, { children: [] });
    if (result.success) {
      expectTypeOf(result.output).toEqualTypeOf<Output>();
    }
  });

  test('should accept generic schemas', () => {
    function generic<TSchema extends GenericSchema>(schema: TSchema) {
      return safeParse(schema, null);
    }
    expectTypeOf(generic).toBeFunction();
  });

  test('should reject unresolved Recur placeholders', () => {
    const unresolved = object({ children: array(Recur) });
    const inputOnly = pipe(
      array(Recur),
      transform((input) => input.length)
    );
    const outputOnly = pipe(
      unknown(),
      transform(() => []),
      array(Recur)
    );
    // @ts-expect-error
    safeParse(unresolved, {});
    // @ts-expect-error
    safeParse(Recur, {});
    // @ts-expect-error
    safeParse(inputOnly, []);
    // @ts-expect-error
    safeParse(outputOnly, []);
  });
});

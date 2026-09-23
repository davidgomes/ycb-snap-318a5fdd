import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string, unknown } from '../../schemas/index.ts';
import type { GenericSchema } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursive } from '../recursive/index.ts';
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

  test('should accept resolved recursive schemas', async () => {
    const schema = recursive(object({ children: array(Recur) }));
    interface Output {
      children: Output[];
    }
    expectTypeOf(safeParseAsync(schema, { children: [] })).toEqualTypeOf<
      Promise<SafeParseResult<typeof schema>>
    >();
    const result = await safeParseAsync(schema, { children: [] });
    if (result.success) {
      expectTypeOf(result.output).toEqualTypeOf<Output>();
    }
  });

  test('should accept generic schemas', () => {
    function generic<TSchema extends GenericSchema>(schema: TSchema) {
      return safeParseAsync(schema, null);
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
    safeParseAsync(unresolved, {});
    // @ts-expect-error
    safeParseAsync(Recur, {});
    // @ts-expect-error
    safeParseAsync(inputOnly, []);
    // @ts-expect-error
    safeParseAsync(outputOnly, []);
  });
});

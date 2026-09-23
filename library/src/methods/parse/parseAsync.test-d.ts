import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string, unknown } from '../../schemas/index.ts';
import type { GenericSchema } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursive } from '../recursive/index.ts';
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

  test('should accept resolved recursive schemas', () => {
    const schema = recursive(object({ children: array(Recur) }));
    interface Output {
      children: Output[];
    }
    expectTypeOf(parseAsync(schema, { children: [] })).toEqualTypeOf<
      Promise<Output>
    >();
  });

  test('should accept generic schemas', () => {
    function generic<TSchema extends GenericSchema>(schema: TSchema) {
      return parseAsync(schema, null);
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
    parseAsync(unresolved, {});
    // @ts-expect-error
    parseAsync(Recur, {});
    // @ts-expect-error
    parseAsync(inputOnly, []);
    // @ts-expect-error
    parseAsync(outputOnly, []);
  });
});

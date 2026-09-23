import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string, unknown } from '../../schemas/index.ts';
import type { GenericSchema } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursive } from '../recursive/index.ts';
import { parse } from './parse.ts';

describe('parse', () => {
  test('should return output type of schema', () => {
    expectTypeOf(
      parse(
        object({
          key: pipe(
            string(),
            transform((input) => input.length)
          ),
        }),
        { key: 'foo' }
      )
    ).toEqualTypeOf<{ key: number }>();
  });

  test('should accept resolved recursive schemas', () => {
    const schema = recursive(object({ children: array(Recur) }));
    type Output = { children: Output[] };
    expectTypeOf(parse(schema, { children: [] })).toEqualTypeOf<Output>();
  });

  test('should accept generic schemas', () => {
    function generic<TSchema extends GenericSchema>(schema: TSchema) {
      return parse(schema, null);
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
    parse(unresolved, {});
    // @ts-expect-error
    parse(Recur, {});
    // @ts-expect-error
    parse(inputOnly, []);
    // @ts-expect-error
    parse(outputOnly, []);
  });
});

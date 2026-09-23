import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string } from '../../schemas/index.ts';
import type { InferOutput } from '../../types/index.ts';
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

  test('should reject unresolved Recur placeholders', () => {
    const inputOnly = pipe(
      object({ child: Recur, name: string() }),
      transform((input) => input.name)
    );
    // @ts-expect-error
    parse(inputOnly, { child: {}, name: 'a' });

    const outputOnly = pipe(
      string(),
      transform((input): InferOutput<typeof Recur> => input as never)
    );
    // @ts-expect-error
    parse(outputOnly, 'a');

    const schema = recursive(
      object({
        value: string(),
        children: array(Recur),
      })
    );
    expectTypeOf(parse(schema, { value: 'a', children: [] })).toEqualTypeOf<
      InferOutput<typeof schema>
    >();
  });
});

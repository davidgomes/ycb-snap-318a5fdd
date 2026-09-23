import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  arrayAsync,
  object,
  objectAsync,
  string,
} from '../../schemas/index.ts';
import type { InferOutput } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursiveAsync } from '../recursive/index.ts';
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

  test('should reject unresolved Recur placeholders', () => {
    const inputOnly = pipe(
      object({ child: Recur, name: string() }),
      transform((input) => input.name)
    );
    // @ts-expect-error
    parseAsync(inputOnly, { child: {}, name: 'a' });

    const outputOnly = pipe(
      string(),
      transform((input): InferOutput<typeof Recur> => input as never)
    );
    // @ts-expect-error
    parseAsync(outputOnly, 'a');

    const schema = recursiveAsync(
      objectAsync({
        value: string(),
        children: arrayAsync(Recur),
      })
    );
    expectTypeOf(
      parseAsync(schema, { value: 'a', children: [] })
    ).toEqualTypeOf<Promise<InferOutput<typeof schema>>>();
  });
});

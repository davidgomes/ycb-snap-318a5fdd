import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { object, string } from '../../schemas/index.ts';
import type { InferOutput } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur } from '../recursive/index.ts';
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

  test('should reject unresolved Recur placeholders', () => {
    const inputOnly = pipe(
      object({ child: Recur, name: string() }),
      transform((input) => input.name)
    );
    // @ts-expect-error
    safeParse(inputOnly, { child: {}, name: 'a' });

    const outputOnly = pipe(
      string(),
      transform((input): InferOutput<typeof Recur> => input as never)
    );
    // @ts-expect-error
    safeParse(outputOnly, 'a');
  });
});

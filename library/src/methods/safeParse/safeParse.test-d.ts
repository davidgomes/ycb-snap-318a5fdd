import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string } from '../../schemas/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur } from '../recursive/Recur.ts';
import { recursive } from '../recursive/recursive.ts';
import type { RecurPlaceholder } from '../recursive/types.ts';
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

  test('should return result type of recursive schema', () => {
    const schema = recursive(
      object({
        name: string(),
        children: array(Recur),
      })
    );
    expectTypeOf(
      safeParse(schema, { name: 'root', children: [] })
    ).toEqualTypeOf<SafeParseResult<typeof schema>>();
  });

  test('should reject unresolved Recur placeholder schema', () => {
    // @ts-expect-error Recur must be wrapped with recursive()
    safeParse(Recur, { name: 'root', children: [] });
  });

  test('should reject unresolved Recur in input type', () => {
    const schema = object({
      name: string(),
      children: array(Recur),
    });
    // @ts-expect-error Recur must be wrapped with recursive()
    safeParse(schema, { name: 'root', children: [] });
  });

  test('should reject unresolved Recur in output type only', () => {
    const schema = pipe(
      string(),
      transform((input): RecurPlaceholder => input as never)
    );
    // @ts-expect-error Recur must be wrapped with recursive()
    safeParse(schema, 'foo');
  });

  test('should reject unresolved Recur in input type only', () => {
    const schema = pipe(
      object({
        child: Recur,
      }),
      transform(() => 123)
    );
    // @ts-expect-error Recur must be wrapped with recursive()
    safeParse(schema, { child: 'foo' });
  });
});

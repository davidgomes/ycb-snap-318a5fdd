import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string } from '../../schemas/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur } from '../recursive/Recur.ts';
import { recursive } from '../recursive/recursive.ts';
import type { RecurPlaceholder } from '../recursive/types.ts';
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

  test('should return output type of recursive schema', () => {
    const schema = recursive(
      object({
        name: string(),
        children: array(Recur),
      })
    );
    interface Tree {
      name: string;
      children: Tree[];
    }
    expectTypeOf(
      parse(schema, { name: 'root', children: [] })
    ).toEqualTypeOf<Tree>();
  });

  test('should reject unresolved Recur placeholder schema', () => {
    // @ts-expect-error Recur must be wrapped with recursive()
    parse(Recur, { name: 'root', children: [] });
  });

  test('should reject unresolved Recur in input type', () => {
    const schema = object({
      name: string(),
      children: array(Recur),
    });
    // @ts-expect-error Recur must be wrapped with recursive()
    parse(schema, { name: 'root', children: [] });
  });

  test('should reject unresolved Recur in output type only', () => {
    const schema = pipe(
      string(),
      transform((input): RecurPlaceholder => input as never)
    );
    // @ts-expect-error Recur must be wrapped with recursive()
    parse(schema, 'foo');
  });

  test('should reject unresolved Recur in input type only', () => {
    const schema = pipe(
      object({
        child: Recur,
      }),
      transform(() => 123)
    );
    // @ts-expect-error Recur must be wrapped with recursive()
    parse(schema, { child: 'foo' });
  });
});

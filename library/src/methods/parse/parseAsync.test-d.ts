import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string } from '../../schemas/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur } from '../recursive/Recur.ts';
import { recursiveAsync } from '../recursive/recursiveAsync.ts';
import type { RecurPlaceholder } from '../recursive/types.ts';
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

  test('should return output type of recursive schema', () => {
    const schema = recursiveAsync(
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
      parseAsync(schema, { name: 'root', children: [] })
    ).resolves.toEqualTypeOf<Tree>();
  });

  test('should reject unresolved Recur placeholder schema', () => {
    // @ts-expect-error Recur must be wrapped with recursiveAsync()
    parseAsync(Recur, { name: 'root', children: [] });
  });

  test('should reject unresolved Recur in input type', () => {
    const schema = object({
      name: string(),
      children: array(Recur),
    });
    // @ts-expect-error Recur must be wrapped with recursiveAsync()
    parseAsync(schema, { name: 'root', children: [] });
  });

  test('should reject unresolved Recur in output type only', () => {
    const schema = pipe(
      string(),
      transform((input): RecurPlaceholder => input as never)
    );
    // @ts-expect-error Recur must be wrapped with recursiveAsync()
    parseAsync(schema, 'foo');
  });

  test('should reject unresolved Recur in input type only', () => {
    const schema = pipe(
      object({
        child: Recur,
      }),
      transform(() => 123)
    );
    // @ts-expect-error Recur must be wrapped with recursiveAsync()
    parseAsync(schema, { child: 'foo' });
  });
});

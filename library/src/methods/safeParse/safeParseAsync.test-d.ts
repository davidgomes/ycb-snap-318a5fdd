import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  object,
  objectAsync,
  string,
} from '../../schemas/index.ts';
import type { GenericSchemaAsync } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursiveAsync, type RecurType } from '../recursive/index.ts';
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

  test('should return safe parse result of recursive schema', () => {
    const schema = recursiveAsync(objectAsync({ children: arrayAsync(Recur) }));
    expectTypeOf(safeParseAsync(schema, {})).toEqualTypeOf<
      Promise<SafeParseResult<typeof schema>>
    >();
  });

  test('should reject unresolved Recur placeholders', () => {
    const schema1 = objectAsync({ children: arrayAsync(Recur) });
    const schema2 = pipe(
      array(Recur),
      transform(() => 1)
    );
    const schema3 = pipe(
      string(),
      transform((): RecurType[] => [])
    );
    // @ts-expect-error
    safeParseAsync(Recur, {});
    // @ts-expect-error
    safeParseAsync(schema1, {});
    // @ts-expect-error
    safeParseAsync(schema2, {});
    // @ts-expect-error
    safeParseAsync(schema3, {});
  });

  test('should accept generic schemas', () => {
    function parse<TSchema extends GenericSchemaAsync>(schema: TSchema) {
      return safeParseAsync(schema, {});
    }
    expectTypeOf(parse).toBeFunction();
  });
});

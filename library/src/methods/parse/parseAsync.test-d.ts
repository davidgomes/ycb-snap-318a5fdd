import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  object,
  objectAsync,
  string,
} from '../../schemas/index.ts';
import type { GenericSchemaAsync, InferOutput } from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursiveAsync, type RecurType } from '../recursive/index.ts';
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
    const schema = recursiveAsync(objectAsync({ children: arrayAsync(Recur) }));
    expectTypeOf(parseAsync(schema, {})).toEqualTypeOf<
      Promise<InferOutput<typeof schema>>
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
    parseAsync(Recur, {});
    // @ts-expect-error
    parseAsync(schema1, {});
    // @ts-expect-error
    parseAsync(schema2, {});
    // @ts-expect-error
    parseAsync(schema3, {});
  });

  test('should accept generic schemas', () => {
    function parse<TSchema extends GenericSchemaAsync>(schema: TSchema) {
      return parseAsync(schema, {});
    }
    expectTypeOf(parse).toBeFunction();
  });
});

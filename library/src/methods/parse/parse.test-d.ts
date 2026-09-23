import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import { array, object, string } from '../../schemas/index.ts';
import type {
  BaseIssue,
  BaseSchema,
  GenericSchema,
  InferOutput,
} from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, recursive, type RecurType } from '../recursive/index.ts';
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
    const schema = recursive(object({ children: array(Recur) }));
    expectTypeOf(parse(schema, {})).toEqualTypeOf<InferOutput<typeof schema>>();
  });

  test('should reject unresolved Recur placeholders', () => {
    const schema1 = object({ children: array(Recur) });
    const schema2 = pipe(
      array(Recur),
      transform(() => 1)
    );
    const schema3 = pipe(
      string(),
      transform((): RecurType[] => [])
    );
    // @ts-expect-error
    parse(Recur, {});
    // @ts-expect-error
    parse(schema1, {});
    // @ts-expect-error
    parse(schema2, {});
    // @ts-expect-error
    parse(schema3, {});
  });

  test('should accept generic schemas', () => {
    function parse1<TSchema extends GenericSchema>(schema: TSchema) {
      return parse(schema, {});
    }
    function parse2<
      TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
    >(schema: TSchema): InferOutput<TSchema> {
      return parse(schema, {});
    }
    expectTypeOf(parse1(string())).toEqualTypeOf<string>();
    expectTypeOf(parse2(string())).toEqualTypeOf<string>();
  });
});

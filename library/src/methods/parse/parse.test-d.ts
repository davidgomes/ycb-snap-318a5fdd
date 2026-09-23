import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  object,
  type ObjectSchema,
  string,
  union,
} from '../../schemas/index.ts';
import type {
  GenericSchema,
  InferOutput,
  ObjectEntries,
} from '../../types/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { Recur, type RecurMarker, recursive } from '../recursive/index.ts';
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
    const schema = recursive(object({ key: string(), children: array(Recur) }));
    interface Output {
      key: string;
      children: Output[];
    }
    expectTypeOf(parse(schema, {})).toEqualTypeOf<Output>();
  });

  test('should reject unresolved Recur placeholders', () => {
    const inputOnly = pipe(
      array(Recur),
      transform((input) => input.length)
    );
    const outputOnly = pipe(
      string(),
      transform(() => ({}) as RecurMarker)
    );
    // @ts-expect-error
    parse(Recur, {});
    // @ts-expect-error
    parse(object({ children: array(Recur) }), {});
    // @ts-expect-error
    parse(inputOnly, {});
    // @ts-expect-error
    parse(outputOnly, {});
    // @ts-expect-error
    parse(union([string(), Recur]), {});
    // @ts-expect-error
    parse<typeof Recur>(Recur, {});
  });

  test('should accept schemas with generic types', () => {
    function parseGeneric<TSchema extends GenericSchema>(
      schema: TSchema
    ): InferOutput<TSchema> {
      return parse(schema, {});
    }
    function parseEntries<TEntries extends ObjectEntries>(
      entries: TEntries
    ): InferOutput<ObjectSchema<TEntries, undefined>> {
      return parse(object(entries), {});
    }
    expectTypeOf(parseGeneric(string())).toEqualTypeOf<string>();
    expectTypeOf(parseEntries({ key: string() })).toEqualTypeOf<{
      key: string;
    }>();
  });
});

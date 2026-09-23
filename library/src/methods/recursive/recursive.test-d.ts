import { describe, expectTypeOf, test } from 'vitest';
import { brand, transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  map,
  object,
  optional,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { parse, parseAsync } from '../parse/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { safeParse, safeParseAsync } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';
import { recursiveAsync } from './recursiveAsync.ts';

describe('recursive', () => {
  const schema = recursive(
    pipe(
      intersect([
        object({
          value: pipe(
            string(),
            transform((input) => input.length)
          ),
        }),
        object({
          child: optional(Recur),
          items: array(Recur),
          kids: record(string(), Recur),
          lookup: map(string(), Recur),
          group: set(Recur),
        }),
      ]),
      transform((input) => ({
        len: input.value,
        child: input.child,
        items: input.items,
        kids: input.kids,
        lookup: input.lookup,
        group: input.group,
      }))
    )
  );

  test('should keep self-references in the input', () => {
    type Input = InferInput<typeof schema>;
    expectTypeOf<NonNullable<Input['child']>>().toEqualTypeOf<Input>();
    expectTypeOf<Input['items'][number]>().toEqualTypeOf<Input>();
    expectTypeOf<Input['kids'][string]>().toEqualTypeOf<Input>();
    expectTypeOf<Input['value']>().toEqualTypeOf<string>();
  });

  test('should keep transformed self-references in the output', () => {
    type Output = InferOutput<typeof schema>;
    expectTypeOf<NonNullable<Output['child']>>().toEqualTypeOf<Output>();
    expectTypeOf<Output['items'][number]>().toEqualTypeOf<Output>();
    expectTypeOf<Output['kids'][string]>().toEqualTypeOf<Output>();
    expectTypeOf<
      Output['lookup'] extends Map<string, infer TValue> ? TValue : never
    >().toEqualTypeOf<Output>();
    expectTypeOf<
      Output['group'] extends Set<infer TValue> ? TValue : never
    >().toEqualTypeOf<Output>();
    expectTypeOf<Output['len']>().toEqualTypeOf<number>();
  });

  test('should reject unresolved placeholders', () => {
    const unresolved = object({ child: optional(Recur) });
    const outputOnly = pipe(
      string(),
      transform(() => ({ child: Recur }))
    );
    // @ts-expect-error
    parse(unresolved, {});
    // @ts-expect-error
    safeParse(unresolved, {});
    // @ts-expect-error
    parseAsync(outputOnly, '');
    // @ts-expect-error
    safeParseAsync(outputOnly, '');
    const branded = pipe(string(), brand('id'));
    expectTypeOf(parse(branded, 'a')).toEqualTypeOf<
      InferOutput<typeof branded>
    >();
  });

  test('should accept a resolved schema', () => {
    expectTypeOf(parse(schema, { value: 'a', items: [] })).toEqualTypeOf<
      InferOutput<typeof schema>
    >();
    expectTypeOf(
      parseAsync(recursiveAsync(schema), { value: 'a', items: [] })
    ).toEqualTypeOf<Promise<InferOutput<typeof schema>>>();
  });
});

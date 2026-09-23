/* eslint-disable @typescript-eslint/no-unused-vars -- values are inspected with typeof */
import { describe, expectTypeOf, test } from 'vitest';
import { readonly, transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  map,
  nullable,
  number,
  object,
  optional,
  record,
  set,
  string,
  unknown,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { parse } from '../parse/index.ts';
import { parseAsync } from '../parse/parseAsync.ts';
import { pipe } from '../pipe/index.ts';
import { safeParse } from '../safeParse/index.ts';
import { safeParseAsync } from '../safeParse/safeParseAsync.ts';
import { Recur } from './recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';

describe('recursive', () => {
  test('should return recursive schema', () => {
    const wrapped = object({ child: nullable(Recur) });
    expectTypeOf(recursive(wrapped)).toEqualTypeOf<
      RecursiveSchema<typeof wrapped>
    >();
  });

  describe('should infer self-referential types', () => {
    test('for nullable child', () => {
      const schema = recursive(
        object({
          value: string(),
          child: nullable(Recur),
        })
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input>().toEqualTypeOf<Output>();
      expectTypeOf<Input['value']>().toEqualTypeOf<string>();
      expectTypeOf<Input['child']>().toEqualTypeOf<Input | null>();
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | null>();
    });

    test('for array values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: array(Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['children']>().toEqualTypeOf<Output[]>();
    });

    test('for record values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: record(string(), Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['children']>().toEqualTypeOf<
        Record<string, Output>
      >();
    });

    test('for map values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: map(string(), Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['children']>().toEqualTypeOf<Map<string, Output>>();
    });

    test('for set values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: set(Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['children']>().toEqualTypeOf<Set<Output>>();
    });

    test('for readonly map and set values', () => {
      const schema = recursive(
        object({
          values: pipe(map(string(), Recur), readonly()),
          items: pipe(set(Recur), readonly()),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['values']>().toEqualTypeOf<
        ReadonlyMap<string, Output>
      >();
      expectTypeOf<Output['items']>().toEqualTypeOf<ReadonlySet<Output>>();
    });

    test('for optional child', () => {
      const schema = recursive(
        object({
          value: string(),
          child: optional(Recur),
        })
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | undefined>();
    });

    test('for pipe transformation', () => {
      const schema = recursive(
        pipe(
          object({
            value: string(),
            child: nullable(Recur),
          }),
          transform((input) => ({
            length: input.value.length,
            child: input.child,
          }))
        )
      );
      type Input = InferInput<typeof schema>;
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Input['value']>().toEqualTypeOf<string>();
      expectTypeOf<Input['child']>().toEqualTypeOf<Input | null>();
      expectTypeOf<Output['length']>().toEqualTypeOf<number>();
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | null>();
    });

    test('for intersect', () => {
      const schema = recursive(
        intersect([
          object({
            value: string(),
            child: nullable(Recur),
          }),
          object({ id: number() }),
        ])
      );
      type Output = InferOutput<typeof schema>;
      expectTypeOf<Output['value']>().toEqualTypeOf<string>();
      expectTypeOf<Output['id']>().toEqualTypeOf<number>();
      expectTypeOf<Output['child']>().toEqualTypeOf<Output | null>();
    });
  });

  describe('should reject unresolved Recur', () => {
    test('in input and output', () => {
      const schema = object({ child: nullable(Recur) });
      // @ts-expect-error
      parse(schema, { child: null });
      // @ts-expect-error
      safeParse(schema, { child: null });
      // @ts-expect-error
      parseAsync(schema, { child: null });
      // @ts-expect-error
      safeParseAsync(schema, { child: null });
    });

    test('in input only', () => {
      const schema = pipe(
        object({ child: Recur }),
        transform(() => 'ok' as const)
      );
      // @ts-expect-error
      parse(schema, { child: null });
      // @ts-expect-error
      safeParse(schema, { child: null });
    });

    test('in output only', () => {
      const schema = pipe(
        unknown(),
        transform((input): { child: InferOutput<typeof Recur> } => ({
          child: input as never,
        }))
      );
      // @ts-expect-error
      parse(schema, null);
      // @ts-expect-error
      safeParse(schema, null);
      // @ts-expect-error
      parseAsync(schema, null);
      // @ts-expect-error
      safeParseAsync(schema, null);
    });
  });

  test('should accept resolved schema', () => {
    const schema = recursive(
      object({
        value: string(),
        child: nullable(Recur),
      })
    );
    expectTypeOf(parse(schema, { value: 'a', child: null })).toEqualTypeOf<
      InferOutput<typeof schema>
    >();
    expectTypeOf(safeParse(schema, { value: 'a', child: null })).toEqualTypeOf<
      ReturnType<typeof safeParse<typeof schema>>
    >();
  });
});

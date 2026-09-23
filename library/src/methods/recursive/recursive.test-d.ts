import { describe, expectTypeOf, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  intersect,
  map,
  number,
  object,
  objectAsync,
  optional,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { parse, parseAsync } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { safeParse, safeParseAsync } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';
import { recursiveAsync } from './recursiveAsync.ts';

interface Tree {
  value: number;
  children: Tree[];
}

interface Containers {
  r?: { [key: string]: Containers } | undefined;
  m?: Map<string, Containers> | undefined;
  s?: Set<Containers> | undefined;
}

describe('recursive', () => {
  test('should infer self-referencing types', () => {
    const Schema = recursive(
      object({ value: number(), children: array(Recur) })
    );
    expectTypeOf<InferInput<typeof Schema>>().toEqualTypeOf<Tree>();
    expectTypeOf<InferOutput<typeof Schema>>().toEqualTypeOf<Tree>();
    expectTypeOf(parse(Schema, null)).toEqualTypeOf<Tree>();
    expectTypeOf(
      parse(Schema, null).children[0].children[0].value
    ).toEqualTypeOf<number>();
  });

  test('should infer container types', () => {
    const Schema = recursive(
      object({
        r: optional(record(string(), Recur)),
        m: optional(map(string(), Recur)),
        s: optional(set(Recur)),
      })
    );
    expectTypeOf<InferOutput<typeof Schema>>().toEqualTypeOf<Containers>();
    expectTypeOf(parse(Schema, null)).toEqualTypeOf<Containers>();
  });

  test('should preserve transformed input and output', () => {
    const Schema = recursive(
      pipe(
        intersect([object({ a: string() }), object({ next: optional(Recur) })]),
        transform((input) => ({ ...input, n: input.a.length }))
      )
    );
    type Input = InferInput<typeof Schema>;
    type Output = InferOutput<typeof Schema>;
    expectTypeOf(parse(Schema, null)).toEqualTypeOf<Output>();
    expectTypeOf<Input['a']>().toEqualTypeOf<string>();
    expectTypeOf<NonNullable<Input['next']>['a']>().toEqualTypeOf<string>();
    expectTypeOf<Output['n']>().toEqualTypeOf<number>();
    expectTypeOf<
      NonNullable<NonNullable<Output['next']>['next']>['n']
    >().toEqualTypeOf<number>();
  });

  test('should reject unresolved Recur', () => {
    const Unresolved = object({ children: array(Recur) });
    // @ts-expect-error
    parse(Unresolved, null);
    // @ts-expect-error
    safeParse(Unresolved, null);
    // @ts-expect-error
    parseAsync(Unresolved, null);
    // @ts-expect-error
    safeParseAsync(Unresolved, null);
    const OutputOnly = pipe(
      string(),
      transform(() => ({ next: Recur['~types']!.output }))
    );
    // @ts-expect-error
    parse(OutputOnly, null);
    parse(string(), null);
  });
});

describe('recursiveAsync', () => {
  test('should infer self-referencing types', () => {
    const Schema = recursiveAsync(
      objectAsync({ value: number(), children: arrayAsync(Recur) })
    );
    expectTypeOf<InferOutput<typeof Schema>>().toEqualTypeOf<Tree>();
    expectTypeOf(parseAsync(Schema, null)).toEqualTypeOf<Promise<Tree>>();
    expectTypeOf(safeParseAsync(Schema, null)).resolves.toHaveProperty(
      'output'
    );
  });
});

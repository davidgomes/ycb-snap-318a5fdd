import { describe, expectTypeOf, test } from 'vitest';
import type { Brand } from '../../actions/index.ts';
import { brand, readonly, transform } from '../../actions/index.ts';
import {
  array,
  type ArrayIssue,
  intersect,
  type IntersectIssue,
  literal,
  type LiteralIssue,
  map,
  nullable,
  number,
  type NumberIssue,
  object,
  type ObjectIssue,
  optional,
  record,
  type RecordIssue,
  set,
  type SetIssue,
  string,
  type StringIssue,
  tuple,
  union,
  type UnionIssue,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { safeParse, type SafeParseResult } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';

describe('recursive', () => {
  test('should infer object recursion on input and output', () => {
    const schema = recursive(
      object({
        value: string(),
        children: array(Recur),
      })
    );
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<{
      value: string;
      children: Input[];
    }>();
    expectTypeOf<Output>().toEqualTypeOf<{
      value: string;
      children: Output[];
    }>();
    expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
      ObjectIssue | ArrayIssue | StringIssue
    >();
    expectTypeOf(
      parse(schema, { value: 'a', children: [] })
    ).toEqualTypeOf<Output>();
  });

  test('should infer array, record, map, and set value recursion', () => {
    const arraySchema = recursive(array(Recur));
    expectTypeOf(arraySchema.type).toEqualTypeOf<'recursive'>();
    type ArrayInput = InferInput<typeof arraySchema>;
    expectTypeOf<ArrayInput>().toEqualTypeOf<ArrayInput[]>();

    const recordSchema = recursive(
      record(string(), object({ id: string(), child: optional(Recur) }))
    );
    expectTypeOf(recordSchema.type).toEqualTypeOf<'recursive'>();
    type RecordInput = InferInput<typeof recordSchema>;
    expectTypeOf<RecordInput>().toEqualTypeOf<
      Record<string, { id: string; child?: RecordInput | undefined }>
    >();

    const mapSchema = recursive(map(number(), Recur));
    expectTypeOf(mapSchema.type).toEqualTypeOf<'recursive'>();
    type MapInput = InferInput<typeof mapSchema>;
    expectTypeOf<MapInput>().toEqualTypeOf<Map<number, MapInput>>();
    expectTypeOf<InferOutput<typeof mapSchema>>().toEqualTypeOf<
      Map<number, InferOutput<typeof mapSchema>>
    >();

    const setSchema = recursive(set(Recur));
    expectTypeOf(setSchema.type).toEqualTypeOf<'recursive'>();
    type SetInput = InferInput<typeof setSchema>;
    expectTypeOf<SetInput>().toEqualTypeOf<Set<SetInput>>();
  });

  test('should preserve transformed input and output', () => {
    const schema = recursive(
      pipe(
        object({
          name: pipe(string(), brand('name')),
          nodes: array(Recur),
        }),
        transform((input) => ({ ...input, count: input.nodes.length })),
        readonly()
      )
    );
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<{
      name: string;
      nodes: Input[];
    }>();
    expectTypeOf<Output>().toEqualTypeOf<
      Readonly<{
        name: string & Brand<'name'>;
        nodes: Output[];
        count: number;
      }>
    >();
    expectTypeOf(
      parse(schema, { name: 'root', nodes: [] })
    ).toEqualTypeOf<Output>();
    expectTypeOf(safeParse(schema, { name: 'root', nodes: [] })).toEqualTypeOf<
      SafeParseResult<typeof schema>
    >();
  });

  test('should infer intersect and union recursion', () => {
    const intersectSchema = recursive(
      intersect([
        object({ name: string() }),
        object({ child: nullable(Recur) }),
      ])
    );
    expectTypeOf(intersectSchema.type).toEqualTypeOf<'recursive'>();
    type IntersectInput = InferInput<typeof intersectSchema>;
    expectTypeOf<IntersectInput>().toEqualTypeOf<{
      name: string;
      child: IntersectInput | null;
    }>();
    expectTypeOf<InferIssue<typeof intersectSchema>>().toEqualTypeOf<
      IntersectIssue | ObjectIssue | StringIssue
    >();

    const unionSchema = recursive(
      union([
        object({ type: literal('leaf'), value: number() }),
        object({ type: literal('branch'), children: array(Recur) }),
      ])
    );
    expectTypeOf(unionSchema.type).toEqualTypeOf<'recursive'>();
    type UnionInput = InferInput<typeof unionSchema>;
    expectTypeOf<UnionInput>().toEqualTypeOf<
      | { type: 'leaf'; value: number }
      | { type: 'branch'; children: UnionInput[] }
    >();
    expectTypeOf<InferIssue<typeof unionSchema>>().toEqualTypeOf<
      | UnionIssue<ObjectIssue | LiteralIssue | NumberIssue | ArrayIssue>
      | ObjectIssue
      | LiteralIssue
      | NumberIssue
      | ArrayIssue
    >();
  });

  test('should infer tuple recursion', () => {
    const schema = recursive(tuple([string(), optional(Recur)]));
    expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
    type Input = InferInput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<(string | Input | undefined)[]>();
  });

  test('should reject unresolved Recur in parse and safeParse', () => {
    const unresolved = object({ child: array(Recur) });
    // @ts-expect-error
    parse(unresolved, { child: [] });
    // @ts-expect-error
    safeParse(unresolved, { child: [] });

    const inputOnly = pipe(
      object({ child: Recur }),
      transform(() => 'ok' as const)
    );
    // @ts-expect-error
    parse(inputOnly, { child: {} });
    // @ts-expect-error
    safeParse(inputOnly, { child: {} });

    const outputOnly = pipe(
      string(),
      transform(
        (): { child: InferOutput<typeof Recur> } =>
          ({ child: undefined }) as never
      )
    );
    // @ts-expect-error
    parse(outputOnly, 'value');
    // @ts-expect-error
    safeParse(outputOnly, 'value');

    expectTypeOf(parse(string(), 'ok')).toEqualTypeOf<string>();
    expectTypeOf(parse(record(string(), number()), { a: 1 })).toEqualTypeOf<
      Record<string, number>
    >();
  });

  test('should infer issues of container recursion', () => {
    const schema = recursive(set(record(string(), Recur)));
    expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
    expectTypeOf<InferIssue<typeof schema>>().toEqualTypeOf<
      SetIssue | RecordIssue | StringIssue
    >();
  });
});

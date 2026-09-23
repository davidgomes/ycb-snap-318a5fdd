import type { Pattern, MatchedValue } from './Pattern';
import type { InvertPatternForExclude, InvertPattern } from './InvertPattern';
import type { DeepExclude } from './DeepExclude';
import type { Union, GuardValue, IsNever } from './helpers';
import type { FindSelected } from './FindSelected';
import type { PickReturnValue } from './Match';

interface NonExhaustiveError<i> {
  __nonExhaustive: never;
}

interface TSPatternError<i> {
  __nonExhaustive: never;
}

type DeepExcludeAll<a, tupleList extends any[]> = [a] extends [never]
  ? never
  : tupleList extends [infer excluded, ...infer tail]
  ? DeepExcludeAll<DeepExclude<a, excluded>, tail>
  : a;

type MakeTuples<ps extends readonly any[], value> = {
  -readonly [index in keyof ps]: InvertPatternForExclude<ps[index], value>;
};

type IsExhaustive<remaining, handledCases extends any[], yes> = [
  DeepExcludeAll<remaining, handledCases>
] extends [never]
  ? yes
  : NonExhaustiveError<DeepExcludeAll<remaining, handledCases>>;

/**
 * #### MatchEach
 * Like `Match`, but every clause is evaluated and all matching handlers'
 * results are collected into an array.
 *
 * - `i` is the input type patterns are checked against.
 * - `remaining` tracks cases not yet handled, for exhaustiveness checking.
 */
export type MatchEach<
  i,
  o,
  handledCases extends any[] = [],
  inferredOutput = never,
  remaining = i
> = {
  with<
    const p extends Pattern<i>,
    c,
    value extends MatchedValue<i, InvertPattern<p, i>>
  >(
    pattern: IsNever<p> extends true ? Pattern<i> : p,
    handler: (
      selections: FindSelected<value, p>,
      value: value
    ) => PickReturnValue<o, c>
  ): InvertPatternForExclude<p, value> extends infer excluded
    ? MatchEach<
        i,
        o,
        [...handledCases, excluded],
        Union<inferredOutput, c>,
        Exclude<remaining, excluded>
      >
    : never;

  with<
    const p1 extends Pattern<i>,
    const p2 extends Pattern<i>,
    c,
    p extends p1 | p2,
    value extends p extends any ? MatchedValue<i, InvertPattern<p, i>> : never
  >(
    p1: p1,
    p2: p2,
    handler: (value: value) => PickReturnValue<o, c>
  ): [
    InvertPatternForExclude<p1, value>,
    InvertPatternForExclude<p2, value>
  ] extends [infer excluded1, infer excluded2]
    ? MatchEach<
        i,
        o,
        [...handledCases, excluded1, excluded2],
        Union<inferredOutput, c>,
        Exclude<remaining, excluded1 | excluded2>
      >
    : never;

  with<
    const p1 extends Pattern<i>,
    const p2 extends Pattern<i>,
    const p3 extends Pattern<i>,
    const ps extends readonly Pattern<i>[],
    c,
    p extends p1 | p2 | p3 | ps[number],
    value extends MatchedValue<i, InvertPattern<p, i>>
  >(
    ...args: [
      p1: p1,
      p2: p2,
      p3: p3,
      ...patterns: ps,
      handler: (value: value) => PickReturnValue<o, c>
    ]
  ): [
    InvertPatternForExclude<p1, value>,
    InvertPatternForExclude<p2, value>,
    InvertPatternForExclude<p3, value>,
    MakeTuples<ps, value>
  ] extends [
    infer excluded1,
    infer excluded2,
    infer excluded3,
    infer excludedRest
  ]
    ? MatchEach<
        i,
        o,
        [
          ...handledCases,
          excluded1,
          excluded2,
          excluded3,
          ...Extract<excludedRest, any[]>
        ],
        Union<inferredOutput, c>,
        Exclude<
          remaining,
          | excluded1
          | excluded2
          | excluded3
          | Extract<excludedRest, any[]>[number]
        >
      >
    : never;

  with<
    const pat extends Pattern<i>,
    pred extends (value: MatchedValue<i, InvertPattern<pat, i>>) => unknown,
    c,
    value extends GuardValue<pred>
  >(
    pattern: pat,
    predicate: pred,
    handler: (
      selections: FindSelected<value, pat>,
      value: value
    ) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        i,
        o,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        Exclude<remaining, narrowed>
      >
    : MatchEach<i, o, handledCases, Union<inferredOutput, c>, remaining>;

  when<pred extends (value: i) => unknown, c, value extends GuardValue<pred>>(
    predicate: pred,
    handler: (value: value) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        i,
        o,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        Exclude<remaining, narrowed>
      >
    : MatchEach<i, o, handledCases, Union<inferredOutput, c>, remaining>;

  /**
   * `.tap(callback)` calls `callback` once for each result collected so far
   * (in declaration order) when the expression is evaluated.
   */
  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => void
  ): MatchEach<i, o, handledCases, inferredOutput, remaining>;

  /**
   * Returns every matching result, or `[handler(value)]` if nothing matched.
   */
  otherwise<c>(
    handler: (value: i) => PickReturnValue<o, c>
  ): PickReturnValue<o, Union<inferredOutput, c>>[];

  exhaustive: IsExhaustive<
    remaining,
    handledCases,
    ExhaustiveEach<o, inferredOutput>
  >;

  /**
   * ⚠️ throws a `NonExhaustiveError` if no pattern matches.
   */
  run(): PickReturnValue<o, inferredOutput>[];

  toFunction(): (input: i) => PickReturnValue<o, inferredOutput>[];

  toExhaustiveFunction: IsExhaustive<
    remaining,
    handledCases,
    () => (input: i) => PickReturnValue<o, inferredOutput>[]
  >;

  toPartialFunction(): (
    input: i
  ) => PickReturnValue<o, inferredOutput>[] | undefined;

  returnType: [inferredOutput] extends [never]
    ? <output>() => MatchEach<i, output, handledCases, never, remaining>
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  narrow(): DeepExcludeAll<remaining, handledCases> extends infer narrowed
    ? MatchEach<narrowed, o, [], inferredOutput, narrowed>
    : never;
};

type ExhaustiveEach<output, inferredOutput> = {
  (): PickReturnValue<output, inferredOutput>[];
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): PickReturnValue<output, Union<inferredOutput, otherOutput>>[];
};
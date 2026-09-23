import type * as symbols from '../internals/symbols';
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

type MatchEachOutput<output, inferredOutput> = PickReturnValue<
  output,
  inferredOutput
>[];

/**
 * `matchEach` evaluates every clause and returns an array of handler results.
 *
 * `i` is the input type patterns are checked against. It stays unchanged by
 * `.with()` / `.when()` so every clause sees the original input (all clauses
 * run). `remaining` is the exhaustiveness tracker: it is narrowed as cases
 * are handled, and `.narrow()` copies it back into `i`.
 * `root` is the input type accepted by compiled functions.
 */
export type MatchEach<
  i,
  o,
  remaining = i,
  handledCases extends any[] = [],
  inferredOutput = never,
  root = i
> = {
  /**
   * `.with(pattern, handler)` registers a pattern and a handler. Unlike
   * `match`, every matching handler runs, so patterns are checked against
   * the original input type rather than the remaining unhandled cases.
   */
  with<
    const p extends Pattern<i>,
    c,
    value extends MatchedValue<i, InvertPattern<p, i>>
  >(
    pattern: IsNever<p> extends true
      ? /**
         * HACK: Using `IsNever<p>` here is a hack to
         * make sure the type checker forwards
         * the input type parameter to pattern
         * creator functions like `P.when`, `P.not`
         * `P.union` when they are passed to `.with`
         * directly.
         */
        Pattern<i>
      : p,
    handler: (
      selections: FindSelected<value, p>,
      value: value
    ) => PickReturnValue<o, c>
  ): InvertPatternForExclude<p, value> extends infer excluded
    ? MatchEach<
        i,
        o,
        Exclude<remaining, excluded>,
        [...handledCases, excluded],
        Union<inferredOutput, c>,
        root
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
        Exclude<remaining, excluded1 | excluded2>,
        [...handledCases, excluded1, excluded2],
        Union<inferredOutput, c>,
        root
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
        Exclude<
          remaining,
          | excluded1
          | excluded2
          | excluded3
          | Extract<excludedRest, any[]>[number]
        >,
        [
          ...handledCases,
          excluded1,
          excluded2,
          excluded3,
          ...Extract<excludedRest, any[]>
        ],
        Union<inferredOutput, c>,
        root
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
        Exclude<remaining, narrowed>,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        root
      >
    : MatchEach<i, o, remaining, handledCases, Union<inferredOutput, c>, root>;

  /**
   * `.when(predicate, handler)` registers a predicate and a handler.
   * The handler runs when the predicate returns a truthy value, in addition
   * to any other matching clauses.
   */
  when<pred extends (value: i) => unknown, c, value extends GuardValue<pred>>(
    predicate: pred,
    handler: (value: value) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        i,
        o,
        Exclude<remaining, narrowed>,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        root
      >
    : MatchEach<i, o, remaining, handledCases, Union<inferredOutput, c>, root>;

  /**
   * `.otherwise(handler)` returns `[handler(value)]` when no clause matched.
   * When at least one clause matched, the handler is not called and the
   * array of matching results is returned instead.
   */
  otherwise<c>(
    handler: (value: i) => PickReturnValue<o, c>
  ): MatchEachOutput<o, Union<inferredOutput, c>>;

  /**
   * `.exhaustive()` returns every matching handler result.
   * It is a type error if some input cases are not handled.
   * Throws `NonExhaustiveError` at runtime when nothing matches, unless a
   * fallback handler is provided — then the fallback result is returned as
   * a single-element array.
   */
  exhaustive: DeepExcludeAll<
    remaining,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? ExhaustiveEach<o, inferredOutput>
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * `.run()` returns every matching handler result.
   *
   * Throws `NonExhaustiveError` if no pattern matches.
   */
  run(): MatchEachOutput<o, inferredOutput>;

  /**
   * `.returnType<T>()` sets the return type of every branch.
   * Only allowed directly after `matchEach(...)`.
   */
  returnType: [inferredOutput] extends [never]
    ? <output>() => MatchEach<i, output, remaining, handledCases, never, root>
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  /**
   * `.narrow()` excludes handled cases from both the exhaustiveness tracker
   * and the input type used by subsequent patterns.
   */
  narrow(): DeepExcludeAll<remaining, handledCases> extends infer narrowed
    ? MatchEach<narrowed, o, narrowed, [], inferredOutput, root>
    : never;

  /**
   * `.tap(callback)` runs `callback` once per result collected so far, in
   * declaration order, when the expression is evaluated. The results array
   * is unchanged.
   */
  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => unknown
  ): MatchEach<i, o, remaining, handledCases, inferredOutput, root>;

  /**
   * Compiles the clauses into `(input) => output[]`.
   * Throws `NonExhaustiveError` if no pattern matches.
   */
  toFunction(): (input: root) => MatchEachOutput<o, inferredOutput>;

  /**
   * Compiles the clauses into `(input) => output[]`, and rejects the build
   * at compile time when the clauses are not exhaustive.
   * Throws `NonExhaustiveError` if no pattern matches.
   */
  toExhaustiveFunction: DeepExcludeAll<
    remaining,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? () => (input: root) => MatchEachOutput<o, inferredOutput>
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * Compiles the clauses into `(input) => output[] | undefined`.
   * Returns `undefined` when no pattern matches. Never throws because nothing matched.
   */
  toPartialFunction(): (
    input: root
  ) => MatchEachOutput<o, inferredOutput> | undefined;
};

type DeepExcludeAll<a, tupleList extends any[]> = [a] extends [never]
  ? never
  : tupleList extends [infer excluded, ...infer tail]
  ? DeepExcludeAll<DeepExclude<a, excluded>, tail>
  : a;

type MakeTuples<ps extends readonly any[], value> = {
  -readonly [index in keyof ps]: InvertPatternForExclude<ps[index], value>;
};

type ExhaustiveEach<output, inferredOutput> = {
  (): MatchEachOutput<output, inferredOutput>;
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): MatchEachOutput<output, Union<inferredOutput, otherOutput>>;
};

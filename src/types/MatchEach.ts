import type { Pattern, MatchedValue } from './Pattern';
import type { InvertPatternForExclude, InvertPattern } from './InvertPattern';
import type { Union, GuardValue, IsNever } from './helpers';
import type { FindSelected } from './FindSelected';
import type { PickReturnValue, DeepExcludeAll, MakeTuples } from './Match';

interface NonExhaustiveError<i> {
  __nonExhaustive: never;
}

interface TSPatternError<i> {
  __nonExhaustive: never;
}

/**
 * #### MatchEach
 * An interface to create a pattern matching expression that evaluates
 * every clause and collects the results of all matching handlers.
 *
 * Type parameters:
 * - `i`: the type patterns passed to `.with()` are checked against.
 * - `o`: the explicit output type set with `.returnType<T>()`.
 * - `handledCases`: the list of excluded types, used to check exhaustiveness.
 * - `inferredOutput`: the union of all handler return types.
 * - `remaining`: the input type minus top-level handled cases.
 * - `input`: the type accepted by compiled functions.
 */
export type MatchEach<
  i,
  o,
  handledCases extends any[] = [],
  inferredOutput = never,
  remaining = i,
  input = i
> = {
  /**
   * `.with(pattern, handler)` Registers a pattern and an handler function that
   * will be called if the pattern matches the input value.
   *
   * Unlike `match`, the pattern is always checked against the full input type,
   * since every clause is evaluated.
   **/
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
        Exclude<remaining, excluded>,
        input
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
        Exclude<remaining, excluded1 | excluded2>,
        input
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
        >,
        input
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
        Exclude<remaining, narrowed>,
        input
      >
    : MatchEach<i, o, handledCases, Union<inferredOutput, c>, remaining, input>;

  /**
   * `.when(predicate, handler)` Registers a predicate function and an handler function.
   * If the predicate returns true, the handler function will be called.
   **/
  when<pred extends (value: i) => unknown, c, value extends GuardValue<pred>>(
    predicate: pred,
    handler: (value: value) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        i,
        o,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        Exclude<remaining, narrowed>,
        input
      >
    : MatchEach<i, o, handledCases, Union<inferredOutput, c>, remaining, input>;

  /**
   * `.tap(callback)` Registers a side-effect callback. When the expression
   * is evaluated, it is called once for each result collected by the
   * clauses declared before it. It doesn't change the results.
   **/
  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => void
  ): MatchEach<i, o, handledCases, inferredOutput, remaining, input>;

  /**
   * `.otherwise(handler)` returns the results of all matching handlers,
   * or `[handler(value)]` if no pattern matched. Never throws.
   **/
  otherwise<c>(
    handler: (value: i) => PickReturnValue<o, c>
  ): PickReturnValue<o, Union<inferredOutput, c>>[];

  /**
   * `.exhaustive()` checks that all cases are handled, and returns
   * the results of all matching handlers.
   *
   * Throws a `NonExhaustiveError` if no pattern matched at runtime, unless
   * a fallback handler is provided.
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
   * `.run()` returns the results of all matching handlers.
   *
   * ⚠️ calling this function is unsafe, and throws if no pattern matches your input.
   */
  run(): PickReturnValue<o, inferredOutput>[];

  /**
   * `.returnType<T>()` Lets you specify the return type for all of your branches.
   */
  returnType: [inferredOutput] extends [never]
    ? <output>() => MatchEach<i, output, handledCases, never, remaining, input>
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  /**
   * `.narrow()` narrows the input type to exclude all cases that have previously been handled.
   * Subsequent `.with()` clauses are checked against the narrowed type.
   */
  narrow(): DeepExcludeAll<remaining, handledCases> extends infer narrowed
    ? MatchEach<narrowed, o, [], inferredOutput, narrowed, input>
    : never;

  /**
   * `.toFunction()` compiles the registered clauses into a reusable function
   * returning the results of all matching handlers.
   *
   * ⚠️ The returned function throws a `NonExhaustiveError` if no pattern matches.
   */
  toFunction(): (input: input) => PickReturnValue<o, inferredOutput>[];

  /**
   * `.toExhaustiveFunction()` checks that all cases are handled, and compiles
   * the registered clauses into a reusable function returning the results
   * of all matching handlers.
   */
  toExhaustiveFunction: DeepExcludeAll<
    remaining,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? () => (input: input) => PickReturnValue<o, inferredOutput>[]
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * `.toPartialFunction()` compiles the registered clauses into a reusable
   * function returning the results of all matching handlers, or `undefined`
   * if no pattern matches.
   */
  toPartialFunction(): (
    input: input
  ) => PickReturnValue<o, inferredOutput>[] | undefined;
};

type ExhaustiveEach<output, inferredOutput> = {
  (): PickReturnValue<output, inferredOutput>[];
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): PickReturnValue<output, Union<inferredOutput, otherOutput>>[];
};

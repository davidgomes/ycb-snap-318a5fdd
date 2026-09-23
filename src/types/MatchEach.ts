import type { Pattern, MatchedValue } from './Pattern';
import type { InvertPatternForExclude, InvertPattern } from './InvertPattern';
import type { Union, GuardValue, IsNever } from './helpers';
import type { FindSelected } from './FindSelected';
import type {
  DeepExcludeAll,
  MakeTuples,
  NonExhaustiveError,
  PickReturnValue,
  TSPatternError,
} from './Match';

/**
 * #### MatchEach
 * An interface to create a pattern matching expression that evaluates
 * every clause and collects the results of all matching handlers.
 *
 * - `i` is the type patterns are checked against. It is the original
 *   input type, and only changes when calling `.narrow()`.
 * - `remaining` tracks the cases that haven't been handled yet,
 *   to check exhaustiveness.
 * - `fnInput` is the input type of compiled functions.
 */
export type MatchEach<
  i,
  o,
  handledCases extends any[] = [],
  inferredOutput = never,
  remaining = i,
  fnInput = i
> = {
  /**
   * `.with(pattern, handler)` Registers a pattern and an handler function.
   * Every registered handler whose pattern matches the input will be called,
   * and its result will be added to the output array.
   **/
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
        [...handledCases, excluded],
        Union<inferredOutput, c>,
        Exclude<remaining, excluded>,
        fnInput
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
        fnInput
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
        fnInput
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
        fnInput
      >
    : MatchEach<
        i,
        o,
        handledCases,
        Union<inferredOutput, c>,
        remaining,
        fnInput
      >;

  /**
   * `.when(predicate, handler)` Registers a predicate function and an handler function.
   * If the predicate returns true, the handler's result will be added to the output array.
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
        fnInput
      >
    : MatchEach<
        i,
        o,
        handledCases,
        Union<inferredOutput, c>,
        remaining,
        fnInput
      >;

  /**
   * `.tap(callback)` Registers a side-effect callback. When the expression
   * is evaluated, the callback is called once for each result
   * collected by the clauses declared before it.
   *
   * It doesn't change the results.
   **/
  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => void
  ): MatchEach<i, o, handledCases, inferredOutput, remaining, fnInput>;

  /**
   * `.otherwise(handler)` returns the results of all matching handlers,
   * or `[handler(value)]` if no pattern matched. Never throws.
   **/
  otherwise<c>(
    handler: (value: i) => PickReturnValue<o, c>
  ): PickReturnValue<o, Union<inferredOutput, c>>[];

  /**
   * `.exhaustive()` checks that all cases are handled, and returns the results
   * of all matching handlers.
   *
   * Throws a `NonExhaustiveError` if no pattern matched, unless a fallback
   * handler is provided.
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
    ? <output>() => MatchEach<
        i,
        output,
        handledCases,
        never,
        remaining,
        fnInput
      >
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  /**
   * `.narrow()` narrows the input type to exclude all cases that have previously been handled.
   * Subsequent patterns are checked against the narrowed input type.
   */
  narrow(): DeepExcludeAll<remaining, handledCases> extends infer narrowed
    ? MatchEach<narrowed, o, [], inferredOutput, narrowed, fnInput>
    : never;

  /**
   * `.toFunction()` compiles the registered clauses into a reusable function
   * returning the results of all matching handlers.
   *
   * ⚠️ The compiled function throws a `NonExhaustiveError` if no pattern matches its input.
   */
  toFunction(): (input: fnInput) => PickReturnValue<o, inferredOutput>[];

  /**
   * `.toExhaustiveFunction()` checks that all cases are handled, and compiles the
   * registered clauses into a reusable function returning the results of all
   * matching handlers.
   */
  toExhaustiveFunction: DeepExcludeAll<
    remaining,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? () => (input: fnInput) => PickReturnValue<o, inferredOutput>[]
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * `.toPartialFunction()` compiles the registered clauses into a reusable function
   * returning the results of all matching handlers, or `undefined` if no pattern matched.
   */
  toPartialFunction(): (
    input: fnInput
  ) => PickReturnValue<o, inferredOutput>[] | undefined;
};

type ExhaustiveEach<output, inferredOutput> = {
  (): PickReturnValue<output, inferredOutput>[];
  /**
   * The fallback function will be called if your input value doesn't match any pattern,
   * and its result will be returned in a single-element array.
   */
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): PickReturnValue<output, Union<inferredOutput, otherOutput>>[];
};

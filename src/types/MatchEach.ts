import type { Pattern, MatchedValue } from './Pattern';
import type { InvertPatternForExclude, InvertPattern } from './InvertPattern';
import type { Union, GuardValue, IsNever } from './helpers';
import type { FindSelected } from './FindSelected';
import type {
  PickReturnValue,
  DeepExcludeAll,
  MakeTuples,
  NonExhaustiveError,
  TSPatternError,
} from './Match';

/**
 * #### MatchEach
 * An interface to create a pattern matching expression that evaluates
 * every clause and collects the results of all matching handlers.
 *
 * - `i` is the type patterns are checked against. It stays the original
 *   input type, since every clause is evaluated, until `.narrow()` is called.
 * - `remaining` is the input type minus handled cases, used to check exhaustiveness.
 * - `compiledInput` is the parameter type of functions returned by `.toFunction()`
 *   and friends. It is never narrowed.
 */
export type MatchEach<
  i,
  o,
  handledCases extends any[] = [],
  inferredOutput = never,
  remaining = i,
  compiledInput = i
> = {
  /**
   * `.with(pattern, handler)` Registers a pattern and an handler function.
   * The handler's result will be added to the output array if the pattern
   * matches the input value, regardless of previous clauses.
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
        compiledInput
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
        compiledInput
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
        compiledInput
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
        compiledInput
      >
    : MatchEach<
        i,
        o,
        handledCases,
        Union<inferredOutput, c>,
        remaining,
        compiledInput
      >;

  /**
   * `.when(predicate, handler)` Registers a predicate function and an handler function.
   * The handler's result will be added to the output array if the predicate returns true.
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
        compiledInput
      >
    : MatchEach<
        i,
        o,
        handledCases,
        Union<inferredOutput, c>,
        remaining,
        compiledInput
      >;

  /**
   * `.tap(callback)` Registers a side-effect callback. When the expression is
   * evaluated, the callback is called once for each result collected by the
   * clauses declared before it. It doesn't change the output array.
   **/
  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => void
  ): MatchEach<i, o, handledCases, inferredOutput, remaining, compiledInput>;

  /**
   * `.otherwise()` takes a **default handler function**. It returns the results
   * of all matching handlers, or `[defaultHandler(value)]` if no pattern matched.
   **/
  otherwise<c>(
    handler: (value: remaining) => PickReturnValue<o, c>
  ): PickReturnValue<o, Union<inferredOutput, c>>[];

  /**
   * `.exhaustive()` checks that all cases are handled, and returns the results
   * of all matching handlers.
   *
   * Throws a `NonExhaustiveError` if no pattern matched, unless a fallback
   * handler is provided.
   */
  exhaustive: DeepExcludeAll<remaining, handledCases> extends infer remainingCases
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
        compiledInput
      >
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  /**
   * `.narrow()` narrows the input type to exclude all cases that have previously been handled.
   * Patterns of subsequent clauses are checked against the narrowed type.
   */
  narrow(): DeepExcludeAll<remaining, handledCases> extends infer narrowed
    ? MatchEach<narrowed, o, [], inferredOutput, narrowed, compiledInput>
    : never;

  /**
   * `.toFunction()` compiles the registered clauses into a reusable function
   * returning the results of all matching handlers.
   *
   * ⚠️ the returned function throws if no pattern matches its input.
   */
  toFunction(): (input: compiledInput) => PickReturnValue<o, inferredOutput>[];

  /**
   * `.toExhaustiveFunction()` checks that all cases are handled, and compiles
   * the registered clauses into a reusable function returning the results of
   * all matching handlers.
   */
  toExhaustiveFunction: DeepExcludeAll<
    remaining,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? () => (input: compiledInput) => PickReturnValue<o, inferredOutput>[]
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * `.toPartialFunction()` compiles the registered clauses into a reusable
   * function returning the results of all matching handlers, or `undefined`
   * if no pattern matches its input.
   */
  toPartialFunction(): (
    input: compiledInput
  ) => PickReturnValue<o, inferredOutput>[] | undefined;
};

type ExhaustiveEach<output, inferredOutput> = {
  /**
   * `.exhaustive()` checks that all cases are handled, and returns the results
   * of all matching handlers.
   */
  (): PickReturnValue<output, inferredOutput>[];
  /**
   * `.exhaustive(fallback)` checks that all cases are handled, and returns the
   * results of all matching handlers.
   *
   * If no pattern matches, the fallback's result is returned in a single-element array.
   */
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): PickReturnValue<output, Union<inferredOutput, otherOutput>>[];
};

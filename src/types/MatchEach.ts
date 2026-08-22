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

/**
 * #### MatchEach
 * An interface to create a pattern matching expression that evaluates all
 * registered patterns and collects every matching handler's result.
 */
export type MatchEach<
  i,
  o,
  handledCases extends any[] = [],
  inferredOutput = never
> = {
  /**
   * `.with(pattern, handler)` Registers a pattern and a handler function that
   * will be called if the pattern matches the input value. Unlike `match`, all
   * patterns are evaluated against the current input type and every matching
   * handler's result is collected.
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
    ? MatchEach<i, o, [...handledCases, excluded], Union<inferredOutput, c>>
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
        Union<inferredOutput, c>
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
        Union<inferredOutput, c>
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
    ? MatchEach<i, o, [...handledCases, narrowed], Union<inferredOutput, c>>
    : MatchEach<i, o, handledCases, Union<inferredOutput, c>>;

  /**
   * `.when(predicate, handler)` Registers a predicate function and a handler
   * function. If the predicate returns true, the handler function will be
   * called and its result collected.
   **/
  when<pred extends (value: i) => unknown, c, value extends GuardValue<pred>>(
    predicate: pred,
    handler: (value: value) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<i, o, [...handledCases, narrowed], Union<inferredOutput, c>>
    : MatchEach<i, o, handledCases, Union<inferredOutput, c>>;

  /**
   * `.tap(callback)` registers a side-effect callback and returns a new
   * `matchEach` for continued chaining. When the expression is evaluated,
   * the callback is invoked once for each result collected up to that point.
   **/
  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => void
  ): MatchEach<i, o, handledCases, inferredOutput>;

  /**
   * `.otherwise()` takes a default handler function that will be called if no
   * previous pattern matched the input. Returns a single-element array with
   * the default handler's result. When at least one pattern matched, returns
   * the array of all matching results without calling the default handler.
   **/
  otherwise<c>(
    handler: (value: i) => PickReturnValue<o, c>
  ): PickReturnValue<o, Union<inferredOutput, c>>[];

  /**
   * `.exhaustive()` checks that all cases are handled, and returns an array of
   * all matching handler results.
   **/
  exhaustive: DeepExcludeAll<i, handledCases> extends infer remainingCases
    ? [remainingCases] extends [never]
      ? ExhaustiveEach<o, inferredOutput>
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * `.run()` returns the array of all matching handler results.
   *
   * ⚠️ calling this function is unsafe, and may throw if no pattern matches.
   */
  run(): PickReturnValue<o, inferredOutput>[];

  /**
   * `.returnType<T>()` Lets you specify the return type for all of your branches.
   */
  returnType: [inferredOutput] extends [never]
    ? <output>() => MatchEach<i, output, handledCases>
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  /**
   * `.narrow()` narrows the input type to exclude all cases that have
   * previously been handled, for both pattern matching and exhaustiveness
   * tracking.
   */
  narrow(): MatchEach<
    DeepExcludeAll<i, handledCases>,
    o,
    [],
    inferredOutput
  >;

  /**
   * Compiles the registered clauses into a reusable function that returns an
   * array of all matching handler results. Throws `NonExhaustiveError` if no
   * pattern matches at runtime.
   */
  toFunction(): (value: i) => PickReturnValue<o, inferredOutput>[];

  /**
   * Compiles the registered clauses into a reusable function that returns an
   * array of all matching handler results. Enforces compile-time exhaustiveness
   * and throws `NonExhaustiveError` if no pattern matches at runtime.
   */
  toExhaustiveFunction(): DeepExcludeAll<i, handledCases> extends infer remainingCases
    ? [remainingCases] extends [never]
      ? (value: i) => PickReturnValue<o, inferredOutput>[]
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * Compiles the registered clauses into a reusable function that returns an
   * array of all matching handler results, or `undefined` when no patterns match.
   */
  toPartialFunction(): (value: i) => PickReturnValue<o, inferredOutput>[] | undefined;
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
  (): PickReturnValue<output, inferredOutput>[];
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): PickReturnValue<output, Union<inferredOutput, otherOutput>>[];
};

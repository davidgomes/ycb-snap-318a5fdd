import type { Pattern, MatchedValue } from './Pattern';
import type { InvertPatternForExclude, InvertPattern } from './InvertPattern';
import type { DeepExclude } from './DeepExclude';
import type { Union, GuardValue, IsNever } from './helpers';
import type { FindSelected } from './FindSelected';
import type { PickReturnValue } from './Match';

interface NonExhaustiveError<i> {
  __nonExhaustive: never;
}

interface MatchEachError<i> {
  __nonExhaustive: never;
}

/**
 * #### MatchEach
 * An interface to create a pattern matching clause that evaluates every
 * registered branch and collects each matching handler's result.
 */
export type MatchEach<
  input,
  output,
  handledCases extends any[] = [],
  inferredOutput = never,
  unhandled = input
> = {
  /**
   * `.with(pattern, handler)` Registers a pattern and a handler function.
   * Every `.with()` is checked against the original input type. All matching
   * handlers run, in declaration order.
   */
  with<
    const p extends Pattern<input>,
    c,
    value extends MatchedValue<input, InvertPattern<p, input>>
  >(
    pattern: IsNever<p> extends true ? Pattern<input> : p,
    handler: (
      selections: FindSelected<value, p>,
      value: value
    ) => PickReturnValue<output, c>
  ): InvertPatternForExclude<p, value> extends infer excluded
    ? MatchEach<
        input,
        output,
        [...handledCases, excluded],
        Union<inferredOutput, c>,
        Exclude<unhandled, excluded>
      >
    : never;

  with<
    const p1 extends Pattern<input>,
    const p2 extends Pattern<input>,
    c,
    p extends p1 | p2,
    value extends p extends any
      ? MatchedValue<input, InvertPattern<p, input>>
      : never
  >(
    p1: p1,
    p2: p2,
    handler: (value: value) => PickReturnValue<output, c>
  ): [
    InvertPatternForExclude<p1, value>,
    InvertPatternForExclude<p2, value>
  ] extends [infer excluded1, infer excluded2]
    ? MatchEach<
        input,
        output,
        [...handledCases, excluded1, excluded2],
        Union<inferredOutput, c>,
        Exclude<unhandled, excluded1 | excluded2>
      >
    : never;

  with<
    const p1 extends Pattern<input>,
    const p2 extends Pattern<input>,
    const p3 extends Pattern<input>,
    const ps extends readonly Pattern<input>[],
    c,
    p extends p1 | p2 | p3 | ps[number],
    value extends MatchedValue<input, InvertPattern<p, input>>
  >(
    ...args: [
      p1: p1,
      p2: p2,
      p3: p3,
      ...patterns: ps,
      handler: (value: value) => PickReturnValue<output, c>
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
        input,
        output,
        [
          ...handledCases,
          excluded1,
          excluded2,
          excluded3,
          ...Extract<excludedRest, any[]>
        ],
        Union<inferredOutput, c>,
        Exclude<
          unhandled,
          | excluded1
          | excluded2
          | excluded3
          | Extract<excludedRest, any[]>[number]
        >
      >
    : never;

  with<
    const pat extends Pattern<input>,
    pred extends (
      value: MatchedValue<input, InvertPattern<pat, input>>
    ) => unknown,
    c,
    value extends GuardValue<pred>
  >(
    pattern: pat,
    predicate: pred,
    handler: (
      selections: FindSelected<value, pat>,
      value: value
    ) => PickReturnValue<output, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        input,
        output,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        Exclude<unhandled, narrowed>
      >
    : MatchEach<
        input,
        output,
        handledCases,
        Union<inferredOutput, c>,
        unhandled
      >;

  /**
   * `.when(predicate, handler)` Registers a predicate and a handler.
   * The predicate is checked against the original input, and the handler runs
   * whenever it returns true, even if earlier clauses also matched.
   */
  when<
    pred extends (value: input) => unknown,
    c,
    value extends GuardValue<pred>
  >(
    predicate: pred,
    handler: (value: value) => PickReturnValue<output, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        input,
        output,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        Exclude<unhandled, narrowed>
      >
    : MatchEach<
        input,
        output,
        handledCases,
        Union<inferredOutput, c>,
        unhandled
      >;

  /**
   * `.tap(callback)` registers a side-effect that runs once for every result
   * collected up to this point, in declaration order. The callback's return
   * value is ignored.
   */
  tap(
    callback: (result: PickReturnValue<output, inferredOutput>) => unknown
  ): MatchEach<input, output, handledCases, inferredOutput, unhandled>;

  /**
   * `.otherwise()` takes a default handler used only when no clause matched.
   * When at least one clause matches, the default handler is not called.
   * This method never throws.
   */
  otherwise<c>(
    handler: (value: input) => PickReturnValue<output, c>
  ): PickReturnValue<output, Union<inferredOutput, c>>[];

  /**
   * `.exhaustive()` returns every matching handler result.
   * It is a type error if some input cases are not handled.
   * At runtime, a `NonExhaustiveError` is thrown when nothing matched,
   * unless a fallback handler is provided — in that case its result is
   * returned in a single-element array.
   */
  exhaustive: DeepExcludeAll<
    unhandled,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? ExhaustiveEach<output, inferredOutput>
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * `.run()` returns every matching handler result.
   *
   * Throws `NonExhaustiveError` if no pattern matches the input.
   */
  run(): PickReturnValue<output, inferredOutput>[];

  /**
   * `.returnType<T>()` sets the return type of every branch.
   * Only allowed directly after `matchEach(...)`.
   */
  returnType: [inferredOutput] extends [never]
    ? <output>() => MatchEach<input, output, handledCases, never, unhandled>
    : MatchEachError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  /**
   * `.narrow()` excludes already handled cases from both the pattern input
   * type and the internal exhaustiveness tracking type.
   */
  narrow(): MatchEach<
    DeepExcludeAll<unhandled, handledCases>,
    output,
    [],
    inferredOutput,
    DeepExcludeAll<unhandled, handledCases>
  >;

  /**
   * Compiles the clauses into `(input) => output[]`.
   * Throws `NonExhaustiveError` when no pattern matches.
   */
  toFunction(): (value: input) => PickReturnValue<output, inferredOutput>[];

  /**
   * Compiles the clauses into `(input) => output[]` and checks exhaustiveness.
   * Throws `NonExhaustiveError` when no pattern matches.
   */
  toExhaustiveFunction: DeepExcludeAll<
    unhandled,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? () => (value: input) => PickReturnValue<output, inferredOutput>[]
      : NonExhaustiveError<remainingCases>
    : never;

  /**
   * Compiles the clauses into `(input) => output[] | undefined`.
   * Returns `undefined` when no pattern matches. Never throws for that case.
   */
  toPartialFunction(): (
    value: input
  ) => PickReturnValue<output, inferredOutput>[] | undefined;
};

type DeepExcludeAll<a, tupleList extends any[]> = [a] extends [never]
  ? never
  : tupleList extends [infer excluded, ...infer tail]
  ? DeepExcludeAll<DeepExclude<a, excluded>, tail>
  : a;

type MakeTuples<ps extends readonly any[], value> = {
  -readonly [index in keyof ps]: InvertPatternForExclude<ps[index], value>;
};

/**
 * Overloaded `.exhaustive` for `matchEach`.
 * The result is always an array of handler return values.
 */
type ExhaustiveEach<output, inferredOutput> = {
  (): PickReturnValue<output, inferredOutput>[];
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): PickReturnValue<output, Union<inferredOutput, otherOutput>>[];
};

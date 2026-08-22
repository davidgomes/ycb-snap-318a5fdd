import type * as symbols from '../internals/symbols';
import type { Pattern, MatchedValue } from './Pattern';
import type { InvertPatternForExclude, InvertPattern } from './InvertPattern';
import type { DeepExclude } from './DeepExclude';
import type { Union, GuardValue, IsNever } from './helpers';
import type { FindSelected } from './FindSelected';

type PickReturnValue<a, b> = a extends symbols.unset ? b : a;

interface NonExhaustiveError<i> {
  __nonExhaustive: never;
}

interface TSPatternError<i> {
  __nonExhaustive: never;
}

export type MatchEach<
  input,
  o,
  handledCases extends any[] = [],
  inferredOutput = never,
  originalInput = input
> = {
  with<
    const p extends Pattern<input>,
    c,
    value extends MatchedValue<input, InvertPattern<p, input>>
  >(
    pattern: IsNever<p> extends true ? Pattern<input> : p,
    handler: (
      selections: FindSelected<value, p>,
      value: value
    ) => PickReturnValue<o, c>
  ): InvertPatternForExclude<p, value> extends infer excluded
    ? MatchEach<
        originalInput,
        o,
        [...handledCases, excluded],
        Union<inferredOutput, c>,
        originalInput
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
    handler: (value: value) => PickReturnValue<o, c>
  ): [
    InvertPatternForExclude<p1, value>,
    InvertPatternForExclude<p2, value>
  ] extends [infer excluded1, infer excluded2]
    ? MatchEach<
        originalInput,
        o,
        [...handledCases, excluded1, excluded2],
        Union<inferredOutput, c>,
        originalInput
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
      handler: (value: value) => PickReturnValue<o, c>
    ]
  ): [
    InvertPatternForExclude<p1, value>,
    InvertPatternForExclude<p2, value>,
    InvertPatternForExclude<p3, value>,
    { -readonly [index in keyof ps]: InvertPatternForExclude<ps[index], value> }
  ] extends [
    infer excluded1,
    infer excluded2,
    infer excluded3,
    infer excludedRest
  ]
    ? MatchEach<
        originalInput,
        o,
        [
          ...handledCases,
          excluded1,
          excluded2,
          excluded3,
          ...Extract<excludedRest, any[]>
        ],
        Union<inferredOutput, c>,
        originalInput
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
    ) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        originalInput,
        o,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        originalInput
      >
    : MatchEach<
        originalInput,
        o,
        handledCases,
        Union<inferredOutput, c>,
        originalInput
      >;

  when<
    pred extends (value: input) => unknown,
    c,
    value extends GuardValue<pred>
  >(
    predicate: pred,
    handler: (value: value) => PickReturnValue<o, c>
  ): pred extends (value: any) => value is infer narrowed
    ? MatchEach<
        originalInput,
        o,
        [...handledCases, narrowed],
        Union<inferredOutput, c>,
        originalInput
      >
    : MatchEach<
        originalInput,
        o,
        handledCases,
        Union<inferredOutput, c>,
        originalInput
      >;

  otherwise<c>(
    handler: (value: input) => PickReturnValue<o, c>
  ): Array<PickReturnValue<o, Union<inferredOutput, c>>>;

  exhaustive: DeepExcludeAll<input, handledCases> extends infer remainingCases
    ? [remainingCases] extends [never]
      ? Exhaustive<o, inferredOutput>
      : NonExhaustiveError<remainingCases>
    : never;

  run(): Array<PickReturnValue<o, inferredOutput>>;

  returnType: [inferredOutput] extends [never]
    ? <output>() => MatchEach<input, output, handledCases, never, originalInput>
    : TSPatternError<'calling `.returnType<T>()` is only allowed directly after `matchEach(...)`.'>;

  narrow(): MatchEach<
    DeepExcludeAll<input, handledCases>,
    o,
    [],
    inferredOutput,
    DeepExcludeAll<input, handledCases>
  >;

  tap(
    callback: (result: PickReturnValue<o, inferredOutput>) => void
  ): MatchEach<input, o, handledCases, inferredOutput, originalInput>;

  toFunction(): (
    input: originalInput
  ) => Array<PickReturnValue<o, inferredOutput>>;
  toExhaustiveFunction: DeepExcludeAll<
    input,
    handledCases
  > extends infer remainingCases
    ? [remainingCases] extends [never]
      ? () => (
          input: originalInput
        ) => Array<PickReturnValue<o, inferredOutput>>
      : NonExhaustiveError<remainingCases>
    : never;
  toPartialFunction(): (
    input: originalInput
  ) => Array<PickReturnValue<o, inferredOutput>> | undefined;
};

type DeepExcludeAll<a, tupleList extends any[]> = [a] extends [never]
  ? never
  : tupleList extends [infer excluded, ...infer tail]
  ? DeepExcludeAll<DeepExclude<a, excluded>, tail>
  : a;

type Exhaustive<output, inferredOutput> = {
  (): Array<PickReturnValue<output, inferredOutput>>;
  <otherOutput>(
    handler: (unexpectedValue: unknown) => PickReturnValue<output, otherOutput>
  ): Array<PickReturnValue<output, Union<inferredOutput, otherOutput>>>;
};

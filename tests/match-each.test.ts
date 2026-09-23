import { matchEach, NonExhaustiveError, P } from '../src';
import { Equal, Expect } from '../src/types/helpers';

describe('matchEach', () => {
  it('collects every matching handler in declaration order', () => {
    const result = matchEach(1 as 1 | 2)
      .with(1, () => 'first' as const)
      .with(1, () => 'second' as const)
      .with(P.number, (value) => {
        type test = Expect<Equal<typeof value, 1 | 2>>;
        return 'num' as const;
      })
      .with(2, () => 'two' as const)
      .exhaustive();

    type test = Expect<
      Equal<typeof result, ('first' | 'second' | 'num' | 'two')[]>
    >;

    expect(result).toEqual(['first', 'second', 'num']);
    expect(
      matchEach(2 as 1 | 2)
        .with(1, () => 'first' as const)
        .with(1, () => 'second' as const)
        .with(P.number, () => 'num' as const)
        .with(2, () => 'two' as const)
        .run()
    ).toEqual(['num', 'two']);
  });

  it('keeps later patterns typed against the original input', () => {
    const input = 1 as 1 | 2 | 3;
    const result = matchEach(input)
      .with(1, (value) => {
        type test = Expect<Equal<typeof value, 1>>;
        return 'a' as const;
      })
      .with(1, (value) => {
        type test = Expect<Equal<typeof value, 1>>;
        return 'b' as const;
      })
      .with(2, 3, (value) => {
        type test = Expect<Equal<typeof value, 2 | 3>>;
        return 'c' as const;
      })
      .exhaustive();

    type test = Expect<Equal<typeof result, ('a' | 'b' | 'c')[]>>;
    expect(result).toEqual(['a', 'b']);
  });

  it('throws NonExhaustiveError when nothing matches', () => {
    expect(() =>
      matchEach<number | string>(1)
        .with(P.string, () => 1)
        .run()
    ).toThrow(NonExhaustiveError);

    expect(() => {
      matchEach<number | string>(1)
        .with(P.string, () => 1)
        // @ts-expect-error
        .exhaustive();
    }).toThrow(NonExhaustiveError);

    try {
      matchEach<number | string>(1)
        .with(P.string, () => 1)
        .run();
    } catch (error) {
      expect(error).toBeInstanceOf(NonExhaustiveError);
      expect((error as NonExhaustiveError).input).toBe(1);
    }
  });

  it('is a type error to call exhaustive when cases remain', () => {
    matchEach(1 as 1 | 2)
      .with(1, () => 'one')
      // @ts-expect-error
      .exhaustive();

    matchEach<1 | 2>()
      .with(1, () => 'one')
      // @ts-expect-error
      .toExhaustiveFunction();
  });

  it('uses the exhaustive fallback only when nothing matches', () => {
    const fallback = jest.fn((value: unknown) => `unexpected ${String(value)}`);

    expect(
      matchEach(1 as 1 | 2)
        .with(1, () => 'one')
        .with(2, () => 'two')
        .exhaustive(fallback)
    ).toEqual(['one']);
    expect(fallback).not.toHaveBeenCalled();

    const unexpected = 'z' as 'a';
    expect(
      matchEach(unexpected)
        .with('a', () => 'A')
        .exhaustive((value) => `unexpected ${String(value)}`)
    ).toEqual(['unexpected z']);
  });

  it('otherwise returns the default only when nothing matched and never throws', () => {
    const handler = jest.fn((value: string) => `other ${value}`);

    expect(
      matchEach<string>('x')
        .with('a', () => 'A')
        .otherwise(handler)
    ).toEqual(['other x']);
    expect(handler).toHaveBeenCalledWith('x');

    handler.mockClear();
    expect(
      matchEach<string>('a')
        .with('a', () => 'A')
        .with(P.string, () => 'str')
        .otherwise(handler)
    ).toEqual(['A', 'str']);
    expect(handler).not.toHaveBeenCalled();

    const result = matchEach(2 as 1 | 2)
      .with(1, () => 'one' as const)
      .otherwise((value) => {
        type test = Expect<Equal<typeof value, 2>>;
        return 'two' as const;
      });

    type test = Expect<Equal<typeof result, ('one' | 'two')[]>>;
    expect(result).toEqual(['two']);
  });

  it('supports guards, when, returnType, and multi-pattern clauses', () => {
    const predicate = jest.fn((value: number) => value === 1);

    expect(
      matchEach<number>(2)
        .with(P.number, predicate, () => 'guarded')
        .with(1, 2, 3, () => 'multi')
        .run()
    ).toEqual(['multi']);
    expect(predicate).toHaveBeenCalledWith(2);

    const skipped = jest.fn(() => true);
    expect(
      matchEach<number | string>('a')
        .with(P.number, skipped, () => 'no')
        .otherwise(() => 'yes')
    ).toEqual(['yes']);
    expect(skipped).not.toHaveBeenCalled();

    const guarded = matchEach(1 as 1 | 2 | 3)
      .with(
        P.number,
        (value): value is 1 => value === 1,
        (value) => {
          type test = Expect<Equal<typeof value, 1>>;
          return 'one' as const;
        }
      )
      .with(2, 3, () => 'rest' as const)
      .exhaustive();

    type guardedTest = Expect<Equal<typeof guarded, ('one' | 'rest')[]>>;
    expect(guarded).toEqual(['one']);

    type Shape = { kind: 'circle'; r: number } | { kind: 'square'; s: number };
    const shape = { kind: 'circle', r: 4 } as Shape;
    const fromWhen = matchEach(shape)
      .when(
        (value): value is { kind: 'circle'; r: number } =>
          value.kind === 'circle',
        (value) => value.r
      )
      .when(
        (value): value is { kind: 'square'; s: number } =>
          value.kind === 'square',
        (value) => value.s
      )
      .exhaustive();

    type whenTest = Expect<Equal<typeof fromWhen, number[]>>;
    expect(fromWhen).toEqual([4]);

    matchEach(1 as 1 | 2)
      .when(
        (value) => value === 1,
        () => 'one'
      )
      // @ts-expect-error
      .exhaustive();

    const typed = matchEach(1 as 1 | 2)
      .returnType<number>()
      .with(1, () => 1)
      .with(2, () => 2)
      .exhaustive();

    type typedTest = Expect<Equal<typeof typed, number[]>>;
    expect(typed).toEqual([1]);

    matchEach(1 as number)
      .with(1, () => 1)
      // @ts-expect-error
      .returnType<number>();
  });

  it('narrows subsequent clause inputs and preserves the compiled input type', () => {
    const input = { prop: 'x' as string | undefined };

    const narrowed = matchEach(input)
      .with({ prop: P.nullish.optional() }, () => false as const)
      .narrow()
      .otherwise(({ prop }) => {
        type test = Expect<Equal<typeof prop, string>>;
        return true as const;
      });

    type narrowedTest = Expect<Equal<typeof narrowed, boolean[]>>;
    expect(narrowed).toEqual([true]);

    expect(
      matchEach({ prop: undefined as string | undefined })
        .with({ prop: P.nullish.optional() }, () => false as const)
        .narrow()
        .otherwise(() => true as const)
    ).toEqual([false]);

    matchEach(1 as 1 | 2 | 3)
      .with(1, () => 'one')
      .narrow()
      // @ts-expect-error
      .with(1, () => 'again');

    const fn = matchEach(1 as 1 | 2 | 3)
      .with(1, () => 'one' as const)
      .narrow()
      .with(2, () => 'two' as const)
      .with(3, () => 'three' as const)
      .toExhaustiveFunction();

    type inputTest = Expect<Equal<Parameters<typeof fn>[0], 1 | 2 | 3>>;
    type outputTest = Expect<
      Equal<ReturnType<typeof fn>, ('one' | 'two' | 'three')[]>
    >;

    expect(fn(1)).toEqual(['one']);
    expect(fn(2)).toEqual(['two']);
    expect(fn(3)).toEqual(['three']);
  });

  it('calls each tap once per result collected up to that point', () => {
    const tap1 = jest.fn();
    const tap2 = jest.fn();

    const result = matchEach<number | string>(1)
      .with(P.number, () => 'num' as const)
      .tap((result) => {
        type test = Expect<Equal<typeof result, 'num'>>;
        tap1(result);
      })
      .with(1, () => 'one' as const)
      .tap(tap2)
      .with('a', () => 'no')
      .run();

    expect(result).toEqual(['num', 'one']);
    expect(tap1.mock.calls).toEqual([['num']]);
    expect(tap2.mock.calls).toEqual([['num'], ['one']]);
  });

  it('runs taps inside compiled functions without changing results', () => {
    const tap = jest.fn();
    const builder = matchEach<number | string>()
      .with(P.number, (value) => value * 10)
      .with(P.string, (value) => value.toUpperCase())
      .tap(tap);

    const fn = builder.toFunction();
    const exhaustive = builder.toExhaustiveFunction();
    const partial = builder.toPartialFunction();

    expect(fn(2)).toEqual([20]);
    expect(exhaustive('ab')).toEqual(['AB']);
    expect(partial(true as unknown as number)).toBeUndefined();

    expect(tap.mock.calls).toEqual([[20], ['AB']]);

    tap.mockClear();
    expect(partial(3)).toEqual([30]);
    expect(tap.mock.calls).toEqual([[30]]);
  });

  it('compiles reusable matchers, including from an explicit type parameter', () => {
    const base = matchEach<1 | 2 | 3>().with(1, () => 'one' as const);
    const two = base.with(2, () => 'two' as const).toPartialFunction();
    const three = base.with(3, () => 'three' as const).toPartialFunction();

    expect(two(1)).toEqual(['one']);
    expect(two(2)).toEqual(['two']);
    expect(two(3)).toBeUndefined();
    expect(three(3)).toEqual(['three']);
    expect(three(2)).toBeUndefined();

    const fn = matchEach<1 | 2>()
      .with(1, () => 'one')
      .with(2, () => 'two')
      .toFunction();

    expect(fn(1)).toEqual(['one']);
    expect(() => fn(3 as unknown as 1)).toThrow(NonExhaustiveError);

    const partial = matchEach(0 as 0 | 1)
      .with(0, () => 'zero')
      .toPartialFunction();

    type partialTest = Expect<
      Equal<ReturnType<typeof partial>, string[] | undefined>
    >;
    expect(partial(0)).toEqual(['zero']);
    expect(partial(1)).toBeUndefined();
  });

  it('is exhaustive for discriminated unions while still running every match', () => {
    type Input = { type: 'a'; n: number } | { type: 'b'; s: string };
    const input = { type: 'a', n: 3 } as Input;

    const result = matchEach(input)
      .with({ type: 'a' }, (value) => {
        type test = Expect<Equal<typeof value, { type: 'a'; n: number }>>;
        return value.n;
      })
      .with({ type: 'a', n: P.select() }, (n) => {
        type test = Expect<Equal<typeof n, number>>;
        return n;
      })
      .with({ type: 'b' }, (value) => value.s.length)
      .exhaustive();

    type resultTest = Expect<Equal<typeof result, number[]>>;
    expect(result).toEqual([3, 3]);

    const fn = matchEach<Input>()
      .with({ type: 'a' }, (value) => value.n)
      .with({ type: 'b' }, (value) => value.s.length)
      .toExhaustiveFunction();

    expect(fn({ type: 'b', s: 'abcd' })).toEqual([4]);
  });

  it('keeps selections independent across clauses and compiled calls', () => {
    expect(
      matchEach({ a: 1, b: 2 })
        .with({ a: P.select('a'), missing: P.select('m') }, (sel) => sel)
        .with({ b: P.select('b') }, (sel) => sel)
        .run()
    ).toEqual([{ b: 2 }]);

    expect(
      matchEach({ a: 1, b: 'x' })
        .with({ a: P.select() }, (a) => a)
        .with({ b: P.select() }, (b) => b)
        .run()
    ).toEqual([1, 'x']);

    const fn = matchEach<{ type: 'a'; a: number } | { type: 'b'; b: string }>()
      .with({ type: 'a', a: P.select('a') }, (sel) => ({ ...sel }))
      .with({ type: 'b', b: P.select('b') }, (sel) => ({ ...sel }))
      .toPartialFunction();

    expect(fn({ type: 'a', a: 1 })).toEqual([{ a: 1 }]);
    expect(fn({ type: 'b', b: 'x' })).toEqual([{ b: 'x' }]);
    expect(fn({ type: 'a', a: 2 })).toEqual([{ a: 2 }]);

    const both = matchEach<{ a: number; b: string }>()
      .with({ a: P.select('a') }, (sel) => ({ ...sel }))
      .with({ b: P.select('b') }, (sel) => ({ ...sel }))
      .toFunction();

    expect(both({ a: 1, b: 'x' })).toEqual([{ a: 1 }, { b: 'x' }]);
    expect(both({ a: 2, b: 'y' })).toEqual([{ a: 2 }, { b: 'y' }]);
  });
});

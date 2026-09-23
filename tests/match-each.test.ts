import { matchEach, P, NonExhaustiveError } from '../src';
import { Equal, Expect } from '../src/types/helpers';

describe('matchEach', () => {
  it('collects every matching handler result in declaration order', () => {
    const result = matchEach<number | string>(1)
      .with(P.number, () => 'num')
      .with(1, () => 'one')
      .with(P.string, () => 'str')
      .with(P._, () => 'any')
      .run();

    expect(result).toEqual(['num', 'one', 'any']);
  });

  it('keeps matching against the original value after earlier clauses match', () => {
    const result = matchEach('a' as 'a' | 'b')
      .with('a', (x) => {
        type t = Expect<Equal<typeof x, 'a'>>;
        return `A:${x}`;
      })
      .with('a', () => 'A2')
      .with('b', (x) => {
        type t = Expect<Equal<typeof x, 'b'>>;
        return `B:${x}`;
      })
      .with(P.union('a', 'b'), (x) => {
        type t = Expect<Equal<typeof x, 'a' | 'b'>>;
        return `union:${x}`;
      })
      .exhaustive();

    type t = Expect<Equal<typeof result, string[]>>;
    expect(result).toEqual(['A:a', 'A2', 'union:a']);
  });

  it('throws NonExhaustiveError from run and exhaustive when nothing matches', () => {
    const run = () =>
      matchEach('c' as string)
        .with('a', () => 'A')
        .with('b', () => 'B')
        .run();

    const exhaustive = () =>
      matchEach('c' as 'a' | 'b')
        .with('a', () => 'A')
        .with('b', () => 'B')
        .exhaustive();

    expect(run).toThrow(NonExhaustiveError);
    expect(exhaustive).toThrow(NonExhaustiveError);
  });

  it('returns matching results from run even when the match is not exhaustive', () => {
    const result = matchEach('a' as 'a' | 'b')
      .with('a', () => 'A')
      .run();

    expect(result).toEqual(['A']);
  });

  it('is a type error to call exhaustive when cases remain', () => {
    matchEach('a' as 'a' | 'b')
      .with('a', () => 'A')
      // @ts-expect-error
      .exhaustive();

    const result = matchEach('a' as 'a' | 'b')
      .with('a', () => 'A' as const)
      .with('b', () => 'B' as const)
      .exhaustive();

    type t = Expect<Equal<typeof result, ('A' | 'B')[]>>;
    expect(result).toEqual(['A']);
  });

  it('calls the exhaustive fallback only when nothing matches', () => {
    const input = 'c' as 'a' | 'b';
    const fallback = jest.fn((value: unknown) => ({ unexpected: value }));

    const missed = matchEach(input)
      .with('a', (x) => x)
      .with('b', (x) => x)
      .exhaustive(fallback);

    type t = Expect<
      Equal<typeof missed, ('a' | 'b' | { unexpected: unknown })[]>
    >;
    expect(missed).toEqual([{ unexpected: 'c' }]);
    expect(fallback).toHaveBeenCalledTimes(1);
    expect(fallback).toHaveBeenCalledWith('c');

    const hitFallback = jest.fn(() => 'fallback');
    const hit = matchEach('a' as 'a' | 'b')
      .with('a', () => 'A')
      .with('b', () => 'B')
      .exhaustive(hitFallback);

    expect(hit).toEqual(['A']);
    expect(hitFallback).not.toHaveBeenCalled();
  });

  it('otherwise returns the matches, or a single default when nothing matches', () => {
    const matched = matchEach(1)
      .with(1, () => 'one' as const)
      .with(P.number, () => 'num' as const)
      .otherwise(() => 'default' as const);

    type t = Expect<Equal<typeof matched, ('one' | 'num' | 'default')[]>>;
    expect(matched).toEqual(['one', 'num']);

    const missed = matchEach<number>(1)
      .with(2, () => 'two' as const)
      .otherwise((value) => {
        type t2 = Expect<Equal<typeof value, number>>;
        return 'default' as const;
      });

    expect(missed).toEqual(['default']);
  });

  it('supports when(), multi-pattern with(), and guard predicates', () => {
    const result = matchEach<number>(15)
      .with(15, 16, (x) => {
        type t = Expect<Equal<typeof x, 15 | 16>>;
        return 'fifteen-or-sixteen';
      })
      .with(
        P.number,
        (n) => n > 10,
        (n) => `big:${n}`
      )
      .with(
        P.number,
        (n) => n > 100,
        () => 'huge'
      )
      .when(
        (n) => n % 2 === 1,
        (n) => `odd:${n}`
      )
      .with(0, () => 'zero')
      .run();

    expect(result).toEqual(['fifteen-or-sixteen', 'big:15', 'odd:15']);
  });

  it('returnType restricts branch results and is only allowed up front', () => {
    const result = matchEach(1 as number | string)
      .returnType<string>()
      .with(P.number, () => 'num')
      // @ts-expect-error
      .with(P.string, () => 1)
      .otherwise(() => 'other');

    type t = Expect<Equal<typeof result, string[]>>;
    expect(result).toEqual(['num']);

    matchEach(1)
      .with(P.number, () => 'num')
      // @ts-expect-error
      .returnType<string>();
  });

  it('narrow() excludes handled cases from subsequent clauses', () => {
    const input = 'a' as 'a' | 'b' | 'c';

    matchEach(input)
      .with('a', () => 'A' as const)
      .narrow()
      // @ts-expect-error 'a' has been excluded
      .with('a', () => 'nope' as const);

    const result = matchEach(input)
      .with('a', () => 'A' as const)
      .narrow()
      .with('b', (x) => {
        type t = Expect<Equal<typeof x, 'b'>>;
        return 'B' as const;
      })
      .with('c', () => 'C' as const)
      .exhaustive();

    type t = Expect<Equal<typeof result, ('A' | 'B' | 'C')[]>>;
    expect(result).toEqual(['A']);

    matchEach(input)
      .with('a', () => 'A' as const)
      .narrow()
      .otherwise((x) => {
        type t2 = Expect<Equal<typeof x, 'b' | 'c'>>;
        return 'rest' as const;
      });
  });

  it('narrow() deeply excludes nested cases', () => {
    const fn = (input: { prop?: 1 | 2 | 3 }) =>
      matchEach(input)
        .with({ prop: P.nullish.optional() }, () => false as const)
        .with({ prop: 2 }, () => false as const)
        .narrow()
        .otherwise(({ prop }) => {
          type test = Expect<Equal<typeof prop, 1 | 3>>;
          return true as const;
        });

    expect(fn({ prop: 1 })).toEqual([true]);
    expect(fn({ prop: 2 })).toEqual([false]);
    expect(fn({})).toEqual([false]);
  });

  describe('tap', () => {
    it('calls each tap once per result collected so far, in order', () => {
      const seen: string[] = [];

      const result = matchEach<number | string>(1)
        .with(P.number, () => 'a')
        .tap((result) => {
          seen.push(`1:${result}`);
        })
        .with(P.number, () => 'b')
        .tap((result) => {
          seen.push(`2:${result}`);
        })
        .with(P.string, () => 'c')
        .tap((result) => {
          seen.push(`3:${result}`);
        })
        .run();

      expect(result).toEqual(['a', 'b']);
      expect(seen).toEqual(['1:a', '2:a', '2:b', '3:a', '3:b']);
    });

    it('does not call a tap when no results have been collected yet', () => {
      const seen: unknown[] = [];

      const result = matchEach('a' as string)
        .tap((result) => seen.push(result))
        .with('b', () => 'B')
        .otherwise(() => 'default');

      expect(result).toEqual(['default']);
      expect(seen).toEqual([]);
    });
  });

  describe('compiled functions', () => {
    it('toFunction throws when nothing matches and reuses clauses', () => {
      const fn = matchEach<'a' | 'b' | 'c'>()
        .with('a', () => 'A' as const)
        .with('b', () => 'B' as const)
        .toFunction();

      type t = Expect<
        Equal<typeof fn, (value: 'a' | 'b' | 'c') => ('A' | 'B')[]>
      >;

      expect(fn('a')).toEqual(['A']);
      expect(fn('b')).toEqual(['B']);
      expect(() => fn('c')).toThrow(NonExhaustiveError);
    });

    it('toExhaustiveFunction enforces exhaustiveness and toPartialFunction does not throw', () => {
      matchEach<'a' | 'b'>()
        .with('a', () => 'A')
        // @ts-expect-error
        .toExhaustiveFunction();

      const partial = matchEach<'a' | 'b'>()
        .with('a', () => 'A' as const)
        .toPartialFunction();

      type tPartial = Expect<
        Equal<typeof partial, (value: 'a' | 'b') => 'A'[] | undefined>
      >;

      expect(partial('a')).toEqual(['A']);
      expect(partial('b')).toBeUndefined();

      const exhaustive = matchEach<'a' | 'b'>()
        .with('a', () => 'A' as const)
        .with('b', () => 'B' as const)
        .toExhaustiveFunction();

      type tEx = Expect<
        Equal<typeof exhaustive, (value: 'a' | 'b') => ('A' | 'B')[]>
      >;

      expect(exhaustive('a')).toEqual(['A']);
      expect(exhaustive('b')).toEqual(['B']);
      expect(() => exhaustive('c' as 'a' | 'b')).toThrow(NonExhaustiveError);
    });

    it('runs taps inside every compiled function', () => {
      const build = () => {
        const seen: string[] = [];
        const builder = matchEach<number>()
          .with(P.number, (n) => `n:${n}`)
          .tap((result) => seen.push(`1:${result}`))
          .when(
            (n) => n > 0,
            () => 'pos'
          )
          .tap((result) => seen.push(`2:${result}`));
        return { seen, builder };
      };

      const asFunction = build();
      expect(asFunction.builder.toFunction()(1)).toEqual(['n:1', 'pos']);
      expect(asFunction.seen).toEqual(['1:n:1', '2:n:1', '2:pos']);

      const asExhaustive = build();
      expect(asExhaustive.builder.toExhaustiveFunction()(-5)).toEqual(['n:-5']);
      expect(asExhaustive.seen).toEqual(['1:n:-5', '2:n:-5']);

      const asPartial = build();
      expect(asPartial.builder.toPartialFunction()(2)).toEqual(['n:2', 'pos']);
      expect(asPartial.seen).toEqual(['1:n:2', '2:n:2', '2:pos']);
    });

    it('keeps P.select() results independent across calls and clauses', () => {
      const fn = matchEach<{ a: number; b: number }>()
        .with({ a: P.select('a') }, (sel) => ({ ...sel }))
        .with({ b: P.select('b') }, (sel) => ({ ...sel }))
        .toFunction();

      const first = fn({ a: 1, b: 2 });
      const second = fn({ a: 3, b: 4 });

      expect(first).toEqual([{ a: 1 }, { b: 2 }]);
      expect(second).toEqual([{ a: 3 }, { b: 4 }]);
      expect(first).toEqual([{ a: 1 }, { b: 2 }]);
    });

    it('keeps anonymous selections independent across compiled calls', () => {
      const functions = [
        matchEach<{ n: number }>()
          .with({ n: P.select() }, (n) => n)
          .toFunction(),
        matchEach<{ n: number }>()
          .with({ n: P.select() }, (n) => n)
          .toExhaustiveFunction(),
        matchEach<{ n: number }>()
          .with({ n: P.select() }, (n) => n)
          .with(P.nullish, () => 0)
          .toPartialFunction(),
      ] as const;

      for (const fn of functions) {
        expect(fn({ n: 1 })).toEqual([1]);
        expect(fn({ n: 2 })).toEqual([2]);
      }
    });

    it('does not leak named selections between clauses', () => {
      const result = matchEach({ a: 1, b: 2, c: 3 })
        .with({ a: P.select('a'), b: P.select('b') }, (sel) => {
          expect(sel).toEqual({ a: 1, b: 2 });
          return sel.a + sel.b;
        })
        .with({ c: P.select('c') }, (sel) => {
          expect(sel).toEqual({ c: 3 });
          return sel.c;
        })
        .run();

      expect(result).toEqual([3, 3]);
    });

    it('can compile from a value or from an explicit type parameter', () => {
      const fromValue = matchEach('a' as 'a' | 'b')
        .with('a', () => 1)
        .with('b', () => 2)
        .toFunction();

      const fromType = matchEach<'a' | 'b'>()
        .with('a', () => 1)
        .with('b', () => 2)
        .toExhaustiveFunction();

      expect(fromValue('b')).toEqual([2]);
      expect(fromType('a')).toEqual([1]);
    });
  });

  it('evaluates a type-guard when() without dropping later clauses', () => {
    const isPositive = (n: number): n is number => n > 0;

    const result = matchEach(-1 as number)
      .when(isPositive, () => 'pos' as const)
      .with(P.number, () => 'num' as const)
      .exhaustive();

    type t = Expect<Equal<typeof result, ('pos' | 'num')[]>>;
    expect(result).toEqual(['num']);
  });

  it('rejects patterns that can never match the input', () => {
    matchEach('a' as 'a' | 'b')
      // @ts-expect-error
      .with('c', () => 1)
      .with('a', () => 1)
      .with('b', () => 1)
      .exhaustive();
  });
});

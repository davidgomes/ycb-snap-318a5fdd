import { matchEach, NonExhaustiveError, P } from '../src';
import { Equal, Expect } from '../src/types/helpers';

describe('matchEach', () => {
  it('collects every matching handler result in declaration order', () => {
    const result = matchEach('hello' as string)
      .with(P.string, (s) => s.length)
      .with('hello', () => 'greeting')
      .with(P.number, () => 'nope')
      .run();

    expect(result).toEqual([5, 'greeting']);
  });

  it('keeps matching later clauses after an earlier match', () => {
    const input = { kind: 'a' as const, n: 1 } as
      | { kind: 'a'; n: number }
      | { kind: 'b' };
    const result = matchEach(input)
      .with({ kind: 'a' }, (v) => v.n)
      .with({ kind: 'a', n: 1 }, () => 'also')
      .with({ kind: 'b' }, () => 'no')
      .run();

    expect(result).toEqual([1, 'also']);
  });

  it('throws NonExhaustiveError from run and exhaustive when nothing matches', () => {
    const run = () =>
      matchEach(1 as number)
        .with(P.string, () => 's')
        .run();
    const exhaustive = () =>
      matchEach('c' as 'a')
        .with('a', () => 's')
        .exhaustive();

    expect(run).toThrow(NonExhaustiveError);
    expect(exhaustive).toThrow(NonExhaustiveError);
  });

  it('calls the exhaustive fallback and wraps it in a single-element array', () => {
    const input = 'c' as 'a' | 'b';
    const result = matchEach(input)
      .with('a', (x) => x)
      .with('b', (x) => x)
      .exhaustive((v) => ({ unexpectedValue: v }));

    type t = Expect<
      Equal<
        typeof result,
        ('a' | 'b')[] | [{ unexpectedValue: unknown }]
      >
    >;

    expect(result).toEqual([{ unexpectedValue: 'c' }]);
  });

  it('returns matching results from exhaustive when patterns match', () => {
    const result = matchEach('a' as 'a' | 'b')
      .with('a', () => 1 as const)
      .with('b', () => 2 as const)
      .with(P.string, () => 3 as const)
      .exhaustive();

    type t = Expect<Equal<typeof result, (1 | 2 | 3)[]>>;
    expect(result).toEqual([1, 3]);
  });

  it('is a type error to call exhaustive when cases remain', () => {
    matchEach('a' as 'a' | 'b')
      .with('a', () => 1)
      // @ts-expect-error
      .exhaustive();
  });

  it('accepts patterns against the original input after earlier clauses', () => {
    const result = matchEach('a' as 'a' | 'b')
      .with('a', () => 'first' as const)
      .with('a', () => 'again' as const)
      .with('b', () => 'bee' as const)
      .exhaustive();

    type t = Expect<Equal<typeof result, ('first' | 'again' | 'bee')[]>>;
    expect(result).toEqual(['first', 'again']);
  });

  it('otherwise returns the default only when nothing matched', () => {
    expect(
      matchEach(1 as number)
        .with(P.string, () => 's')
        .otherwise(() => 'no')
    ).toEqual(['no']);

    expect(
      matchEach('a' as string)
        .with(P.string, () => 's')
        .with('a', () => 'a')
        .otherwise(() => 'no')
    ).toEqual(['s', 'a']);
  });

  it('types otherwise as matching results or a one-element default', () => {
    const result = matchEach('a' as 'a' | 'b')
      .with('a', () => 1 as const)
      .otherwise(() => 'no' as const);

    type t = Expect<Equal<typeof result, 1[] | ['no']>>;
    expect(result).toEqual([1]);
  });

  it('supports when, returnType, and guards', () => {
    const result = matchEach(2 as number)
      .returnType<string>()
      .with(P.number, (n) => {
        type t = Expect<Equal<typeof n, number>>;
        return `#${n}`;
      })
      .when(
        (n): n is 2 => n === 2,
        (n) => {
          type t = Expect<Equal<typeof n, 2>>;
          return 'two';
        }
      )
      .with(
        P.number,
        (n) => n > 0,
        (n) => `pos ${n}`
      )
      .otherwise(() => 'other');

    expect(result).toEqual(['#2', 'two', 'pos 2']);
  });

  it('supports several patterns in one clause', () => {
    const result = matchEach('b' as 'a' | 'b' | 'c')
      .with('a', 'b', (x) => x)
      .with('c', () => 'c' as const)
      .exhaustive();

    expect(result).toEqual(['b']);
  });

  it('narrows the input type for subsequent clauses', () => {
    const input = 'b' as 'a' | 'b' | 'c';

    const result = matchEach(input)
      .with('a', () => 'A' as const)
      .narrow()
      .with('b', (x) => {
        type t = Expect<Equal<typeof x, 'b'>>;
        return 'B' as const;
      })
      .with('c', (x) => {
        type t = Expect<Equal<typeof x, 'c'>>;
        return 'C' as const;
      })
      // @ts-expect-error 'a' was removed from the input type
      .with('a', () => 'nope' as const)
      .exhaustive();

    expect(result).toEqual(['B']);
  });

  it('runs each tap once per result collected up to that point', () => {
    const seen: string[] = [];

    const result = matchEach('a' as 'a' | 'b')
      .with('a', () => 'A')
      .tap((value) => {
        seen.push(`t1:${value}`);
      })
      .with('b', () => 'B')
      .tap((value) => {
        seen.push(`t2:${value}`);
      })
      .with(P.string, () => 'S')
      .tap((value) => {
        seen.push(`t3:${value}`);
      })
      .run();

    expect(result).toEqual(['A', 'S']);
    expect(seen).toEqual(['t1:A', 't2:A', 't3:A', 't3:S']);
  });

  it('compiles reusable functions', () => {
    const fn = matchEach<'a' | 'b' | number>()
      .with('a', () => 'A' as const)
      .with(P.number, (n) => n)
      .toFunction();

    expect(fn('a')).toEqual(['A']);
    expect(fn(4)).toEqual([4]);
    expect(() => fn('b')).toThrow(NonExhaustiveError);

    const partial = matchEach<'a' | 'b' | number>()
      .with('a', () => 'A' as const)
      .with(P.number, (n) => n)
      .toPartialFunction();

    type partialResult = Expect<
      Equal<ReturnType<typeof partial>, ('A' | number)[] | undefined>
    >;

    expect(partial('a')).toEqual(['A']);
    expect(partial('b')).toBeUndefined();

    const exhaustive = matchEach<'a' | 'b'>()
      .with('a', () => 1 as const)
      .with('b', () => 2 as const)
      .toExhaustiveFunction();

    expect(exhaustive('b')).toEqual([2]);

    matchEach<'a' | 'b'>()
      .with('a', () => 1)
      // @ts-expect-error
      .toExhaustiveFunction();
  });

  it('keeps selections independent across clauses and compiled calls', () => {
    const fn = matchEach<{ a: number; b: string }>()
      .with({ a: P.select('a') }, (sel) => sel)
      .with({ b: P.select('b') }, (sel) => sel)
      .toFunction();

    expect(fn({ a: 1, b: 'x' })).toEqual([{ a: 1 }, { b: 'x' }]);
    expect(fn({ a: 2, b: 'y' })).toEqual([{ a: 2 }, { b: 'y' }]);

    const anon = matchEach<{ n: number }>()
      .with({ n: P.select() }, (n) => n)
      .toPartialFunction();

    expect(anon({ n: 1 })).toEqual([1]);
    expect(anon({ n: 2 })).toEqual([2]);
  });

  it('runs taps inside compiled functions', () => {
    const seen: string[] = [];
    const fn = matchEach<string>()
      .with(P.string, (s) => s.toUpperCase())
      .tap((value) => {
        seen.push(value);
      })
      .with('ab', () => 'exact')
      .toPartialFunction();

    expect(fn('ab')).toEqual(['AB', 'exact']);
    expect(fn('c')).toEqual(['C']);
    expect(seen).toEqual(['AB', 'C']);
  });
});

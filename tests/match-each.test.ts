import { matchEach, NonExhaustiveError, P } from '../src';
import { Equal, Expect } from '../src/types/helpers';

describe('matchEach', () => {
  it('collects every matching handler in declaration order', () => {
    const result = matchEach(2 as number)
      .with(P.number, () => 'num' as const)
      .with(2, () => 'two' as const)
      .with(P._, () => 'any' as const)
      .with(P.string, () => 'str' as const)
      .run();

    type t = Expect<Equal<typeof result, ('num' | 'two' | 'any' | 'str')[]>>;
    expect(result).toEqual(['num', 'two', 'any']);
  });

  it('does not stop after the first match', () => {
    const calls: string[] = [];
    const result = matchEach('a' as const)
      .with('a', () => {
        calls.push('1');
        return 1 as const;
      })
      .with(P._, () => {
        calls.push('2');
        return 2 as const;
      })
      .run();

    expect(calls).toEqual(['1', '2']);
    expect(result).toEqual([1, 2]);
  });

  it('skips clauses that do not match', () => {
    const calls: string[] = [];
    const result = matchEach('a' as 'a' | 'b')
      .with('b', () => {
        calls.push('b');
        return 'b' as const;
      })
      .with('a', () => {
        calls.push('a');
        return 'a' as const;
      })
      .run();

    expect(calls).toEqual(['a']);
    expect(result).toEqual(['a']);
  });

  it('throws NonExhaustiveError from run and exhaustive when nothing matches', () => {
    const input = { color: 'orange' };
    const run = () =>
      matchEach(input)
        .with({ color: 'red' }, () => 'red')
        .run();

    expect(run).toThrow(NonExhaustiveError);
    try {
      run();
    } catch (error) {
      const err = error as NonExhaustiveError;
      expect(err).toBeInstanceOf(NonExhaustiveError);
      expect(err.input).toEqual(input);
      expect(err.message).toBe(
        'Pattern matching error: no pattern matches value {"color":"orange"}'
      );
    }

    expect(() =>
      matchEach('c' as 'a')
        .with('a', () => 1)
        .exhaustive()
    ).toThrow(NonExhaustiveError);
  });

  it('exhaustive fallback returns a single-element array and is not called when something matched', () => {
    const input = 'c' as 'a' | 'b';
    const result = matchEach(input)
      .with('a', (x) => x)
      .with('b', (x) => x)
      .exhaustive((value) => ({ unexpected: value }));

    type t = Expect<
      Equal<typeof result, ('a' | 'b' | { unexpected: unknown })[]>
    >;
    expect(result).toEqual([{ unexpected: 'c' }]);

    expect(
      matchEach('a' as 'a' | 'b')
        .with('a', () => 1 as const)
        .with('b', () => 2 as const)
        .exhaustive(() => {
          throw new Error('fallback should not run');
        })
    ).toEqual([1]);
  });

  it('otherwise returns the matches or a single default, and never throws', () => {
    const matched = matchEach('a' as 'a' | 'b')
      .with('a', () => 1 as const)
      .with('b', () => 2 as const)
      .otherwise(() => {
        throw new Error('default should not run');
      });
    expect(matched).toEqual([1]);

    let calls = 0;
    const missed = matchEach('z' as 'a')
      .with('a', () => 1 as const)
      .otherwise((value) => {
        calls += 1;
        return value;
      });

    type t = Expect<Equal<typeof missed, ('a' | 1)[]>>;
    expect(calls).toBe(1);
    expect(missed).toEqual(['z']);
  });

  it('supports when, guards, and several patterns in one clause', () => {
    expect(
      matchEach(5 as number)
        .when(
          (n) => n > 0,
          () => 'pos' as const
        )
        .with(
          P.number,
          (n) => n > 10,
          () => 'big' as const
        )
        .with(1, 2, 5, (value) => `hit:${value}` as const)
        .with(P.number, () => 'num' as const)
        .run()
    ).toEqual(['pos', 'hit:5', 'num']);

    expect(
      matchEach({ a: 1, b: 2 })
        .with(
          { a: P.select('a') },
          (x) => x.a === 1 && 'b' in x,
          (sel, value) => ({ sel, value })
        )
        .run()
    ).toEqual([
      {
        sel: { a: 1 },
        value: { a: 1, b: 2 },
      },
    ]);
  });

  it('keeps selection state independent between clauses', () => {
    const input: { x: number; y: number; type: 'a' | 'b' } = {
      x: 1,
      y: 2,
      type: 'b',
    };
    const result = matchEach(input)
      .with({ x: P.select('x'), type: 'a' as const }, (sel) => ({ ...sel }))
      .with({ y: P.select('y'), type: 'b' as const }, (sel) => ({ ...sel }))
      .with(
        { x: P.select('x'), type: 'a' as const },
        { y: P.select('y'), type: 'b' as const },
        (sel) => sel
      )
      .run();

    expect(result).toEqual([{ y: 2 }, { y: 2 }]);
  });

  it('infers selections and does not call a guard when the pattern misses', () => {
    matchEach<{ a: number; b: string }>({ a: 1, b: 'x' })
      .with({ a: P.select('a'), b: P.select('b') }, ({ a, b }, value) => {
        type t1 = Expect<Equal<typeof a, number>>;
        type t2 = Expect<Equal<typeof b, string>>;
        type t3 = Expect<Equal<typeof value, { a: number; b: string }>>;
        return [a, b, value.a] as const;
      })
      .exhaustive();

    let guardCalls = 0;
    expect(
      matchEach('a' as string)
        .with(
          'b',
          () => {
            guardCalls += 1;
            return true;
          },
          () => 'no' as const
        )
        .with(P.string, () => 'yes' as const)
        .run()
    ).toEqual(['yes']);
    expect(guardCalls).toBe(0);
  });

  it('passes anonymous selections through', () => {
    expect(
      matchEach({ a: 1, b: 2 })
        .with({ a: P.select() }, (a) => a)
        .with({ b: P.select() }, (b) => b)
        .run()
    ).toEqual([1, 2]);
  });

  it('runs tap points once per result collected so far', () => {
    const first: number[] = [];
    const second: number[] = [];
    const returned: unknown[] = [];

    const result = matchEach(1 as number)
      .tap(() => {
        first.push(-1);
      })
      .with(P.number, (n) => n)
      .with(P._, () => 2)
      .tap((value) => {
        first.push(value as number);
        return 999;
      })
      .tap((value) => {
        second.push(value as number);
      })
      .with(1, () => 3)
      .tap((value) => {
        returned.push(value);
      })
      .run();

    expect(result).toEqual([1, 2, 3]);
    expect(first).toEqual([1, 2]);
    expect(second).toEqual([1, 2]);
    expect(returned).toEqual([1, 2, 3]);
  });

  it('still accepts earlier cases after a clause, and narrow() removes them', () => {
    const wide = matchEach('a' as 'a' | 'b')
      .with('a', (x) => {
        type t = Expect<Equal<typeof x, 'a'>>;
        return x;
      })
      .with('a', (x) => {
        type t = Expect<Equal<typeof x, 'a'>>;
        return x;
      })
      .with(P._, (x) => {
        type t = Expect<Equal<typeof x, 'a' | 'b'>>;
        return x;
      })
      .exhaustive();

    expect(wide).toEqual(['a', 'a', 'a']);

    matchEach('a' as 'a' | 'b')
      .with('a', () => 1)
      // @ts-expect-error missing 'b'
      .exhaustive();

    matchEach('a' as 'a' | 'b')
      .with('a', () => 1)
      .narrow()
      // @ts-expect-error 'a' was excluded by narrow()
      .with('a', () => 2);

    const narrowed = matchEach('b' as 'a' | 'b' | 'c')
      .with('a', () => 'A' as const)
      .narrow()
      .with('b', (x) => {
        type t = Expect<Equal<typeof x, 'b'>>;
        return x;
      })
      .with('c', (x) => {
        type t = Expect<Equal<typeof x, 'c'>>;
        return x;
      })
      .exhaustive();

    expect(narrowed).toEqual(['b']);

    const fn = (input: { prop?: 1 | 2 | 3 }) =>
      matchEach(input)
        .with({ prop: P.nullish.optional() }, () => false as const)
        .with({ prop: 2 }, () => false as const)
        .narrow()
        .otherwise(({ prop }) => {
          type test = Expect<Equal<typeof prop, 1 | 3>>;
          return prop;
        });

    expect(fn({ prop: 1 })).toEqual([1]);
    expect(fn({})).toEqual([false]);
    expect(fn({ prop: 2 })).toEqual([false]);
  });

  it('checks exhaustiveness for nested unions and lists of patterns', () => {
    type Input = { value: 'a' | 'b' };
    const input = { value: 'a' } as Input;

    matchEach(input)
      .with({ value: 'a' }, () => 1)
      // @ts-expect-error
      .exhaustive();

    expect(
      matchEach(input)
        .with({ value: 'a' }, () => 1 as const)
        .with({ value: 'b' }, () => 2 as const)
        .exhaustive()
    ).toEqual([1]);

    type Letters = 'a' | 'b' | 'c' | 'd';
    matchEach<Letters>('a')
      .with('a', 'b', () => 1)
      .with('c', () => 2)
      // @ts-expect-error
      .exhaustive();

    expect(
      matchEach<Letters>('a')
        .with('a', 'b', 'c', (value) => value)
        .with('d', () => 'd' as const)
        .exhaustive()
    ).toEqual(['a']);

    type Option<T> = { kind: 'some'; value: T } | { kind: 'none' };
    matchEach<Option<number>>({ kind: 'some', value: 3 })
      .with({ kind: 'some', value: 3 }, ({ value }): number => value)
      .with({ kind: 'none' }, () => 0)
      // @ts-expect-error missing { kind: 'some', value: number }
      .exhaustive();

    expect(
      matchEach<Option<number>>({ kind: 'some', value: 3 })
        .with({ kind: 'some', value: 3 }, ({ value }): number => value)
        .with({ kind: 'some', value: P.number }, ({ value }): number => value)
        .with({ kind: 'none' }, () => 0)
        .exhaustive()
    ).toEqual([3, 3]);
  });

  it('returnType constrains branches and is only allowed at the start', () => {
    const result = matchEach('a' as 'a' | 'b')
      .returnType<string>()
      .with('a', () => 'a')
      .with('b', () => 'b')
      .exhaustive();

    type t = Expect<Equal<typeof result, string[]>>;
    expect(result).toEqual(['a']);

    matchEach<number>(1)
      .returnType<string>()
      // @ts-expect-error number is not a string
      .with(P.number, () => 1)
      .otherwise(() => 'ok');

    matchEach(1)
      .with(1, () => 1)
      // @ts-expect-error
      .returnType<number>();
  });

  it('exhaustive boolean and not-patterns agree with match', () => {
    type Pair = [boolean, boolean];
    const input = [true, true] as Pair;

    matchEach(input)
      .with([true, true], () => true)
      .with([false, true], () => false)
      .with([true, false], () => false)
      // @ts-expect-error
      .exhaustive();

    expect(
      matchEach(input)
        .with([true, true], () => 'tt' as const)
        .with([false, true], () => 'ft' as const)
        .with([true, false], () => 'tf' as const)
        .with([false, false], () => 'ff' as const)
        .with(P._, () => 'any' as const)
        .exhaustive()
    ).toEqual(['tt', 'any']);

    expect(
      matchEach(1 as string | number)
        .with(P.not(P.string), (value) => value)
        .with(P.string, (value) => value.length)
        .exhaustive()
    ).toEqual([1]);

    const guarded = matchEach('ab' as string | number)
      .with(
        P._,
        (value): value is string => typeof value === 'string',
        (value) => {
          type t = Expect<Equal<typeof value, string>>;
          return value.length;
        }
      )
      .with(P.number, (value) => value)
      .exhaustive();
    expect(guarded).toEqual([2]);
  });

  it('type guards count toward exhaustiveness without hiding later patterns', () => {
    const result = matchEach(2 as string | number)
      .when(
        (x): x is string => typeof x === 'string',
        (x) => {
          type t = Expect<Equal<typeof x, string>>;
          return x.length;
        }
      )
      .with('hello', () => 0)
      .with(P.number, (n) => n)
      .exhaustive();

    expect(result).toEqual([2]);
  });

  describe('compiled functions', () => {
    it('toFunction, toExhaustiveFunction, and toPartialFunction', () => {
      const fn = matchEach<'a' | 'b'>()
        .with('a', () => 'A' as const)
        .with('b', () => 'B' as const)
        .with(P.string, (value) => value)
        .toExhaustiveFunction();

      type t = Expect<
        Equal<typeof fn, (input: 'a' | 'b') => ('A' | 'B' | 'a' | 'b')[]>
      >;
      expect(fn('a')).toEqual(['A', 'a']);
      expect(fn('b')).toEqual(['B', 'b']);

      const partial = matchEach<'a' | 'b'>()
        .with('a', () => 1 as const)
        .toPartialFunction();

      type tp = Expect<
        Equal<typeof partial, (input: 'a' | 'b') => 1[] | undefined>
      >;
      expect(partial('a')).toEqual([1]);
      expect(partial('b')).toBeUndefined();

      const unsafe = matchEach<'a' | 'b'>()
        .with('a', () => 1 as const)
        .toFunction();
      expect(unsafe('a')).toEqual([1]);
      expect(() => unsafe('b')).toThrow(NonExhaustiveError);

      matchEach<'a' | 'b'>()
        .with('a', () => 1)
        // @ts-expect-error
        .toExhaustiveFunction();

      const typed = matchEach<'a' | 'b', string>()
        .with('a', () => 'A')
        .with('b', () => 'B')
        .toExhaustiveFunction();
      type tr = Expect<Equal<ReturnType<typeof typed>, string[]>>;
      expect(typed('b')).toEqual(['B']);
    });

    it('keeps selections independent across calls and clauses', () => {
      const fn = matchEach<
        { type: 'a'; x: number } | { type: 'b'; y: number }
      >()
        .with({ type: 'a', x: P.select('x') }, (sel) => ({ ...sel }))
        .with({ type: 'b', y: P.select('y') }, (sel) => ({ ...sel }))
        .toPartialFunction();

      expect(fn({ type: 'a', x: 1 })).toEqual([{ x: 1 }]);
      expect(fn({ type: 'b', y: 2 })).toEqual([{ y: 2 }]);
      expect(fn({ type: 'a', x: 3 })).toEqual([{ x: 3 }]);

      const lists = matchEach<number[]>()
        .with(P.array(P.select('xs')), ({ xs }) => xs.slice())
        .toFunction();
      expect(lists([1, 2])).toEqual([[1, 2]]);
      expect(lists([3])).toEqual([[3]]);

      const pattern = { n: P.select('n') };
      const reused = matchEach<{ n: number }>()
        .with(pattern, ({ n }) => n)
        .with(pattern, ({ n }) => n * 10)
        .toFunction();
      expect(reused({ n: 2 })).toEqual([2, 20]);
      expect(reused({ n: 3 })).toEqual([3, 30]);
    });

    it('runs taps inside every compiled function', () => {
      const seen: string[] = [];
      const builder = matchEach<string>()
        .with('a', () => 'A' as const)
        .with(P.string, (value) => value.toUpperCase())
        .tap((value) => {
          seen.push(value);
        });

      const exhaustive = builder.toExhaustiveFunction();
      expect(exhaustive('a')).toEqual(['A', 'A']);
      expect(exhaustive('b')).toEqual(['B']);
      expect(seen).toEqual(['A', 'A', 'B']);

      seen.length = 0;
      const partial = matchEach<string>()
        .with('a', () => 1 as const)
        .tap(() => {
          seen.push('tap');
        })
        .toPartialFunction();
      expect(partial('b')).toBeUndefined();
      expect(seen).toEqual([]);
      expect(partial('a')).toEqual([1]);
      expect(seen).toEqual(['tap']);

      seen.length = 0;
      const throwing = matchEach<'a' | 'b'>()
        .with('a', () => 'A' as const)
        .tap((value) => {
          seen.push(value);
        })
        .toFunction();
      expect(throwing('a')).toEqual(['A']);
      expect(() => throwing('b')).toThrow(NonExhaustiveError);
      expect(seen).toEqual(['A']);
    });

    it('compiled functions keep the original input type after narrow()', () => {
      const fn = matchEach<'a' | 'b' | 'c'>()
        .with('a', () => 1 as const)
        .narrow()
        .with('b', () => 2 as const)
        .with('c', () => 3 as const)
        .toExhaustiveFunction();

      type t = Expect<Equal<Parameters<typeof fn>[0], 'a' | 'b' | 'c'>>;
      expect(fn('a')).toEqual([1]);
      expect(fn('b')).toEqual([2]);
      expect(fn('c')).toEqual([3]);
    });

    it('can be built from a value and ignores that value when compiled', () => {
      const fn = matchEach(1 as number)
        .with(P.number, (n) => n + 1)
        .toFunction();
      expect(fn(4)).toEqual([5]);
      expect(
        matchEach(1 as number)
          .with(P.number, (n) => n + 1)
          .run()
      ).toEqual([2]);
    });
  });

  it('is a named export that supports arrays and returnType fallbacks', () => {
    expect(
      matchEach([1, 2, 3])
        .with(P.array(P.number), (xs) => xs.length)
        .with([1, 2, 3], () => 'exact' as const)
        .otherwise(() => 'no' as const)
    ).toEqual([3, 'exact']);

    const input = 'c' as 'a' | 'b';
    const result = matchEach<'a' | 'b', 'a' | 'b'>(input)
      .with('a', (x) => x)
      .with('b', (x) => x)
      // @ts-expect-error 'c' isn't assignable to 'a' | 'b'
      .exhaustive(() => 'c');

    expect(result).toEqual(['c']);
  });
});

import { P, matchEach, NonExhaustiveError } from '../src';
import { Equal, Expect } from '../src/types/helpers';

type Shape =
  | { type: 'circle'; radius: number }
  | { type: 'square'; size: number };

describe('matchEach', () => {
  it('should collect the results of every matching clause in order', () => {
    const res = matchEach(12 as number)
      .with(P.number.positive(), () => 'positive')
      .with(P.number.negative(), () => 'negative')
      .with(P.number.int(), () => 'int')
      .when(
        (n) => n % 2 === 0,
        () => 'even'
      )
      .otherwise(() => 'other');

    type t = Expect<Equal<typeof res, string[]>>;
    expect(res).toEqual(['positive', 'int', 'even']);
  });

  it('should accept patterns against the original input type', () => {
    const input = 'a' as 'a' | 'b';
    const res = matchEach(input)
      .with('a', (x) => {
        type t = Expect<Equal<typeof x, 'a'>>;
        return 1 as const;
      })
      .with(P.union('a', 'b'), (x) => {
        type t = Expect<Equal<typeof x, 'a' | 'b'>>;
        return 2 as const;
      })
      .exhaustive();

    type t = Expect<Equal<typeof res, (1 | 2)[]>>;
    expect(res).toEqual([1, 2]);
  });

  it('should support multi-pattern and guard overloads', () => {
    const res = matchEach(3 as 1 | 2 | 3 | 4)
      .with(1, 2, 3, (x) => `a${x}`)
      .with(3, 4, (x) => `b${x}`)
      .with(
        P.number,
        (x) => x > 2,
        (x) => `c${x}`
      )
      .exhaustive();
    expect(res).toEqual(['a3', 'b3', 'c3']);
  });

  it('.exhaustive() should be a type error if not all cases are handled', () => {
    const shape = { type: 'circle', radius: 1 } as Shape;

    matchEach(shape)
      .with({ type: 'circle' }, () => 1)
      // @ts-expect-error
      .exhaustive();

    const res = matchEach(shape)
      .with({ type: 'circle' }, () => 1)
      .with({ type: 'square' }, () => 2)
      .exhaustive();
    expect(res).toEqual([1]);
  });

  it('.run() and .exhaustive() should throw when nothing matched', () => {
    const value = 'c' as 'a' | 'b';
    const expr = matchEach(value)
      .with('a', () => 1)
      .with('b', () => 2);
    expect(() => expr.run()).toThrow(NonExhaustiveError);
    expect(() => expr.exhaustive()).toThrow(NonExhaustiveError);
    const res = expr.exhaustive(() => 'fallback');
    type t = Expect<Equal<typeof res, (number | string)[]>>;
    expect(res).toEqual(['fallback']);
  });

  it('.otherwise() should not include the default when patterns matched', () => {
    expect(
      matchEach(1 as number)
        .with(1, () => 'one')
        .otherwise(() => 'default')
    ).toEqual(['one']);
    expect(
      matchEach(2 as number)
        .with(1, () => 'one')
        .otherwise(() => 'default')
    ).toEqual(['default']);
  });

  it('.returnType() should constrain handlers', () => {
    const res = matchEach(1 as number)
      .returnType<string>()
      .with(1, () => 'one')
      // @ts-expect-error
      .with(2, () => 2)
      .otherwise(() => 'default');
    type t = Expect<Equal<typeof res, string[]>>;
  });

  it('.narrow() should narrow the input type of subsequent clauses', () => {
    const input = { prop: 1 } as { prop?: 1 | 2 };
    const res = matchEach(input)
      .with({ prop: P.nullish.optional() }, () => 'nullish')
      .narrow()
      .with({ prop: P.select() }, (prop) => {
        type t = Expect<Equal<typeof prop, 1 | 2>>;
        return `prop${prop}`;
      })
      .exhaustive();
    expect(res).toEqual(['prop1']);
  });

  it('.tap() should be called for each result collected so far', () => {
    const tapped: [string, number][] = [];
    const res = matchEach(4 as number)
      .with(P.number.positive(), () => 1)
      .tap((r) => tapped.push(['first', r]))
      .with(P.number.int(), () => 2)
      .with(P.string, () => 3)
      .tap((r) => tapped.push(['second', r]))
      .otherwise(() => 0);
    expect(res).toEqual([1, 2]);
    expect(tapped).toEqual([
      ['first', 1],
      ['second', 1],
      ['second', 2],
    ]);
  });

  it('should keep selections independent between clauses', () => {
    const res = matchEach({ a: 1, b: 2 })
      .with({ a: P.select('a') }, (sel) => sel)
      .with({ b: P.select('b') }, (sel) => sel)
      .with({ a: P.number }, (sel) => sel)
      .run();
    expect(res).toEqual([{ a: 1 }, { b: 2 }, { a: 1, b: 2 }]);
  });

  describe('compiled functions', () => {
    const tapped: string[] = [];
    const base = matchEach<Shape, string>()
      .with({ type: 'circle', radius: P.select() }, (r) => `circle ${r}`)
      .with({ type: 'square', size: P.select() }, (s) => `square ${s}`)
      .with({ type: P.select('type') }, ({ type }) => type)
      .tap((r) => tapped.push(r));

    it('.toFunction() should be reusable with independent selections', () => {
      const fn = base.toFunction();
      type t = Expect<Equal<typeof fn, (input: Shape) => string[]>>;
      expect(fn({ type: 'circle', radius: 1 })).toEqual(['circle 1', 'circle']);
      expect(fn({ type: 'square', size: 2 })).toEqual(['square 2', 'square']);
      expect(() => fn(42 as any)).toThrow(NonExhaustiveError);
      expect(tapped).toEqual(['circle 1', 'circle', 'square 2', 'square']);
    });

    it('.toExhaustiveFunction() should check exhaustiveness', () => {
      const fn = base.toExhaustiveFunction();
      expect(fn({ type: 'square', size: 3 })).toEqual(['square 3', 'square']);
      expect(() => fn(42 as any)).toThrow(NonExhaustiveError);

      matchEach<Shape>()
        .with({ type: 'circle' }, () => 1)
        // @ts-expect-error
        .toExhaustiveFunction();
    });

    it('.toPartialFunction() should return undefined when nothing matched', () => {
      const fn = matchEach<Shape>()
        .with({ type: 'circle' }, () => 1)
        .toPartialFunction();
      type t = Expect<Equal<typeof fn, (input: Shape) => number[] | undefined>>;
      expect(fn({ type: 'circle', radius: 1 })).toEqual([1]);
      expect(fn({ type: 'square', size: 1 })).toBeUndefined();
    });
  });
});

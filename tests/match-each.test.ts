import { matchEach, P } from '../src';
import { NonExhaustiveError } from '../src/errors';
import { Equal, Expect } from '../src/types/helpers';

describe('matchEach', () => {
  it('should collect all matching handler results in declaration order', () => {
    expect(
      matchEach(3)
        .with(P.number.positive(), () => 'positive')
        .with(P.number, () => 'number')
        .run()
    ).toEqual(['positive', 'number']);
  });

  it('should not short-circuit on the first matching pattern', () => {
    expect(
      matchEach('hello')
        .with(P.string, () => 'string')
        .with(P.string.startsWith('hello'), () => 'hello')
        .run()
    ).toEqual(['string', 'hello']);
  });

  it('should return an empty-matching array only via otherwise', () => {
    expect(
      matchEach(0)
        .with(P.number.positive(), () => 'positive')
        .with(P.number.negative(), () => 'negative')
        .otherwise(() => 'zero')
    ).toEqual(['zero']);
  });

  it('should throw NonExhaustiveError when nothing matches on run()', () => {
    expect(() =>
      matchEach(0)
        .with(P.number.positive(), () => 'positive')
        .with(P.number.negative(), () => 'negative')
        .run()
    ).toThrow(NonExhaustiveError);
  });

  it('should use exhaustive fallback when nothing matches at runtime', () => {
    const input: 'a' | 'b' = 'c' as any;
    const result = matchEach(input)
      .with('a', () => 'A')
      .with('b', () => 'B')
      .exhaustive((v) => ({ unexpectedValue: v }));

    expect(result).toStrictEqual([{ unexpectedValue: 'c' }]);
  });

  it('should not include otherwise handler when patterns match', () => {
    expect(
      matchEach(5)
        .with(P.number.positive(), () => 'positive')
        .with(P.number, () => 'number')
        .otherwise(() => 'fallback')
    ).toEqual(['positive', 'number']);
  });

  it('should never throw with otherwise()', () => {
    expect(
      matchEach('nope')
        .with(P.number, () => 'number')
        .otherwise((value) => `fallback: ${value}`)
    ).toEqual(['fallback: nope']);
  });

  describe('.when()', () => {
    it('should collect matching when clauses', () => {
      expect(
        matchEach(10)
          .when(
            (x) => x > 0,
            () => 'positive'
          )
          .when(
            (x) => x % 2 === 0,
            () => 'even'
          )
          .run()
      ).toEqual(['positive', 'even']);
    });
  });

  describe('multiple patterns', () => {
    it('should match if one of the patterns in a with() clause matches', () => {
      expect(
        matchEach('a' as 'a' | 'b' | 'c')
          .with('a', 'b', () => 'ab')
          .with('c', () => 'c')
          .run()
      ).toEqual(['ab']);
    });
  });

  describe('guard variants', () => {
    it('should respect guard predicates', () => {
      expect(
        matchEach(4)
          .with(P.number, (x) => x > 0, () => 'positive guarded')
          .with(P.number, () => 'number')
          .run()
      ).toEqual(['positive guarded', 'number']);
    });
  });

  describe('.tap()', () => {
    it('should call tap once per collected result at that point', () => {
      const tapped: string[] = [];

      matchEach(3)
        .with(P.number.positive(), () => 'positive')
        .tap((result) => tapped.push(`tap1:${result}`))
        .with(P.number, () => 'number')
        .tap((result) => tapped.push(`tap2:${result}`))
        .run();

      expect(tapped).toEqual(['tap1:positive', 'tap2:positive', 'tap2:number']);
    });

    it('should not affect the results array', () => {
      const results = matchEach(2)
        .with(P.number.positive(), () => 1)
        .tap(() => {})
        .with(P.number, () => 2)
        .run();

      expect(results).toEqual([1, 2]);
    });

    it('should execute tap callbacks inside compiled functions', () => {
      const tapped: number[] = [];
      const fn = matchEach<number>()
        .with(P.number.positive(), () => 1)
        .tap((result) => tapped.push(result))
        .with(P.number, () => 2)
        .tap((result) => tapped.push(result * 10))
        .toFunction();

      expect(fn(3)).toEqual([1, 2]);
      expect(tapped).toEqual([1, 10, 20]);
    });
  });

  describe('P.select()', () => {
    it('should keep independent selection state per clause', () => {
      expect(
        matchEach({ a: 1, b: 2 })
          .with({ a: P.select('a') }, ({ a }) => a)
          .with({ b: P.select('b') }, ({ b }) => b)
          .run()
      ).toEqual([1, 2]);
    });

    it('should not leak named selections across clauses in compiled functions', () => {
      const fn = matchEach<{ x: number; y: number }>()
        .with({ x: P.select('x') }, ({ x }) => x)
        .with({ y: P.select('y') }, ({ y }) => y)
        .toFunction();

      expect(fn({ x: 10, y: 20 })).toEqual([10, 20]);
      expect(fn({ x: 5, y: 7 })).toEqual([5, 7]);
    });
  });

  describe('compiled functions', () => {
    it('toFunction() should return all matching results', () => {
      const fn = matchEach<number>()
        .with(P.number.positive(), () => 'positive')
        .with(P.number, () => 'number')
        .toFunction();

      expect(fn(3)).toEqual(['positive', 'number']);
      expect(fn(-1)).toEqual(['number']);
    });

    it('toFunction() should throw when nothing matches', () => {
      const fn = matchEach<number>()
        .with(P.number.positive(), () => 'positive')
        .toFunction();

      expect(() => fn(-1)).toThrow(NonExhaustiveError);
    });

    it('toPartialFunction() should return undefined when nothing matches', () => {
      const fn = matchEach<number>()
        .with(P.number.positive(), () => 'positive')
        .toPartialFunction();

      expect(fn(3)).toEqual(['positive']);
      expect(fn(-1)).toBeUndefined();
    });

    it('toPartialFunction() should never throw', () => {
      const fn = matchEach<string>()
        .with(P.number, () => 'number')
        .toPartialFunction();

      expect(() => fn('hello')).not.toThrow();
      expect(fn('hello')).toBeUndefined();
    });

    it('toExhaustiveFunction() should work like toFunction() at runtime', () => {
      const fn = matchEach<'a' | 'b'>()
        .with('a', () => 'A')
        .with('b', () => 'B')
        .toExhaustiveFunction();

      expect(fn('a')).toEqual(['A']);
      expect(fn('b')).toEqual(['B']);
    });
  });

  describe('.returnType()', () => {
    it('should constrain handler return types', () => {
      const result = matchEach(3)
        .returnType<string>()
        .with(P.number.positive(), () => 'positive')
        .with(P.number.negative(), () => 'negative')
        .otherwise(() => 'zero');

      type t = Expect<Equal<typeof result, string[]>>;
      expect(result).toEqual(['positive']);
    });
  });

  describe('.narrow()', () => {
    it('should narrow the input type for subsequent calls', () => {
      const fn = (input: { prop?: 1 | 2 | 3 }) =>
        matchEach(input)
          .with({ prop: 2 }, () => 'two')
          .narrow()
          .otherwise(({ prop }) => {
            type t = Expect<Equal<typeof prop, 1 | 3 | undefined>>;
            return String(prop);
          });

      expect(fn({ prop: 2 })).toEqual(['two']);
      expect(fn({ prop: 1 })).toEqual(['1']);
    });
  });

  describe('exhaustiveness', () => {
    it('should allow exhaustive matching when all cases are handled', () => {
      const fn = (input: 'a' | 'b') =>
        matchEach(input)
          .with('a', () => 'A')
          .with('b', () => 'B')
          .exhaustive();

      expect(fn('a')).toEqual(['A']);
      expect(fn('b')).toEqual(['B']);
    });

    it('should typecheck missing cases on exhaustive()', () => {
      matchEach<'a' | 'b'>('a')
        .with('a', () => 'A')
        // @ts-expect-error: 'b' is missing
        .exhaustive();
    });

    it('should typecheck missing cases on toExhaustiveFunction()', () => {
      // @ts-expect-error: 'b' is missing
      const fn: (value: 'a' | 'b') => string[] = matchEach<'a' | 'b'>()
        .with('a', () => 'A')
        .toExhaustiveFunction();

      void fn;
    });
  });

  describe('patterns use original input type', () => {
    it('should allow overlapping patterns against the same input type', () => {
      type Input = { kind: 'a'; value: number } | { kind: 'b'; value: string };

      expect(
        matchEach<Input>({ kind: 'a', value: 1 })
          .with({ kind: 'a' }, () => 'a')
          .with({ kind: P.union('a', 'b') }, () => 'any')
          .run()
      ).toEqual(['a', 'any']);
    });
  });
});

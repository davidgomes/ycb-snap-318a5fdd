import { matchEach, NonExhaustiveError, P } from '../src';
import { Equal, Expect } from '../src/types/helpers';

type Shape =
  | { type: 'circle'; radius: number }
  | { type: 'square'; size: number }
  | { type: 'rect'; width: number; height: number };

describe('matchEach', () => {
  it('should collect the results of all matching clauses in declaration order', () => {
    const input = 6 as number;
    const res = matchEach(input)
      .with(P.number.positive(), () => 'positive' as const)
      .with(P.number.lt(0), () => 'negative' as const)
      .when(
        (n) => n % 2 === 0,
        (n) => `even ${n}`
      )
      .with(P.number.int(), (n) => n * 2)
      .run();

    type t = Expect<
      Equal<typeof res, ('positive' | 'negative' | string | number)[]>
    >;
    expect(res).toEqual(['positive', 'even 6', 12]);
  });

  it('should throw a NonExhaustiveError from .run() if nothing matched', () => {
    expect(() =>
      matchEach(0 as number)
        .with(P.number.positive(), () => 1)
        .run()
    ).toThrow(NonExhaustiveError);
  });

  it('should check patterns against the original input type', () => {
    const input = 'a' as 'a' | 'b';
    const res = matchEach(input)
      .with('a', (x) => {
        type t = Expect<Equal<typeof x, 'a'>>;
        return x;
      })
      .with(P.string, (x) => {
        type t = Expect<Equal<typeof x, 'a' | 'b'>>;
        return x.toUpperCase();
      })
      // 'a' is still accepted after being handled
      .with('a', (x) => `${x}!`)
      .exhaustive();

    type t = Expect<Equal<typeof res, string[]>>;
    expect(res).toEqual(['a', 'A', 'a!']);
  });

  it('should support multiple patterns and guards', () => {
    const shape = { type: 'square', size: 2 } as Shape;
    const res = matchEach(shape)
      .with({ type: 'circle' }, { type: 'square' }, (s) => {
        type t = Expect<
          Equal<
            typeof s,
            | { type: 'circle'; radius: number }
            | { type: 'square'; size: number }
          >
        >;
        return 'round-ish or square';
      })
      .with(
        { type: 'rect' },
        { type: 'square' },
        { type: 'circle' },
        () => 'any shape'
      )
      .with(
        { type: 'square' },
        (s) => s.size > 1,
        (s) => `big square ${s.size}`
      )
      .with(
        { type: 'square' },
        (s) => s.size > 10,
        () => 'huge square'
      )
      .exhaustive();

    expect(res).toEqual(['round-ish or square', 'any shape', 'big square 2']);
  });

  describe('exhaustive', () => {
    it('should be a type error if not all cases are handled', () => {
      const shape = { type: 'circle', radius: 1 } as Shape;
      const expr = matchEach(shape)
        .with({ type: 'circle' }, () => 1)
        .with({ type: 'square' }, () => 2);

      // @ts-expect-error rect isn't handled
      expr.exhaustive();

      const res = expr.with({ type: 'rect' }, () => 3).exhaustive();
      type t = Expect<Equal<typeof res, number[]>>;
      expect(res).toEqual([1]);
    });

    it('should not consider guards without type predicates exhaustive', () => {
      const input = 1 as 1 | 2;
      const expr = matchEach(input)
        .with(1, () => 'one')
        .with(
          2,
          () => true,
          () => 'two'
        );
      // @ts-expect-error 2 isn't handled
      expr.exhaustive();
    });

    it('should call the fallback if nothing matched', () => {
      const input = 'c' as 'a' | 'b';
      const res = matchEach(input)
        .with('a', (x) => x)
        .with('b', (x) => x)
        .exhaustive((v) => ({ unexpected: v }));

      type t = Expect<
        Equal<typeof res, ('a' | 'b' | { unexpected: unknown })[]>
      >;
      expect(res).toEqual([{ unexpected: 'c' }]);
    });

    it('should not call the fallback if something matched', () => {
      const fallback = jest.fn(() => 'fallback');
      const res = matchEach('a' as 'a' | 'b')
        .with('a', (x) => x)
        .with('b', (x) => x)
        .exhaustive(fallback);
      expect(res).toEqual(['a']);
      expect(fallback).not.toHaveBeenCalled();
    });

    it('should throw a NonExhaustiveError without fallback', () => {
      expect(() =>
        matchEach('c' as 'a' | 'b')
          .with('a', (x) => x)
          .with('b', (x) => x)
          .exhaustive()
      ).toThrow(NonExhaustiveError);
    });
  });

  describe('otherwise', () => {
    it('should return [handler(value)] if nothing matched', () => {
      const res = matchEach(3 as number)
        .with(0, () => 'zero')
        .otherwise((n) => n);
      type t = Expect<Equal<typeof res, (string | number)[]>>;
      expect(res).toEqual([3]);
    });

    it('should not include the default handler result if something matched', () => {
      const handler = jest.fn(() => 'default');
      const res = matchEach(0 as number)
        .with(0, () => 'zero')
        .with(P.number, () => 'number')
        .otherwise(handler);
      expect(res).toEqual(['zero', 'number']);
      expect(handler).not.toHaveBeenCalled();
    });
  });

  it('should support .returnType()', () => {
    const res = matchEach(1 as number)
      .returnType<string>()
      .with(1, () => 'one')
      // @ts-expect-error number isn't assignable to string
      .with(2, () => 2)
      .otherwise(() => 'other');
    type t = Expect<Equal<typeof res, string[]>>;

    const expr = matchEach(1 as number).with(1, () => 'one');
    // @ts-expect-error only allowed directly after matchEach()
    expr.returnType<string>();
  });

  it('should support .narrow()', () => {
    type Input = { a: 'x' | 'y'; b: string | null };
    const input: Input = { a: 'x', b: null };

    const res = matchEach(input)
      .with({ b: null }, () => 'null')
      .narrow()
      .with({ a: 'x' }, (x) => {
        type t = Expect<Equal<typeof x, { a: 'x'; b: string }>>;
        return 'x';
      })
      .with({ a: 'y' }, () => 'y')
      .exhaustive();

    expect(res).toEqual(['null', 'x']);

    matchEach(input)
      .with({ b: null }, () => 'null')
      .narrow()
      // @ts-expect-error b: null has been excluded from the input type
      .with({ b: null }, () => 'null again');
  });

  describe('selections', () => {
    it('should keep selections independent across clauses', () => {
      const input = { name: 'Gabriel', age: 30 };
      const res = matchEach(input)
        .with({ name: P.select('name') }, (sel) => {
          type t = Expect<Equal<typeof sel, { name: string }>>;
          return sel;
        })
        .with({ age: P.select('age') }, (sel) => {
          type t = Expect<Equal<typeof sel, { age: number }>>;
          return sel;
        })
        .with({ age: P.select() }, (age) => age)
        .with({ name: P.string }, (value) => value)
        .run();

      expect(res).toEqual([{ name: 'Gabriel' }, { age: 30 }, 30, input]);
    });
  });

  describe('tap', () => {
    it('should call each tap once per result collected before it', () => {
      const tap1 = jest.fn();
      const tap2 = jest.fn();
      const tap3 = jest.fn();

      const res = matchEach(4 as number)
        .tap(tap1)
        .with(P.number.positive(), () => 'positive')
        .with(P.number.negative(), () => 'negative')
        .tap(tap2)
        .with(P.number.int(), () => 'int')
        .tap(tap3)
        .run();

      expect(res).toEqual(['positive', 'int']);
      expect(tap1).not.toHaveBeenCalled();
      expect(tap2.mock.calls).toEqual([['positive']]);
      expect(tap3.mock.calls).toEqual([['positive'], ['int']]);
    });

    it('should type the callback argument with the output so far', () => {
      matchEach(1 as number)
        .with(1, () => 'one' as const)
        .tap((result) => {
          type t = Expect<Equal<typeof result, 'one'>>;
        })
        .with(2, () => 2 as const)
        .tap((result) => {
          type t = Expect<Equal<typeof result, 'one' | 2>>;
        })
        .otherwise(() => null);
    });

    it('should run taps in compiled functions', () => {
      const tap = jest.fn();
      const fn = matchEach<number>()
        .with(P.number.positive(), (n) => n)
        .tap(tap)
        .toPartialFunction();

      expect(tap).not.toHaveBeenCalled();
      fn(1);
      fn(-1);
      fn(2);
      expect(tap.mock.calls).toEqual([[1], [2]]);
    });
  });

  describe('compiled functions', () => {
    const builder = matchEach<Shape, string>()
      .with({ type: 'circle' }, (s) => `circle ${s.radius}`)
      .with({ type: P.union('circle', 'square') }, () => 'round or square')
      .with({ type: 'square' }, (s) => `square ${s.size}`);

    it('.toFunction() should return all results and throw if nothing matched', () => {
      const fn = builder.toFunction();
      type t = Expect<Equal<typeof fn, (input: Shape) => string[]>>;

      expect(fn({ type: 'circle', radius: 1 })).toEqual([
        'circle 1',
        'round or square',
      ]);
      expect(fn({ type: 'square', size: 2 })).toEqual([
        'round or square',
        'square 2',
      ]);
      expect(() => fn({ type: 'rect', width: 1, height: 2 })).toThrow(
        NonExhaustiveError
      );
    });

    it('.toExhaustiveFunction() should enforce exhaustiveness', () => {
      // @ts-expect-error rect isn't handled
      builder.toExhaustiveFunction();

      const fn = builder
        .with({ type: 'rect' }, (s) => `rect ${s.width}x${s.height}`)
        .toExhaustiveFunction();
      type t = Expect<Equal<typeof fn, (input: Shape) => string[]>>;

      expect(fn({ type: 'rect', width: 1, height: 2 })).toEqual(['rect 1x2']);
      expect(() => fn({ type: 'triangle' } as any)).toThrow(NonExhaustiveError);
    });

    it('.toPartialFunction() should return undefined if nothing matched', () => {
      const fn = builder.toPartialFunction();
      type t = Expect<Equal<typeof fn, (input: Shape) => string[] | undefined>>;

      expect(fn({ type: 'rect', width: 1, height: 2 })).toBe(undefined);
      expect(fn({ type: 'square', size: 3 })).toEqual([
        'round or square',
        'square 3',
      ]);
    });

    it('should keep the original input type after .narrow()', () => {
      const fn = matchEach<'a' | 'b'>()
        .with('a', () => 1)
        .narrow()
        .with('b', () => 2)
        .toExhaustiveFunction();
      type t = Expect<Equal<typeof fn, (input: 'a' | 'b') => number[]>>;
      expect(fn('b')).toEqual([2]);
    });

    it('should produce independent selections across calls', () => {
      const fn = matchEach<{ a: number; b?: number }>()
        .with({ a: P.select('a'), b: P.optional(P.select('b')) }, (sel) => sel)
        .toFunction();

      const first = fn({ a: 1, b: 2 });
      const second = fn({ a: 3 });

      expect(first).toEqual([{ a: 1, b: 2 }]);
      expect(second).toEqual([{ a: 3, b: undefined }]);
      expect(first[0]).not.toBe(second[0]);
    });

    it('should not be affected by clauses added later to the same builder', () => {
      const base = matchEach<number>().with(1, () => 'one');
      const fn = base.toPartialFunction();
      base.with(P.number, () => 'number');
      expect(fn(1)).toEqual(['one']);
    });
  });
});

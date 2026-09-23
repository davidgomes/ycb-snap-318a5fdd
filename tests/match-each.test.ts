import { matchEach, NonExhaustiveError, P } from '../src';
import { Equal, Expect } from '../src/types/helpers';

type Shape =
  | { type: 'circle'; radius: number }
  | { type: 'square'; size: number }
  | { type: 'rectangle'; width: number; height: number };

describe('matchEach', () => {
  describe('evaluation', () => {
    it('should collect the results of every matching clause in declaration order', () => {
      const res = matchEach(6 as number)
        .with(P.number.gt(5), () => 'gt 5')
        .with(P.number.negative(), () => 'negative')
        .with(P.number.int(), () => 'int')
        .with(6, () => 'six')
        .run();

      type t = Expect<Equal<typeof res, string[]>>;
      expect(res).toEqual(['gt 5', 'int', 'six']);
    });

    it('should call every matching handler, and only those', () => {
      const calls: string[] = [];
      matchEach<Shape>({ type: 'square', size: 2 })
        .with({ type: 'circle' }, () => calls.push('circle'))
        .with({ type: 'square' }, () => calls.push('square'))
        .with({ size: P.number.gt(1) }, () => calls.push('big square'))
        .with({ type: 'rectangle' }, () => calls.push('rectangle'))
        .with(P._, () => calls.push('anything'))
        .run();

      expect(calls).toEqual(['square', 'big square', 'anything']);
    });

    it('should infer the union of all handler return types', () => {
      const res = matchEach('a' as 'a' | 'b')
        .with('a', () => 1)
        .with('b', () => 'b')
        .with(P.string, () => true)
        .exhaustive();

      type t = Expect<Equal<typeof res, (number | string | boolean)[]>>;
      expect(res).toEqual([1, true]);
    });

    it('should keep results of handlers returning undefined', () => {
      const res = matchEach(1 as number)
        .with(1, () => undefined)
        .run();

      expect(res).toEqual([undefined]);
    });

    it('should not evaluate clauses before a terminal method is called', () => {
      const handler = jest.fn(() => 'called');
      const expression = matchEach(1 as number).with(1, handler);

      expect(handler).not.toHaveBeenCalled();
      expect(expression.run()).toEqual(['called']);
      expect(handler).toHaveBeenCalledTimes(1);
    });
  });

  describe('.with', () => {
    it('should check every pattern against the original input type', () => {
      const res = matchEach('a' as 'a' | 'b')
        .with('a', (x) => {
          type t = Expect<Equal<typeof x, 'a'>>;
          return 'first a';
        })
        .with('a', (x) => {
          type t = Expect<Equal<typeof x, 'a'>>;
          return 'second a';
        })
        .with(P.string, (x) => {
          type t = Expect<Equal<typeof x, 'a' | 'b'>>;
          return `string ${x}`;
        })
        .exhaustive();

      expect(res).toEqual(['first a', 'second a', 'string a']);
    });

    it('should forward the input type to pattern creator functions', () => {
      const res = matchEach(3 as number)
        .with(
          P.when((x) => {
            type t = Expect<Equal<typeof x, number>>;
            return x > 2;
          }),
          () => 'gt 2'
        )
        .with(P.not(3), () => 'not 3')
        .run();

      expect(res).toEqual(['gt 2']);
    });

    it('should support multiple patterns', () => {
      const fn = (input: 1 | 2 | 3 | 4) =>
        matchEach(input)
          .with(1, 2, (x) => {
            type t = Expect<Equal<typeof x, 1 | 2>>;
            return `1 or 2`;
          })
          .with(2, 3, 4, (x) => {
            type t = Expect<Equal<typeof x, 2 | 3 | 4>>;
            return `2, 3 or 4`;
          })
          .with(1, 2, 3, 4, (x) => {
            type t = Expect<Equal<typeof x, 1 | 2 | 3 | 4>>;
            return `any`;
          })
          .exhaustive();

      expect(fn(1)).toEqual(['1 or 2', 'any']);
      expect(fn(2)).toEqual(['1 or 2', '2, 3 or 4', 'any']);
      expect(fn(4)).toEqual(['2, 3 or 4', 'any']);
    });

    it('should support guard functions', () => {
      const fn = (input: number) =>
        matchEach(input)
          .with(
            P.number,
            (n) => n > 10,
            (n) => {
              type t = Expect<Equal<typeof n, number>>;
              return 'big';
            }
          )
          .with(
            P.number,
            (n) => n % 2 === 0,
            () => 'even'
          )
          .otherwise(() => 'other');

      expect(fn(12)).toEqual(['big', 'even']);
      expect(fn(11)).toEqual(['big']);
      expect(fn(4)).toEqual(['even']);
      expect(fn(3)).toEqual(['other']);
    });

    it('should use type guards to track exhaustiveness', () => {
      const isA = (x: unknown): x is 'a' => x === 'a';

      const res = matchEach('b' as 'a' | 'b')
        .with(P._, isA, (x) => {
          type t = Expect<Equal<typeof x, 'a'>>;
          return 'a';
        })
        .with('b', () => 'b')
        .exhaustive();

      expect(res).toEqual(['b']);

      matchEach('b' as 'a' | 'b')
        .with(
          P._,
          (x): boolean => x === 'a',
          () => 'a'
        )
        .with('b', () => 'b')
        // @ts-expect-error: non type-guard predicates don't handle 'a'
        .exhaustive();
    });
  });

  describe('.when', () => {
    it('should add the result of every predicate returning true', () => {
      const res = matchEach(4 as number)
        .when(
          (n) => n % 2 === 0,
          (n) => {
            type t = Expect<Equal<typeof n, number>>;
            return `${n} is even`;
          }
        )
        .when(
          (n) => n > 10,
          () => 'big'
        )
        .when(
          (n) => n > 3,
          () => 'gt 3'
        )
        .run();

      expect(res).toEqual(['4 is even', 'gt 3']);
    });

    it('should use type guards to track exhaustiveness', () => {
      const isString = (x: unknown): x is string => typeof x === 'string';

      const fn = (input: string | number) =>
        matchEach(input)
          .when(isString, (x) => {
            type t = Expect<Equal<typeof x, string>>;
            return 'string';
          })
          .with(P.number, () => 'number')
          .exhaustive();

      expect(fn('hello')).toEqual(['string']);
      expect(fn(1)).toEqual(['number']);

      matchEach(1 as string | number)
        .when(
          (x): boolean => typeof x === 'string',
          () => 'string'
        )
        .with(P.number, () => 'number')
        // @ts-expect-error: non type-guard predicates don't handle `string`
        .exhaustive();
    });
  });

  describe('.run', () => {
    it('should throw a NonExhaustiveError if no pattern matched', () => {
      expect(() =>
        matchEach(1 as number)
          .with(2, () => 'two')
          .run()
      ).toThrow(NonExhaustiveError);

      expect(() => matchEach(1).run()).toThrow(NonExhaustiveError);
    });
  });

  describe('.exhaustive', () => {
    it('should return every matching result', () => {
      const fn = (shape: Shape) =>
        matchEach(shape)
          .with({ type: 'circle' }, ({ radius }) => Math.PI * radius ** 2)
          .with({ type: 'square' }, ({ size }) => size ** 2)
          .with({ type: 'rectangle' }, ({ width, height }) => width * height)
          .with({ type: P.union('square', 'rectangle') }, () => 'polygon')
          .exhaustive();

      type t = Expect<Equal<ReturnType<typeof fn>, (number | string)[]>>;

      expect(fn({ type: 'circle', radius: 1 })).toEqual([Math.PI]);
      expect(fn({ type: 'square', size: 2 })).toEqual([4, 'polygon']);
      expect(fn({ type: 'rectangle', width: 2, height: 3 })).toEqual([
        6,
        'polygon',
      ]);
    });

    it('should be a type error if some cases are not handled', () => {
      matchEach('a' as 'a' | 'b' | 'c')
        .with('a', () => 1)
        .with('a', 'b', () => 2)
        // @ts-expect-error: 'c' isn't handled
        .exhaustive();

      matchEach<Shape>({ type: 'circle', radius: 1 })
        .with({ type: 'circle' }, () => 1)
        .with({ type: 'square', size: 1 }, () => 2)
        .with({ type: 'rectangle' }, () => 3)
        // @ts-expect-error: { type: 'square', size: number } isn't handled
        .exhaustive();

      type Input = { color: 'red' | 'blue'; size: 'small' | 'large' };

      matchEach<Input>({ color: 'red', size: 'small' })
        .with({ color: 'red' }, () => 1)
        .with({ color: 'blue', size: 'small' }, () => 2)
        // @ts-expect-error: { color: 'blue', size: 'large' } isn't handled
        .exhaustive();

      const res = matchEach<Input>({ color: 'red', size: 'large' })
        .with({ color: 'red' }, () => 1)
        .with({ color: 'blue', size: 'small' }, () => 2)
        .with({ size: 'large' }, () => 3)
        .exhaustive();

      expect(res).toEqual([1, 3]);
    });

    it('should throw a NonExhaustiveError if no pattern matched', () => {
      const input = 'c' as 'a' | 'b';

      expect(() =>
        matchEach(input)
          .with('a', () => 1)
          .with('b', () => 2)
          .exhaustive()
      ).toThrow(NonExhaustiveError);
    });

    it('should call the fallback and return its result in an array if no pattern matched', () => {
      const input = 'c' as 'a' | 'b';
      const res = matchEach(input)
        .with('a', () => 1)
        .with('b', () => 2)
        .exhaustive((unexpected) => ({ unexpected }));

      type t = Expect<Equal<typeof res, (number | { unexpected: unknown })[]>>;
      expect(res).toStrictEqual([{ unexpected: 'c' }]);
    });

    it('should not call the fallback if a pattern matched', () => {
      const fallback = jest.fn(() => 0);
      const res = matchEach('a' as 'a' | 'b')
        .with('a', () => 1)
        .with('b', () => 2)
        .exhaustive(fallback);

      expect(res).toEqual([1]);
      expect(fallback).not.toHaveBeenCalled();
    });
  });

  describe('.otherwise', () => {
    it('should return the default result in an array if no pattern matched', () => {
      const res = matchEach(1 as number)
        .with(2, () => 'two')
        .otherwise((n) => `default ${n}`);

      type t = Expect<Equal<typeof res, string[]>>;
      expect(res).toEqual(['default 1']);
    });

    it('should not include the default result if a pattern matched', () => {
      const defaultHandler = jest.fn(() => 'default');
      const res = matchEach(2 as number)
        .with(2, () => 'two')
        .with(P.number.positive(), () => 'positive')
        .otherwise(defaultHandler);

      expect(res).toEqual(['two', 'positive']);
      expect(defaultHandler).not.toHaveBeenCalled();
    });

    it('should call the default handler with unhandled values', () => {
      const res = matchEach('c' as 'a' | 'b' | 'c')
        .with('a', () => 'a')
        .otherwise((x) => {
          type t = Expect<Equal<typeof x, 'b' | 'c'>>;
          return x;
        });

      expect(res).toEqual(['c']);
    });

    it('should infer the union of all handler return types', () => {
      const res = matchEach(1 as number)
        .with(1, () => 'one')
        .otherwise(() => 0);

      type t = Expect<Equal<typeof res, (string | number)[]>>;
      expect(res).toEqual(['one']);
    });
  });

  describe('.returnType', () => {
    it('should set the output type of every handler', () => {
      const res = matchEach('a' as 'a' | 'b')
        .returnType<string>()
        .with('a', () => 'a')
        .with(P.string, (x) => x)
        .exhaustive();

      type t = Expect<Equal<typeof res, string[]>>;
      expect(res).toEqual(['a', 'a']);

      matchEach('a' as 'a' | 'b')
        .returnType<string>()
        // @ts-expect-error: number isn't assignable to string
        .with('a', () => 1)
        .with('b', () => 'b')
        .exhaustive();
    });

    it('should only be allowed directly after matchEach(...)', () => {
      matchEach('a' as 'a' | 'b')
        .with('a', () => 'a')
        // @ts-expect-error: not allowed
        .returnType<string>();
    });
  });

  describe('.narrow', () => {
    type Input = { color: 'red' | 'blue'; size: 'small' | 'large' };

    it('should narrow the input type of subsequent clauses', () => {
      const fn = (input: Input) =>
        matchEach(input)
          .with({ color: 'red', size: 'small' }, () => 'red small')
          .with({ color: 'blue', size: 'large' }, () => 'blue large')
          .narrow()
          .with(P._, (x) => {
            type t = Expect<
              Equal<
                typeof x,
                | { color: 'red'; size: 'large' }
                | { color: 'blue'; size: 'small' }
              >
            >;
            return 'rest';
          })
          .exhaustive();

      expect(fn({ color: 'red', size: 'large' })).toEqual(['rest']);
      // every clause is still evaluated at runtime.
      expect(fn({ color: 'red', size: 'small' })).toEqual([
        'red small',
        'rest',
      ]);
    });

    it('should keep tracking exhaustiveness', () => {
      const fn = (input: Input) =>
        matchEach(input)
          .with({ color: 'red', size: 'small' }, () => 1)
          .narrow()
          .with({ color: 'red' }, () => 2)
          .narrow()
          .with({ size: 'small' }, (x) => {
            type t = Expect<Equal<typeof x, { color: 'blue'; size: 'small' }>>;
            return 3;
          })
          .with({ size: 'large' }, () => 4)
          .exhaustive();

      expect(fn({ color: 'blue', size: 'small' })).toEqual([3]);

      matchEach<Input>({ color: 'red', size: 'small' })
        .with({ color: 'red', size: 'small' }, () => 1)
        .narrow()
        .with({ color: 'red' }, () => 2)
        // @ts-expect-error: { color: 'blue' } isn't handled
        .exhaustive();
    });

    it('should not narrow the input type of compiled functions', () => {
      const fn = matchEach<'a' | 'b'>()
        .with('a', () => 1)
        .narrow()
        .with(P.string, (x) => {
          type t = Expect<Equal<typeof x, 'b'>>;
          return 2;
        })
        .toExhaustiveFunction();

      type t = Expect<Equal<typeof fn, (input: 'a' | 'b') => number[]>>;
      expect(fn('a')).toEqual([1, 2]);
      expect(fn('b')).toEqual([2]);
    });
  });

  describe('.tap', () => {
    it('should call the callback once per result collected before it', () => {
      const tapped1: string[] = [];
      const tapped2: string[] = [];

      const res = matchEach(3 as number)
        .with(P.number.positive(), () => 'positive')
        .tap((result) => {
          type t = Expect<Equal<typeof result, string>>;
          tapped1.push(result);
        })
        .with(P.number.negative(), () => 'negative')
        .with(3, () => 'three')
        .tap((result) => tapped2.push(result))
        .run();

      expect(res).toEqual(['positive', 'three']);
      expect(tapped1).toEqual(['positive']);
      expect(tapped2).toEqual(['positive', 'three']);
    });

    it('should run tap points in declaration order', () => {
      const log: string[] = [];

      matchEach(1 as number)
        .with(1, () => {
          log.push('handler 1');
          return 'one';
        })
        .tap((result) => log.push(`tap 1: ${result}`))
        .with(P.number, () => {
          log.push('handler 2');
          return 'number';
        })
        .tap((result) => log.push(`tap 2: ${result}`))
        .tap((result) => log.push(`tap 3: ${result}`))
        .run();

      expect(log).toEqual([
        'handler 1',
        'tap 1: one',
        'handler 2',
        'tap 2: one',
        'tap 2: number',
        'tap 3: one',
        'tap 3: number',
      ]);
    });

    it('should not change the results', () => {
      const res = matchEach(1 as number)
        .tap(() => 'ignored')
        .with(1, () => 'one')
        .tap(() => 'ignored')
        .otherwise(() => 'default');

      type t = Expect<Equal<typeof res, string[]>>;
      expect(res).toEqual(['one']);
    });

    it('should not be called with default or fallback results', () => {
      const callback = jest.fn();

      expect(
        matchEach(1 as number)
          .with(2, () => 'two')
          .tap(callback)
          .otherwise(() => 'default')
      ).toEqual(['default']);

      expect(
        matchEach('c' as 'a' | 'b')
          .with('a', () => 'a')
          .with('b', () => 'b')
          .tap(callback)
          .exhaustive(() => 'fallback')
      ).toEqual(['fallback']);

      expect(callback).not.toHaveBeenCalled();
    });

    it('should run inside compiled functions', () => {
      const tapped: number[] = [];

      const expression = matchEach<number, number>()
        .with(P.number.positive(), (n) => n)
        .with(P.number.int(), (n) => n * 10)
        .tap((result) => tapped.push(result));

      expect(expression.toFunction()(2)).toEqual([2, 20]);
      expect(tapped).toEqual([2, 20]);

      expect(expression.toPartialFunction()(0.5)).toEqual([0.5]);
      expect(tapped).toEqual([2, 20, 0.5]);

      expect(expression.toPartialFunction()(-0.5)).toBe(undefined);
      expect(tapped).toEqual([2, 20, 0.5]);

      const exhaustiveFn = matchEach<'a' | 'b'>()
        .with('a', () => 'a')
        .with(P.string, () => 'string')
        .tap((result) => tapped.push(result.length))
        .toExhaustiveFunction();

      expect(exhaustiveFn('a')).toEqual(['a', 'string']);
      expect(tapped).toEqual([2, 20, 0.5, 1, 6]);
    });
  });

  describe('compiled functions', () => {
    it('.toFunction() should compile the clauses into a reusable function', () => {
      const fn = matchEach<Shape, string>()
        .with({ type: 'circle' }, () => 'circle')
        .with({ type: P.union('square', 'rectangle') }, () => 'polygon')
        .with({ type: 'square' }, () => 'square')
        .toFunction();

      type t = Expect<Equal<typeof fn, (input: Shape) => string[]>>;

      expect(fn({ type: 'circle', radius: 1 })).toEqual(['circle']);
      expect(fn({ type: 'square', size: 1 })).toEqual(['polygon', 'square']);
      expect(fn({ type: 'rectangle', width: 1, height: 2 })).toEqual([
        'polygon',
      ]);
    });

    it('.toFunction() should throw a NonExhaustiveError if no pattern matched', () => {
      const fn = matchEach<number>()
        .with(1, () => 'one')
        .toFunction();

      expect(fn(1)).toEqual(['one']);
      expect(() => fn(2)).toThrow(NonExhaustiveError);

      try {
        fn(2);
      } catch (error) {
        expect((error as NonExhaustiveError).input).toBe(2);
      }
    });

    it('.toExhaustiveFunction() should check exhaustiveness', () => {
      const fn = matchEach<Shape>()
        .with({ type: 'circle' }, ({ radius }) => radius)
        .with({ type: 'square' }, ({ size }) => size)
        .with({ type: 'rectangle' }, ({ width }) => width)
        .toExhaustiveFunction();

      type t = Expect<Equal<typeof fn, (input: Shape) => number[]>>;
      expect(fn({ type: 'square', size: 3 })).toEqual([3]);

      matchEach<Shape>()
        .with({ type: 'circle' }, () => 1)
        .with({ type: 'square' }, () => 2)
        // @ts-expect-error: { type: 'rectangle' } isn't handled
        .toExhaustiveFunction();
    });

    it('.toExhaustiveFunction() should throw a NonExhaustiveError on unexpected values', () => {
      const fn = matchEach<'a' | 'b'>()
        .with('a', () => 'a')
        .with('b', () => 'b')
        .toExhaustiveFunction();

      expect(() => fn('c' as any)).toThrow(NonExhaustiveError);
    });

    it('.toPartialFunction() should return undefined if no pattern matched', () => {
      const fn = matchEach<number>()
        .with(P.number.positive(), () => 'positive')
        .with(P.number.int(), () => 'int')
        .toPartialFunction();

      type t = Expect<
        Equal<typeof fn, (input: number) => string[] | undefined>
      >;

      expect(fn(2)).toEqual(['positive', 'int']);
      expect(fn(-2)).toEqual(['int']);
      expect(fn(-0.5)).toBe(undefined);
    });

    it('should return a new results array on every call', () => {
      const fn = matchEach<number>()
        .with(P._, () => 'any')
        .toFunction();

      const first = fn(1);
      first.push('mutated');

      expect(fn(1)).toEqual(['any']);
    });

    it('should not be affected by clauses added after compilation', () => {
      const expression = matchEach<number>().with(1, () => 'one');
      const fn = expression.toFunction();
      expression.with(P.number, () => 'number');

      expect(fn(1)).toEqual(['one']);
    });
  });

  describe('selections', () => {
    type Input =
      | { type: 'user'; name: string; age: number }
      | { type: 'admin'; name: string; permissions: string[] };

    it('should keep selections of each clause independent', () => {
      const res = matchEach<Input>({ type: 'user', name: 'Gabriel', age: 30 })
        .with({ name: P.select('name') }, (selections) => {
          type t = Expect<Equal<typeof selections, { name: string }>>;
          return selections;
        })
        .with({ type: 'user', age: P.select('age') }, (selections) => {
          type t = Expect<Equal<typeof selections, { age: number }>>;
          return selections;
        })
        .with({ type: 'user', name: P.select() }, (name) => {
          type t = Expect<Equal<typeof name, string>>;
          return name;
        })
        .with({ type: 'user' }, (user) => user.age)
        .run();

      expect(res).toStrictEqual([
        { name: 'Gabriel' },
        { age: 30 },
        'Gabriel',
        30,
      ]);
    });

    it('should produce independent selections across calls of compiled functions', () => {
      const fns = [
        (e: ReturnType<typeof getExpression>) => e.toFunction(),
        (e: ReturnType<typeof getExpression>) => e.toExhaustiveFunction(),
        (e: ReturnType<typeof getExpression>) => e.toPartialFunction(),
      ];

      function getExpression() {
        return matchEach<Input>()
          .with(
            { type: 'user', name: P.select('name'), age: P.select('age') },
            (selections) => selections
          )
          .with(
            { type: 'admin', permissions: P.select('permissions') },
            (selections) => selections
          )
          .with({ name: P.select('name') }, (selections) => selections);
      }

      fns.forEach((compile) => {
        const fn = compile(getExpression());

        const first = fn({ type: 'user', name: 'Alice', age: 30 });
        const second = fn({ type: 'admin', name: 'Bob', permissions: ['a'] });
        const third = fn({ type: 'user', name: 'Carol', age: 40 });

        expect(first).toStrictEqual([
          { name: 'Alice', age: 30 },
          { name: 'Alice' },
        ]);
        expect(second).toStrictEqual([{ permissions: ['a'] }, { name: 'Bob' }]);
        expect(third).toStrictEqual([
          { name: 'Carol', age: 40 },
          { name: 'Carol' },
        ]);
        expect(first![0]).not.toBe(third![0]);
      });
    });
  });
});

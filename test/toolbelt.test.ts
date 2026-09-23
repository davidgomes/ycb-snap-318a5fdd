import { describe, expect, expectTypeOf, test } from 'vitest';

import Maybe from 'true-myth/maybe';
import Result from 'true-myth/result';
import {
  transposeResult,
  transposeMaybe,
  toOkOrElseErr,
  toOkOrErr,
  fromResult,
  fromMaybe,
  toMaybe,
  sequenceMaybeAsResult,
  traverseMaybeAsResult,
  zipMaybeAsResult,
} from 'true-myth/toolbelt';

describe('transposeResult', () => {
  test('Ok(Just(T))', () => {
    let result = Result.ok<Maybe<number>, string>(Maybe.just(12));
    let transposed = transposeResult(result);
    expect(transposed).toStrictEqual(Maybe.just(Result.ok(12)));
    expectTypeOf(transposed).toEqualTypeOf<Maybe<Result<number, string>>>();
  });

  test('Ok(Nothing)', () => {
    let result = Result.ok<Maybe<number>, string>(Maybe.nothing<number>());
    let transposed = transposeResult(result);
    expect(transposed).toStrictEqual(Maybe.nothing());
    expectTypeOf(transposed).toEqualTypeOf<Maybe<Result<number, string>>>();
  });

  test('Err(E)', () => {
    let result = Result.err<Maybe<number>, string>('hello');
    let transposed = transposeResult(result);
    expect(transposed).toStrictEqual(Maybe.just(Result.err('hello')));
    expectTypeOf(transposed).toEqualTypeOf<Maybe<Result<number, string>>>();
  });
});

test('`toMaybe`', () => {
  const theValue = 'huzzah';
  const anOk = Result.ok(theValue);
  expect(toMaybe(anOk)).toEqual(Maybe.just(theValue));

  const anErr = Result.err<number, string>('uh uh');
  expect(toMaybe(anErr)).toEqual(Maybe.nothing());
});

test('fromMaybe', () => {
  const theValue = 'something';
  const errValue = 'what happened?';

  const aJust = Maybe.just(theValue);
  const anOk = Result.ok(theValue);
  expect(fromMaybe(errValue, aJust)).toEqual(anOk);

  const aNothing = Maybe.nothing();
  const anErr = Result.err(errValue);
  expect(fromMaybe(errValue, aNothing)).toEqual(anErr);
});

describe('transposeMaybe', () => {
  test('Just(Ok(T))', () => {
    let maybe = Maybe.just(Result.ok<number, string>(12));
    let transposed = transposeMaybe(maybe);
    expect(transposed).toStrictEqual(Result.ok(Maybe.just(12)));
    expectTypeOf(transposed).toEqualTypeOf<Result<Maybe<number>, string>>();
  });

  test('Just(Err(E))', () => {
    let maybe = Maybe.just(Result.err<number, string>('whoops'));
    let transposed = transposeMaybe(maybe);
    expect(transposed).toStrictEqual(Result.err('whoops'));
    expectTypeOf(transposed).toEqualTypeOf<Result<Maybe<number>, string>>();
  });

  test('Nothing', () => {
    let maybe = Maybe.nothing<Result<number, string>>();
    let transposed = transposeMaybe(maybe);
    expect(transposed).toStrictEqual(Result.ok(Maybe.nothing()));
    expectTypeOf(transposed).toEqualTypeOf<Result<Maybe<number>, string>>();
  });
});

test('`toOkOrErr`', () => {
  const theValue = 'string';
  const theJust = Maybe.of(theValue);
  const errValue = { reason: 'such badness' };

  expect(toOkOrErr(errValue, theJust)).toEqual(Result.ok(theValue));
  expect(toOkOrErr(errValue, Maybe.nothing())).toEqual(Result.err(errValue));

  expect(toOkOrErr<string, typeof errValue>(errValue)(theJust)).toEqual(
    toOkOrErr(errValue, theJust)
  );
});

test('`toOkOrElseErr`', () => {
  const theJust = Maybe.of(12);
  const errValue = 24;
  const getErrValue = () => errValue;

  expect(toOkOrElseErr(getErrValue, theJust)).toEqual(Result.ok(12));
  expect(toOkOrElseErr(getErrValue, Maybe.nothing())).toEqual(Result.err(errValue));

  expect(toOkOrElseErr<number, number>(getErrValue)(theJust)).toEqual(
    toOkOrElseErr(getErrValue, theJust)
  );
});

test('`fromResult`', () => {
  const value = 1000;
  const anOk = Result.ok(value);
  expect(fromResult(anOk)).toEqual(Maybe.just(value));

  const reason = 'oh teh noes';
  const anErr = Result.err<number, string>(reason);
  expect(fromResult(anErr)).toEqual(Maybe.nothing());
});

describe('`Maybe` collections as `Result`s', () => {
  const errValue = { reason: 'missing' };

  const parse = (s: string): Maybe<number> => {
    const n = Number.parseInt(s, 10);
    return Number.isNaN(n) ? Maybe.nothing() : Maybe.just(n);
  };

  /** Wrap `items` in a generator which records every item pulled from it. */
  function tracked<T>(items: readonly T[]): { iterable: Iterable<T>; pulled: T[] } {
    const pulled: T[] = [];
    function* generate() {
      for (const item of items) {
        pulled.push(item);
        yield item;
      }
    }
    return { iterable: generate(), pulled };
  }

  describe('`sequenceMaybeAsResult`', () => {
    test('with all `Just`s', () => {
      const sequenced = sequenceMaybeAsResult(errValue, [Maybe.just(1), Maybe.just(2)]);
      expect(sequenced).toEqual(Result.ok([1, 2]));
      expectTypeOf(sequenced).toEqualTypeOf<Result<number[], typeof errValue>>();
    });

    test('with a `Nothing`', () => {
      const sequenced = sequenceMaybeAsResult(errValue, [Maybe.just(1), Maybe.nothing<number>()]);
      expect(sequenced).toEqual(Result.err(errValue));
    });

    test('with an empty iterable', () => {
      expect(sequenceMaybeAsResult(errValue, [])).toEqual(Result.ok([]));
    });

    test('stops advancing the iterator after the first `Nothing`', () => {
      const bad = Maybe.nothing<number>();
      const { iterable, pulled } = tracked([bad, Maybe.just(2)]);
      expect(sequenceMaybeAsResult(errValue, iterable)).toEqual(Result.err(errValue));
      expect(pulled).toEqual([bad]);
    });

    test('curried form', () => {
      const allPresent = sequenceMaybeAsResult(errValue);
      const sequenced = allPresent([Maybe.just('a'), Maybe.just('b')]);
      expect(sequenced).toEqual(Result.ok(['a', 'b']));
      expectTypeOf(sequenced).toEqualTypeOf<Result<string[], typeof errValue>>();
      expect(allPresent([Maybe.nothing<string>()])).toEqual(Result.err(errValue));
    });

    test('curried form with explicit type parameters', () => {
      const allPresent = sequenceMaybeAsResult<number, string>('missing');
      expectTypeOf(allPresent).toEqualTypeOf<
        (maybes: Iterable<Maybe<number>>) => Result<number[], string>
      >();
      expect(allPresent([Maybe.just(1)])).toEqual(Result.ok([1]));
    });
  });

  describe('`traverseMaybeAsResult`', () => {
    test('when every call produces `Just`', () => {
      const traversed = traverseMaybeAsResult(errValue, ['1', '2'], parse);
      expect(traversed).toEqual(Result.ok([1, 2]));
      expectTypeOf(traversed).toEqualTypeOf<Result<number[], typeof errValue>>();
    });

    test('when a call produces `Nothing`', () => {
      expect(traverseMaybeAsResult(errValue, ['1', 'two'], parse)).toEqual(Result.err(errValue));
    });

    test('stops calling `fn` and advancing the iterator after the first `Nothing`', () => {
      const { iterable, pulled } = tracked(['one', '2', '3']);
      const seen: string[] = [];
      const traversed = traverseMaybeAsResult(errValue, iterable, (s) => {
        seen.push(s);
        return parse(s);
      });
      expect(traversed).toEqual(Result.err(errValue));
      expect(seen).toEqual(['one']);
      expect(pulled).toEqual(['one']);
    });

    test('curried form', () => {
      const parseAll = traverseMaybeAsResult(errValue);
      const traversed = parseAll(['3', '4'], parse);
      expect(traversed).toEqual(Result.ok([3, 4]));
      expectTypeOf(traversed).toEqualTypeOf<Result<number[], typeof errValue>>();
      expect(parseAll(new Set(['three']), parse)).toEqual(Result.err(errValue));
    });

    test('curried form with explicit type parameters', () => {
      const parseAll = traverseMaybeAsResult<string, number, string>('missing');
      expectTypeOf(parseAll).toEqualTypeOf<
        (items: Iterable<string>, fn: (item: string) => Maybe<number>) => Result<number[], string>
      >();
      expect(parseAll(['5'], parse)).toEqual(Result.ok([5]));
    });
  });

  describe('`zipMaybeAsResult`', () => {
    test('with two `Just`s', () => {
      const zipped = zipMaybeAsResult(errValue, Maybe.just(1), Maybe.just('a'));
      expect(zipped).toEqual(Result.ok([1, 'a']));
      expectTypeOf(zipped).toEqualTypeOf<Result<[number, string], typeof errValue>>();
    });

    test('with a `Nothing`', () => {
      expect(zipMaybeAsResult(errValue, Maybe.nothing<number>(), Maybe.just('a'))).toEqual(
        Result.err(errValue)
      );
      expect(zipMaybeAsResult(errValue, Maybe.just(1), Maybe.nothing<string>())).toEqual(
        Result.err(errValue)
      );
    });

    test('curried form', () => {
      const bothPresent = zipMaybeAsResult(errValue);
      const zipped = bothPresent(Maybe.just(true), Maybe.just(2));
      expect(zipped).toEqual(Result.ok([true, 2]));
      expectTypeOf(zipped).toEqualTypeOf<Result<[boolean, number], typeof errValue>>();
      expect(bothPresent(Maybe.nothing(), Maybe.just(2))).toEqual(Result.err(errValue));
    });

    test('curried form with explicit type parameters', () => {
      const bothPresent = zipMaybeAsResult<number, string, string>('missing');
      expectTypeOf(bothPresent).toEqualTypeOf<
        (a: Maybe<number>, b: Maybe<string>) => Result<[number, string], string>
      >();
      expect(bothPresent(Maybe.just(1), Maybe.just('a'))).toEqual(Result.ok([1, 'a']));
    });
  });
});

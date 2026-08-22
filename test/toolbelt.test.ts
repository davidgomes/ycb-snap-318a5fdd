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

describe('`sequenceMaybeAsResult`', () => {
  test('with all Just', () => {
    expect(sequenceMaybeAsResult('missing', [Maybe.just(1), Maybe.just(2)])).toEqual(
      Result.ok([1, 2])
    );
  });

  test('converts the first Nothing into Err', () => {
    let visits = 0;
    const items: Iterable<Maybe<number>> = {
      *[Symbol.iterator]() {
        visits += 1;
        yield Maybe.just(1);
        visits += 1;
        yield Maybe.nothing();
        visits += 1;
        yield Maybe.just(3);
      },
    };

    expect(sequenceMaybeAsResult('missing', items)).toEqual(Result.err('missing'));
    expect(visits).toBe(2);
  });

  test('curried form', () => {
    const asResult = sequenceMaybeAsResult('missing');
    expect(asResult([Maybe.just(1), Maybe.just(2)])).toEqual(Result.ok([1, 2]));
    expect(asResult([Maybe.just(1), Maybe.nothing()])).toEqual(Result.err('missing'));
  });
});

describe('`traverseMaybeAsResult`', () => {
  const even = (n: number) => (n % 2 === 0 ? Maybe.just(n * 10) : Maybe.nothing<number>());

  test('with all successes', () => {
    expect(traverseMaybeAsResult('odd', [2, 4, 6], even)).toEqual(Result.ok([20, 40, 60]));
  });

  test('converts the first Nothing into Err and stops', () => {
    let visits = 0;
    const items: Iterable<number> = {
      *[Symbol.iterator]() {
        for (const n of [2, 3, 4]) {
          visits += 1;
          yield n;
        }
      },
    };

    expect(traverseMaybeAsResult('odd', items, even)).toEqual(Result.err('odd'));
    expect(visits).toBe(2);
  });

  test('curried form', () => {
    const asResult = traverseMaybeAsResult('odd');
    expect(asResult([2, 4], even)).toEqual(Result.ok([20, 40]));
    expect(asResult([2, 3], even)).toEqual(Result.err('odd'));
  });
});

describe('`zipMaybeAsResult`', () => {
  test('with two Justs', () => {
    expect(zipMaybeAsResult('missing', Maybe.just('a'), Maybe.just(1))).toEqual(
      Result.ok(['a', 1])
    );
  });

  test('with a Nothing', () => {
    expect(zipMaybeAsResult('missing', Maybe.just('a'), Maybe.nothing())).toEqual(
      Result.err('missing')
    );
    expect(zipMaybeAsResult('missing', Maybe.nothing(), Maybe.just(1))).toEqual(
      Result.err('missing')
    );
  });

  test('curried form', () => {
    const zipOrMissing = zipMaybeAsResult('missing');
    expect(zipOrMissing(Maybe.just('a'), Maybe.just(1))).toEqual(Result.ok(['a', 1]));
    expect(zipOrMissing(Maybe.just('a'), Maybe.nothing())).toEqual(Result.err('missing'));
  });
});

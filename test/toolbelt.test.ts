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
  test('all `Just`', () => {
    let result = sequenceMaybeAsResult('missing', [Maybe.just(1), Maybe.just(2)]);
    expect(result).toEqual(Result.ok([1, 2]));
    expectTypeOf(result).toEqualTypeOf<Result<number[], string>>();
  });

  test('with a `Nothing`, stopping after it', () => {
    let pulled: Array<Maybe<number>> = [];
    function* items() {
      for (let m of [Maybe.just(1), Maybe.nothing<number>(), Maybe.just(3)]) {
        pulled.push(m);
        yield m;
      }
    }
    expect(sequenceMaybeAsResult('missing', items())).toEqual(Result.err('missing'));
    expect(pulled).toEqual([Maybe.just(1), Maybe.nothing()]);
  });

  test('curried', () => {
    let orMissing = sequenceMaybeAsResult('missing');
    let result = orMissing([Maybe.just('a'), Maybe.just('b')]);
    expect(result).toEqual(Result.ok(['a', 'b']));
    expectTypeOf(result).toEqualTypeOf<Result<string[], string>>();
    expect(orMissing([Maybe.nothing<string>()])).toEqual(Result.err('missing'));
  });

  test('with an `undefined` error value', () => {
    let result = sequenceMaybeAsResult(undefined, [Maybe.nothing<number>()]);
    expect(result).toEqual(Result.err(undefined));
    expectTypeOf(result).toEqualTypeOf<Result<number[], undefined>>();
  });
});

describe('`traverseMaybeAsResult`', () => {
  const positive = (n: number) => (n > 0 ? Maybe.just(n) : Maybe.nothing<number>());

  test('all `Just`', () => {
    let result = traverseMaybeAsResult('not positive', [1, 2], positive);
    expect(result).toEqual(Result.ok([1, 2]));
    expectTypeOf(result).toEqualTypeOf<Result<number[], string>>();
  });

  test('with a `Nothing`, not calling `fn` after it', () => {
    let called: number[] = [];
    let result = traverseMaybeAsResult('not positive', [1, -2, 3], (n) => {
      called.push(n);
      return positive(n);
    });
    expect(result).toEqual(Result.err('not positive'));
    expect(called).toEqual([1, -2]);
  });

  test('curried', () => {
    let orNotPositive = traverseMaybeAsResult('not positive');
    let result = orNotPositive([1, 2], positive);
    expect(result).toEqual(Result.ok([1, 2]));
    expectTypeOf(result).toEqualTypeOf<Result<number[], string>>();
    expect(orNotPositive([-1], positive)).toEqual(Result.err('not positive'));
  });
});

describe('`zipMaybeAsResult`', () => {
  test('both `Just`', () => {
    let result = zipMaybeAsResult('missing', Maybe.just(1), Maybe.just('a'));
    expect(result).toEqual(Result.ok([1, 'a']));
    expectTypeOf(result).toEqualTypeOf<Result<[number, string], string>>();
  });

  test('either `Nothing`', () => {
    expect(zipMaybeAsResult('missing', Maybe.nothing<number>(), Maybe.just('a'))).toEqual(
      Result.err('missing')
    );
    expect(zipMaybeAsResult('missing', Maybe.just(1), Maybe.nothing<string>())).toEqual(
      Result.err('missing')
    );
  });

  test('curried', () => {
    let orMissing = zipMaybeAsResult('missing');
    let result = orMissing(Maybe.just(1), Maybe.just(true));
    expect(result).toEqual(Result.ok([1, true]));
    expectTypeOf(result).toEqualTypeOf<Result<[number, boolean], string>>();
    expect(orMissing(Maybe.nothing<number>(), Maybe.just(true))).toEqual(Result.err('missing'));
  });
});

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

function counting<T>(values: readonly T[]): { iterable: Iterable<T>; count: () => number } {
  let count = 0;
  return {
    count: () => count,
    iterable: {
      *[Symbol.iterator]() {
        for (const value of values) {
          count += 1;
          yield value;
        }
      },
    },
  };
}

describe('Maybe collections as Result', () => {
  test('`sequenceMaybeAsResult`', () => {
    expect(sequenceMaybeAsResult('missing', [Maybe.just(1), Maybe.just(2)])).toEqual(
      Result.ok([1, 2])
    );
    expect(sequenceMaybeAsResult<number, string>('missing')([Maybe.just(4)])).toEqual(
      Result.ok([4])
    );
    expect(sequenceMaybeAsResult('missing', [] as Array<Maybe<number>>)).toEqual(Result.ok([]));

    const source = counting([Maybe.just(1), Maybe.nothing<number>(), Maybe.just(3)]);
    expect(sequenceMaybeAsResult('missing', source.iterable)).toEqual(Result.err('missing'));
    expect(source.count()).toBe(2);
  });

  test('`traverseMaybeAsResult`', () => {
    const render = (n: number) => (n === 0 ? Maybe.nothing<string>() : Maybe.just(String(n)));
    const seen: number[] = [];
    const source = counting([1, 0, 2]);
    expect(
      traverseMaybeAsResult('missing', source.iterable, (n) => {
        seen.push(n);
        return render(n);
      })
    ).toEqual(Result.err('missing'));
    expect(seen).toEqual([1, 0]);
    expect(source.count()).toBe(2);

    const curried = traverseMaybeAsResult<string>('missing');
    expect(curried([1, 2], render)).toEqual(Result.ok(['1', '2']));
    expectTypeOf(curried([1, 2], render)).toEqualTypeOf<Result<string[], string>>();
  });

  test('`zipMaybeAsResult`', () => {
    expect(zipMaybeAsResult('missing', Maybe.just(1), Maybe.just(2))).toEqual(Result.ok([1, 2]));
    expect(zipMaybeAsResult('missing')(Maybe.just('a'), Maybe.just('b'))).toEqual(
      Result.ok(['a', 'b'])
    );
    expect(zipMaybeAsResult('missing', Maybe.nothing<number>(), Maybe.just(2))).toEqual(
      Result.err('missing')
    );
    expect(zipMaybeAsResult('missing', Maybe.just(1), Maybe.nothing<number>())).toEqual(
      Result.err('missing')
    );
    expect(zipMaybeAsResult('missing', Maybe.nothing<number>(), Maybe.nothing<number>())).toEqual(
      Result.err('missing')
    );
  });
});

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
    const r = sequenceMaybeAsResult('missing', [Maybe.just(1), Maybe.just(2)]);
    expect(r).toEqual(Result.ok([1, 2]));
    expectTypeOf(r).toEqualTypeOf<Result<number[], string>>();
  });

  test('stops at the first `Nothing`', () => {
    let pulled = 0;
    function* items() {
      for (const m of [Maybe.just(1), Maybe.nothing<number>(), Maybe.just(3)]) {
        pulled += 1;
        yield m;
      }
    }
    expect(sequenceMaybeAsResult('missing', items())).toEqual(Result.err('missing'));
    expect(pulled).toBe(2);
  });

  test('curried', () => {
    const seq = sequenceMaybeAsResult<number, string>('missing');
    expectTypeOf(seq).toEqualTypeOf<
      (maybes: Iterable<Maybe<number>>) => Result<number[], string>
    >();
    expect(seq([Maybe.nothing()])).toEqual(Result.err('missing'));
    expect(seq([])).toEqual(Result.ok([]));
  });
});

describe('`traverseMaybeAsResult`', () => {
  const lookup = (key: string, i: number) =>
    Maybe.of(({ a: 1, b: 2 } as Record<string, number>)[key]).map((n) => n + i);

  test('all `Just`', () => {
    const r = traverseMaybeAsResult('missing', ['a', 'b'], lookup);
    expect(r).toEqual(Result.ok([1, 3]));
    expectTypeOf(r).toEqualTypeOf<Result<number[], string>>();
  });

  test('stops at the first `Nothing`', () => {
    const calls: string[] = [];
    const r = traverseMaybeAsResult('missing', ['a', 'z', 'b'], (key, i) => {
      calls.push(key);
      return lookup(key, i);
    });
    expect(r).toEqual(Result.err('missing'));
    expect(calls).toEqual(['a', 'z']);
  });

  test('curried', () => {
    const trav = traverseMaybeAsResult('missing');
    const r = trav(['a'], lookup);
    expectTypeOf(r).toEqualTypeOf<Result<number[], string>>();
    expect(r).toEqual(Result.ok([1]));
    expect(trav(['z'], lookup)).toEqual(Result.err('missing'));
  });
});

describe('`zipMaybeAsResult`', () => {
  test('both `Just`', () => {
    const r = zipMaybeAsResult('missing', Maybe.just(1), Maybe.just('a'));
    expect(r).toEqual(Result.ok([1, 'a']));
    expectTypeOf(r).toEqualTypeOf<Result<[number, string], string>>();
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
    const zip = zipMaybeAsResult('missing');
    const r = zip(Maybe.just(1), Maybe.just(true));
    expectTypeOf(r).toEqualTypeOf<Result<[number, boolean], string>>();
    expect(r).toEqual(Result.ok([1, true]));
    expect(zip(Maybe.nothing<number>(), Maybe.just(true))).toEqual(Result.err('missing'));
  });
});

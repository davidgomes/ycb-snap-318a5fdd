/**
  Tools for working easily with `Maybe` and `Result` *together*... but which do
  not *require* you to use both. If they were in the `true-myth/maybe` or
  `true-myth/result` modules, then importing either would always include the
  other. While that is not usually a concern with bundlers, it *is* an issue
  when using dynamic imports or otherwise doing runtime resolution in a browser
  or similar environment.

  The flip side of that is: importing from *this* module *does* require access
  to both `Maybe` and `Result` modules.

  @module
 */

import Result from './result.js';
import Maybe, * as maybe from './maybe.js';
import { curry1 } from './-private/utils.js';

/**
  Transposes a {@linkcode Result} of a {@linkcode Maybe} into a `Maybe` of a
  `Result`.

  | Input         | Output         |
  | ------------- | -------------- |
  | `Ok(Just(T))` | `Just(Ok(T))`  |
  | `Err(E)`      | `Just(Err(E))` |
  | `Ok(Nothing)` | `Nothing`      |

  @param result a `Result<Maybe<T>, E>` to transform to a `Maybe<Result<T, E>>`.
 */
export function transposeResult<T extends {}, E>(result: Result<Maybe<T>, E>): Maybe<Result<T, E>> {
  return result.match({
    Ok: (maybe) =>
      maybe.match({
        Just: (v) => Maybe.just(Result.ok<T, E>(v)),
        Nothing: () => Maybe.nothing(),
      }),
    Err: (e) => Maybe.just(Result.err<T, E>(e)),
  });
}

/**
  Convert a {@linkcode Result} to a {@linkcode Maybe}.

  The converted type will be {@linkcode "maybe".Just Just} if the `Result` is
  {@linkcode "result".Ok Ok} or {@linkcode "maybe".Nothing Nothing} if the
  `Result` is {@linkcode "result".Err Err}; the wrapped error value will be
  discarded.

  @param result The `Result` to convert to a `Maybe`
  @returns      `Just` the value in `result` if it is `Ok`; otherwise `Nothing`
 */
export function toMaybe<T extends {}>(result: Result<T, unknown>): Maybe<T> {
  return result.isOk ? Maybe.just(result.value) : Maybe.nothing();
}

/**
  Transform a {@linkcode Maybe} into a {@linkcode Result}.

  If the `Maybe` is a {@linkcode "maybe".Just Just}, its value will be wrapped
  in the {@linkcode "result".Ok Ok} variant; if it is a {@linkcode
  "maybe".Nothing Nothing}, the `errValue` will be wrapped in the {@linkcode
  "result".Err Err} variant.

  @param errValue A value to wrap in an `Err` if `maybe` is a `Nothing`.
  @param maybe    The `Maybe` to convert to a `Result`.
 */
export function fromMaybe<T extends {}, E>(errValue: E, maybe: Maybe<T>): Result<T, E>;
export function fromMaybe<T extends {}, E>(errValue: E): (maybe: Maybe<T>) => Result<T, E>;
export function fromMaybe<T extends {}, E>(
  errValue: E,
  maybe?: Maybe<T>
): Result<T, E> | ((maybe: Maybe<T>) => Result<T, E>) {
  const op = (m: Maybe<T>) => (m.isJust ? Result.ok<T, E>(m.value) : Result.err<T, E>(errValue));
  return curry1(op, maybe);
}

/**
  Transposes a {@linkcode Maybe} of a {@linkcode Result} into a `Result` of a
  `Maybe`.

  | Input          | Output        |
  | -------------- | ------------- |
  | `Just(Ok(T))`  | `Ok(Just(T))` |
  | `Just(Err(E))` | `Err(E)`      |
  | `Nothing`      | `Ok(Nothing)` |

  @param maybe a `Maybe<Result<T, E>>` to transform to a `Result<Maybe<T>, E>>`.
 */
export function transposeMaybe<T extends {}, E>(maybe: Maybe<Result<T, E>>): Result<Maybe<T>, E> {
  return maybe.match({
    Just: (result) =>
      result.match({
        Ok: (v) => Result.ok(Maybe.just(v)),
        Err: (e) => Result.err(e),
      }),
    Nothing: () => Result.ok(Maybe.nothing()),
  });
}

/**
  Transform the {@linkcode Maybe} into a {@linkcode Result}, using the wrapped
  value as the {@linkcode "result".Ok Ok} value if the `Maybe` is {@linkcode
  "maybe".Just Just}; otherwise using the supplied `error` value for {@linkcode
  "result".Err Err}.

  @template T  The wrapped value.
  @template E  The error type to in the `Result`.
  @param error The error value to use if the `Maybe` is `Nothing`.
  @param maybe The `Maybe` instance to convert.
  @returns     A `Result` containing the value wrapped in `maybe` in an `Ok`, or
               `error` in an `Err`.
 */
export function toOkOrErr<T extends {}, E>(error: E, maybe: Maybe<T>): Result<T, E>;
export function toOkOrErr<T extends {}, E>(error: E): (maybe: Maybe<T>) => Result<T, E>;
export function toOkOrErr<T extends {}, E>(
  error: E,
  maybe?: Maybe<T>
): Result<T, E> | ((maybe: Maybe<T>) => Result<T, E>) {
  const op = (m: Maybe<T>) => (m.isJust ? Result.ok<T, E>(m.value) : Result.err<T, E>(error));
  return maybe !== undefined ? op(maybe) : op;
}

/**
  Transform the {@linkcode Maybe} into a {@linkcode Result}, using the wrapped
  value as the {@linkcode "result".Ok Ok} value if the `Maybe` is {@linkcode
  "maybe".Just Just}; otherwise using `elseFn` to generate the {@linkcode
  "result".Err Err}.

  @template T  The wrapped value.
  @template E  The error type to in the `Result`.
  @param elseFn The function which generates an error of type `E`.
  @param maybe  The `Maybe` instance to convert.
  @returns     A `Result` containing the value wrapped in `maybe` in an `Ok`, or
               the value generated by `elseFn` in an `Err`.
 */
export function toOkOrElseErr<T extends {}, E>(elseFn: () => E, maybe: Maybe<T>): Result<T, E>;
export function toOkOrElseErr<T extends {}, E>(elseFn: () => E): (maybe: Maybe<T>) => Result<T, E>;
export function toOkOrElseErr<T extends {}, E>(
  elseFn: () => E,
  maybe?: Maybe<T>
): Result<T, E> | ((maybe: Maybe<T>) => Result<T, E>) {
  const op = (m: Maybe<T>) => (m.isJust ? Result.ok<T, E>(m.value) : Result.err<T, E>(elseFn()));
  return curry1(op, maybe);
}

/**
  Construct a {@linkcode "maybe".Maybe Maybe<T>} from a
  {@linkcode "result".Result Result<T, E>}.

  If the `Result` is a {@linkcode "result".Ok Ok}, wrap its value in {@linkcode
  "maybe".Just Just}. If the `Result` is an {@linkcode "result".Err Err}, throw
  away the wrapped `E` and transform to a {@linkcode "maybe".Nothing Nothing}.

  @template T  The type of the value wrapped in a {@linkcode "result".Ok Ok} and
    therefore in the {@linkcode "maybe".Just Just} of the resulting `Maybe`.
  @param result The `Result` to construct a `Maybe` from.
  @returns      `Just` if `result` was `Ok` or `Nothing` if it was `Err`.
 */
export function fromResult<T extends {}>(result: Result<T, unknown>): Maybe<T> {
  return result.isOk ? Maybe.just(result.value) : Maybe.nothing<T>();
}

/**
  Given any iterable of {@linkcode Maybe}s, produce {@linkcode "result".Ok Ok}
  an array of all their values if every item is {@linkcode "maybe".Just Just},
  or {@linkcode "result".Err Err} with `errValue` if any item is {@linkcode
  "maybe".Nothing Nothing}.

  This is {@linkcode "maybe".sequence maybe.sequence} followed by
  {@linkcode toOkOrErr}: the iterable is not advanced any further once a
  `Nothing` is found.

  ```ts
  import { sequenceMaybeAsResult } from 'true-myth/toolbelt';
  import * as maybe from 'true-myth/maybe';

  sequenceMaybeAsResult('missing', [maybe.just(1), maybe.just(2)]); // Ok([1, 2])
  sequenceMaybeAsResult('missing', [maybe.just(1), maybe.nothing()]); // Err('missing')
  ```

  @param errValue The error to use if any item is `Nothing`.
  @param maybes   The `Maybe`s to combine.
 */
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes: Iterable<Maybe<T>>
): Result<Array<T>, E>;
/**
  Curried form of {@linkcode sequenceMaybeAsResult}: provide the error value
  first, and get back a function which accepts the `Maybe`s to combine.

  ```ts
  import { sequenceMaybeAsResult } from 'true-myth/toolbelt';
  import * as maybe from 'true-myth/maybe';

  const allPresent = sequenceMaybeAsResult('missing');
  allPresent([maybe.just(1), maybe.just(2)]); // Ok([1, 2])
  ```

  @param errValue The error to use if any item is `Nothing`.
 */
export function sequenceMaybeAsResult<E>(
  errValue: E
): <T extends {}>(maybes: Iterable<Maybe<T>>) => Result<Array<T>, E>;
/**
  Curried form of {@linkcode sequenceMaybeAsResult} with explicit type
  parameters for the wrapped value and the error.

  @param errValue The error to use if any item is `Nothing`.
 */
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E
): (maybes: Iterable<Maybe<T>>) => Result<Array<T>, E>;
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes?: Iterable<Maybe<T>>
): Result<Array<T>, E> | ((maybes: Iterable<Maybe<T>>) => Result<Array<T>, E>) {
  const op = (ms: Iterable<Maybe<T>>) => toOkOrErr(errValue, maybe.sequence(ms));
  return curry1(op, maybes);
}

/**
  Apply a function which produces a {@linkcode Maybe} to every item in an
  iterable, producing {@linkcode "result".Ok Ok} an array of the results if
  every call produced a {@linkcode "maybe".Just Just}, or
  {@linkcode "result".Err Err} with `errValue` if any call produces a
  {@linkcode "maybe".Nothing Nothing}.

  This is {@linkcode "maybe".traverse maybe.traverse} followed by
  {@linkcode toOkOrErr}: `fn` is not called again once it produces a
  `Nothing`.

  ```ts
  import { traverseMaybeAsResult } from 'true-myth/toolbelt';
  import * as maybe from 'true-myth/maybe';

  const lookup = (key: string) => maybe.of(process.env[key]);

  traverseMaybeAsResult('missing env var', ['HOME', 'PATH'], lookup);
  // Ok(['/home/me', '/usr/bin:/bin']), or Err('missing env var')
  ```

  @param errValue The error to use if `fn` produces `Nothing` for any item.
  @param items    The items to transform.
  @param fn       The function to apply to each item.
 */
export function traverseMaybeAsResult<A, B extends {}, E>(
  errValue: E,
  items: Iterable<A>,
  fn: (item: A) => Maybe<B>
): Result<Array<B>, E>;
/**
  Curried form of {@linkcode traverseMaybeAsResult}: provide the error value
  first, and get back a function which accepts the items and the function to
  apply to them.

  ```ts
  import { traverseMaybeAsResult } from 'true-myth/toolbelt';
  import * as maybe from 'true-myth/maybe';

  const lookupAll = traverseMaybeAsResult('missing env var');
  lookupAll(['HOME', 'PATH'], (key: string) => maybe.of(process.env[key]));
  ```

  @param errValue The error to use if `fn` produces `Nothing` for any item.
 */
export function traverseMaybeAsResult<E>(
  errValue: E
): <A, B extends {}>(items: Iterable<A>, fn: (item: A) => Maybe<B>) => Result<Array<B>, E>;
/**
  Curried form of {@linkcode traverseMaybeAsResult} with explicit type
  parameters for the items, the wrapped values, and the error.

  @param errValue The error to use if `fn` produces `Nothing` for any item.
 */
export function traverseMaybeAsResult<A, B extends {}, E>(
  errValue: E
): (items: Iterable<A>, fn: (item: A) => Maybe<B>) => Result<Array<B>, E>;
export function traverseMaybeAsResult<A, B extends {}, E>(
  errValue: E,
  items?: Iterable<A>,
  fn?: (item: A) => Maybe<B>
): Result<Array<B>, E> | ((items: Iterable<A>, fn: (item: A) => Maybe<B>) => Result<Array<B>, E>) {
  const op = (theItems: Iterable<A>, mapFn: (item: A) => Maybe<B>) =>
    toOkOrErr(errValue, maybe.traverse(theItems, mapFn));
  return items !== undefined && fn !== undefined ? op(items, fn) : op;
}

/**
  Combine two {@linkcode Maybe}s into {@linkcode "result".Ok Ok} a tuple of
  their values if both are {@linkcode "maybe".Just Just}, or
  {@linkcode "result".Err Err} with `errValue` if either is
  {@linkcode "maybe".Nothing Nothing}.

  This is {@linkcode "maybe".zip maybe.zip} followed by {@linkcode toOkOrErr}.

  ```ts
  import { zipMaybeAsResult } from 'true-myth/toolbelt';
  import * as maybe from 'true-myth/maybe';

  zipMaybeAsResult('missing', maybe.just(1), maybe.just('a')); // Ok([1, 'a'])
  zipMaybeAsResult('missing', maybe.just(1), maybe.nothing()); // Err('missing')
  ```

  @param errValue The error to use if either `Maybe` is `Nothing`.
  @param a        The first `Maybe`.
  @param b        The second `Maybe`.
 */
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  a: Maybe<A>,
  b: Maybe<B>
): Result<[A, B], E>;
/**
  Curried form of {@linkcode zipMaybeAsResult}: provide the error value first,
  and get back a function which accepts the two `Maybe`s to combine.

  ```ts
  import { zipMaybeAsResult } from 'true-myth/toolbelt';
  import * as maybe from 'true-myth/maybe';

  const bothPresent = zipMaybeAsResult('missing');
  bothPresent(maybe.just(1), maybe.just('a')); // Ok([1, 'a'])
  ```

  @param errValue The error to use if either `Maybe` is `Nothing`.
 */
export function zipMaybeAsResult<E>(
  errValue: E
): <A extends {}, B extends {}>(a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>;
/**
  Curried form of {@linkcode zipMaybeAsResult} with explicit type parameters
  for the two wrapped values and the error.

  @param errValue The error to use if either `Maybe` is `Nothing`.
 */
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E
): (a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>;
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  a?: Maybe<A>,
  b?: Maybe<B>
): Result<[A, B], E> | ((a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>) {
  const op = (first: Maybe<A>, second: Maybe<B>) => toOkOrErr(errValue, maybe.zip(first, second));
  return a !== undefined && b !== undefined ? op(a, b) : op;
}

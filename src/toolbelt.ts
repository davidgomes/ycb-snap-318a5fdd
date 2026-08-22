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
import Maybe from './maybe.js';
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
  {@linkcode "maybe".sequence sequence} an iterable of {@linkcode Maybe}s as a
  {@linkcode Result}, using `errValue` for the first {@linkcode "maybe".Nothing
  Nothing}.

  @param errValue The error to use when a `Maybe` is `Nothing`.
  @param maybes   The `Maybe`s to collect.
 */
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes: Iterable<Maybe<T>>
): Result<T[], E>;
/**
  Curried form of {@linkcode sequenceMaybeAsResult}.

  @param errValue The error to use when a `Maybe` is `Nothing`.
 */
export function sequenceMaybeAsResult<E>(
  errValue: E
): <T extends {}>(maybes: Iterable<Maybe<T>>) => Result<T[], E>;
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes?: Iterable<Maybe<T>>
): Result<T[], E> | ((maybes: Iterable<Maybe<T>>) => Result<T[], E>) {
  const op = (items: Iterable<Maybe<T>>): Result<T[], E> => {
    const values: T[] = [];

    for (const maybe of items) {
      if (maybe.isNothing) {
        return Result.err(errValue);
      }

      values.push(maybe.value);
    }

    return Result.ok(values);
  };

  return curry1(op, maybes);
}

/**
  {@linkcode "maybe".traverse traverse} an iterable as a {@linkcode Result},
  using `errValue` for the first {@linkcode "maybe".Nothing Nothing}.

  @param errValue The error to use when a mapped `Maybe` is `Nothing`.
  @param items    The items to traverse.
  @param fn       A function that produces a `Maybe` for each item.
 */
export function traverseMaybeAsResult<T, U extends {}, E>(
  errValue: E,
  items: Iterable<T>,
  fn: (item: T) => Maybe<U>
): Result<U[], E>;
/**
  Curried form of {@linkcode traverseMaybeAsResult}: pass `errValue` first, then
  the remaining arguments.

  @param errValue The error to use when a mapped `Maybe` is `Nothing`.
 */
export function traverseMaybeAsResult<E>(
  errValue: E
): <T, U extends {}>(items: Iterable<T>, fn: (item: T) => Maybe<U>) => Result<U[], E>;
export function traverseMaybeAsResult<T, U extends {}, E>(
  errValue: E,
  items?: Iterable<T>,
  fn?: (item: T) => Maybe<U>
): Result<U[], E> | ((items: Iterable<T>, fn: (item: T) => Maybe<U>) => Result<U[], E>) {
  const op = (source: Iterable<T>, mapFn: (item: T) => Maybe<U>): Result<U[], E> => {
    const values: U[] = [];

    for (const item of source) {
      const mapped = mapFn(item);
      if (mapped.isNothing) {
        return Result.err(errValue);
      }

      values.push(mapped.value);
    }

    return Result.ok(values);
  };

  return items === undefined || fn === undefined ? op : op(items, fn);
}

/**
  {@linkcode "maybe".zip zip} two {@linkcode Maybe}s as a {@linkcode Result},
  using `errValue` if either is {@linkcode "maybe".Nothing Nothing}.

  @param errValue The error to use when either `Maybe` is `Nothing`.
  @param a        The first `Maybe`.
  @param b        The second `Maybe`.
 */
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  a: Maybe<A>,
  b: Maybe<B>
): Result<[A, B], E>;
/**
  Curried form of {@linkcode zipMaybeAsResult}: pass `errValue` first, then the
  remaining arguments.

  @param errValue The error to use when either `Maybe` is `Nothing`.
 */
export function zipMaybeAsResult<E>(
  errValue: E
): <A extends {}, B extends {}>(a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>;
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  a?: Maybe<A>,
  b?: Maybe<B>
): Result<[A, B], E> | ((a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>) {
  const op = (left: Maybe<A>, right: Maybe<B>): Result<[A, B], E> => {
    if (left.isJust && right.isJust) {
      return Result.ok([left.value, right.value]);
    }

    return Result.err(errValue);
  };

  return a === undefined || b === undefined ? op : op(a, b);
}

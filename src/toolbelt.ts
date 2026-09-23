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
import Maybe, {
  sequence as sequenceMaybes,
  traverse as traverseMaybes,
  zip as zipMaybes,
} from './maybe.js';
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
  {@linkcode "maybe".sequence sequence} an iterable of {@linkcode Maybe}s into a
  {@linkcode Result}, using `errValue` for the {@linkcode "result".Err Err} when
  any item is {@linkcode "maybe".Nothing Nothing}.

  Iteration stops immediately after the first `Nothing`.

  @param errValue The error used when a `Maybe` is `Nothing`.
  @param maybes The `Maybe`s to combine.
 */
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes: Iterable<Maybe<T>>
): Result<T[], E>;
/**
  Curried {@linkcode sequenceMaybeAsResult}. `sequenceMaybeAsResult(errValue)`
  returns a function of the remaining `maybes` argument.

  @param errValue The error used when a `Maybe` is `Nothing`.
 */
export function sequenceMaybeAsResult<E>(
  errValue: E
): <T extends {}>(maybes: Iterable<Maybe<T>>) => Result<T[], E>;
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes?: Iterable<Maybe<T>>
): Result<T[], E> | (<U extends {}>(items: Iterable<Maybe<U>>) => Result<U[], E>) {
  let op = <U extends {}>(items: Iterable<Maybe<U>>): Result<U[], E> => {
    let sequenced = sequenceMaybes(items);
    return sequenced.isJust ? Result.ok(sequenced.value) : Result.err(errValue);
  };

  if (arguments.length === 1) {
    return op;
  }

  return op(maybes as Iterable<Maybe<T>>);
}

/**
  Map `items` with `fn` and {@linkcode sequenceMaybeAsResult} the resulting
  {@linkcode Maybe}s.

  `Nothing` becomes {@linkcode "result".Err Err(errValue)}. Iteration stops
  immediately after the first `Nothing`, and `fn` is not called for later items.

  @param errValue The error used when `fn` returns `Nothing`.
  @param items Values to map.
  @param fn Function that returns a `Maybe` for one item.
 */
export function traverseMaybeAsResult<A, T extends {}, E>(
  errValue: E,
  items: Iterable<A>,
  fn: (item: A) => Maybe<T>
): Result<T[], E>;
/**
  Curried {@linkcode traverseMaybeAsResult}.
  `traverseMaybeAsResult(errValue)` returns a function of the remaining
  `(items, fn)` arguments.

  @param errValue The error used when `fn` returns `Nothing`.
 */
export function traverseMaybeAsResult<E>(
  errValue: E
): <A, T extends {}>(items: Iterable<A>, fn: (item: A) => Maybe<T>) => Result<T[], E>;
export function traverseMaybeAsResult<A, T extends {}, E>(
  errValue: E,
  items?: Iterable<A>,
  fn?: (item: A) => Maybe<T>
):
  | Result<T[], E>
  | (<B, U extends {}>(nextItems: Iterable<B>, nextFn: (item: B) => Maybe<U>) => Result<U[], E>) {
  let op = <B, U extends {}>(
    nextItems: Iterable<B>,
    nextFn: (item: B) => Maybe<U>
  ): Result<U[], E> => {
    let sequenced = traverseMaybes(nextItems, nextFn);
    return sequenced.isJust ? Result.ok(sequenced.value) : Result.err(errValue);
  };

  if (arguments.length === 1) {
    return op;
  }

  return op(items as Iterable<A>, fn as (item: A) => Maybe<T>);
}

/**
  Pair two {@linkcode Maybe}s into a {@linkcode Result}. Both {@linkcode
  "maybe".Just Just} values become {@linkcode "result".Ok Ok} of the pair.
  Either {@linkcode "maybe".Nothing Nothing} becomes {@linkcode "result".Err
  Err(errValue)}.

  @param errValue The error used when either `Maybe` is `Nothing`.
  @param maybeA The first `Maybe`.
  @param maybeB The second `Maybe`.
 */
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  maybeA: Maybe<A>,
  maybeB: Maybe<B>
): Result<[A, B], E>;
/**
  Curried {@linkcode zipMaybeAsResult}. `zipMaybeAsResult(errValue)` returns a
  function of the remaining `(maybeA, maybeB)` arguments.

  @param errValue The error used when either `Maybe` is `Nothing`.
 */
export function zipMaybeAsResult<E>(
  errValue: E
): <A extends {}, B extends {}>(maybeA: Maybe<A>, maybeB: Maybe<B>) => Result<[A, B], E>;
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  maybeA?: Maybe<A>,
  maybeB?: Maybe<B>
):
  | Result<[A, B], E>
  | (<T extends {}, U extends {}>(left: Maybe<T>, right: Maybe<U>) => Result<[T, U], E>) {
  let op = <T extends {}, U extends {}>(left: Maybe<T>, right: Maybe<U>): Result<[T, U], E> => {
    let zipped = zipMaybes(left, right);
    return zipped.isJust ? Result.ok(zipped.value) : Result.err(errValue);
  };

  if (arguments.length === 1) {
    return op;
  }

  return op(maybeA as Maybe<A>, maybeB as Maybe<B>);
}

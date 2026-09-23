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
  Given any iterable of {@linkcode Maybe}s, produce {@linkcode "result".Ok Ok}
  an array of all the wrapped values if every item is {@linkcode "maybe".Just
  Just}, or {@linkcode "result".Err Err} with `errValue` if any item is
  {@linkcode "maybe".Nothing Nothing}. Iteration stops immediately after the
  first `Nothing`.

  ```ts
  import Maybe from 'true-myth/maybe';
  import { sequenceMaybeAsResult } from 'true-myth/toolbelt';

  sequenceMaybeAsResult('missing', [Maybe.just(1), Maybe.just(2)]); // Ok([1, 2])
  sequenceMaybeAsResult('missing', [Maybe.just(1), Maybe.nothing()]); // Err('missing')
  ```

  @param errValue The error to use if any item is `Nothing`.
  @param maybes   The `Maybe`s to combine.
 */
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes: Iterable<Maybe<T>>
): Result<Array<T>, E>;
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E
): (maybes: Iterable<Maybe<T>>) => Result<Array<T>, E>;
export function sequenceMaybeAsResult<T extends {}, E>(
  errValue: E,
  maybes?: Iterable<Maybe<T>>
): Result<Array<T>, E> | ((maybes: Iterable<Maybe<T>>) => Result<Array<T>, E>) {
  const op = (ms: Iterable<Maybe<T>>) => traverseMaybeAsResult(errValue, ms, (m: Maybe<T>) => m);
  return curry1(op, maybes);
}

/**
  Apply a {@linkcode Maybe}-producing function to every item in an iterable,
  producing {@linkcode "result".Ok Ok} an array of the results if every call
  produced a {@linkcode "maybe".Just Just}, or {@linkcode "result".Err Err}
  with `errValue` as soon as any call produces {@linkcode "maybe".Nothing
  Nothing}. Iteration stops immediately after the first `Nothing`.

  ```ts
  import Maybe from 'true-myth/maybe';
  import { traverseMaybeAsResult } from 'true-myth/toolbelt';

  const lookup = (key: string) => Maybe.of(config[key]);
  traverseMaybeAsResult('missing config', ['host', 'port'], lookup);
  ```

  @param errValue The error to use if any call produces `Nothing`.
  @param items    The items to transform.
  @param fn       The function to apply to each item, along with its index.
 */
export function traverseMaybeAsResult<T, U extends {}, E>(
  errValue: E,
  items: Iterable<T>,
  fn: (item: T, index: number) => Maybe<U>
): Result<Array<U>, E>;
export function traverseMaybeAsResult<E>(
  errValue: E
): <T, U extends {}>(
  items: Iterable<T>,
  fn: (item: T, index: number) => Maybe<U>
) => Result<Array<U>, E>;
export function traverseMaybeAsResult<T, U extends {}, E>(
  errValue: E,
  items?: Iterable<T>,
  fn?: (item: T, index: number) => Maybe<U>
):
  | Result<Array<U>, E>
  | ((items: Iterable<T>, fn: (item: T, index: number) => Maybe<U>) => Result<Array<U>, E>) {
  const op = (is: Iterable<T>, f: (item: T, index: number) => Maybe<U>): Result<Array<U>, E> => {
    const values: U[] = [];
    let index = 0;
    for (const item of is) {
      const m = f(item, index++);
      if (m.isNothing) {
        return Result.err(errValue);
      }
      values.push(m.value);
    }
    return Result.ok(values);
  };

  return items !== undefined && fn !== undefined ? op(items, fn) : op;
}

/**
  Combine two {@linkcode Maybe}s into a {@linkcode Result} of a tuple:
  {@linkcode "result".Ok Ok} if both are {@linkcode "maybe".Just Just}, or
  {@linkcode "result".Err Err} with `errValue` if either is {@linkcode
  "maybe".Nothing Nothing}.

  ```ts
  import Maybe from 'true-myth/maybe';
  import { zipMaybeAsResult } from 'true-myth/toolbelt';

  zipMaybeAsResult('missing', Maybe.just(1), Maybe.just('a')); // Ok([1, 'a'])
  zipMaybeAsResult('missing', Maybe.just(1), Maybe.nothing()); // Err('missing')
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
export function zipMaybeAsResult<E>(
  errValue: E
): <A extends {}, B extends {}>(a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>;
export function zipMaybeAsResult<A extends {}, B extends {}, E>(
  errValue: E,
  a?: Maybe<A>,
  b?: Maybe<B>
): Result<[A, B], E> | ((a: Maybe<A>, b: Maybe<B>) => Result<[A, B], E>) {
  const op = (ma: Maybe<A>, mb: Maybe<B>): Result<[A, B], E> =>
    ma.isJust && mb.isJust ? Result.ok([ma.value, mb.value]) : Result.err(errValue);

  return a !== undefined && b !== undefined ? op(a, b) : op;
}

import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferOutput,
} from '../../types/index.ts';

/**
 * Brand for an unresolved recursive input.
 */
declare const recurInputBrand: unique symbol;

/**
 * Brand for an unresolved recursive output.
 */
declare const recurOutputBrand: unique symbol;

/**
 * Placeholder input left by `Recur`.
 *
 * Resolved schemas replace this marker with their own input type.
 */
export interface RecurInput {
  readonly [recurInputBrand]: 'input';
}

/**
 * Placeholder output left by `Recur`.
 *
 * Resolved schemas replace this marker with their own output type.
 */
export interface RecurOutput {
  readonly [recurOutputBrand]: 'output';
}

/**
 * Any schema that may contain `Recur`.
 */
type AnySchema =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Whether a type is `any`.
 */
type IsAny<T> = 0 extends 1 & T ? true : false;

/**
 * Built-in objects that must not be walked while searching for `Recur`.
 */
type Builtin =
  | Date
  | RegExp
  | Error
  | Promise<unknown>
  | ArrayBuffer
  | DataView
  | ((...args: never[]) => unknown);

/**
 * Whether `T` still contains an unresolved `Recur` marker.
 *
 * The search is depth-limited so an already resolved self-reference, which
 * points at itself, does not look like an unresolved placeholder.
 */
type HasRecur<
  T,
  TDepth extends readonly unknown[] = [],
> = TDepth['length'] extends 24
  ? false
  : IsAny<T> extends true
    ? false
    : T extends RecurInput | RecurOutput
      ? true
      : T extends readonly (infer TItem)[]
        ? HasRecur<TItem, [...TDepth, 1]>
        : T extends Map<infer TKey, infer TValue>
          ? HasRecur<TKey, [...TDepth, 1]> | HasRecur<TValue, [...TDepth, 1]>
          : T extends Set<infer TValue>
            ? HasRecur<TValue, [...TDepth, 1]>
            : T extends Builtin
              ? false
              : T extends object
                ? HasRecur<T[keyof T], [...TDepth, 1]>
                : false;

/**
 * Replaces `Recur` markers in `T` with the schema input and output.
 *
 * Array, record, map, set, and object positions stay wrapped around the
 * schema's own input or output, so the result is self-referential.
 */
type Build<TInput, TOutput, T> =
  IsAny<T> extends true
    ? T
    : true extends HasRecur<T>
      ? T extends RecurInput
        ? Build<TInput, TOutput, TInput>
        : T extends RecurOutput
          ? Build<TInput, TOutput, TOutput>
          : T extends readonly (infer TItem)[]
            ? Build<TInput, TOutput, TItem>[]
            : T extends Map<infer TKey, infer TValue>
              ? Map<
                  Build<TInput, TOutput, TKey>,
                  Build<TInput, TOutput, TValue>
                >
              : T extends Set<infer TValue>
                ? Set<Build<TInput, TOutput, TValue>>
                : T extends Builtin
                  ? T
                  : T extends object
                    ? { [TKey in keyof T]: Build<TInput, TOutput, T[TKey]> }
                    : T
      : T;

/**
 * Input of a schema after `Recur` has been resolved.
 */
export type RecursiveInput<TWrapped extends AnySchema> = Build<
  InferInput<TWrapped>,
  InferOutput<TWrapped>,
  InferInput<TWrapped>
>;

/**
 * Output of a schema after `Recur` has been resolved.
 */
export type RecursiveOutput<TWrapped extends AnySchema> = Build<
  InferInput<TWrapped>,
  InferOutput<TWrapped>,
  InferOutput<TWrapped>
>;

/**
 * Schema parameter that rejects an unresolved `Recur` placeholder.
 *
 * The placeholder is detected on the input type and the output type. A
 * transformation can remove it from only one of them.
 */
export type RejectUnresolvedRecur<TSchema extends AnySchema> =
  true extends HasRecur<InferInput<TSchema> | InferOutput<TSchema>>
    ? never
    : TSchema;

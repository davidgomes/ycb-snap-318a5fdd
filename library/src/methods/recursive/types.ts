import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferOutput,
  IsAny,
} from '../../types/index.ts';

/**
 * Brand for the unresolved `Recur` placeholder.
 */
export declare const recurBrand: unique symbol;

/**
 * Placeholder type substituted when a schema is wrapped with `recursive`.
 *
 * Recursive positions keep this type until `recursive` or `recursiveAsync`
 * replaces it with that schema's own input or output type.
 */
export interface RecurMark {
  readonly [recurBrand]: 'Recur';
}

/**
 * Schema accepted by recursive helpers.
 */
type AnySchema =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Depth steps for type walks.
 *
 * Index `N` stores `N - 1`, so a walk can count down without recursing forever.
 */
type Prev = [0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15];

/**
 * Values that cannot nest a placeholder.
 */
type Primitive = string | number | boolean | bigint | symbol | null | undefined;

/**
 * Built-in objects that cannot nest a placeholder.
 */
type BuiltIn =
  | Date
  | RegExp
  | Error
  | ((...args: never[]) => unknown)
  | ArrayBuffer
  | ArrayBufferView
  | WeakMap<object, unknown>
  | WeakSet<object>;

/**
 * Whether `TValue` was already visited.
 */
type Includes<
  TSeen extends readonly unknown[],
  TValue,
> = TSeen extends readonly [infer THead, ...infer TRest]
  ? [TValue] extends [THead]
    ? [THead] extends [TValue]
      ? true
      : Includes<TRest extends readonly unknown[] ? TRest : [], TValue>
    : Includes<TRest extends readonly unknown[] ? TRest : [], TValue>
  : false;

/**
 * Whether one constituent of a type contains `Recur`.
 */
type HasRecurOne<
  TValue,
  TSeen extends readonly unknown[],
  TDepth extends number,
> = TDepth extends 0
  ? false
  : IsAny<TValue> extends true
    ? false
    : TValue extends unknown
      ? [TValue] extends [RecurMark]
        ? true
        : [TValue] extends [Primitive]
          ? false
          : Includes<TSeen, TValue> extends true
            ? false
            : TValue extends Promise<infer TNested>
              ? HasRecur<TNested, [TValue, ...TSeen], Prev[TDepth]>
              : TValue extends Map<infer TKey, infer TNested>
                ? HasRecur<TKey | TNested, [TValue, ...TSeen], Prev[TDepth]>
                : TValue extends ReadonlyMap<infer TKey, infer TNested>
                  ? HasRecur<TKey | TNested, [TValue, ...TSeen], Prev[TDepth]>
                  : TValue extends Set<infer TNested>
                    ? HasRecur<TNested, [TValue, ...TSeen], Prev[TDepth]>
                    : TValue extends ReadonlySet<infer TNested>
                      ? HasRecur<TNested, [TValue, ...TSeen], Prev[TDepth]>
                      : [TValue] extends [BuiltIn]
                        ? false
                        : TValue extends readonly (infer TItem)[]
                          ? HasRecur<TItem, [TValue, ...TSeen], Prev[TDepth]>
                          : TValue extends object
                            ? HasRecur<
                                TValue[keyof TValue],
                                [TValue, ...TSeen],
                                Prev[TDepth]
                              >
                            : false
      : false;

/**
 * Whether a type contains the unresolved `Recur` placeholder.
 *
 * Cycles are treated as resolved. A transform can leave the placeholder on
 * only one side of a schema, so callers check input and output separately.
 */
type HasRecur<
  TValue,
  TSeen extends readonly unknown[] = [],
  TDepth extends number = 15,
> = true extends HasRecurOne<TValue, TSeen, TDepth> ? true : false;

/**
 * Substitutes `Recur` inside a type that is known to contain it.
 */
type ReplaceRecur<TValue, TSelf> =
  IsAny<TValue> extends true
    ? TValue
    : TValue extends unknown
      ? [TValue] extends [RecurMark]
        ? TSelf
        : [TValue] extends [Primitive]
          ? TValue
          : TValue extends Promise<infer TNested>
            ? Promise<ReplaceRecur<TNested, TSelf>>
            : TValue extends Map<infer TKey, infer TNested>
              ? Map<ReplaceRecur<TKey, TSelf>, ReplaceRecur<TNested, TSelf>>
              : TValue extends ReadonlyMap<infer TKey, infer TNested>
                ? ReadonlyMap<
                    ReplaceRecur<TKey, TSelf>,
                    ReplaceRecur<TNested, TSelf>
                  >
                : TValue extends Set<infer TNested>
                  ? Set<ReplaceRecur<TNested, TSelf>>
                  : TValue extends ReadonlySet<infer TNested>
                    ? ReadonlySet<ReplaceRecur<TNested, TSelf>>
                    : [TValue] extends [BuiltIn]
                      ? TValue
                      : TValue extends unknown[]
                        ? ReplaceRecur<TValue[number], TSelf>[]
                        : TValue extends readonly (infer TItem)[]
                          ? readonly ReplaceRecur<TItem, TSelf>[]
                          : TValue extends object
                            ? {
                                [TKey in keyof TValue]: ReplaceRecur<
                                  TValue[TKey],
                                  TSelf
                                >;
                              }
                            : TValue
      : never;

/**
 * Substitutes `Recur` with `TSelf`.
 *
 * Types that do not contain the placeholder are returned unchanged so
 * branded primitives, transformed values, and nested resolutions stay intact.
 */
type ResolveRecur<TValue, TSelf> =
  HasRecur<TValue> extends true ? ReplaceRecur<TValue, TSelf> : TValue;

/**
 * Resolved shape of `TValue`.
 *
 * `TRoot` stays the original type, including unions, so every `Recur` points
 * at the whole schema type. The reference sits in a property, element, or
 * collection slot, which keeps the type self-referential instead of unrolling
 * it or collapsing it to `unknown`.
 */
type RecursiveType<TValue, TRoot = TValue> =
  HasRecur<TValue> extends false
    ? TValue
    : IsAny<TValue> extends true
      ? TValue
      : TValue extends unknown
        ? [TValue] extends [Primitive]
          ? TValue
          : TValue extends Promise<infer TNested>
            ? Promise<ResolveRecur<TNested, RecursiveType<TRoot>>>
            : TValue extends Map<infer TKey, infer TNested>
              ? Map<
                  ResolveRecur<TKey, RecursiveType<TRoot>>,
                  ResolveRecur<TNested, RecursiveType<TRoot>>
                >
              : TValue extends ReadonlyMap<infer TKey, infer TNested>
                ? ReadonlyMap<
                    ResolveRecur<TKey, RecursiveType<TRoot>>,
                    ResolveRecur<TNested, RecursiveType<TRoot>>
                  >
                : TValue extends Set<infer TNested>
                  ? Set<ResolveRecur<TNested, RecursiveType<TRoot>>>
                  : TValue extends ReadonlySet<infer TNested>
                    ? ReadonlySet<ResolveRecur<TNested, RecursiveType<TRoot>>>
                    : [TValue] extends [BuiltIn]
                      ? TValue
                      : TValue extends unknown[]
                        ? ResolveRecur<TValue[number], RecursiveType<TRoot>>[]
                        : TValue extends readonly (infer TItem)[]
                          ? readonly ResolveRecur<TItem, RecursiveType<TRoot>>[]
                          : TValue extends object
                            ? {
                                [TKey in keyof TValue]: ResolveRecur<
                                  TValue[TKey],
                                  RecursiveType<TRoot>
                                >;
                              }
                            : TValue
        : never;

/**
 * Input type of a schema after resolving `Recur` to that same input type.
 */
export type RecursiveInput<TSchema extends AnySchema> = RecursiveType<
  InferInput<TSchema>
>;

/**
 * Output type of a schema after resolving `Recur` to that same output type.
 */
export type RecursiveOutput<TSchema extends AnySchema> = RecursiveType<
  InferOutput<TSchema>
>;

/**
 * Whether a schema still exposes the unresolved `Recur` placeholder.
 *
 * The placeholder is detected on the input type and the output type
 * separately. A transform can remove it from only one of them.
 */
export type IsUnresolvedRecur<TSchema extends AnySchema> = true extends
  | HasRecur<InferInput<TSchema>>
  | HasRecur<InferOutput<TSchema>>
  ? true
  : false;

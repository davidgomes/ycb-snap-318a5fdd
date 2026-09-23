import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferOutput,
  IsAny,
} from '../../types/index.ts';

/**
 * Brand symbol for the `Recur` placeholder.
 */
declare const recurBrand: unique symbol;

/**
 * Placeholder input and output of `Recur`.
 *
 * Positions that contain this type are replaced with the enclosing recursive
 * schema's own input or output type.
 */
export interface RecurMark {
  readonly [recurBrand]: 'recur';
}

/**
 * Whether a type is a plain object that can contain nested placeholders.
 */
type IsPlainObject<T> = T extends object
  ? T extends
      | readonly unknown[]
      | ((...args: never[]) => unknown)
      | Date
      | RegExp
      | Error
      | Promise<unknown>
      | Map<unknown, unknown>
      | ReadonlyMap<unknown, unknown>
      | Set<unknown>
      | ReadonlySet<unknown>
      | ArrayBuffer
      | Blob
    ? false
    : true
  : false;

/**
 * Depth counter for placeholder detection.
 *
 * Resolved recursive types refer to themselves. The walk stops after a fixed
 * number of steps so those types are not expanded forever.
 */
type Depth = readonly unknown[];

/**
 * Whether a type contains the `Recur` placeholder.
 */
type HasRecur<T, TDepth extends Depth = []> = TDepth['length'] extends 24
  ? false
  : IsAny<T> extends true
    ? false
    : true extends HasRecurPart<T, TDepth>
      ? true
      : false;

/**
 * Whether one constituent of a type contains the `Recur` placeholder.
 */
type HasRecurPart<T, TDepth extends Depth> = T extends unknown
  ? [T] extends [RecurMark]
    ? true
    : T extends readonly (infer TItem)[]
      ? HasRecur<TItem, [...TDepth, 0]>
      : T extends ReadonlyMap<infer TKey, infer TValue>
        ? HasRecur<TKey | TValue, [...TDepth, 0]>
        : T extends ReadonlySet<infer TValue>
          ? HasRecur<TValue, [...TDepth, 0]>
          : IsPlainObject<T> extends true
            ? HasRecur<T[keyof T], [...TDepth, 0]>
            : false
  : false;

/**
 * Array or tuple with `Recur` placeholders replaced.
 */
type FixArray<T extends readonly unknown[], TSelf> = number extends T['length']
  ? FixProp<T[number], TSelf>[]
  : FixTuple<T, TSelf>;

/**
 * Tuple with `Recur` placeholders replaced.
 */
type FixTuple<T extends readonly unknown[], TSelf> = T extends readonly []
  ? []
  : T extends readonly [infer THead, ...infer TRest]
    ? [
        FixProp<THead, TSelf>,
        ...FixTuple<TRest extends readonly unknown[] ? TRest : [], TSelf>,
      ]
    : [];

/**
 * Replaces `Recur` placeholders in a nested type.
 */
type FixProp<T, TSelf> = T extends unknown
  ? [T] extends [RecurMark]
    ? TSelf
    : T extends readonly unknown[]
      ? FixArray<T & readonly unknown[], TSelf>
      : T extends ReadonlyMap<infer TKey, infer TValue>
        ? Map<FixProp<TKey, TSelf>, FixProp<TValue, TSelf>>
        : T extends ReadonlySet<infer TValue>
          ? Set<FixProp<TValue, TSelf>>
          : IsPlainObject<T> extends true
            ? { [TKey in keyof T]: FixProp<T[TKey], TSelf> }
            : T
  : never;

/**
 * Replaces `Recur` placeholders with a self reference to the root type.
 *
 * The self reference is passed through `Fix` so it stays in property
 * position. A direct alias such as `Fix<T> = Expand<T, Fix<T>>` is rejected
 * when the placeholder is the whole type, and it collapses nested positions.
 */
type Fix<T> = T extends readonly unknown[]
  ? FixArray<T, Fix<T>>
  : T extends ReadonlyMap<infer TKey, infer TValue>
    ? Map<FixProp<TKey, Fix<T>>, FixProp<TValue, Fix<T>>>
    : T extends ReadonlySet<infer TValue>
      ? Set<FixProp<TValue, Fix<T>>>
      : IsPlainObject<T> extends true
        ? { [TKey in keyof T]: FixProp<T[TKey], Fix<T>> }
        : T;

/**
 * Resolves `Recur` placeholders to a self-referencing type.
 *
 * Types that do not contain the placeholder are returned unchanged so
 * transformed inputs and outputs keep their original inference.
 */
export type ResolveRecur<T> = HasRecur<T> extends true ? Fix<T> : T;

/**
 * Whether a schema's input or output still contains `Recur`.
 *
 * Input and output are checked separately. A pipe can remove the placeholder
 * from only one of them, and combining the two types first drops keys that
 * are not shared.
 */
export type HasUnresolvedRecur<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> =
  HasRecur<InferInput<TSchema>> extends true
    ? true
    : HasRecur<InferOutput<TSchema>> extends true
      ? true
      : false;

/**
 * Schema argument that still contains an unwrapped `Recur` placeholder.
 */
export interface UnresolvedRecurSchema {
  /**
   * The unresolved placeholder marker.
   *
   * Wrap the schema with `recursive` or `recursiveAsync` before parsing.
   */
  readonly '~recursive': 'unresolved';
}

/**
 * Rejects schemas that still contain an unwrapped `Recur` placeholder.
 */
export type EnsureRecurResolved<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = HasUnresolvedRecur<TSchema> extends true ? UnresolvedRecurSchema : TSchema;

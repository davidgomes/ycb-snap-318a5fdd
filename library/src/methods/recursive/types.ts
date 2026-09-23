import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferOutput,
  IsAny,
} from '../../types/index.ts';
import type { RecurInput, RecurOutput } from './recur.ts';

/**
 * Schema accepted by recursive resolution.
 */
type SchemaLike =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Carrier for a recursive schema's own input and output.
 */
interface RecurSelf {
  /**
   * The recursive input type.
   */
  readonly i: unknown;
  /**
   * The recursive output type.
   */
  readonly o: unknown;
}

/**
 * Depth budget for walking inferred types.
 *
 * Resolved recursive types point at themselves. The budget stops that walk
 * without treating a self reference as an unresolved `Recur` placeholder.
 */
type MaxDepth = 24;

/**
 * Whether a type contains an unresolved `Recur` placeholder.
 *
 * Hint: `any` and `unknown` hide every member of a union. Call this on the
 * input and output types separately so one wide side cannot hide the other.
 */
export type ContainsRecur<T> =
  IsAny<T> extends true ? false : true extends ScanRecur<T> ? true : false;

/**
 * Scans a type for `Recur` input or output placeholders.
 *
 * @internal
 */
export type ScanRecur<
  T,
  TDepth extends readonly unknown[] = [],
> = TDepth['length'] extends MaxDepth
  ? false
  : IsAny<T> extends true
    ? false
    : T extends RecurInput | RecurOutput
      ? true
      : T extends readonly (infer TItem)[]
        ? ScanRecur<TItem, [...TDepth, 1]>
        : T extends Map<infer TKey, infer TValue>
          ? ScanRecur<TKey, [...TDepth, 1]> | ScanRecur<TValue, [...TDepth, 1]>
          : T extends ReadonlyMap<infer TKey, infer TValue>
            ?
                | ScanRecur<TKey, [...TDepth, 1]>
                | ScanRecur<TValue, [...TDepth, 1]>
            : T extends Set<infer TValue>
              ? ScanRecur<TValue, [...TDepth, 1]>
              : T extends ReadonlySet<infer TValue>
                ? ScanRecur<TValue, [...TDepth, 1]>
                : T extends object
                  ? ScanRecur<T[keyof T], [...TDepth, 1]>
                  : false;

/**
 * Replaces `Recur` placeholders with a recursive schema's own types.
 *
 * The self type is passed whole. Its input and output are read only from
 * conditional branches so the resulting type stays self-referential.
 */
type BuildRecur<T, TSelf extends RecurSelf> =
  ContainsRecur<T> extends true
    ? T extends unknown
      ? BuildRecurLeaf<T, TSelf>
      : never
    : T;

/**
 * Replaces one non-union layer of a type.
 */
type BuildRecurLeaf<T, TSelf extends RecurSelf> =
  ContainsRecur<T> extends true
    ? T extends RecurInput
      ? TSelf['i']
      : T extends RecurOutput
        ? TSelf['o']
        : T extends readonly [unknown, ...unknown[]]
          ? { [TKey in keyof T]: BuildRecur<T[TKey], TSelf> }
          : T extends readonly (infer TItem)[]
            ? T extends unknown[]
              ? BuildRecur<TItem, TSelf>[]
              : readonly BuildRecur<TItem, TSelf>[]
            : T extends Map<infer TKey, infer TValue>
              ? Map<BuildRecur<TKey, TSelf>, BuildRecur<TValue, TSelf>>
              : T extends ReadonlyMap<infer TKey, infer TValue>
                ? ReadonlyMap<
                    BuildRecur<TKey, TSelf>,
                    BuildRecur<TValue, TSelf>
                  >
                : T extends Set<infer TValue>
                  ? Set<BuildRecur<TValue, TSelf>>
                  : T extends ReadonlySet<infer TValue>
                    ? ReadonlySet<BuildRecur<TValue, TSelf>>
                    : T extends object
                      ? { [TKey in keyof T]: BuildRecur<T[TKey], TSelf> }
                      : T
    : T;

/**
 * Lazy input and output of a schema after `Recur` is resolved.
 *
 * Hint: The properties refer to `RecurBox` itself. TypeScript defers those
 * lookups, which keeps recursive positions self-referential.
 */
export interface RecurBox<TSchema extends SchemaLike> {
  /**
   * The resolved input type.
   */
  readonly i: BuildRecur<InferInput<TSchema>, RecurBox<TSchema>>;
  /**
   * The resolved output type.
   */
  readonly o: BuildRecur<InferOutput<TSchema>, RecurBox<TSchema>>;
}

/**
 * Resolved input type of a recursive schema.
 */
export type InferRecursiveInput<TSchema extends SchemaLike> =
  RecurBox<TSchema>['i'];

/**
 * Resolved output type of a recursive schema.
 */
export type InferRecursiveOutput<TSchema extends SchemaLike> =
  RecurBox<TSchema>['o'];

/**
 * Error type for a schema that still contains `Recur`.
 */
export interface UnresolvedRecurSchema {
  /**
   * The rejection kind.
   */
  readonly kind: 'unresolved_recur';
  /**
   * The rejection message.
   */
  readonly message: 'Unresolved Recur placeholder. Wrap the schema with recursive() or recursiveAsync() before parsing.';
}

/**
 * Rejects schemas whose input or output still contains `Recur`.
 *
 * @internal
 */
export type RejectUnresolvedRecur<TSchema extends SchemaLike> =
  ContainsRecur<InferInput<TSchema>> extends true
    ? UnresolvedRecurSchema
    : ContainsRecur<InferOutput<TSchema>> extends true
      ? UnresolvedRecurSchema
      : unknown;

import type { InferInput, InferOutput } from '../../types/infer.ts';
import type { BaseIssue } from '../../types/issue.ts';
import type { BaseSchema, BaseSchemaAsync } from '../../types/schema.ts';
import type { IsAny } from '../../types/utils.ts';

/**
 * Recur brand symbol.
 */
export declare const RecurSymbol: unique symbol;

/**
 * Recur placeholder type.
 */
export interface RecurPlaceholder {
  readonly [RecurSymbol]: 'recur';
}

/**
 * Generic schema type used by recur helpers.
 */
type RecurGenericSchema =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Primitive values that must not be walked while resolving recur types.
 */
type RecurPrimitive =
  | string
  | number
  | boolean
  | bigint
  | symbol
  | null
  | undefined;

/**
 * Resolves `Recur` placeholders in a type to a self-referencing type.
 */
export type ResolveRecur<TValue, TSelf> =
  IsAny<TValue> extends true
    ? TValue
    : TValue extends RecurPlaceholder
      ? TSelf
      : [TValue] extends [RecurPrimitive]
        ? TValue
        : TValue extends Map<infer TKey, infer TValue_>
          ? Map<ResolveRecur<TKey, TSelf>, ResolveRecur<TValue_, TSelf>>
          : TValue extends Set<infer TItem>
            ? Set<ResolveRecur<TItem, TSelf>>
            : TValue extends readonly unknown[]
              ? {
                  [TIndex in keyof TValue]: ResolveRecur<TValue[TIndex], TSelf>;
                }
              : TValue extends Record<string, unknown>
                ? { [TKey in keyof TValue]: ResolveRecur<TValue[TKey], TSelf> }
                : TValue;

/**
 * Checks whether a type contains an unresolved `Recur` placeholder.
 */
export type HasRecur<
  TValue,
  TSeen = never,
  TDepth extends readonly unknown[] = [],
> =
  IsAny<TValue> extends true
    ? false
    : TDepth['length'] extends 16
      ? false
      : [TValue] extends [TSeen]
        ? false
        : true extends (
              TValue extends RecurPlaceholder
                ? true
                : [TValue] extends [RecurPrimitive]
                  ? false
                  : TValue extends Map<infer TKey, infer TValue_>
                    ?
                        | HasRecur<TKey, TSeen | TValue, [...TDepth, unknown]>
                        | HasRecur<
                            TValue_,
                            TSeen | TValue,
                            [...TDepth, unknown]
                          >
                    : TValue extends Set<infer TItem>
                      ? HasRecur<TItem, TSeen | TValue, [...TDepth, unknown]>
                      : TValue extends readonly unknown[]
                        ? HasRecur<
                            TValue[number],
                            TSeen | TValue,
                            [...TDepth, unknown]
                          >
                        : TValue extends Record<string, unknown>
                          ? HasRecur<
                              TValue[keyof TValue],
                              TSeen | TValue,
                              [...TDepth, unknown]
                            >
                          : false
            )
          ? true
          : false;

/**
 * Checks whether a schema is a recursive wrapper that already resolved `Recur`.
 */
type IsRecursiveSchema<TSchema> = TSchema extends {
  readonly type: 'recursive';
  readonly wrapped: RecurGenericSchema;
}
  ? true
  : false;

/**
 * Checks whether a schema still contains an unresolved `Recur` placeholder
 * in its input or output type.
 */
export type HasUnresolvedRecur<TSchema extends RecurGenericSchema> =
  IsRecursiveSchema<TSchema> extends true
    ? false
    : true extends
          | HasRecur<InferInput<TSchema>>
          | HasRecur<InferOutput<TSchema>>
      ? true
      : false;

/**
 * Constraint that is `never` when `TSchema` still contains `Recur`.
 */
export type RecurResolved<TSchema extends RecurGenericSchema> =
  HasUnresolvedRecur<TSchema> extends true ? never : unknown;

/**
 * Deferred self-reference used to resolve recursive input and output types.
 */
export interface RecurSelf<TWrapped extends RecurGenericSchema> {
  /**
   * The resolved recursive input type.
   */
  readonly input: ResolveRecur<InferInput<TWrapped>, this['input']>;
  /**
   * The resolved recursive output type.
   */
  readonly output: ResolveRecur<InferOutput<TWrapped>, this['output']>;
}

/**
 * Resolved recursive input type.
 */
export type RecurSelfInput<TWrapped extends RecurGenericSchema> =
  // @ts-expect-error TS2589 -- deferred until TWrapped is a concrete schema
  RecurSelf<TWrapped>['input'];

/**
 * Resolved recursive output type.
 */
export type RecurSelfOutput<TWrapped extends RecurGenericSchema> =
  // @ts-expect-error TS2589 -- deferred until TWrapped is a concrete schema
  RecurSelf<TWrapped>['output'];
